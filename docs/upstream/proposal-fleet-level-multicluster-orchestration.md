# Fleet-level multi-cluster inference orchestration

## Summary

Provide a composable fleet-level orchestration boundary for operators running
llm-d inference across multiple Kubernetes clusters. The fleet layer discovers
provider health and capabilities, selects a compatible cluster and physical
model, and delegates endpoint selection to the llm-d routing path in that
cluster. It preserves exact-model semantics during failures and exposes a
single OpenAI-compatible entry point without allowing clients to address
internal clusters directly.

## Motivation

Multiple llm-d installations are currently independent failure and scheduling
domains. Generic global load balancing can select a reachable endpoint, but it
does not understand model compatibility, accelerator requirements, draining
state, session escalation, or whether a requested model is highly available.
Operators otherwise have to encode these decisions in application clients or
in vendor-specific infrastructure.

An implementation experience report across three physical OpenShift clusters
demonstrated CPU distribution across two sites, exact H100 model routing to a
third site, explicit non-HA capability reporting, session-aware CPU-to-GPU
escalation, routing-header isolation, and fail-closed behavior when compatible
capacity disappeared. The session-escalation implementation uses llm-d-sc as
an optional semantic classification signal while retaining policy, health,
capacity, and exact-model enforcement in the fleet layer.

Router v0.10 now provides an Alpha family of cluster-scoped EPP plugins for
file-based peer discovery, aggregate queue and KV-cache metrics, approximate
prefix affinity, and session affinity. This proposal treats those plugins as
the preferred initial cluster-scoring integration rather than duplicating
their routing intelligence. The remaining proposal scope is the dynamic fleet
control plane and the production semantics around the eligible cluster set.

### Goals

- Define the responsibility boundary between fleet-wide cluster selection and
  llm-d's cluster-local routing and inference optimization.
- Represent provider capability, health, draining state, failure domain, and
  model availability across clusters.
- Select a compatible cluster and physical model without silent downgrade.
- Provide health-aware failover with measurable convergence.
- Preserve OpenAI-compatible streaming, cancellation, status codes, and model
  metadata through the fleet entry point.
- Define conformance tests that can be run against different cross-cluster
  transports and Kubernetes distributions.
- Allow optional workload-classification signals, including llm-d-sc output,
  to inform placement without making a classifier authoritative.
- Keep the design composable with Gateway API and the Gateway API Inference
  Extension.

### Non-Goals

- Replacing llm-d Router, EPP scoring, flow control, autoscaling, disaggregated
  serving, or KV-cache optimization within a cluster.
- Mandating OpenShift Routes, a service mesh, Multi-Cluster Services, or any
  other specific cross-cluster transport.
- Provisioning or upgrading Kubernetes clusters.
- Operating model servers or durable infrastructure dependencies.
- Standardizing tenant billing, cognitive-governance, or immutable-ledger
  systems in the first iteration.
- Requiring llm-d-sc or defining semantic classifier internals and taxonomy.
- Advertising a model as highly available when fewer than two compatible
  providers exist in separate failure domains.

## Proposal

Establish a fleet selection layer above cluster-local llm-d deployments. Each
participating cluster publishes a bounded set of capabilities and health
signals. For every admitted inference request, the fleet layer resolves the
logical request to an exact physical model, filters incompatible or unhealthy
providers, and applies placement policy. A cluster-scoped EPP, initially using
Router v0.10's multi-cluster plugins, scores the resulting eligible cluster
endpoints and selects one. The request then enters that cluster's supported
llm-d path, where normal cluster-local endpoint selection continues.

Provider health transitions should be bounded and observable. Retries may occur
only before response headers are returned; streamed generations must never be
replayed after output begins. If no compatible provider remains, the fleet
entry point returns a structured unavailable response and does not rewrite the
request to a weaker or different model.

Implementations may consume a workload-classification signal when resolving
placement. The reference implementation integrates llm-d-sc for semantic
classification and session escalation. Classification is advisory input: it
cannot override authentication, tenant policy, model compatibility, health, or
capacity constraints, and the fleet contract remains classifier-neutral.

Success means that independently implemented fleet layers can pass common
conformance tests for capability discovery, exact-model selection, failure
convergence, streaming behavior, cancellation, and internal-routing-header
isolation while composing with an unmodified cluster-local llm-d deployment.

### Adapter and serving model

The reference implementation exposes a single routing-provider interface.
Praxis remains its validated adapter and llm-d Router is the upstream-native
adapter; exactly one is authoritative in a deployment. This keeps fleet policy
independent of a particular data plane without asking llm-d to productize
Praxis. Cluster-local placement may target the existing `InferencePool` path or
KServe `LLMInferenceService`; KServe owns workload and Router lifecycle when
selected.

KEDA over EPP metrics is the default local scaling primitive. WVA is optional
for heterogeneous variants and feeds desired replicas to KEDA/HPA. Fleet policy
sets cross-cluster budgets and placement bounds rather than replacing either.

