# Sovereign Cloud Deployment Pattern

## Fleet-Level LLM Inference for OSAC, Government, and Sovereign Cloud Providers

**fleet-llm-d Version:** v1alpha1 | **Last Updated:** July 2026

---

## 1. Overview

This pattern addresses organizations that must guarantee data residency, air-gapped
operation, and cryptographic model provenance. Target audiences:

- **OSAC (Open Sovereign AI Cloud)** -- National cloud providers building sovereign AI
  infrastructure on OpenShift with no external data egress.
- **Government Agencies** -- Defense, intelligence, and civilian agencies operating
  classified AI workloads behind air-gap boundaries (NIST 800-53, CNSSI 1253).
- **Sovereign Cloud Providers** -- Commercial providers (T-Systems, OVHcloud, Scaleway)
  offering GPU-as-a-Service to multiple government tenants with strict isolation.

fleet-llm-d supports this through PlacementPolicy regulatory constraints, ModelPack
OCI-based provenance with cryptographic signatures, TenantProfile GPU-as-a-Service
governance, and ARE Immutable Ledger integration for tamper-evident compliance records.

---

## 2. Architecture

Each sovereign zone is a fully self-contained fleet-llm-d deployment. No inference data,
model weights, or tenant content crosses zone boundaries. The only inter-zone channel is
a federated coordination plane that synchronizes model catalog metadata only.

```
+============================================================================+
|                    FEDERATED COORDINATION PLANE                             |
|              (metadata-only sync: model catalog, no inference data)         |
|                                                                            |
|   Model Catalog Sync          Model Catalog Sync        Model Catalog Sync |
|         |                           |                          |           |
+=========|===========================|==========================|===========+
          |                           |                          |
  ========|=========          ========|=========         ========|=========
  | AIR-GAP BOUNDARY|        | AIR-GAP BOUNDARY|       | AIR-GAP BOUNDARY|
  ===================        ===================       ===================
          |                           |                          |
+---------v-----------+  +-----------v-----------+  +-----------v-----------+
| EU-SOVEREIGN ZONE   |  | MEA-SOVEREIGN ZONE    |  | APAC-SOVEREIGN ZONE   |
|                      |  |                       |  |                       |
| +------------------+ |  | +-------------------+ |  | +-------------------+ |
| | fleet-llm-d      | |  | | fleet-llm-d       | |  | | fleet-llm-d       | |
| | Control Plane    | |  | | Control Plane     | |  | | Control Plane     | |
| | (standalone)     | |  | | (standalone)      | |  | | (standalone)      | |
| +--------+---------+ |  | +---------+---------+ |  | +---------+---------+ |
|          |            |  |           |           |  |           |           |
| +--------v---------+ |  | +---------v---------+ |  | +---------v---------+ |
| | ARE Immutable    | |  | | ARE Immutable     | |  | | ARE Immutable     | |
| | Ledger           | |  | | Ledger            | |  | | Ledger            | |
| +------------------+ |  | +-------------------+ |  | +-------------------+ |
|                      |  |                       |  |                       |
| +------------------+ |  | +-------------------+ |  | +-------------------+ |
| | Offline Model    | |  | | Offline Model     | |  | | Offline Model     | |
| | Registry (OCI)   | |  | | Registry (OCI)    | |  | | Registry (OCI)    | |
| | ModelPack Store  | |  | | ModelPack Store   | |  | | ModelPack Store   | |
| +------------------+ |  | +-------------------+ |  | +-------------------+ |
|                      |  |                       |  |                       |
| GPU POOL             |  | GPU POOL              |  | GPU POOL              |
| +------+ +------+   |  | +------+ +------+    |  | +------+ +------+    |
| |H100  | |H100  |   |  | |H100  | |A100  |    |  | |H100  | |H100  |    |
| |Tenant| |Tenant|   |  | |Tenant| |Tenant|    |  | |Tenant| |Tenant|    |
| |A,B,C | |D,E   |   |  | |F,G,H | |I,J   |    |  | |K,L,M | |N,O   |    |
| +------+ +------+   |  | +------+ +------+    |  | +------+ +------+    |
|                      |  |                       |  |                       |
| Cluster: eu-sov-01   |  | Cluster: mea-sov-01   |  | Cluster: apac-sov-01  |
| Cluster: eu-sov-02   |  | Cluster: mea-sov-02   |  | Cluster: apac-sov-02  |
| Cluster: eu-sov-03   |  | Cluster: mea-sov-03   |  | Cluster: apac-sov-03  |
+----------------------+  +-----------------------+  +-----------------------+
```

