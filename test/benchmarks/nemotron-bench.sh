#!/bin/bash
# Nemotron 3.5 Lightning 30B benchmark on HubCluster (Xeon 6767P)
# Tests: single-request latency, TTFT, throughput, concurrency sweep

set -euo pipefail

PORT="${1:-8888}"
BASE="http://localhost:${PORT}/v1/chat/completions"
RESULTS_DIR="test/benchmarks/reports"
TIMESTAMP=$(date +%Y%m%d-%H%M)
OUTFILE="${RESULTS_DIR}/nemotron-bench-${TIMESTAMP}.md"

mkdir -p "$RESULTS_DIR"

SYSTEM_PROMPT="You are a helpful assistant. Answer directly and concisely. Do not use thinking tags or show your reasoning process."

make_request() {
    local prompt="$1"
    local max_tokens="${2:-50}"
    curl -s "$BASE" \
        -H "Content-Type: application/json" \
        -d "{
            \"model\": \"nemotron\",
            \"messages\": [
                {\"role\": \"system\", \"content\": \"${SYSTEM_PROMPT}\"},
                {\"role\": \"user\", \"content\": \"${prompt}\"}
            ],
            \"max_tokens\": ${max_tokens},
            \"temperature\": 0.1
        }"
}

timed_request() {
    local prompt="$1"
    local max_tokens="${2:-50}"
    local start=$(python3 -c "import time; print(time.time())")
    local resp=$(make_request "$prompt" "$max_tokens")
    local end=$(python3 -c "import time; print(time.time())")
    local elapsed=$(python3 -c "print(f'{${end} - ${start}:.3f}')")

    local prompt_tps=$(echo "$resp" | python3 -c "import sys,json; d=json.load(sys.stdin); print(f\"{d['timings']['prompt_per_second']:.1f}\")" 2>/dev/null || echo "err")
    local gen_tps=$(echo "$resp" | python3 -c "import sys,json; d=json.load(sys.stdin); print(f\"{d['timings']['predicted_per_second']:.1f}\")" 2>/dev/null || echo "err")
    local prompt_ms=$(echo "$resp" | python3 -c "import sys,json; d=json.load(sys.stdin); print(f\"{d['timings']['prompt_ms']:.0f}\")" 2>/dev/null || echo "err")
    local gen_ms=$(echo "$resp" | python3 -c "import sys,json; d=json.load(sys.stdin); print(f\"{d['timings']['predicted_ms']:.0f}\")" 2>/dev/null || echo "err")
    local tokens=$(echo "$resp" | python3 -c "import sys,json; d=json.load(sys.stdin); print(d['usage']['completion_tokens'])" 2>/dev/null || echo "err")
    local content=$(echo "$resp" | python3 -c "import sys,json; d=json.load(sys.stdin); print(d['choices'][0]['message']['content'][:80])" 2>/dev/null || echo "err")

    echo "${elapsed}|${prompt_ms}|${gen_ms}|${prompt_tps}|${gen_tps}|${tokens}|${content}"
}

