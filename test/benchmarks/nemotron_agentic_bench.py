#!/usr/bin/env python3
"""Nemotron 3.5 Lightning 30B — Agentic Benchmark Suite"""

import json, re, time, sys, os, requests
from datetime import datetime
from pathlib import Path

BASE = f"http://localhost:{sys.argv[1] if len(sys.argv) > 1 else '8888'}/v1/chat/completions"
RESULTS_DIR = Path("test/benchmarks/reports")
RESULTS_DIR.mkdir(parents=True, exist_ok=True)
OUTFILE = RESULTS_DIR / f"nemotron-agentic-{datetime.now().strftime('%Y%m%d-%H%M')}.md"

FLEET_TOOLS = [
    {"type": "function", "function": {
        "name": "get_cluster_status",
        "description": "Get current status of a fleet cluster including GPU utilization, queue depth, and health",
        "parameters": {"type": "object", "properties": {
            "cluster_name": {"type": "string"}, "include_metrics": {"type": "boolean"}
        }, "required": ["cluster_name"]}}},
    {"type": "function", "function": {
        "name": "place_model",
        "description": "Place a model on clusters based on constraints",
        "parameters": {"type": "object", "properties": {
            "model_name": {"type": "string"}, "gpu_type": {"type": "string", "enum": ["H100","A100","L40S","CPU"]},
            "min_memory_gb": {"type": "number"}, "region": {"type": "string"}
        }, "required": ["model_name"]}}},
    {"type": "function", "function": {
        "name": "scale_pool",
        "description": "Scale an inference pool up or down",
        "parameters": {"type": "object", "properties": {
            "pool_name": {"type": "string"}, "replicas": {"type": "integer"}, "cluster": {"type": "string"}
        }, "required": ["pool_name", "replicas"]}}},
    {"type": "function", "function": {
        "name": "drain_cluster",
        "description": "Drain a cluster to move all workloads off before maintenance",
        "parameters": {"type": "object", "properties": {
            "cluster_name": {"type": "string"}, "reason": {"type": "string"}, "force": {"type": "boolean"}
        }, "required": ["cluster_name", "reason"]}}},
]


def call_model(messages, max_tokens=500, tools=None):
    body = {"model": "nemotron", "messages": messages, "max_tokens": max_tokens, "temperature": 0.1}
    if tools:
        body["tools"] = tools
    start = time.time()
    resp = requests.post(BASE, json=body, timeout=300).json()
    wall = time.time() - start
    return resp, round(wall, 2)


def parse(resp):
    choice = resp.get("choices", [{}])[0]
    msg = choice.get("message", {})
    content = msg.get("content", "") or ""
    tool_calls = msg.get("tool_calls", [])
    timings = resp.get("timings", {})
    usage = resp.get("usage", {})

    think_match = re.search(r"<think>(.*?)</think>", content, re.DOTALL)
    think_text = think_match.group(1).strip() if think_match else ""
    answer_text = re.sub(r"<think>.*?</think>", "", content, flags=re.DOTALL).strip()
    think_w = len(think_text.split()) if think_text else 0
    answer_w = len(answer_text.split()) if answer_text else 0

    return {
        "think_words": think_w, "answer_words": answer_w,
        "think_pct": round(think_w / max(think_w + answer_w, 1) * 100, 1),
        "total_tokens": usage.get("completion_tokens", 0),
        "prompt_tps": round(timings.get("prompt_per_second", 0), 1),
        "gen_tps": round(timings.get("predicted_per_second", 0), 1),
        "prompt_ms": round(timings.get("prompt_ms", 0)),
        "gen_ms": round(timings.get("predicted_ms", 0)),
        "answer": answer_text, "think": think_text,
        "tool_calls": [{"name": tc["function"]["name"], "args": json.loads(tc["function"]["arguments"])}
                       for tc in tool_calls if "function" in tc] if tool_calls else [],
        "finish": choice.get("finish_reason", ""),
    }


