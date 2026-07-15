"""Production-emulation soak test for the governed AI inference fleet platform.

Exercises the full decision pipeline (deepfield -> GCL -> fleet -> ledger) on
Oberon with variable duration profiles. Measures end-to-end latency, memory
growth, chain integrity, and degradation recovery.

Usage:
    python3 test/soak/ecosystem_soak.py \
        --gcl-url https://gcl-app.192.168.1.123.sslip.io \
        --fleet-url https://fleet-controller-fleet-llm-d.apps.ocpv-infra01.dal12.infra.demo.redhat.com \
        --profile quick
"""

from __future__ import annotations

import argparse
import asyncio
import json
import statistics
import sys
import time
import uuid
from dataclasses import dataclass, field
from typing import Optional

import httpx

PROFILES = {
    "quick":    {"duration": 1800,  "event_interval": 5, "inject_interval": 600,  "inject_count": 2},
    "standard": {"duration": 7200,  "event_interval": 3, "inject_interval": 900,  "inject_count": 8},
    "overnight":{"duration": 28800, "event_interval": 5, "inject_interval": 1800, "inject_count": 16},
}

SCENARIOS = [
    "inference_fleet_spike", "compliance_breach", "capacity_exhaustion",
    "slo_cascade", "mixed_storm", "multi_cluster_migration",
]


@dataclass
class Snapshot:
    timestamp: float
    gcl_cycle_latency_ms: float = 0
    fleet_healthz_latency_ms: float = 0
    gcl_cycle_count: int = 0
    fleet_cluster_count: int = 0
    pipeline_success: int = 0
    pipeline_error: int = 0
    gcl_healthy: bool = True
    fleet_healthy: bool = True


@dataclass
class SoakResult:
    profile: str
    duration_s: float = 0
    total_events: int = 0
    pipeline_successes: int = 0
    pipeline_errors: int = 0
    gcl_latencies: list[float] = field(default_factory=list)
    fleet_latencies: list[float] = field(default_factory=list)
    e2e_latencies: list[float] = field(default_factory=list)
    snapshots: list[Snapshot] = field(default_factory=list)
    injections: list[dict] = field(default_factory=list)
    chain_verifications: list[dict] = field(default_factory=list)
    gcl_memory_series: list[float] = field(default_factory=list)
    fleet_memory_series: list[float] = field(default_factory=list)
    scenario_actions: dict[str, list[str]] = field(default_factory=dict)
    slo_violations: list[str] = field(default_factory=list)


