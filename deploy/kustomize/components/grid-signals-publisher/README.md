# Grid Signals publisher component

This optional Kubernetes component deploys the portable, mTLS-only pool signal
publisher. Before enabling it, an overlay must replace:

- `GRID_SIGNALS_SITE`, `GRID_SIGNALS_PROVIDER`,
  `GRID_SIGNALS_SOURCE_URL`, and `GRID_SIGNALS_HEALTH_URL`;
- the `grid-signals-publisher:dev` image with a released digest;
- `grid-signals-server-tls` with the server identity Secret;
- `grid-signals-client-ca` with the client trust Secret; and
- `grid-signals-peer-identity/fingerprints` with the approved client
  certificate SHA-256 fingerprints.

The component intentionally contains no Ingress, Gateway, OpenShift Route,
certificate issuer, or external secret implementation. Add the transport and
identity resources appropriate to the target platform in an overlay. The
transport must preserve TLS through to the publisher so it can verify the
client certificate itself.

The plaintext health listener is for Kubernetes probes only. Do not expose
port `8081` through a Service, Route, Ingress, or Gateway.
