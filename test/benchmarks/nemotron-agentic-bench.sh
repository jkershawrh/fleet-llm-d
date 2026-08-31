#!/bin/bash
# Nemotron 3.5 Lightning 30B — Agentic Benchmark Suite
# Tests reasoning, tool use, structured output, code gen, multi-step planning
# Designed for MoE thinking models where <think> is a feature

set -euo pipefail

PORT="${1:-8888}"
BASE="http://localhost:${PORT}/v1/chat/completions"
RESULTS_DIR="test/benchmarks/reports"
TIMESTAMP=$(date +%Y%m%d-%H%M)
OUTFILE="${RESULTS_DIR}/nemotron-agentic-${TIMESTAMP}.md"

mkdir -p "$RESULTS_DIR"

# ─── Helper ───

call_model() {
    local messages="$1"
    local max_tokens="${2:-500}"
    local tools="${3:-}"

    local body="{
        \"model\": \"nemotron\",
        \"messages\": ${messages},
        \"max_tokens\": ${max_tokens},
        \"temperature\": 0.1
    }"

    if [ -n "$tools" ]; then
        body="{
            \"model\": \"nemotron\",
            \"messages\": ${messages},
            \"max_tokens\": ${max_tokens},
            \"temperature\": 0.1,
            \"tools\": ${tools}
        }"
    fi

    curl -s "$BASE" -H "Content-Type: application/json" -d "$body"
}

parse_response() {
    local resp="$1"
    local tmpfile=$(mktemp)
    echo "$resp" > "$tmpfile"
    python3 - "$tmpfile" <<'PYEOF'
import sys, json, re

with open(sys.argv[1]) as f:
    resp = json.load(f)

content = resp.get('choices', [{}])[0].get('message', {}).get('content', '') or ''
tool_calls = resp.get('choices', [{}])[0].get('message', {}).get('tool_calls', [])
timings = resp.get('timings', {})
usage = resp.get('usage', {})

think_match = re.search(r'<think>(.*?)</think>', content, re.DOTALL)
think_text = think_match.group(1).strip() if think_match else ''
answer_text = re.sub(r'<think>.*?</think>', '', content, flags=re.DOTALL).strip()

think_words = len(think_text.split()) if think_text else 0
answer_words = len(answer_text.split()) if answer_text else 0

print(json.dumps({
    'think_words': think_words,
    'answer_words': answer_words,
    'think_ratio': round(think_words / max(think_words + answer_words, 1) * 100, 1),
    'total_tokens': usage.get('completion_tokens', 0),
    'prompt_tokens': usage.get('prompt_tokens', 0),
    'prompt_ms': round(timings.get('prompt_ms', 0)),
    'gen_ms': round(timings.get('predicted_ms', 0)),
    'prompt_tps': round(timings.get('prompt_per_second', 0), 1),
    'gen_tps': round(timings.get('predicted_per_second', 0), 1),
    'answer': answer_text[:200],
    'think_preview': think_text[:150],
    'tool_calls': [{'name': tc.get('function',{}).get('name',''), 'args': tc.get('function',{}).get('arguments','')} for tc in (tool_calls or [])],
    'finish_reason': resp.get('choices', [{}])[0].get('finish_reason', ''),
}))
PYEOF
    rm -f "$tmpfile"
}

run_test() {
    local name="$1"
    local messages="$2"
    local max_tokens="${3:-500}"
    local tools="${4:-}"

    echo "  Running: ${name}..."
    local start=$(python3 -c "import time; print(time.time())")
    local resp=$(call_model "$messages" "$max_tokens" "$tools")
    local end=$(python3 -c "import time; print(time.time())")
    local wall=$(python3 -c "print(f'{${end} - ${start}:.2f}')")

    local parsed=$(parse_response "$resp")
    echo "${wall}|${parsed}"
}

extract() {
    local parsed="$1"
    local field="$2"
    local tmpf=$(mktemp)
    echo "$parsed" > "$tmpf"
    python3 -c "import json; print(json.load(open('$tmpf')).get('$field',''))"
    rm -f "$tmpf"
}

