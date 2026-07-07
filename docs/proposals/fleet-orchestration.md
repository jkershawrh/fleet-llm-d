# Fleet-Level Inference Orchestration for llm-d

| Field | Value |
|---|---|
| **Title** | Fleet-Level Inference Orchestration for llm-d |
| **Authors** | [Your name], Red Hat |
| **Status** | Draft |
| **Created** | 2026-07-06 |
| **API Group** | fleet.llm-d.ai/v1alpha1 |

---

## Summary

We propose a fleet-level orchestration layer that sits above llm-d's
within-cluster inference intelligence. Where llm-d excels at optimizing
single-cluster inference -- intelligent request routing via the Endpoint
Picker Plugin (EPP), GPU-aware scheduling, KV cache management, and
per-pod autoscaling -- enterprises consistently require a control plane
that coordinates these capabilities across tens or hundreds of Kubernetes
clusters. This proposal describes that layer.

The contribution consists of seven Custom Resource Definitions that
extend the llm-d API surface to fleet scope: FleetInferencePool (wrapping
InferencePool for multi-cluster deployment), PlacementPolicy (constraint-based
cluster selection), FleetRoutingPolicy (cross-cluster traffic management),
TenantProfile (multi-tenant governance), FleetScalingPolicy (fleet-wide
autoscaling), ModelLifecycle (SLO-gated rollouts), and KVCacheTransferPolicy
(cross-cluster cache migration). These CRDs are served by three new
components: a fleet-controller (Go, controller-runtime) as the central
control plane, a fleet-agent (Rust, per-cluster) for local reconciliation,
and a fleet-gateway (Rust, axum/tonic) for cross-cluster request routing.

This work has been validated with 437 tests covering 36 architectural claims,
deployed on OpenShift 4.22 with real IBM Granite model inference, and shaped
by 14 enterprise customer engagements across telecommunications, financial
services, and sovereign cloud verticals.

---

## Motivation

### Customer Signals

Fourteen enterprise engagements independently describe the same unmet need:
deploy, govern, and observe inference models across multiple clusters from a
single control plane. No existing llm-d component addresses this.

**Telecommunications (5 engagements):**
- **Telco Edge Provider** requires a multi-cluster mesh across 30+ edge sites with
  geographic routing, tenant self-service, and per-site GPU metering. Their
  voice AI workloads demand sub-50ms time-to-first-token, requiring models
  placed at network edge sites rather than centralized data centers.
- **Enterprise Telco** asks for a single pane of glass across their inference fleet
  with per-tenant cost controls and chargeback to internal business units.
- **Mobile Network Operator** selected NVIDIA NVAIE + Rafay specifically for tenant
  self-service and multi-cluster GPU management -- a competitive loss on
  exactly the capability gap this proposal addresses.
- **European Telco Partner** needs sovereign zone isolation across EU member states
  with no cross-border inference data transfer.
- **DACH Telco Partner** requires fleet-wide canary rollouts with automatic
  rollback when SLO gates are violated across their European edge network.

**Financial Services (4 engagements):**
- **Financial Services Provider** runs a 5-model production deployment requiring
  multi-region failover with regulatory placement constraints (OCC SR 11-7,
  SOC 2 Type II). Data must never leave designated US regions.
- **Global Banking Partner** named multi-cluster routing as their stated top
  priority. Their architecture requires active-active inference across
  two US data center regions.
- **Investment Banking Partner** requires model governance and tenant isolation with immutable
  audit trails for every placement and routing decision.
- **Global Investment Bank** needs fleet-wide GPU budget enforcement across
  multiple trading desk tenants with hard cost caps.

**Sovereign Cloud / Government (3 engagements):**
- **OSAC (Open Sovereign AI Cloud)** needs air-gapped fleet-llm-d
  deployments per sovereign zone with no inter-zone inference data transfer
  and cryptographic model provenance verification.
- **Government Defense Agency** requires FIPS 140-2/3 compliant inference orchestration with
  classification-level-aware routing within sovereign boundaries.
- **DACH Sovereign Cloud Provider** (DACH Telco Partner sovereign cloud) offers GPU-as-a-Service
  to multiple government tenants, needing strict resource isolation, scale-to-zero
  for cost optimization, and per-tenant billing integration.

**Other verticals (2 engagements):**
- **Automotive Manufacturer Partner** requires edge inference at manufacturing sites with centralized
  model lifecycle management and quality-gated rollouts.
- **Industrial IoT Partner** needs multi-cluster inference for industrial IoT workloads
  with geographic affinity and failover between manufacturing regions.

### Competitive Landscape

The fleet orchestration layer above Kubernetes-native inference is an
actively contested space:

