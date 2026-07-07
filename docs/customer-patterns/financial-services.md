# Financial Services Deployment Pattern

## fleet-llm-d for Regulated Banking Infrastructure

**Target customers:** Wells Fargo, Bank of America (BoA), JPMC
**Pattern version:** 0.1
**Last updated:** July 2026

---

## 1. Overview

Financial services institutions face a unique intersection of requirements when
deploying large language model inference at scale: strict regulatory compliance,
multi-region data residency, immutable audit trails, and zero-tolerance for
unplanned downtime. This document describes the fleet-llm-d deployment pattern
designed for Tier 1 US banks, addressing OCC SR 11-7 model risk management,
SOC 2 Type II audit requirements, FIPS 140-2/3 cryptographic standards, and
cross-region failover with sub-30-second recovery time objectives.

The pattern is informed by direct engagement with Wells Fargo (5-model production
deployment), Bank of America (multi-cluster routing as stated top priority), and
JPMC (model governance and tenant isolation requirements). It replaces Run:ai GPU
scheduling with fleet-llm-d's native placement and scaling controllers.

### Key Differentiators

- **ARE Immutable Ledger** -- hash-chained audit trail meeting SOC 2 Type II
  and OCC SR 11-7 evidence requirements.
- **Regulatory placement constraints** -- data residency enforced at the CRD
  level.
- **SLO-gated canary rollouts** -- automatic rollback on SLO violation.
- **Per-tenant tokenomics** -- LOB chargeback with budget alerts and hard caps.

---

## 2. Architecture

### 2.1 Multi-Region Topology

```
+=========================================================================+
|                     REGULATORY BOUNDARY (US ONLY)                       |
|                                                                         |
|  +-------------------------------+  +-------------------------------+   |
|  |        us-east-1 (PRIMARY)    |  |       us-west-2 (SECONDARY)   |   |
|  |                               |  |                               |   |
|  |  +-------------------------+  |  |  +-------------------------+  |   |
|  |  |   Hub Cluster (East)    |  |  |  |   Hub Cluster (West)    |  |   |
|  |  |  +-------------------+  |  |  |  |  +-------------------+  |  |   |
|  |  |  | fleet-controller  |  |  |  |  | fleet-controller  |  |  |   |
|  |  |  | placement-engine  |<----+---->| placement-engine  |  |  |   |
|  |  |  | tenant-governor   |  |  |  |  | tenant-governor   |  |  |   |
|  |  |  | lifecycle-mgr     |  |  |  |  | lifecycle-mgr     |  |  |   |
|  |  |  +-------------------+  |  |  |  +-------------------+  |  |   |
|  |  +-------------------------+  |  |  +-------------------------+  |   |
|  |                               |  |                               |   |
|  |  +----------+ +----------+   |  |  +----------+ +----------+   |   |
|  |  | DC-East-1| | DC-East-2|   |  |  | DC-West-1| | DC-West-2|   |   |
|  |  | Worker   | | Worker   |   |  |  | Worker   | | Worker   |   |   |
|  |  | Cluster  | | Cluster  |   |  |  | Cluster  | | Cluster  |   |   |
|  |  | (FIPS)   | | (FIPS)   |   |  |  | (FIPS)   | | (FIPS)   |   |   |
|  |  | H100x8   | | H100x8   |   |  |  | H100x8   | | H100x8   |   |   |
|  |  +----+-----+ +----+-----+   |  |  +----+-----+ +----+-----+   |   |
|  |       |             |         |  |       |             |         |   |
|  +-------------------------------+  +-------------------------------+   |
|          |             |                    |             |              |
|          +------+------+--------------------+------+------+             |
|                 |                                  |                     |
|  +--------------v----------------------------------v-----------------+  |
|  |              ARE LEDGER (HARDENED INFRASTRUCTURE)                  |  |
|  |  +--------------------+    +--------------------+                 |  |
|  |  | are-ledger (East)  |<-->| are-ledger (West)  |                 |  |
|  |  | are-gateway        |    | are-gateway        |                 |  |
|  |  | PostgreSQL (FIPS)  |    | PostgreSQL (FIPS)  |                 |  |
|  |  +--------------------+    +--------------------+                 |  |
|  |  Hash-chained append-only ledger | Replicated across regions     |  |
|  +-------------------------------------------------------------------+  |
|                                                                         |
+=========================================================================+
```