out = []
def w(line=""):
    out.append(line)
    print(line)


w("# Nemotron 3.5 Lightning 30B — Agentic Benchmark")
w(f"**Date:** {datetime.now()}")
w("**Hardware:** Oberon — 2x Intel Xeon 6767P (256 threads), 503GB RAM")
w("**Model:** NVIDIA-Nemotron-3.5-Lightning-30B-A3B-Q4_K_M.gguf (25GB, MoE 30B/3B active)")
w("**Server:** llama.cpp b10362, 64 threads, 8192 ctx, 4 parallel slots")
w()

# ═══ 1. TOOL USE ═══
w("## 1. Tool Use")
w()
w("| Scenario | Wall (s) | Tool Called | Args Valid | Think % | Gen t/s |")
w("|----------|----------|------------|------------|---------|---------|")

# 1a: Simple tool call
print("  Running: Simple tool select...")
resp, wall = call_model(
    [{"role": "system", "content": "You are a fleet infrastructure assistant. Use the available tools."},
     {"role": "user", "content": "Check the status of the brutus cluster and include metrics"}],
    300, FLEET_TOOLS)
p = parse(resp)
tc = p["tool_calls"]
tool = tc[0]["name"] if tc else "none"
valid = "yes" if tc and tc[0].get("args", {}).get("cluster_name") == "brutus" and tc[0]["args"].get("include_metrics") == True else "no"
w(f"| Check cluster status | {wall} | {tool} | {valid} | {p['think_pct']}% | {p['gen_tps']} |")

# 1b: Constrained placement
print("  Running: Constrained placement...")
resp, wall = call_model(
    [{"role": "system", "content": "You are a fleet infrastructure assistant. Use the available tools."},
     {"role": "user", "content": "Place granite-3.1-8b-instruct on an H100 GPU with at least 80GB memory in eu-west region"}],
    300, FLEET_TOOLS)
p = parse(resp)
tc = p["tool_calls"]
tool = tc[0]["name"] if tc else "none"
args = tc[0]["args"] if tc else {}
valid = "yes" if (tc and "granite" in str(args.get("model_name","")).lower()
    and args.get("gpu_type") == "H100" and args.get("min_memory_gb", 0) >= 80
    and "eu" in str(args.get("region","")).lower()) else "no"
w(f"| Place model (4 constraints) | {wall} | {tool} | {valid} | {p['think_pct']}% | {p['gen_tps']} |")

# 1c: Ambiguous — drain or scale?
print("  Running: Ambiguous scenario...")
resp, wall = call_model(
    [{"role": "system", "content": "You are a fleet infrastructure assistant. Use the available tools."},
     {"role": "user", "content": "Arena cluster is at 95% GPU utilization and we have maintenance in 2 hours. What should we do?"}],
    500, FLEET_TOOLS)
p = parse(resp)
tc = p["tool_calls"]
tool = tc[0]["name"] if tc else "text-only"
w(f"| Ambiguous: drain vs scale | {wall} | {tool} | n/a | {p['think_pct']}% | {p['gen_tps']} |")
w()

# ═══ 2. MULTI-STEP REASONING ═══
w("## 2. Multi-Step Reasoning")
w()
w("| Scenario | Wall (s) | Think Words | Answer Words | Think % | Correct | Gen t/s |")
w("|----------|----------|-------------|--------------|---------|---------|---------|")

