# In-cluster certification jobs

These Jobs remove workstation kubeconfig and administrator-token lifetime from
release certification:

- `fleet-certification-traffic` runs on the Oberon control-plane cluster and
  calls the internal inference-gateway Service. Its bearer token is supplied by
  the `fleet-certification-credentials` Secret and is never embedded in a Job.
- `fleet-certification-resource-snapshot` runs independently on every provider
  cluster. Its namespaced service account may only read Pods and PodMetrics.
  Patch `FLEET_CERT_TARGET`, `FLEET_CERT_SELECTOR`, and the image digest for the
  local backend before applying it.

Traffic and resource reports use separate versioned JSON schemas. A passing
traffic report is not a certification by itself: every applicable provider
must also produce a ready, zero-restart resource report below the configured
guardrail. Public OpenShift Route and TLS latency is measured by a separate
external probe and is not conflated with the internal data-plane benchmark.
