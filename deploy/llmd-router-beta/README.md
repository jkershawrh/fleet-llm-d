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
