# Deployment profiles

llm-d Fleet is one portable OSS project with composable deployment profiles.
The profiles do not create community, enterprise, or scale forks of the code.

## OSS core

The core includes fleet registration, capability inventory, exact-model
eligibility, tenant admission, placement and draining policy, health and
failure-domain state, provider reconciliation, the controller, agent, CLI,
APIs, tests, and generic Kubernetes packaging.

External governance, observability products, model weights, inference engines,
and environment-specific infrastructure are not prerequisites. The community
overlay is the smallest portable installation and is not itself a production
readiness claim.

## Routing adapter profile

Choose exactly one authoritative routing provider:

- `praxis` is the currently validated default.
- `llm-d-router` publishes the fleet-qualified provider set to the upstream
  Router discovery contract and is beta while that upstream contract evolves.
- `disabled` runs the fleet control plane without publishing routing state.

Adapters translate the same qualified provider set. They cannot relax fleet
policy, add an incompatible provider, or substitute a different physical
model.

## Production scale and HA profile

Production scale is an OSS deployment configuration, not a separate product.
It adds multiple stateless inference gateways and routing replicas, topology
spread and disruption budgets, external HA PostgreSQL, verified TLS and
credentials, bounded queues and timeouts, health convergence, capacity
certification, failure injection, observability, and rollback artifacts.

Every model advertised as HA requires compatible providers in at least two
failure domains. A model with one eligible provider remains available but is
reported as `degraded/non-HA`.

The `--production` flag is narrower than this profile: it selects the governed-
evidence production contract and therefore also requires signed proposal and
external-ledger dependencies.

## Governed evidence profile

This optional profile composes authenticated GCL DecisionPackages, an external
immutable ledger, DeepField observation signals, semantic classification, and
other ecosystem adapters. Configured evidence dependencies fail closed, but
their absence does not prevent OSS-core or non-governed scale deployments.

## Environment validation profile

Physical-cluster overlays, private endpoints, registry coordinates, certificate
material, raw capacity reports, and operational observations belong to a lab or
deployment repository. Sanitized topology-neutral conformance methodology and
results may be published with the OSS project.

Named physical validation environments are not architectural requirements or
supported names in the portable interface.

## Repository naming

The intended public name is **llm-d Fleet**, with repository name
`llm-d-fleet`, subject to upstream project approval. Until accepted into the
llm-d organization, documentation must describe it as a proposed llm-d
ecosystem component and must not imply official project status.
