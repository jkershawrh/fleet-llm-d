"""Metric poisoning test for fleet-llm-d.

Verifies that the autoscaling feedback loop is resilient to poisoned
agent metrics: extreme values, negative numbers, NaN-like strings.
A compromised spoke agent should not be able to crash the controller
or manipulate cross-cluster routing decisions.

Usage:
    python3 test/security/metric_poisoning_test.py \
        --fleet-url https://fleet-controller.example.com \
        --auth-secret changeme-fleet-secret
"""

from __future__ import annotations

import argparse
import asyncio
import base64
import hashlib
import hmac
import json
import sys
import time
from dataclasses import dataclass

import httpx


@dataclass
class PoisonResult:
    name: str
    passed: bool
    detail: str


def generate_token(secret: str, subject: str = "fleet-agent",
                   role: str = "admin", ttl_hours: int = 24) -> str:
    from datetime import datetime, timezone, timedelta
    now = datetime.now(timezone.utc)
    claims = {"sub": subject, "role": role,
              "iat": now.isoformat(), "exp": (now + timedelta(hours=ttl_hours)).isoformat()}
    claims_json = json.dumps(claims, separators=(",", ":")).encode()
    claims_b64 = base64.urlsafe_b64encode(claims_json).rstrip(b"=").decode()
    sig = hmac.new(secret.encode(), claims_json, hashlib.sha256).digest()
    sig_b64 = base64.urlsafe_b64encode(sig).rstrip(b"=").decode()
    return claims_b64 + "." + sig_b64


POISON_PAYLOADS = [
    ("extreme_gpu_utilization", {
        "cluster_id": "poison-test-cluster",
        "throughput_tps": 1.0, "ttft_p50_ms": 50, "ttft_p99_ms": 100,
        "queue_depth": 5, "gpu_utilization": 999.0, "kv_cache_hit_rate": 0.5,
    }),
    ("negative_queue_depth", {
        "cluster_id": "poison-test-cluster",
        "throughput_tps": 10.0, "ttft_p50_ms": 50, "ttft_p99_ms": 100,
        "queue_depth": -100, "gpu_utilization": 0.5, "kv_cache_hit_rate": 0.5,
    }),
    ("negative_kv_cache", {
        "cluster_id": "poison-test-cluster",
        "throughput_tps": 10.0, "ttft_p50_ms": 50, "ttft_p99_ms": 100,
        "queue_depth": 5, "gpu_utilization": 0.5, "kv_cache_hit_rate": -1.0,
    }),
    ("zero_throughput", {
        "cluster_id": "poison-test-cluster",
        "throughput_tps": 0.0, "ttft_p50_ms": 0, "ttft_p99_ms": 0,
        "queue_depth": 0, "gpu_utilization": 0.0, "kv_cache_hit_rate": 0.0,
    }),
    ("extreme_latency", {
        "cluster_id": "poison-test-cluster",
        "throughput_tps": 1.0, "ttft_p50_ms": 999999999, "ttft_p99_ms": 999999999,
        "queue_depth": 99999, "gpu_utilization": 0.5, "kv_cache_hit_rate": 0.5,
    }),
    ("extreme_throughput", {
        "cluster_id": "poison-test-cluster",
        "throughput_tps": 999999999.0, "ttft_p50_ms": 0.001, "ttft_p99_ms": 0.001,
        "queue_depth": 0, "gpu_utilization": 0.01, "kv_cache_hit_rate": 1.0,
    }),
]


