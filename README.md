# fleet-llm-d

<!-- TODO: Add project logo -->
<!-- ![fleet-llm-d](docs/assets/logo.png) -->

**Fleet-level inference orchestration for [llm-d](https://github.com/llm-d), built for the Open Sovereign AI Cloud.**

fleet-llm-d extends llm-d from single-cluster inference to multi-cluster fleet operations. It provides a Go control plane, a Rust data plane, and a Next.js dashboard that together deliver model placement, cross-cluster routing, fleet autoscaling, observability, tenant governance, lifecycle management, and KV cache state transfer across heterogeneous GPU infrastructure.

[![License](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](LICENSE)
[![Go](https://img.shields.io/badge/Go-1.23+-00ADD8.svg)](https://go.dev/)
[![Rust](https://img.shields.io/badge/Rust-1.79+-DEA584.svg)](https://www.rust-lang.org/)
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

## Quick Start

### Prerequisites

- Docker and Docker Compose
- Go 1.23+
- Rust 1.79+
- `kubectl` configured for at least one cluster

### Bring up the infrastructure

```bash
# Start PostgreSQL, Redis, Kafka, Prometheus, Grafana,
# ARE Ledger (DB + service + gateway)
docker compose up -d
```

This starts the following services: `postgres`, `redis`, `kafka`, `prometheus`, `grafana`, `are-ledger-db`, `are-ledger`, `are-gateway`.

### Build binaries

```bash
# Go binaries (fleet-controller, fleetctl)
make build

# Rust binaries (fleet-agent, fleet-gateway)
make build-rust
```

### Register a cluster and deploy a model

```bash
# Register a cluster with the fleet
fleetctl cluster register --name edge-site-01 \
  --kubeconfig ~/.kube/edge-site-01.yaml

# Deploy a model across the fleet
fleetctl model deploy --name llama-3-70b \
  --pool default --replicas 6

# Check fleet status
fleetctl status
```

## Deployment Modes

| Mode | Description | Details |
|------|-------------|---------|
| **Hub** | RHACM-style hub with 3-replica HA control plane managing spoke clusters. | See `deploy/overlays/hub/` |
| **Standalone** | Single-node deployment for development, CI, or small-scale production. | See `deploy/overlays/standalone/` |
| **Federated** | Peer-to-peer mesh where multiple fleet-controllers coordinate as equals. | See `deploy/overlays/federated/` |

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
# Run all tests
make test

# Unit tests only
make test-unit

# BDD / behavior-driven tests
make test-bdd

# Contract tests (API compatibility)
make test-contracts

# End-to-end tests (requires running infrastructure)
make test-e2e
```

### Benchmarks

```bash
make bench-quick       # Fast smoke benchmarks
make bench-standard    # Standard benchmark suite
make bench-full        # Full benchmark suite (long-running)
```

### Production Gate Model

Model rollouts follow a five-stage gate progression:

| Stage | Gate | Criteria |
|-------|------|----------|
| 1 | **Red** | Initial deployment, not yet validated. |
| 2 | **Yellow** | Unit and contract tests pass. |
| 3 | **Green** | BDD and integration tests pass. |
| 4 | **Blue** | Canary traffic validated in staging clusters. |
| 5 | **Gold** | Full production approval, all compliance checks cleared. |

See `docs/test-matrix.md` for the complete test matrix and gate criteria.

## Customer Deployment Patterns

| Pattern | Example Customers | Profile | Reference |
|---------|-------------------|---------|-----------|
| **Telco** | Verizon, T-Mobile | 30+ edge sites, latency-sensitive placement, distributed GPU pools. | `docs/patterns/telco.md` |
| **Financial** | Wells Fargo, Bank of America | Multi-region regulatory constraints, strict tenant isolation, audit trails. | `docs/patterns/financial.md` |
| **Sovereign** | OSAC partners | Air-gapped deployment, data residency enforcement, ARE Ledger integration. | `docs/patterns/sovereign.md` |

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
├── dashboard/                   # Next.js (TypeScript) UI
├── deploy/
│   └── overlays/                # hub, standalone, federated
├── docker-compose.yml           # Local dev infrastructure
├── docs/                        # Documentation
└── test/                        # BDD, contract, e2e tests
```

## REST API

The fleet controller exposes 15 REST endpoints. See `docs/api-reference.md` for the complete API specification.

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
Copyright 2024 Red Hat, Inc.

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
