#!/usr/bin/env bash
set -euo pipefail

if [[ $# -ne 2 ]]; then
  echo "usage: $0 KUBE_CONTEXT CONTROLLER_IMAGE_BY_DIGEST" >&2
  exit 2
fi

context=$1
image=$2
namespace=fleet-llm-d

if [[ $image != *@sha256:* ]]; then
  echo "controller image must be pinned by digest" >&2
  exit 2
fi

for deployment in fleet-router-cpu-epp fleet-router-gpu-epp; do
  oc --context "$context" -n "$namespace" set volume deployment/"$deployment" \
    --add --overwrite --name=fleet-router-endpoints --type=emptydir
  oc --context "$context" -n "$namespace" patch deployment "$deployment" --type=strategic --patch "$(
    jq -cn --arg image "$image" '{spec:{template:{spec:{containers:[{name:"epp",volumeMounts:[{name:"fleet-router-endpoints",mountPath:"/var/run/fleet-router",readOnly:true}]},{name:"endpoint-mirror",image:$image,args:["--mode=endpoint-mirror","--kube-api=https://kubernetes.default.svc","--namespace=fleet-llm-d","--ledger-mode=disabled"],env:[{name:"LLMD_ROUTER_ENDPOINTS_CONFIGMAP",value:"fleet-router-endpoints"},{name:"LLMD_ROUTER_ENDPOINTS_DIR",value:"/var/run/fleet-router"}],resources:{requests:{cpu:"10m",memory:"32Mi"},limits:{cpu:"100m",memory:"64Mi"}},securityContext:{runAsNonRoot:true,allowPrivilegeEscalation:false,readOnlyRootFilesystem:true,capabilities:{drop:["ALL"]}},volumeMounts:[{name:"fleet-router-endpoints",mountPath:"/var/run/fleet-router"}]}]}}}}'
  )"
  oc --context "$context" -n "$namespace" rollout status deployment/$deployment --timeout=180s
done
