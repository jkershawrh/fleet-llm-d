#!/usr/bin/env bash
set -euo pipefail

# fleet-llm-d Demo Deployment
# Deploys the complete fleet-llm-d stack to any OpenShift cluster.
#
# Usage:
#   ./hack/deploy-demo.sh --cluster-url URL --token TOKEN [--namespace NS] [--ledger-url URL]
#
# Prerequisites: oc CLI, kubeadmin access

NAMESPACE="${NAMESPACE:-fleet-llm-d}"
CONTROLLER_IMAGE="quay.io/fleet-llm-d/fleet-controller:latest"

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"

# --- Output helpers -----------------------------------------------------------

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
CYAN='\033[0;36m'
BOLD='\033[1m'
NC='\033[0m' # No Color

info()    { echo -e "${CYAN}[INFO]${NC}  $*"; }
warn()    { echo -e "${YELLOW}[WARN]${NC}  $*"; }
error()   { echo -e "${RED}[ERROR]${NC} $*" >&2; }
success() { echo -e "${GREEN}[OK]${NC}    $*"; }

# --- Usage --------------------------------------------------------------------

usage() {
  cat <<USAGE
${BOLD}fleet-llm-d Demo Deployment${NC}

Usage:
  $0 --cluster-url URL --token TOKEN [OPTIONS]

Required:
  --cluster-url URL     OpenShift API server URL (e.g. https://api.cluster.example.com:6443)
  --token TOKEN         Authentication token (e.g. from 'oc whoami -t')

Options:
  --namespace NS        Target namespace (default: fleet-llm-d)
  --ledger-url URL      ARE Immutable Ledger gRPC endpoint (e.g. ledger.example.com:9092)
  --skip-crds           Skip CRD installation (useful if CRDs are already present)
  -h, --help            Show this help message

Environment variables:
  NAMESPACE             Override default namespace (--namespace takes precedence)
  CONTROLLER_IMAGE      Override controller container image

Examples:
  $0 --cluster-url https://api.ocp.example.com:6443 --token sha256~abc123
  $0 --cluster-url https://api.ocp.example.com:6443 --token sha256~abc123 \\
     --namespace my-demo --ledger-url ledger.corp.com:9092
USAGE
}

# --- Argument parsing ---------------------------------------------------------

CLUSTER_URL=""
TOKEN=""
LEDGER_URL=""
SKIP_CRDS=false

while [[ $# -gt 0 ]]; do
  case "$1" in
    --cluster-url)
      CLUSTER_URL="$2"; shift 2 ;;
    --token)
      TOKEN="$2"; shift 2 ;;
    --namespace)
      NAMESPACE="$2"; shift 2 ;;
    --ledger-url)
      LEDGER_URL="$2"; shift 2 ;;
    --skip-crds)
      SKIP_CRDS=true; shift ;;
    -h|--help)
      usage; exit 0 ;;
    *)
      error "Unknown argument: $1"
      usage
      exit 1 ;;
  esac
done

if [[ -z "$CLUSTER_URL" ]]; then
  error "Missing required argument: --cluster-url"
  usage
  exit 1
fi

if [[ -z "$TOKEN" ]]; then
  error "Missing required argument: --token"
  usage
  exit 1
fi

# --- Prerequisites check ------------------------------------------------------

check_prerequisites() {
  info "Checking prerequisites..."

  if ! command -v oc &>/dev/null; then
    error "'oc' CLI not found. Install it from https://mirror.openshift.com/pub/openshift-v4/clients/ocp/latest/"
    exit 1
  fi
  success "oc CLI found: $(oc version --client 2>/dev/null | head -1)"

  if ! command -v curl &>/dev/null; then
    error "'curl' not found."
    exit 1
  fi
  success "curl found"
}

# --- Cleanup trap -------------------------------------------------------------

DEPLOY_STARTED=false

cleanup() {
  local exit_code=$?
  if [[ $exit_code -ne 0 && "$DEPLOY_STARTED" == "true" ]]; then
    echo ""
    error "Deployment failed (exit code $exit_code)."
    warn "Resources may have been partially created in namespace '${NAMESPACE}'."
    warn "To clean up:"
    warn "  oc delete project ${NAMESPACE}"
    warn "  oc delete crd -l app.kubernetes.io/part-of=fleet-llm-d"
  fi
}
trap cleanup EXIT

# --- Step 1: Login ------------------------------------------------------------

