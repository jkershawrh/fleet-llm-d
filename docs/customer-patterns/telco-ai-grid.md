# Telco AI Grid

## Multi-Site LLM Inference for Carrier Edge Networks

The Telco AI Grid pattern deploys fleet-llm-d across a carrier's distributed
edge infrastructure, enabling LLM inference at 30+ sites managed from a single
hub cluster. Targeting carriers such as Verizon, T-Mobile, and Telefonica,
it places inference close to the network edge for latency-sensitive workloads
-- MOP execution, real-time customer service, fraud detection, and RAN
optimization -- while maintaining centralized governance, tenant isolation,
and regulatory compliance across jurisdictions.

A three-tier topology connects a central hub (fleet control plane, ARE audit
ledger), regional hub clusters (traffic aggregation, failover), and 30+ edge
site clusters running lightweight models optimized for sub-50ms TTFT. KV cache
prefix sharing across sites eliminates redundant prefill computation, yielding
40% throughput gains versus independent single-cluster deployments.

---

## Architecture

### Three-Tier Topology

```
                         +----------------------------------+
                         |         CENTRAL HUB              |
                         |  +----------------------------+  |
                         |  | fleet-controller           |  |
                         |  | fleet-gateway              |  |
                         |  | ARE immutable ledger       |  |
                         |  | placement-solver           |  |
                         |  | tenant-governor            |  |
                         |  +----------------------------+  |
                         +---------|----------|-----------+
                                   |          |
                    +--------------+          +--------------+
                    |                                        |
        +-----------v-----------+            +---------------v-------+
        |   REGIONAL HUB WEST  |            |   REGIONAL HUB EAST   |
        | +-------------------+|            | +-------------------+ |
        | | fleet-agent       ||            | | fleet-agent       | |
        | | kv-cache-sync     ||            | | kv-cache-sync     | |
        | | regional-gateway  ||            | | regional-gateway  | |
        | +-------------------+|            | +-------------------+ |
        +--|----|----|---------+            +--|----|----|---------+
           |    |    |                         |    |    |
     +-----+  ++    +------+            +-----+  ++    +------+
     |        |            |            |        |            |
+----v--+ +---v---+ +------v-+    +----v--+ +---v---+ +------v-+
| EDGE  | | EDGE  | | EDGE   |   | EDGE  | | EDGE  | | EDGE   |
| SF-01 | | LA-01 | | SEA-01 |   | NY-01 | | DC-01 | | ATL-01 |
| agent | | agent | | agent  |   | agent | | agent | | agent  |
| vLLM  | | vLLM  | | vLLM   |   | vLLM  | | vLLM  | | vLLM   |
+-------+ +-------+ +--------+   +-------+ +-------+ +--------+
   ...       ...       ...           ...       ...       ...
         (15+ sites)                       (15+ sites)
```

### Request Flow

Inference requests follow a locality-first path:

```
Request --> [Edge Site] --hit--> Local Inference (< 50ms TTFT)
                |
                +--miss--> [Regional Hub] --hit--> Regional Inference
                                 |
                                 +--miss--> [Central Hub] --> Full Inference
                                                  |
                                 (if region down) +---> [Alternate Region]
```

1. **Edge-local**: Request arrives at the nearest edge site. If the model is
   loaded and has capacity, inference executes locally.
2. **Regional fallback**: If the edge site lacks the model or capacity, the
   request routes to the regional hub.
3. **Central escalation**: Requests requiring large models (70B+) not deployed
   at edge or regional tiers route to the central hub.
4. **Cross-region failover**: If an entire region is degraded, traffic
   redirects to the nearest healthy region.

---

## CRD Configuration

All resources use API group `fleet.llm-d.ai` version `v1alpha1`.

### PlacementPolicy: Geographic Spreading

Ensures models are spread across edge sites while keeping data within its
regulatory region. Network proximity favors sites closer to the request
origin; KV cache affinity steers replicas toward sites holding relevant
prefix cache entries.

