# Three-cluster staging proof — 2026-08-27

## Scope

This checkpoint validates the production Praxis path on the
`codex/production-scale-oss-baseline` branch. Traffic was deliberately bounded;
governance and optional observability integrations were not part of this run.

## Deployed state

- Oberon controller: 3 replicas, one leader Service endpoint.
- Oberon inference gateway: 2 stateless replicas.
- Oberon Praxis: 2 replicas.
- Controller and gateway image digest:
  `sha256:ecc7830e4cb5e0c6235ece7266a31109fe0a12613f3c00884eb6f91e2976f54c`.
- Oberon agent image digest:
  `sha256:e263af056f6bb32be57f5fe13bdfc2d4e9fb1a8f146fc04ae15d7e15064fd7e4`.
- Router qualification deployments remained scaled to zero. Praxis remained
  the authoritative production data plane.

## Contract results

- A request without an Authorization header returned structured HTTP 401.
- Six CPU requests with spoofed fleet, data-plane, and destination headers all
  returned HTTP 200. The gateway replaced the spoofed values and reported:
  - Oberon: 3 requests.
  - Arena: 3 requests.
  - Exact model: `granite-2b-cpu` for every request.
  - Data plane: `praxis` for every request.
  - A request ID for every request.
- One exact Granite 8B request returned successfully from `brutus-h100` with
  model `ibm-granite/granite-3.1-8b-instruct` and data plane `praxis`.
- A streamed CPU request returned `text/event-stream`, 10 data frames, and one
  terminal `[DONE]` frame.
- A streamed GPU request was cancelled by the client after one second. Curl
  exited with its expected timeout code after receiving response bytes; both
  gateway replicas remained ready with zero restarts.

## Bounded CPU result

The accepted rerun used concurrency 1, a two-second inter-request interval,
eight completion tokens, and a 60-second request window.

- Requests: 21/21 successful.
- Distribution: Arena 11, Oberon 10.
- Throughput: 0.35 requests/second and 2.45 completion tokens/second.
- Total latency: p50 816.51 ms; p95/p99 1377.395 ms.
- Oberon peak sample: 12.625% CPU, 21.069% memory, zero restarts.
- Arena peak sample: 9% CPU, 22.29% memory, zero restarts.
- Guardrail breaches: none.
- Provisional safe concurrency: 1. Provisional 50% certification rate:
  0.175 requests/second. This is a staging floor, not a capacity ceiling.

The harness now accepts an explicit Kubernetes context per resource target so
multi-cluster telemetry does not depend on each kubeconfig's current context.

## Infrastructure findings and remaining gates

- Arena's failed builds were caused by `default-deny-all` selecting ephemeral
  OpenShift build pods without a corresponding egress allowance. The scoped
  `allow-openshift-build-egress` policy restored API, DNS, HTTPS, and internal
  registry access. Build 5 completed and Arena rolled out agent digest
  `sha256:0329e3b7d8ecd91498bb3856368e499ac14ff05bbe788541b93de64d6e65d928`.
  Four post-rollout CPU requests succeeded and reached both Arena and Oberon.
- Brutus reproduced the same missing build-pod egress policy. Applying the
  scoped policy allowed build 6 to complete, and Brutus rolled out agent digest
  `sha256:671370936daf593aef5d2350b30525b8b76c594eab1beb5bc23b6cf923c0caff`.
  Two post-rollout exact-model requests returned HTTP 200 from `brutus-h100`.
- Two obsolete overlapping Oberon PDBs (`fleet-core-services-pdb` and
  `praxis-ai-pdb`) were removed. The current workload-specific PDBs remain.
- This checkpoint does not certify Router beta, GPU resource telemetry,
  external HA PostgreSQL, or the governed ledger profile. Those remain release
  gates rather than failures of this bounded proof.

## Controlled CPU provider loss

- Arena loss: all 12 requests succeeded on Oberon. The first measured request
  began 12 seconds after scale-down. Arena restored to 1/1, both gateway
  replicas logged health recovery, and subsequent traffic reached both sites.
- Oberon backend loss: request 1 succeeded on Arena at 12 seconds; request 2
  received HTTP 502 from stale Oberon selection at 16 seconds. Requests 3-12
  all succeeded on Arena from 20 seconds onward. Oberon restored to 1/1 and
  traffic distribution recovered.
- Observed convergence was below 20 seconds and met the 30-second objective,
  but the pre-convergence 502 proves bounded pre-header retry is still required
  to satisfy the stronger surviving-traffic acceptance criterion.

## Pre-header retry remediation

- The gateway now performs at most one retry to a different fleet-qualified,
  healthy CPU provider when the first attempt fails before client headers are
  written. Client cancellation prevents retry, and exact GPU requests are
  never retried or substituted.
- Unit tests cover alternate-provider success and exact-GPU non-retry behavior.
- The production canary passed CPU and exact-GPU requests before promotion.
- During the repeated Oberon backend loss, all 10 requests returned HTTP 200
  from Arena. Four stale Oberon selections returned with routing reason
  `health-failover`; no client-visible 502 remained.
- Metric `fleet_inference_retries_total{reason="provider_failure"}` was present
  and incremented on a production gateway replica.
- Controller and gateway converged on image digest
  `sha256:880e35236fcba885d0fbbc34b4aa813372327a1705aa670b838dc33b40290760`.
  Controller remained 3/3 with exactly one ready leader Service endpoint;
  gateway remained 2/2 and Praxis 2/2.

## Exact GPU provider loss

- Brutus GPU serving was scaled from one replica to zero under an automatic
  restoration guard.
- All seven measured requests, beginning 17 seconds after the event, returned
  structured HTTP 503 with error code `no_compatible_capacity`.
- No request reported a routed cluster or substituted CPU model.
- Brutus restored to 1/1 after the expected 8B model cold start. After health
  recovery, an exact-model request returned HTTP 200 from `brutus-h100` with
  `ibm-granite/granite-3.1-8b-instruct` in both headers and response metadata.