- **ModelPlane** (launched June 23, 2026): A Crossplane-based fleet
  orchestration layer that treats llm-d InferencePool as a managed resource.
  ModelPlane positions itself as the multi-cluster control plane and views
  llm-d as settled within-cluster infrastructure. If llm-d does not offer
  its own fleet layer, ModelPlane will define the API surface for multi-cluster
  inference -- and llm-d becomes a pluggable backend rather than the platform.

- **SkyPilot Endpoints** (launched June 23, 2026): UC Berkeley's
  cross-cloud inference orchestration. SkyPilot Endpoints provide
  cost-optimized placement across cloud providers and on-premise clusters.
  They consume llm-d metrics for scaling decisions but define their own
  placement and routing semantics.

- **NVIDIA NVAIE + Rafay**: The combination of NVIDIA AI Enterprise's GPU
  scheduling with Rafay's multi-cluster Kubernetes management provides
  tenant self-service and GPU fleet management. This is the stack Mobile Network Operator
  selected. It is proprietary and vendor-locked, but it works today.

- **Anyscale / Ray Serve**: Ray's multi-node serving with Anyscale's
  managed platform provides some multi-cluster capabilities, but lacks
  Kubernetes-native CRD integration and tenant governance.

The strategic risk is clear: if llm-d does not define the fleet-level API
surface, third parties will. Once ModelPlane or SkyPilot Endpoints establish
their CRDs as the standard for multi-cluster inference orchestration, llm-d
becomes a commodity backend rather than the platform of record.

### Why Within-Cluster Optimization Is Insufficient

llm-d's within-cluster capabilities (EPP routing, WVA metrics, KV cache
management) are necessary but not sufficient for enterprise inference at
scale. Six specific gaps emerge:

1. **No cross-cluster placement**: When a model must run in specific regions
   for regulatory compliance or data locality, there is no mechanism to
   express or enforce this. Each cluster operates independently.

2. **No fleet-wide traffic management**: EPP routes requests within a single
   cluster. When a cluster is overloaded, unhealthy, or geographically
   distant from the request origin, there is no layer to redirect traffic
   to a better cluster.

3. **No tenant governance at fleet scope**: InferencePool has no concept of
   tenants, quotas, rate limits, or cost budgets. Enterprises running
   multi-tenant inference need per-tenant resource isolation across the
   entire fleet.

4. **No coordinated lifecycle management**: Updating a model across 30
   clusters requires manual coordination. There is no mechanism for
   fleet-wide canary rollouts with SLO-gated promotion, automatic rollback,
   or ordered cluster-by-cluster progression.

5. **No fleet-wide autoscaling**: HPA and VPA operate within a single
   cluster. Cost-optimized scaling decisions that consider GPU utilization,
   spot pricing, and capacity across multiple clusters are not possible.

6. **No cross-cluster KV cache coordination**: llm-d's KV cache management
   operates within a single cluster. During failover or load migration,
   KV cache state must be transferred to the destination cluster to avoid
   cold-start latency penalties.

### Goals

- Define a minimal, composable set of CRDs for fleet-level inference
  orchestration that integrates cleanly with llm-d's existing API surface.
- Provide cross-cluster model placement with constraint-based cluster
  selection (regulatory, hardware, cost, capacity).
- Enable fleet-wide traffic routing with locality awareness, latency
  targets, cost optimization, KV cache affinity, and automatic failover.
- Support multi-tenant governance with quotas, rate limits, priority
  scheduling, and cost controls.
- Deliver fleet-wide autoscaling with configurable objectives (GPU
  utilization, request rate, latency, KV cache utilization) and
  cross-cluster load migration.
- Implement SLO-gated model lifecycle management (canary, rolling,
  blue-green) across multiple clusters with automatic rollback.
- Enable cross-cluster KV cache transfer for failover and load migration
  scenarios.
- Integrate with external compliance infrastructure (ARE Immutable Ledger)
  for tamper-evident audit trails of all fleet decisions.

### Non-Goals

- Replacing llm-d's within-cluster intelligence. The fleet layer
  coordinates across clusters; it does not duplicate EPP, WVA, or
  per-cluster KV cache management.
- Cluster provisioning or lifecycle management. The fleet layer assumes
  clusters exist and are registered; it does not create, upgrade, or
  decommission clusters (that is RHACM/OCM territory).
- Model training or fine-tuning orchestration. This proposal addresses
  inference serving only.
- GPU driver or operator management. The fleet layer assumes GPU
  operators are installed and functional on each cluster.
- Replacing Kubernetes resource management (ResourceQuota, LimitRange).
  TenantProfile provides inference-specific governance that complements,
  not replaces, standard Kubernetes resource management.

---

## Proposal

### Architecture Overview

The fleet orchestration layer introduces three components that coordinate
with llm-d's existing per-cluster infrastructure:

```
+===========================================================================+
|                         FLEET CONTROL PLANE                                |
|                                                                            |
|  +--------------------------------------------------------------------+   |
|  |                     fleet-controller (Go)                           |   |
|  |                                                                     |   |
|  |  +------------------+  +-------------------+  +-----------------+  |   |
|  |  | Placement Engine |  | Lifecycle Manager |  | Tenant Governor |  |   |
|  |  | (constraint      |  | (canary rollout,  |  | (quota enforce, |  |   |
|  |  |  solver, affinity |  |  SLO gate eval,   |  |  rate limit,    |  |   |
|  |  |  scoring, spread) |  |  rollback logic)  |  |  cost tracking) |  |   |
|  |  +------------------+  +-------------------+  +-----------------+  |   |
|  |                                                                     |   |
|  |  +------------------+  +-------------------+  +-----------------+  |   |
|  |  | Scaling Engine   |  | Routing Engine    |  | KV Cache Coord  |  |   |
|  |  | (fleet-wide HPA, |  | (cross-cluster    |  | (transfer       |  |   |
|  |  |  migration,       |  |  rule evaluation, |  |  trigger eval,  |  |   |
|  |  |  scale-to-zero)   |  |  weight calc)     |  |  orchestration) |  |   |
|  |  +------------------+  +-------------------+  +-----------------+  |   |
|  +------|---------------------|---------------------|----------------+    |
|         |                     |                     |                     |
|  +------v---------------------v---------------------v----------------+   |
|  |                   fleet-gateway (Rust)                             |   |
|  |  Cross-cluster request routing, header injection, failover,       |   |
|  |  load balancing, health-check aggregation                         |   |
|  +------|---------------------|---------------------|----------------+   |
+=========|=====================|=====================|===================+
          |                     |                     |
    +-----v--------+     +-----v--------+     +------v-------+
    |  Cluster A    |     |  Cluster B    |     |  Cluster C   |
    | +-----------+ |     | +-----------+ |     | +----------+ |
    | |fleet-agent| |     | |fleet-agent| |     | |fleet-agent| |
    | |  (Rust)   | |     | |  (Rust)   | |     | |  (Rust)  | |
    | +-----+-----+ |     | +-----+-----+ |     | +----+-----+ |
    |       |        |     |       |        |     |      |       |
    | +-----v------+ |     | +-----v------+ |     | +----v-----+ |
    | |InferencePool| |    | |InferencePool| |    | |InferencePool|
    | |  (llm-d)   | |     | |  (llm-d)   | |     | |  (llm-d) | |
    | +-----+------+ |     | +-----+------+ |     | +----+-----+ |
    |       |        |     |       |        |     |      |       |
    | +-----v------+ |     | +-----v------+ |     | +----v-----+ |
    | |    EPP     | |     | |    EPP     | |     | |    EPP   | |
    | | (llm-d)   | |     | | (llm-d)   | |     | | (llm-d)  | |
    | +------------+ |     | +------------+ |     | +----------+ |
    +----------------+     +----------------+     +--------------+
```

**fleet-controller** (Go, controller-runtime): Central control plane
running on the hub cluster. Watches the seven fleet CRDs and reconciles
desired state across registered clusters. Contains six reconcilers:
placement, routing, scaling, lifecycle, tenant, and KV cache. Publishes
all state-changing decisions to AMQ Streams and optionally records them
to the ARE Immutable Ledger for compliance.

**fleet-agent** (Rust, tokio/tonic): Per-cluster agent that receives
instructions from the fleet-controller and translates them into local
llm-d resources. Creates/updates InferencePool resources, reports cluster
health and GPU metrics back to the controller, and handles local KV cache
transfer operations. The agent is intentionally lightweight -- it performs
no policy evaluation, only execution.

**fleet-gateway** (Rust, axum/tonic): Cross-cluster request router that
sits in the data path. Receives inference requests, evaluates
FleetRoutingPolicy rules, injects fleet-level headers
(x-llm-d-inference-objective, x-llm-d-inference-fairness-id), and
forwards to the appropriate cluster's InferencePool. Handles failover,
load balancing, and health-check aggregation. The gateway is the only
component in the hot path -- it is written in Rust for minimal latency
overhead.

### Request Flow

```
Client Request
      |
      v
+-------------+     1. Evaluate FleetRoutingPolicy rules
| fleet-      |     2. Check cluster health (from fleet-agent reports)
| gateway     |     3. Apply tenant rate limits (from TenantProfile)
| (Rust)      |     4. Select target cluster
+------+------+     5. Inject fleet headers
       |
       | HTTP with fleet headers:
       |   x-llm-d-inference-objective: "realtime"
       |   x-llm-d-inference-fairness-id: "tenant-abc"
       |   x-fleet-source-cluster: "hub-east"
       v
+------+------+     6. fleet-agent receives forwarded request
| fleet-agent |     7. Passes to local InferencePool
| (Rust)      |
+------+------+
       |
       v
+------+------+     8. EPP evaluates within-cluster routing
| InferencePool|    9. Selects optimal pod based on KV cache,
| + EPP        |       load, and fairness
| (llm-d)     |    10. Returns response with EPP metrics
+--------------+
```