### 2.2 Data Flow

All inference traffic and control plane communication remains within the US
regulatory boundary. Cross-region traffic traverses dedicated private links
with mTLS. The ARE Ledger runs on isolated infrastructure with its own RBAC,
database, and certificate chain.

```
  Client --> Fleet Gateway --> PlacementPolicy eval --> DC Worker --> ARE Ledger
                                    |                                    ^
                               [local or cross-region]                   |
                                    +-----> Failover DC Worker ----------+
```

### 2.3 Run:ai Migration Path

1. **Shadow mode:** fleet-llm-d runs alongside Run:ai, logging placement
   recommendations to the ARE Ledger without making decisions.
2. **Gradual cutover:** Non-critical models migrate to fleet-llm-d.
3. **Full replacement:** PlacementPolicy and FleetScalingPolicy CRDs replace
   Run:ai scheduling rules.

---

## 3. CRD Configuration

All resources use API group `fleet.llm-d.ai`, version `v1alpha1`.

### 3.1 PlacementPolicy -- Regulatory Data Residency

```yaml
apiVersion: fleet.llm-d.ai/v1alpha1
kind: PlacementPolicy
metadata:
  name: financial-services-regulated
  namespace: fleet-system
spec:
  constraints:
    - type: regulatory
      rule: "cluster.region in ['us-east-1', 'us-west-2']"
      description: "Data residency: inference must remain in approved US regions."
    - type: hardware
      rule: "cluster.labels['security.fips'] == 'true'"
      description: "FIPS 140-2/3: only FIPS-validated clusters eligible."
  affinity:
    - type: dataLocality
      weight: 0.5
      parameters:
        preferredRegion: "us-east-1"
    - type: costEfficiency
      weight: 0.3
  spreading:
    maxSkew: 1
    topologyKey: "topology.kubernetes.io/region"
```

### 3.2 TenantProfile -- LOB Governance with Tokenomics

Each line of business receives a TenantProfile governing quotas, rate limits,
priority, cost controls, and cluster access.

```yaml
apiVersion: fleet.llm-d.ai/v1alpha1
kind: TenantProfile
metadata:
  name: consumer-banking-lob
  namespace: fleet-tenants
spec:
  quotas:
    maxTokensPerMinute: 1000000
    maxConcurrentRequests: 500
    maxModels: 10
    gpuBudget:
      maxGPUs: 64
      gpuTypes: ["nvidia-h100", "nvidia-h200"]
  rateLimit:
    requestsPerSecond: 2000    # Sustained RPS
    burstSize: 5000            # Burst for intraday market spikes
  priority: 800                # Yields to risk/compliance (900+)
  costControl:
    monthlyBudget: "200000.00"
    alertThreshold: 0.7
  clusters:
    allowed: ["dc-east-1-prod-fips", "dc-east-2-prod-fips",
              "dc-west-1-prod-fips", "dc-west-2-prod-fips"]
```

### 3.3 FleetInferencePool -- SLO-Gated Canary Rollout

