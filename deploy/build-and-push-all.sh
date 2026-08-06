#!/usr/bin/env bash
set -euo pipefail

# Build all ecosystem images and push to internal OpenShift registries.
# Usage:
#   ./deploy/build-and-push-all.sh              # build + push to all clusters
#   ./deploy/build-and-push-all.sh build-only   # build only, no push
#   ./deploy/build-and-push-all.sh push-only    # push only (images must exist)

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
PLATFORM="${PLATFORM:-linux/amd64}"
MODE="${1:-all}"

# Cluster registry routes (set via /etc/hosts → internal IPs)
OBERON_REGISTRY="default-route-openshift-image-registry.apps.oberon.fm2aihpcsed.com"
ARENA_REGISTRY="default-route-openshift-image-registry.apps.arena.fm2aihpcsed.com"
BRUTUS_REGISTRY="default-route-openshift-image-registry.apps.brutus.fm2aihpcsed.com"

# Ecosystem repo paths
ARE_LEDGER_ROOT="${ARE_LEDGER_ROOT:-$PROJECT_ROOT/../are-immutable-ledger}"
DEEPFIELD_ROOT="${DEEPFIELD_ROOT:-$PROJECT_ROOT/../deepfield-fleet}"
GCL_ROOT="${GCL_ROOT:-$PROJECT_ROOT/../governed-cognitive-loop}"

login_registry() {
  local registry=$1
  local kubeconfig=$2
  echo "--- Logging into $registry ---"
  local token
  token=$(KUBECONFIG="$kubeconfig" oc whoami -t 2>/dev/null) || {
    echo "WARNING: could not get token for $registry — skipping login"
    return 1
  }
  podman login --tls-verify=false -u kubeadmin -p "$token" "$registry"
}

build_images() {
  echo ""
  echo "=========================================="
  echo "  Building all images"
  echo "=========================================="

  echo "=== fleet-controller ==="
  podman build --platform "$PLATFORM" \
    -t fleet-controller:latest \
    -f "$PROJECT_ROOT/deploy/docker/Dockerfile.controller" "$PROJECT_ROOT"

  echo "=== fleet-agent ==="
  podman build --platform "$PLATFORM" \
    -t fleet-agent:latest \
    -f "$PROJECT_ROOT/deploy/docker/Dockerfile.agent" "$PROJECT_ROOT"

  echo "=== mock-inference ==="
  podman build --platform "$PLATFORM" \
    -t mock-inference:latest \
    -f "$PROJECT_ROOT/deploy/oberon/Dockerfile.mock-inference" "$PROJECT_ROOT"

  echo "=== modelplane-mock ==="
  podman build --platform "$PLATFORM" \
    -t modelplane-mock:latest \
    -f "$PROJECT_ROOT/deploy/docker/Dockerfile.modelplane-mock" "$PROJECT_ROOT"

  echo "=== are-immutable-ledger (Rust core) ==="
  podman build --platform "$PLATFORM" \
    -t are-immutable-ledger:latest \
    -f "$ARE_LEDGER_ROOT/Dockerfile" "$ARE_LEDGER_ROOT"

  echo "=== are-ledger-gateway (Python REST) ==="
  podman build --platform "$PLATFORM" \
    -t are-ledger-gateway:latest \
    -f "$ARE_LEDGER_ROOT/api/Dockerfile" "$ARE_LEDGER_ROOT"

  echo "=== deepfield-fleet ==="
  podman build --platform "$PLATFORM" \
    -t deepfield-fleet:latest \
    -f "$DEEPFIELD_ROOT/Containerfile" "$DEEPFIELD_ROOT"

  echo "=== gcl-demo ==="
  podman build --platform "$PLATFORM" \
    -t gcl-demo:latest \
    -f "$GCL_ROOT/Containerfile" "$GCL_ROOT"

  echo "=== praxis-ai (mirror from GHCR) ==="
  podman pull --platform "$PLATFORM" ghcr.io/praxis-proxy/ai:latest
  podman tag ghcr.io/praxis-proxy/ai:latest praxis-ai:latest

  echo ""
  echo "All images built."
}

