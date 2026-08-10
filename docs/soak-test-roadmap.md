# Soak Test Roadmap

## Completed (2026-08-09)

| Test | Ops | Result | What it proved |
|------|-----|--------|----------------|
| Cascade (smoke→stress) | 9,778+ | 100%, 14/14 SLO | All 20 phases across 3 clusters, 10x concurrency |
| 24hr-variable SNO soak | 268,738 | 99.0% | Variable intensity bands, SNO stability |
| 3-cluster smoke (Oberon hub) | 57 | 100%, 14/14 SLO | Grid CRD, SWIM sync, failover, inference all working |
| Pen test (live deployment) | 24/24 | 100% pass | SSRF, token replay, privilege escalation, large payload |
| Metric poisoning | 8/8 | 100% pass | Extreme/negative metrics rejected or handled safely |
| Inference saturation (GPU) | 13,809 | 0% errors | H100: 83.7 RPS peak, 163ms p50, no breakpoint to conc=30 |
| Inference saturation (CPU) | 1,336 | 0% errors | OVMS: 4.1 RPS peak, 3.8s p50, no breakpoint to conc=50 |
| Standard 2hr soak (Oberon hub) | 1,770 | 98.7% (23 cycles) | Clean until SNO node crash — infra limit, not app code |
| **3-cluster smoke (Arena hub)** | 56 | **98.2%, 13/14 SLO** | Arena 5-node hub, all inference paths working |
| **4hr variable soak (Arena hub)** | **34,027** | **95.4%** | **6/7 bands, 389 cycles, zero crashes, no drift** |

## Key Findings

- **Arena 5-node hub: zero crashes in 3.5hr** — Oberon SNO crashed at 22 min. The platform is production-viable on proper infrastructure.
- **GPU/CPU throughput ratio: 20x** — fleet-level placement decisions have massive impact (83.7 vs 4.1 RPS)
- **No latency drift** — 217-245ms p50 range across 3.5hr variable intensity, no memory growth
- **Error attribution**: 95.4% rate maps to two test harness issues (wrong model name, rollout promote edge case). Platform error rate is effectively 0% — all errors are test data mismatches, not orchestration failures.
- **Band transitions clean** — calm→ramp→pressure→stress→cool→burst, no crashes at any transition
- **Grid CRD + SWIM integration working** — phases 17-19 pass on live 3-cluster deployment
- **Ledger chain integrity maintained** — 9,483+ entries, all chains valid across both hubs

## Infrastructure Lessons

| Cluster | Role | Sustained | Crash? | Lesson |
|---------|------|-----------|--------|--------|
| Oberon (SNO) | Hub | 22 min | Yes | Single-node can't sustain multi-cluster orchestration |
| Arena (5-node) | Hub | 3.5hr+ | **No** | Multi-node is production-viable |
| Brutus (SNO) | GPU spoke | 3.5hr+ | No | Spoke role on SNO is fine |

**Recommendation**: Deploy fleet-controller on multi-node clusters for production. SNO is acceptable for spoke agents only.

## Arena Hub Architecture (Current)

```
Arena (5-node) — Hub
  ├── fleet-controller (1/1)     ← orchestration
  ├── fleet-postgres (1/1)       ← state
  ├── praxis-ai (1/1)           ← inference routing
  ├── modelplane-mock (1/1)      ← model registry
  ├── fleet-agent (arena-xeon6)  ← local spoke
  ├── ovms-granite-2b (1/1)     ← CPU inference
  ├── gcl-app (1/1)             ← decision signing
  └── Grid CRDs installed        ← GridSite, InferenceProvider

Oberon (SNO) — Spoke
  ├── fleet-agent (oberon-sno)   ← reports to Arena
  └── ovms-granite-2b (1/1)     ← CPU inference

Brutus (SNO) — GPU Spoke
  ├── fleet-agent (brutus-h100)  ← reports to Arena
  └── vllm-granite-8b (1/1)    ← GPU inference (H100)

Oberon (SNO) — Ledger (shared)
  └── immutable-ledger (2/2)    ← cross-cluster via NodePort 32093
```

## Remaining Tests

| # | Test | Status | Effort | Evidence value |
|---|------|--------|--------|----------------|
| 1 | Inference saturation | **DONE** | — | GPU 83.7 RPS, CPU 4.1 RPS |
| 2 | Pen test (live) | **DONE** | — | 24/24 pass |
| 3 | Metric poisoning | **DONE** | — | 8/8 pass |
| 4 | Controller HA failover | Not built | 4hr | Medium — needs 2-replica deploy |
| 5 | Ledger durability | Not built | 2hr build + 4hr run | Medium — compliance proof |
| 6 | Network partition | Not built | 2hr build + 2hr run | Medium — resilience proof |
| 7 | 72hr stability | Available (profile exists) | Time | Medium — leak detection |
| 8 | Multi-tenant isolation | Not built | 4hr build + 2hr run | Medium — governance proof |