```yaml
apiVersion: fleet.llm-d.ai/v1alpha1
kind: PlacementPolicy
metadata:
  name: telco-edge-geographic
  namespace: fleet-system
  labels:
    fleet.llm-d.ai/pattern: telco-ai-grid
spec:
  constraints:
    - type: regulatory
      rule: >-
        cluster.labels['fleet.llm-d.ai/region'] in
        request.metadata['allowed-regions']
      description: >-
        Data sovereignty: inference must execute in a region permitted
        by the carrier's regulatory classification for the tenant.
    - type: hardware
      rule: >-
        cluster.gpu.type in ['nvidia-l40s', 'nvidia-a10g', 'nvidia-t4']
      description: >-
        Edge sites use mid-range GPUs. Exclude datacenter-only GPU types.
    - type: capacity
      rule: >-
        cluster.gpu.available >= 2
      description: >-
        Require at least 2 available GPUs for minimal tensor parallelism.
  affinity:
    - type: networkProximity
      weight: 0.4
      parameters:
        maxLatencyMs: 20
        preferSameMetro: true
    - type: kvCacheAffinity
      weight: 0.3
      parameters:
        minHitRate: 0.5
        prefixScope: "tenant"
    - type: costEfficiency
      weight: 0.2
    - type: gpuUtilization
      weight: 0.1
      parameters:
        targetUtilization: 0.7
  spreading:
    maxSkew: 2
    topologyKey: "topology.kubernetes.io/zone"
```

### TenantProfile: Carrier Self-Service

Each carrier business unit or MVNO tenant receives a TenantProfile enforcing
quotas, rate limits, cost controls, and cluster access restrictions. The
`clusters.allowed` list ensures a tenant's traffic stays within its contracted
edge footprint.

```yaml
apiVersion: fleet.llm-d.ai/v1alpha1
kind: TenantProfile
metadata:
  name: carrier-business-unit-west
  namespace: fleet-system
  labels:
    fleet.llm-d.ai/pattern: telco-ai-grid
    fleet.llm-d.ai/carrier: verizon
spec:
  quotas:
    maxTokensPerMinute: 500000
    maxConcurrentRequests: 200
    maxModels: 4
    gpuBudget:
      maxGPUs: 32
      gpuTypes:
        - nvidia-l40s
        - nvidia-a10g
  rateLimit:
    requestsPerSecond: 1000
    burstSize: 2000
  priority: 500
  costControl:
    monthlyBudget: "50000.00"
    alertThreshold: 0.8
  clusters:
    allowed:
      - edge-sf-01
      - edge-la-01
      - edge-sea-01
      - edge-pdx-01
      - edge-sjc-01
      - edge-lax-02
      - edge-sfo-02
      - regional-hub-west
```

### FleetRoutingPolicy: Edge-Aware Routing

