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

- Arena inference is healthy, but new image builds are blocked by Arena's
  cluster-wide pod service-network failure. Build pods on both `rhgnr1` and
  `gnr2` could not reach `172.30.0.1:443` or cluster DNS. The existing Arena
  agent remains ready on its prior digest.
- Brutus inference is healthy and exact-model routing is proven. Its API
  endpoint recovered during the checkpoint, but its build pod then reproduced
  the same inability to reach `172.30.0.1:443`. Build 5 was cancelled and the
  existing Brutus agent remains ready on its prior digest.
- Two obsolete overlapping Oberon PDBs (`fleet-core-services-pdb` and
  `praxis-ai-pdb`) were removed. The current workload-specific PDBs remain.
- This checkpoint does not certify failover convergence, Router beta, GPU
  resource telemetry, external HA PostgreSQL, or the governed ledger profile.
  Those remain release gates rather than failures of this bounded proof.
