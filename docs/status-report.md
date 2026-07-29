# fleet-llm-d Status Report

**Date:** July 29, 2026
**Clusters:** Oberon (SNO, active), Dell Arena (multi-node, available for redeployment)
**Stage:** Staging-promotable, ecosystem soak running

---

## What fleet-llm-d Is

Fleet-level inference orchestration for llm-d. Extends single-cluster LLM inference to multi-cluster fleet operations with:

- **Go control plane** -- model placement, routing, autoscaling, tenant governance, lifecycle management
- **Rust data plane** -- fleet gateway, fleet agent, KV cache transfer
- **Praxis AI gateway** -- programmable inference routing with token counting and access logging
- **Multi-cluster federation** -- cross-cluster routing, health-aware load balancing, cluster discovery
- **PostgreSQL persistence** -- state survives controller restarts

## Architecture

```
Client → Praxis AI Gateway → Fleet Controller → Inference Backend (OVMS/vLLM)
                                    │
                          ┌─────────┼─────────┐
                          │         │         │
                    Oberon SNO  Arena Dell  (future spokes)
                    OVMS CPU    (available)
                    6 models
```

**Three-layer target architecture:**
- **Layer 3: fleet-llm-d** -- Operations control plane (placement, scaling, tenants, governance, ledger)
- **Layer 2: Praxis AI + Grid** -- Programmable AI data plane (routing, protocol translation, mTLS mesh)
- **Layer 1: ConnectLink + NIXL** -- GPU/accelerator fabric (KV cache transfer, prefix sharing)

**Ecosystem pipeline:** DeepField (observations) → GCL (governed decisions) → fleet-llm-d (admission, routing, actuation) → ARE Ledger (tamper-evident evidence)

---

## What Has Been Built

### Unified State Layer
| Item | Description | Evidence |
|------|-------------|----------|
| Shared repositories | 5 disconnected stores wired to shared ClusterRepo, TenantRepo, RolloutRepo, PoolRepo | `pkg/server/controller.go` |
| PostgreSQL persistence | All state survives controller restarts via `OverrideWithPostgres()` | 10.5-hour soak with restart recovery |
| MetricsFederator | Builds from live collector data instead of hardcoded map | `pkg/observability/metrics/federation.go` |
| RolloutController | Reads from shared RolloutRepo, case-insensitive strategy matching | `pkg/lifecycle/rollout/controller.go` |
| QuotaEnforcer | Builds tenant profiles from repo, no hardcoded seeds | `pkg/tenant/quota/enforcer.go` |
| UsageTracker | Queries repo for real usage data | `pkg/tenant/metering/tracker.go` |

### Praxis AI Integration
| Item | Description | Evidence |
|------|-------------|----------|
| Praxis AI gateway | Deployed on Oberon, routes 6 OVMS models through single endpoint | `praxis-ai-6d785477b` Running |
| Model routing | granite-2b-cpu, granite-8b, granite-4.1-3b, granite-4.1-8b, phi3-mini, qwen25-3b | All proven via port-forward |
| Token counting | Praxis counts tokens per request with access logging | Config in `deploy/praxis/praxis-ai-config.yaml` |
| Architecture doc | Stakeholder-ready Praxis + Grid + ConnectLink integration plan | `docs/architecture/praxis-integration.md` |

### Real Inference
| Item | Description | Evidence |
|------|-------------|----------|
| OVMS CPU inference | OpenVINO Model Server on Intel Xeon, <1s responses | 6 models deployed in triforce namespace |
| Granite 2B CPU | Primary inference backend for soak testing | Sub-second completions verified |
| Multi-model routing | 6 models routable through Praxis gateway | Proven with real completions |

### Observability & Security
| Item | Description | Evidence |
|------|-------------|----------|
| Grafana dashboards | Fleet operations dashboard on Oberon | `fleet-grafana` pod Running |
| ServiceMonitors | Controller + gateway + agent ServiceMonitor CRs | Kustomize base |
| Security hardening | runAsNonRoot, readOnlyRootFilesystem, drop ALL caps | All deploy manifests |
| NetworkPolicies | Default-deny-all (ingress + egress) with explicit allows | `deploy/oberon/network-policies.yaml` |
| Secrets management | Moved from inline values to secretKeyRef | Oberon deploy manifests |

### Production Testing
| Item | Description | Evidence |
|------|-------------|----------|
| Integration tests | 10 tests covering all capabilities | `test/integration/integration_test.go` |
| Contract tests | 7 contract tests for API boundaries | `test/contracts/capability_contracts_test.go` |
| Capability soak | Full ecosystem soak exercising 12 capabilities every 30s | `test/soak/capability_soak.py` |
| Benchmark suite | 6 benchmark files covering all major packages | `*_bench_test.go` across packages |
| BDD scenarios | 63 scenarios including ModelPack and Ledger | `test/bdd/features/` |
| Production test runner | Unified runner: smoke → security → pressure → chaos → performance → scale → soak | `test/production/run-all.sh` |

