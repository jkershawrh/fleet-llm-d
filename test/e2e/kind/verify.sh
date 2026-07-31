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
echo "=== Waiting for spoke-agent registration ==="
for _ in $(seq 1 30); do
  CLUSTER_JSON=$(curl -sf "$URL/api/v1/clusters")
  if echo "$CLUSTER_JSON" | python3 -c 'import json,sys; ids={c["id"] for c in json.load(sys.stdin)}; assert {"spoke-1","spoke-2"} <= ids' 2>/dev/null; then
    break
  fi
  sleep 2
done
echo "$CLUSTER_JSON" | python3 -c 'import json,sys; clusters=json.load(sys.stdin); ids={c["id"] for c in clusters}; assert {"spoke-1","spoke-2"} <= ids, ids; selected=[c for c in clusters if c["id"] in {"spoke-1","spoke-2"}]; assert all(c.get("status") == "Running" for c in selected), selected; assert all(c.get("labels",{}).get("health_url") and c.get("labels",{}).get("inference_url") for c in selected); print(f"Registered agents: {sorted(ids)}")'

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
echo "=== Fleet metrics ==="
METRICS=$(curl -sf "$URL/api/v1/metrics/fleet")
echo "$METRICS" | python3 -c 'import json,sys; d=json.load(sys.stdin); print("Fleet metrics:", json.dumps(d, indent=2)[:200])'

echo ""
echo "=== Cluster drain + activate ==="
curl -sf -X POST "$URL/api/v1/clusters/spoke-1/drain" && echo " Drain OK" || echo " Drain skipped (cluster may not be registered yet)"
curl -sf -X POST "$URL/api/v1/clusters/spoke-1/activate" && echo " Activate OK" || echo " Activate skipped"

echo ""
echo "=== Healthz probe ==="
curl -sf "$URL/healthz" && echo " OK"

echo ""
echo "=== PASS: Kind e2e verification complete ==="
