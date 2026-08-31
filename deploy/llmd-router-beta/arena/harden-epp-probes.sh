#!/usr/bin/env bash
set -euo pipefail

context=${1:?usage: harden-epp-probes.sh KUBE_CONTEXT}
namespace=${2:-fleet-llm-d}
patch='{"spec":{"template":{"spec":{"containers":[{"name":"epp","startupProbe":{"grpc":{"port":9003,"service":"readiness"},"periodSeconds":2,"timeoutSeconds":3,"failureThreshold":60},"readinessProbe":{"grpc":{"port":9003,"service":"readiness"},"periodSeconds":5,"timeoutSeconds":3,"failureThreshold":3},"livenessProbe":{"grpc":{"port":9003,"service":"liveness"},"initialDelaySeconds":10,"periodSeconds":15,"timeoutSeconds":3,"failureThreshold":6}}]}}}}'

for deployment in fleet-router-cpu-epp fleet-router-gpu-epp; do
  oc --context="$context" -n "$namespace" patch deployment "$deployment" --type=strategic -p "$patch"
  oc --context="$context" -n "$namespace" rollout status deployment "$deployment" --timeout=5m
done
