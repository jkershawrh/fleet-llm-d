# In-cluster certification jobs

These Jobs remove workstation kubeconfig and administrator-token lifetime from
release certification:

- `fleet-certification-traffic` runs on the Oberon control-plane cluster and
  calls the internal inference-gateway Service by default. Set
  `FLEET_CERT_FLEET_URL` to the isolated Router qualification Service for a
  Router run; the NetworkPolicies admit only those two gateway identities. Its
  bearer token is supplied by the `fleet-certification-credentials` Secret and
  is never embedded in a Job.
- `fleet-certification-resource-snapshot` runs independently on every provider
  cluster. Its namespaced service account may only read Pods and PodMetrics.
  Patch `FLEET_CERT_TARGET`, `FLEET_CERT_SELECTOR`, and the image digest for the
  local backend before applying it.

Traffic and resource reports use separate versioned JSON schemas. A passing
traffic report is not a certification by itself: every applicable provider
must also produce a ready, zero-restart resource report below the configured
guardrail. Public OpenShift Route and TLS latency is measured by a separate
external probe and is not conflated with the internal data-plane benchmark.

`traffic-job-arena-router.yaml` runs that external probe from Arena through
the TLS Router qualification Route on Oberon. It is pinned to the Arena-local
certification image digest and records `source_cluster=arena` plus
`transport=external-route`. Apply its separate NetworkPolicy on Arena; it
grants only DNS and HTTPS egress to the labeled certification pod.
Because the environment uses split DNS and a private ingress CA, the Arena
overlay maps the Oberon ingress node address to the Route hostname and mounts
the copied public `fleet-route-ca` ConfigMap. SNI, hostname checks, and CA
verification remain enabled; do not replace this with `--insecure`.

Traffic Jobs ignore Kubernetes `DisruptionTarget` failures so node-pressure
preemption creates a replacement pod instead of a false inference failure.
Each replacement restarts the complete requested measurement interval; partial
runs are not merged into certification evidence. Other process failures retain
a bounded retry count and the Job has a four-hour overall deadline.