login_cluster() {
  info "Logging in to cluster: ${CLUSTER_URL}"
  oc login "${CLUSTER_URL}" --token="${TOKEN}" --insecure-skip-tls-verify=true
  success "Logged in as $(oc whoami)"
}

# --- Step 2: Create namespace -------------------------------------------------

create_namespace() {
  info "Creating namespace: ${NAMESPACE}"
  if oc get project "${NAMESPACE}" &>/dev/null; then
    warn "Namespace '${NAMESPACE}' already exists, reusing it."
    oc project "${NAMESPACE}"
  else
    oc new-project "${NAMESPACE}" --display-name="fleet-llm-d Demo" \
      --description="fleet-llm-d multi-cluster LLM orchestration demo"
    success "Namespace '${NAMESPACE}' created."
  fi
}

# --- Step 3: Apply CRDs ------------------------------------------------------

apply_crds() {
  if [[ "$SKIP_CRDS" == "true" ]]; then
    warn "Skipping CRD installation (--skip-crds)."
    return 0
  fi

  info "Applying fleet-llm-d CRDs from ${ROOT_DIR}/api/crds/"

  if [[ ! -d "${ROOT_DIR}/api/crds" ]]; then
    error "CRD directory not found: ${ROOT_DIR}/api/crds"
    exit 1
  fi

  oc apply -f "${ROOT_DIR}/api/crds/"
  success "CRDs applied successfully."

  # Verify CRDs are established
  info "Waiting for CRDs to become established..."
  local crds=(
    fleetinferencepools.fleet.llm-d.ai
    fleetroutingpolicies.fleet.llm-d.ai
    fleetscalingpolicies.fleet.llm-d.ai
    kvcachetransferpolicies.fleet.llm-d.ai
    modellifecycles.fleet.llm-d.ai
    placementpolicies.fleet.llm-d.ai
    tenantprofiles.fleet.llm-d.ai
  )
  for crd in "${crds[@]}"; do
    if oc wait --for=condition=Established "crd/${crd}" --timeout=30s &>/dev/null; then
      success "CRD ready: ${crd}"
    else
      warn "CRD may not be established yet: ${crd}"
    fi
  done
}

# --- Step 4: Deploy fleet-controller ------------------------------------------

deploy_controller() {
  info "Deploying fleet-controller..."

  DEPLOY_STARTED=true

  # Build LEDGER_URL env var block if provided
  local ledger_env=""
  if [[ -n "$LEDGER_URL" ]]; then
    ledger_env="
            - name: LEDGER_URL
              value: \"${LEDGER_URL}\"
            - name: LEDGER_ENABLED
              value: \"true\""
  fi

  cat <<EOF | oc apply -f -
apiVersion: v1
kind: ServiceAccount
metadata:
  name: fleet-controller
  namespace: ${NAMESPACE}
  labels:
    app.kubernetes.io/name: fleet-controller
    app.kubernetes.io/component: controller
    app.kubernetes.io/part-of: fleet-llm-d
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: fleet-controller
  namespace: ${NAMESPACE}
  labels:
    app.kubernetes.io/name: fleet-controller
    app.kubernetes.io/component: controller
    app.kubernetes.io/part-of: fleet-llm-d
spec:
  replicas: 1
  selector:
    matchLabels:
      app.kubernetes.io/name: fleet-controller
      app.kubernetes.io/component: controller
  template:
    metadata:
      labels:
        app.kubernetes.io/name: fleet-controller
        app.kubernetes.io/component: controller
        app.kubernetes.io/part-of: fleet-llm-d
    spec:
      securityContext:
        runAsNonRoot: true
        fsGroup: 65534
      serviceAccountName: fleet-controller
      containers:
        - name: fleet-controller
          image: ${CONTROLLER_IMAGE}
          securityContext:
            runAsNonRoot: true
            runAsUser: 65534
            readOnlyRootFilesystem: true
            allowPrivilegeEscalation: false
            capabilities:
              drop: ["ALL"]
            seccompProfile:
              type: RuntimeDefault
          env:
            - name: NAMESPACE
              value: "${NAMESPACE}"
            - name: LOG_LEVEL
              value: "info"${ledger_env}
          ports:
            - name: api
              containerPort: 8080
              protocol: TCP
            - name: metrics
              containerPort: 9090
              protocol: TCP
          livenessProbe:
            httpGet:
              path: /healthz
              port: 8080
            initialDelaySeconds: 15
            periodSeconds: 10
          readinessProbe:
            httpGet:
              path: /readyz
              port: 8080
            initialDelaySeconds: 5
            periodSeconds: 5
          resources:
            requests:
              cpu: 500m
              memory: 512Mi
            limits:
              cpu: "2"
              memory: 2Gi
---
apiVersion: v1
kind: Service
metadata:
  name: fleet-controller
  namespace: ${NAMESPACE}
  labels:
    app.kubernetes.io/name: fleet-controller
    app.kubernetes.io/component: controller
    app.kubernetes.io/part-of: fleet-llm-d
spec:
  type: ClusterIP
  selector:
    app.kubernetes.io/name: fleet-controller
    app.kubernetes.io/component: controller
  ports:
    - name: api
      port: 8080
      targetPort: 8080
      protocol: TCP
    - name: metrics
      port: 9090
      targetPort: 9090
      protocol: TCP
EOF

  success "fleet-controller resources applied."
}

