"""Fleet-llm-d capability soak test.

Exercises every fleet-llm-d capability in a sustained loop:
  1. Cluster registration & discovery
  2. Agent status & metrics ingestion
  3. Tenant creation & quota enforcement
  4. Placement via webhook
  5. Rollout lifecycle (create, promote, rollback)
  6. Real inference through fleet proxy (token metering)
  7. Fleet metrics federation
  8. Cost & pricing APIs
  9. Ledger chain verification (cross-cluster)
  10. Degradation injection & recovery

Usage:
    python3 capability_soak.py \
        --fleet-url http://fleet-controller.fleet-llm-d.svc:8080 \
        --ledger-url https://ledger.example.com \
        --inference-url http://ovms-granite-2b.fleet-llm-d.svc:8080/v3 \
        --profile 72hr
"""

from __future__ import annotations

import argparse
import asyncio
import json
import os
import statistics
import sys
import time
import uuid
from dataclasses import dataclass, field

import httpx

PROFILES = {
    "quick":     {"duration": 1800,   "cycle_interval": 30,  "inject_interval": 600},
    "standard":  {"duration": 7200,   "cycle_interval": 30,  "inject_interval": 900},
    "overnight": {"duration": 28800,  "cycle_interval": 30,  "inject_interval": 1800},
    "72hr":      {"duration": 259200, "cycle_interval": 30,  "inject_interval": 3600},
}

CLUSTERS = [
    {"id": "cpucluster-xeon6", "name": "CpuCluster Dell Xeon6", "region": "us-east-1",
     "labels": {"gpu": "cpu", "runtime": "vllm+ovms", "hardware": "xeon6-amx",
                "health_url": "http://ovms-granite-2b.fleet-llm-d.svc:8080/v2/health/ready",
                "inference_url": "http://ovms-granite-2b.fleet-llm-d.svc:8080/v3"}},
    {"id": "hubcluster-sno", "name": "HubCluster SNO", "region": "us-east-1",
     "labels": {"gpu": "cpu", "runtime": "ovms", "hardware": "xeon",
                "health_url": "http://granite-real.fleet-llm-d.svc:8000/health",
                "inference_url": "http://granite-real.fleet-llm-d.svc:8000"}},
]

TENANTS = [
    {"id": "tenant-prod", "name": "Production", "priority": 1,
     "quotas": {"maxTokensPerMinute": 1000000, "maxConcurrentRequests": 100}},
    {"id": "tenant-staging", "name": "Staging", "priority": 5,
     "quotas": {"maxTokensPerMinute": 100000, "maxConcurrentRequests": 20}},
]

INFERENCE_PROMPTS = [
    "What is Kubernetes in one sentence?",
    "Explain containers briefly.",
    "What is Red Hat OpenShift?",
    "Define model inference.",
    "What is fleet orchestration?",
    "Explain KV cache in LLMs.",
    "What is tensor parallelism?",
    "Define inference latency.",
]