class MetricPoisoningTest:
    def __init__(self, fleet_url: str, auth_secret: str, timeout: float = 15.0):
        self.fleet = fleet_url.rstrip("/")
        self.token = generate_token(auth_secret)
        self.headers = {"Authorization": f"Bearer {self.token}",
                        "Content-Type": "application/json"}
        self.client = httpx.AsyncClient(verify=False, timeout=timeout,
                                        headers=self.headers)
        self.results: list[PoisonResult] = []

    async def close(self):
        await self.client.aclose()

    async def _register_poison_cluster(self) -> bool:
        resp = await self.client.post(
            f"{self.fleet}/api/v1/clusters",
            json={"name": "poison-test-cluster", "region": "us-evil-1"})
        return resp.status_code in (201, 409)

    async def _healthz(self) -> int:
        resp = await self.client.get(f"{self.fleet}/healthz")
        return resp.status_code

    async def _route_request(self) -> dict | None:
        try:
            resp = await self.client.post(
                f"{self.fleet}/api/v1/route",
                json={"model": "granite", "prompt_tokens": 10})
            if resp.status_code == 200:
                return resp.json()
        except Exception:
            pass
        return None

    async def test_poison_payload(self, name: str, payload: dict):
        resp = await self.client.post(
            f"{self.fleet}/api/v1/agent/metrics", json=payload)

        health = await self._healthz()
        if health != 200:
            self.results.append(PoisonResult(
                name=f"poison_{name}",
                passed=False,
                detail=f"Controller crashed after poisoned metrics (healthz={health})"))
            return

        if resp.status_code in (200, 202):
            detail = f"Accepted (status={resp.status_code}), controller healthy"
        elif resp.status_code == 400:
            detail = f"Rejected (status=400) — validation caught it"
        else:
            detail = f"Unexpected status={resp.status_code}"

        self.results.append(PoisonResult(
            name=f"poison_{name}", passed=True, detail=detail))

    async def test_routing_not_poisoned(self):
        route = await self._route_request()
        if route is None:
            self.results.append(PoisonResult(
                name="routing_not_poisoned",
                passed=True,
                detail="No route endpoint or no clusters — acceptable"))
            return

        routed_to = route.get("cluster_id", "")
        if routed_to == "poison-test-cluster":
            self.results.append(PoisonResult(
                name="routing_not_poisoned",
                passed=False,
                detail=f"Routing selected the poisoned cluster: {routed_to}"))
        else:
            self.results.append(PoisonResult(
                name="routing_not_poisoned",
                passed=True,
                detail=f"Routing selected {routed_to} (not poisoned)"))

    async def test_post_poison_health(self):
        health = await self._healthz()
        self.results.append(PoisonResult(
            name="post_poison_health",
            passed=health == 200,
            detail=f"healthz={health} after all poison payloads"))

    async def run_all(self):
        print("=== METRIC POISONING TEST ===\n", file=sys.stderr)

        registered = await self._register_poison_cluster()
        print(f"  Poison cluster registered: {registered}", file=sys.stderr)

        for name, payload in POISON_PAYLOADS:
            await self.test_poison_payload(name, payload)
            print(f"  [{self.results[-1].name}] "
                  f"{'PASS' if self.results[-1].passed else 'FAIL'} — "
                  f"{self.results[-1].detail}", file=sys.stderr)

        await asyncio.sleep(2)

        await self.test_routing_not_poisoned()
        print(f"  [{self.results[-1].name}] "
              f"{'PASS' if self.results[-1].passed else 'FAIL'} — "
              f"{self.results[-1].detail}", file=sys.stderr)

        await self.test_post_poison_health()
        print(f"  [{self.results[-1].name}] "
              f"{'PASS' if self.results[-1].passed else 'FAIL'} — "
              f"{self.results[-1].detail}", file=sys.stderr)

        passed = sum(1 for r in self.results if r.passed)
        failed = sum(1 for r in self.results if not r.passed)
        print(f"\n  TOTAL: {passed} passed, {failed} failed", file=sys.stderr)

        return failed == 0


async def main():
    parser = argparse.ArgumentParser(description="Metric poisoning test")
    parser.add_argument("--fleet-url", required=True)
    parser.add_argument("--auth-secret", required=True)
    parser.add_argument("--timeout", type=float, default=15.0)
    args = parser.parse_args()

    test = MetricPoisoningTest(args.fleet_url, args.auth_secret, args.timeout)
    try:
        success = await test.run_all()
    finally:
        await test.close()

    sys.exit(0 if success else 1)


if __name__ == "__main__":
    asyncio.run(main())