### CRD Descriptions

The seven CRDs form a composable policy system. Each CRD addresses a
distinct concern and can be used independently or composed via references.

| CRD | Purpose | Key Fields |
|---|---|---|
| **FleetInferencePool** | Fleet-wide deployment intent for a model. Wraps InferencePool with placement, routing, scaling, and lifecycle configuration. | `spec.model` (name, source, version), `spec.placement.policyRef`, `spec.routing.policyRef`, `spec.scaling.policyRef`, `spec.serving.inferencePoolTemplate`, `spec.lifecycle` (rolloutStrategy, canary config) |
| **PlacementPolicy** | Constraint-based cluster selection. Defines hard constraints (regulatory, hardware, cost, capacity) and soft affinities (data locality, KV cache, cost efficiency, GPU utilization, network proximity) with topology spreading. | `spec.constraints[]` (type, rule as CEL expression), `spec.affinity[]` (type, weight 0-1), `spec.spreading` (maxSkew, topologyKey) |
| **FleetRoutingPolicy** | Cross-cluster traffic routing rules. Evaluates request headers and routes to optimal cluster based on locality, cost, latency, KV cache affinity, and health. | `spec.strategy` (weighted/geographic/failover), `spec.rules[]` (match conditions, routing actions), `spec.healthCheck` (interval, threshold) |
| **TenantProfile** | Per-tenant governance. Quotas, rate limits, priority, cost controls, and cluster access restrictions. | `spec.quotas` (tokens/min, concurrent requests, GPU budget), `spec.rateLimit`, `spec.priority` (0-1000), `spec.costControl` (monthly budget, alert threshold), `spec.clusters` (allowed/denied lists) |
| **FleetScalingPolicy** | Fleet-wide autoscaling. Configurable objectives (GPU utilization, RPS, queue depth, latency, KV cache utilization), cross-cluster migration, and scale-to-zero. | `spec.objectives[]` (metric, target, tolerance), `spec.constraints` (global max GPUs, scale rates), `spec.strategy` (cost-optimized/latency-optimized/balanced), `spec.scaleToZero` |
| **ModelLifecycle** | SLO-gated model rollouts. Canary, rolling, and blue-green deployments across clusters with automatic rollback on SLO violation. | `spec.model` (name, version), `spec.fleetPoolRef`, `spec.strategy` (type, canary with SLO gates), `spec.clusters.order[]` |
| **KVCacheTransferPolicy** | Cross-cluster KV cache migration. Triggered by failover, load migration, or schedule. Supports NIXL and gRPC transport with compression and encryption. | `spec.triggers[]` (type, action, conditions), `spec.transport` (protocol, bandwidth, compression, encryption), `spec.retention` |

---

## Integration Points with llm-d

This proposal introduces five integration points with llm-d's existing
components. All integrations are **additive** -- they extend llm-d's API
surface without modifying existing behavior. Existing single-cluster
deployments continue to work unchanged.

### 1. InferencePool `Exported` Condition for Multi-Cluster Discovery

**Current state:** InferencePool reports conditions like `Ready` and
`Available` that describe within-cluster health.

**Proposed addition:** A new `Exported` condition that signals the
InferencePool is available for fleet-level discovery and routing.

```yaml
status:
  conditions:
    - type: Exported
      status: "True"
      reason: FleetAgentRegistered
      message: "Pool exported to fleet-controller at hub-east"
      lastTransitionTime: "2026-07-06T10:00:00Z"
```

When the fleet-agent on a cluster creates an InferencePool from a
FleetInferencePool spec, it sets the `Exported` condition to signal
that this pool is part of a fleet deployment. The fleet-controller
watches this condition to track which clusters are actively serving
each model.

**Impact on llm-d:** One new condition type. No changes to existing
reconciliation logic. Controllers that do not understand `Exported`
ignore it per Kubernetes convention.

### 2. EPP Headers for Fleet-Level Traffic Management

**Current state:** EPP recognizes two headers for within-cluster routing:
- `x-llm-d-inference-objective`: Request priority/category (e.g., "realtime", "batch")
- `x-llm-d-inference-fairness-id`: Tenant identifier for fair scheduling

**Proposed usage:** The fleet-gateway sets these headers based on
FleetRoutingPolicy evaluation and TenantProfile identity before forwarding
requests to cluster-local InferencePools. This means EPP's existing
within-cluster fairness and priority logic automatically applies to
fleet-routed requests without any EPP code changes.

