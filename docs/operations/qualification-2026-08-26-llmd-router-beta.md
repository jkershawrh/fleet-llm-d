# llm-d Router beta qualification checkpoint — 2026-08-26

## Scope

This checkpoint started the upstream-native Router qualification on Oberon
without replacing the certified Praxis inference path. Governance and optional
observability integrations remained disabled.

## Deployed state

- Controller routing adapter: `llm-d-router`.
- Endpoint publication: `fleet-router-endpoints` ConfigMap, updated by the
  controller through a resource-name-scoped RBAC grant.
- CPU shadow EPP: `fleet-router-cpu-epp`.
- GPU shadow EPP: `fleet-router-gpu-epp`.
- Router image resolved by OpenShift:
  `ghcr.io/llm-d/llm-d-router-endpoint-picker@sha256:b1eb81f01ea9b56271f0fd536380454d0d82bc03bc59dab7197764da11ffa2ff`.
- Upstream build commit reported by EPP: `b7a7c4c5600319e2f9b0c513e1796003eb5a38c9`.
- Upstream chart reference: `llm-d-router-standalone:v0`, chart digest
  `sha256:1c1414c497cf3a8ffffa1408bf6583e461116b3de1e889a60dab3d7243c82551`.
- Fleet controller image:
  `image-registry.openshift-image-registry.svc:5000/fleet-llm-d/fleet-controller@sha256:7d1aedc715f04f776fbb0dce38912928fe1227de1c6c2aa3a2a4b09a7df64e24`.
- Oberon agent image:
  `image-registry.openshift-image-registry.svc:5000/fleet-llm-d/fleet-agent@sha256:b0a699083029cc57e174ab5a768c79cf234975734d6311e9a40c4019a87cf0f1`.
- Arena and Brutus agent image:
  `image-registry.openshift-image-registry.svc:5000/fleet-llm-d/fleet-agent@sha256:d3a174cc790357cdbd4f990906c6ad8c760f0e9fa70da942c441e9ce68566bf8`.

## Results

- The controller publishes deterministic, model-separated files.
- CPU file contains only Oberon and Arena, both over verified HTTPS Route
  hostnames.
- GPU file contains only Brutus and the exact physical model
  `ibm-granite/granite-3.1-8b-instruct`.
- The CPU EPP metric inventory contains the two expected endpoint identities.
- The GPU EPP metric inventory contains the one expected endpoint identity.
- Both EPPs started in file-discovery mode with
  `multicluster-file-discovery` and `watchFile: true`.
- Existing authenticated Praxis requests still returned HTTP 200:
  - CPU: `granite-2b-cpu`, routed to `oberon-cpu` during the check.
  - GPU: exact 8B model, routed to `brutus-h100`.
- Controller, gateway, Praxis, both EPPs, and all three agents were Ready with
  zero restarts after rollout.

## Defects found and corrected

- Local-only agent inference addresses were not usable as fleet routing
  endpoints. Agents now advertise transport-neutral routing, metrics, and TLS
  metadata; OpenShift Route values remain environment-overlay data.
- `nvidia.com/gpu >= 1` was misparsed as a GPU type. Kubernetes GPU resource
  constraints are now supported and malformed hardware rules fail closed.
- `cpu in ['intel-amx']` was unsupported. CPU feature capability matching is
  now explicit and fail closed.
- Legacy pool membership could label the Brutus 8B endpoint as CPU-compatible.
  Router output now enforces exact physical-model capability labels before
  publishing an endpoint.

## Not yet accepted

Router inference is not yet qualified. The shadow EPPs intentionally have no
client proxy. The stock standalone Envoy profile sends plaintext upstream,
while the fleet uses re-encrypt OpenShift Routes requiring verified TLS, SNI,
and authority handling.

`llm_d_epp_ready_endpoints` is currently zero. Discovery itself is proven by
the per-endpoint metrics, but the default EPP metrics source cannot establish
valid readiness for the current endpoints: the OVMS Routes do not expose the
required pool-level `/metrics` contract, and the default source is not the
TLS-verified multicluster metrics pipeline.

## Next acceptance slice

1. Publish the allowlisted pool-level Grid Signals metrics contract for both
   CPU providers and the GPU provider.
2. Configure `multicluster-metrics-data-source` with Route CA verification,
   response-size and freshness bounds, plus `multicluster-metrics-extractor`.
3. Require EPP readiness counts of CPU=2 and GPU=1.
4. Add a TLS-capable shadow proxy with dynamic authority/SNI and no public
   Route initially.
5. Send authenticated CPU and GPU requests directly through the shadow Router,
   verify streaming/error propagation, then run drain and stale-provider
   convergence tests before considering gateway cutover.
