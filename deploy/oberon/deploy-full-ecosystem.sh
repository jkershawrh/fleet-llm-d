#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
DEEPFIELD_ROOT="${DEEPFIELD_ROOT:-$PROJECT_ROOT/../deepfield-fleet}"
GCL_ROOT="${GCL_ROOT:-$PROJECT_ROOT/../governed-cognitive-loop}"

OC="oc"
CONTEXT=""
if [[ -n "${KUBECONFIG:-}" ]]; then
  OC="oc --kubeconfig=$KUBECONFIG"
fi
if [[ -n "${OC_CONTEXT:-}" ]]; then
  CONTEXT="--context=$OC_CONTEXT"
fi

kc() { $OC $CONTEXT "$@"; }

# Load secrets
SECRETS_FILE="$SCRIPT_DIR/.secrets.env"
if [[ ! -f "$SECRETS_FILE" ]]; then
  echo "ERROR: $SECRETS_FILE not found. Run the secrets generation step first."
  exit 1
fi
source "$SECRETS_FILE"

echo "=== Deploying full fleet-llm-d ecosystem to Oberon ==="
echo ""

# ---- Step 1: Namespaces ----
echo "--- Step 1: Namespaces ---"
kc create namespace fleet-llm-d --dry-run=client -o yaml | kc apply -f -
kc create namespace immutable-ledger --dry-run=client -o yaml | kc apply -f -
kc create namespace governed-cognitive-loop --dry-run=client -o yaml | kc apply -f -

# ---- Step 2: Secrets ----
echo "--- Step 2: Secrets ---"
kc apply -f - <<EOF
apiVersion: v1
kind: Secret
metadata:
  name: fleet-identity
  namespace: fleet-llm-d
type: Opaque
stringData:
  hmac-secret: "${HMAC_SECRET}"
  gcl-signing-key: "${GCL_SIGNING_KEY}"
---
apiVersion: v1
kind: Secret
metadata:
  name: fleet-postgres-credentials
  namespace: fleet-llm-d
type: Opaque
stringData:
  password: "${PG_PASS}"
  pg-url: "postgres://fleet:${PG_PASS}@fleet-postgres.fleet-llm-d.svc:5432/fleet_llm_d?sslmode=prefer"
---
apiVersion: v1
kind: Secret
metadata:
  name: ledger-postgres-credentials
  namespace: immutable-ledger
type: Opaque
stringData:
  password: "${LEDGER_PG_PASS}"
  database-url: "postgres://ledger:${LEDGER_PG_PASS}@ledger-db.immutable-ledger.svc:5432/are_ledger"
EOF

# GCL secrets
kc apply -f - <<EOF
apiVersion: v1
kind: Secret
metadata:
  name: gcl-identity
  namespace: governed-cognitive-loop
type: Opaque
stringData:
  deepfield-event-token: "${HMAC_SECRET}"
  oidc-token: "${HMAC_SECRET}"
---
apiVersion: v1
kind: Secret
metadata:
  name: gcl-decision-signing
  namespace: governed-cognitive-loop
type: Opaque
stringData:
  active-key: "${GCL_SIGNING_KEY}"
  active-key-id: "fleet-llm-d-2026-08"
EOF

# ---- Step 3: ARE Ledger ----
echo "--- Step 3: ARE Immutable Ledger ---"
kc apply -f "$SCRIPT_DIR/ledger.yaml"
echo "Waiting for ledger-db..."
kc rollout status deploy/ledger-db -n immutable-ledger --timeout=120s
echo "Waiting for ledger-gateway..."
kc rollout status deploy/ledger-gateway -n immutable-ledger --timeout=120s

# ---- Step 4: Fleet PostgreSQL ----
echo "--- Step 4: Fleet PostgreSQL ---"
kc apply -f "$SCRIPT_DIR/fleet-postgres.yaml"
echo "Waiting for fleet-postgres..."
kc rollout status deploy/fleet-postgres -n fleet-llm-d --timeout=120s

# ---- Step 5: Mock services ----
echo "--- Step 5: Mock inference + ModelPlane ---"
kc apply -f "$SCRIPT_DIR/mock-inference.yaml"
kc apply -f "$SCRIPT_DIR/modelplane-mock.yaml"
kc rollout status deploy/mock-inference -n fleet-llm-d --timeout=60s
kc rollout status deploy/modelplane-mock -n fleet-llm-d --timeout=60s