Key properties: each zone runs standalone (no hub dependency); the air-gap boundary is a
network-level hard cut with no TCP/UDP traversal; GPU pools are shared across tenants
within each zone using MIG/MPS hardware isolation and TenantProfile quotas.

---

## 3. CRD Configuration

### 3.1 PlacementPolicy: Sovereign Placement Constraints

```yaml
apiVersion: fleet.llm-d.ai/v1alpha1
kind: PlacementPolicy
metadata:
  name: eu-sovereign-placement
  namespace: sovereign-ai
  labels:
    fleet.llm-d.ai/zone: eu-sovereign
    fleet.llm-d.ai/classification: restricted
  annotations:
    fleet.llm-d.ai/compliance-framework: "eu-ai-act,nist-ai-rmf"
spec:
  constraints:
    - type: regulatory
      rule: "cluster.labels['sovereignty.zone'] == 'eu-sovereign'"
      description: "Data must not leave EU sovereign zone"
    - type: regulatory
      rule: "cluster.labels['airgap.certified'] == 'true'"
      description: "Cluster must be certified for air-gapped operation"
    - type: hardware
      rule: "cluster.gpu.type in ['nvidia-h100', 'nvidia-a100']"
      description: "Only H100 and A100 GPUs approved for sovereign workloads"
  affinity:
    - type: gpuUtilization
      weight: 0.4
    - type: costEfficiency
      weight: 0.3
    - type: dataLocality
      weight: 0.3
  # No spreading field: sovereign workloads stay within a single zone.
  # Spreading across zones would violate sovereignty constraints.
```

### 3.2 TenantProfile: Government Agency GPU-as-a-Service

```yaml
apiVersion: fleet.llm-d.ai/v1alpha1
kind: TenantProfile
metadata:
  name: eu-ministry-of-defense
  namespace: sovereign-ai
  labels:
    fleet.llm-d.ai/zone: eu-sovereign
    fleet.llm-d.ai/tenant-class: government
spec:
  quotas:
    maxTokensPerMinute: 200000
    maxConcurrentRequests: 100
    maxModels: 3
    gpuBudget:
      maxGPUs: 16
      gpuTypes:
        - "nvidia-h100"
  rateLimit:
    requestsPerSecond: 500
    burstSize: 1000
  # Priority 300: elevated for defense (scale 0-1000, default 100)
  priority: 300
  costControl:
    monthlyBudget: "25000.00"
    alertThreshold: 0.75
  clusters:
    # Restricted to sovereign-zone clusters only
    allowed:
      - "eu-sov-01"
      - "eu-sov-02"
      - "eu-sov-03"
```

### 3.3 FleetScalingPolicy: Sovereign Zone Autoscaling

```yaml
apiVersion: fleet.llm-d.ai/v1alpha1
kind: FleetScalingPolicy
metadata:
  name: eu-sovereign-scaling
  namespace: sovereign-ai
  labels:
    fleet.llm-d.ai/zone: eu-sovereign
spec:
  strategy: cost-optimized
  objectives:
    - metric: gpu-utilization
      target: "85%"
      tolerancePercent: 10
    - metric: queue-depth
      target: "50"
      tolerancePercent: 15
  constraints:
    globalMaxGPUs: 128
    stabilizationWindowSeconds: 600
    maxScaleUpRate: "8/10m"
    maxScaleDownRate: "4/10m"
  crossCluster:
    # Migration within zone only (zone boundary enforced by PlacementPolicy)
    enableMigration: true
    migrationThreshold: 0.25
    migrationCooldownSeconds: 600
  scaleToZero:
    enabled: true
    cooldownPeriod: "1h"
    scaleUpTrigger: request-arrival
```

