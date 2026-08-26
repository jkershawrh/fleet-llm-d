# Core-only Praxis three-cluster certification

Date: 2026-08-26  
Branch: `feat/require-ed25519-decision-packages`  
Final source commit: `385d7fd`  
Profile: OSS core with Praxis; GCL, DeepField, immutable-ledger recording,
semantic classification, ModelPlane, and optional event integrations were not
used by the controller or inference gateway.

## Deployment under test

- Oberon controller and gateway:
  `fleet-controller@sha256:95f86d4dd606518ee3b9df73bf766706131a78207ff1c4421d2c3110f182aa5f`
- Oberon agent:
  `fleet-agent@sha256:80b9102c9203047f04dbb900cdecceadac767cc4beaa933432dede19a9ebb951`
- Arena and Brutus agents:
  `fleet-agent@sha256:335133dc13a43866689217c36927a68e66dc641efb5adc4392f8a593bde1c6c9`
- Praxis:
  `praxis-ai@sha256:fc33e12166005d5ad57216a35f5162dab6711cb947f89b47392bd299a657c33c`
- Oberon and Arena CPU backend:
  `openvino/model_server@sha256:069a1c1191995e2b1338879137efc9551829e47f1d2e97049004453a6076d898`
- Brutus GPU backend:
  `vllm/vllm-openai@sha256:0a51ea5b4ae2dc5d81890e5173f54203d2a3ae0cfffe51b8fd2afd4391bfd967`

All controller, gateway, Praxis, agent, and inference deployments had one
ready replica during this low-impact qualification.

## Pre-soak conformance

- OpenAI-compatible CPU and exact-model GPU chat inference returned `200`.
- CPU traffic reached both Oberon and Arena.
- The exact Granite 8B request reached Brutus and was never downgraded.
- Streaming returned `text/event-stream`.
- A caller-provided `X-Fleet-Target-Cluster` value was ignored.
- Removing Arena moved CPU traffic to Oberon.
- Removing Brutus produced structured `503 no_compatible_capacity` for the
  exact GPU model; service was restored before the soak.
- Platform metrics omitted governance, classification, and ledger sections
  when those integrations were not configured.

## One-hour soak result

The accepted run started at `2026-08-26T21:12:13Z`. It used one sequential
request every two seconds, 90% CPU and 10% exact-model GPU, a maximum of ten
generated tokens, and a dedicated bounded soak tenant.

- Requests: 1,072
- HTTP 200: 1,072
- Errors and gateway 5xx: 0
- Oberon CPU routes: 482
- Arena CPU routes: 483
- Brutus GPU routes: 107
- Overall average latency: 1.2556 seconds
- Overall maximum latency: 2.4460 seconds
- Oberon CPU average / maximum: 1.325 / 2.446 seconds
- Arena CPU average / maximum: 1.357 / 2.191 seconds
- Brutus GPU average / maximum: 0.485 / 0.662 seconds
- Controller, gateway, Praxis, agent, and vLLM restarts: 0
- Final controller / gateway / Praxis memory: 12 MiB / 12 MiB / 58 MiB
- Final public gateway, Arena CPU, and Brutus GPU health checks: `200`

Latency is client-observed wall time. It includes OpenShift Route and TLS,
gateway authentication and quota admission, fleet selection, Praxis proxying,
backend queue/prefill/generation, and response transfer. It is not isolated
router overhead.

## Environmental interruption excluded from certification

An earlier run completed 321 requests without application errors before the
workstation lost all reachability to the `192.168.1.x` lab network, including
the Oberon Kubernetes API. That run was stopped and excluded. The accepted
one-hour run began only after all three cluster APIs, workloads, and inference
Routes were healthy again.

## Defects found and corrected during qualification

- `3b7282f`: declared Praxis multi-cluster `matchExpressions` in the
  `InferenceProvider` CRD.
- `12c5cd8`: allowed OpenShift DNS service and translated endpoint traffic for
  the Brutus vLLM pod.
- `385d7fd`: removed implicit GCL, DeepField, and ledger endpoints so platform
  metrics integrations are genuinely opt-in.

## Conclusion

The core-only Praxis profile passed the one-hour three-cluster qualification.
This result does not certify multi-replica HA or the beta llm-d Router adapter;
those are separate release gates.
