# llm-d Router beta qualification

These values deploy one shadow EPP per exact physical model. They consume the
controller-owned `fleet-router-endpoints` ConfigMap and do not expose a client
proxy, so the validated Praxis path remains unchanged during discovery and
configuration qualification.

The shadow EPPs scrape each provider's optional Grid Signals endpoint over
verified mTLS. The current direct OVMS and vLLM providers publish health only,
so this profile retains random selection within the fleet-qualified provider
set. Queue and KV scorers must not be enabled until cluster-local EPPs publish
those aggregates; missing load signals are not replaced with synthetic data.

The beta currently tracks the upstream `main` EPP image because
`multicluster-file-discovery` landed after the v0.10.0 release. Resolve and
record the pulled image digest during qualification; do not promote this
overlay until a released image includes the plugin.

```sh
helm upgrade --install fleet-router-cpu \
  oci://ghcr.io/llm-d/charts/llm-d-router-standalone \
  --version v0 --namespace fleet-llm-d \
  -f deploy/llmd-router-beta/values-cpu.yaml

helm upgrade --install fleet-router-gpu \
  oci://ghcr.io/llm-d/charts/llm-d-router-standalone \
  --version v0 --namespace fleet-llm-d \
  -f deploy/llmd-router-beta/values-gpu.yaml
```

The next qualification step adds an HTTPS-capable proxy. The stock standalone
Envoy profile is plaintext upstream and must not be pointed directly at the
re-encrypt OpenShift Routes.
