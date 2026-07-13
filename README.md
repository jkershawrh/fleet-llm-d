# fleet-llm-d

<!-- TODO: Add project logo -->
<!-- ![fleet-llm-d](docs/assets/logo.png) -->

**Fleet-level inference orchestration for [llm-d](https://github.com/llm-d), built for the Open Sovereign AI Cloud.**

fleet-llm-d extends llm-d from single-cluster inference to multi-cluster fleet operations. It provides a Go control plane, a Rust data plane, and a Next.js dashboard that together deliver model placement, cross-cluster routing, fleet autoscaling, observability, tenant governance, lifecycle management, and KV cache state transfer across heterogeneous GPU infrastructure.

[![License](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](LICENSE)
[![Go](https://img.shields.io/badge/Go-1.26+-00ADD8.svg)](https://go.dev/)
[![Rust](https://img.shields.io/badge/Rust-1.90+-DEA584.svg)](https://www.rust-lang.org/)
[![Tests](https://img.shields.io/badge/Tests-500%2B_passing-brightgreen.svg)](#testing)
[![Architecture](https://img.shields.io/badge/Arch_Tests-55%2F55-blue.svg)](#architectural-proof)
[![CI](https://img.shields.io/badge/CI-passing-brightgreen.svg)](#testing)

---

> **Maturity notice:** this repository currently has contract, unit, and
> prototype integration evidence. It is **not** promoted to Blue or Gold under
> the current evidence rubric. Historical mock/demo results below are retained
> as development evidence and do not prove assembled ModelPlane, llm-d,
> multi-cluster, security-audit, or 72-hour-soak behavior.

## Why fleet-llm-d

llm-d solves single-cluster inference scheduling, but enterprises operating across dozens of clusters -- edge sites, regulated regions, air-gapped sovereign zones -- hit a coordination gap that no upstream project addresses. Customers in telco, financial services, and sovereign cloud have asked for fleet-wide placement, routing, and compliance controls that respect data residency and hardware topology. fleet-llm-d delivers those capabilities through a declarative CRD-driven control plane, a high-performance Rust data plane, and integrations with the broader ecosystem including ModelPack and the ARE Immutable Ledger.

## Architecture

```
                         ┌─────────────────────────────────┐
                         │        fleet-controller          │
                         │  (Go control plane, CRD-driven)  │
                         │  placement | routing | scaling   │
                         │  lifecycle | tenant | kvcache    │
                         └──────────┬──────────┬───────────┘
                                    │          │
                     ┌──────────────┘          └──────────────┐
                     │        Fleet Network (Kafka/gRPC)       │
                     └──┬──────────┬──────────┬──────────┬────┘
                        │          │          │          │
                   ┌────▼───┐ ┌───▼────┐ ┌───▼────┐ ┌───▼────┐
                   │Cluster │ │Cluster │ │Cluster │ │Cluster │
                   │  A     │ │  B     │ │  C     │ │  N     │
                   │        │ │        │ │        │ │        │
                   │ agent  │ │ agent  │ │ agent  │ │ agent  │
                   │gateway │ │gateway │ │gateway │ │gateway │
                   │llm-d   │ │llm-d   │ │llm-d   │ │llm-d   │
                   └────────┘ └────────┘ └────────┘ └────────┘

  Binaries:  fleet-controller, fleetctl (Go)
             fleet-agent, fleet-gateway (Rust)
  Dashboard: Next.js (TypeScript)
```

## Seven Capabilities

| # | Capability | Description | Status |
|---|-----------|-------------|--------|
| 1 | **Model Placement** | Solver and scorer assign models to clusters based on GPU topology, locality, and policy constraints. | Contract/unit evidence |
| 2 | **Cross-Cluster Routing** | Balancer and policy engine route inference requests across clusters with latency-aware load distribution. | Contract/unit evidence |
| 3 | **Fleet Autoscaling** | Collector and optimizer scale model replicas across the fleet using aggregated metrics from all clusters. | Contract/unit evidence |
| 4 | **Multi-Cluster Observability** | Unified metrics pipeline aggregates per-cluster Prometheus data into fleet-wide dashboards and alerts. | Partial prototype |
| 5 | **Tenant Governance** | Metering and quota enforcement give platform teams per-tenant controls over GPU-hours and throughput. | Contract/unit evidence |
| 6 | **Lifecycle Management** | Rollout controller orchestrates model version upgrades across clusters with the 5-stage production gate model. | Contract/unit evidence |
| 7 | **KV Cache State Transfer** | Transfers KV cache state between clusters during migration, rescheduling, or failover to minimize cold-start latency. | In-process prototype only |

### Custom Resource Definitions

fleet-llm-d defines seven CRDs that drive all fleet behavior declaratively:

| CRD | Purpose |
|-----|---------|
| `FleetInferencePool` | Defines a fleet-wide pool of model replicas spanning multiple clusters. |
| `PlacementPolicy` | Constrains where models may be placed (topology, region, compliance). |
| `FleetRoutingPolicy` | Configures cross-cluster routing rules, weights, and failover. |
| `TenantProfile` | Declares tenant quotas, metering rules, and priority classes. |
| `FleetScalingPolicy` | Sets autoscaling targets, thresholds, and per-cluster bounds. |
| `ModelLifecycle` | Specifies rollout strategy and production gate criteria. |
| `KVCacheTransferPolicy` | Governs when and how KV cache state is migrated between clusters. |

## Integrations

### ModelPack (CNCF model-spec)

fleet-llm-d consumes [ModelPack](https://github.com/model-spec) artifacts -- OCI-packaged models with structured metadata -- as its canonical model format. The `modelpack` package resolves model references, validates signatures, and extracts hardware requirements used by the placement solver.

### Standalone Immutable Ledger

[are-immutable-ledger](https://github.com/jkershawrh/are-immutable-ledger) is the
independent audit spine for this ecosystem. It stores hash-chained fleet, GCL,
and DeepField evidence and issues portable proof receipts. Receipts prove that
an entry was recorded; they are not credentials and never authorize a fleet
mutation. The ledger-owned `are.ledger.v1.ImmutableLedgerService` gRPC contract
is canonical. `pkg/ledger` also implements the repository's optional `/api/*`
REST gateway for explicit compatibility/development deployments.
The controller fails startup if `grpc` is selected today; it will advertise
that mode only after the ledger-owned protobuf is consumed through a generated,
pinned Go client. It never falls back to in-memory receipts for a configured
external-ledger failure.

### ModelPlane Integration

fleet-llm-d sits on top of [ModelPlane](https://github.com/modelplane) as the operations layer in a three-layer stack:

```
  ┌──────────────────────────────────────────┐
  │            fleet-llm-d                   │  Operations layer
  │  placement | routing | scaling | cost    │  (this project)
  │  tenant | lifecycle | observability      │
  ├──────────────────────────────────────────┤
  │            ModelPlane                     │  Infrastructure layer
  │  ModelDeployment | ModelCluster           │  (Crossplane-based)
  │  cluster lifecycle | resource mgmt       │
  ├──────────────────────────────────────────┤
  │            llm-d                         │  Inference layer
  │  EPP | WVA | KV cache | prefill/decode   │  (within-cluster)
  └──────────────────────────────────────────┘
```

The `modelplane` package (`pkg/modelplane/`) provides six integration points: CRD consumption (reading ModelDeployment and ModelCluster resources), policy injection (annotating ModelDeployments with fleet placement decisions), cost integration (feeding GPU pricing into fleet cost projections), compliance bridge (forwarding ModelPlane events to the standalone immutable ledger), routing integration (using ModelCluster health for traffic decisions), and scaling integration (coordinating fleet autoscaling with ModelPlane resource limits). Three API endpoints expose ModelPlane state: `/api/v1/modelplane/clusters`, `/api/v1/modelplane/deployments`, and `/api/v1/modelplane/cost/{deployment}`.

**Prototype evidence.** The checked-in demo used `cmd/modelplane-mock/` to
exercise the watcher and cost paths with CRD-shaped fixtures. That proves the
mock contract path only. It is not evidence of the pinned, official ModelPlane
provider, Gateway API ownership, or observed multi-cluster actuation.

The core ecosystem spine is `deepfield-fleet -> governed-cognitive-loop ->
fleet-llm-d -> are-immutable-ledger`: DeepField owns observations and
forecasts, GCL owns signed and falsified proposals, fleet owns admission,
authorization, desired/observed state, and actuation, and the ledger owns
tamper-evident evidence. ModelPlane and llm-d remain infrastructure and
within-cluster inference providers below the fleet boundary.

### Governed Cognitive Loop

The [governed-cognitive-loop](https://github.com/jkershawrh/governed-cognitive-loop) sits above fleet-llm-d as the governed autonomy layer. It receives classifications from deepfield-fleet, derives constraints from evidence, optimizes under hard constraints, challenges every plan through a falsification gate, and sends typed intents to fleet-llm-d only when the action survives all checks.

fleet-llm-d evaluates received intents against its CRD-defined policies before actuating. The GCL governs the decision; fleet-llm-d governs the execution.

GCL submits signed, expiry-bounded `DecisionPackage` proposals to the v2 intent
boundary. A submission acknowledgement is not execution. Fleet admission and
approval policy determines whether an operation may actuate, while the
standalone immutable ledger records admission and outcome evidence without
granting authority.

Production v2 admission fails closed unless the request is a verified
`application/cloudevents+json` GCL DecisionPackage. The unsigned
`application/json` shape is self-asserted development/operator compatibility
only and is disabled by default. It can be enabled deliberately with
`--allow-operator-json-intents` or
`FLEET_ALLOW_OPERATOR_JSON_INTENTS=true`; Helm exposes the same switch as
`controller.allowOperatorJSONIntents` and defaults it to `false`.

### Cost Model

fleet-llm-d includes a full cost model (`pkg/cost/`) for GPU inference economics:

- **GPU Pricing** -- Pricing table covering 6 GPU types (A100-40GB, A100-80GB, H100-80GB, H200-141GB, B200-192GB, MI300X-192GB) across 3 tiers (on-demand, reserved, spot).
- **Tokenomics** -- Cost-per-million-tokens calculation per model, factoring GPU type, utilization, and throughput.
- **Chargeback** -- Per-tenant cost attribution reports for enterprise billing integration.
- **Budget Alerts** -- Configurable alert thresholds on tenant and fleet-wide GPU spend with projection-based early warning.

Six API endpoints: `/api/v1/cost/pricing`, `/api/v1/cost/tokenomics/{model}`, `/api/v1/cost/chargeback/{tenant}`, `/api/v1/cost/projection`, `/api/v1/cost/savings`, `/api/v1/cost/alerts`.

## Security

- **Authentication**: HMAC-SHA256 bearer tokens with role-based access (admin, operator, viewer, tenant)
- **Rate Limiting**: Per-IP and per-tenant token bucket middleware
- **TLS**: Optional HTTPS via `--tls-cert` and `--tls-key` flags
- **RBAC**: Least-privilege controller and agent roles plus fleet-viewer and fleet-tenant-admin roles
- **Network Policies**: Default-deny with explicit allowlists per component
- **Container Hardening**: UBI base images, non-root (UID 65534), read-only filesystem, drop ALL capabilities
- **Webhook Validation**: Admission webhook rejects invalid CRD specs
- **Audit Trail**: Auth failures and RBAC denials recorded as evidence in the standalone immutable ledger

## Quick Start

### One-Click Deploy (OpenShift)

```bash
./hack/deploy-demo.sh \
  --cluster-url https://api.mycluster.example.com:6443 \
  --token $(oc whoami -t) \
  --ledger-url http://ledger-gateway:28099
```

### Local Development

```bash
# Prerequisites: Go 1.26+, Rust 1.90+, podman or docker

# Build binaries
make build-go          # → bin/fleet-controller, bin/fleetctl

# Start the controller (in-memory mode, no external deps)
./bin/fleet-controller --port 8080

# Register a cluster
./bin/fleetctl --server http://localhost:8080 clusters register \
  --id my-cluster --name "My Cluster" --region us-east

# View the test matrix
./bin/fleetctl matrix --format table
```

## Customer Examples

Ready-to-apply CRD examples for specific deployment patterns:

| Pattern | Directory | Key Features |
|---|---|---|
| **Telco AI Grid** | [`examples/telco-edge/`](examples/telco-edge/) | 30+ edge sites, geographic routing, 50ms latency target |
| **Financial Services** | [`examples/financial-services/`](examples/financial-services/) | Regulatory data residency, SLO-gated canary, ARE ledger compliance |
| **Sovereign Cloud** | [`examples/sovereign-cloud/`](examples/sovereign-cloud/) | Air-gapped zones, GPU-as-a-Service multi-tenancy, scale-to-zero |

## Deployment Modes

| Mode | Description | Details |
|------|-------------|---------|
| **Hub** | RHACM-style hub managing spoke clusters; one active controller is enforced until leader election exists. | See [`deploy/kustomize/overlays/hub/`](deploy/kustomize/overlays/hub/) |
| **Standalone** | Single-node development/CI deployment with convenience dependencies; not a production default. | See [`deploy/kustomize/overlays/standalone/`](deploy/kustomize/overlays/standalone/) |
| **Federated** | Peer-to-peer mesh where multiple fleet-controllers coordinate as equals. | See [`deploy/kustomize/overlays/federated/`](deploy/kustomize/overlays/federated/) |

The [Kustomize deployment guide](deploy/kustomize/README.md) and
[Helm chart guide](charts/fleet-llm-d/README.md) document the controller,
gateway, and agent port contracts, required cluster identity, disruption
budgets, and production-safe external dependency configuration.

## Dashboard

<!-- TODO: Add screenshot -->
<!-- ![Dashboard](docs/assets/dashboard-screenshot.png) -->

The fleet-llm-d dashboard is a Next.js (TypeScript) application providing fleet-wide visibility and management.

**Pages:**

1. **Overview** -- Fleet health summary, aggregate GPU utilization, active model count.
2. **Clusters** -- Per-cluster status, capacity, and connectivity.
3. **Models** -- Model inventory, placement map, and version history.
4. **Tenants** -- Tenant quota usage, metering dashboards, and policy editor.
5. **Rollouts** -- Active and historical rollouts with production gate status.
6. **Compliance** -- ARE Ledger audit trail, compliance posture, and attestation records.
7. **Test Matrix** -- Cross-cluster test results, compatibility matrix, and gate progression.

## Testing

```bash
make test              # Run all tests
make test-unit         # Unit tests (22 Go packages + 5 Rust crates)
make test-bdd          # BDD scenarios (63 passing, 8 suites)
make test-contracts    # Contract tests (proto + OpenAPI validation)
make test-e2e          # End-to-end tests (requires running infrastructure)
```

```bash
# Architecture proof: 50 claims about how the system works
go test -tags=architecture ./test/architecture/...

# Security tests: auth, rate limiting, webhook validation
go test -tags=security ./test/security/...

# Compliance: audit trail completeness
go test -tags=compliance ./test/compliance/...

# Soak test: sustained load for configurable duration
./test/soak/run-soak.sh --duration 7200 --rps 10
```

### Architectural Proof

55 architectural assertions are exercised by tests in `test/architecture/`.
These tests are design evidence; they do not by themselves prove assembled
runtime behavior:

| Category | Claims | Method | What's Proven |
|---|---|---|---|
| Reconciliation | 5 | EDD | Webhook → solver → phase transitions → events |
| Routing | 6 | TDD | Model selection, latency, failover, header injection |
| Tenant Governance | 5 | TDD | Quota enforcement, budget caps, multi-tenant isolation |
| Lifecycle | 5 | TDD | Canary, SLO gates, rollback |
| Autoscaling | 4 | TDD | Scale up/down, GPU cap, cross-cluster migration |
| Compliance | 7 | CDD | Every decision → ARE ledger, chain verification |
| Event Flow | 4 | EDD | Pub/sub + HTTP external delivery |
| Multi-Cluster | 3 | TDD | Cross-cluster routing, failover, multi-cluster placement |
| Security | 2 | TDD | Rate limiting, webhook validation |
| Cost Model | 4 | TDD | GPU pricing accuracy, tokenomics calculation, chargeback aggregation, budget alerts |
| ModelPlane | 5 | TDD | CRD consumption, policy injection, cost integration, compliance bridge, routing integration |

### Test Harness (Demo Cluster)

The historical demo harness recorded nine suites against one OpenShift demo
deployment. Those results remain useful regression data, but they are not the
required hub-plus-two-spoke release-candidate gate.

| Suite | Result | Highlights |
|-------|--------|------------|
| Smoke | 24/24 pass | All 16 endpoints healthy |
| Stress | Pass | Survived 500 concurrent goroutines, no breaking point |
| Pressure | 4/4 pass | Concurrent writes, race detection, rapid register/deregister 1000x |
| Chaos | 8/8 pass | 1MB body, invalid JSON, unicode, null bytes, burst 1000 |
| Red Team | 11/11 pass | Duplicate registration returns 409 Conflict |
| Latency | Pass | health p50=0.4ms, auth-reads p50=0.45ms, auth-writes p50=0.44ms |
| Throughput | Pass | healthz 2,000 rps, GET clusters 812 rps, POST clusters 2,000 rps |
| Soak | Pass | 30 min, 15,950 requests, 0 errors, 0.00% error rate |
| Security | Pass | TLS enforced, HTTP rejected, 0 Go CVEs (Trivy) |

**Go microbenchmarks:** Token generation 2.9M ops/s, token validation 2.0M ops/s, routing decision 19.5M ops/s.

See [`test/harness/`](test/harness/) for the harness source and [`test/benchmarks/reports/benchmark-results.md`](test/benchmarks/reports/benchmark-results.md) for full results.

### Production Gate Model

| Stage | Gate | Criteria | Status |
|-------|------|----------|--------|
| 0 | **Red** | Interfaces defined and executable tests authored | Passed |
| 1 | **Yellow** | Unit, BDD, and contract tests pass | Passed for the current development slice |
| 2 | **Green** | Three-cluster Kind integration passes for the capability | Not yet evidenced |
| 3 | **Blue** | Real hub + two OpenShift spokes, performance, and chaos gates pass | Not yet evidenced |
| 4 | **Gold** | All seven capabilities, 72-hour soak, signed external evidence, and no critical security findings | Not promoted |

See [`test/matrix/matrix.yaml`](test/matrix/matrix.yaml) and [`test/matrix/rubric.yaml`](test/matrix/rubric.yaml).

## Customer Deployment Patterns

| Pattern | Example Customers | Profile | Reference |
|---------|-------------------|---------|-----------|
| **Telco** | Telco Edge Provider, Mobile Network Operator | 30+ edge sites, latency-sensitive placement, distributed GPU pools. | [`docs/customer-patterns/telco-ai-grid.md`](docs/customer-patterns/telco-ai-grid.md) |
| **Financial** | Financial Services Provider, Global Banking Partner | Multi-region regulatory constraints, strict tenant isolation, audit trails. | [`docs/customer-patterns/financial-services.md`](docs/customer-patterns/financial-services.md) |
| **Sovereign** | OSAC partners | Air-gapped deployment, data residency enforcement, ARE Ledger integration. | [`docs/customer-patterns/sovereign-cloud.md`](docs/customer-patterns/sovereign-cloud.md) |

## Project Structure

```
fleet-llm-d/
├── api/
│   └── crds/                    # 7 CRD definitions
├── cmd/
│   ├── fleet-controller/        # Go control plane binary
│   ├── fleetctl/                # CLI tool
│   └── modelplane-mock/         # ModelPlane mock API server
├── pkg/
│   ├── placement/               # solver, scorer
│   ├── routing/                 # balancer, policy
│   ├── autoscaling/             # collector, optimizer
│   ├── lifecycle/               # rollout
│   ├── tenant/                  # metering, quota
│   ├── observability/           # metrics
│   ├── kvcache/                 # transfer
│   ├── modelpack/               # CNCF model-spec integration
│   ├── ledger/                  # ARE Ledger client
│   ├── modelplane/              # ModelPlane integration (adapter, watcher, policy injector)
│   ├── cost/                    # GPU pricing, tokenomics, chargeback, budget alerts
│   ├── store/                   # events, postgres
│   ├── cluster/                 # client
│   └── apis/                    # generated API types
├── crates/
│   ├── fleet-agent/             # Rust per-cluster agent
│   ├── fleet-gateway/           # Rust request gateway
│   ├── fleet-common/            # Shared Rust types
│   ├── fleet-ledger/            # Rust ledger integration
│   └── kv-transfer/             # KV cache transfer engine
├── web/                         # Next.js (TypeScript) UI
├── deploy/
│   ├── kustomize/overlays/      # hub, standalone, federated
│   ├── docker/                  # Dockerfiles (UBI base, non-root)
│   └── demo-cluster/             # Demo cluster deployment manifests
├── examples/                    # Customer CRD examples (Telco, Financial Services, Sovereign)
├── workflows/                   # Deployment workflow definitions
├── docker-compose.yml           # Local dev infrastructure
├── docs/
│   ├── whitepaper/              # Architecture whitepaper
│   ├── customer-patterns/       # Telco, Financial, Sovereign patterns
│   ├── demo/                    # 15-minute demo script
│   └── proposals/               # llm-d upstream SIG proposal
├── hack/
│   ├── deploy-demo.sh           # One-click deployment script
│   └── local-dev.sh             # Kind multi-cluster dev setup
└── test/
    ├── architecture/            # 50 architectural proof tests
    ├── bdd/                     # 63 BDD scenario tests
    ├── compliance/              # Audit trail completeness
    ├── contracts/               # Proto + OpenAPI validation
    ├── security/                # Auth integration tests
    ├── soak/                    # Sustained load test harness
    └── benchmarks/              # Workloads + scenarios
```

## REST API

The fleet controller exposes 27 REST endpoints. See [`api/openapi/fleet-api.yaml`](api/openapi/fleet-api.yaml) for the complete OpenAPI 3.1 specification.

## Infrastructure

| Component | Purpose |
|-----------|---------|
| PostgreSQL | Primary state store for fleet configuration and placement data. |
| Kafka (AMQ Streams) | Event bus for cross-cluster coordination and audit event streaming. |
| Redis | Caching layer for routing decisions and metrics aggregation. |
| Prometheus + Grafana | Monitoring and dashboarding for fleet-wide observability. |
| ARE Ledger (separate network) | Independent compliance ledger with own PostgreSQL on `are-ledger-net`. |

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) for development setup, coding standards, and contribution guidelines.

<!-- TODO: CONTRIBUTING.md is not yet written. -->

## License

This project is licensed under the [Apache License 2.0](LICENSE).

```
Copyright 2026 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
```
