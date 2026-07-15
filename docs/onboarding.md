# fleet-llm-d Onboarding Guide

Fleet-level inference orchestration platform built on llm-d. This guide takes you
from a fresh clone to a fully running ecosystem on OpenShift, with all four systems
wired together: deepfield-fleet, governed-cognitive-loop (GCL), fleet-llm-d, and
the ARE immutable ledger.

Audience: Red Hat internal engineers working on AI inference PoCs.


## 1. Prerequisites

### Required tooling

| Tool | Minimum version | Install |
|------|----------------|---------|
| Go | 1.26+ | `brew install go` or [go.dev/dl](https://go.dev/dl/) |
| Rust | 1.90+ | `curl --proto '=https' --tlsv1.2 -sSf https://sh.rustup.rs \| sh` |
| Node.js | 20+ | `brew install node@20` or [nvm](https://github.com/nvm-sh/nvm) |
| Python | 3.12+ | `brew install python@3.12` |
| Podman | 4.x+ | `brew install podman` |
| oc CLI | 4.14+ | [mirror.openshift.com](https://mirror.openshift.com/pub/openshift-v4/clients/ocp/latest/) |
| protoc | 3.x+ | `brew install protobuf` (only needed if regenerating protos) |

### Cluster access

You need `oc login` access to an OpenShift cluster. The deploy scripts target the
`fleet-llm-d` and `sovereign-ai-lab` namespaces. Ask your cluster admin for
project-admin access to both.

### Verify prerequisites

```bash
go version          # go1.26.5 or later
rustc --version     # 1.90.0 or later
node --version      # v20.x or later
python3 --version   # 3.12.x or later
podman --version    # 4.x or later
oc version          # Client Version: 4.14+
```


## 2. Quick Start (Local Development)

### Clone and build

```bash
git clone https://github.com/llm-d/fleet-llm-d.git
cd fleet-llm-d

# Build Go binaries (fleet-controller, fleetctl)
make build-go

# Build Rust crates (fleet-agent, fleet-gateway, kv-transfer, fleet-ledger)
make build-rust

# Build the web dashboard (optional, requires npm install first)
cd web && npm install && cd ..
make build-web
```

### Run fleet-controller locally

The simplest way to start is with in-memory stores and the memory ledger backend.
No PostgreSQL, no Kubernetes, no external dependencies.

```bash
./bin/fleet-controller \
  --port 8080 \
  --metrics-port 9091 \
  --ledger-mode memory \
  --rate-limit 0 \
  --allow-operator-json-intents
```

This starts the API server on `:8080` and the Prometheus metrics server on `:9091`.

### Verify

```bash
# Liveness probe
curl -s http://localhost:8080/healthz | jq .
# Expected: {"status":"ok"}

# Readiness probe
curl -s http://localhost:8080/readyz | jq .
# Expected: {"status":"ready"}

# List clusters (empty at first)
curl -s http://localhost:8080/api/v1/clusters | jq .
# Expected: []

# Register a test cluster
curl -s -X POST http://localhost:8080/api/v1/clusters \
  -H 'Content-Type: application/json' \
  -d '{"name":"test-cluster","region":"us-east"}' | jq .
# Expected: {"status":"registered","id":"..."}

# Verify it shows up
curl -s http://localhost:8080/api/v1/clusters | jq .
```

### Run with docker-compose (full local stack)

For a more complete local environment with PostgreSQL, Redis, Kafka, Prometheus,
Grafana, and the ARE ledger:

```bash
# Start all infrastructure services
make dev

# Wait for services to be healthy, then run the controller against Postgres
./bin/fleet-controller \
  --port 8080 \
  --ledger-mode http \
  --ledger-endpoint http://localhost:18099 \
  --pg-url "postgres://fleet:fleet@localhost:5432/fleet_llm_d?sslmode=disable" \
  --rate-limit 0 \
  --allow-operator-json-intents

# To tear down
make dev-down
```


## 3. Deploy to OpenShift

### 3a. Build and push container images

The `build-and-push.sh` script builds three images using Podman and pushes them to
your Quay registry.

```bash
# Log in to your registry first
podman login quay.io

# Build and push (defaults to quay.io/rh-ee-$USER)
REGISTRY=quay.io/rh-ee-$(whoami) bash deploy/oberon/build-and-push.sh
```

This builds:
- `fleet-controller:latest` from `deploy/docker/Dockerfile.controller`
- `modelplane-mock:latest` from `deploy/docker/Dockerfile.modelplane-mock`
- `mock-inference:latest` from `deploy/oberon/Dockerfile.mock-inference`

If your registry path is different, set `REGISTRY` accordingly.

### 3b. Create the image pull secret

The deployment manifests reference a `quay-pull-secret` in each namespace. Create
it before deploying:

```bash
oc create namespace fleet-llm-d || true
oc create namespace sovereign-ai-lab || true

# Create the pull secret in both namespaces
oc create secret docker-registry quay-pull-secret \
  --docker-server=quay.io \
  --docker-username=<your-quay-user> \
  --docker-password=<your-quay-token> \
  -n fleet-llm-d

oc create secret docker-registry quay-pull-secret \
  --docker-server=quay.io \
  --docker-username=<your-quay-user> \
  --docker-password=<your-quay-token> \
  -n sovereign-ai-lab
```

### 3c. Deploy

```bash
bash deploy/oberon/deploy.sh
```

This script:
1. Creates the `sovereign-ai-lab` and `fleet-llm-d` namespaces
2. Deploys the ARE ledger database and gateway into `sovereign-ai-lab`
3. Deploys `modelplane-mock`, `mock-inference`, and `fleet-controller` into `fleet-llm-d`
4. Waits for all rollouts to complete
5. Runs health checks and prints the route URLs

### 3d. Deploy PostgreSQL (optional, for persistent storage)

By default the fleet-controller uses in-memory stores. To use PostgreSQL:

```bash
oc apply -f deploy/oberon/fleet-postgres.yaml
oc rollout status deploy/fleet-postgres -n fleet-llm-d --timeout=120s
```

Then update the fleet-controller deployment to include `--pg-url`:

```bash
oc set env deploy/fleet-controller -n fleet-llm-d \
  PG_URL="postgres://fleet:fleet@fleet-postgres.fleet-llm-d.svc:5432/fleet_llm_d?sslmode=disable"
```

Or edit the deployment args directly.

### 3e. Deploy NetworkPolicies (optional, recommended for production)

```bash
oc apply -f deploy/oberon/network-policies.yaml
```

This applies a default-deny ingress policy to the `fleet-llm-d` namespace, then
opens specific ports for each component.

### 3f. Deploy the agent simulator (optional, for scale testing)

```bash
oc apply -f deploy/oberon/agent-sim.yaml
oc rollout status statefulset/fleet-agent-sim -n fleet-llm-d --timeout=120s
```

This creates 10 simulated fleet agents that register with the controller.

### Verify

```bash
FLEET_ROUTE=$(oc get route fleet-controller -n fleet-llm-d -o jsonpath='{.spec.host}')

# Health check
curl -sk "https://$FLEET_ROUTE/healthz" | jq .
# Expected: {"status":"ok"}

# Readiness
curl -sk "https://$FLEET_ROUTE/readyz" | jq .
# Expected: {"status":"ready"}

# Prometheus metrics
curl -sk "https://$FLEET_ROUTE:9091/metrics" | head -20

# List pools
curl -sk "https://$FLEET_ROUTE/api/v1/pools" | jq .
```


## 4. Wire the Ecosystem

The full platform consists of four systems deployed across two namespaces.

```
deepfield-fleet  -->  governed-cognitive-loop  -->  fleet-llm-d  -->  are-immutable-ledger
  (observations)       (signed proposals)          (admission)        (tamper-evident proof)
```

### 4a. Deploy GCL (governed-cognitive-loop)

GCL lives in a separate repo and its own namespace.

```bash
# Clone the GCL repo
git clone https://github.com/jkershawrh/governed-cognitive-loop.git
cd governed-cognitive-loop

# Build and push the GCL image
podman build --platform linux/amd64 \
  -t quay.io/rh-ee-$(whoami)/gcl-app:latest .
podman push quay.io/rh-ee-$(whoami)/gcl-app:latest

# Deploy (the GCL repo has its own deployment manifests)
oc create namespace governed-cognitive-loop || true
oc create secret docker-registry quay-pull-secret \
  --docker-server=quay.io \
  --docker-username=<your-quay-user> \
  --docker-password=<your-quay-token> \
  -n governed-cognitive-loop
oc apply -f deploy/openshift/
oc rollout status deploy/gcl-app -n governed-cognitive-loop --timeout=120s
```

### 4b. Deploy deepfield-fleet

deepfield-fleet is the observability layer that produces CloudEvent observations.
It runs in the `fleet-llm-d` namespace.

```bash
# Check your fleet-llm-d repo for deepfield deployment
oc apply -f deploy/oberon/deepfield-fleet.yaml  # if available
# Or deploy from the deepfield-fleet repo
```

### 4c. The ARE ledger is already deployed

The `deploy.sh` script deploys the ledger into `sovereign-ai-lab`. Verify it:

```bash
LEDGER_POD=$(oc get pods -n sovereign-ai-lab -l app=ledger-gateway -o jsonpath='{.items[0].metadata.name}')

# Health check via port-forward
oc port-forward -n sovereign-ai-lab $LEDGER_POD 28099:28099 &
curl -s http://localhost:28099/api/health | jq .
kill %1
```

### 4d. Configure signing keys

GCL signs DecisionPackage CloudEvents before submitting them to fleet-llm-d.
Fleet verifies these signatures before admitting intents.

```bash
# Generate a shared signing key (at least 32 bytes)
SIGNING_KEY=$(openssl rand -base64 48)

# Create a secret in the fleet-llm-d namespace
oc create secret generic fleet-identity \
  -n fleet-llm-d \
  --from-literal=gcl-signing-key="base64:${SIGNING_KEY}" \
  --from-literal=hmac-secret="$(openssl rand -hex 32)"

# Set the same key in the GCL namespace
oc create secret generic gcl-signing \
  -n governed-cognitive-loop \
  --from-literal=decision-signing-key="base64:${SIGNING_KEY}"
```

The fleet-controller picks up `GCL_DECISION_SIGNING_KEY` from the `fleet-identity`
secret (see the deployment manifest). GCL uses the corresponding key to sign
outbound DecisionPackage events.

For development without signing, the fleet-controller accepts unsigned JSON intents
when `FLEET_ALLOW_OPERATOR_JSON_INTENTS=true` (already set in the Oberon manifests).

### 4e. Verify cross-system health

```bash
FLEET_ROUTE=$(oc get route fleet-controller -n fleet-llm-d -o jsonpath='{.spec.host}')
GCL_ROUTE=$(oc get route gcl-app -n governed-cognitive-loop -o jsonpath='{.spec.host}')

# Fleet
curl -sk "https://$FLEET_ROUTE/healthz" | jq .

# GCL
curl -sk "https://$GCL_ROUTE/healthz" | jq .

# Ledger (via fleet's verify endpoint)
curl -sk "https://$FLEET_ROUTE/api/v1/verify/chains" | jq .

# Platform metrics (aggregates health from all systems)
curl -sk "https://$FLEET_ROUTE/api/v1/metrics/platform" | jq .

# Submit a test intent through the full pipeline
curl -sk -X POST "https://$FLEET_ROUTE/api/v2/intents" \
  -H 'Content-Type: application/json' \
  -d '{
    "type": "scale",
    "pool": "granite-fleet",
    "model": "granite-3.3-2b",
    "target_clusters": ["edge-east"],
    "desired_replicas": 2,
    "proposer": {"subject": "onboarding-test"},
    "idempotency_key": "onboarding-test-001"
  }' | jq .
```


## 5. Run the Test Suite

### Unit tests

```bash
# All unit tests (Go + Rust)
make test

# Go unit tests only (with race detector)
make test-unit-go

# Rust unit tests only
make test-unit-rust
```

### BDD and contract tests

```bash
# BDD scenario tests
make test-bdd

# Contract tests (OpenAPI, protobuf)
make test-contracts
```

### Linting

```bash
make lint
```

### Scale microbenchmarks

```bash
make bench-scale
```

This runs benchmarks for the in-memory store, placement solver, weighted balancer,
and reconciler.

### Ecosystem stress test

The stress test lives in the governed-cognitive-loop repo and exercises all four
systems across 8 phases.

```bash
cd /path/to/governed-cognitive-loop

pip install httpx

python3 tests/test_ecosystem_stress.py \
  --gcl-url "https://$GCL_ROUTE" \
  --fleet-url "https://$FLEET_ROUTE" \
  --phase all
```

Available phases: `smoke`, `baseline`, `pressure`, `edge`, `degradation`, `soak`,
`pentest`, `chaos`, `all`.

### Resilience test

Tests component restart recovery and system stability on OpenShift. Must be run
from inside the cluster (or with `oc port-forward`).

```bash
cd /path/to/fleet-llm-d

pip install httpx

python3 test/soak/resilience_test.py \
  --gcl-url "http://gcl-app.governed-cognitive-loop.svc:8000" \
  --fleet-url "http://fleet-controller.fleet-llm-d.svc:8080"
```

### Soak test

Long-running production-emulation test. Measures end-to-end latency, memory growth,
chain integrity, and degradation recovery.

```bash
python3 test/soak/ecosystem_soak.py \
  --gcl-url "https://$GCL_ROUTE" \
  --fleet-url "https://$FLEET_ROUTE" \
  --profile quick
```

Available profiles:

| Profile | Duration | Event interval | Injection count |
|---------|----------|---------------|-----------------|
| `quick` | 30 min | 5s | 2 |
| `standard` | 2 hr | 3s | 8 |
| `overnight` | 8 hr | 5s | 16 |
| `72hr` | 72 hr | 5s | 72 |

### Test harness (Go-based)

The Go test harness provides additional suites for smoke, stress, soak, pressure,
chaos, latency, throughput, and scale testing.

```bash
# Build the harness
make harness-build

# Run smoke tests against a live instance
make harness-smoke HARNESS_URL=https://$FLEET_ROUTE

# Run the full suite
make harness-all HARNESS_URL=https://$FLEET_ROUTE HARNESS_DURATION=10m
```

### Verify

After running `make test`, all tests should pass with zero failures:

```bash
make test
echo "Exit code: $?"
# Expected: 0
```


## 6. Configuration Reference

### fleet-controller CLI flags

| Flag | Default | Description |
|------|---------|-------------|
| `--port` | `8080` | API server port |
| `--metrics-port` | `9091` | Prometheus metrics server port |
| `--grpc-port` | `0` | gRPC (JSON-RPC) server port. 0 disables. |
| `--mode` | `all` | Server mode: `all`, `control` (fleet API only), `inference` (proxy only) |
| `--ledger-mode` | `memory` | Ledger backend: `disabled`, `memory`, `http` |
| `--ledger-endpoint` | `http://localhost:18099` | ARE ledger REST gateway URL (HTTP mode only) |
| `--pg-url` | (empty) | PostgreSQL connection string. When set, uses Postgres instead of in-memory stores. Example: `postgres://fleet:fleet@host:5432/fleet_llm_d?sslmode=disable` |
| `--backend-vllm` | `http://vllm-cpu.fleet-llm-d.svc:8000` | Base URL for the vLLM inference backend |
| `--backend-ovms` | `http://ovms-granite-external.fleet-llm-d.svc:8080` | Base URL for the OVMS inference backend |
| `--backends` | (empty) | JSON array of additional inference backends. Format: `[{"model":"name","url":"http://...","runtime":"vllm\|openvino","path_prefix":"/v3"}]` |
| `--kube-api` | (empty) | Kubernetes API server URL. Enables CRD watching and authoritative intent persistence when set. |
| `--namespace` | `default` | Kubernetes namespace to watch for FleetInferencePool CRDs |
| `--modelplane-api` | (empty) | ModelPlane API server URL. Enables ModelPlane integration when set. |
| `--modelplane-namespace` | `default` | ModelPlane namespace to watch |
| `--rate-limit` | `100` | Requests per second per IP. 0 disables rate limiting. |
| `--rate-burst` | `200` | Rate limit burst size |
| `--rate-limit-exempt` | `/healthz,/readyz,/metrics` | Comma-separated paths exempt from rate limiting and auth |
| `--max-inflight` | `0` | Max concurrent inference requests per model. 0 disables. |
| `--tls-cert` | (empty) | Path to TLS certificate file |
| `--tls-key` | (empty) | Path to TLS private key file |
| `--log-level` | `info` | Log level: `debug`, `info`, `warn`, `error` |
| `--allow-operator-json-intents` | `false` | Enable unsigned JSON v2 intent input (development only) |

### Environment variables

| Variable | Description |
|----------|-------------|
| `FLEET_AUTH_SECRET` | HMAC secret for JWT auth. When empty, auth is disabled. |
| `FLEET_HMAC_SECRET` | Alias for `FLEET_AUTH_SECRET` (used in K8s secrets) |
| `FLEET_ALLOW_OPERATOR_JSON_INTENTS` | Set to `true` to enable unsigned JSON intents via env instead of flag |
| `GCL_DECISION_SIGNING_KEY` | Shared HMAC key for verifying GCL DecisionPackage CloudEvents. Prefix with `base64:` for base64-encoded keys. |
| `GCL_DECISION_SIGNING_KEY_ID` | Key ID for the signing key (default: `gcl-decision-v1`) |
| `GCL_DECISION_SIGNING_KEYS_JSON` | JSON map of `{key_id: key_material}` for multiple signing keys |
| `LEDGER_GATEWAY_API_TOKEN` | Bearer token for authenticating to the ARE ledger REST gateway |
| `GCL_URL` | Override the GCL service URL for platform metrics (default: `http://gcl-app.governed-cognitive-loop.svc:8000`) |
| `DEEPFIELD_URL` | Override the deepfield service URL for platform metrics (default: `http://deepfield-fleet.fleet-llm-d.svc:8000`) |
| `LEDGER_GATEWAY_URL` | Override the ledger gateway URL for platform metrics |
| `SEMANTIC_CLASSIFIER_URL` | URL for the semantic routing classifier (default: GCL endpoint) |

### API endpoints

Health and diagnostics:
- `GET /healthz` - liveness probe
- `GET /readyz` - readiness probe
- `GET /metrics` (port 9091) - Prometheus metrics

Fleet management (v1):
- `GET /api/v1/clusters` - list clusters
- `POST /api/v1/clusters` - register a cluster
- `DELETE /api/v1/clusters/{id}` - deregister a cluster
- `GET /api/v1/pools` - list inference pools
- `GET /api/v1/pools/{name}/state` - get reconciled pool state
- `GET /api/v1/tenants` - list tenants
- `GET /api/v1/tenants/{id}/usage` - tenant usage
- `GET /api/v1/metrics/fleet` - fleet-wide metrics
- `GET /api/v1/metrics/model/{model}` - per-model metrics
- `GET /api/v1/metrics/platform` - cross-system platform metrics
- `GET /api/v1/rollouts` - list rollouts
- `POST /api/v1/rollouts` - create a rollout
- `GET /api/v1/verify/chains` - verify ledger chains
- `GET /api/v1/cost/pricing` - GPU pricing table
- `GET /api/v1/cost/projection` - monthly cost projection

Intent pipeline (v2):
- `POST /api/v2/intents` - submit a DecisionPackage CloudEvent or JSON intent
- `GET /api/v2/intents/{id}` - get intent status
- `GET /api/v2/operations/{id}` - get operation status
- `POST /api/v2/operations/{id}/approve` - approve an operation
- `POST /api/v2/operations/{id}/cancel` - cancel an operation

Inference proxy:
- `POST /v1/chat/completions` - OpenAI-compatible chat completions
- `POST /v1/completions` - OpenAI-compatible completions


## 7. Troubleshooting

### Auth is disabled by default

When `FLEET_AUTH_SECRET` is empty (the default for local development), authentication
is disabled. All requests are accepted and the audit identity is
`unauthenticated-development`.

To enable auth, set the secret:

```bash
# Local
export FLEET_AUTH_SECRET="your-secret-here"
./bin/fleet-controller --port 8080 --ledger-mode memory

# OpenShift
oc create secret generic fleet-identity \
  -n fleet-llm-d \
  --from-literal=hmac-secret="$(openssl rand -hex 32)"
```

Then use a bearer token in requests:

```bash
curl -s -H "Authorization: Bearer <token>" http://localhost:8080/api/v1/clusters
```

### Verify

```bash
# With auth disabled, this should return 200
curl -s -o /dev/null -w "%{http_code}" http://localhost:8080/api/v1/clusters
# Expected: 200

# With auth enabled and no token, this should return 401
curl -s -o /dev/null -w "%{http_code}" http://localhost:8080/api/v1/clusters
# Expected: 401
```

### NetworkPolicy blocking cross-namespace traffic

The default NetworkPolicies in `deploy/oberon/network-policies.yaml` apply a
default-deny ingress rule. If fleet-controller cannot reach the ledger or other
services, check that the correct policies are in place.

Symptoms:
- fleet-controller logs: `failed to record placement` or ledger connection timeouts
- GCL cannot reach fleet-controller
- Cross-namespace service calls timeout

Fix: make sure the allow rules are applied for all components.

```bash
# Check active NetworkPolicies
oc get networkpolicy -n fleet-llm-d
oc get networkpolicy -n sovereign-ai-lab

# If policies are blocking traffic, either remove default-deny or add explicit allows
oc apply -f deploy/oberon/network-policies.yaml

# Temporary: delete default-deny for debugging
oc delete networkpolicy default-deny-ingress -n fleet-llm-d
```

### Verify

```bash
# From a fleet-llm-d pod, curl the ledger
oc exec deploy/fleet-controller -n fleet-llm-d -- \
  curl -s http://ledger-gateway.sovereign-ai-lab.svc:28099/api/health
# Expected: healthy response
```

### Pod limit on SNO (Single Node OpenShift)

SNO clusters have a default kubelet `maxPods` of 250. The full ecosystem with
agent-sim (10 replicas) and all supporting services can approach this limit.

Symptoms:
- Pods stuck in `Pending` state
- Events: `0/1 nodes are available: 1 Too many pods`

Fix: reduce replicas or increase the pod limit.

```bash
# Check current pod count
oc get pods --all-namespaces --no-headers | wc -l

# Reduce agent-sim replicas
oc scale statefulset/fleet-agent-sim -n fleet-llm-d --replicas=3

# Or check the kubelet config
oc get kubeletconfig -o yaml
```

### Verify

```bash
# All pods should be Running (not Pending)
oc get pods -n fleet-llm-d
oc get pods -n sovereign-ai-lab
oc get pods -n governed-cognitive-loop
```

### Ledger in "disabled" mode still working

If `--ledger-mode=disabled`, all ledger recording calls are no-ops. The
fleet-controller will still function (admission, routing, reconciliation) but
no tamper-evident proof is recorded. Audit evidence will be absent.

Verify the mode:

```bash
# Check the fleet-controller logs for the startup line
oc logs deploy/fleet-controller -n fleet-llm-d | grep "ledger-mode"
# Expected: fleet-controller starting (mode=all, log-level=info, ledger-mode=http, ...)
```

### GCL DecisionPackage verification failures

If GCL and fleet-llm-d have mismatched signing keys, intent submissions will fail
with `invalid GCL DecisionPackage CloudEvent`.

```bash
# Check fleet-controller logs for signing errors
oc logs deploy/fleet-controller -n fleet-llm-d | grep -i "decision"

# Verify the key is loaded
oc logs deploy/fleet-controller -n fleet-llm-d | grep "DecisionPackage verification"
# Expected: GCL DecisionPackage verification enabled with 1 trusted key(s)
```

Fix: regenerate the shared signing key and update both secrets (see section 4d).

### Inference proxy returns 502

If the inference backend (vLLM or OVMS) is down, the proxy returns an error.
Check backend health:

```bash
# List registered backends via the fleet-controller logs
oc logs deploy/fleet-controller -n fleet-llm-d | grep "registered backend"

# Check if mock-inference is running
oc get pods -n fleet-llm-d -l app=mock-inference
```

### Verify

```bash
# Send a test inference request
curl -sk -X POST "https://$FLEET_ROUTE/v1/chat/completions" \
  -H 'Content-Type: application/json' \
  -d '{"model":"granite-3.3-2b","messages":[{"role":"user","content":"hello"}]}' | jq .
```

### Getting help

- Repo: [github.com/llm-d/fleet-llm-d](https://github.com/llm-d/fleet-llm-d)
- GCL repo: [github.com/jkershawrh/governed-cognitive-loop](https://github.com/jkershawrh/governed-cognitive-loop)
- Internal Slack: `#fleet-llm-d`