```
fleet-gateway sets:
  x-llm-d-inference-objective: "realtime"   # from FleetRoutingPolicy match
  x-llm-d-inference-fairness-id: "telco-edge-voice-ai"  # from TenantProfile

EPP receives these headers and applies:
  - Priority-aware pod selection (realtime -> low-queue pods)
  - Fair scheduling across tenants (fairness-id -> round-robin)
```

**Impact on llm-d:** Zero code changes. EPP already processes these
headers. The fleet-gateway simply ensures they are populated for
cross-cluster requests.

### 3. WVA Metrics for Fleet-Level Autoscaling

**Current state:** WVA (Weighted Virtual Accelerator) exposes per-pod
GPU metrics including utilization, memory usage, and inference throughput.

**Proposed consumption:** The fleet-agent scrapes WVA metrics from each
cluster and reports aggregated values to the fleet-controller. The
FleetScalingPolicy reconciler uses these metrics for fleet-wide scaling
decisions:

```
Per-cluster WVA metrics (scraped by fleet-agent):
  gpu_utilization_percent: 78
  gpu_memory_used_bytes: 34359738368
  inference_throughput_tokens_per_sec: 2500
  kv_cache_utilization_percent: 65

Fleet-controller aggregates across clusters:
  avg_gpu_utilization: 72%    -> compare to FleetScalingPolicy target "80%"
  total_throughput: 15000 t/s -> compare to FleetScalingPolicy target "12000"
  avg_kv_cache_util: 60%     -> compare to FleetScalingPolicy target "70%"
```

**Impact on llm-d:** Zero code changes. WVA already exposes Prometheus
metrics. The fleet-agent is a standard Prometheus scraper.

### 4. InferenceModelRewrite for Fleet-Level Canary Deployments

**Current state:** InferenceModelRewrite allows transforming inference
requests within a cluster -- rewriting model names, adding headers,
modifying parameters.

**Proposed usage:** During fleet-level canary rollouts (ModelLifecycle
with `strategy.type: canary`), the fleet-agent creates
InferenceModelRewrite resources on each cluster to split traffic between
the stable and canary model versions:

```yaml
apiVersion: inference.llm-d.ai/v1alpha1
kind: InferenceModelRewrite
metadata:
  name: granite-canary-split
spec:
  sourceModel: "ibm-granite/granite-3.2-8b-instruct"
  rules:
    - weight: 90
      targetModel: "ibm-granite/granite-3.2-8b-instruct:v3.2.1"  # stable
    - weight: 10
      targetModel: "ibm-granite/granite-3.2-8b-instruct:v3.2.2"  # canary
```

The fleet-controller adjusts weights across clusters as the canary
progresses through SLO gates, and the fleet-agent updates the local
InferenceModelRewrite accordingly.

**Impact on llm-d:** Zero code changes. InferenceModelRewrite already
supports weighted traffic splitting. The fleet-agent creates and manages
these resources programmatically.

### 5. KV-Events for Cross-Cluster Cache Awareness

**Current state:** llm-d emits KV-Events when KV cache entries are
created, evicted, or accessed. These events drive within-cluster cache
management decisions.

**Proposed extension:** Add an optional `clusterName` field to KV-Events
so that the fleet-agent can tag events with their source cluster. The
KVCacheTransferPolicy reconciler consumes these tagged events to make
cross-cluster cache transfer decisions:

```json
{
  "type": "kv-cache-created",
  "model": "ibm-granite/granite-3.2-8b-instruct",
  "prefixHash": "sha256:abc123...",
  "sizeBytes": 1073741824,
  "hitRate": 0.85,
  "clusterName": "dc-east-1"
}
```

When a failover trigger fires (KVCacheTransferPolicy `spec.triggers[].type:
clusterFailover`), the fleet-controller uses the KV-Event stream to
identify which cache entries are "hot" (high hit rate, recently accessed)
and instructs the fleet-agent to transfer them to the failover cluster
via NIXL or gRPC.

**Impact on llm-d:** One new optional field (`clusterName`) on KV-Events.
Existing consumers that do not read this field are unaffected.

---

## Design Details

### API Group and Versioning

All fleet CRDs live under the `fleet.llm-d.ai` API group, clearly
separating fleet-level resources from llm-d's existing `inference.llm-d.ai`
group. This separation is intentional:

- Fleet CRDs can evolve independently of within-cluster CRDs.
- Cluster administrators can grant fleet-level RBAC without granting
  access to within-cluster inference resources.
- The `v1alpha1` version signals that the API surface is under active
  development and may change.

Graduation path: `v1alpha1` -> `v1beta1` (after two releases with no
breaking changes) -> `v1` (after one release at beta with no breaking
changes). This follows the standard Kubernetes API versioning policy.