```yaml
apiVersion: fleet.llm-d.ai/v1alpha1
kind: FleetInferencePool
metadata:
  name: llama-70b-consumer-banking
  namespace: fleet-inference
  annotations:
    fleet.llm-d.ai/approved-by: "model-governance-board"
    fleet.llm-d.ai/approval-date: "2026-06-15"
    fleet.llm-d.ai/risk-classification: "tier-2"
spec:
  model:
    name: "meta-llama/Llama-3.1-70B-Instruct"
    source: "s3://wf-approved-models/llama-3.1-70b-instruct"
    version: "3.1.0-fips"
  placement:
    policyRef:
      name: financial-services-regulated
      namespace: fleet-system
    minClusters: 2
    maxClusters: 4
  routing:
    policyRef:
      name: latency-optimized-failover
      namespace: fleet-system
  scaling:
    policyRef:
      name: banking-hours-scaling
      namespace: fleet-system
  serving:
    inferencePoolTemplate:
      targetPorts:
        serving: 8080
        health: 8081
        metrics: 9090
      endpointPickerRef:
        name: slo-aware-epp
        namespace: fleet-inference
  lifecycle:
    rolloutStrategy: Canary
    canary:
      initialWeight: 5
      weightIncrement: 5
      interval: "30m"
      sloGate:
        maxLatencyP99Ms: 150
        minSuccessRate: 0.999
      rollbackOnFailure: true
```

### 3.4 ARE Ledger Integration

The ARE Immutable Ledger is an independent compliance platform outside
fleet-llm-d. Every placement, routing, scaling, and rollout decision is
recorded as a hash-chained entry.

#### Ledger Entry -- Placement Decision

```json
{
  "entryId": "are-0x7f3a91c2e4b8",
  "chainIndex": 48291,
  "previousHash": "sha256:a4f8c1d92e3b...6c7d8e9f0a1b",
  "entryHash": "sha256:b5e9d2c83f4a...7c8d9e0f1a",
  "timestamp": "2026-07-06T14:23:17.482Z",
  "writer": "fleet-controller/placement-engine",
  "writerSignature": "ed25519:...",
  "type": "placement",
  "payload": {
    "resource": "FleetInferencePool/fleet-inference/llama-70b-consumer-banking",
    "action": "place",
    "model": "meta-llama/Llama-3.1-70B-Instruct",
    "version": "3.1.0-fips",
    "decision": {
      "selectedClusters": ["dc-east-1-prod-fips", "dc-west-1-prod-fips"],
      "rejectedClusters": [
        {
          "cluster": "dc-east-3-staging",
          "reason": "constraint-violation:regulatory -- region not in allowed set"
        },
        {
          "cluster": "dc-west-3-dev",
          "reason": "constraint-violation:hardware -- security.fips != true"
        }
      ],
      "constraintsEvaluated": [
        {"type": "regulatory", "result": "pass", "matchedClusters": 4},
        {"type": "hardware", "result": "pass", "matchedClusters": 4}
      ],
      "affinityScores": {
        "dc-east-1-prod-fips": 0.92,
        "dc-east-2-prod-fips": 0.87,
        "dc-west-1-prod-fips": 0.85,
        "dc-west-2-prod-fips": 0.81
      }
    },
    "policyRef": "PlacementPolicy/fleet-system/financial-services-regulated"
  }
}
```

#### Chain Verification

```bash
curl -s --cert /etc/fleet/certs/auditor.crt \
     --key /etc/fleet/certs/auditor.key \
     --cacert /etc/fleet/certs/ca.crt \
     https://fleet-controller.fleet-system.svc:8443/api/v1/verify/chains \
     -d '{"scope": "FleetInferencePool/fleet-inference/llama-70b-consumer-banking",
          "fromIndex": 0, "toIndex": 48291}' | jq .
# Response: {"verified": true, "chainLength": 48291, "tamperDetected": false}
```

#### SOC 2 Type II Control Mapping

| SOC 2 Control | ARE Ledger Evidence | Entry Type |
|---|---|---|
| CC6.1 -- Logical access controls | Tenant cluster restrictions, RBAC records | placement, routing |
| CC7.1 -- System monitoring | SLO gate evaluations, health check results | scaling, rollout |
| CC7.2 -- Anomaly detection | Rollback triggers, constraint violations | rollout, placement |
| CC8.1 -- Change management | Canary promotion steps, rollout decisions | rollout |
| CC9.1 -- Risk mitigation | Rejected cluster rationale, constraint evaluation | placement |

Each entry includes a `writerSignature` (Ed25519) for non-repudiation.