### 3.4 FleetInferencePool: Sovereign Model Deployment

```yaml
apiVersion: fleet.llm-d.ai/v1alpha1
kind: FleetInferencePool
metadata:
  name: sovereign-glm-130b
  namespace: sovereign-ai
  labels:
    fleet.llm-d.ai/zone: eu-sovereign
    fleet.llm-d.ai/model-class: sovereign-llm
  annotations:
    fleet.llm-d.ai/modelpack-verified: "true"
    fleet.llm-d.ai/modelpack-sbom: "registry.sovereign-zone.local/sbom/glm-130b:v2.1"
spec:
  model:
    name: "sovereign-ai/GLM-130B"
    # Air-gapped OCI registry within the sovereign zone
    source: "registry.sovereign-zone.local/models/glm-130b"
    version: "v2.1.0"
  placement:
    policyRef:
      name: eu-sovereign-placement
      namespace: sovereign-ai
    minClusters: 2
    maxClusters: 3
  routing:
    policyRef:
      name: eu-sovereign-routing
      namespace: sovereign-ai
  scaling:
    policyRef:
      name: eu-sovereign-scaling
      namespace: sovereign-ai
  serving:
    inferencePoolTemplate:
      targetPorts:
        serving: 8080
        health: 8081
        metrics: 9090
  lifecycle:
    # Rolling: conservative, sequential cluster-by-cluster updates
    rolloutStrategy: Rolling
```

### 3.5 FleetRoutingPolicy: Sovereign Zone Routing

```yaml
apiVersion: fleet.llm-d.ai/v1alpha1
kind: FleetRoutingPolicy
metadata:
  name: eu-sovereign-routing
  namespace: sovereign-ai
  labels:
    fleet.llm-d.ai/zone: eu-sovereign
spec:
  strategy: geographic
  rules:
    - name: sovereign-zone-local
      action:
        preferLocal: true
        kvCacheAffinity: true
        maxLatencyMs: 500
        # Failover only to clusters within the sovereign zone
        failover:
          clusters:
            - "eu-sov-01"
            - "eu-sov-02"
            - "eu-sov-03"
  healthCheck:
    interval: "10s"
    unhealthyThreshold: 3
```

---

## 4. Key Requirements

### 4.1 Disconnected / Air-Gapped Deployment

Sovereign zones operate with zero external network connectivity: no outbound DNS, no
public registry pulls, no OLM catalog access, no telemetry. All images (fleet-llm-d
control plane, data plane, SGLang runtime) are pre-pulled into the zone-local OCI
registry. Operator catalogs are mirrored offline.

### 4.2 Data Sovereignty Enforcement

Enforced through layered mechanisms:

- **PlacementPolicy constraints** -- CEL expressions evaluated against cluster labels
  reject any cluster outside the designated sovereignty zone.
- **ARE Immutable Ledger** -- Every placement decision is hash-chained with the full
  constraint evaluation trace, providing cryptographic proof of sovereignty enforcement.
- **FleetRoutingPolicy zone binding** -- Failover targets are restricted to zone-local
  clusters. Traffic cannot route outside the zone even during cluster failure.
- **Network-level enforcement** -- Air-gap boundary eliminates any network path for
  data to leave the zone, providing the ultimate hard guarantee.

### 4.3 ModelPack for Model Provenance

- **OCI Signatures** -- Verified against the sovereign zone's PKI trust anchor using
  Sigstore/cosign before any model is accepted for deployment.
- **SBOM** -- CycloneDX bill of materials listing model components, fine-tuning
  datasets, quantization toolchain, and runtime dependencies.
- **Vulnerability Scanning** -- Artifacts scanned against offline CVE/NVD database.
  Critical findings block deployment; medium findings generate alerts.