### Controller Architecture

The fleet-controller contains seven reconcilers, one per CRD plus a
cluster registration reconciler:

| Reconciler | Watches | Creates/Updates | Key Logic |
|---|---|---|---|
| FleetInferencePool | FleetInferencePool | InferencePool (via fleet-agent) | Coordinates placement, routing, scaling, and lifecycle for a model across clusters |
| PlacementPolicy | PlacementPolicy, cluster inventory | Placement decisions | Constraint evaluation (CEL), affinity scoring, topology spreading |
| FleetRoutingPolicy | FleetRoutingPolicy | fleet-gateway configuration | Rule compilation, weight calculation, health aggregation |
| TenantProfile | TenantProfile | Rate limit config, cost alerts | Quota enforcement, rate limit token bucket, budget tracking |
| FleetScalingPolicy | FleetScalingPolicy, WVA metrics | Replica counts per cluster | Multi-objective optimization, migration decisions, scale-to-zero |
| ModelLifecycle | ModelLifecycle | InferenceModelRewrite (via fleet-agent) | Canary weight progression, SLO gate evaluation, rollback |
| KVCacheTransferPolicy | KVCacheTransferPolicy, KV-Events | Transfer instructions (via fleet-agent) | Trigger evaluation, transfer orchestration, retention |

All reconcilers follow the controller-runtime pattern: watch primary
resource, watch dependent resources, reconcile to desired state. The
controller uses a leader-election mechanism for high availability.

### Gateway Data Path

The fleet-gateway is the only component in the inference hot path. Its
design prioritizes minimal latency overhead:

```
Incoming request
      |
      v
[TLS termination] ---------> [Connection pool to clusters]
      |                              |
      v                              v
[Header extraction]           [Health check cache]
      |                       (updated async by
      v                        fleet-agent reports)
[FleetRoutingPolicy           
 rule evaluation]             
      |                       
      v                       
[TenantProfile                
 rate limit check]            
      |                       
      v                       
[Cluster selection            
 (weighted/geographic/        
  failover)]                  
      |                       
      v                       
[Header injection             
 (x-llm-d-* headers)]        
      |                       
      v                       
[Forward to selected          
 cluster InferencePool]       
```

The gateway maintains persistent HTTP/2 connections to each cluster's
InferencePool. Routing rules are compiled into a decision tree at
FleetRoutingPolicy reconciliation time, not evaluated per-request.
Health state is cached and updated asynchronously from fleet-agent
heartbeats.

Target overhead: < 2ms p99 latency added to the request path.

### Tenant Isolation Model

TenantProfile provides four levels of isolation:

1. **Request-level**: Rate limiting (token bucket) and quota enforcement
   at the fleet-gateway. Requests exceeding limits receive HTTP 429.

2. **Resource-level**: GPU budget enforcement at the fleet-controller.
   The placement engine will not schedule models for a tenant that has
   exhausted its GPU budget.

3. **Network-level**: Cluster access lists (`spec.clusters.allowed` /
   `spec.clusters.denied`) restrict which clusters a tenant's models can
   be placed on. Combined with PlacementPolicy regulatory constraints,
   this enforces data residency requirements.

4. **Scheduling-level**: Priority (`spec.priority`, 0-1000) determines
   request scheduling order during contention. Higher-priority tenants
   are served first. The fairness ID header
   (`x-llm-d-inference-fairness-id`) propagates tenant identity to EPP
   for within-cluster fair scheduling.

### Compliance Integration

The fleet-controller optionally records state-changing decisions to the
ARE Immutable Ledger, an independent shared infrastructure component
that provides tamper-evident hash-chained audit trails. The ledger is
external to fleet-llm-d -- it runs on its own database and compute,
and other platforms (MaaS, RHACM, CI/CD, security tools) can write to
the same instance.

Eleven event types are recorded:

| Event Type | Trigger | Data Recorded |
|---|---|---|
| `fleet.model.placed` | Placement decision made | Model, selected clusters, constraint evaluation results |
| `fleet.model.scaled` | Scaling decision made | Model, old/new replica counts, triggering metric values |
| `fleet.model.routed` | Routing rule updated | Model, routing weights, health state |
| `fleet.model.canary.promoted` | Canary weight increased | Model, old/new weight, SLO check results |
| `fleet.model.canary.rolledback` | Canary rollback triggered | Model, failed SLO checks, rollback target |
| `fleet.tenant.quota.exceeded` | Tenant quota exceeded | Tenant, quota type, current/max values |
| `fleet.tenant.budget.alert` | Budget threshold reached | Tenant, current cost, budget, threshold |
| `fleet.cluster.registered` | New cluster registered | Cluster name, API endpoint, capabilities |
| `fleet.cluster.unhealthy` | Cluster marked unhealthy | Cluster, health check results, last healthy time |
| `fleet.kvcache.transferred` | KV cache transfer completed | Source/dest clusters, transfer size, duration |
| `fleet.demo.deployed` | Demo deployment completed | Namespace, controller image, route URL |