# ---- Step 6: Fleet Controller ----
echo "--- Step 6: Fleet Controller ---"
kc apply -f "$SCRIPT_DIR/fleet-controller.yaml"
echo "Waiting for fleet-controller..."
kc rollout status deploy/fleet-controller -n fleet-llm-d --timeout=120s

# ---- Step 7: Fleet Agent (Oberon as local spoke) ----
echo "--- Step 7: Fleet Agent (oberon-sno) ---"
kc apply -f "$SCRIPT_DIR/fleet-agent.yaml"
kc rollout status deploy/fleet-agent -n fleet-llm-d --timeout=120s

# ---- Step 8: Network Policies ----
echo "--- Step 8: Network Policies ---"
kc apply -f "$SCRIPT_DIR/network-policies.yaml"

# ---- Step 9: deepfield-fleet ----
echo "--- Step 9: deepfield-fleet ---"
if [[ -d "$DEEPFIELD_ROOT/deploy" ]]; then
  # Create deepfield secrets
  kc apply -f - <<EOF
apiVersion: v1
kind: Secret
metadata:
  name: deepfield-secrets
  namespace: fleet-llm-d
type: Opaque
stringData:
  GCL_EVENT_SINK_URL: "http://gcl-app.governed-cognitive-loop.svc:8000/api/v1/events/deepfield"
  GCL_EVENT_SINK_TOKEN: "${HMAC_SECRET}"
  DEEPFIELD_TENANT: "default"
  DEEPFIELD_ZONE: "intel-lab"
  DEEPFIELD_CLUSTER: "oberon-sno"
  DEEPFIELD_NAMESPACE: "fleet-llm-d"
EOF
  # Apply deployment (update image ref inline)
  sed 's|quay.io/deepfield-fleet/deepfield-fleet:latest|image-registry.openshift-image-registry.svc:5000/fleet-llm-d/deepfield-fleet:latest|g' \
    "$DEEPFIELD_ROOT/deploy/deployment.yaml" | kc apply -n fleet-llm-d -f -
  echo "Waiting for deepfield-fleet..."
  kc rollout status deploy/deepfield-fleet -n fleet-llm-d --timeout=120s || echo "WARNING: deepfield-fleet rollout incomplete"
else
  echo "WARNING: deepfield-fleet repo not found at $DEEPFIELD_ROOT — skipping"
fi

# ---- Step 10: Governed Cognitive Loop ----
echo "--- Step 10: Governed Cognitive Loop ---"
if [[ -d "$GCL_ROOT/deploy" ]]; then
  kc apply -f "$GCL_ROOT/deploy/namespace.yaml"
  # Apply deployment (update image ref inline)
  sed 's|quay.io/rh-ee-jkershaw/gcl-demo:latest|image-registry.openshift-image-registry.svc:5000/governed-cognitive-loop/gcl-demo:latest|g' \
    "$GCL_ROOT/deploy/deployment.yaml" | kc apply -n governed-cognitive-loop -f -
  echo "Waiting for gcl-app..."
  kc rollout status deploy/gcl-app -n governed-cognitive-loop --timeout=120s || echo "WARNING: gcl-app rollout incomplete"
else
  echo "WARNING: governed-cognitive-loop repo not found at $GCL_ROOT — skipping"
fi

# ---- Health Checks ----
echo ""
echo "=== Health Checks ==="
FLEET_ROUTE=$(kc get route fleet-controller -n fleet-llm-d -o jsonpath='{.spec.host}' 2>/dev/null || echo "")
if [[ -n "$FLEET_ROUTE" ]]; then
  echo "Fleet Controller: https://$FLEET_ROUTE/healthz"
  curl -sk "https://$FLEET_ROUTE/healthz" && echo ""
fi

echo ""
echo "=== Deployment complete ==="
echo ""
echo "Fleet URL:      https://$FLEET_ROUTE"
echo "Ledger:         http://ledger-gateway.immutable-ledger.svc:28099"
echo "deepfield:      http://deepfield-fleet.fleet-llm-d.svc:8000"
echo "GCL:            http://gcl-app.governed-cognitive-loop.svc:8000"
echo ""
echo "Next steps:"
echo "  1. Deploy inference on Oberon/Arena/Brutus (Phase 3)"
echo "  2. Deploy spoke agents on Arena/Brutus (Phase 4)"
echo "  3. Apply fleet CRD resources (Phase 5)"
echo "  4. Run ecosystem soak test (Phase 6)"
