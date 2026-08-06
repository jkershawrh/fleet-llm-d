#!/usr/bin/env bash
set -euo pipefail

if [[ -z "${OBERON_CONTROLLER_URL:-}" ]]; then
  echo "ERROR: OBERON_CONTROLLER_URL must be set (e.g. https://fleet-controller-fleet-llm-d.apps.oberon.example.com)"
  exit 1
fi

echo "=== Deploying fleet-llm-d spoke to Arena ==="
echo "Hub controller: $OBERON_CONTROLLER_URL"

echo "--- Creating namespace ---"
oc create namespace fleet-llm-d --dry-run=client -o yaml | oc apply -f -

echo "--- Deploying granite inference (vLLM CPU) ---"
oc apply -f deploy/arena/granite-inference.yaml

echo "--- Applying network policies ---"
oc apply -f deploy/arena/network-policies.yaml

echo "--- Creating inference Route (for cross-cluster access from Praxis) ---"
oc apply -f deploy/arena/inference-route.yaml

echo "--- Deploying fleet-agent ---"
sed "s|FLEET_CONTROL_PLANE_URL_PLACEHOLDER|${OBERON_CONTROLLER_URL}|g" \
  deploy/arena/fleet-agent.yaml | oc apply -f -

echo "--- Waiting for rollouts ---"
echo "Waiting for granite-real (model download may take several minutes)..."
oc rollout status deploy/granite-real -n fleet-llm-d --timeout=600s
echo "Waiting for fleet-agent..."
oc rollout status deploy/fleet-agent -n fleet-llm-d --timeout=120s

echo ""
echo "=== Health checks ==="
INFERENCE_ROUTE=$(oc get route ovms-granite-2b -n fleet-llm-d -o jsonpath='{.spec.host}' 2>/dev/null || echo "")
echo "Fleet agent proxy:   http://fleet-agent.fleet-llm-d.svc:8090/healthz"
echo "Granite inference:   http://granite-real.fleet-llm-d.svc:8000/health"
echo "Inference Route:     https://$INFERENCE_ROUTE"

echo ""
echo "=== Arena spoke deployment complete ==="
echo "Cluster ID: arena-xeon6"
echo "Hub:        $OBERON_CONTROLLER_URL"