# --- Step 5: Create OpenShift route -------------------------------------------

create_route() {
  info "Creating OpenShift route for fleet-controller API..."

  cat <<EOF | oc apply -f -
apiVersion: route.openshift.io/v1
kind: Route
metadata:
  name: fleet-controller-api
  namespace: ${NAMESPACE}
  labels:
    app.kubernetes.io/name: fleet-controller
    app.kubernetes.io/component: controller
    app.kubernetes.io/part-of: fleet-llm-d
spec:
  to:
    kind: Service
    name: fleet-controller
    weight: 100
  port:
    targetPort: api
  tls:
    termination: edge
    insecureEdgeTerminationPolicy: Redirect
  wildcardPolicy: None
EOF

  success "Route created."
}

# --- Step 6: Wait for rollout -------------------------------------------------

wait_for_rollout() {
  info "Waiting for fleet-controller rollout to complete..."
  oc rollout status deployment/fleet-controller -n "${NAMESPACE}" --timeout=120s
  success "fleet-controller is running."
}

# --- Step 7: Health check -----------------------------------------------------

health_check() {
  info "Running health check..."

  local route_url
  route_url="$(oc get route fleet-controller-api -n "${NAMESPACE}" -o jsonpath='{.spec.host}')"

  if [[ -z "$route_url" ]]; then
    error "Could not determine route URL."
    return 1
  fi

  local health_url="https://${route_url}/healthz"
  info "Checking ${health_url}"

  local attempts=0
  local max_attempts=12
  while [[ $attempts -lt $max_attempts ]]; do
    if curl -sk --max-time 5 "${health_url}" | grep -qi "ok\|healthy\|alive"; then
      success "Health check passed."
      return 0
    fi
    attempts=$((attempts + 1))
    info "Attempt ${attempts}/${max_attempts} - waiting for health endpoint..."
    sleep 5
  done

  warn "Health check did not return expected response after ${max_attempts} attempts."
  warn "The controller may still be starting. Check manually:"
  warn "  curl -sk ${health_url}"
}

# --- Step 8: Register managed cluster ----------------------------------------

register_managed_cluster() {
  info "Registering this cluster as a managed fleet member..."

  local cluster_id
  cluster_id="$(oc get clusterversion version -o jsonpath='{.spec.clusterID}' 2>/dev/null || echo "demo-$(date +%s)")"

  local cluster_api
  cluster_api="${CLUSTER_URL}"

  cat <<EOF | oc apply -f -
apiVersion: v1
kind: ConfigMap
metadata:
  name: managed-cluster-info
  namespace: ${NAMESPACE}
  labels:
    app.kubernetes.io/name: fleet-controller
    app.kubernetes.io/part-of: fleet-llm-d
    fleet.llm-d.ai/managed-cluster: "true"
  annotations:
    fleet.llm-d.ai/cluster-id: "${cluster_id}"
    fleet.llm-d.ai/cluster-api: "${cluster_api}"
    fleet.llm-d.ai/registered-at: "$(date -u +%Y-%m-%dT%H:%M:%SZ)"
data:
  cluster-id: "${cluster_id}"
  cluster-api: "${cluster_api}"
  registration-mode: "self"
EOF

  success "Cluster registered as managed member (id: ${cluster_id})."
}

# --- Step 9: Notify ARE ledger ------------------------------------------------

