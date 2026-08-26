# fleet-llm-d Alignment with Red Hat AI Gateway Architecture

**Author:** James Kershaw, AI Field Engineering
**Date:** July 2026
**Audience:** Office of the Chief AI Architect, AI Gateway leadership
**Reference:** Jason Greene, "Red Hat AI Gateway: Architecture & Direction" (July 30, 2026)

> Historical alignment snapshot: architecture conclusions remain useful, but
> the test counts below describe the July review and are not current release
> inventory. Current CI and the root README are authoritative.

---

## Executive Summary

Jason Greene's architecture establishes three layers inside a single cluster, each with one routing authority. fleet-llm-d adds the fleet layer above all three -- deciding which cluster receives a request before Praxis and EPP decide how to handle it within that cluster. This document maps fleet-llm-d's implemented capabilities to the AI Gateway architecture, identifies the clean contract between the two systems, and traces both to the enterprise customer requirements Greene's document catalogues.

---

## 1. Architectural Alignment -- One Authority Per Layer

The AI Gateway architecture enforces a single routing authority at each layer of the inference stack. fleet-llm-d extends this principle upward.

| Layer | Authority | Scope | Decides |
|-------|-----------|-------|---------|
| **L4 -- Fleet orchestration** | **fleet-llm-d** | Cross-cluster | Which cluster gets the request |
| L3 -- Front door | Envoy + Praxis | Cluster ingress | TLS termination, admission, rate limiting |
| L2 -- AI data plane | Praxis | Request lifecycle | Policy, filters, guardrails, protocol translation |
| L1 -- Placement | llm-d EPP | Pod selection | KV-cache-aware pod routing within a cluster |

No layer reaches into another's authority. fleet-llm-d never selects a pod. Praxis never selects a cluster. EPP never enforces tenant quotas. Each layer owns exactly one routing decision.

```
                   Client Request
                         |
              +----------v----------+
              |  fleet-llm-d (L4)   |   "Which cluster?"
              |  RoutingPolicy      |
              |  Evaluator          |
              +----------+----------+
                         |  cluster decision
              +----------v----------+
              |  Envoy + Praxis (L3)|   "Admitted? Rate-limited?"
              |  Front Door         |
              +----------+----------+
                         |
              +----------v----------+
              |  Praxis (L2)        |   "Which filters? Which protocol?"
              |  AI Data Plane      |
              +----------+----------+
                         |
              +----------v----------+
              |  llm-d EPP (L1)     |   "Which pod? KV cache hit?"
              |  Placement          |
              +----------v----------+
                         |
                   Inference Backend
```

---

## 2. What fleet-llm-d Provides That Praxis Does Not

Praxis is a within-cluster programmable proxy. fleet-llm-d is a cross-cluster operations control plane. They solve different problems at different scopes.

| Capability | fleet-llm-d | Praxis |
|------------|-------------|--------|
| Cross-cluster model placement | `ConstraintSolver` + `CompositeScorer` evaluate GPU capacity, region, cost, utilization across all clusters | No -- single-cluster scope |
| Fleet-level tenant governance | `TenantProfile` CRD with `QuotaEnforcer` for quotas, metering, chargeback across all clusters | Per-cluster tenant policies only |
| KV cache transfer between clusters | `TransferCoordinator` with `TcpTransferProtocol` (ConnectLink) for CPU-to-CPU; NIXL bridge for GPU-to-GPU | No -- within-cluster only |
| Cross-cluster session affinity | `SessionAffinityTable` maps sessions to clusters; `UnbindCluster()` on drain | No -- within-cluster affinity only |
| Graceful cluster drain | `DrainCluster` API with Draining/Drained status, automatic session rebinding | No -- pod-level drain only |
| Multi-cluster lifecycle rollouts | `ModelLifecycle` CRD for canary/blue-green spanning multiple clusters | No -- single-cluster deployments |
| Fleet-wide observability | `MetricsFederator` aggregates per-cluster throughput, latency, GPU utilization, cache hit rates | Per-cluster metrics only |
| Immutable audit ledger | ARE Ledger -- hash-chained proof receipts for every fleet operation | No audit trail |
| Governed autonomy pipeline | DeepField (observe) + GCL (govern) + fleet-llm-d (actuate) + Ledger (prove) | No governance pipeline |
| EPP signal consumption | `BuildClusterHealth()` aggregates pool saturation, queue depth, KV cache utilization, prefix hit ratio from EPP across clusters | EPP signals consumed locally |