The optional signal plane uses SWIM for membership and capability announcements
and mTLS HTTPS polling for allowlisted pool-level Prometheus gauges. A central
management hub is not required in the inference request path.

### User Stories

#### Multi-region CPU availability

As a platform operator, I can advertise a CPU-served model from two independent
clusters, drain or lose either cluster, and continue compatible requests on the
remaining provider within a declared convergence bound.

#### Exact accelerator placement

As an application owner, I can request a model that requires a particular GPU
capability and know that the request either reaches a compatible provider or
fails explicitly. It is never silently sent to a different CPU model.

#### Semantic session escalation

As an application owner, I can allow a session classified as more complex to
escalate from a CPU-served model to a compatible GPU-served model, while fleet
policy and exact-model guarantees continue to apply.

#### Cluster-private routing

As a security operator, I can expose one fleet endpoint while keeping cluster
addresses private. Caller-provided routing metadata cannot force selection of
an internal provider.

## Design Details

### Responsibility boundary

The fleet layer owns authenticated cluster registration, capability discovery,
fleet health and draining, placement policy, logical-to-physical model
resolution, the eligible cluster set, and fleet-wide availability status. The
cluster-scoped EPP owns queue/KV/prefix/session scoring and final selection
within that eligible set. The selected cluster's local llm-d stack owns
pod-level endpoint selection, flow control, cache-aware scoring, disaggregated
serving, and local scaling.

The first design phase should map this boundary onto existing Gateway API and
Gateway API Inference Extension resources before introducing new APIs.

### Router v0.10 adapter

The initial adapter can render eligible providers into the multi-cluster
plugin's watched endpoint file, including separate inference and metrics hosts.
This allows the fleet reconciler to remove incompatible, stale, unhealthy, or
draining providers before EPP scoring. The EPP can then consume the existing
pool-level queue and KV metrics and preserve its native prefix and session
affinity behavior.

The adapter must not interpret a successful metrics scrape as complete model
or provider health. Fleet health includes capability freshness and active
inference probes with hysteresis. A failed or stale provider is removed from
the eligible endpoint input within the declared convergence bound. The watched
file is an initial implementation boundary, not a proposed permanent public
API; SIG Router may prefer a native discovery source later.

### Provider state

A provider record minimally includes a stable cluster identity, failure domain,
supported physical models and hardware capabilities, supported protocols,
health freshness, draining state, and an authenticated destination reference.
User-visible aggregate capability status distinguishes:

- `healthy`: sufficient compatible providers satisfy the advertised
  availability policy;
- `degraded/non-HA`: compatible capacity exists but does not satisfy that
  policy;
- `draining`: excluded from new placement while existing work completes; and
- `unavailable`: no eligible compatible provider exists.

### Request behavior

The public entry point accepts OpenAI-compatible chat and completion requests.
It authenticates and admits the request before resolving placement. Internal
cluster-selection headers are stripped or rejected and replaced only after the
fleet decision is made. Implementations propagate a request identifier and
return selected-cluster, routing-reason, and actual-model metadata using names
agreed during API review.

Backend status and streaming semantics are preserved. Timeouts and cancellation
propagate to the selected backend. A retry is permitted only before response
headers are committed and only when policy says the alternate provider is
compatible. No-compatible-capacity responses are structured `503` errors.

### Health and failover

Health combines cluster-reported state with active probes, with explicit
freshness bounds and hysteresis. A provider is restored only after consecutive
successful observations. The default conformance target is removal of an
unhealthy provider within 30 seconds, while allowing implementations to expose
stricter values.

### Conformance

The initial suite should cover CPU distribution across two failure domains,
exact GPU routing, sole-provider loss, drain and restore, spoofed internal
routing metadata, classifier-driven session escalation, classifier failure or
absence, streaming and cancellation, backend error propagation, and agreement
between the routing decision and backend execution identity.

## Alternatives

### Application-side routing

Applications can maintain a list of cluster endpoints, but this exposes
topology, duplicates policy in every client, and cannot provide consistent
health, exact-model, or failover semantics.

### DNS or generic global load balancing

These mechanisms are useful transports but generally route by reachability and
coarse health. They do not own model compatibility, hardware placement,
draining, or session policy and therefore cannot define the complete behavior.

### One stretched Kubernetes cluster

A single cluster may work in some environments, but it does not cover sovereign
boundaries, independent administrative domains, disconnected edge sites, or
deployments that require separate failure domains.

### Embedding all fleet logic in the cluster-local EPP

This could blur cluster and fleet failure domains and couple local hot-path
optimization to remote discovery and policy. The proposal instead keeps the
boundary explicit. Router v0.10's cluster-scoped EPP remains the preferred
scoring layer, while fleet reconciliation and admission stay outside its hot
path.
