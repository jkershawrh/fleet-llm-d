# Kustomize deployment profiles

The base deploys the three fleet runtime components in the `fleet-llm-d`
namespace. The overlays retain the existing standalone, hub, and federated
topologies.

Every overlay runs exactly one controller. Leader election is not implemented,
so gateway scaling and disruption budgets do not constitute control-plane HA.

## Required cluster identity

`fleet-agent` refuses to start without a stable cluster identity. Create the
required ConfigMap before applying any profile; use an ID that is unique across
every cluster registered with the same control plane:

```sh
kubectl -n fleet-llm-d create configmap fleet-cluster-identity \
  --from-literal=cluster-id=us-central1-prod-01
kubectl apply -k deploy/kustomize/overlays/hub
```

The reference is deliberately non-optional. Packaging must not silently invent
a shared cluster ID for production clusters.

## Runtime ports

| Component | Port | Purpose |
| --- | ---: | --- |
| controller | 8080 | control-plane API and health probes |
| controller | 9091 | Prometheus metrics |
| gateway | 8080 | inference routing proxy |
| gateway | 8081 | liveness and fail-closed readiness probes |
| gateway | 9090 | Prometheus metrics |
| agent | 8090 | local proxy, liveness, and fail-closed readiness probes |

The manifests expose only listeners the current binaries bind. Agent ports 8080
and 9090 remain reserved CLI contracts and are not rendered as Services. Agent
readiness stays false until synchronization and upstream forwarding exist;
gateway readiness stays false until a real routing snapshot is installed. This
prevents scaffold processes from being counted as live provider evidence.

The base keeps the gateway `ClusterIP`; expose it only through an explicitly
managed ingress, OpenShift Route, Gateway API object, or LoadBalancer policy.
The controller starts with the external ARE ledger disabled and without a
PostgreSQL or event-publisher endpoint. Production overlays should opt into
those services with operator-managed credentials and TLS endpoints rather than
guessed service addresses. The standalone overlay's PostgreSQL and Redis
resources are development conveniences, not production dependency defaults.

Validate all profiles with:

```sh
kubectl kustomize deploy/kustomize/base >/dev/null
kubectl kustomize deploy/kustomize/overlays/standalone >/dev/null
kubectl kustomize deploy/kustomize/overlays/hub >/dev/null
kubectl kustomize deploy/kustomize/overlays/federated >/dev/null
```
