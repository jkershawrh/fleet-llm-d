# Governed AI Inference Fleet Platform: Ecosystem Overview

## What This Is

A 4-system platform for managing AI inference fleets at scale with predictive intelligence, governed autonomy, fleet orchestration, and tamper-evident accountability. Built on Intel Xeon CPU inference, Red Hat OpenShift, and the open-source [llm-d](https://github.com/llm-d) project.

```
deepfield-fleet  ->  governed-cognitive-loop  ->  fleet-llm-d  ->  are-immutable-ledger
  (Observe)              (Govern)                  (Act)              (Prove)
```

Each system owns one concern. No system bypasses its neighbors.

---

## Open-Source Foundation

This platform is built on and integrates with open-source projects across the Red Hat, Intel, and CNCF ecosystems:

### Red Hat / llm-d

| Project | Role in Platform | Link |
|---|---|---|
| **llm-d** | Single-cluster inference scheduling and gateway for Kubernetes. The upstream foundation that fleet-llm-d extends to multi-cluster. | [github.com/llm-d](https://github.com/llm-d) |
| **fleet-llm-d** | Fleet-level multi-cluster orchestration: placement, routing, autoscaling, lifecycle, tenant governance, KV-cache transfer. | [github.com/llm-d/fleet-llm-d](https://github.com/llm-d/fleet-llm-d) |
| **ModelPlane** | Model lifecycle state management. fleet-llm-d's ComplianceBridge watches ModelPlane for compliance-driven placement updates. | Integrated in fleet-llm-d |
| **ModelPack** | OCI-based model metadata resolution (CNCF model-spec). Resolves GPU requirements, precision, and format for placement decisions. | [github.com/cncf/model-spec](https://github.com/cncf/model-spec) |
| **vLLM Semantic Router** | Prompt classification and tier-based routing. Classifies prompts as simple/standard/complex and routes to the appropriate inference backend. Deployed as an ExtProc filter. | Integrated in fleet-llm-d |
| **Red Hat OpenShift** | Container orchestration platform. CRD-driven fleet management, RBAC, network policies, HPA, routes. | [openshift.com](https://www.redhat.com/en/technologies/cloud-computing/openshift) |

### Intel

| Project | Role in Platform | Link |
|---|---|---|
| **OpenVINO Model Server (OVMS)** | High-performance C++ inference serving on Intel hardware. INT8 quantization with AMX instruction acceleration. $0.60/hr on Xeon 6 vs $32/hr on H100. | [github.com/openvinotoolkit/model_server](https://github.com/openvinotoolkit/model_server) |
| **Intel Xeon 6 (Granite Rapids)** | CPU inference hardware. 256 cores, AMX instructions for accelerated matrix operations. Enables GPU-free inference for simple and standard workloads. | [intel.com/xeon](https://www.intel.com/content/www/us/en/products/details/processors/xeon.html) |

### Inference Runtimes

| Project | Role in Platform | Link |
|---|---|---|
| **vLLM** | GPU inference serving runtime. FP16 precision, continuous batching, PagedAttention. Used for complex prompts and large models. | [github.com/vllm-project/vllm](https://github.com/vllm-project/vllm) |
| **OVMS** | CPU inference serving runtime. INT8 precision, AMX acceleration. Used for simple/standard prompts on Intel Xeon. | [github.com/openvinotoolkit/model_server](https://github.com/openvinotoolkit/model_server) |

### Governance and Policy

| Project | Role in Platform | Link |
|---|---|---|
| **Open Policy Agent (OPA)** | Runtime honesty boundary enforcement. Guardian sidecar runs Rego policies that block action fields from LLM responses. | [github.com/open-policy-agent/opa](https://github.com/open-policy-agent/opa) |
| **CloudEvents** | Interoperability standard for event-driven communication between all 4 systems. deepfield publishes advisory CloudEvents; GCL emits signed DecisionPackage CloudEvents. | [cloudevents.io](https://cloudevents.io) |

### Platform Projects

| Project | Role in Platform | Link |
|---|---|---|
| **deepfield-fleet** | Predictive signal intelligence. Three-tier classification cascade, SLO forecasting, blast radius scoping, advisory CloudEvents producer. | Red Hat |
| **governed-cognitive-loop (GCL)** | Governed autonomy layer. Constraint classification, horizon prediction, falsification gate, signed DecisionPackages, post-commit accountability. | Red Hat |
| **ARE Immutable Ledger** | Standalone tamper-evident evidence infrastructure. Hash-chained proof receipts for every governance decision and fleet operation. gRPC canonical, REST gateway for compatibility. | Red Hat |
| **Agent Promotion Pipeline** | Non-authoritative compatibility metadata source. Provides optional proposer-ceiling provenance for GCL DecisionPackages. Attestation is explicitly non-authoritative. | Red Hat |

---

## The Four Systems

### 1. deepfield-fleet: Observe

**Role**: Predictive signal intelligence. Observes the fleet, classifies anomalies, forecasts SLO breaches, and publishes advisory evidence. Never executes.

**Capabilities**:
- **Three-tier classification cascade**: Nanoagents (deterministic rules, zero inference cost) filter most signals. Microagents (rule-backed with optional LLM) handle ambiguous cases. Macroagents (LLM-backed) synthesize complex multi-signal patterns.
- **SLO forecasting**: Linear regression on latency time series predicts breach timing (e.g., "SLO breach in 22 minutes at current trajectory").
- **Blast radius scoping**: Assesses how many users, models, and clusters are affected by a predicted breach.
- **Event profiles**: YAML-driven scheduling for known load events (e.g., Summit Connect: 200 users, 5 models, 30-minute pre-warm window).
- **Advisory CloudEvents 1.0**: All output is structured as advisory CloudEvents with `advisory_only: true` enforced at the type level. deepfield never calls fleet-llm-d or the ledger directly.
- **Bootstrap lab**: Self-service onboarding where the system analyzes raw signals, proposes classification agents, and deploys them after human approval.

**Tech**: Python, FastAPI, React 19, Vite, motion. 295 tests.

### 2. governed-cognitive-loop (GCL): Govern

**Role**: Governed autonomy. Receives advisory evidence, synthesizes a governed response through a deterministic pipeline, and emits signed proposals. The LLM interprets objectives only; it never computes the committed action.

**Capabilities**:
- **Constraint classification**: Two-stage classifier (deterministic rules first, LLM only for ambiguous cases). Derives capacity, latency, compliance, and custom constraints from live evidence.
- **Horizon prediction**: Forecasts metric trajectories with confidence envelopes. Spike detection (bimodal peak/baseline ratio). CV-based confidence for stable data.
- **Objective interpretation**: LLM frames the situation as an objective (e.g., "reduce latency to within SLO"). A deterministic controller then computes the action. The LLM never touches action parameters.
- **Deterministic optimization**: Numpy-based controller selects actions: scale, pre-warm, shed load, alert, migrate, rollback. Scale capped at 20 replicas. Custom hard constraints produce no-action.
- **Falsification gate**: 7 deterministic checks challenge every proposed action before it can be emitted (capacity available, scale magnitude reasonable, warmup time realistic, prediction confidence, compliance action valid, shed load bounded, migration target available).
- **Honesty boundary**: OPA Guardian sidecar blocks action fields from LLM responses at runtime. AST-verified separation.
- **Signed DecisionPackages**: Every surviving proposal is HMAC-SHA256 signed, expiry-bounded, scope-bound (tenant + zone), and carries its full evidence chain.
- **Post-commit accountability**: Outcome tracking (did the action work?), 60-second decision cooldown, actuation verification against fleet response.
- **Semantic routing**: Classifies prompts by complexity (simple/standard/complex) and routes to the appropriate inference tier.

**Tech**: Python, FastAPI, Pydantic v2, numpy, React 19, Vite, motion. 822 tests, 33 EDD rubric dimensions, 15 BDD scenarios.

### 3. fleet-llm-d: Act

**Role**: Fleet-level inference orchestration. Owns admission, authorization, operation state, desired/observed state, and actuation. Based on the open-source [llm-d](https://github.com/llm-d) project. Independently decides whether to execute a DecisionPackage.

**Capabilities**:
- **Multi-cluster placement**: Constraint-based solver assigns models to clusters based on hardware affinity (CPU vs GPU), capacity, latency, and compliance requirements.
- **Inference proxy**: OpenAI-compatible gateway with semantic routing, load shedding, and SSE streaming. Routes `model="auto"` requests to the appropriate backend based on prompt classification.
- **Autoscaling**: Metrics-driven fleet optimization with HPA integration. Collector aggregates per-model throughput, latency, and utilization.
- **Lifecycle management**: Canary, blue-green, and rolling update strategies with SLO gates. Rollouts pause automatically if SLO gates fail.
- **Tenant governance**: Quota enforcement, budget tracking, priority-based scheduling, per-tenant metering and chargeback.
- **KV-cache transfer**: Cross-cluster KV-cache migration for session continuity during model placement changes.
- **v1beta1 CRD model**: FleetCluster, FleetInferencePool, FleetIntent, FleetOperation, PlacementPolicy, RoutingPolicy, ScalingPolicy, TenantProfile, ModelLifecycle, KVCacheTransferPolicy.
- **DecisionPackage admission (v2)**: Verifies GCL producer signatures, checks expiry and scope binding, applies fleet-owned authorization policy, then creates a FleetOperation with full lifecycle tracking (RECEIVED -> ACCEPTED -> PLANNED -> AUTHORIZED -> ACTUATING -> SUCCEEDED/FAILED).
- **Platform metrics**: Centralized aggregation from all 4 systems via `GET /api/v1/metrics/platform`.

**Tech**: Go (control plane), Rust (data plane: gateway, agent, KV transfer), Next.js (dashboard), Helm charts. Full CI (Go, Rust, CRD validation, Docker build).

### 4. are-immutable-ledger: Prove

**Role**: Independent tamper-evident evidence infrastructure. Records every governance decision, fleet operation, and compliance event as hash-chained proof receipts. A receipt is not a credential, grant, passport, scope, authorization decision, or evidence that infrastructure changed.

**Capabilities**:
- **Hash-chained entries**: Every entry includes entry_type, content, correlation_id, agent_id, source_id, timestamp, and a SHA-256 hash linking to the previous entry.
- **Correlation-based reconstruction**: Query any correlation_id to reconstruct the full decision chain (classify -> predict -> interpret -> plan -> falsify -> propose/reject).
- **Chain verification**: Verify integrity of any entry type chain. Detect tampering or gaps.
- **Proof receipts**: External REST gateway supports `input_hash` for content-addressed verification. gRPC service is canonical.
- **Multi-source evidence**: Records from GCL (governance decisions), fleet-llm-d (placement, scaling, routing, lifecycle, tenant operations), and deepfield-fleet (observations, classifications).
- **Fleet ecosystem contract**: Formal integration contract defines entry types, correlation semantics, and the trust boundary ("correlation establishes a reconstructable timeline; it does not imply that one entry authorized another").

**Tech**: Rust (core library + gRPC service), Python (REST gateway), PostgreSQL (persistent storage). 4 Rust conformance tests, Python gateway contract tests.

---

## Ecosystem Overhead

### Runtime footprint (measured on OpenShift, Oberon cluster)

| System | CPU (actual) | Memory (actual) | Idle |
|---|---|---|---|
| deepfield-fleet | 2m | 42 MB | Yes |
| GCL | 4m | 134 MB | Yes |
| fleet-controller | 1m | 13 MB | Yes |
| ARE ledger gateway | ~1m | ~20 MB | Yes |
| **Total platform** | **~8m cores** | **~210 MB** | |

### Decision latency

| Operation | Local Latency | Oberon (remote) |
|---|---|---|
| deepfield-fleet classification (nano tier) | 5-12ms | -- |
| GCL governance cycle (classify through signed DecisionPackage) | 54-75ms | p50=560ms (includes ~500ms network) |
| fleet-llm-d intent admission | <10ms | p50=2ms |
| Ledger write (proof receipt) | <5ms | -- |
| **Full pipeline (observe -> govern -> act -> prove)** | **~100ms** | -- |

Under sustained load (300 sequential cycles on Oberon): p50=566ms, p95=900ms, 0 errors, 1.2x latency drift. Under pressure (50 concurrent): 0 errors, linear scaling. See the ecosystem stress test results below for full data.

For a system managing fleet-scale inference where actions happen on the order of seconds to minutes, 100ms of local governance overhead is negligible. Remote latency is dominated by network round-trip, not processing.

### Operational footprint

- 4 independent services, each with its own repo, CI, container image, and deployment
- Total idle: ~210 MB memory, ~8m CPU cores
- Each system deploys, scales, and updates independently
- No shared databases between systems (ledger has its own PostgreSQL)

---

## Heterogeneous Inference

CPU and GPU inference managed side by side:

| Hardware | Runtime | Precision | Cost | Use Case |
|---|---|---|---|---|
| Intel Xeon 6 (AMX) | OVMS C++ | INT8 | $0.60/hr | Simple/standard prompts, sovereign workloads |
| NVIDIA H100 80GB | vLLM | FP16 | $32.00/hr | Complex prompts, large models |
| NVIDIA A100 40GB | vLLM | FP16 | $12.00/hr | Medium complexity, cost-balanced |

Semantic routing classifies prompts by complexity and routes to the appropriate tier. 53x cost reduction for eligible workloads.

---

## Test Coverage

| System | Tests | Methodology |
|---|---|---|
| deepfield-fleet | 295 passed | Unit, integration, ecosystem contract, BDD scenarios |
| GCL | 822 passed | Unit, 33 EDD rubric, 15 BDD scenarios, 7 failure mode benchmarks |
| fleet-llm-d | 462 passed (436 Go + 26 Python) | Unit, contract, CRD validation, architecture, OpenAPI conformance |
| ARE ledger | 40 passed (38 Rust + 2 Python) | Chain integrity, concurrent writes, delegation, gRPC contract, gateway contract |
| **Cross-system** | **42/48 passed** | **8-phase ecosystem stress test on Oberon (smoke, perf, pressure, edge, degradation, soak, pen, chaos)** |

### Ecosystem Stress Test Results (Oberon, July 2026)

An 8-phase stress test exercised all 4 systems on the Oberon cluster. GCL ran as a single pod on OpenShift. Fleet controller ran locally against the same harness.

| Phase | Result | Highlights |
|---|---|---|
| 1. Smoke | 5/6 | All core endpoints healthy |
| 2. Performance Baseline | 1/2 | GCL p50=560ms (includes network round-trip), fleet p50=2ms |
| 3. Pressure | 7/7 | Zero errors at 50 concurrent governance cycles |
| 4. Edge Cases | 7/9 | Evidence poisoning, falsification bypass, cooldown all correct |
| 5. Degradation | 10/10 | GCL operates correctly when fleet unreachable, all 6 scenarios degrade gracefully |
| 6. Soak | 6/6 | 300 cycles with 0 errors, 1.2x latency drift, 479 mixed requests at 0.0% error rate |
| 7. Pen Testing | 5/5 | No injection or traversal vulnerabilities |
| 8. Chaos | 1/3 | 200 simultaneous cycles with 0 errors; single-pod ceiling reached at saturation |

See [ecosystem stress test benchmarks](docs/benchmarks/ecosystem-stress-benchmarks.md) for the full breakdown.

### Production-Emulation Soak (On-Cluster, 2 Hours)

A 2-hour production-emulation soak ran on-cluster on Oberon (pod-to-pod, no external network). The soak driver ran as a Kubernetes Job inside the `fleet-llm-d` namespace, exercising the full decision pipeline across all 6 governance scenarios with 7 degradation injections.

| Metric | Value |
|---|---|
| Duration | 120 minutes |
| Total governance cycles | 2,240 |
| Success rate | **100.0%** |
| E2E latency p50 | **147ms** |
| E2E latency p95 | 504ms |
| Chain integrity | **23/23 verifications passed** |
| Degradation injections | **7/7 passed** (burst, invalid intents, state resets, expired events) |
| Health availability | GCL 100%, Fleet 100% |
| Max injection recovery | 5.6s |

All 5 SLO gates passed. Governance cycle latency held flat at 135-155ms across the full 2 hours with no drift. The full pipeline (observe -> govern -> act -> prove) operates at production-grade reliability under sustained load with active fault injection.

---

## Where This Can Go

### Near-term: production-ready inference governance

- Live end-to-end DecisionPackage flow on production workloads
- Multi-model cost-aware routing (factor $/token into tier selection)
- KV-cache-aware placement (co-locate models sharing prompt prefixes)
- Deeper Intel AMX optimization (INT4, mixed precision scheduling)

### Medium-term: platform-level fleet management

- **Any workload type**: The 4-system separation is workload-agnostic. Replace the inference-specific classifiers and controllers with domain-specific ones to govern databases, microservices, batch jobs, or edge deployments. The falsification gate, DecisionPackage signing, and accountability tracking apply to any consequential fleet action.
- **Federated governance**: Hub-spoke model where a central GCL governs multiple fleet-llm-d instances across regions, each with its own authorization policy and ledger.
- **Learning loops**: Closed-loop feedback where fleet outcomes update deepfield's prediction models and GCL's confidence thresholds. The accountability tracker already captures effectiveness data.
- **Compliance-driven governance**: Financial services, healthcare, and sovereign cloud scenarios where every infrastructure decision must be auditable, explainable, and traceable to evidence.
- **Multi-cloud fleet management**: Extend fleet-llm-d placement across cloud providers with compliance-aware routing (e.g., EU data stays on EU sovereign clusters).

### Long-term: governed autonomy as a pattern

- **Reusable governance framework**: Observe, Govern, Act, Prove is a general architecture for letting AI systems make consequential decisions with accountability, trust boundaries, and tamper-evident proof. Not limited to inference fleets.
- **Ecosystem integration**: Complements NVIDIA Dynamo (scheduling), vLLM (serving), OVMS (CPU serving), CNCF model-spec/ModelPack (packaging), OPA (policy), and CloudEvents (interop) by adding the governed autonomy and accountability layer they do not provide. The platform already integrates all of these; the long-term path is upstream contribution and standardization.
- **Standards track**: DecisionPackage v1 and the CloudEvents ecosystem contract could become a reference implementation for governed fleet operations.

---

## The Core Insight

The overhead of governed fleet management (~100ms latency, ~210 MB memory across 4 systems) is trivial compared to the cost of ungoverned fleet decisions: miscaled inference ($32/hr wasted on idle GPUs), SLO breaches (73% user-facing degradation), compliance violations (unauditable actions), and cascading failures (one bad scaling decision affecting the entire fleet).

The platform makes the governance cost negligible while making the accountability gap zero.
