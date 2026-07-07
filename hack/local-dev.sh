#!/usr/bin/env bash
# Creates 3 Kind clusters and deploys fleet-llm-d for local development
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"

CLUSTERS=("fleet-hub" "fleet-spoke-1" "fleet-spoke-2")
KIND_IMAGE="${KIND_IMAGE:-kindest/node:v1.31.0}"

echo "==> Creating Kind clusters for fleet-llm-d local development"

for cluster in "${CLUSTERS[@]}"; do
    if kind get clusters 2>/dev/null | grep -q "^${cluster}$"; then
        echo "    Cluster ${cluster} already exists, skipping"
    else
        echo "    Creating cluster ${cluster}..."
        kind create cluster --name "${cluster}" --image "${KIND_IMAGE}" --wait 60s
    fi
done

echo "==> Applying CRDs to all clusters"
for cluster in "${CLUSTERS[@]}"; do
    echo "    Applying CRDs to ${cluster}..."
    for crd in "${ROOT_DIR}"/api/crds/*.yaml; do
        kubectl apply -f "$crd" --context "kind-${cluster}"
    done
done

echo "==> Deploying fleet-controller to hub cluster"
kubectl apply -k "${ROOT_DIR}/deploy/kustomize/base" --context "kind-fleet-hub"

echo "==> Deploying fleet-agent to spoke clusters"
for cluster in "${CLUSTERS[@]:1}"; do
    echo "    Deploying fleet-agent to ${cluster}..."
    kubectl apply -f "${ROOT_DIR}/deploy/kustomize/base/fleet-agent.yaml" --context "kind-${cluster}"
done

echo "==> Waiting for deployments to be ready"
kubectl wait --for=condition=available deployment/fleet-controller -n fleet-llm-d --timeout=120s --context "kind-fleet-hub" || true

echo ""
echo "Fleet development environment is ready!"
echo ""
echo "Clusters:"
for cluster in "${CLUSTERS[@]}"; do
    echo "  - ${cluster} (context: kind-${cluster})"
done
echo ""
echo "To tear down: hack/local-dev-teardown.sh or:"
echo "  for c in ${CLUSTERS[*]}; do kind delete cluster --name \$c; done"
