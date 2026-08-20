# llm-d-semantic-classifier — Benchmark Report
**Date:** 2026-08-19 17:05:48.067930
**Hardware:** Oberon — 2x Intel Xeon 6767P (256 threads), 503GB RAM
**Classifier:** complexity (4 labels: SIMPLE, MEDIUM, COMPLEX, REASONING)
**Endpoint:** localhost:50051 (gRPC)
**Model:** sentence-transformers/all-MiniLM-L6-v2 (anchor-topk-mean)

## 1. Classification Accuracy

| # | Prompt | Expected | Actual | Score | Margin | Latency (ms) | Correct |
|---|--------|----------|--------|-------|--------|-------------|---------|
| 1 | What is Kubernetes? | SIMPLE | SIMPLE | 0.999 | 1.074 | 521.1 | yes |
| 2 | List all pods in a namespace | SIMPLE | MEDIUM | 0.844 | 0.394 | 816.4 | **NO** |
| 3 | What port does etcd use? | SIMPLE | SIMPLE | 0.999 | 1.075 | 696.1 | yes |
| 4 | What is the default service type in Kubernetes? | SIMPLE | SIMPLE | 0.999 | 1.081 | 1198.2 | yes |
| 5 | What is a ConfigMap? | SIMPLE | SIMPLE | 0.999 | 1.071 | 688.5 | yes |
| 6 | What does kubectl get nodes show? | SIMPLE | SIMPLE | 0.996 | 1.024 | 675.2 | yes |
| 7 | How many CPUs does an H100 have? | SIMPLE | SIMPLE | 0.999 | 1.064 | 665.3 | yes |
| 8 | What is the capital of France? | SIMPLE | SIMPLE | 1.000 | 1.073 | 1006.5 | yes |
| 9 | Explain the difference between vLLM and OVMS for CPU inference | MEDIUM | MEDIUM | 0.930 | 0.945 | 641.8 | yes |
| 10 | How does KV cache affinity routing work in fleet-llm-d? | MEDIUM | SIMPLE | 0.878 | 0.587 | 872.4 | **NO** |
| 11 | Compare StatefulSet vs Deployment for model serving | MEDIUM | MEDIUM | 0.773 | 0.611 | 759.6 | yes |
| 12 | What are the tradeoffs between round-robin and weighted routing? | MEDIUM | SIMPLE | 0.982 | 0.998 | 1153.6 | **NO** |
| 13 | How do you configure a NetworkPolicy to allow only specific pod traffi... | MEDIUM | MEDIUM | 0.996 | 1.055 | 780.4 | yes |
| 14 | Explain how OpenShift Routes differ from Kubernetes Ingress | MEDIUM | MEDIUM | 0.984 | 0.893 | 705.4 | yes |
| 15 | What is the difference between horizontal and vertical pod autoscaling... | MEDIUM | SIMPLE | 0.999 | 1.062 | 690.9 | **NO** |
| 16 | Design a multi-tenant inference routing policy with cost optimization ... | COMPLEX | COMPLEX | 0.998 | 1.212 | 676.9 | yes |
| 17 | Architect a fleet-wide model placement strategy that balances GPU util... | COMPLEX | COMPLEX | 1.000 | 1.228 | 1009.6 | yes |
| 18 | Design a cache eviction strategy for KV cache transfers during live mi... | COMPLEX | COMPLEX | 0.998 | 1.233 | 720.6 | yes |
| 19 | Create a comprehensive monitoring and alerting strategy for a multi-cl... | COMPLEX | COMPLEX | 0.999 | 1.232 | 701.3 | yes |
| 20 | Design a rollback mechanism for model deployments across a fleet that ... | COMPLEX | COMPLEX | 0.997 | 1.244 | 698.5 | yes |
| 21 | Diagnose why fleet p99 latency spikes to 5s during rolling updates wit... | REASONING | MEDIUM | 0.982 | 1.095 | 701.4 | **NO** |
| 22 | Prove that round-robin routing is suboptimal for mixed-complexity work... | REASONING | REASONING | 1.000 | 1.237 | 976.5 | yes |
| 23 | Given 3 clusters with different GPU types and a budget of $50k/month, ... | REASONING | REASONING | 0.999 | 1.234 | 753.4 | yes |
| 24 | Analyze the failure modes when a fleet controller loses connectivity t... | REASONING | REASONING | 0.682 | 0.237 | 736.9 | yes |
| 25 | Prove by induction that the sum of the first n odd numbers is n square... | REASONING | REASONING | 0.999 | 1.232 | 524.2 | yes |
| 26 | A fleet has clusters A (H100, 80 RPS), B (A100, 40 RPS), C (CPU, 5 RPS... | REASONING | REASONING | 0.999 | 1.239 | 914.8 | yes |

**Accuracy: 21/26 (80.8%)**

## 2. Latency Profile

### Cache Miss (unique prompts)
| Metric | Value |
|--------|-------|
| Count | 50 |
| p50 | 710.32 ms |
| p95 | 1166.26 ms |
| p99 | 1249.63 ms |
| min | 632.57 ms |
| max | 1249.63 ms |
| mean | 787.32 ms |

### Cache Hit (repeated prompt)
| Metric | Value |
|--------|-------|
| Count | 100 |
| p50 | 509.25 ms |
| p95 | 615.57 ms |
| p99 | 658.92 ms |
| min | 418.95 ms |
| max | 658.92 ms |
| mean | 513.25 ms |

## 3. Throughput

| Concurrency | Requests | Wall (s) | RPS | p50 (ms) | p99 (ms) | Errors |
|-------------|----------|----------|-----|----------|----------|--------|
| 1 | 50 | 28.17 | 1.8 | 527.4 | 1216.0 | 0 |
| 2 | 50 | 9.73 | 5.1 | 362.1 | 520.0 | 0 |
| 4 | 48 | 3.46 | 13.9 | 269.8 | 376.6 | 0 |
| 8 | 80 | 2.44 | 32.9 | 224.3 | 366.4 | 0 |
| 16 | 160 | 2.62 | 61.1 | 219.7 | 424.6 | 0 |
| 32 | 320 | 2.80 | 114.2 | 239.7 | 333.9 | 0 |

## 4. Edge Cases

| Test | Input | Status | Label | Latency (ms) | Notes |
|------|-------|--------|-------|-------------|-------|
| Empty input | 0 chars | OK | SIMPLE | 562.4 | Should handle gracefully |
| Whitespace only | 7 chars | OK | SIMPLE | 382.8 | Should handle gracefully |
| Single word | 10 chars | OK | SIMPLE | 558.2 | Minimal context |
| Very long input | 14000 chars | OK | MEDIUM | 1221.2 | 8K+ tokens, tests truncation |
| Non-English (French) | 59 chars | OK | REASONING | 703.0 | Cross-language |
| Non-English (Japanese) | 34 chars | OK | MEDIUM | 669.5 | Non-Latin script |
| Code only (Python) | 84 chars | OK | MEDIUM | 669.8 | Pure code |
| Code only (YAML) | 98 chars | OK | MEDIUM | 718.9 | K8s manifest |
| Mixed code + NL | 85 chars | OK | MEDIUM | 759.1 | Mixed content |
| Adversarial | 62 chars | OK | MEDIUM | 757.9 | Prompt injection |
| Numbers only | 26 chars | OK | SIMPLE | 756.4 | Non-NL numeric input |
| Special chars | 29 chars | OK | SIMPLE | 738.3 | Non-text input |

## 5. Classifier Metadata

| Field | Value |
|-------|-------|
| classifier_id | complexity |
| model_revision | c5f55ef419d268ba843c544dc00988d1e9878044 |
| tokenizer_revision | c5f55ef419d268ba843c544dc00988d1e9878044 |
| taxonomy_revision | scr-default-anchors-v1 |
| status | OK |

## Summary

- **Accuracy:** 21/26 (80.8%)
- **Cache miss p50:** 710.32 ms
- **Cache hit p50:** 509.25 ms
- **Cache speedup:** 1x