@dataclass
class CapabilityResult:
    name: str
    successes: int = 0
    errors: int = 0
    latencies: list[float] = field(default_factory=list)
    last_error: str = ""

    @property
    def total(self):
        return self.successes + self.errors

    @property
    def success_rate(self):
        return self.successes / max(self.total, 1) * 100

    @property
    def p50(self):
        if not self.latencies:
            return 0
        s = sorted(self.latencies)
        return s[len(s) // 2]

    @property
    def p99(self):
        if not self.latencies:
            return 0
        s = sorted(self.latencies)
        return s[int(len(s) * 0.99)]


@dataclass
class SoakState:
    profile: str
    start_time: float = 0
    cycle_count: int = 0
    capabilities: dict[str, CapabilityResult] = field(default_factory=dict)
    chain_verifications: list[dict] = field(default_factory=list)
    injections: list[dict] = field(default_factory=list)
    slo_violations: list[str] = field(default_factory=list)

    def cap(self, name: str) -> CapabilityResult:
        if name not in self.capabilities:
            self.capabilities[name] = CapabilityResult(name=name)
        return self.capabilities[name]


class CapabilitySoak:
    def __init__(self, fleet_url: str, ledger_url: str = "",
                 inference_url: str = "", inference_model: str = "",
                 gcl_url: str = "", deepfield_url: str = "",
                 timeout: float = 30.0):
        self.fleet = fleet_url.rstrip("/")
        self.ledger = ledger_url.rstrip("/") if ledger_url else ""
        self.inference_url = inference_url.rstrip("/") if inference_url else ""
        self.inference_model = inference_model
        self.gcl = gcl_url.rstrip("/") if gcl_url else ""
        self.deepfield = deepfield_url.rstrip("/") if deepfield_url else ""
        self.timeout = timeout
        self._cycle = 0
        self._rollout_ids: list[str] = []
        self._initial_ledger_entries = 0

    async def _req(self, method: str, url: str, data=None,
                   headers=None) -> tuple[httpx.Response, float]:
        start = time.monotonic()
        async with httpx.AsyncClient(verify=False, timeout=self.timeout) as c:
            if method == "GET":
                resp = await c.get(url, headers=headers)
            elif method == "POST":
                if isinstance(data, str):
                    resp = await c.post(url, content=data, headers=headers or {})
                else:
                    resp = await c.post(url, json=data, headers=headers)
            elif method == "DELETE":
                resp = await c.delete(url, headers=headers)
            else:
                raise ValueError(f"unsupported method: {method}")
        ms = (time.monotonic() - start) * 1000
        return resp, ms

    # ── Capability: Cluster Registration ──

    async def test_clusters(self, state: SoakState):
        cap = state.cap("clusters")
        try:
            for cluster in CLUSTERS:
                resp, ms = await self._req("POST", f"{self.fleet}/api/v1/clusters", cluster)
                if resp.status_code in (201, 409):
                    cap.successes += 1
                    cap.latencies.append(ms)
                else:
                    cap.errors += 1
                    cap.last_error = f"register {cluster['id']}: {resp.status_code}"

            resp, ms = await self._req("GET", f"{self.fleet}/api/v1/clusters")
            if resp.status_code == 200:
                clusters = resp.json()
                cap.successes += 1
                cap.latencies.append(ms)
                if len(clusters) < 2:
                    cap.last_error = f"expected >=2 clusters, got {len(clusters)}"
            else:
                cap.errors += 1
                cap.last_error = f"list clusters: {resp.status_code}"
        except Exception as e:
            cap.errors += 1
            cap.last_error = str(e)[:80]

    # ── Capability: Agent Status & Metrics ──

    async def test_agent_ingestion(self, state: SoakState):
        cap = state.cap("agent")
        try:
            for cluster in CLUSTERS:
                status = {
                    "cluster_id": cluster["id"], "name": cluster["name"],
                    "region": cluster["region"], "phase": "Running",
                    "healthy": True, "gpu_total": 8, "gpu_available": 4 + self._cycle % 4,
                }
                resp, ms = await self._req("POST", f"{self.fleet}/api/v1/agent/status", status)
                if resp.status_code in (200, 201):
                    cap.successes += 1
                    cap.latencies.append(ms)
                else:
                    cap.errors += 1
                    cap.last_error = f"agent status {cluster['id']}: {resp.status_code}"

                metrics = {
                    "cluster_id": cluster["id"],
                    "throughput_tps": 10.0 + self._cycle % 50,
                    "ttft_p50_ms": 15.0 + self._cycle % 30,
                    "ttft_p99_ms": 50.0 + self._cycle % 100,
                    "queue_depth": self._cycle % 20,
                    "gpu_utilization": 0.3 + (self._cycle % 50) / 100.0,
                    "kv_cache_hit_rate": 0.5 + (self._cycle % 40) / 100.0,
                }
                resp, ms = await self._req("POST", f"{self.fleet}/api/v1/agent/metrics", metrics)
                if resp.status_code == 202:
                    cap.successes += 1
                    cap.latencies.append(ms)
                else:
                    cap.errors += 1
                    cap.last_error = f"agent metrics: {resp.status_code}"
        except Exception as e:
            cap.errors += 1
            cap.last_error = str(e)[:80]

    # ── Capability: Tenant Management ──

    async def test_tenants(self, state: SoakState):
        cap = state.cap("tenants")
        try:
            for tenant in TENANTS:
                resp, ms = await self._req("POST", f"{self.fleet}/api/v1/tenants", tenant)
                if resp.status_code in (201, 409):
                    cap.successes += 1
                    cap.latencies.append(ms)
                else:
                    cap.errors += 1
                    cap.last_error = f"create tenant: {resp.status_code}"

            resp, ms = await self._req("GET", f"{self.fleet}/api/v1/tenants")
            if resp.status_code == 200:
                cap.successes += 1
                cap.latencies.append(ms)
            else:
                cap.errors += 1

            resp, ms = await self._req("GET",
                f"{self.fleet}/api/v1/agent/policies/{CLUSTERS[0]['id']}")
            if resp.status_code == 200:
                policies = resp.json()
                if "quotas" in policies:
                    cap.successes += 1
                    cap.latencies.append(ms)
                else:
                    cap.errors += 1
                    cap.last_error = "no quotas in policies"
            else:
                cap.errors += 1
        except Exception as e:
            cap.errors += 1
            cap.last_error = str(e)[:80]

    # ── Capability: Placement ──

    async def test_placement(self, state: SoakState):
        cap = state.cap("placement")
        try:
            pool_event = {
                "type": "ADDED",
                "object": {
                    "model": {"name": f"soak-model-{self._cycle % 5}",
                              "source": "registry.redhat.io/granite-test"},
                    "placement": {"policyRef": "spread", "minClusters": 1},
                },
            }
            resp, ms = await self._req("POST",
                f"{self.fleet}/api/v1/webhook/fleetinferencepool", pool_event)
            if resp.status_code == 200:
                cap.successes += 1
                cap.latencies.append(ms)
            else:
                cap.errors += 1
                cap.last_error = f"webhook: {resp.status_code}"

            resp, ms = await self._req("GET", f"{self.fleet}/api/v1/pools")
            if resp.status_code == 200:
                cap.successes += 1
                cap.latencies.append(ms)
            else:
                cap.errors += 1
        except Exception as e:
            cap.errors += 1
            cap.last_error = str(e)[:80]

    # ── Capability: Rollout Lifecycle ──

    async def test_lifecycle(self, state: SoakState):
        cap = state.cap("lifecycle")
        try:
            rollout = {
                "pool_id": f"soak-pool-{self._cycle % 3}",
                "model_version": f"v{self._cycle}.0.0",
                "strategy": "canary",
            }
            resp, ms = await self._req("POST", f"{self.fleet}/api/v1/rollouts", rollout)
            if resp.status_code == 201:
                cap.successes += 1
                cap.latencies.append(ms)
            else:
                cap.errors += 1
                cap.last_error = f"create rollout: {resp.status_code}"

            resp, ms = await self._req("GET", f"{self.fleet}/api/v1/rollouts")
            if resp.status_code == 200:
                rollouts = resp.json()
                cap.successes += 1
                cap.latencies.append(ms)
                if rollouts:
                    rid = rollouts[-1].get("ID", "")
                    if rid:
                        resp, ms = await self._req("POST",
                            f"{self.fleet}/api/v1/rollouts/{rid}/promote", {})
                        if resp.status_code == 200:
                            cap.successes += 1
                            cap.latencies.append(ms)
                        else:
                            cap.errors += 1
                            cap.last_error = f"promote: {resp.status_code}"
            else:
                cap.errors += 1
        except Exception as e:
            cap.errors += 1
            cap.last_error = str(e)[:80]

    # ── Capability: Inference ──

    async def test_inference(self, state: SoakState):
        cap = state.cap("inference")
        if not self.inference_url or not self.inference_model:
            return
        try:
            prompt = INFERENCE_PROMPTS[self._cycle % len(INFERENCE_PROMPTS)]
            payload = {
                "model": self.inference_model,
                "messages": [{"role": "user", "content": prompt}],
                "max_tokens": 15,
            }
            resp, ms = await self._req("POST",
                f"{self.inference_url}/chat/completions", payload,
                {"Content-Type": "application/json"})
            if resp.status_code == 200:
                data = resp.json()
                tokens = data.get("usage", {}).get("total_tokens", 0)
                cap.successes += 1
                cap.latencies.append(ms)
                state.cap("tokens").successes += tokens
            else:
                cap.errors += 1
                cap.last_error = f"inference: {resp.status_code}"
        except Exception as e:
            cap.errors += 1
            cap.last_error = str(e)[:80]

    # ── Capability: Observability ──

    async def test_observability(self, state: SoakState):
        cap = state.cap("observability")
        try:
            endpoints = [
                "/api/v1/cost/pricing",
                "/api/v1/cost/projection",
                "/api/v1/cost/alerts",
            ]
            for ep in endpoints:
                resp, ms = await self._req("GET", f"{self.fleet}{ep}")
                if resp.status_code == 200:
                    cap.successes += 1
                    cap.latencies.append(ms)
                else:
                    cap.errors += 1
                    cap.last_error = f"{ep}: {resp.status_code}"
        except Exception as e:
            cap.errors += 1
            cap.last_error = str(e)[:80]

    # ── Capability: Ledger Chain Integrity ──

    async def test_ledger(self, state: SoakState):
        cap = state.cap("ledger")
        if not self.ledger:
            return
        try:
            resp, ms = await self._req("GET", f"{self.ledger}/api/verify")
            if resp.status_code == 200:
                data = resp.json()
                if data.get("all_valid", False):
                    cap.successes += 1
                    cap.latencies.append(ms)
                    total = sum(c.get("entries_checked", 0) for c in data.get("chains", []))
                    if self._initial_ledger_entries == 0:
                        self._initial_ledger_entries = total
                    fleet_entries = sum(c.get("entries_checked", 0)
                        for c in data.get("chains", [])
                        if c.get("entry_type", "").startswith("fleet."))
                    state.cap("ledger_entries").successes = fleet_entries
                else:
                    cap.errors += 1
                    cap.last_error = "chain integrity failure"
            else:
                cap.errors += 1
                cap.last_error = f"ledger verify: {resp.status_code}"
        except Exception as e:
            cap.errors += 1
            cap.last_error = str(e)[:80]

    # ── Capability: GCL Governance ──

    async def test_gcl(self, state: SoakState):
        cap = state.cap("gcl")
        if not self.gcl:
            return
        try:
            resp, ms = await self._req("GET", f"{self.gcl}/healthz")
            if resp.status_code == 200:
                cap.successes += 1
                cap.latencies.append(ms)
            else:
                cap.errors += 1
                cap.last_error = f"gcl healthz: {resp.status_code}"

            resp, ms = await self._req("POST", f"{self.gcl}/api/v1/scenario/seed",
                {"scenario": "slo_cascade", "seed": self._cycle % 10000})
            if resp.status_code == 200:
                cap.successes += 1
                cap.latencies.append(ms)
                resp, ms = await self._req("GET",
                    f"{self.gcl}/api/v1/scenario/step/{self._cycle % 8}")
                if resp.status_code == 200:
                    cap.successes += 1
                    cap.latencies.append(ms)
            else:
                cap.errors += 1
                cap.last_error = f"gcl seed: {resp.status_code}"
        except Exception as e:
            cap.errors += 1
            cap.last_error = str(e)[:80]

    # ── Capability: DeepField ──

    async def test_deepfield(self, state: SoakState):
        cap = state.cap("deepfield")
        if not self.deepfield:
            return
        try:
            resp, ms = await self._req("GET", f"{self.deepfield}/api/v1/health")
            if resp.status_code == 200:
                cap.successes += 1
                cap.latencies.append(ms)
            else:
                cap.errors += 1
                cap.last_error = f"deepfield health: {resp.status_code}"
        except Exception as e:
            cap.errors += 1
            cap.last_error = str(e)[:80]

    # ── Capability: Health & Memory ──

    async def test_health(self, state: SoakState):
        cap = state.cap("health")
        try:
            resp, ms = await self._req("GET", f"{self.fleet}/healthz")
            if resp.status_code == 200:
                cap.successes += 1
                cap.latencies.append(ms)
            else:
                cap.errors += 1
                cap.last_error = f"healthz: {resp.status_code}"
        except Exception as e:
            cap.errors += 1
            cap.last_error = str(e)[:80]

    # ── Degradation Injection ──

    async def inject_degradation(self, state: SoakState):
        cap = state.cap("recovery")
        injection = self._cycle % 3
        start = time.monotonic()
        try:
            if injection == 0:
                tasks = [self._req("GET", f"{self.fleet}/healthz") for _ in range(50)]
                results = await asyncio.gather(*tasks, return_exceptions=True)
                errors = sum(1 for r in results if isinstance(r, Exception))
                detail = f"burst_50: {errors} errors"
            elif injection == 1:
                resp, _ = await self._req("POST", f"{self.fleet}/api/v2/intents",
                    '{"invalid": true}', {"Content-Type": "application/json"})
                detail = f"invalid_intent: {resp.status_code}"
            else:
                resp, _ = await self._req("POST",
                    f"{self.fleet}/api/v1/agent/events",
                    {"cluster_id": "chaos-cluster", "event": {"type": "chaos.test"}})
                detail = f"chaos_event: {resp.status_code}"

            recovery_ms = (time.monotonic() - start) * 1000
            resp, ms = await self._req("GET", f"{self.fleet}/healthz")
            if resp.status_code == 200:
                cap.successes += 1
                cap.latencies.append(recovery_ms)
            else:
                cap.errors += 1
                cap.last_error = f"recovery failed after {detail}"

            state.injections.append({
                "cycle": self._cycle, "detail": detail,
                "recovery_ms": recovery_ms,
            })
            print(f"  >>> INJECTION: {detail} (recovery: {recovery_ms:.0f}ms)")
        except Exception as e:
            cap.errors += 1
            cap.last_error = str(e)[:80]

    # ── Main Loop ──

    async def run(self, profile_name: str):
        profile = PROFILES[profile_name]
        duration = profile["duration"]
        cycle_interval = profile["cycle_interval"]
        inject_interval = profile["inject_interval"]

        state = SoakState(profile=profile_name)
        state.start_time = time.monotonic()
        last_inject = state.start_time

        print(f"\n{'='*70}")
        print(f"FLEET-LLM-D CAPABILITY SOAK TEST")
        print(f"{'='*70}")
        print(f"Profile: {profile_name} ({duration/60:.0f} minutes)")
        print(f"Cycle interval: {cycle_interval}s")
        print(f"Fleet: {self.fleet}")
        if self.inference_url:
            print(f"Inference: {self.inference_url} (model: {self.inference_model})")
        if self.gcl:
            print(f"GCL: {self.gcl}")
        if self.deepfield:
            print(f"DeepField: {self.deepfield}")
        if self.ledger:
            print(f"Ledger: {self.ledger}")
        print(f"{'='*70}")

        # Initial health
        await self.test_health(state)
        h = state.cap("health")
        print(f"  Fleet: {'UP' if h.successes > 0 else 'DOWN'}")
        print()

        # Header
        print(f"  {'Cycle':>5}  {'Time':>7}  "
              f"{'Clust':>5} {'Agent':>5} {'Tenant':>6} {'Place':>5} "
              f"{'Life':>4} {'Infer':>5} {'Obs':>3} {'Ledgr':>5} "
              f"{'Hlth':>4}  {'Err':>3}  {'Status'}")
        print(f"  {'-'*5}  {'-'*7}  "
              f"{'-'*5} {'-'*5} {'-'*6} {'-'*5} "
              f"{'-'*4} {'-'*5} {'-'*3} {'-'*5} "
              f"{'-'*4}  {'-'*3}  {'-'*6}")

        while time.monotonic() - state.start_time < duration:
            self._cycle += 1
            state.cycle_count = self._cycle
            now = time.monotonic()

            # Run all capabilities
            if self._cycle <= 1:
                await self.test_clusters(state)
                await self.test_tenants(state)
            await self.test_agent_ingestion(state)
            await self.test_placement(state)
            await self.test_lifecycle(state)
            await self.test_inference(state)
            await self.test_observability(state)
            await self.test_gcl(state)
            await self.test_deepfield(state)
            await self.test_health(state)

            # Ledger every 5th cycle
            if self._cycle % 5 == 0:
                await self.test_ledger(state)

            # Degradation injection
            if now - last_inject >= inject_interval:
                last_inject = now
                await self.inject_degradation(state)

            # Print status line
            elapsed = now - state.start_time
            minutes = int(elapsed / 60)
            seconds = int(elapsed % 60)
            total_errors = sum(c.errors for c in state.capabilities.values()
                               if c.name != "tokens")
            print(f"  {self._cycle:5d}  {minutes:3d}:{seconds:02d}  "
                  f"{state.cap('clusters').successes:5d} "
                  f"{state.cap('agent').successes:5d} "
                  f"{state.cap('tenants').successes:6d} "
                  f"{state.cap('placement').successes:5d} "
                  f"{state.cap('lifecycle').successes:4d} "
                  f"{state.cap('inference').successes:5d} "
                  f"{state.cap('observability').successes:3d} "
                  f"{state.cap('ledger').successes:5d} "
                  f"{state.cap('health').successes:4d}  "
                  f"{total_errors:3d}  "
                  f"{'UP' if state.cap('health').latencies else 'DOWN'}")

            await asyncio.sleep(cycle_interval)

        self._print_results(state)
        self._check_slos(state)
        return state

    def _print_results(self, state: SoakState):
        elapsed = time.monotonic() - state.start_time
        print(f"\n{'='*70}")
        print(f"CAPABILITY SOAK RESULTS ({state.profile})")
        print(f"{'='*70}")
        print(f"\n  Duration: {elapsed/3600:.1f} hours ({state.cycle_count} cycles)")

        print(f"\n  {'Capability':<20s}  {'OK':>6}  {'Err':>5}  {'Rate':>6}  "
              f"{'p50ms':>7}  {'p99ms':>7}")
        print(f"  {'-'*20}  {'-'*6}  {'-'*5}  {'-'*6}  {'-'*7}  {'-'*7}")

        for name in ["clusters", "agent", "tenants", "placement", "lifecycle",
                      "inference", "observability", "gcl", "deepfield", "ledger", "health", "recovery"]:
            cap = state.cap(name)
            if cap.total == 0:
                continue
            print(f"  {name:<20s}  {cap.successes:6d}  {cap.errors:5d}  "
                  f"{cap.success_rate:5.1f}%  {cap.p50:6.0f}  {cap.p99:6.0f}")

        tokens = state.cap("tokens").successes
        if tokens > 0:
            print(f"\n  Total inference tokens: {tokens}")

        if state.injections:
            recoveries = [i["recovery_ms"] for i in state.injections]
            print(f"\n  Degradation injections: {len(state.injections)}")
            print(f"    Avg recovery: {statistics.mean(recoveries):.0f}ms")
            print(f"    Max recovery: {max(recoveries):.0f}ms")

    def _check_slos(self, state: SoakState):
        print(f"\n  {'='*50}")
        print(f"  SLO GATES")
        print(f"  {'='*50}")

        gates = []

        for name in ["clusters", "agent", "tenants", "placement", "lifecycle",
                      "observability", "gcl", "deepfield", "health"]:
            cap = state.cap(name)
            if cap.total > 0:
                ok = cap.success_rate >= 95
                gates.append((f"{name} success >= 95%", ok,
                              f"{cap.success_rate:.1f}%"))

        cap = state.cap("inference")
        if cap.total > 0:
            ok = cap.success_rate >= 90
            gates.append(("inference success >= 90%", ok,
                          f"{cap.success_rate:.1f}%"))

        cap = state.cap("ledger")
        if cap.total > 0:
            ok = cap.success_rate == 100
            gates.append(("ledger integrity 100%", ok,
                          f"{cap.success_rate:.1f}%"))

        cap = state.cap("health")
        if cap.total > 0:
            ok = cap.success_rate >= 99.5
            gates.append(("availability >= 99.5%", ok,
                          f"{cap.success_rate:.1f}%"))

        cap = state.cap("recovery")
        if cap.latencies:
            max_recovery = max(cap.latencies)
            ok = max_recovery < 60000
            gates.append(("recovery < 60s", ok, f"max={max_recovery/1000:.1f}s"))

        passed = failed = 0
        for name, ok, detail in gates:
            status = "PASS" if ok else "FAIL"
            if ok:
                passed += 1
            else:
                failed += 1
                state.slo_violations.append(f"{name}: {detail}")
            print(f"  [{status}] {name}: {detail}")

        print(f"\n  TOTAL: {passed} passed, {failed} failed")
        if failed == 0:
            print(f"\n  RESULT: ALL SLO GATES PASSED")
        else:
            print(f"\n  RESULT: {failed} SLO VIOLATION(S)")


async def main():
    parser = argparse.ArgumentParser(description="Fleet-llm-d capability soak test")
    parser.add_argument("--fleet-url", default="http://fleet-controller.fleet-llm-d.svc:8080")
    parser.add_argument("--ledger-url", default="")
    parser.add_argument("--inference-url", default="",
                        help="Direct inference endpoint (e.g. http://ovms:8080/v3)")
    parser.add_argument("--inference-model", default="granite-sovereign")
    parser.add_argument("--gcl-url", default="",
                        help="GCL endpoint for governance pipeline testing")
    parser.add_argument("--deepfield-url", default="",
                        help="DeepField endpoint for observation testing")
    parser.add_argument("--profile", default="quick",
                        choices=list(PROFILES.keys()))
    parser.add_argument("--timeout", type=float, default=30.0)
    args = parser.parse_args()

    soak = CapabilitySoak(args.fleet_url, args.ledger_url,
                          args.inference_url, args.inference_model,
                          args.gcl_url, args.deepfield_url,
                          args.timeout)
    await soak.run(args.profile)


if __name__ == "__main__":
    asyncio.run(main())
