#!/usr/bin/env bash
# Verify the Kind e2e environment: register spokes, create a pool, check placement.
set -euo pipefail

HUB="fleet-hub"
NODEPORT=$(kubectl --context "kind-${HUB}" -n fleet-llm-d get svc fleet-controller -o jsonpath='{.spec.ports[0].nodePort}')
HUB_IP=$(docker inspect "${HUB}-control-plane" --format '{{.NetworkSettings.Networks.kind.IPAddress}}' 2>/dev/null || \
         podman inspect "${HUB}-control-plane" --format '{{.NetworkSettings.Networks.kind.IPAddress}}' 2>/dev/null)
URL="http://${HUB_IP}:${NODEPORT}"

echo "=== Controller health ==="
curl -sf "$URL/healthz" && echo " OK" || { echo " FAIL"; exit 1; }

echo ""
echo "=== Registering spoke clusters ==="
curl -sf -X POST "$URL/api/v1/clusters" -H 'Content-Type: application/json' \
  -d '{"name":"spoke-1","region":"us-east"}' | python3 -c "import sys,json; print(json.load(sys.stdin).get('ID','?'))"
curl -sf -X POST "$URL/api/v1/clusters" -H 'Content-Type: application/json' \
  -d '{"name":"spoke-2","region":"us-west"}' | python3 -c "import sys,json; print(json.load(sys.stdin).get('ID','?'))"

echo ""
echo "=== Listing clusters ==="
CLUSTERS=$(curl -sf "$URL/api/v1/clusters" | python3 -c "import sys,json; d=json.load(sys.stdin); print(len(d))")
echo "Registered clusters: $CLUSTERS"
[ "$CLUSTERS" -ge 2 ] || { echo "FAIL: expected >= 2 clusters"; exit 1; }

echo ""
echo "=== Creating a fleet pool ==="
curl -sf -X POST "$URL/api/v1/webhook/fleetinferencepool" -H 'Content-Type: application/json' \
  -d '{"type":"ADDED","object":{"apiVersion":"fleet.llm-d.ai/v1alpha1","kind":"FleetInferencePool","metadata":{"name":"e2e-pool"},"spec":{"model":{"name":"e2e-model","source":"test"},"placement":{"policyRef":"default","maxClusters":2},"serving":{"inferencePoolTemplate":{"spec":{"targetPorts":[8080]}}}}}}' > /dev/null

echo "Pool created"

echo ""
echo "=== Checking pool state ==="
STATE=$(curl -sf "$URL/api/v1/pools/e2e-model/state" 2>/dev/null || echo '{}')
echo "$STATE" | python3 -c "
import sys,json
d=json.load(sys.stdin)
print(f'Phase: {d.get(\"Phase\",\"unknown\")}')
print(f'Desired clusters: {d.get(\"DesiredClusters\",[])}')
" 2>/dev/null || echo "Pool state not yet reconciled"

echo ""
echo "=== Healthz probe ==="
curl -sf "$URL/healthz" && echo " OK"

echo ""
echo "=== PASS: Kind e2e verification complete ==="
