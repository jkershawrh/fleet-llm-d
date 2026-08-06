#!/usr/bin/env bash
set -euo pipefail

REGISTRY="${REGISTRY:-quay.io/rh-ee-jkershaw}"
PLATFORM="${PLATFORM:-linux/amd64}"

echo "=== Building fleet-controller ==="
podman build --platform "$PLATFORM" \
  -t "$REGISTRY/fleet-controller:latest" \
  -f deploy/docker/Dockerfile.controller .

echo "=== Building modelplane-mock ==="
podman build --platform "$PLATFORM" \
  -t "$REGISTRY/modelplane-mock:latest" \
  -f deploy/docker/Dockerfile.modelplane-mock .

echo "=== Building mock-inference ==="
podman build --platform "$PLATFORM" \
  -t "$REGISTRY/mock-inference:latest" \
  -f deploy/oberon/Dockerfile.mock-inference .

echo "=== Building fleet-agent ==="
podman build --platform "$PLATFORM" \
  -t "$REGISTRY/fleet-agent:latest" \
  -f deploy/docker/Dockerfile.agent .

echo "=== Mirroring Praxis AI (external) ==="
podman pull ghcr.io/praxis-proxy/ai:latest
podman tag ghcr.io/praxis-proxy/ai:latest "$REGISTRY/praxis-ai:latest"

echo "=== Pushing images ==="
podman push "$REGISTRY/fleet-controller:latest"
podman push "$REGISTRY/modelplane-mock:latest"
podman push "$REGISTRY/mock-inference:latest"
podman push "$REGISTRY/fleet-agent:latest"
podman push "$REGISTRY/praxis-ai:latest"

echo "=== Done ==="
echo "Images pushed to $REGISTRY"
