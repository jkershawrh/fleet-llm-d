# fleet-llm-d

<!-- TODO: Add project logo -->
<!-- ![fleet-llm-d](docs/assets/logo.png) -->

**Fleet-level inference orchestration for [llm-d](https://github.com/llm-d), built for the Open Sovereign AI Cloud.**

fleet-llm-d extends llm-d from single-cluster inference to multi-cluster fleet operations. It provides a Go control plane, a Rust data plane, and a Next.js dashboard that together deliver model placement, cross-cluster routing, fleet autoscaling, observability, tenant governance, lifecycle management, and KV cache state transfer across heterogeneous GPU infrastructure.

[![License](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](LICENSE)
[![Go](https://img.shields.io/badge/Go-1.26+-00ADD8.svg)](https://go.dev/)
[![Rust](https://img.shields.io/badge/Rust-1.90+-DEA584.svg)](https://www.rust-lang.org/)
[![Tests](https://img.shields.io/badge/Tests-450%2B_passing-brightgreen.svg)](#testing)
[![Architecture](https://img.shields.io/badge/Arch_Proofs-41%2F41-blue.svg)](#architectural-proof)
[![CI](https://img.shields.io/badge/CI-passing-brightgreen.svg)](#testing)

---

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
| 1 | **Model Placement** | Solver and scorer assign models to clusters based on GPU topology, locality, and policy constraints. | Active |
| 2 | **Cross-Cluster Routing** | Balancer and policy engine route inference requests across clusters with latency-aware load distribution. | Active |
| 3 | **Fleet Autoscaling** | Collector and optimizer scale model replicas across the fleet using aggregated metrics from all clusters. | Active |
| 4 | **Multi-Cluster Observability** | Unified metrics pipeline aggregates per-cluster Prometheus data into fleet-wide dashboards and alerts. | Active |
| 5 | **Tenant Governance** | Metering and quota enforcement give platform teams per-tenant controls over GPU-hours and throughput. | Active |
| 6 | **Lifecycle Management** | Rollout controller orchestrates model version upgrades across clusters with the 5-stage production gate model. | Active |
| 7 | **KV Cache State Transfer** | Transfers KV cache state between clusters during migration, rescheduling, or failover to minimize cold-start latency. | Active |

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

### ARE Immutable Ledger

The ARE Immutable Ledger is an **independent shared compliance platform** that runs on a separate network (`are-ledger-net`) with its own PostgreSQL instance. fleet-llm-d publishes audit events -- placement decisions, scaling actions, model deployments -- to the ledger through the `are-gateway`. The ledger provides tamper-evident records for regulatory and sovereign compliance. The `ledger` package in fleet-llm-d handles event submission and verification.

## Security

- **Authentication**: HMAC-SHA256 bearer tokens with role-based access (admin, operator, viewer, tenant)
- **Rate Limiting**: Per-IP and per-tenant token bucket middleware
- **TLS**: Optional HTTPS via `--tls-cert` and `--tls-key` flags
- **RBAC**: 3 Kubernetes ClusterRoles (fleet-controller, fleet-viewer, fleet-tenant-admin)
- **Network Policies**: Default-deny with explicit allowlists per component
- **Container Hardening**: UBI base images, non-root (UID 65534), read-only filesystem, drop ALL capabilities
- **Webhook Validation**: Admission webhook rejects invalid CRD specs
- **Audit Trail**: Auth failures and RBAC denials recorded to ARE ledger

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
| **Telco AI Grid** | [`examples/verizon-edge/`](examples/verizon-edge/) | 30+ edge sites, geographic routing, 50ms latency target |
| **Financial Services** | [`examples/wells-fargo/`](examples/wells-fargo/) | Regulatory data residency, SLO-gated canary, ARE ledger compliance |
| **Sovereign Cloud** | [`examples/sovereign-cloud/`](examples/sovereign-cloud/) | Air-gapped zones, GPU-as-a-Service multi-tenancy, scale-to-zero |

## Deployment Modes

| Mode | Description | Details |
|------|-------------|---------|
| **Hub** | RHACM-style hub with 3-replica HA control plane managing spoke clusters. | See [`deploy/kustomize/overlays/hub/`](deploy/kustomize/overlays/hub/) |
| **Standalone** | Single-node deployment for development, CI, or small-scale production. | See [`deploy/kustomize/overlays/standalone/`](deploy/kustomize/overlays/standalone/) |
| **Federated** | Peer-to-peer mesh where multiple fleet-controllers coordinate as equals. | See [`deploy/kustomize/overlays/federated/`](deploy/kustomize/overlays/federated/) |

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
make test-unit         # Unit tests (19 Go packages + 5 Rust crates)
make test-bdd          # BDD scenarios (63 passing, 8 suites)
make test-contracts    # Contract tests (proto + OpenAPI validation)
make test-e2e          # End-to-end tests (requires running infrastructure)
```

```bash
# Architecture proof — 41 claims about how the system works
go test -tags=architecture ./test/architecture/...

# Security tests — auth, rate limiting, webhook validation
go test -tags=security ./test/security/...

# Compliance — audit trail completeness
go test -tags=compliance ./test/compliance/...

# Soak test — sustained load for configurable duration
./test/soak/run-soak.sh --duration 7200 --rps 10
```

### Architectural Proof

41 architectural claims are proven by tests in `test/architecture/`:

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

### Production Gate Model

| Stage | Gate | Criteria | Status |
|-------|------|----------|--------|
| 0 | **Red** | Interfaces defined, tests written (failing) | Passed |
| 1 | **Yellow** | Unit + BDD + contract tests pass | Passed |
| 2 | **Green** | Integration + soak tests pass, benchmarks within 2x | Passed |
| 3 | **Blue** | Multi-cloud E2E, benchmarks meet target, 72hr soak, rubric ≥80 | **Current (83.45)** |
| 4 | **Gold** | Customer deployment validated, SLO met 30 days | Pending |

See [`test/matrix/matrix.yaml`](test/matrix/matrix.yaml) and [`test/matrix/rubric.yaml`](test/matrix/rubric.yaml).

## Customer Deployment Patterns

| Pattern | Example Customers | Profile | Reference |
|---------|-------------------|---------|-----------|
| **Telco** | Telco Provider A, Mobile Network Provider | 30+ edge sites, latency-sensitive placement, distributed GPU pools. | [`docs/customer-patterns/telco-ai-grid.md`](docs/customer-patterns/telco-ai-grid.md) |
| **Financial** | Financial Services Provider A, Financial Services Provider B | Multi-region regulatory constraints, strict tenant isolation, audit trails. | [`docs/customer-patterns/financial-services.md`](docs/customer-patterns/financial-services.md) |
| **Sovereign** | OSAC partners | Air-gapped deployment, data residency enforcement, ARE Ledger integration. | [`docs/customer-patterns/sovereign-cloud.md`](docs/customer-patterns/sovereign-cloud.md) |

## Project Structure

```
fleet-llm-d/
├── api/
│   └── crds/                    # 7 CRD definitions
├── cmd/
│   ├── fleet-controller/        # Go control plane binary
│   └── fleetctl/                # CLI tool
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
│   └── dev-cluster-1/                  # dev-cluster-1 cluster deployment manifests
├── examples/                    # Customer CRD examples (Telco Provider A, WF, Sovereign)
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
    ├── architecture/            # 41 architectural proof tests
    ├── bdd/                     # 63 BDD scenario tests
    ├── compliance/              # Audit trail completeness
    ├── contracts/               # Proto + OpenAPI validation
    ├── security/                # Auth integration tests
    ├── soak/                    # Sustained load test harness
    └── benchmarks/              # Workloads + scenarios
```

## REST API

The fleet controller exposes 15 REST endpoints. See [`api/openapi/fleet-api.yaml`](api/openapi/fleet-api.yaml) for the complete OpenAPI 3.1 specification.

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
