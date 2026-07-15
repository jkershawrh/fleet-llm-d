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

- `api/` - API definitions (protobuf, OpenAPI, CRDs)
- `cmd/` - Binary entry points (fleet-controller, fleet-agent, fleet-gateway, fleetctl)
- `pkg/` - Go packages (control plane logic)
- `crates/` - Rust crates (data plane: agent, gateway, KV transfer)
- `web/` - TypeScript dashboard (Next.js)
- `python/` - Python analytics
- `deploy/` - Kubernetes deployment manifests (Kustomize + Helm)
- `test/` - Test infrastructure (BDD features, contracts, e2e)

## Architecture

The Go fleet controller manages fleet CRDs and coordinates with per-cluster agents. The Rust fleet gateway handles cross-cluster traffic routing. The core ecosystem path is:

`deepfield-fleet -> governed-cognitive-loop -> fleet-llm-d -> are-immutable-ledger`

DeepField owns observations, findings, and forecasts. GCL owns signed and falsified proposals. fleet-llm-d owns admission, authorization, operation state, desired/observed state, and actuation. The standalone immutable ledger owns tamper-evident evidence and proof verification; its receipts never authorize execution.

### External Integrations

- **ModelPack (CNCF model-spec)**: OCI-based model metadata resolution. `pkg/modelpack/` resolves GPU requirements, precision, and format for placement.
- **deepfield-fleet**: canonical producer of observation, finding, forecast, and advisory-remediation CloudEvents.
- **governed-cognitive-loop**: submits signed, expiry-bounded `DecisionPackage` CloudEvents to `POST /api/v2/intents`; it never actuates infrastructure.
- **are-immutable-ledger**: independent evidence infrastructure with its own database and compute. The ledger-owned gRPC service is canonical. `pkg/ledger/` currently supports memory/disabled modes and the optional authenticated REST compatibility gateway. A configured ledger error must fail closed, never fall back to fabricated memory evidence.

## Dependencies

- Go 1.26+
- Rust 1.79+
- Node.js 20+
- Python 3.12+
- PostgreSQL 16+
- Protocol Buffers (protoc)

## Conventions

- Go: standard library style, table-driven tests, raw HTTPS against Kubernetes API (no controller-runtime, single dependency: lib/pq)
- Rust: tokio async runtime, tonic for gRPC, axum for HTTP
- Proto files define the contract between control plane and data plane
- CRD schemas in `api/crds/` are the source of truth for Kubernetes types
- All fleet state changes publish events to the configured event transport
- The immutable ledger is external proof infrastructure: connect to it, never embed it, and never treat a receipt as authority