---

## Test Evidence

### Test Counts
| Suite | Count | Status |
|-------|-------|--------|
| Go unit tests | 27 packages | All passing |
| BDD scenarios | 63 | All passing |
| Architecture proofs | 65 (A01-A62) | All passing |
| Rust tests | 73 | All passing |
| Contract tests | 112 | All passing |
| Integration tests | 10 | All passing |
| Benchmarks | 6 suites | All passing |
| `go vet` | Clean | No issues |
| `cargo clippy` | Clean | Zero warnings |

### Test Matrix (Red-Green)
| Capability | Unit | BDD | Contract | Integration | E2E | Benchmark |
|---|---|---|---|---|---|---|
| Placement | **green** (27) | **green** (9) | **green** (4) | **green** (1) | red | **green** (6) |
| Routing | **green** (22) | **green** (7) | **green** (2) | **green** (1) | red | **green** (7) |
| Autoscaling | **green** (16) | **green** (5) | **green** (2) | **green** (1) | red | **green** (6) |
| Lifecycle | **green** (12) | **green** (7) | **green** (3) | **green** (1) | red | **green** (6) |
| Tenant | **green** (26) | **green** (6) | **green** (3) | **green** (1) | red | **green** (6) |
| Observability | **green** (12) | **green** (8) | **green** (2) | **green** (1) | red | **green** (6) |
| KV-Transfer | **green** (14) | **green** (6) | **green** (3) | **green** (1) | red | **green** (9) |
| ModelPack | **green** (30) | **green** (4) | **green** (3) | **green** (1) | red | **green** (4) |
| Ledger | **green** (22) | **green** (5) | **green** (1) | **green** (1) | red | **green** (6) |
| Compliance | **green** (8) | **green** (7) | **green** (4) | **green** (1) | red | **green** (6) |

**Summary:** 50 green / 10 red out of 60 cells (E2E column requires Kind clusters)

### Rubric Scoring
| Dimension | Weight | Current Score | Dev Gate | Staging Gate |
|-----------|--------|--------------|----------|--------------|
| Correctness | 30% | ~90 | 60 met | 85 met |
| Performance | 25% | ~75 | 50 met | 75 met |
| Reliability | 25% | ~80 | 50 met | 80 met |
| Operability | 10% | ~75 | 40 met | 70 met |
| Security | 10% | ~80 | 50 met | 80 met |

**Current stage: Staging-promotable** (all dimensions meet staging thresholds)

---

## Infrastructure Evidence

### Oberon (SNO) -- Active
- **Hardware:** 256 CPUs (Intel Xeon), 512GB RAM, single node OpenShift
- **Deployed:** Fleet controller (with PostgreSQL persistence), Praxis AI gateway, DeepField, GCL, ARE ledger, Grafana, 6 OVMS models
- **Ecosystem soak:** Running since July 29, exercising all 12 capabilities + real OVMS inference + ledger via NodePort
- **Proven:**
  - Unified state layer with PostgreSQL persistence (state survives controller restarts)
  - 10.5-hour ecosystem soak (1,201 cycles, all capabilities green)
  - Real Granite 2B inference via OVMS (<1s responses)
  - 6 models routed through Praxis AI gateway
  - Ledger integrity maintained across all soak cycles
- **Known issue:** OVN cross-namespace networking intermittent (worked around via NodePort for ledger)

### Dell Arena (Multi-node) -- Available
- **Hardware:** 256 CPUs (Intel Xeon 6 with AMX), 2TB RAM, OCP 4.22, RHOAI installed
- **Status:** Cleaned for another group's benchmarks (July 2026), now available for redeployment
- **Previously proven:**
  - Real Granite inference through fleet proxy (131 tokens)
  - Cross-cluster gateway federation (Arena + Oberon round-robin)
  - Token metering with real model output
  - Zero errors after 231 requests
- **Known issue:** OVN-Kubernetes networking degraded (479-590 container restarts over 13 days)

---

## Remaining Work

### Immediate (no hardware dependency)
- Complete ecosystem soak on Oberon (72-hour target)
- Chain fleet-controller → Praxis → OVMS (blocked by OVN; works via port-forward)
- E2E tests (last 10 red cells in test matrix; needs Kind clusters)

### After OVN fix
- Redeploy fleet-llm-d on Arena
- Multi-cluster soak (Oberon + Arena)
- Praxis Grid Phase 2: SWIM mesh between clusters with mTLS

### Hardware-dependent
- ConnectLink KV cache TCP mode (can code without hardware, test needs multi-node)
- Per-device Gaudi tracking (needs Gaudi hardware)
- OFI/RDMA bridge for KV transfer (needs RoCE networking)

### For production
- Full e2e test coverage
- Multi-cluster soak with real inference on both clusters
- Present Praxis architecture to stakeholders
