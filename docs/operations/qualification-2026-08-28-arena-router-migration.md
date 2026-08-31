# Arena-hosted Router qualification

Date: 2026-08-28

## Reason for migration

The first eight-hour Arena-to-Oberon Route run completed 1,923 requests with
1,912 HTTP 200 responses (99.428%). It recorded ten
`503 no_compatible_capacity` responses and one
`502 routing_evidence_mismatch`. Oberon was at its 500-pod limit when a
qualification gateway pod was preempted at 19:46:20Z; its replacement could
not schedule immediately. The run is retained as infrastructure-interrupted
evidence and does not satisfy the 99.9% acceptance gate.

## New staging topology

- Arena hosts the isolated qualification gateway, CPU/GPU Envoy proxies, and
  CPU/GPU shadow EPPs. Each component has two replicas.
- Arena and Oberon remain the two qualified `granite-2b-cpu` execution sites.
- Brutus remains the sole exact
  `ibm-granite/granite-3.1-8b-instruct` GPU execution site and is non-HA.
- Oberon's production controller, Praxis, and inference gateway are unchanged.
- The core qualification gateway disables PostgreSQL, ledger, and semantic
  classification. Its explicit static provider inventory is rejected in
  production mode and whenever PostgreSQL is configured.

## Correctness changes

- Gateway `/readyz` now remains unavailable until every configured provider
  has reached an authoritative healthy or unhealthy probe threshold.
- Gateway, proxy, and EPP probes tolerate short node-level stalls while still
  failing closed; startup readiness is based on authoritative provider health.
- The certification harness records up to 100 timestamped error events with
  status, request ID, routed cluster, actual model, and data plane.
- The harness strips surrounding bearer-token whitespace so Secret creation
  cannot accidentally place a newline in the HTTP Authorization header.
- Arena Route traffic uses both host-network ingress endpoints because the
  restricted traffic identity cannot rely on the cluster DNS path.

## Qualification evidence

- Arena gateway image:
  `sha256:1b0bfbaec557e71cef89e6d45a52d833affd49b3f86f00af438c43665acc8c23`
- Arena certification image:
  `sha256:e3b4ab588be69aa600b637acdc58ffab3917556ef686b51dafddc3c07d3aff8d`
- CPU preflight: HTTP 200, `llm-d-router`, exact `granite-2b-cpu`.
- GPU preflight: HTTP 200, `brutus-h100`, exact
  `ibm-granite/granite-3.1-8b-instruct`.
- Post-hardening 60-second Route preflight: 7/7 HTTP 200; Arena 3, Oberon 4;
  zero errors; p50/p95/p99 814/1,408/1,408 ms.
- Exact-GPU preflight: 1/1 HTTP 200; `brutus-h100`; exact
  `ibm-granite/granite-3.1-8b-instruct`; 126 ms latency.
- Fifteen-minute forced-replacement canary: 120/120 HTTP 200; Arena 63,
  Oberon 57; zero errors; p50/p95/p99 756/1,613/1,704 ms. One gateway replica
  was replaced after two minutes, returned ready, and all gateway, proxy, and
  EPP containers finished with zero restarts.
- After the canary, the duplicate Oberon qualification deployments were scaled
  to zero. Oberon production remained controller 3/3, Praxis 2/2, and inference
  gateway 2/2.

The first eight-hour Arena-local durability attempt started on 2026-08-29 at
18:21Z and is retained as failed configuration evidence. It completed 2,054
requests: four HTTP 200 responses followed by 2,050 structured
`429 quota_exceeded` responses. Preflight and canary traffic had consumed the
core-only gateway's default in-memory fallback budget before durability began.
The Router components remained healthy, but this result does not satisfy
acceptance.

The fallback enforcer now resets token accounting each minute and budget
accounting monthly. Arena uses an explicit certification-only bounded profile;
the override is rejected with production mode or PostgreSQL. Digest
`sha256:0e1d701453889a07f8eab52760459f0b281400d39dec462490a29991562d318c`
passed two consecutive quota canaries totaling 392/392 HTTP 200 responses,
including one 227-request run that exceeds the former per-replica fallback
budget. The clean retry-disabled eight-hour rerun started on 2026-08-30 at
03:06Z and must pass before this topology is accepted as staging certification.