- **Provenance Chain** -- Import timestamp, signature result, SBOM hash, scan result,
  and operator identity recorded in the ARE ledger as hash-chained entries.

### 4.4 Multi-Tenant GPU Sharing

- **GPU Budgets** -- Hard per-tenant GPU quota via TenantProfile prevents monopolization.
- **MIG** -- H100 GPUs partitioned into up to 7 isolated instances for hardware-level
  memory and compute isolation between tenants.
- **MPS** -- Process-level GPU sharing for smaller workloads not requiring full MIG.
- **Priority Scheduling** -- TenantProfile priority (0-1000) governs request scheduling
  during contention. Defense agencies receive elevated priority (300+).
- **Cost Attribution** -- Per-tenant cost tracking with budget alerts and chargeback.

---

## 5. Deployment

### 5.1 Standalone Overlay per Sovereign Zone

Each zone deploys fleet-llm-d in standalone mode via a zone-specific Kustomize overlay:

```
deploy/overlays/sovereign/eu-sovereign/
  kustomization.yaml
  namespace.yaml
  fleet-config.yaml           # Zone-specific configuration
  registry-mirror.yaml        # OCI registry mirror
  airgap-dns.yaml             # Zone-internal DNS
  mig-config.yaml             # GPU MIG partitioning
  are-ledger-config.yaml      # ARE ledger zone-local config
  tenant-profiles/
    ministry-of-defense.yaml
    ministry-of-interior.yaml
    national-ai-center.yaml
```

The overlay replaces image references with `registry.sovereign-zone.local/...`, disables
external network access, configures zone-local ARE ledger storage, and sets the fleet
controller's `--sovereignty-zone` flag.

### 5.2 Air-Gapped Installation

**Phase 1: Staging (Connected Environment)**

```bash
# Mirror container images
oc adm catalog mirror \
  registry.redhat.io/fleet-llm-d/fleet-llm-d-operator-catalog:v1 \
  file:///mirror/fleet-llm-d --to-manifests=manifests/

# Export ModelPack with signatures and SBOMs
modelpack export sovereign-ai/GLM-130B:v2.1.0 \
  --include-signatures --include-sbom \
  --output /mirror/models/glm-130b-v2.1.0.tar

# Mirror vulnerability database
modelpack mirror-vulndb --output /mirror/vulndb/
```

**Phase 2: Physical Transfer** -- Transfer `/mirror/` across the air-gap via encrypted
USB, optical media, or data diode.

**Phase 3: Import (Air-Gapped Environment)**

```bash
# Load images into zone-local registry
oc adm catalog mirror file:///import/fleet-llm-d \
  registry.sovereign-zone.local/fleet-llm-d --from-manifests=manifests/

# Import ModelPack with verification
modelpack import /import/models/glm-130b-v2.1.0.tar \
  --registry registry.sovereign-zone.local/models \
  --verify-signatures --verify-sbom --record-to-ledger

# Apply sovereign zone overlay
oc apply -k deploy/overlays/sovereign/eu-sovereign/

# Initialize ARE ledger
are-ledger init --schema /import/are/schema.sql \
  --zone eu-sovereign --signing-key /etc/are/zone-signing-key.pem
```

### 5.3 ModelPack Import Pipeline

Each model import passes through a five-stage verification pipeline:

1. **Signature Verification** -- OCI signature verified against zone trust anchor.
   Failure rejects the import and records the event in the ARE ledger.
2. **SBOM Validation** -- CycloneDX SBOM checked against the approved software list.
3. **Vulnerability Scan** -- Scanned against offline CVE/NVD. Critical findings block.
4. **Metadata Resolution** -- GPU memory, tensor parallelism, and compatible GPU types
   resolved from model architecture and indexed in the zone fleet catalog.
5. **Ledger Recording** -- Complete import record written as a hash-chained ARE entry.

### 5.4 Federated Coordination

Each zone periodically exports its model catalog as a JSON manifest (model names,
versions, content hashes, resource requirements). No model weights or tenant data is
included. The manifest is transferred via the same physical media process and indexed
in peer zones as a read-only federated catalog view.