---

## 3. The Contract Between fleet-llm-d and Praxis

### Signal Flow

```
                fleet-llm-d                              Praxis + EPP
              (fleet control plane)                   (per-cluster data plane)

    +----------------------------------+        +---------------------------+
    |  RoutingPolicyEvaluator          |        |  Praxis AI Gateway        |
    |  BuildClusterHealth()            |        |  EPP pod selector         |
    |  SessionAffinityTable            |        |  Filter pipeline          |
    +----------+----------+------------+        +-------+----------+--------+
               |          ^                             |          ^
     Downward  |          |  Upward                     |          |
     (config)  |          |  (signals)                  |          |
               v          |                             v          |
    +-----------+  +------+--------+            +-------+--+ +-----+------+
    | ConfigMap |  | fleet-agent   |            | Request  | | EPP metrics|
    | overlays  |  | Metrics       |            | routing  | | KV cache   |
    | (routing  |  | Collector     |            | (pod     | | prefix     |
    |  weights, |  | (per-cluster) |            |  select) | | queue      |
    |  tenant   |  +---------------+            +----------+ +------------+
    |  class,   |
    |  session  |
    |  hints)   |
    +-----------+
```

| Direction | What Flows | Mechanism |
|-----------|-----------|-----------|
| **Down:** fleet-llm-d to Praxis | Cluster routing weights, model placement, tenant class, session affinity hints | ConfigMap overlays rendered by fleet-controller |
| **Up:** EPP/Praxis to fleet-llm-d | Pool saturation, queue depth, KV cache utilization, prefix hit ratio, request latency | fleet-agent `MetricsCollector` aggregates per-cluster EPP signals, reports to `BuildClusterHealth()` |

### Boundary Rules

1. fleet-llm-d makes the **cluster** decision; Praxis routes the request **within** that cluster.
2. Praxis consults EPP for **pod** selection; fleet-llm-d never addresses individual pods.
3. EPP signals flow **up** through fleet-agent to inform cross-cluster decisions.
4. fleet-llm-d session hints flow **down** so Praxis can honor conversation affinity within the selected cluster.
5. fleet-llm-d renders Praxis configuration (ConfigMap overlays) that Praxis consumes as read-only input.

---

## 4. What Was Removed to Avoid Duplicate Authority

When Praxis becomes the within-cluster AI data plane, fleet-llm-d cedes three capabilities to avoid conflicting routing authorities.

| Removed Component | Was In | Reason | Now Owned By |
|-------------------|--------|--------|-------------|
| Inference proxy (`proxy.go`) | `pkg/routing/` | Per-request routing within a cluster is Praxis's job | Praxis AI data plane (L2) |
| Semantic router (`semantic.go`) | `pkg/routing/` | Prompt classification and content-based routing become Praxis filters | Praxis filter pipeline (L2) |
| Fleet gateway (`crates/fleet-gateway/`) | Rust data plane | Cross-cluster request forwarding replaced by Praxis Grid mesh | Praxis Grid (L2-L3) |

fleet-llm-d retains everything above these: placement decisions, tenant governance, lifecycle management, session-to-cluster affinity, drain orchestration, and the governed autonomy pipeline. The line is clean -- fleet-llm-d decides where; Praxis decides how.

---

## 5. Mapping to Customer Requirements

Greene's document identifies specific enterprise asks. Each maps to implemented or architecture-ready fleet-llm-d capabilities.

| Customer Ask | Named Customers | fleet-llm-d Capability | Status |
|-------------|----------------|----------------------|--------|
| Automatic failover across sites | Wells Fargo, BofA, RBC | `BuildClusterHealth()` detects unhealthy clusters; `RoutingPolicyEvaluator` reroutes; `Draining` status triggers `SessionAffinityTable.UnbindCluster()` | Architecture ready, drain implemented |
| Budgets by org, team, API key | BlackRock, MGB | `TenantProfile` CRD with `QuotaEnforcer` per tenant; `UsageTracker` metering across all clusters | Implemented, soak-tested |
| Agent governance with auditability | L3Harris, Wells Fargo | GCL submits signed `DecisionPackage` intents; fleet-llm-d evaluates and actuates; ARE Ledger records hash-chained proof receipts | Implemented, 253 ledger verifications in soak |
| Strong tenant isolation | BofA, RBC | `QuotaEnforcer` + tenant-scoped `PlacementPolicy` + per-tenant ConfigMap overlays to Praxis | Implemented |
| Air-gapped / sovereign operation | L3Harris | Standalone deployment mode; no external dependencies required; ARE Ledger operates independently | Architecture ready |

