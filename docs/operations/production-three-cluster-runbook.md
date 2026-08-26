# Three-cluster production runbook

The supported inference entry point is the Route exposing the controller's
OpenAI-compatible inference endpoints on Oberon. The current physical-fleet
manifest names it `fleet-inference-gateway` for compatibility, but it selects
the inference-mode controller deployment; there is no separate gateway binary.
Clients must not call Praxis or the spoke inference Routes directly.

## Release gates

- Images are referenced by immutable digest and the previous digests are recorded.
- `PG_URL` resolves to an external HA PostgreSQL service with certificate verification and tested point-in-time recovery.
- The immutable ledger uses authenticated HTTPS; memory and disabled modes are forbidden.
- Oberon has at least three leader-elected control-plane replicas serving the
  inference ingress and at least two ready Praxis replicas. This is the target
  production topology, not the default replica count installed by the generic
  v0.3.0 chart or Kustomize overlays.
- Oberon and Arena are both healthy for the CPU model. The Brutus-only GPU model is reported as degraded/non-HA until a second GPU provider exists.
- TLS Route probes succeed from the Praxis pod and no NodePort or manually maintained EndpointSlice is in the active data path.

## Cluster loss

Drain the site through the fleet API before planned work. For an unplanned loss, confirm the agent/Grid health becomes unhealthy within 30 seconds and that new CPU requests move to the other eligible site. Never retry a stream after response headers have been sent. Brutus loss must produce `503 no_compatible_capacity` for GPU requests; do not rewrite them to the CPU model.

## Certificates and credentials

Rotate Route certificates and Praxis credentials one site at a time. Install the new trust material first, verify an HTTPS health probe from Praxis, rotate the server credential, and remove the old trust material only after successful inference. A TLS or authentication failure is fail-closed.

## PostgreSQL or ledger outage

Do not switch to in-memory state. Restore the external HA database or ledger endpoint, verify certificate and credential validity, then confirm reconciliation and evidence recording before reopening admission. Inference requests already admitted may continue only when their routing state is current and policy permits it.

## Rollback

Apply the previous versioned manifests and image digests. Keep the Route-based spoke transport unless it caused the incident; the legacy NodePort objects are migration-only and must not become the default rollback path. Confirm `/readyz`, Praxis `/ready`, one CPU request to each CPU site, and the expected explicit Brutus GPU behavior.

## Certification soak

1. Run the bounded saturation probe independently for CPU and GPU and record the maximum clean rate.
2. Select 50% of the lower applicable clean rate.
3. Run a 60-minute mixed chat/completions workload through the fleet inference
   Route backed by the controller.
4. During the run, remove and restore Oberon CPU, Arena CPU, and Brutus GPU in sequence.
5. Accept only with at least 99.9% success where compatible capacity remains, failover under 30 seconds, no cross-tenant or malformed responses, no pod restarts, and no sustained memory, queue, or connection growth.
