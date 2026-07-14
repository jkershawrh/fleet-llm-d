#!/usr/bin/env bash
set -euo pipefail

echo "=== Deploying fleet-llm-d ecosystem to Oberon ==="

echo "--- Creating namespaces ---"
oc apply -f deploy/oberon/ledger.yaml 2>/dev/null | head -2 || true
oc apply -f deploy/oberon/fleet-controller.yaml 2>/dev/null | head -1 || true

echo "--- Deploying ARE ledger ---"
oc apply -f deploy/oberon/ledger.yaml
echo "Waiting for ledger-db..."
oc rollout status deploy/ledger-db -n sovereign-ai-lab --timeout=120s
echo "Waiting for ledger-gateway..."
oc rollout status deploy/ledger-gateway -n sovereign-ai-lab --timeout=120s

echo "--- Deploying fleet-llm-d components ---"
oc apply -f deploy/oberon/modelplane-mock.yaml
oc apply -f deploy/oberon/mock-inference.yaml
oc apply -f deploy/oberon/fleet-controller.yaml

echo "Waiting for modelplane-mock..."
oc rollout status deploy/modelplane-mock -n fleet-llm-d --timeout=60s
echo "Waiting for mock-inference..."
oc rollout status deploy/mock-inference -n fleet-llm-d --timeout=60s
echo "Waiting for fleet-controller..."
oc rollout status deploy/fleet-controller -n fleet-llm-d --timeout=120s

echo ""
echo "=== Health checks ==="
FLEET_ROUTE=$(oc get route fleet-controller -n fleet-llm-d -o jsonpath='{.spec.host}')
GCL_ROUTE=$(oc get route gcl-app -n governed-cognitive-loop -o jsonpath='{.spec.host}' 2>/dev/null || echo "gcl-app.192.168.1.123.sslip.io")

echo "Fleet: https://$FLEET_ROUTE/healthz"
curl -sk "https://$FLEET_ROUTE/healthz"
echo ""

echo "GCL: https://$GCL_ROUTE/healthz"
curl -sk "https://$GCL_ROUTE/healthz"
echo ""

echo ""
echo "=== Deployment complete ==="
echo "Fleet URL: https://$FLEET_ROUTE"
echo "GCL URL: https://$GCL_ROUTE"
echo ""
echo "Run the soak test:"
echo "  python3 test/soak/ecosystem_soak.py \\"
echo "    --fleet-url https://$FLEET_ROUTE \\"
echo "    --gcl-url https://$GCL_ROUTE \\"
echo "    --profile quick"