---

## 4. Key Requirements

### 4.1 FIPS 140-2/3 Compliance

- **Data at rest:** AES-256 encrypted storage for model weights, KV cache, logs.
- **Data in transit:** mTLS with FIPS-validated TLS 1.3 on all inter-cluster
  communication (fleet gateway, agents, ARE Ledger replication).
- **Placement enforcement:** Hardware constraint
  `cluster.labels['security.fips'] == 'true'` excludes non-FIPS nodes.

### 4.2 SOC 2 Type II Audit Trail

- **Completeness:** Every fleet decision recorded before execution.
- **Immutability:** Hash-chaining prevents retroactive modification.
- **Non-repudiation:** Ed25519 writer signatures on every entry.
- **Availability:** Synchronous cross-region replication, zero RPO.

### 4.3 Cross-DC Routing with Automatic Failover

Target RTO: less than 30 seconds.

- **Health monitoring:** Worker clusters run health probes at 5-second intervals.
  Three consecutive failures trigger failover.
- **Traffic rerouting:** FleetRoutingPolicy redirects traffic to the secondary
  region within the RTO window.
- **KV cache transfer:** Stateful sessions use KVCacheTransferPolicy for cache
  migration. The ARE Ledger issues a proof receipt for each transfer.
- **Automatic recovery:** When the failed cluster recovers, traffic is gradually
  rebalanced using the spreading configuration.

### 4.4 Run:ai Migration

| Run:ai Capability | fleet-llm-d Equivalent |
|---|---|
| GPU scheduling | PlacementPolicy + FleetScalingPolicy |
| Quota management | TenantProfile quotas and gpuBudget |
| Workload prioritization | TenantProfile priority (0-1000) |
| GPU fractions | FleetScalingPolicy with fine-grained allocation |
| Dashboard | Fleet observability (Prometheus federation, Grafana) |

### 4.5 Model Governance

- **Approved model registry:** Models from internal S3 only
  (`s3://[bank]-approved-models/`).
- **Version pinning:** `model.version` locks to approved version; upgrades
  require governance board approval.
- **Governance annotations:** `approved-by`, `approval-date`,
  `risk-classification` on every FleetInferencePool.
- **ModelPack integration:** OCI-compliant metadata provides a verifiable chain
  from model artifact to deployment.

---

## 5. Deployment

### 5.1 Hub Overlay per Region

```
deploy/overlays/financial-services/
  us-east-1/                        # Primary region overlay
    kustomization.yaml, fleet-controller-patch.yaml,
    placement-engine-patch.yaml, are-ledger-patch.yaml, certificates/
  us-west-2/                        # Secondary region overlay
    kustomization.yaml, fleet-controller-patch.yaml,
    placement-engine-patch.yaml, are-ledger-patch.yaml, certificates/
  base/                             # Shared config
    kustomization.yaml, fips-node-selector.yaml, network-policies.yaml, rbac/
```

### 5.2 Cross-Region Federation

1. **Shared CRD state:** PlacementPolicy, TenantProfile, and
   FleetRoutingPolicy synchronized between hubs. FleetInferencePool is
   region-local but references shared policies.
2. **Leader election:** One hub is active placement leader; automatic transfer
   during failover, recorded in ARE Ledger.
3. **Split-brain prevention:** Quorum-based consensus; each hub serves its
   local region independently if connectivity is lost.

### 5.3 ARE Ledger on Hardened Nodes

```yaml
# ARE Ledger node pool -- isolated from inference workloads
apiVersion: v1
kind: Node
metadata:
  labels:
    node-role.kubernetes.io/are-ledger: "true"
    security.fips: "true"
    fleet.llm-d.ai/dedicated: "are-ledger"
spec:
  taints:
    - key: fleet.llm-d.ai/dedicated
      value: "are-ledger"
      effect: NoSchedule
```

- **Isolated infrastructure:** Separate node pool with taints. No inference
  workloads run on ledger nodes.