### 5.5 GPU Partitioning (MIG/MPS)

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: mig-config
  namespace: sovereign-ai
data:
  config.yaml: |
    profiles:
      large-tenant:
        gpu-type: nvidia-h100
        mig-profile: "3g.40gb"
        instances-per-gpu: 2
      medium-tenant:
        gpu-type: nvidia-h100
        mig-profile: "2g.20gb"
        instances-per-gpu: 3
      small-tenant:
        gpu-type: nvidia-h100
        mig-profile: "1g.10gb"
        instances-per-gpu: 7
```

---

## 6. Benchmark Targets

Reference: MEA government sector GLM deployments on SGLang. Configuration per zone:
3 OpenShift clusters, 8x H100-80GB GPUs per cluster (24 total), sovereign-ai/GLM-130B
with TP=8, 50+ tenants sharing via TenantProfile.

### 6.1 Performance Targets

| Metric | Target | Measurement |
|---|---|---|
| Availability (within zone) | 99.9% uptime | Synthetic probe, 30-day rolling |
| GPU Utilization | > 85% average | DCGM Exporter, 1-hour window |
| ModelPack Verification | < 5s per ModelPack | Signature + SBOM + scan end-to-end |
| Data Egress | Zero (enforced + audited) | Network policy + ARE ledger audit |
| Tenants per Zone | 50+ concurrent | Active TenantProfiles with quotas |
| Placement Latency | < 100ms p99 | Placement engine metrics |
| Cross-Cluster Migration | < 60s | KV cache transfer + replica startup |
| Scale-from-Zero Cold Start | < 30s | Request arrival to first token |
| ARE Ledger Throughput | > 10,000 entries/sec | Ledger benchmark under load |
| ARE Ledger Latency | < 10ms p99 | Ledger benchmark under load |

### 6.2 Compliance Targets

| Framework | Requirement | Mechanism |
|---|---|---|
| EU AI Act (Article 12) | Automatic decision logging | ARE ledger records all placement/routing/scaling |
| NIST AI RMF (MAP) | Deployment rationale | PlacementPolicy constraint trace in ARE ledger |
| NIST AI RMF (MEASURE) | Continuous measurement | FleetScalingPolicy objectives + Prometheus |
| NIST AI RMF (MANAGE) | Documented deployments | ModelLifecycle rollout records in ARE ledger |
| NIST 800-53 (SI-7) | Software integrity | ModelPack OCI signature verification |
| SOC 2 Type II | Non-repudiation | ARE ledger hash-chained entries with signatures |

### 6.3 Zero Data Egress Verification

Enforced at three layers:

1. **Network** -- Kubernetes NetworkPolicies deny all egress outside the zone CIDR.
   Calico/Cilium provide additional L7 enforcement.
2. **Application** -- FleetRoutingPolicy failover lists contain only zone-local clusters.
   The fleet gateway rejects any out-of-zone routing decision.
3. **Audit** -- ARE ledger records all routing decisions. A scheduled audit job queries
   for any routing events targeting out-of-zone clusters. The hash-chained ledger
   prevents retroactive modification of routing records.

---

## 7. Operational Considerations

**Day-2 Model Updates** -- Follow the staged import process (Section 5.2). Rolling
lifecycle ensures one-cluster-at-a-time updates within the zone with ARE ledger tracing.

**Tenant Onboarding** -- Create a TenantProfile with quotas, GPU budget, and cluster
restrictions. Onboarding is recorded in the ARE ledger for audit.

**Capacity Planning** -- Monitor GPU utilization trends via Prometheus federation. Adjust
globalMaxGPUs and per-tenant budgets based on demand. The 85% utilization target balances
cost efficiency with burst headroom.

**Incident Response** -- FleetRoutingPolicy fails over to healthy zone-local clusters
(never outside the zone). FleetScalingPolicy redistributes replicas respecting GPU
constraints. TenantProfile priority ensures high-priority tenants maintain service during
capacity-constrained failover. All actions are ledger-recorded for post-incident review.
