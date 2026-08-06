#!/usr/bin/env bash
set -euo pipefail

if [[ -z "${OBERON_CONTROLLER_URL:-}" ]]; then
  echo "ERROR: OBERON_CONTROLLER_URL must be set (e.g. https://fleet-controller-fleet-llm-d.apps.oberon.fm2aihpcsed.com)"
  exit 1
fi

echo "=== Deploying fleet-llm-d GPU spoke to Brutus ==="
echo "Hub controller: $OBERON_CONTROLLER_URL"

echo "--- Creating namespace ---"
oc create namespace fleet-llm-d --dry-run=client -o yaml | oc apply -f -

echo "--- Deploying vLLM GPU inference (Granite 3.1 8B on H100 NVL) ---"
oc apply -f deploy/brutus/vllm-gpu-inference.yaml

echo "--- Applying network policies ---"
oc apply -f deploy/brutus/network-policies.yaml

echo "--- Deploying fleet-agent ---"
sed "s|FLEET_CONTROL_PLANE_URL_PLACEHOLDER|${OBERON_CONTROLLER_URL}|g" \
  deploy/brutus/fleet-agent.yaml | oc apply -f -

echo "--- Waiting for rollouts ---"
echo "Waiting for vLLM GPU inference (model download + load may take several minutes)..."
oc rollout status deploy/vllm-granite-8b-gpu -n fleet-llm-d --timeout=600s
echo "Waiting for fleet-agent..."
oc rollout status deploy/fleet-agent -n fleet-llm-d --timeout=120s

echo ""
echo "=== Health checks ==="
echo "Fleet agent proxy:   http://fleet-agent.fleet-llm-d.svc:8090/healthz"
echo "vLLM GPU inference:  http://vllm-granite-8b-gpu.fleet-llm-d.svc:8000/health"

echo ""
echo "=== Brutus GPU spoke deployment complete ==="
echo "Cluster ID: brutus-h100"
echo "GPU:        NVIDIA H100 NVL (94 GB)"
echo "Model:      ibm-granite/granite-3.1-8b-instruct (vLLM CUDA BF16)"
echo "Hub:        $OBERON_CONTROLLER_URL"
