# fleet-llm-d

Fleet-level inference orchestration platform built on llm-d.

## Build

```bash
make build          # Build all (Go + Rust + TypeScript)
make build-go       # Go binaries only
make build-rust     # Rust crates only
make build-web      # TypeScript dashboard only
```

## Test

```bash
make test           # Run all tests
make test-unit      # Unit tests (Go + Rust)
make test-bdd       # BDD scenarios
make test-contracts # Contract tests
make test-e2e       # End-to-end tests (requires Kind clusters)
make lint           # Lint all code
```

## Project Structure

- `api/` — API definitions (protobuf, OpenAPI, CRDs)
- `cmd/` — Binary entry points (fleet-controller, fleet-agent, fleet-gateway, fleetctl)
- `pkg/` — Go packages (control plane logic)
- `crates/` — Rust crates (data plane: agent, gateway, KV transfer)
- `web/` — TypeScript dashboard (Next.js)
- `python/` — Python analytics
- `deploy/` — Kubernetes deployment manifests (Kustomize + Helm)
- `test/` — Test infrastructure (BDD features, contracts, e2e)

## Architecture

Go control plane + Rust data plane. The fleet controller manages CRDs (FleetInferencePool, PlacementPolicy, TenantProfile, etc.) and coordinates with per-cluster fleet agents. The fleet gateway (Rust) handles cross-cluster traffic routing.

### External Integrations

- **ModelPack (CNCF model-spec)**: OCI-based model metadata resolution. `pkg/modelpack/` resolves GPU requirements, precision, format from OCI registry. Used by placement engine for auto GPU sizing.
- **ARE Immutable Ledger**: **Independent shared enterprise infrastructure** — NOT a fleet-llm-d component. The ledger runs on its own database and compute, operated separately. fleet-llm-d is one of many writers; other platforms (MaaS, RHACM, agentic frameworks, CI/CD, security tools) can write to the same instance. `pkg/ledger/` (Go client) and `crates/fleet-ledger/` (Rust client) connect to the ledger via gRPC. In docker-compose, the ledger runs on a separate network (`are-ledger-net`) with its own PostgreSQL instance.

## Dependencies

- Go 1.23+
- Rust 1.79+
- Node.js 20+
- Python 3.12+
- PostgreSQL 16+
- Protocol Buffers (protoc)

## External Services (not owned by fleet-llm-d)

- ARE Immutable Ledger (gRPC on port 9092) — compliance audit trail
- OCI-compatible model registry — ModelPack model metadata source
- Governed Cognitive Loop (GCL): governed autonomy layer that sends typed intents (ScaleIntent, PreWarmIntent, ShedLoadIntent, AlertIntent, MigrateIntent) to fleet-llm-d via POST /api/v1/intents with HMAC-SHA256 auth. fleet-llm-d evaluates intents against policy before actuating. Repo: https://github.com/jkershawrh/governed-cognitive-loop

## Conventions

- Go: standard library style, table-driven tests, controller-runtime for K8s operators
- Rust: tokio async runtime, tonic for gRPC, axum for HTTP
- Proto files define the contract between control plane and data plane
- CRD schemas in api/crds/ are the source of truth for Kubernetes types
- All fleet state changes publish events to AMQ Streams
- The ARE ledger is treated as external infrastructure — fleet-llm-d connects to it, never embeds it
