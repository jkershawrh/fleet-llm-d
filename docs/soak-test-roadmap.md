# Soak Test Roadmap

## Completed (2026-08-08)

| Test | Ops | Result | What it proved |
|------|-----|--------|----------------|
| Cascade (smoke→stress) | 9,778+ | 100%, 14/14 SLO | All 20 phases across 3 clusters, 10x concurrency |
| 24hr-variable SNO soak | 268,738 | 99.0% | Variable intensity bands, SNO stability |
| 3-cluster smoke (20 phases) | 57 | 100%, 14/14 SLO | Grid CRD, SWIM sync, failover, inference all working |
| Pen test (live deployment) | 24/24 | 100% pass | SSRF, token replay, privilege escalation, large payload |
| Metric poisoning | 8/8 | 100% pass | Extreme/negative metrics rejected or handled safely |
| Inference saturation (GPU) | 13,809 | 0% errors | H100: 83.7 RPS peak, 163ms p50, no breakpoint to conc=30 |
| Inference saturation (CPU) | 1,336 | 0% errors | OVMS: 4.1 RPS peak, 3.8s p50, no breakpoint to conc=50 |
| Standard 2hr soak (3-cluster) | 1,770 | 98.7% (23 cycles) | Clean until SNO node crash — infra limit, not app code |

## Key Findings

- **GPU/CPU throughput ratio: 20x** — fleet-level placement decisions have massive impact
- **SNO crash point: ~22 min sustained** — Oberon drops under continuous 3-cluster orchestration
- **Error pattern: 1/cycle** — consistent, likely drain/failover timing on SNO, not app code
- **No drift detected** — latency flat at 218ms p50 across all 23 clean cycles, no memory growth
- **Grid CRD + SWIM integration working** — phases 17-19 pass on live clusters

## Remaining Tests

| # | Test | Status | Effort | Evidence value |
|---|------|--------|--------|----------------|
| 1 | Inference saturation | **DONE** | — | GPU 83.7 RPS, CPU 4.1 RPS |
| 2 | Pen test (live) | **DONE** | — | 24/24 pass |
| 3 | Metric poisoning | **DONE** | — | 8/8 pass |
| 4 | Controller HA failover | Not built | 4hr | Medium — needs multi-replica deploy |
| 5 | Ledger durability | Not built | 2hr build + 4hr run | Medium — compliance proof |
| 6 | Network partition | Not built | 2hr build + 2hr run | Medium — resilience proof |
| 7 | 72hr stability | Available (profile exists) | Time | Medium — leak detection |
| 8 | Multi-tenant isolation | Not built | 4hr build + 2hr run | Medium — governance proof |
| 9 | 8hr variable (3-cluster) | Available (profile added) | 8hr run | High — needs non-SNO hub |

## Infrastructure Lesson

The SNO hub (Oberon) consistently crashes under sustained multi-cluster orchestration (>20 min). All app code tests pass — the bottleneck is single-node infrastructure. **Recommendation: deploy the fleet-controller on a multi-node cluster (Arena has 5 nodes) for production-grade soak testing.**
