# Multi-cluster product conformance report

**Date:** 2026-08-31

**Scope:** portable llm-d-fleet behavior, exercised on a disposable three-cluster reference testbed

**Classification:** product proof, not certification of the reference infrastructure

## Result

The test run demonstrated that fleet eligibility and policy are applied before
the llm-d Router chooses among the remaining endpoints. Round-robin or random
distribution is not the product policy: it is only the final selection behavior
inside an already qualified, exact-model provider set.

The portable product behaviors below passed:

- Exact physical-model qualification before Router selection.
- Two-provider CPU distribution across separate cluster failure domains.
- Single-provider GPU routing to the exact Granite 8B model.
- Explicit `503 no_compatible_capacity` without CPU substitution when the sole
  compatible GPU provider was removed.
- Health-based provider removal and restoration within the 30-second target.
- Tenant allow/deny restrictions preceding locality and optimization policy.
- Unhealthy candidates excluded before affinity or capacity optimization.
- Locality preference with ordered failover.
- Semantic tier matching with confidence gating and a non-semantic fallback.
- Internal destination and routing headers controlled by the gateway, not the
  caller.
- Optional governed integrations absent from the portable core without opening
  an authorization bypass.

## Reference execution evidence

Logical provider names are used for the portable claim. The reference-testbed
mapping is included only to make the run auditable.

- Provider A and Provider B served the same CPU physical model. A 50-request
  bounded mixed run completed with 50 HTTP 200 responses: 45 CPU requests were
  split 19/26 between the two providers, and five GPU requests reached Provider
  C.
- Provider C served only `ibm-granite/granite-3.1-8b-instruct`. Scaling it to
  zero produced the required structured 503 after eight seconds. No request was
  rewritten to the CPU model. Three consecutive requests passed after recovery.
- Independent CPU-provider removals converged within 18 seconds and 25 seconds,
  respectively. Transient errors stopped before provider removal completed.
- The live security suite rejected all 24 attack cases after accounting for
  the portable core profile's intentional `415` legacy-ingress rejection and
  fail-closed `503` when optional GCL verification is not configured.

Reference mapping: Provider A = Arena, Provider B = Oberon, Provider C = Brutus.
These names, capacities, credentials, Routes, and operational constraints are
not part of the portable product contract.

## Policy conformance matrix

| Policy layer | Required behavior | Evidence | Result |
| --- | --- | --- | --- |
| Compatibility | Admit only providers serving the requested physical model | gateway/model-provider tests and live GPU removal | Pass |
| Tenant scope | Apply allowed and denied clusters before routing preference | routing policy unit tests | Pass |
| Health and drain | Exclude unavailable, stale, or draining providers | Router publisher tests and live CPU failure tests | Pass |
| Locality | Prefer a healthy local provider, then ordered failover | routing policy unit tests | Pass |
| Semantic tier | Escalate only when label and confidence policy match | routing policy and BDD tests | Pass |
| Session/KV affinity | Never allow an unhealthy candidate to win | routing policy unit tests | Pass, contract-level |
| Distribution | Select among the fleet-qualified CPU endpoints | bounded live mixed run | Pass |
| Header authority | Ignore or replace caller-controlled internal routing data | gateway and contract tests | Pass |
| No-capacity response | Return structured 503 with no model downgrade | gateway tests and live GPU removal | Pass |

## Deliberate non-claims

- The current Router qualification profile uses random selection within the
  fleet-qualified set. It does not claim production queue-, KV-, latency-,
  carbon-, or cost-aware Router scoring.
- KV and queue scoring remain disabled until genuine, fresh cluster-local
  aggregate signals are available. Synthetic values are not accepted as scale
  evidence.
- The single GPU provider proves exact-model behavior and failure semantics; it
  is not GPU high availability.
- This report does not certify the security, capacity, availability, database,
  ledger, certificates, or operations of Oberon, Arena, or Brutus for
  production use.
- The run is staging-level product evidence. Independent reproducibility,
  formal security review, and release artifact provenance remain release gates.

## Reproduction boundary

Portable reproduction requires three Kubernetes or OpenShift clusters, two
providers for one exact CPU model, one provider for a distinct exact GPU model,
the fleet gateway, and one supported routing adapter. Environment-specific
hostnames, credentials, storage classes, certificates, and node selectors must
be supplied by the test operator and are intentionally absent from this report.