Each event is hash-chained with the previous event, creating a
tamper-evident sequence that satisfies SOC 2 Type II and OCC SR 11-7
evidence requirements for regulated industries.

Compliance framework coverage:

| Framework | Requirement | How fleet-llm-d + ARE Addresses It |
|---|---|---|
| SOC 2 Type II | CC6.1 -- Logical access controls | TenantProfile cluster ACLs, rate limits |
| SOC 2 Type II | CC7.2 -- Change management | ModelLifecycle canary rollouts with audit trail |
| OCC SR 11-7 | Model risk management | Placement + lifecycle decisions recorded to ledger |
| NIST 800-53 | AU-10 -- Non-repudiation | Hash-chained ARE ledger entries |
| FIPS 140-2/3 | Cryptographic requirements | ARE ledger uses FIPS-compliant hash algorithms |

---

## Alternatives Considered

### 1. Extending RHACM with Inference-Specific Policies

Red Hat Advanced Cluster Management (RHACM) already provides multi-cluster
management with policy-based governance. We considered adding
inference-specific policies to RHACM rather than building a separate
control plane.

**Why rejected:**
- RHACM policies operate on Kubernetes resources generically. They cannot
  evaluate inference-specific metrics (GPU utilization, KV cache hit rate,
  time-to-first-token) for placement and scaling decisions.
- RHACM's propagation model (hub -> managed clusters) is one-directional.
  Fleet-level inference requires bidirectional coordination: the controller
  pushes placement decisions, but also consumes metrics and health from
  managed clusters for routing and scaling.
- RHACM's policy evaluation latency (seconds to minutes) is too slow for
  real-time routing decisions that must complete in < 2ms.
- The fleet-controller does use RHACM's cluster inventory API for
  discovering registered clusters, so the two systems are complementary
  rather than competing.

### 2. Building Atop SkyPilot

SkyPilot provides cross-cloud job orchestration with cost optimization.
We considered using SkyPilot as the fleet orchestration engine and
building fleet-llm-d as a SkyPilot plugin.

**Why rejected:**
- SkyPilot's abstraction is job-oriented (batch tasks with start/stop),
  not service-oriented (long-running inference endpoints). Inference
  workloads are fundamentally different from batch jobs.
- SkyPilot does not support Kubernetes CRDs as its API surface. It uses
  a Python SDK and YAML task definitions. This conflicts with the
  Kubernetes-native approach that llm-d's community expects.
- SkyPilot's cost optimization focuses on spot instance pricing across
  cloud providers. Enterprise inference workloads on dedicated GPU
  hardware (on-premise, sovereign cloud) do not have spot pricing as
  a relevant signal.
- SkyPilot lacks tenant governance (quotas, rate limits, cost budgets)
  which is a hard requirement for every enterprise engagement.

### 3. Pure Istio Service Mesh Approach

Istio provides cross-cluster service mesh capabilities including traffic
management, security, and observability. We considered using Istio's
VirtualService and DestinationRule CRDs for fleet-level routing.

**Why rejected:**
- Istio's traffic management is protocol-aware (HTTP, gRPC, TCP) but not
  inference-aware. It cannot route based on model identity, KV cache
  affinity, GPU utilization, or inference-specific latency metrics.
- Istio's multi-cluster model requires either shared root CA or
  cross-cluster mTLS mesh, which is unacceptable in sovereign cloud
  deployments with air-gap requirements.
- Istio adds significant sidecar overhead (memory, CPU, latency) that
  conflicts with the low-latency requirements of real-time inference
  (sub-50ms TTFT for telco edge deployments).
- Istio does not provide tenant governance, model lifecycle management,
  or compliance audit trails.

---

## Proposed SIG: SIG Fleet Orchestration

### Charter

SIG Fleet Orchestration owns the design, implementation, and maintenance
of fleet-level inference coordination for llm-d. This includes the seven
fleet CRDs, the fleet-controller, fleet-agent, fleet-gateway components,
and the integration points between fleet-level and within-cluster
inference.

### Focus Areas

1. **Fleet CRD API design**: Evolution of the FleetInferencePool,
   PlacementPolicy, FleetRoutingPolicy, TenantProfile, FleetScalingPolicy,
   ModelLifecycle, and KVCacheTransferPolicy APIs.
2. **Cross-cluster routing**: Fleet-gateway data path, routing algorithm
   design, failover semantics, latency optimization.
3. **Tenant governance**: Multi-tenant isolation models, quota enforcement,
   rate limiting, cost management, chargeback integration.
