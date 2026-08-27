# Benchmark evidence and provenance

Files in this directory are immutable measurement records. Their numeric
results must not be rewritten when the architecture changes. Interpret each
report using the environment, date, model, server, and transport recorded in
that file.

## Evidence rules

- A component microbenchmark proves only that component and execution mode.
- A single-cluster result does not prove fleet routing or failover.
- A simulated or mock integration does not prove the external product.
- A Praxis or legacy gateway result does not qualify the llm-d Router adapter.
- A NodePort result remains historical after migration to TLS Routes.
- Optional classifier, governance, ledger, and observation benchmarks are not
  OSS-core performance requirements.
- A result without a source commit or image digest is historical evidence and
  must not be used as current-release certification.

New reports should record:

1. Source commit and immutable image digest.
2. Physical, virtual, simulated, or mocked environment.
3. Core-only or governed-observability profile.
4. Routing provider and cross-cluster transport, or `not applicable`.
5. Exact logical and physical model identifiers.
6. Client-observed latency boundaries, including network, TLS, queueing,
   generation, and response transfer where applicable.
7. Workload shape, concurrency, duration, warm-up, and failure injections.
8. Whether the result is current, historical, superseded, or qualification
   evidence.

The authoritative current three-cluster qualification records live under
`docs/operations/`. They identify the adapter and state explicitly which
acceptance gates remain open.