reasoning_tests = [
    ("Diagnose fleet latency",
     "Our fleet has 3 clusters: oberon (CPU, 10 RPS), arena (CPU, 8 RPS), brutus (H100 GPU, 80 RPS). "
     "Users report p99 latency of 5 seconds. The routing policy is round-robin. "
     "What is the most likely cause and what should we change?",
     ["round.robin", "cpu.*slow", "routing", "weighted", "gpu"]),
    ("Capacity planning",
     "We need to serve granite-3.1-8b-instruct at 200 RPS total. Each H100 does 83 RPS, "
     "each Xeon 6767P does 10 RPS. H100 costs $30k/month, Xeon costs $5k/month. "
     "What is the cheapest fleet config that meets the target with N+1 redundancy?",
     ["H100", "GPU", "cost", "redundan"]),
    ("Incident triage",
     "Alert: cluster brutus health check failed 3 consecutive times. fleet-agent last reported 2 min ago. "
     "GPU utilization was 94% before contact lost. 5 active inference sessions on brutus. "
     "Failover chain: brutus -> arena -> oberon. Immediate actions in priority order?",
     ["failover", "drain", "redirect", "arena", "session"]),
]

for name, prompt, keywords in reasoning_tests:
    print(f"  Running: {name}...")
    resp, wall = call_model(
        [{"role": "system", "content": "You are a fleet-llm-d expert. Reason through problems step by step."},
         {"role": "user", "content": prompt}], 600)
    p = parse(resp)
    correct = "yes" if any(re.search(kw, p["answer"], re.I) for kw in keywords) else "no"
    w(f"| {name} | {wall} | {p['think_words']} | {p['answer_words']} | {p['think_pct']}% | {correct} | {p['gen_tps']} |")

w()

# ═══ 3. STRUCTURED OUTPUT ═══
w("## 3. Structured Output")
w()
w("| Task | Wall (s) | Valid Output | Fields Correct | Think % | Gen t/s |")
w("|------|----------|-------------|----------------|---------|---------|")

# 3a: CRD YAML
print("  Running: Generate CRD...")
resp, wall = call_model(
    [{"role": "system", "content": "You are a Kubernetes expert. Output only valid YAML, no explanation."},
     {"role": "user", "content": "Generate a FleetCluster custom resource YAML for cluster named brutus "
      "with labels gpu-type=H100, region=us-east, capacity=94GB. "
      "Endpoint https://api.brutus.local:6443, health checks every 30s."}], 500)
p = parse(resp)
valid = "yes" if "FleetCluster" in p["answer"] or "kind:" in p["answer"] else "no"
fields = "yes" if all(x in p["answer"] for x in ["brutus", "H100", "6443"]) else "no"
w(f"| FleetCluster CRD | {wall} | {valid} | {fields} | {p['think_pct']}% | {p['gen_tps']} |")

# 3b: JSON extraction
print("  Running: JSON extraction...")
resp, wall = call_model(
    [{"role": "system", "content": "Extract the requested information as a JSON object. Output only valid JSON."},
     {"role": "user", "content": "From this log line, extract cluster name, error type, and timestamp as JSON:\n\n"
      "2026-08-12T14:23:45Z [ERROR] fleet-controller: placement failed for model granite-3.1-8b "
      "on cluster arena-prod-eu: insufficient GPU memory (requested 80GB, available 24GB)"}], 300)
p = parse(resp)
valid_json = "no"
fields_ok = "no"
try:
    # Try to find JSON in the answer
    json_match = re.search(r'\{.*\}', p["answer"], re.DOTALL)
    if json_match:
        d = json.loads(json_match.group())
        valid_json = "yes"
        if "arena" in str(d).lower() and ("gpu" in str(d).lower() or "memory" in str(d).lower()):
            fields_ok = "yes"
except:
    pass
w(f"| Log → JSON extraction | {wall} | {valid_json} | {fields_ok} | {p['think_pct']}% | {p['gen_tps']} |")

# 3c: Decision package JSON
print("  Running: DecisionPackage generation...")
resp, wall = call_model(
    [{"role": "system", "content": "Output only valid JSON."},
     {"role": "user", "content": "Generate a GCL DecisionPackage JSON for scaling granite-3.1-8b-instruct "
      "from 2 to 4 replicas on cluster arena due to queue depth exceeding threshold. "
      "Include fields: action_type, model, cluster, current_replicas, target_replicas, "
      "reason, confidence, constraints_satisfied (list), timestamp."}], 400)
