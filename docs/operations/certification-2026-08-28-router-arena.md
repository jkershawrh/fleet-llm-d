# llm-d Router Arena certification — 2026-08-28

## Result

The isolated llm-d Router data plane passed its staging external-Route
certification. The traffic generator ran on Arena and reached the qualification
gateway through verified TLS on Oberon's OpenShift Route. Praxis remained the
production default throughout the run.

- Duration: 3,600.007 seconds.
- Requests: 482/482 HTTP 200; zero errors.
- Data plane: `llm-d-router` on every response.
- Exact model: `granite-2b-cpu` on every response.
- Distribution: Arena 245, Oberon 237.
- Latency p50/p95/p99: 698.766/1,308.844/1,560.727 ms.
- Source/transport: `arena` / `external-route`.
- Test pod and all measured serving components: zero restarts.
- Merged evidence result: passed with no failures.

## Resource evidence

All post-run snapshots were ready and below the 70% guardrail.

- Oberon backend: CPU 0.178%, memory 19.945%.
- Arena backend: CPU 0.189%, memory 21.588%.
- Brutus backend: CPU 0.170%, memory 9.128%.
- Router EPPs: CPU 17.254%, memory 4.960%.
- Router proxies: CPU 0.327%, memory 4.532%.
- Router qualification gateway: CPU 0.028%, memory 0.930%.

## Transport and image

- Gateway: `fleet-router-qualification-gateway-fleet-llm-d.apps.oberon.fm2aihpcsed.com`.
- Arena certification image: `sha256:c53eacf2ef3ca373fc1a2ad6fca96f811f0946bbc5253915394c3efe5836feb1`.
- Arena split DNS used an environment-specific host mapping to Oberon's
  host-network ingress address.
- The public `fleet-route-ca` bundle was mounted into the Job; SNI, hostname,
  and CA verification remained enabled. No insecure TLS mode was used.
- The bearer credential was copied directly between Kubernetes APIs and was
  never printed or committed.

## Earlier runs

- The Oberon in-cluster Job was preempted twice at the node's 500-pod ceiling.
  Its disruption policy correctly created replacements. The third attempt
  completed 470/470 requests over 3,600 seconds, split Arena 231/Oberon 239,
  with p50/p95/p99 of 895.050/1,557.188/1,992.242 ms.
- The first Arena external run completed 474 correct responses and one
  harness-generated transport error at the exact measurement boundary. The
  harness was corrected to stop admitting requests at the deadline while
  allowing the final admitted request to drain under its bounded HTTP timeout.
  A 60-second regression canary passed 11/11 before the accepted rerun.

## Qualification boundary

This is staging proof of cross-cluster TLS transport, exact-model routing,
replica coexistence, and one-hour durability. Oberon's two Router replicas
share one physical node, so this does not prove node-failure HA. Production HA
still requires replicas in independent failure domains.