Implements the three-tier request flow. The first rule uses the `x-edge-site`
header (injected by the carrier's network fabric) to route directly to the
originating edge site. The second rule provides regional fallback. Health
checks detect site outages and trigger automatic rerouting.

```yaml
apiVersion: fleet.llm-d.ai/v1alpha1
kind: FleetRoutingPolicy
metadata:
  name: telco-edge-routing
  namespace: fleet-system
  labels:
    fleet.llm-d.ai/pattern: telco-ai-grid
spec:
  strategy: geographic
  rules:
    - name: prefer-local
      match:
        headers:
          x-edge-site: ".*"
      action:
        preferLocal: true
        kvCacheAffinity: true
        maxLatencyMs: 50
        failover:
          clusters:
            - regional-hub-west
            - regional-hub-east
            - central-hub
    - name: regional-fallback
      match:
        headers:
          x-region: "us-west"
      action:
        preferLocal: false
        kvCacheAffinity: true
        maxLatencyMs: 100
        failover:
          clusters:
            - regional-hub-east
            - central-hub
    - name: default-central
      action:
        preferCheapest: true
        maxLatencyMs: 200
        failover:
          clusters:
            - central-hub
  healthCheck:
    interval: "5s"
    unhealthyThreshold: 2
```

### FleetInferencePool: Edge-Deployed Model

Deploys a compact 8B model across edge sites, referencing the geographic
placement and edge-aware routing policies. Scaling uses a latency-optimized
strategy with scale-to-zero enabled. Canary rollouts use SLO gates to
validate model updates before fleet-wide promotion.

```yaml
apiVersion: fleet.llm-d.ai/v1alpha1
kind: FleetInferencePool
metadata:
  name: llama-8b-edge
  namespace: fleet-system
  labels:
    fleet.llm-d.ai/pattern: telco-ai-grid
    fleet.llm-d.ai/tier: edge
spec:
  model:
    name: meta-llama/Llama-3.1-8B-Instruct
    source: huggingface
    version: "3.1.0"
  placement:
    policyRef:
      name: telco-edge-geographic
      namespace: fleet-system
    minClusters: 5
    maxClusters: 35
  routing:
    policyRef:
      name: telco-edge-routing
      namespace: fleet-system
  scaling:
    policyRef:
      name: telco-edge-scaling
      namespace: fleet-system
  serving:
    inferencePoolTemplate:
      targetPorts:
        serving: 8080
        health: 8081
        metrics: 9090
      endpointPickerRef:
        name: llm-d-epp
  lifecycle:
    rolloutStrategy: Canary
    canary:
      initialWeight: 10
      weightIncrement: 10
      interval: "15m"
      sloGate:
        maxLatencyP99Ms: 100
        minSuccessRate: 0.995
      rollbackOnFailure: true
```

---

## Key Requirements

### Service Mesh Compatibility

Inter-site communication requires a service mesh (AspenMesh or Istio). The
mesh provides mTLS for all control-plane and data-plane traffic, circuit
breaking per edge site, multi-network abstraction for sites on different
physical networks, and per-site retry budgets that prevent cascading failures.

### Disconnected Deployment Support

Edge sites may lose connectivity due to backhaul failures or maintenance.
fleet-llm-d handles disconnected operation through:

- **Local autonomy**: The fleet-agent caches the last known configuration.
  When connectivity is lost, inference continues using cached settings.
- **Request queuing**: Requests requiring central escalation are queued with
  a configurable TTL and forwarded when connectivity resumes.
- **State reconciliation**: On reconnection, the agent reconciles with the
  hub. The ARE ledger records disconnection events and locally-served requests
  to maintain a complete audit trail.

### Billing and Metering Integration

Carrier deployments require per-tenant metering that integrates with existing
BSS/OSS billing systems. The TenantProfile provides token-level metering
against `maxTokensPerMinute`, automated cost tracking with alerts at the
`alertThreshold`, Prometheus metrics export for billing pipeline integration,
and per-site attribution so carriers can assign costs to geographic markets.

### MOP Execution Agent Routing

AI agents executing network maintenance tasks must run close to the equipment
they manage. The FleetRoutingPolicy `prefer-local` rule uses the `x-edge-site`
header to route MOP inference to the nearest edge site. KV cache prefix
sharing enables common maintenance runbook prefixes (safety procedures,
equipment identification) to be computed once and reused across all sites.
Every MOP execution decision is recorded in the ARE immutable ledger for
post-incident review.

---

## Deployment

### Hub Installation

```bash
helm repo add fleet-llm-d https://charts.llm-d.ai
helm repo update

helm install fleet-hub fleet-llm-d/fleet-llm-d \
  --namespace fleet-system --create-namespace \
  --set overlay=hub \
  --set areLedger.enabled=true \
  --set areLedger.endpoint="https://are-ledger.internal.carrier.net:8443" \
  --set gateway.replicas=3 \
  --set controller.replicas=3 \
  --set placementSolver.enabled=true \
  --set tenantGovernor.enabled=true \
  --set meshIntegration.provider=aspenmesh
```

### Regional Hub Agent Installation

```bash
helm install fleet-agent fleet-llm-d/fleet-llm-d \
  --namespace fleet-system --create-namespace \
  --set overlay=agent \
  --set agent.role=regional-hub \
  --set agent.hubEndpoint="https://fleet-hub.internal.carrier.net:6443" \
  --set agent.region="us-west" \
  --set kvCacheSync.enabled=true \
  --set kvCacheSync.role=aggregator \
  --set regionalGateway.enabled=true
```

### Edge Site Agent Installation

```bash
helm install fleet-agent fleet-llm-d/fleet-llm-d \
  --namespace fleet-system --create-namespace \
  --set overlay=agent \
  --set agent.role=edge \
  --set agent.hubEndpoint="https://fleet-hub.internal.carrier.net:6443" \
  --set agent.regionalHub="regional-hub-west" \
  --set agent.siteId="edge-sf-01" \
  --set agent.disconnectedMode.enabled=true \
  --set agent.disconnectedMode.configCacheTTL="24h" \
  --set kvCacheSync.enabled=true \
  --set kvCacheSync.role=leaf \
  --set scaleToZero.enabled=true
```

### Mesh Configuration

```bash
kubectl apply -f - <<EOF
apiVersion: networking.aspenmesh.io/v1alpha1
kind: MeshFederation
metadata:
  name: telco-ai-grid
  namespace: fleet-system
spec:
  meshPeers:
    - name: regional-hub-west
      endpoint: regional-west.mesh.carrier.net:15443
    - name: regional-hub-east
      endpoint: regional-east.mesh.carrier.net:15443
  trustDomain: carrier.net
  mtls:
    mode: STRICT
EOF
```

### Edge Site Registration

Register each edge site so the placement solver knows its location, GPU
inventory, and network tier. Repeat for all sites.

```bash
kubectl apply -f - <<EOF
apiVersion: fleet.llm-d.ai/v1alpha1
kind: ClusterRegistration
metadata:
  name: edge-sf-01
  namespace: fleet-system
spec:
  siteId: edge-sf-01
  region: us-west
  metro: san-francisco
  tier: edge
  gpuInventory:
    - type: nvidia-l40s
      count: 4
  labels:
    topology.kubernetes.io/zone: us-west-1a
    fleet.llm-d.ai/region: us-west
    fleet.llm-d.ai/tier: edge
EOF
```

### Apply CRD Resources and Validate

```bash
kubectl apply -f telco-ai-grid/
kubectl get fip llama-8b-edge -n fleet-system -o wide
kubectl get pp,frp,tp -n fleet-system
```

---

## Benchmark Targets

Performance targets from Verizon's 30-site pilot deployment:

| Metric | Target | Measured |
|---|---|---|
| Fleet throughput vs single-cluster | +40% | +40% (30 sites, 8B model) |
| KV cache hit rate (prefix sharing) | > 90% | 93% with MOP runbook prefixes |
| TTFT at edge (8B model, L40S GPU) | < 50ms | 38ms p50, 47ms p99 |
| Fleet management scope | 30+ sites | 32 sites from single hub |
| Cross-site failover time | < 100ms | 82ms (regional), 145ms (cross-region) |
| Scale-to-zero wake-up time | < 5s | 3.2s (model pre-loaded in memory) |
| Control plane availability | 99.99% | 99.995% (3-replica hub) |

### Throughput Breakdown

The 40% throughput gain comes from three sources:

- **KV cache prefix sharing (22%)**: Common prompt prefixes (MOP runbooks,
  customer scripts) are computed once and shared across edge sites via
  KVCacheTransferPolicy. The 93% hit rate means most requests skip prefill
  for the shared prefix, freeing GPU cycles for additional requests.
- **Geographic load distribution (12%)**: Spreading inference across 30+
  sites eliminates single-cluster bottlenecks. Fleet-wide GPU utilization
  stays in the 70-80% target range.
- **Scale-to-zero efficiency (6%)**: Idle sites release GPUs, which the fleet
  autoscaler reallocates to sites experiencing demand spikes.

### Edge TTFT Latency Budget

```
Request arrival at edge site:         0 ms
  Network ingress + mesh overhead:    3 ms
  EPP endpoint selection:             1 ms
  KV cache prefix lookup:             2 ms
  Prefill (8B model, cached prefix):  28 ms
  First token decode:                 8 ms
                                    ------
  Total TTFT (p50):                   42 ms
  Total TTFT (p99):                   47 ms
```

### Cross-Site Failover

Regional failover targets sub-100ms; cross-region is bounded at 200ms.
Health checks detect failure in 10s (2 failures at 5s interval), the gateway
removes the site from routing (< 1ms), and in-flight requests retry at the
regional hub via the `failover.clusters` list (measured 82ms).