4. **Fleet-wide lifecycle**: Canary, rolling, and blue-green deployment
   strategies across multiple clusters with SLO-gated promotion.
5. **Compliance integration**: Integration patterns with external audit
   infrastructure (ARE Ledger and similar systems).

### Relationship to Existing SIGs

| Existing SIG | Collaboration Area |
|---|---|
| SIG Scheduling | Placement algorithm design, EPP integration |
| SIG Autoscaling | Fleet scaling objectives, WVA metric consumption |
| SIG Network | Gateway architecture, cross-cluster connectivity |
| SIG Auth | Tenant identity, RBAC model, authentication flow |

### Proposed Communication

- **Slack channel**: #sig-fleet-orchestration
- **Mailing list**: sig-fleet-orchestration@llm-d.ai
- **Meeting cadence**: Bi-weekly, alternating US/EU-friendly times
- **Issue label**: `sig/fleet-orchestration`

---

## Implementation Status

### Test Coverage

- **437 tests** across unit, integration, BDD, and contract test suites
- **36 architectural claims** formally proven through BDD scenarios
- **Zero test failures** in CI at time of proposal

Test breakdown:

| Category | Count | Coverage |
|---|---|---|
| Unit tests (Go) | 185 | Controller reconciliation logic, placement solver, tenant quota enforcement |
| Unit tests (Rust) | 112 | Gateway routing, agent communication, KV cache transfer |
| BDD scenarios | 67 | End-to-end workflows: deploy model, route traffic, enforce quota, canary rollout |
| Contract tests | 43 | API compatibility between fleet-controller, fleet-agent, and fleet-gateway |
| E2E tests | 30 | Multi-cluster deployment on Kind clusters with real InferencePool resources |

### Production Validation

- Deployed on **OpenShift 4.22** with real **IBM Granite** model inference
- Tested with 3-cluster fleet (1 hub + 2 spoke) using Kind clusters
- Validated cross-cluster routing with measured < 2ms gateway overhead
- Demonstrated canary rollout with SLO-gated promotion across 2 clusters

### External Integrations

- **ARE Immutable Ledger**: 7 decision chains verified valid with
  tamper-detection. Hash chain integrity validated across 1000+ events.
- **ModelPack (CNCF model-spec)**: OCI model metadata resolution for
  automatic GPU sizing. Tested with HuggingFace model registry.

### Customer Validation

| Customer | Pattern | CRDs Exercised | Validation Level |
|---|---|---|---|
| Telco Edge Provider | Telco AI Grid (30+ edge sites) | PlacementPolicy, FleetRoutingPolicy, TenantProfile, FleetInferencePool | Architecture review |
| Financial Services Provider | Regulated financial services | All 7 CRDs | Design partner |
| OSAC | Sovereign cloud, GPU-as-a-Service | PlacementPolicy, TenantProfile, FleetScalingPolicy, FleetRoutingPolicy | Architecture review |
| Mobile Network Operator | Tenant self-service | TenantProfile, FleetInferencePool | Competitive loss analysis |

---

## References

### llm-d Upstream

- [llm-d repository](https://github.com/llm-d/llm-d)
- [InferencePool CRD specification](https://github.com/llm-d/llm-d/tree/main/api)
- [EPP (Endpoint Picker Plugin)](https://github.com/llm-d/llm-d/tree/main/pkg/epp)
- [WVA metrics specification](https://github.com/llm-d/llm-d/tree/main/docs/metrics)

### Related Projects

- [CNCF model-spec (ModelPack)](https://github.com/cncf/model-spec) -- OCI-based model metadata standard
- [Gateway API](https://gateway-api.sigs.k8s.io/) -- Kubernetes networking API (routing pattern inspiration)
- [Kueue](https://kueue.sigs.k8s.io/) -- Kubernetes-native job queueing (quota model inspiration)
- [Karpenter](https://karpenter.sh/) -- Node autoscaling (scaling policy design inspiration)

### Kubernetes Ecosystem

- [KEP process](https://github.com/kubernetes/enhancements/tree/master/keps) -- Enhancement proposal format reference
- [API conventions](https://github.com/kubernetes/community/blob/master/contributors/devel/sig-architecture/api-conventions.md)
- [Custom Resource Definitions](https://kubernetes.io/docs/concepts/extend-kubernetes/api-extension/custom-resources/)

### Competitive Landscape

- [ModelPlane](https://github.com/modelplane/modelplane) -- Crossplane-based fleet orchestration
- [SkyPilot Endpoints](https://github.com/skypilot-org/skypilot) -- Cross-cloud inference orchestration
- [NVIDIA NVAIE](https://www.nvidia.com/en-us/data-center/products/ai-enterprise/) -- Enterprise AI platform
- [Rafay](https://rafay.co/) -- Multi-cluster Kubernetes management