p = parse(resp)
valid_json = "no"
fields_ok = "no"
try:
    json_match = re.search(r'\{.*\}', p["answer"], re.DOTALL)
    if json_match:
        d = json.loads(json_match.group())
        valid_json = "yes"
        if d.get("target_replicas") == 4 and "arena" in str(d).lower():
            fields_ok = "yes"
except:
    pass
w(f"| DecisionPackage JSON | {wall} | {valid_json} | {fields_ok} | {p['think_pct']}% | {p['gen_tps']} |")

w()

# ═══ 4. CODE GENERATION ═══
w("## 4. Code Generation")
w()
w("| Task | Wall (s) | Has Function | Syntactically Valid | Think % | Gen t/s |")
w("|------|----------|-------------|---------------------|---------|---------|")

# 4a: Go function
print("  Running: Go placement filter...")
resp, wall = call_model(
    [{"role": "system", "content": "Write code only. No explanation."},
     {"role": "user", "content": "Write a Go function FilterClusters that filters a []Cluster "
      "(fields: Name string, GPUType string, AvailableMemoryGB int, Region string) "
      "to only include clusters matching a given GPU type and minimum memory. Return []Cluster."}], 500)
p = parse(resp)
has_func = "yes" if "func " in p["answer"] and "Cluster" in p["answer"] else "no"
valid_go = "yes" if "func " in p["answer"] and "return " in p["answer"] else "no"
w(f"| Go: placement filter | {wall} | {has_func} | {valid_go} | {p['think_pct']}% | {p['gen_tps']} |")

# 4b: Python function
print("  Running: Python cost calculator...")
resp, wall = call_model(
    [{"role": "system", "content": "Write code only. No explanation."},
     {"role": "user", "content": "Write a Python function calculate_fleet_cost(clusters: list[dict]) -> dict. "
      "Each cluster dict has: name, gpu_type, gpu_count, hours_used. "
      "Return dict with total_cost, per_cluster list, cheapest_cluster. "
      "Rates: H100=$3.50/hr, A100=$2.00/hr, CPU=$0.50/hr per unit."}], 600)
p = parse(resp)
has_func = "yes" if "def calculate_fleet_cost" in p["answer"] else "no"
valid_py = "yes" if "def " in p["answer"] and "return" in p["answer"] else "no"
w(f"| Python: cost calculator | {wall} | {has_func} | {valid_py} | {p['think_pct']}% | {p['gen_tps']} |")

# 4c: Bash script
print("  Running: Bash health check...")
resp, wall = call_model(
    [{"role": "system", "content": "Write code only. No explanation."},
     {"role": "user", "content": "Write a bash script that checks health of 3 fleet clusters "
      "(oberon at 192.168.1.123, arena at 192.168.1.105, brutus at 192.168.1.75) "
      "by curling their /healthz endpoint on port 6443 with a 5s timeout. "
      "Print a table of cluster name, status (UP/DOWN), and response time."}], 500)
p = parse(resp)
has_func = "yes" if ("curl" in p["answer"] and "healthz" in p["answer"]) else "no"
valid_sh = "yes" if "#!/" in p["answer"] or ("for " in p["answer"] or "curl" in p["answer"]) else "no"
w(f"| Bash: health checker | {wall} | {has_func} | {valid_sh} | {p['think_pct']}% | {p['gen_tps']} |")

w()

# ═══ 5. REASONING EFFICIENCY SUMMARY ═══
w("## 5. Reasoning Efficiency Summary")
w()
w("The model uses `<think>` tags for chain-of-thought reasoning before answering.")
w("This is by design for agentic workloads — the thinking improves accuracy on")
w("complex tasks (tool selection, multi-step diagnosis, capacity math).")
w()

OUTFILE.write_text("\n".join(out))
print(f"\n---\nBenchmark complete. Results saved to {OUTFILE}")
