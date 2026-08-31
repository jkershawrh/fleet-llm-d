# Sovereign Cloud Deployment Pattern

> **Evidence status:** Reference architecture. Air-gapped and standalone
> deployment modes are structurally supported by the codebase (in-memory
> stores, no external dependencies required). Multi-tenant GPU-as-a-Service
> is a design target based on unit-tested TenantProfile enforcement.

## Context

Sovereign cloud deployments require all infrastructure, data, and governance
to operate within a single sovereign boundary. Air-gapped zones have no
external network access. Multiple tenants share GPU resources with strict
isolation.

## Architecture

Standalone deployment mode: the fleet-controller runs with in-memory stores
(or local PostgreSQL) and no external dependencies. The entire
observe-govern-act-prove pipeline operates within the sovereign boundary.

## fleet-llm-d Capabilities Used

| Requirement | CRD / Capability | Notes |
|---|---|---|
| Air-gapped operation | Standalone deployment mode | Controller + PostgreSQL on single node, no external deps |
| Data residency | PlacementPolicy | Label-selector constraints prevent cross-zone placement |
| GPU-as-a-Service | TenantProfile | Per-tenant GPU quotas and budget controls |
| Scale-to-zero | FleetScalingPolicy | Cooldown-based scale-to-zero for idle models |
| Compliance evidence | ARE ledger | Runs within the sovereign boundary on its own database |

## Example CRD

See [`examples/sovereign-cloud/fleet-resources.yaml`](../../examples/sovereign-cloud/fleet-resources.yaml) for
PlacementPolicy with sovereignty constraints, TenantProfile with GPU quotas,
and FleetScalingPolicy with scale-to-zero.

## Design Targets (Not Yet Validated)

- Complete fleet operation within air-gapped sovereign boundary
- Multi-tenant GPU-as-a-Service with per-tenant cost controls
- Scale-to-zero for cost optimization in low-utilization periods
- Intel TDX confidential inference (requires BIOS enablement)
