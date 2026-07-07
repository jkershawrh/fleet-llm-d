# Fleet-Level Inference Orchestration for llm-d

## Authors

- J. Kershaw, Red Hat

## Summary

Propose a fleet-level orchestration layer above llm-d's within-cluster inference intelligence. This layer coordinates model placement, traffic routing, autoscaling, tenant governance, lifecycle management, observability, and KV cache state transfer across multiple Kubernetes clusters.

## Motivation

### Customer Demand

Fourteen enterprise engagements independently describe the same need: deploy, govern, and observe inference models across clusters from a central plane.

- **Verizon** requires multi-cluster mesh topology across 30+ edge sites with tenant isolation and usage metering.
- **AT&T** asks for a single pane of glass across their inference fleet with per-tenant cost controls.
- **Wells Fargo** needs multi-region failover with regulatory constraints on model placement.
- **Bank of America** named multi-cluster routing as their top priority.
- **T-Mobile** selected NVAIE + Rafay on tenant self-service — a competitive loss on this exact capability.

### Competitive Landscape

On June 23, 2026, two independent open-source projects launched targeting this layer: ModelPlane (Crossplane-based) and SkyPilot Endpoints (UC Berkeley). Both treat llm-d as settled within-cluster infrastructure and position themselves as the fleet-level control plane above it.

## Proposal

### Seven CRDs (fleet.llm-d.ai/v1alpha1)

| CRD | Purpose |
|---|---|
| FleetInferencePool | Model's fleet-wide deployment intent (wraps InferencePool) |
| PlacementPolicy | Where models can/should run (regulatory, hardware, cost) |
| FleetRoutingPolicy | Cross-cluster traffic routing rules |
| TenantProfile | Per-tenant quotas, rate limits, cost caps |
| FleetScalingPolicy | Fleet-wide autoscaling objectives |
| ModelLifecycle | SLO-gated canary rollouts across clusters |
| KVCacheTransferPolicy | Cross-cluster KV cache migration |

### Integration Points with llm-d

1. **InferencePool Exported condition** for multi-cluster discovery
2. **EPP headers** (x-llm-d-inference-objective, x-llm-d-inference-fairness-id) for fleet traffic management
3. **WVA metrics** for fleet-level autoscaling decisions
4. **InferenceModelRewrite** for fleet-level canary deployments
5. **KV-Events** for cross-cluster cache awareness

### External Integrations

- **ModelPack (CNCF model-spec)** — OCI model metadata for auto GPU sizing
- **ARE Immutable Ledger** — independent compliance infrastructure for tamper-evident decision audit trails

## Implementation Status

- 41/41 architectural claims proven
- 437+ tests, zero failures
- Deployed on OpenShift 4.22 with real Granite inference
- 7 ARE Ledger decision chains verified valid

## Proposed SIG

**SIG Fleet Orchestration** — multi-cluster inference coordination, fleet CRD design, cross-cluster routing, tenant governance at fleet scale.