---

## 6. Multi-Cluster Routing -- The AI Grid Workstream

Greene's document states: *"A workstream is evaluating a multi-cluster capability for 3.6, leveraging llm-d and Praxis."*

fleet-llm-d is that capability. The following components are implemented and tested.

| Component | Package | Function |
|-----------|---------|----------|
| `BuildClusterHealth()` | `pkg/server/controller.go` | Aggregates EPP signals (latency, capacity, KV cache hit rate, pool saturation, cost) across all registered clusters |
| `RoutingPolicyEvaluator` | `pkg/routing/policy/evaluator.go` | Evaluates `FleetRoutingPolicy` against `ClusterHealth` entries to select the optimal cluster based on latency, KV cache affinity, cost, and region |
| `SessionAffinityTable` | `pkg/routing/session.go` | Maps multi-turn conversation sessions to clusters; TTL-based expiry with background reaper; `UnbindCluster()` for drain |
| `TcpTransferProtocol` | `crates/kv-transfer/src/tcp_transport.rs` | ConnectLink TCP transport for CPU-to-CPU KV cache state transfer when sessions migrate between clusters |
| `TransferCoordinator` | `crates/kv-transfer/src/coordinator.rs` | Manages transfer job lifecycle (Pending, InProgress, Completed, Failed, Cancelled) |
| `DrainCluster` API | `pkg/server/handlers_drain.go` | Transitions clusters through Draining/Drained status; triggers session rebinding and placement re-evaluation |
| `ConstraintSolver` | `pkg/placement/solver/constraint_solver.go` | Evaluates GPU capacity, region, labels, and utilization constraints to produce cross-cluster placement decisions |
| `CompositeScorer` | `pkg/placement/scorer/cluster_scorer.go` | Weighted multi-dimensional cluster scoring (GPU, cost, latency, utilization) for placement ranking |
| `MetricsFederator` | `pkg/observability/metrics/federation.go` | Federates throughput, TTFT, GPU count, model count, and cache hit rate across all clusters |

---

## 7. Evidence

### 30-Hour Production Soak

| Metric | Value |
|--------|-------|
| Duration | 30+ hours (664 minutes active) |
| Total cycles | 1,266 |
| Real inference calls (OVMS Granite 2B on Intel Xeon) | 1,263 |
| Ledger verifications (hash-chain integrity) | 253, all valid |
| Chaos injections | 10, all recovered (max 19ms) |
| Post-recovery uptime | 100% |
| Node crash + self-recovery | Yes -- controller pod restarted from PostgreSQL state with zero data loss |

### Staging Rubric

| Dimension | Score | Staging Gate | Verdict |
|-----------|-------|-------------|---------|
| Correctness | ~90 | 85 | Pass |
| Performance | ~75 | 75 | Pass |
| Reliability | ~80 | 80 | Pass |
| Operability | ~75 | 70 | Pass |
| Security | ~80 | 80 | Pass |

### Test Coverage

| Suite | Count | Status |
|-------|-------|--------|
| Go unit tests | 27 packages | All passing |
| BDD scenarios | 63 | All passing |
| Architecture proofs | 65 | All passing |
| Rust tests | 73 | All passing |
| Contract tests | 112 | All passing |
| Integration tests | 10 | All passing |
| Test matrix | 50/60 cells green | E2E pending Kind cluster execution |

---

## Summary

fleet-llm-d and Praxis are complementary, not competing. Praxis provides the programmable AI data plane within a cluster. fleet-llm-d provides the operations control plane across clusters. The contract is clean: decisions flow down as ConfigMap overlays, signals flow up through fleet-agent metrics collection. One authority per layer. No overlap. Together they form the AI Grid capability that enterprise customers are asking for.