concurrent_requests() {
    local concurrency=$1
    local prompt="$2"
    local max_tokens="${3:-50}"
    local pids=()
    local tmpdir=$(mktemp -d)

    local start=$(python3 -c "import time; print(time.time())")
    for i in $(seq 1 $concurrency); do
        (
            local resp=$(make_request "$prompt" "$max_tokens")
            echo "$resp" > "${tmpdir}/${i}.json"
        ) &
        pids+=($!)
    done

    local errors=0
    for pid in "${pids[@]}"; do
        wait $pid || ((errors++))
    done
    local end=$(python3 -c "import time; print(time.time())")
    local wall=$(python3 -c "print(f'{${end} - ${start}:.3f}')")

    local total_tokens=0
    local total_gen_ms=0
    local count=0
    for f in "${tmpdir}"/*.json; do
        local t=$(python3 -c "import sys,json; d=json.load(open('$f')); print(d['usage']['completion_tokens'])" 2>/dev/null || echo "0")
        local g=$(python3 -c "import sys,json; d=json.load(open('$f')); print(f\"{d['timings']['predicted_ms']:.0f}\")" 2>/dev/null || echo "0")
        total_tokens=$((total_tokens + t))
        total_gen_ms=$((total_gen_ms + ${g%.*}))
        ((count++))
    done

    local throughput=$(python3 -c "print(f'{${total_tokens} / ${wall}:.1f}')")
    rm -rf "$tmpdir"

    echo "${concurrency}|${wall}|${total_tokens}|${throughput}|${errors}"
}

echo "# Nemotron 3.5 Lightning 30B Benchmark" | tee "$OUTFILE"
echo "**Date:** $(date)" | tee -a "$OUTFILE"
echo "**Hardware:** HubCluster — 2x Intel Xeon 6767P (256 threads), 503GB RAM" | tee -a "$OUTFILE"
echo "**Model:** NVIDIA-Nemotron-3.5-Lightning-30B-A3B-Q4_K_M.gguf (25GB)" | tee -a "$OUTFILE"
echo "**Server:** llama.cpp b10362, 64 threads, 8192 ctx, 4 parallel slots" | tee -a "$OUTFILE"
echo "**Architecture:** MoE 30B total / 3B active params" | tee -a "$OUTFILE"
echo "" | tee -a "$OUTFILE"

# --- Single Request Latency ---
echo "## Single Request Latency" | tee -a "$OUTFILE"
echo "" | tee -a "$OUTFILE"
echo "| Prompt | Wall (s) | Prompt (ms) | Gen (ms) | Prompt t/s | Gen t/s | Tokens | Preview |" | tee -a "$OUTFILE"
echo "|--------|----------|-------------|----------|------------|---------|--------|---------|" | tee -a "$OUTFILE"

PROMPTS=(
    "What is Kubernetes in one sentence?"
    "Write a Python function to calculate fibonacci numbers"
    "Explain the CAP theorem in distributed systems"
    "What are the key differences between vLLM and llama.cpp?"
)

for prompt in "${PROMPTS[@]}"; do
    echo "  Testing: ${prompt:0:40}..."
    result=$(timed_request "$prompt" 100)
    IFS='|' read -r wall pms gms ptps gtps tok content <<< "$result"
    short_prompt="${prompt:0:50}"
    echo "| ${short_prompt} | ${wall} | ${pms} | ${gms} | ${ptps} | ${gtps} | ${tok} | ${content:0:40} |" | tee -a "$OUTFILE"
done

echo "" | tee -a "$OUTFILE"

# --- Long Generation ---
echo "## Long Generation (200 tokens)" | tee -a "$OUTFILE"
echo "" | tee -a "$OUTFILE"
echo "| Prompt | Wall (s) | Gen t/s | Tokens |" | tee -a "$OUTFILE"
echo "|--------|----------|---------|--------|" | tee -a "$OUTFILE"

LONG_PROMPTS=(
    "Write a detailed explanation of how Kubernetes handles pod scheduling"
    "Explain the architecture of a modern LLM inference serving system"
)

for prompt in "${LONG_PROMPTS[@]}"; do
    echo "  Testing long: ${prompt:0:40}..."
    result=$(timed_request "$prompt" 200)
    IFS='|' read -r wall pms gms ptps gtps tok content <<< "$result"
    short_prompt="${prompt:0:60}"
    echo "| ${short_prompt} | ${wall} | ${gtps} | ${tok} |" | tee -a "$OUTFILE"
done

echo "" | tee -a "$OUTFILE"

# --- Concurrency Sweep ---
echo "## Concurrency Sweep (50 tokens each)" | tee -a "$OUTFILE"
echo "" | tee -a "$OUTFILE"
echo "| Concurrent | Wall (s) | Total Tokens | Throughput (t/s) | Errors |" | tee -a "$OUTFILE"
echo "|------------|----------|--------------|------------------|--------|" | tee -a "$OUTFILE"

for c in 1 2 4 8; do
    echo "  Testing concurrency: ${c}..."
    result=$(concurrent_requests $c "Explain containerization in two sentences." 50)
    IFS='|' read -r conc wall tok tps errs <<< "$result"
    echo "| ${conc} | ${wall} | ${tok} | ${tps} | ${errs} |" | tee -a "$OUTFILE"
    sleep 2
done

echo "" | tee -a "$OUTFILE"
echo "---" | tee -a "$OUTFILE"
echo "Benchmark complete. Results saved to ${OUTFILE}"
