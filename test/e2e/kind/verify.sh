#!/usr/bin/env bash
# Verify the Kind e2e environment: register spokes, create a pool, check placement.
set -euo pipefail

HUB="fleet-hub"
NODEPORT=$(kubectl --context "kind-${HUB}" -n fleet-llm-d get svc fleet-controller -o jsonpath='{.spec.ports[0].nodePort}')
HUB_IP=$(docker inspect "${HUB}-control-plane" --format '{{.NetworkSettings.Networks.kind.IPAddress}}' 2>/dev/null || \
         podman inspect "${HUB}-control-plane" --format '{{.NetworkSettings.Networks.kind.IPAddress}}' 2>/dev/null)
URL="http://${HUB_IP}:${NODEPORT}"
GATEWAY_NODEPORT=$(kubectl --context "kind-${HUB}" -n fleet-llm-d get svc fleet-gateway -o jsonpath='{.spec.ports[0].nodePort}')
GATEWAY_URL="http://${HUB_IP}:${GATEWAY_NODEPORT}"

echo "=== Controller health ==="
curl -sf "$URL/healthz" && echo " OK" || { echo " FAIL"; exit 1; }

echo ""
echo "=== Waiting for spoke-agent registration ==="
for _ in $(seq 1 30); do
  CLUSTER_JSON=$(curl -sf "$URL/api/v1/clusters")
  if echo "$CLUSTER_JSON" | python3 -c 'import json,sys; ids={c["id"] for c in json.load(sys.stdin)}; assert {"spoke-1","spoke-2"} <= ids' 2>/dev/null; then
    break
  fi
  sleep 2
done
echo "$CLUSTER_JSON" | python3 -c 'import json,sys; clusters=json.load(sys.stdin); ids={c["id"] for c in clusters}; assert {"spoke-1","spoke-2"} <= ids, ids; assert all(c.get("labels",{}).get("health_url") for c in clusters if c["id"] in {"spoke-1","spoke-2"}); print(f"Registered agents: {sorted(ids)}")'

echo ""
echo "=== Creating a fleet pool ==="
curl -sf -X POST "$URL/api/v1/webhook/fleetinferencepool" -H 'Content-Type: application/json' \
  -d '{"type":"ADDED","object":{"model":{"name":"e2e-model","source":"test"},"placement":{"policyRef":"default","minClusters":2,"maxClusters":2},"serving":{"inferencePoolTemplate":{"spec":{"targetPorts":[8080]}}}}}' > /dev/null

echo "Pool created"

echo ""
echo "=== Checking pool state ==="
STATE=$(curl -sf "$URL/api/v1/pools/e2e-model/state")
echo "$STATE" | python3 -c '
import sys,json
d=json.load(sys.stdin)
desired=set(d.get("DesiredClusters", []))
assert desired == {"spoke-1", "spoke-2"}, desired
print("Phase:", d.get("Phase"))
print("Desired clusters:", sorted(desired))
'

echo ""
echo "=== Gateway discovery and health probing ==="
for _ in $(seq 1 30); do
  if curl -sf "$GATEWAY_URL/readyz" >/dev/null; then
    echo "Gateway has at least one healthy discovered cluster"
    break
  fi
  sleep 2
done
curl -sf "$GATEWAY_URL/readyz" >/dev/null || { echo "FAIL: gateway never observed a healthy spoke"; exit 1; }

echo ""
echo "=== Healthz probe ==="
curl -sf "$URL/healthz" && echo " OK"

echo ""
echo "=== PASS: Kind e2e verification complete ==="