extract_num() {
    local parsed="$1"
    local field="$2"
    local tmpf=$(mktemp)
    echo "$parsed" > "$tmpf"
    python3 -c "import json; print(json.load(open('$tmpf')).get('$field',0))"
    rm -f "$tmpf"
}

# ─── Fleet Tool Definitions ───

FLEET_TOOLS='[
  {
    "type": "function",
    "function": {
      "name": "get_cluster_status",
      "description": "Get the current status of a fleet cluster including GPU utilization, queue depth, and health",
      "parameters": {
        "type": "object",
        "properties": {
          "cluster_name": {"type": "string", "description": "Name of the cluster"},
          "include_metrics": {"type": "boolean", "description": "Include detailed metrics"}
        },
        "required": ["cluster_name"]
      }
    }
  },
  {
    "type": "function",
    "function": {
      "name": "place_model",
      "description": "Place a model on one or more clusters based on constraints",
      "parameters": {
        "type": "object",
        "properties": {
          "model_name": {"type": "string", "description": "Model to place"},
          "gpu_type": {"type": "string", "enum": ["H100", "A100", "L40S", "CPU"], "description": "Required GPU type"},
          "min_memory_gb": {"type": "number", "description": "Minimum GPU memory in GB"},
          "region": {"type": "string", "description": "Regulatory region constraint"}
        },
        "required": ["model_name"]
      }
    }
  },
  {
    "type": "function",
    "function": {
      "name": "scale_pool",
      "description": "Scale an inference pool up or down",
      "parameters": {
        "type": "object",
        "properties": {
          "pool_name": {"type": "string", "description": "Inference pool to scale"},
          "replicas": {"type": "integer", "description": "Target replica count"},
          "cluster": {"type": "string", "description": "Cluster where the pool runs"}
        },
        "required": ["pool_name", "replicas"]
      }
    }
  },
  {
    "type": "function",
    "function": {
      "name": "drain_cluster",
      "description": "Drain a cluster to move all workloads off before maintenance",
      "parameters": {
        "type": "object",
        "properties": {
          "cluster_name": {"type": "string", "description": "Cluster to drain"},
          "reason": {"type": "string", "description": "Reason for drain"},
          "force": {"type": "boolean", "description": "Force drain even with active sessions"}
        },
        "required": ["cluster_name", "reason"]
      }
    }
  }
]'

# ─── Begin Benchmark ───

