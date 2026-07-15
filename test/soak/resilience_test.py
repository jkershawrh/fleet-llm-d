"""Multi-node resilience test for the governed AI inference fleet platform.

Tests component restart recovery, resource contention, and system stability
on OpenShift. Designed for SNO (Single Node OpenShift) clusters.

Usage:
    python3 test/soak/resilience_test.py \
        --gcl-url http://gcl-app.governed-cognitive-loop.svc:8000 \
        --fleet-url http://fleet-controller.fleet-llm-d.svc:8080 \
        --deepfield-token oberon-deepfield-token-2026
"""

from __future__ import annotations

import argparse
import asyncio
import json
import os
import sys
import time
from dataclasses import dataclass, field

import httpx


@dataclass
class ResilienceResult:
    test: str
    passed: bool
    detail: str
    recovery_ms: float = 0
    events_during: int = 0
    errors_during: int = 0


class ResilienceTest:
    def __init__(self, gcl_url: str, fleet_url: str, deepfield_token: str = "",
                 timeout: float = 15.0):
        self.gcl = gcl_url.rstrip("/")
        self.fleet = fleet_url.rstrip("/")
        self.deepfield_token = deepfield_token
        self.timeout = timeout
        self.results: list[ResilienceResult] = []
        self._event_counter = 0

    async def _get(self, url: str) -> httpx.Response:
        async with httpx.AsyncClient(verify=False, timeout=self.timeout) as c:
            return await c.get(url)

    async def _post(self, url: str, data) -> httpx.Response:
        async with httpx.AsyncClient(verify=False, timeout=self.timeout) as c:
            if isinstance(data, str):
                return await c.post(url, content=data,
                                    headers={"Content-Type": "application/cloudevents+json",
                                             "Authorization": f"Bearer {self.deepfield_token}"})
            return await c.post(url, json=data)

    def _k8s_token(self) -> str:
        try:
            with open("/var/run/secrets/kubernetes.io/serviceaccount/token") as f:
                return f.read().strip()
        except FileNotFoundError:
            return os.environ.get("KUBE_TOKEN", "")

    def _k8s_api(self) -> str:
        host = os.environ.get("KUBERNETES_SERVICE_HOST", "kubernetes.default.svc")
        port = os.environ.get("KUBERNETES_SERVICE_PORT", "443")
        return f"https://{host}:{port}"

    async def _delete_pods(self, namespace: str, label: str):
        api = self._k8s_api()
        token = self._k8s_token()
        url = f"{api}/api/v1/namespaces/{namespace}/pods?labelSelector={label}"
        async with httpx.AsyncClient(verify=False, timeout=15) as c:
            resp = await c.get(url, headers={"Authorization": f"Bearer {token}"})
            pods = resp.json().get("items", [])
            for pod in pods:
                name = pod["metadata"]["name"]
                del_url = f"{api}/api/v1/namespaces/{namespace}/pods/{name}?gracePeriodSeconds=0"
                await c.delete(del_url, headers={"Authorization": f"Bearer {token}"})

    async def _wait_healthy(self, name: str, url: str, timeout_s: float = 120) -> float:
        start = time.monotonic()
        while time.monotonic() - start < timeout_s:
            try:
                resp = await self._get(f"{url}/healthz")
                if resp.status_code == 200:
                    return (time.monotonic() - start) * 1000
            except Exception:
                pass
            await asyncio.sleep(1)
        return -1

    async def _run_background_load(self, duration_s: float) -> tuple[int, int]:
        ok, err = 0, 0
        start = time.monotonic()
        while time.monotonic() - start < duration_s:
            try:
                self._event_counter += 1
                eid = f"resilience-{self._event_counter}"
                now = time.strftime("%Y-%m-%dT%H:%M:%S+00:00", time.gmtime())
                expires = time.strftime("%Y-%m-%dT%H:%M:%S+00:00",
                                       time.gmtime(time.time() + 300))
                sha = f"{self._event_counter:064x}"

                if self.deepfield_token:
                    event = json.dumps({
                        "specversion": "1.0",
                        "type": "io.srex.deepfield.observation.v1",
                        "source": "urn:srex:deepfield-fleet",
                        "id": eid, "subject": "fleet/edge-east/granite-fleet",
                        "time": now, "datacontenttype": "application/json",
                        "dataschema": "urn:srex:deepfield:schema:observation:v1",
                        "correlationid": f"corr-{eid}", "causationid": f"cause-{eid}",
                        "idempotencykey": f"idem-{eid}", "tenant": "default",
                        "zone": "us-east",
                        "traceparent": "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01",
                        "expiresat": expires,
                        "data": {
                            "observation_id": eid, "observed_at": now,
                            "resource": {"cluster": "edge-east", "kind": "InferencePool",
                                         "name": "granite-fleet"},
                            "signal_type": "latency_ms", "severity": "high",
                            "value": 5000.0, "unit": "ms",
                            "evidence": [{"uri": f"urn:srex:deepfield:obs:{eid}",
                                          "sha256": sha}],
                        },
                    })
                    resp = await self._post(f"{self.gcl}/api/v1/events/deepfield", event)
                else:
                    resp = await self._post(f"{self.gcl}/api/v1/cycle",
                                            {"signals": [{"metric": "latency_ms",
                                                          "value": 5000.0, "source": "test"}]})
                if resp.status_code < 400:
                    ok += 1
                else:
                    err += 1
            except Exception:
                err += 1
            await asyncio.sleep(2)
        return ok, err

    # ── Test 1: Fleet controller pod kill ──

    async def test_fleet_kill(self):
        print("\n  === Test 1: Fleet controller pod kill ===")
        print("    Starting background load...")

        load_task = asyncio.create_task(self._run_background_load(90))
        await asyncio.sleep(10)

        print("    Killing fleet-controller pod...")
        await self._delete_pods("fleet-llm-d", "app=fleet-controller")

        recovery_ms = await self._wait_healthy("fleet", self.fleet)
        await asyncio.sleep(5)

        load_task.cancel()
        try:
            ok, err = await load_task
        except asyncio.CancelledError:
            ok, err = 0, 0

        passed = recovery_ms > 0 and recovery_ms < 60000
        self.results.append(ResilienceResult(
            "fleet_controller_kill", passed,
            f"recovery={recovery_ms:.0f}ms, events={ok}, errors={err}",
            recovery_ms, ok, err))
        print(f"    Recovery: {recovery_ms:.0f}ms  Events: {ok} ok, {err} errors  "
              f"{'PASS' if passed else 'FAIL'}")

    # ── Test 2: GCL pod kill ──

    async def test_gcl_kill(self):
        print("\n  === Test 2: GCL pod kill ===")
        print("    Starting background load...")

        load_task = asyncio.create_task(self._run_background_load(90))
        await asyncio.sleep(10)

        print("    Killing GCL pod...")
        await self._delete_pods("governed-cognitive-loop", "app=gcl-app")

        recovery_ms = await self._wait_healthy("gcl", self.gcl)
        await asyncio.sleep(5)

        load_task.cancel()
        try:
            ok, err = await load_task
        except asyncio.CancelledError:
            ok, err = 0, 0

        passed = recovery_ms > 0 and recovery_ms < 60000
        self.results.append(ResilienceResult(
            "gcl_kill", passed,
            f"recovery={recovery_ms:.0f}ms, events={ok}, errors={err}",
            recovery_ms, ok, err))
        print(f"    Recovery: {recovery_ms:.0f}ms  Events: {ok} ok, {err} errors  "
              f"{'PASS' if passed else 'FAIL'}")

    # ── Test 3: Mock inference kill (fleet should handle missing backend) ──

    async def test_inference_kill(self):
        print("\n  === Test 3: Mock inference pod kill ===")

        print("    Killing mock-inference pod...")
        await self._delete_pods("fleet-llm-d", "app=mock-inference")

        await asyncio.sleep(5)

        try:
            resp = await self._get(f"{self.fleet}/healthz")
            fleet_ok = resp.status_code == 200
        except Exception:
            fleet_ok = False

        recovery_ms = 0
        if not fleet_ok:
            recovery_ms = await self._wait_healthy("fleet", self.fleet)

        await asyncio.sleep(15)

        passed = fleet_ok
        self.results.append(ResilienceResult(
            "inference_kill", passed,
            f"fleet_healthy={fleet_ok} after inference backend killed",
            recovery_ms))
        print(f"    Fleet healthy after inference kill: {fleet_ok}  {'PASS' if passed else 'FAIL'}")

    # ── Test 4: Simultaneous kill (fleet + GCL) ──

    async def test_simultaneous_kill(self):
        print("\n  === Test 4: Simultaneous fleet + GCL kill ===")

        print("    Killing both pods...")
        await self._delete_pods("fleet-llm-d", "app=fleet-controller")
        await self._delete_pods("governed-cognitive-loop", "app=gcl-app")

        fleet_recovery = await self._wait_healthy("fleet", self.fleet)
        gcl_recovery = await self._wait_healthy("gcl", self.gcl)

        await asyncio.sleep(5)

        ok, err = 0, 0
        for _ in range(10):
            try:
                resp = await self._get(f"{self.fleet}/healthz")
                if resp.status_code == 200:
                    ok += 1
                else:
                    err += 1
            except Exception:
                err += 1

        passed = fleet_recovery > 0 and gcl_recovery > 0 and fleet_recovery < 120000 and gcl_recovery < 120000
        self.results.append(ResilienceResult(
            "simultaneous_kill", passed,
            f"fleet_recovery={fleet_recovery:.0f}ms, gcl_recovery={gcl_recovery:.0f}ms, post_health={ok}/10",
            max(fleet_recovery, gcl_recovery), ok, err))
        print(f"    Fleet: {fleet_recovery:.0f}ms  GCL: {gcl_recovery:.0f}ms  "
              f"Post-health: {ok}/10  {'PASS' if passed else 'FAIL'}")

    # ── Test 5: Rapid restart cycling ──

    async def test_rapid_restart(self):
        print("\n  === Test 5: Rapid restart cycling (5x fleet kill) ===")
        recoveries = []

        for i in range(5):
            await self._delete_pods("fleet-llm-d", "app=fleet-controller")
            recovery_ms = await self._wait_healthy("fleet", self.fleet)
            recoveries.append(recovery_ms)
            print(f"    Kill #{i+1}: recovery={recovery_ms:.0f}ms")
            await asyncio.sleep(3)

        all_recovered = all(r > 0 for r in recoveries)
        max_recovery = max(recoveries) if recoveries else 0
        avg_recovery = sum(recoveries) / len(recoveries) if recoveries else 0

        passed = all_recovered and max_recovery < 120000
        self.results.append(ResilienceResult(
            "rapid_restart", passed,
            f"5 kills: avg={avg_recovery:.0f}ms, max={max_recovery:.0f}ms, all_recovered={all_recovered}",
            max_recovery))
        print(f"    Avg: {avg_recovery:.0f}ms  Max: {max_recovery:.0f}ms  "
              f"{'PASS' if passed else 'FAIL'}")

    # ── Test 6: Post-disruption soak ──

    async def test_post_disruption_soak(self):
        print("\n  === Test 6: Post-disruption soak (60s) ===")

        fleet_ok = await self._wait_healthy("fleet", self.fleet)
        gcl_ok = await self._wait_healthy("gcl", self.gcl)

        if fleet_ok < 0 or gcl_ok < 0:
            self.results.append(ResilienceResult(
                "post_disruption_soak", False,
                "systems not healthy before soak"))
            print(f"    Systems not healthy, skipping soak")
            return

        print("    Running 60s soak after all disruptions...")
        ok, err = await self._run_background_load(60)

        error_rate = err / max(ok + err, 1)
        passed = error_rate < 0.05 and ok > 10

        self.results.append(ResilienceResult(
            "post_disruption_soak", passed,
            f"events={ok}, errors={err}, error_rate={error_rate:.1%}",
            0, ok, err))
        print(f"    Events: {ok}  Errors: {err}  Error rate: {error_rate:.1%}  "
              f"{'PASS' if passed else 'FAIL'}")

    # ── Run all ──

    async def run(self):
        print(f"\n{'='*60}")
        print(f"MULTI-NODE RESILIENCE TEST (SNO)")
        print(f"{'='*60}")
        print(f"  Fleet: {self.fleet}")
        print(f"  GCL: {self.gcl}")
        print(f"  Mode: {'production (CloudEvent)' if self.deepfield_token else 'dev (/cycle)'}")

        await self.test_fleet_kill()
        await self.test_gcl_kill()
        await self.test_inference_kill()
        await self.test_simultaneous_kill()
        await self.test_rapid_restart()
        await self.test_post_disruption_soak()

        print(f"\n{'='*60}")
        print(f"RESILIENCE TEST RESULTS")
        print(f"{'='*60}")

        passed = sum(1 for r in self.results if r.passed)
        failed = sum(1 for r in self.results if not r.passed)

        for r in self.results:
            status = "PASS" if r.passed else "FAIL"
            print(f"  [{status}] {r.test}: {r.detail}")

        print(f"\n  TOTAL: {passed} passed, {failed} failed")
        if failed == 0:
            print(f"\n  RESULT: ALL RESILIENCE TESTS PASSED")
        else:
            print(f"\n  RESULT: {failed} FAILURE(S)")


async def main():
    parser = argparse.ArgumentParser(description="Multi-node resilience test")
    parser.add_argument("--gcl-url", default="http://gcl-app.governed-cognitive-loop.svc:8000")
    parser.add_argument("--fleet-url", default="http://fleet-controller.fleet-llm-d.svc:8080")
    parser.add_argument("--deepfield-token", default="")
    parser.add_argument("--timeout", type=float, default=15.0)
    args = parser.parse_args()

    test = ResilienceTest(args.gcl_url, args.fleet_url, args.deepfield_token,
                          args.timeout)
    await test.run()


if __name__ == "__main__":
    asyncio.run(main())