push_to_oberon() {
  echo ""
  echo "=========================================="
  echo "  Pushing to Oberon (hub)"
  echo "=========================================="
  login_registry "$OBERON_REGISTRY" "$HOME/.kube/config" || return 1

  # fleet-llm-d namespace images
  for img in fleet-controller fleet-agent mock-inference modelplane-mock deepfield-fleet praxis-ai; do
    echo "--- Pushing $img → Oberon/fleet-llm-d ---"
    podman tag "${img}:latest" "${OBERON_REGISTRY}/fleet-llm-d/${img}:latest"
    podman push --tls-verify=false "${OBERON_REGISTRY}/fleet-llm-d/${img}:latest"
  done

  # immutable-ledger namespace images
  for img in are-immutable-ledger are-ledger-gateway; do
    echo "--- Pushing $img → Oberon/immutable-ledger ---"
    podman tag "${img}:latest" "${OBERON_REGISTRY}/immutable-ledger/${img}:latest"
    podman push --tls-verify=false "${OBERON_REGISTRY}/immutable-ledger/${img}:latest"
  done

  # governed-cognitive-loop namespace images
  echo "--- Pushing gcl-demo → Oberon/governed-cognitive-loop ---"
  podman tag "gcl-demo:latest" "${OBERON_REGISTRY}/governed-cognitive-loop/gcl-demo:latest"
  podman push --tls-verify=false "${OBERON_REGISTRY}/governed-cognitive-loop/gcl-demo:latest"
}

push_to_arena() {
  echo ""
  echo "=========================================="
  echo "  Pushing to Arena (CPU spoke)"
  echo "=========================================="
  login_registry "$ARENA_REGISTRY" "$HOME/.kube/config" || return 1

  echo "--- Pushing fleet-agent → Arena/fleet-llm-d ---"
  podman tag "fleet-agent:latest" "${ARENA_REGISTRY}/fleet-llm-d/fleet-agent:latest"
  podman push --tls-verify=false "${ARENA_REGISTRY}/fleet-llm-d/fleet-agent:latest"
}

push_to_brutus() {
  echo ""
  echo "=========================================="
  echo "  Pushing to Brutus (GPU spoke)"
  echo "=========================================="
  login_registry "$BRUTUS_REGISTRY" "$HOME/Downloads/kubeconfig-brutus" || return 1

  echo "--- Pushing fleet-agent → Brutus/fleet-llm-d ---"
  podman tag "fleet-agent:latest" "${BRUTUS_REGISTRY}/fleet-llm-d/fleet-agent:latest"
  podman push --tls-verify=false "${BRUTUS_REGISTRY}/fleet-llm-d/fleet-agent:latest"
}

case "$MODE" in
  build-only)
    build_images
    ;;
  push-only)
    push_to_oberon
    push_to_arena
    push_to_brutus
    ;;
  all|*)
    build_images
    push_to_oberon
    push_to_arena
    push_to_brutus
    ;;
esac

echo ""
echo "=========================================="
echo "  Done"
echo "=========================================="
echo ""
echo "Internal registry image references:"
echo "  Oberon:  image-registry.openshift-image-registry.svc:5000/fleet-llm-d/<image>:latest"
echo "  Oberon:  image-registry.openshift-image-registry.svc:5000/immutable-ledger/<image>:latest"
echo "  Oberon:  image-registry.openshift-image-registry.svc:5000/governed-cognitive-loop/<image>:latest"
echo "  Arena:   image-registry.openshift-image-registry.svc:5000/fleet-llm-d/fleet-agent:latest"
echo "  Brutus:  image-registry.openshift-image-registry.svc:5000/fleet-llm-d/fleet-agent:latest"