- **Encrypted storage:** PostgreSQL with FIPS-validated AES-256. WAL archiving
  to encrypted S3 for point-in-time recovery.
- **Cross-region replication:** Synchronous replication between regions. RPO is
  zero (no data loss on regional failover).

### 5.4 RBAC Configuration

Namespace-level isolation per LOB. LOB operators get read-only access to their
own FleetInferencePool and TenantProfile resources. PlacementPolicy and
FleetRoutingPolicy are managed by the platform team only. Auditors get
read-only access to all fleet resources plus the `/api/v1/verify/*` chain
verification endpoints.

```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: lob-inference-operator
  namespace: fleet-lob-consumer-banking
rules:
  - apiGroups: ["fleet.llm-d.ai"]
    resources: ["fleetinferencepools", "tenantprofiles"]
    verbs: ["get", "list", "watch"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: fleet-auditor
rules:
  - apiGroups: ["fleet.llm-d.ai"]
    resources: ["*"]
    verbs: ["get", "list", "watch"]
  - nonResourceURLs: ["/api/v1/verify/*"]
    verbs: ["get", "post"]
```

### 5.5 Certificate Rotation and mTLS

Customer-managed PKI with FIPS-validated HSM-backed root CA. A fleet-specific
intermediate CA signs all component certificates. 90-day rotation via
cert-manager with zero-downtime dual-certificate rollover. The ARE Ledger uses
a separate certificate chain; writer and auditor certs are issued from a
dedicated intermediate CA.

```yaml
apiVersion: cert-manager.io/v1
kind: Certificate
metadata:
  name: fleet-controller-cert
  namespace: fleet-system
spec:
  secretName: fleet-controller-tls
  duration: 2160h       # 90 days
  renewBefore: 360h     # Renew 15 days before expiry
  issuerRef: {name: fleet-intermediate-ca, kind: ClusterIssuer}
  commonName: fleet-controller.fleet-system.svc
  dnsNames: ["fleet-controller.fleet-system.svc",
             "fleet-controller.fleet-system.svc.cluster.local"]
  privateKey: {algorithm: ECDSA, size: 384}
  usages: [server auth, client auth]
```

---

## 6. Benchmark Targets

### 6.1 Customer Reference Points

| Customer | Deployment Scale | Status |
|---|---|---|
| Wells Fargo | 5 models in production, multi-region | Active engagement |
| AT&T | $2.5M annual cost projection, fleet-wide inference | Capacity planning |
| Bank of America | Multi-cluster routing (stated top priority) | Architecture review |
| JPMC | Model governance, tenant isolation | Requirements gathering |

### 6.2 Performance Targets

| Metric | Target | Measurement Point |
|---|---|---|
| Availability | 99.99% across regions | Fleet gateway |
| TTFT p99 | < 150ms for 70B models | End-to-end, client-measured |
| Cross-region failover RTO | < 30 seconds | Failure detection to traffic reroute |
| Placement decision latency | < 100ms p99 | fleet-controller internal |
| Routing decision latency | < 5ms p99 | Fleet gateway internal |
| Autoscale reaction time | < 30 seconds | Signal to new replica ready |
| ARE Ledger write throughput | > 10,000 entries/sec | Sustained, per-region |
| ARE Ledger write latency | < 2ms p50, < 10ms p99 | Per-entry |

99.99% availability (52.6 minutes downtime/year) is achieved through
multi-region active-passive, multi-DC spreading, SLO-gated rollouts with
automatic rollback, and ARE Ledger independence from the inference path.

### 6.3 Audit and Zero-Downtime Updates

Full ARE Ledger audit trail covers placement (cluster selection rationale,
rejected clusters), routing (failover events), scaling (SLO signals), and
rollout (canary promotion steps, gate results, rollback triggers).

SLO-gated canary rollouts provide zero-downtime model updates: 5% initial
traffic, 5% increments every 30 minutes, automatic rollback on gate failure.
Full rollout takes approximately 10 hours. Every step is recorded in the ARE
Ledger.