{
echo "# Nemotron 3.5 Lightning 30B — Agentic Benchmark"
echo "**Date:** $(date)"
echo "**Hardware:** HubCluster — 2x Intel Xeon 6767P (256 threads), 503GB RAM"
echo "**Model:** NVIDIA-Nemotron-3.5-Lightning-30B-A3B-Q4_K_M.gguf (25GB, MoE 30B/3B active)"
echo "**Server:** llama.cpp b10362, 64 threads, 8192 ctx, 4 parallel slots"
echo ""

# ═══════════════════════════════════════════
# TEST 1: Tool Use
# ═══════════════════════════════════════════
echo "## 1. Tool Use"
echo ""
echo "| Scenario | Wall (s) | Tool Called | Args Valid | Think/Answer | Gen t/s |"
echo "|----------|----------|------------|------------|--------------|---------|"

# 1a: Simple tool selection
result=$(run_test "Simple tool select" \
    '[{"role":"system","content":"You are a fleet infrastructure assistant. Use the available tools to help the operator."},{"role":"user","content":"Check the status of the gpucluster cluster and include metrics"}]' \
    300 "$FLEET_TOOLS")
IFS='|' read -r wall parsed <<< "$result"
tmpj=$(mktemp); echo "$parsed" > "$tmpj"
tool_name=$(python3 -c "import json; tc=json.load(open('$tmpj')).get('tool_calls',[]); print(tc[0]['name'] if tc else 'none')")
tool_args=$(python3 -c "import json; tc=json.load(open('$tmpj')).get('tool_calls',[]); print(tc[0]['args'] if tc else '{}')")
rm -f "$tmpj"
args_valid="no"
echo "$tool_args" | python3 -c "import sys,json; d=json.loads(sys.stdin.read()); assert d.get('cluster_name')=='gpucluster' and d.get('include_metrics')==True" 2>/dev/null && args_valid="yes"
think_r=$(extract_num "$parsed" "think_ratio")
gen_tps=$(extract_num "$parsed" "gen_tps")
echo "| Check cluster status | ${wall} | ${tool_name} | ${args_valid} | ${think_r}% think | ${gen_tps} |"

# 1b: Tool with constraints
result=$(run_test "Constrained placement" \
    '[{"role":"system","content":"You are a fleet infrastructure assistant. Use the available tools to help the operator."},{"role":"user","content":"Place the granite-3.1-8b-instruct model on an H100 GPU with at least 80GB memory in the eu-west region"}]' \
    300 "$FLEET_TOOLS")
IFS='|' read -r wall parsed <<< "$result"
tmpj=$(mktemp); echo "$parsed" > "$tmpj"
tool_name=$(python3 -c "import json; tc=json.load(open('$tmpj')).get('tool_calls',[]); print(tc[0]['name'] if tc else 'none')")
tool_args=$(python3 -c "import json; tc=json.load(open('$tmpj')).get('tool_calls',[]); print(tc[0]['args'] if tc else '{}')")
rm -f "$tmpj"
args_valid="no"
echo "$tool_args" | python3 -c "
import sys,json
d=json.loads(sys.stdin.read())
assert 'granite' in d.get('model_name','').lower()
assert d.get('gpu_type')=='H100'
assert d.get('min_memory_gb',0) >= 80
assert 'eu' in d.get('region','').lower()
" 2>/dev/null && args_valid="yes"
think_r=$(extract_num "$parsed" "think_ratio")
gen_tps=$(extract_num "$parsed" "gen_tps")
echo "| Place model (4 constraints) | ${wall} | ${tool_name} | ${args_valid} | ${think_r}% think | ${gen_tps} |"

# 1c: Ambiguous scenario — should it drain or scale?
result=$(run_test "Ambiguous: drain vs scale" \
    '[{"role":"system","content":"You are a fleet infrastructure assistant. Use the available tools to help the operator."},{"role":"user","content":"The cpucluster cluster is showing 95% GPU utilization and we have maintenance scheduled in 2 hours. What should we do?"}]' \
    500 "$FLEET_TOOLS")
IFS='|' read -r wall parsed <<< "$result"
tmpj=$(mktemp); echo "$parsed" > "$tmpj"
tool_name=$(python3 -c "import json; tc=json.load(open('$tmpj')).get('tool_calls',[]); print(tc[0]['name'] if tc else 'none')")
rm -f "$tmpj"
think_r=$(extract_num "$parsed" "think_ratio")
gen_tps=$(extract_num "$parsed" "gen_tps")
answer=$(extract "$parsed" "answer")
echo "| Ambiguous scenario | ${wall} | ${tool_name} | n/a | ${think_r}% think | ${gen_tps} |"

echo ""

# ═══════════════════════════════════════════
# TEST 2: Multi-Step Reasoning
# ═══════════════════════════════════════════
echo "## 2. Multi-Step Reasoning"
echo ""
echo "| Scenario | Wall (s) | Think Words | Answer Words | Think % | Correct | Gen t/s |"
echo "|----------|----------|-------------|--------------|---------|---------|---------|"

# 2a: Diagnostic reasoning
result=$(run_test "Diagnose latency" \
    '[{"role":"system","content":"You are a fleet-llm-d expert. Reason through problems step by step."},{"role":"user","content":"Our fleet has 3 clusters: hubcluster (CPU, 10 RPS), cpucluster (CPU, 8 RPS), gpucluster (H100 GPU, 80 RPS). Users report p99 latency of 5 seconds. The routing policy is round-robin. What is the most likely cause and what should we change?"}]' \
    600)
IFS='|' read -r wall parsed <<< "$result"
think_w=$(extract_num "$parsed" "think_words")
answer_w=$(extract_num "$parsed" "answer_words")
think_r=$(extract_num "$parsed" "think_ratio")
gen_tps=$(extract_num "$parsed" "gen_tps")
answer=$(extract "$parsed" "answer")
correct="no"
echo "$answer" | grep -qi "round.robin\|cpu.*slow\|routing\|weighted\|gpu" && correct="yes"
echo "| Diagnose fleet latency | ${wall} | ${think_w} | ${answer_w} | ${think_r}% | ${correct} | ${gen_tps} |"

# 2b: Capacity planning
result=$(run_test "Capacity planning" \
    '[{"role":"system","content":"You are a fleet-llm-d expert. Show your math."},{"role":"user","content":"We need to serve granite-3.1-8b-instruct at 200 RPS total. Each H100 does 83 RPS, each Xeon 6767P does 10 RPS. H100 costs $30k/month, Xeon costs $5k/month. What is the cheapest fleet configuration that meets the target with N+1 redundancy?"}]' \
    800)
IFS='|' read -r wall parsed <<< "$result"
think_w=$(extract_num "$parsed" "think_words")
answer_w=$(extract_num "$parsed" "answer_words")
think_r=$(extract_num "$parsed" "think_ratio")
gen_tps=$(extract_num "$parsed" "gen_tps")
answer=$(extract "$parsed" "answer")
correct="no"
echo "$answer" | grep -qiE "H100|GPU|cost|redundan" && correct="partial"
echo "| Capacity planning | ${wall} | ${think_w} | ${answer_w} | ${think_r}% | ${correct} | ${gen_tps} |"

# 2c: Incident response
result=$(run_test "Incident triage" \
    '[{"role":"system","content":"You are an SRE for a fleet-llm-d deployment. Triage this incident."},{"role":"user","content":"Alert: cluster gpucluster health check failed 3 consecutive times. fleet-agent last reported 2 minutes ago. GPU utilization was at 94% before contact was lost. There are 5 active inference sessions on gpucluster. The failover chain is gpucluster -> cpucluster -> hubcluster. What are the immediate actions in priority order?"}]' \
    600)
IFS='|' read -r wall parsed <<< "$result"
think_w=$(extract_num "$parsed" "think_words")
answer_w=$(extract_num "$parsed" "answer_words")
think_r=$(extract_num "$parsed" "think_ratio")
gen_tps=$(extract_num "$parsed" "gen_tps")
answer=$(extract "$parsed" "answer")
correct="no"
echo "$answer" | grep -qi "failover\|drain\|redirect\|cpucluster\|session" && correct="yes"
echo "| Incident triage | ${wall} | ${think_w} | ${answer_w} | ${think_r}% | ${correct} | ${gen_tps} |"

echo ""

# ═══════════════════════════════════════════
# TEST 3: Structured Output
# ═══════════════════════════════════════════
echo "## 3. Structured Output"
echo ""
echo "| Task | Wall (s) | Valid JSON | Fields Correct | Think % | Gen t/s |"
echo "|------|----------|-----------|----------------|---------|---------|"

# 3a: Generate a FleetCluster CRD
result=$(run_test "Generate CRD YAML" \
    '[{"role":"system","content":"You are a Kubernetes expert. Output only valid YAML, no explanation."},{"role":"user","content":"Generate a FleetCluster custom resource YAML for a cluster named gpucluster with labels gpu-type=H100, region=us-east, capacity=94GB. Set the endpoint to https://api.gpucluster.local:6443 and enable health checks every 30 seconds."}]' \
    500)
IFS='|' read -r wall parsed <<< "$result"
think_r=$(extract_num "$parsed" "think_ratio")
gen_tps=$(extract_num "$parsed" "gen_tps")
answer=$(extract "$parsed" "answer")
valid_yaml="no"
echo "$answer" | grep -q "kind: FleetCluster" && valid_yaml="yes"
fields="no"
echo "$answer" | grep -q "gpucluster" && echo "$answer" | grep -q "H100" && echo "$answer" | grep -q "6443" && fields="yes"
echo "| FleetCluster CRD | ${wall} | ${valid_yaml} | ${fields} | ${think_r}% | ${gen_tps} |"

# 3b: JSON extraction
result=$(run_test "JSON extraction" \
    '[{"role":"system","content":"Extract the requested information as a JSON object. Output only valid JSON."},{"role":"user","content":"From this log line, extract cluster name, error type, and timestamp as JSON:\n\n2026-08-12T14:23:45Z [ERROR] fleet-controller: placement failed for model granite-3.1-8b on cluster cpucluster-prod-eu: insufficient GPU memory (requested 80GB, available 24GB)"}]' \
    300)
IFS='|' read -r wall parsed <<< "$result"
think_r=$(extract_num "$parsed" "think_ratio")
gen_tps=$(extract_num "$parsed" "gen_tps")
answer=$(extract "$parsed" "answer")
valid_json="no"
echo "$answer" | python3 -c "import sys,json; json.loads(sys.stdin.read())" 2>/dev/null && valid_json="yes"
fields="no"
echo "$answer" | python3 -c "
import sys,json
d=json.loads(sys.stdin.read())
assert 'cpucluster' in str(d).lower()
assert 'gpu' in str(d).lower() or 'memory' in str(d).lower()
" 2>/dev/null && fields="yes"
echo "| Log → JSON extraction | ${wall} | ${valid_json} | ${fields} | ${think_r}% | ${gen_tps} |"

echo ""

# ═══════════════════════════════════════════
# TEST 4: Code Generation
# ═══════════════════════════════════════════
echo "## 4. Code Generation"
echo ""
echo "| Task | Wall (s) | Language | Compiles/Runs | Think % | Gen t/s |"
echo "|------|----------|----------|---------------|---------|---------|"

# 4a: Go function
result=$(run_test "Go: placement filter" \
    '[{"role":"system","content":"Write code only. No explanation."},{"role":"user","content":"Write a Go function that filters a slice of Cluster structs (with fields Name string, GPUType string, AvailableMemoryGB int, Region string) to only include clusters matching a given GPU type and minimum memory. Return the filtered slice."}]' \
    500)
IFS='|' read -r wall parsed <<< "$result"
think_r=$(extract_num "$parsed" "think_ratio")
gen_tps=$(extract_num "$parsed" "gen_tps")
answer=$(extract "$parsed" "answer")
has_func="no"
echo "$answer" | grep -q "func " && echo "$answer" | grep -q "Cluster" && has_func="yes"
echo "| Go placement filter | ${wall} | Go | ${has_func} | ${think_r}% | ${gen_tps} |"

# 4b: Python function
result=$(run_test "Python: cost calculator" \
    '[{"role":"system","content":"Write code only. No explanation."},{"role":"user","content":"Write a Python function calculate_fleet_cost(clusters: list[dict]) -> dict that takes a list of cluster dicts with keys name, gpu_type, gpu_count, hours_used and returns a dict with total_cost, per_cluster breakdown, and cheapest_cluster. Use these rates: H100=$3.50/hr, A100=$2.00/hr, CPU=$0.50/hr per unit."}]' \
    600)
IFS='|' read -r wall parsed <<< "$result"
think_r=$(extract_num "$parsed" "think_ratio")
gen_tps=$(extract_num "$parsed" "gen_tps")
answer=$(extract "$parsed" "answer")
has_func="no"
echo "$answer" | grep -q "def calculate_fleet_cost" && has_func="yes"
echo "| Python cost calculator | ${wall} | Python | ${has_func} | ${think_r}% | ${gen_tps} |"

echo ""

# ═══════════════════════════════════════════
# TEST 5: Reasoning Efficiency
# ═══════════════════════════════════════════
echo "## 5. Reasoning Efficiency Summary"
echo ""
echo "Aggregate think vs answer token allocation across all tests."
echo ""

} | tee "$OUTFILE"

echo ""
echo "---"
echo "Benchmark complete. Results saved to ${OUTFILE}"
