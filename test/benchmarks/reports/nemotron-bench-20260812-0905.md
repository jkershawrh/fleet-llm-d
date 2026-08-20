# Nemotron 3.5 Lightning 30B Benchmark
**Date:** Wed Aug 12 09:05:59 CDT 2026
**Hardware:** Oberon — 2x Intel Xeon 6767P (256 threads), 503GB RAM
**Model:** NVIDIA-Nemotron-3.5-Lightning-30B-A3B-Q4_K_M.gguf (25GB)
**Server:** llama.cpp b10362, 64 threads, 8192 ctx, 4 parallel slots
**Architecture:** MoE 30B total / 3B active params

## Single Request Latency

| Prompt | Wall (s) | Prompt (ms) | Gen (ms) | Prompt t/s | Gen t/s | Tokens | Preview |
|--------|----------|-------------|----------|------------|---------|--------|---------|
| What is Kubernetes in one sentence? | 5.269 | 375 | 4254 | 125.2 | 22.8 | 97 | <think> |
| Write a Python function to calculate fibonacci num | 5.710 | 218 | 4853 | 91.7 | 20.6 | 100 | <think> |
| Explain the CAP theorem in distributed systems | 5.649 | 205 | 4818 | 92.6 | 20.8 | 100 | <think> |
| What are the key differences between vLLM and llam | 6.492 | 588 | 5095 | 40.8 | 19.6 | 100 | <think> |

## Long Generation (200 tokens)

| Prompt | Wall (s) | Gen t/s | Tokens |
|--------|----------|---------|--------|
| Write a detailed explanation of how Kubernetes handles pod s | 11.719 | 18.6 | 200 |
| Explain the architecture of a modern LLM inference serving s | 10.980 | 20.0 | 200 |

## Concurrency Sweep (50 tokens each)

| Concurrent | Wall (s) | Total Tokens | Throughput (t/s) | Errors |
|------------|----------|--------------|------------------|--------|
| 1 | 3.435 | 50 | 14.6 | 0 |
| 2 | 3.293 | 100 | 30.4 | 0 |
| 4 | 9.564 | 0 | 0.0 | 0 |
| 8 | 0.535 | 0 | 0.0 | 0 |

---