notify_ledger() {
  if [[ -z "$LEDGER_URL" ]]; then
    return 0
  fi

  info "Posting fleet.demo.deployed event to ARE ledger at ${LEDGER_URL}..."

  local route_url
  route_url="$(oc get route fleet-controller-api -n "${NAMESPACE}" -o jsonpath='{.spec.host}')"

  local payload
  payload=$(cat <<EOJSON
{
  "event_type": "fleet.demo.deployed",
  "timestamp": "$(date -u +%Y-%m-%dT%H:%M:%SZ)",
  "source": "deploy-demo.sh",
  "data": {
    "namespace": "${NAMESPACE}",
    "cluster_url": "${CLUSTER_URL}",
    "controller_image": "${CONTROLLER_IMAGE}",
    "route_url": "https://${route_url}",
    "deployed_by": "$(oc whoami 2>/dev/null || echo unknown)"
  }
}
EOJSON
  )

  # The ledger listens on gRPC (port 9092), but we attempt an HTTP POST
  # to a REST gateway if available. Failures here are non-fatal.
  local ledger_http="${LEDGER_URL}"
  # Strip trailing port if present and assume REST gateway on same host
  ledger_http="${ledger_http%%:*}"

  if curl -sk --max-time 10 \
    -X POST \
    -H "Content-Type: application/json" \
    -d "${payload}" \
    "https://${ledger_http}:9092/v1/events" 2>/dev/null; then
    success "Ledger event posted."
  else
    warn "Could not reach ARE ledger at ${LEDGER_URL}. Event not recorded."
    warn "This is non-fatal; the deployment is still complete."
  fi
}

# --- Step 10: Print summary ---------------------------------------------------

print_summary() {
  local route_url
  route_url="$(oc get route fleet-controller-api -n "${NAMESPACE}" -o jsonpath='{.spec.host}')"

  local auth_token
  auth_token="$(oc create token fleet-controller -n "${NAMESPACE}" --duration=24h 2>/dev/null || oc whoami -t 2>/dev/null || echo '<TOKEN>')"

  echo ""
  echo -e "${GREEN}${BOLD}============================================================${NC}"
  echo -e "${GREEN}${BOLD}  fleet-llm-d Demo Deployment Complete${NC}"
  echo -e "${GREEN}${BOLD}============================================================${NC}"
  echo ""
  echo -e "  ${BOLD}Namespace:${NC}     ${NAMESPACE}"
  echo -e "  ${BOLD}Controller:${NC}    ${CONTROLLER_IMAGE}"
  echo -e "  ${BOLD}API Route:${NC}     https://${route_url}"
  echo -e "  ${BOLD}Health:${NC}        https://${route_url}/healthz"
  echo -e "  ${BOLD}Metrics:${NC}       http://fleet-controller.${NAMESPACE}.svc:9090/metrics  (cluster-internal)"
  if [[ -n "$LEDGER_URL" ]]; then
    echo -e "  ${BOLD}ARE Ledger:${NC}    ${LEDGER_URL}"
  fi
  echo ""
  echo -e "  ${BOLD}Auth Token${NC} (24h, for demo use):"
  echo -e "    ${auth_token}"
  echo ""
  echo -e "  ${BOLD}Sample Commands:${NC}"
  echo ""
  echo -e "    ${CYAN}# Health check${NC}"
  echo "    curl -sk https://${route_url}/healthz"
  echo ""
  echo -e "    ${CYAN}# List fleet inference pools${NC}"
  echo "    curl -sk -H \"Authorization: Bearer ${auth_token}\" \\"
  echo "      https://${route_url}/apis/fleet.llm-d.ai/v1alpha1/fleetinferencepools"
  echo ""
  echo -e "    ${CYAN}# Use fleetctl${NC}"
  echo "    ${ROOT_DIR}/bin/fleetctl --endpoint https://${route_url} status"
  echo ""
  echo -e "${GREEN}${BOLD}============================================================${NC}"
  echo ""
}

# --- Main ---------------------------------------------------------------------

main() {
  echo ""
  echo -e "${BOLD}fleet-llm-d Demo Deployment${NC}"
  echo -e "Deploying to ${CYAN}${CLUSTER_URL}${NC} in namespace ${CYAN}${NAMESPACE}${NC}"
  echo ""

  check_prerequisites
  login_cluster
  create_namespace
  apply_crds
  deploy_controller
  create_route
  wait_for_rollout
  health_check
  register_managed_cluster
  notify_ledger
  print_summary

  success "Deployment finished."
}

main
