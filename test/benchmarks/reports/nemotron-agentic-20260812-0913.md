# Nemotron 3.5 Lightning 30B — Agentic Benchmark
**Date:** 2026-08-12 09:13:02.363765
**Hardware:** Oberon — 2x Intel Xeon 6767P (256 threads), 503GB RAM
**Model:** NVIDIA-Nemotron-3.5-Lightning-30B-A3B-Q4_K_M.gguf (25GB, MoE 30B/3B active)
**Server:** llama.cpp b10362, 64 threads, 8192 ctx, 4 parallel slots

## 1. Tool Use

| Scenario | Wall (s) | Tool Called | Args Valid | Think % | Gen t/s |
|----------|----------|------------|------------|---------|---------|
| Check cluster status | 6.46 | get_cluster_status | yes | 100.0% | 19.8 |
| Place model (4 constraints) | 7.3 | place_model | yes | 100.0% | 19.9 |
| Ambiguous: drain vs scale | 7.37 | get_cluster_status | n/a | 100.0% | 20.4 |

## 2. Multi-Step Reasoning

| Scenario | Wall (s) | Think Words | Answer Words | Think % | Correct | Gen t/s |
|----------|----------|-------------|--------------|---------|---------|---------|
| Diagnose fleet latency | 30.62 | 0 | 324 | 0.0% | yes | 20.5 |
| Capacity planning | 30.12 | 0 | 380 | 0.0% | yes | 20.8 |
| Incident triage | 32.15 | 0 | 350 | 0.0% | yes | 19.4 |

## 3. Structured Output

| Task | Wall (s) | Valid Output | Fields Correct | Think % | Gen t/s |
|------|----------|-------------|----------------|---------|---------|
| FleetCluster CRD | 24.47 | yes | yes | 0.0% | 21.5 |
| Log → JSON extraction | 14.36 | no | no | 0.0% | 23.1 |
| DecisionPackage JSON | 18.77 | no | no | 0.0% | 22.8 |

## 4. Code Generation

| Task | Wall (s) | Has Function | Syntactically Valid | Think % | Gen t/s |
|------|----------|-------------|---------------------|---------|---------|
| Go: placement filter | 22.66 | yes | yes | 0.0% | 23.5 |
| Python: cost calculator | 24.32 | yes | no | 0.0% | 26.0 |
| Bash: health checker | 22.24 | yes | yes | 0.0% | 24.0 |

## 5. Reasoning Efficiency Summary

The model uses `<think>` tags for chain-of-thought reasoning before answering.
This is by design for agentic workloads — the thinking improves accuracy on
complex tasks (tool selection, multi-step diagnosis, capacity math).
