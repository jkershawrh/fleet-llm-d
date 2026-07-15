#!/usr/bin/env bash
# Three-cluster Kind environment for fleet-llm-d e2e testing.
# Creates: fleet-hub (controller), fleet-spoke-1, fleet-spoke-2
# Requires: kind, kubectl, docker/podman
set -euo pipefail

KIND_IMAGE="${KIND_IMAGE:-kindest/node:v1.31.0}"
HUB="fleet-hub"
SPOKE1="fleet-spoke-1"
SPOKE2="fleet-spoke-2"

echo "=== Creating Kind clusters ==="
for cluster in "$HUB" "$SPOKE1" "$SPOKE2"; do
  if kind get clusters 2>/dev/null | grep -q "^${cluster}$"; then
    echo "$cluster already exists, skipping"
  else
    kind create cluster --name "$cluster" --image "$KIND_IMAGE" --wait 60s
    echo "$cluster created"
  fi
done

echo ""
echo "=== Applying CRDs to all clusters ==="
for cluster in "$HUB" "$SPOKE1" "$SPOKE2"; do
  kubectl --context "kind-${cluster}" apply -f api/crds/ 2>/dev/null || true
  echo "CRDs applied to $cluster"
done

echo ""
echo "=== Building fleet-controller ==="
CGO_ENABLED=0 go build -o bin/fleet-controller ./cmd/fleet-controller

echo ""
echo "=== Loading controller image into hub ==="
docker build -t fleet-controller:e2e -f deploy/docker/Dockerfile.controller . 2>/dev/null || \
  podman build -t fleet-controller:e2e -f deploy/docker/Dockerfile.controller . 2>/dev/null
kind load docker-image fleet-controller:e2e --name "$HUB"

echo ""
echo "=== Deploying controller to hub ==="
kubectl --context "kind-${HUB}" create namespace fleet-llm-d 2>/dev/null || true
cat <<EOF | kubectl --context "kind-${HUB}" apply -f -
apiVersion: apps/v1
kind: Deployment
metadata:
  name: fleet-controller
  namespace: fleet-llm-d
spec:
  replicas: 1
  selector:
    matchLabels:
      app: fleet-controller
  template:
    metadata:
      labels:
        app: fleet-controller
    spec:
      containers:
        - name: controller
          image: fleet-controller:e2e
          imagePullPolicy: Never
          args: ["--port=8080", "--ledger-mode=memory", "--rate-limit=0"]
          ports:
            - containerPort: 8080
---
apiVersion: v1
kind: Service
metadata:
  name: fleet-controller
  namespace: fleet-llm-d
spec:
  selector:
    app: fleet-controller
  ports:
    - port: 8080
      targetPort: 8080
  type: NodePort
EOF

echo ""
echo "=== Waiting for controller ==="
kubectl --context "kind-${HUB}" -n fleet-llm-d rollout status deploy/fleet-controller --timeout=120s

# Get the NodePort for cross-cluster access
NODEPORT=$(kubectl --context "kind-${HUB}" -n fleet-llm-d get svc fleet-controller -o jsonpath='{.spec.ports[0].nodePort}')
HUB_IP=$(docker inspect "${HUB}-control-plane" --format '{{.NetworkSettings.Networks.kind.IPAddress}}' 2>/dev/null || \
         podman inspect "${HUB}-control-plane" --format '{{.NetworkSettings.Networks.kind.IPAddress}}' 2>/dev/null)
CONTROLLER_URL="http://${HUB_IP}:${NODEPORT}"

echo ""
echo "=== Controller accessible at $CONTROLLER_URL ==="
curl -s "$CONTROLLER_URL/healthz" && echo ""

# Create cluster identity ConfigMaps on spokes
for spoke in "$SPOKE1" "$SPOKE2"; do
  SPOKE_ID=$(echo "$spoke" | sed 's/fleet-//')
  kubectl --context "kind-${spoke}" create namespace fleet-llm-d 2>/dev/null || true
  kubectl --context "kind-${spoke}" -n fleet-llm-d create configmap fleet-cluster-identity \
    --from-literal=cluster-id="$SPOKE_ID" 2>/dev/null || true
  kubectl --context "kind-${spoke}" -n fleet-llm-d create configmap fleet-control-plane \
    --from-literal=url="$CONTROLLER_URL" 2>/dev/null || true
  echo "ConfigMaps created on $spoke (id=$SPOKE_ID, url=$CONTROLLER_URL)"
done

echo ""
echo "=== Environment ready ==="
echo "Hub:     kind-${HUB} (controller at $CONTROLLER_URL)"
echo "Spoke 1: kind-${SPOKE1}"
echo "Spoke 2: kind-${SPOKE2}"
echo ""
echo "Register spokes:"
echo "  curl -X POST $CONTROLLER_URL/api/v1/clusters -H 'Content-Type: application/json' -d '{\"name\":\"spoke-1\",\"region\":\"us-east\"}'"
echo "  curl -X POST $CONTROLLER_URL/api/v1/clusters -H 'Content-Type: application/json' -d '{\"name\":\"spoke-2\",\"region\":\"us-west\"}'"
