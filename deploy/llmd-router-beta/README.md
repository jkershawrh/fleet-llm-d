# llm-d Router beta qualification

These values deploy one active EPP and one warm standby per exact physical
model. Upstream leader election keeps routing state authoritative on one EPP;
preferred pod anti-affinity spreads the pair when the cluster has multiple
nodes. Apply `epp-pdb.yaml` after the Helm releases to retain one member during
voluntary disruption. The EPPs consume the controller-owned
`fleet-router-endpoints` ConfigMap and do not expose a client proxy, so the
validated Praxis path remains unchanged during discovery and configuration
qualification.

The shadow EPPs scrape each provider's optional Grid Signals endpoint over
verified mTLS. The current direct OVMS and vLLM providers publish health only,
so this profile retains random selection within the fleet-qualified provider
set. Queue and KV scorers must not be enabled until cluster-local EPPs publish
those aggregates; missing load signals are not replaced with synthetic data.

The beta uses the qualified upstream `main` EPP build by immutable digest
because `multicluster-file-discovery` landed after the v0.10.0 release. Update
the digest only through a new qualification run; do not promote this overlay
to a released Router channel until a released image includes the plugin.

```sh
helm upgrade --install fleet-router-cpu \
  oci://ghcr.io/llm-d/charts/llm-d-router-standalone \
  --version v0 --namespace fleet-llm-d \
  -f deploy/llmd-router-beta/values-cpu.yaml

helm upgrade --install fleet-router-gpu \
  oci://ghcr.io/llm-d/charts/llm-d-router-standalone \
  --version v0 --namespace fleet-llm-d \
  -f deploy/llmd-router-beta/values-gpu.yaml

oc apply -f deploy/llmd-router-beta/epp-pdb.yaml
```

`router-proxies.yaml` adds isolated CPU and GPU Envoy Services for inference
qualification. Each proxy uses the EPP as a fail-closed external processor,
removes caller-supplied destination headers before EPP processing, rewrites
authority from the EPP-selected endpoint, and verifies the selected Route with
dynamic SNI against the mounted `fleet-route-ca` bundle. The Services have no
Route; only the qualification gateway NetworkPolicy identity may call them.

`qualification-gateway.yaml` deploys that isolated gateway with the
`llm-d-router` inference provider and a separate OpenShift Route. It retains
tenant authentication, PostgreSQL state, semantic classification, exact-model
resolution, and provider health checks while disabling the optional ledger for
core data-plane qualification. Replace `QUALIFICATION_IMAGE_DIGEST` with the
controller digest built from the commit under test before applying it. The
production `fleet-inference-gateway` and Praxis resources are not modified.

The proxy permits one retry for connection failures, resets, or refused
streams. Envoy retries only before response headers are returned; an active
stream is never replayed. The stock standalone Envoy profile remains
unsuitable because it sends plaintext upstream.

## Arena-hosted certification profile

`arena/kustomization.yaml` moves only the isolated qualification gateway and
proxies to Arena. Install the two EPP Helm releases there with the same pinned
values, copy the controller-published endpoint ConfigMap and mTLS identities
directly between cluster APIs, and build the qualification controller image in
Arena's registry. The Arena gateway intentionally uses in-memory admission
state and no semantic classifier for core data-plane certification; governed
PostgreSQL, ledger, and classification qualification remains a separate
profile. Oberon's production controller, Praxis, and gateway are unchanged,
while Brutus remains the exact Granite 8B GPU execution provider.

Build the Arena qualification overlay with the explicit parent-resource
allowance:

```sh
oc kustomize --load-restrictor LoadRestrictionsNone \
  deploy/llmd-router-beta/arena
```

After the 15-minute local-gateway canary passes, render the eight-hour Job with:

```sh
oc kustomize deploy/certification/arena-durability
```

The upstream standalone chart currently hard-codes one-second EPP probe
timeouts. Apply `arena/harden-epp-probes.sh KUBE_CONTEXT` after each Helm
upgrade. The qualification profile tolerates short node scheduling stalls but
still removes an unready EPP promptly; this post-render patch can be removed
when the chart exposes equivalent probe values.

The chart also exposes EPP volumes but not arbitrary sidecars. To meet the
30-second provider-removal SLO, the Arena profile replaces the projected
ConfigMap volume with an `emptyDir` and adds the controller's endpoint-mirror
mode as a least-privilege sidecar. The mirror polls the authoritative
ConfigMap, reloads rotating service-account tokens, and atomically updates the
EPP's watched files without depending on kubelet projection timing. Apply the
access resources and post-Helm patch after each Router upgrade:

```sh
oc apply -f deploy/llmd-router-beta/arena/endpoint-mirror-access.yaml
deploy/llmd-router-beta/arena/configure-epp-endpoint-mirror.sh \
  KUBE_CONTEXT CONTROLLER_IMAGE_BY_DIGEST
```