class EcosystemSoak:
    def __init__(self, gcl_url: str, fleet_url: str, ledger_url: str = "",
                 deepfield_token: str = "", timeout: float = 30.0):
        self.gcl = gcl_url.rstrip("/")
        self.fleet = fleet_url.rstrip("/")
        self.ledger = ledger_url.rstrip("/") if ledger_url else ""
        self.deepfield_token = deepfield_token
        self.timeout = timeout
        self._stop = False
        self._event_counter = 0

    async def _get(self, url: str) -> httpx.Response:
        async with httpx.AsyncClient(verify=False, timeout=self.timeout) as c:
            return await c.get(url)

    async def _post(self, url: str, data: dict | str,
                    headers: dict | None = None) -> httpx.Response:
        async with httpx.AsyncClient(verify=False, timeout=self.timeout) as c:
            if isinstance(data, str):
                return await c.post(url, content=data, headers=headers or {})
            return await c.post(url, json=data, headers=headers)

    async def _timed_get(self, url: str) -> tuple[httpx.Response, float]:
        start = time.monotonic()
        resp = await self._get(url)
        return resp, (time.monotonic() - start) * 1000

    async def _timed_post(self, url: str, data: dict | str,
                          headers: dict | None = None) -> tuple[httpx.Response, float]:
        start = time.monotonic()
        resp = await self._post(url, data, headers)
        return resp, (time.monotonic() - start) * 1000

    # ── A. Decision Pipeline ──

    def _build_deepfield_cloudevent(self, signal: dict) -> dict:
        self._event_counter += 1
        eid = f"soak-obs-{self._event_counter}"
        now = time.strftime("%Y-%m-%dT%H:%M:%S+00:00", time.gmtime())
        expires = time.strftime(
            "%Y-%m-%dT%H:%M:%S+00:00",
            time.gmtime(time.time() + 300))
        sha = f"{self._event_counter:064x}"
        return {
            "specversion": "1.0",
            "type": "io.srex.deepfield.observation.v1",
            "source": "urn:srex:deepfield-fleet",
            "id": eid,
            "subject": "fleet/edge-east/granite-fleet",
            "time": now,
            "datacontenttype": "application/json",
            "dataschema": "urn:srex:deepfield:schema:observation:v1",
            "correlationid": f"corr-{eid}",
            "causationid": f"cause-{eid}",
            "idempotencykey": f"idem-{eid}",
            "tenant": "default",
            "zone": "us-east",
            "traceparent": "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01",
            "expiresat": expires,
            "data": {
                "observation_id": eid,
                "observed_at": now,
                "resource": {
                    "cluster": "edge-east",
                    "kind": "InferencePool",
                    "name": "granite-fleet",
                },
                "signal_type": signal.get("metric", "latency_ms"),
                "severity": "high" if signal.get("value", 0) > 3000 else "medium",
                "value": signal.get("value", 0),
                "unit": signal.get("metric", "unknown"),
                "evidence": [{"uri": f"urn:srex:deepfield:obs:{eid}", "sha256": sha}],
            },
        }

    async def run_governance_cycle(self, scenario: str, step: int
                                   ) -> tuple[bool, float, str]:
        e2e_start = time.monotonic()
        try:
            await self._post(f"{self.gcl}/api/v1/scenario/seed",
                             {"scenario": scenario, "seed": int(time.monotonic() * 1000) % 100000})
            resp = await self._get(f"{self.gcl}/api/v1/scenario/step/{step % 8}")
            data = resp.json()
            signals = data.get("signals", data.get("step", {}).get("signals", []))

            if self.deepfield_token and signals:
                event = self._build_deepfield_cloudevent(signals[0])
                headers = {
                    "Content-Type": "application/cloudevents+json",
                    "Authorization": f"Bearer {self.deepfield_token}",
                }
                resp, gcl_ms = await self._timed_post(
                    f"{self.gcl}/api/v1/events/deepfield",
                    json.dumps(event), headers)
            else:
                resp, gcl_ms = await self._timed_post(
                    f"{self.gcl}/api/v1/cycle", {"signals": signals})

            d = resp.json()
            action = d.get("action_type", "none")

            e2e_ms = (time.monotonic() - e2e_start) * 1000
            return True, e2e_ms, action
        except Exception as e:
            e2e_ms = (time.monotonic() - e2e_start) * 1000
            return False, e2e_ms, str(e)

    # ── B. Health Probes ──

    async def probe_health(self) -> dict:
        result = {}
        for name, url in [("gcl", self.gcl), ("fleet", self.fleet)]:
            try:
                resp, ms = await self._timed_get(f"{url}/healthz")
                result[name] = {"healthy": resp.status_code == 200, "latency_ms": ms}
            except Exception:
                result[name] = {"healthy": False, "latency_ms": 0}
        return result

    # ── C. Memory Tracking ──

    async def get_fleet_memory(self) -> float:
        try:
            resp = await self._get(f"{self.fleet}/debug/vars")
            data = resp.json()
            memstats = data.get("memstats", {})
            return memstats.get("Alloc", 0) / (1024 * 1024)
        except Exception:
            return 0

    async def get_gcl_cycle_count(self) -> int:
        try:
            resp = await self._get(f"{self.gcl}/api/v1/cycles")
            return len(resp.json())
        except Exception:
            return 0

    # ── D. Chain Integrity ──

    async def verify_chains(self) -> dict:
        result = {"timestamp": time.time(), "verified": False, "detail": ""}
        if not self.ledger:
            result["detail"] = "no ledger configured"
            result["verified"] = True
            return result
        try:
            resp = await self._get(f"{self.ledger}/api/verify")
            d = resp.json()
            result["verified"] = d.get("valid", resp.status_code == 200)
            result["detail"] = json.dumps(d)[:200]
        except Exception as e:
            result["detail"] = str(e)
        return result

    # ── E. Degradation Injection ──

    async def inject_degradation(self, injection_type: str) -> dict:
        result = {"type": injection_type, "timestamp": time.time(),
                  "recovery_ms": 0, "detail": ""}
        start = time.monotonic()

        if injection_type == "expired_event":
            try:
                resp = await self._post(f"{self.gcl}/api/v1/cycle", {"signals": []})
                result["detail"] = f"empty signals: status={resp.status_code}"
            except Exception as e:
                result["detail"] = str(e)

        elif injection_type == "burst_50":
            signals = [{"metric": "latency_ms", "value": 5000.0, "source": "soak"},
                       {"metric": "replicas", "value": 3.0, "source": "soak"},
                       {"metric": "max_replicas", "value": 10.0, "source": "soak"}]
            errors = 0
            tasks = []
            for _ in range(50):
                tasks.append(self._post(f"{self.gcl}/api/v1/cycle", {"signals": signals}))
            results = await asyncio.gather(*tasks, return_exceptions=True)
            errors = sum(1 for r in results if isinstance(r, Exception)
                         or (hasattr(r, 'status_code') and r.status_code >= 500))
            result["detail"] = f"burst 50: {errors} errors"

        elif injection_type == "fleet_invalid_intent":
            try:
                resp = await self._post(
                    f"{self.fleet}/api/v2/intents",
                    '{"invalid": true}',
                    {"Content-Type": "application/json"})
                result["detail"] = f"invalid intent: status={resp.status_code}"
            except Exception as e:
                result["detail"] = str(e)

        elif injection_type == "gcl_reset":
            try:
                await self._post(f"{self.gcl}/api/v1/reset", {})
                await asyncio.sleep(1)
                resp, ms = await self._timed_get(f"{self.gcl}/healthz")
                result["detail"] = f"reset+recovery: healthz={resp.status_code} in {ms:.0f}ms"
            except Exception as e:
                result["detail"] = str(e)

        result["recovery_ms"] = (time.monotonic() - start) * 1000
        return result

    # ── Main Soak Loop ──

    async def run(self, profile_name: str):
        profile = PROFILES[profile_name]
        duration = profile["duration"]
        event_interval = profile["event_interval"]
        inject_interval = profile["inject_interval"]

        result = SoakResult(profile=profile_name)
        start = time.monotonic()
        last_snapshot = start
        last_inject = start
        last_verify = start
        scenario_idx = 0
        step_idx = 0
        snapshot_count = 0
        inject_count = 0
        injection_types = ["expired_event", "burst_50", "fleet_invalid_intent", "gcl_reset"]

        print(f"\n{'='*70}")
        print(f"ECOSYSTEM PRODUCTION-EMULATION SOAK TEST")
        print(f"{'='*70}")
        print(f"Profile: {profile_name} ({duration/60:.0f} minutes)")
        print(f"Event rate: 1 every {event_interval}s")
        print(f"Degradation injections: every {inject_interval/60:.0f} min")
        print(f"GCL: {self.gcl}")
        print(f"Fleet: {self.fleet}")
        print(f"{'='*70}\n")

        # Initial health check
        health = await self.probe_health()
        for name, h in health.items():
            status = "UP" if h["healthy"] else "DOWN"
            print(f"  {name}: {status} ({h['latency_ms']:.0f}ms)")

        initial_fleet_mem = await self.get_fleet_memory()
        initial_gcl_cycles = await self.get_gcl_cycle_count()
        print(f"  Fleet memory: {initial_fleet_mem:.1f}MB")
        print(f"  GCL cycles: {initial_gcl_cycles}")
        print()

        # Header for live progress
        print(f"  {'Time':>6}  {'Events':>6}  {'OK':>4}  {'Err':>4}  "
              f"{'GCL p50':>8}  {'Fleet':>6}  {'Scenario':<25}")
        print(f"  {'-'*6}  {'-'*6}  {'-'*4}  {'-'*4}  "
              f"{'-'*8}  {'-'*6}  {'-'*25}")

        while time.monotonic() - start < duration and not self._stop:
            now = time.monotonic()
            elapsed = now - start

            # Run governance cycle
            scenario = SCENARIOS[scenario_idx % len(SCENARIOS)]
            ok, e2e_ms, action = await self.run_governance_cycle(scenario, step_idx)

            result.total_events += 1
            if ok:
                result.pipeline_successes += 1
                result.e2e_latencies.append(e2e_ms)
                result.scenario_actions.setdefault(scenario, []).append(action)
            else:
                result.pipeline_errors += 1

            step_idx += 1
            if step_idx >= 8:
                step_idx = 0
                scenario_idx += 1

            # Snapshot every 30s
            if now - last_snapshot >= 30:
                last_snapshot = now
                snapshot_count += 1

                health = await self.probe_health()
                fleet_mem = await self.get_fleet_memory()
                gcl_cycles = await self.get_gcl_cycle_count()

                result.fleet_memory_series.append(fleet_mem)

                snap = Snapshot(
                    timestamp=time.time(),
                    gcl_cycle_latency_ms=e2e_ms if ok else 0,
                    fleet_healthz_latency_ms=health.get("fleet", {}).get("latency_ms", 0),
                    gcl_cycle_count=gcl_cycles,
                    fleet_cluster_count=0,
                    pipeline_success=result.pipeline_successes,
                    pipeline_error=result.pipeline_errors,
                    gcl_healthy=health.get("gcl", {}).get("healthy", False),
                    fleet_healthy=health.get("fleet", {}).get("healthy", False),
                )
                result.snapshots.append(snap)

                # Compute running p50
                recent = result.e2e_latencies[-60:] if result.e2e_latencies else [0]
                p50 = sorted(recent)[len(recent) // 2]

                minutes = int(elapsed / 60)
                seconds = int(elapsed % 60)
                print(f"  {minutes:3d}:{seconds:02d}  {result.total_events:6d}  "
                      f"{result.pipeline_successes:4d}  {result.pipeline_errors:4d}  "
                      f"{p50:7.0f}ms  "
                      f"{'UP' if snap.fleet_healthy else 'DOWN':>6}  "
                      f"{scenario:<25}")

            # Degradation injection
            if (now - last_inject >= inject_interval
                    and inject_count < profile["inject_count"]):
                last_inject = now
                inject_count += 1
                inj_type = injection_types[inject_count % len(injection_types)]
                print(f"\n  >>> INJECTION #{inject_count}: {inj_type}")
                inj_result = await self.inject_degradation(inj_type)
                result.injections.append(inj_result)
                print(f"  <<< {inj_result['detail']} ({inj_result['recovery_ms']:.0f}ms)\n")

            # Chain verification every 5 minutes
            if now - last_verify >= 300:
                last_verify = now
                verification = await self.verify_chains()
                result.chain_verifications.append(verification)
                if not verification["verified"]:
                    print(f"  !!! CHAIN INTEGRITY FAILURE: {verification['detail']}")

            await asyncio.sleep(event_interval)

        result.duration_s = time.monotonic() - start

        # Final measurements
        final_fleet_mem = await self.get_fleet_memory()
        final_gcl_cycles = await self.get_gcl_cycle_count()

        self._print_results(result, initial_fleet_mem, final_fleet_mem,
                            initial_gcl_cycles, final_gcl_cycles)
        self._check_slos(result, initial_fleet_mem, final_fleet_mem)
        return result

    def _print_results(self, result: SoakResult,
                       initial_mem: float, final_mem: float,
                       initial_cycles: int, final_cycles: int):
        print(f"\n{'='*70}")
        print(f"SOAK TEST RESULTS ({result.profile})")
        print(f"{'='*70}")

        print(f"\n  Duration: {result.duration_s/60:.1f} minutes")
        print(f"  Total events: {result.total_events}")
        print(f"  Pipeline successes: {result.pipeline_successes}")
        print(f"  Pipeline errors: {result.pipeline_errors}")

        if result.total_events > 0:
            success_rate = result.pipeline_successes / result.total_events * 100
            print(f"  Success rate: {success_rate:.1f}%")

        if result.e2e_latencies:
            lats = sorted(result.e2e_latencies)
            p50 = lats[len(lats) // 2]
            p95 = lats[int(len(lats) * 0.95)]
            p99 = lats[int(len(lats) * 0.99)]
            print(f"\n  End-to-end latency:")
            print(f"    p50: {p50:.0f}ms")
            print(f"    p95: {p95:.0f}ms")
            print(f"    p99: {p99:.0f}ms")
            print(f"    min: {min(lats):.0f}ms")
            print(f"    max: {max(lats):.0f}ms")

        print(f"\n  Memory:")
        print(f"    Fleet initial: {initial_mem:.1f}MB")
        print(f"    Fleet final: {final_mem:.1f}MB")
        print(f"    Fleet growth: {final_mem - initial_mem:.1f}MB")
        if initial_mem > 0:
            print(f"    Growth factor: {final_mem / initial_mem:.2f}x")

        print(f"\n  State growth:")
        print(f"    GCL cycles: {initial_cycles} -> {final_cycles} "
              f"(+{final_cycles - initial_cycles})")

        if result.scenario_actions:
            print(f"\n  Scenario action distribution:")
            for scenario, actions in result.scenario_actions.items():
                action_counts = {}
                for a in actions:
                    action_counts[a] = action_counts.get(a, 0) + 1
                print(f"    {scenario}: {dict(action_counts)}")

        if result.injections:
            print(f"\n  Degradation injections ({len(result.injections)}):")
            for inj in result.injections:
                print(f"    [{inj['type']}] {inj['detail']} "
                      f"({inj['recovery_ms']:.0f}ms)")

        if result.chain_verifications:
            verified_count = sum(1 for v in result.chain_verifications if v["verified"])
            total = len(result.chain_verifications)
            print(f"\n  Chain integrity: {verified_count}/{total} verifications passed")

        # Latency stability
        if result.fleet_memory_series and len(result.fleet_memory_series) > 2:
            first_third = result.fleet_memory_series[:len(result.fleet_memory_series)//3]
            last_third = result.fleet_memory_series[-len(result.fleet_memory_series)//3:]
            if first_third and last_third:
                first_avg = statistics.mean(first_third)
                last_avg = statistics.mean(last_third)
                if first_avg > 0:
                    print(f"\n  Memory trend:")
                    print(f"    First third avg: {first_avg:.1f}MB")
                    print(f"    Last third avg: {last_avg:.1f}MB")
                    print(f"    Trend: {last_avg/first_avg:.2f}x")

    def _check_slos(self, result: SoakResult,
                    initial_mem: float, final_mem: float):
        print(f"\n  {'='*50}")
        print(f"  SLO GATES")
        print(f"  {'='*50}")

        gates = []

        # Success rate > 95%
        if result.total_events > 0:
            rate = result.pipeline_successes / result.total_events
            ok = rate >= 0.95
            gates.append(("Pipeline success rate > 95%", ok,
                          f"{rate*100:.1f}%"))

        # E2E p95 < 2000ms
        if result.e2e_latencies:
            lats = sorted(result.e2e_latencies)
            p95 = lats[int(len(lats) * 0.95)]
            ok = p95 < 2000
            gates.append(("E2E latency p95 < 2s", ok, f"{p95:.0f}ms"))

        # Memory growth < 2x
        if initial_mem > 0:
            growth = final_mem / initial_mem
            ok = growth < 2.0
            gates.append(("Memory growth < 2x", ok, f"{growth:.2f}x"))

        # Chain integrity 100%
        if result.chain_verifications:
            verified = sum(1 for v in result.chain_verifications if v["verified"])
            total = len(result.chain_verifications)
            ok = verified == total
            gates.append(("Chain integrity 100%", ok,
                          f"{verified}/{total}"))

        # Health availability > 99.5%
        if result.snapshots:
            gcl_up = sum(1 for s in result.snapshots if s.gcl_healthy)
            fleet_up = sum(1 for s in result.snapshots if s.fleet_healthy)
            total = len(result.snapshots)
            gcl_avail = gcl_up / total
            fleet_avail = fleet_up / total
            ok = gcl_avail >= 0.995 and fleet_avail >= 0.995
            gates.append(("Health availability > 99.5%", ok,
                          f"GCL={gcl_avail*100:.1f}% Fleet={fleet_avail*100:.1f}%"))

        # Injection recovery < 60s
        if result.injections:
            max_recovery = max(i["recovery_ms"] for i in result.injections)
            ok = max_recovery < 60000
            gates.append(("Injection recovery < 60s", ok,
                          f"max={max_recovery/1000:.1f}s"))

        passed = 0
        failed = 0
        for name, ok, detail in gates:
            status = "PASS" if ok else "FAIL"
            if ok:
                passed += 1
            else:
                failed += 1
                result.slo_violations.append(f"{name}: {detail}")
            print(f"  [{status}] {name}: {detail}")

        print(f"\n  TOTAL: {passed} passed, {failed} failed")
        if failed == 0:
            print(f"\n  RESULT: ALL SLO GATES PASSED")
        else:
            print(f"\n  RESULT: {failed} SLO VIOLATION(S)")


async def main():
    parser = argparse.ArgumentParser(description="Ecosystem production-emulation soak test")
    parser.add_argument("--gcl-url", default="https://gcl-app.192.168.1.123.sslip.io")
    parser.add_argument("--fleet-url", default="http://localhost:18080")
    parser.add_argument("--ledger-url", default="")
    parser.add_argument("--deepfield-token", default="",
                        help="Bearer token for deepfield CloudEvent submission (enables production path)")
    parser.add_argument("--profile", default="quick",
                        choices=["quick", "standard", "overnight"],
                        help="Soak duration profile")
    parser.add_argument("--timeout", type=float, default=30.0)
    args = parser.parse_args()

    soak = EcosystemSoak(args.gcl_url, args.fleet_url, args.ledger_url,
                         args.deepfield_token, args.timeout)
    await soak.run(args.profile)


if __name__ == "__main__":
    asyncio.run(main())
