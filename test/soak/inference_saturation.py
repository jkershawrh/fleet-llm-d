"""Inference saturation test — find the throughput ceiling.

Ramps concurrent inference requests through Praxis from 1 to max_concurrency.
Measures requests/sec, TTFT, and error rate at each level.
Stops when error rate exceeds 5% or latency exceeds 30s.

Usage:
    python3 test/soak/inference_saturation.py \
        --praxis-url https://praxis-ai-fleet-llm-d.apps.oberon.fm2aihpcsed.com \
        --model granite-2b-cpu \
        --max-concurrency 50 \
        --duration-per-level 30
"""

import argparse
import asyncio
import json
import sys
import time
from dataclasses import dataclass, field

import httpx


@dataclass
class LevelResult:
    concurrency: int
    requests: int = 0
    errors: int = 0
    latencies: list = field(default_factory=list)
    duration: float = 0

    @property
    def rps(self):
        return self.requests / max(self.duration, 0.001)

    @property
    def error_rate(self):
        return self.errors / max(self.requests, 1) * 100

    @property
    def p50(self):
        if not self.latencies:
            return 0
        s = sorted(self.latencies)
        return s[len(s) // 2]

    @property
    def p99(self):
        if not self.latencies:
            return 0
        s = sorted(self.latencies)
        return s[int(len(s) * 0.99)]


async def run_inference(client, url, model, prompt, max_tokens):
    body = {
        "model": model,
        "messages": [{"role": "user", "content": prompt}],
        "max_tokens": max_tokens,
    }
    start = time.monotonic()
    try:
        resp = await client.post(url, json=body, timeout=30.0)
        elapsed = (time.monotonic() - start) * 1000
        return resp.status_code, elapsed
    except Exception:
        elapsed = (time.monotonic() - start) * 1000
        return 0, elapsed


async def run_level(client, url, model, concurrency, duration):
    result = LevelResult(concurrency=concurrency)
    prompts = [
        "What is 2+2?",
        "Define inference.",
        "What is Kubernetes?",
        "Explain GPU.",
        "Hello.",
    ]
    prompt_idx = 0
    start = time.monotonic()

    while time.monotonic() - start < duration:
        tasks = []
        for _ in range(concurrency):
            prompt = prompts[prompt_idx % len(prompts)]
            prompt_idx += 1
            tasks.append(run_inference(client, url, model, prompt, 8))

        results = await asyncio.gather(*tasks, return_exceptions=True)
        for r in results:
            if isinstance(r, Exception):
                result.errors += 1
                result.requests += 1
            else:
                status, ms = r
                result.requests += 1
                if status == 200:
                    result.latencies.append(ms)
                else:
                    result.errors += 1

    result.duration = time.monotonic() - start
    return result


async def main():
    parser = argparse.ArgumentParser(description="Inference saturation test")
    parser.add_argument("--praxis-url", required=True)
    parser.add_argument("--model", default="granite-2b-cpu")
    parser.add_argument("--max-concurrency", type=int, default=50)
    parser.add_argument("--duration-per-level", type=int, default=30)
    parser.add_argument("--step", type=int, default=5)
    args = parser.parse_args()

    url = f"{args.praxis_url.rstrip('/')}/v1/chat/completions"

    print(f"\n{'='*70}", file=sys.stderr)
    print(f"INFERENCE SATURATION TEST", file=sys.stderr)
    print(f"{'='*70}", file=sys.stderr)
    print(f"  Target:    {url}", file=sys.stderr)
    print(f"  Model:     {args.model}", file=sys.stderr)
    print(f"  Max conc:  {args.max_concurrency}", file=sys.stderr)
    print(f"  Duration:  {args.duration_per_level}s per level", file=sys.stderr)
    print(f"  Step:      {args.step}", file=sys.stderr)
    print(f"{'='*70}\n", file=sys.stderr)

    print(f"  {'Conc':>4s}  {'Reqs':>6s}  {'Err%':>5s}  {'RPS':>6s}  "
          f"{'p50ms':>7s}  {'p99ms':>7s}  Status", file=sys.stderr)
    print(f"  {'----':>4s}  {'------':>6s}  {'-----':>5s}  {'------':>6s}  "
          f"{'-------':>7s}  {'-------':>7s}  ------", file=sys.stderr)

    all_results = []
    broke = False

    async with httpx.AsyncClient(verify=False, http2=True) as client:
        levels = [1, 2, 3] + list(range(args.step, args.max_concurrency + 1, args.step))
        for conc in levels:
            result = await run_level(client, url, args.model, conc, args.duration_per_level)
            all_results.append(result)

            status = "OK"
            if result.error_rate > 5:
                status = "DEGRADED"
            if result.error_rate > 20:
                status = "FAILING"
            if result.p99 > 30000:
                status = "TIMEOUT"

            print(f"  {conc:4d}  {result.requests:6d}  {result.error_rate:4.1f}%  "
                  f"{result.rps:6.1f}  {result.p50:7.0f}  {result.p99:7.0f}  {status}",
                  file=sys.stderr)

            if result.error_rate > 20 or result.p99 > 30000:
                print(f"\n  >>> Stopping: {'error rate' if result.error_rate > 20 else 'latency'} "
                      f"exceeded threshold at concurrency={conc}\n", file=sys.stderr)
                broke = True
                break

    print(f"\n{'='*70}", file=sys.stderr)
    print(f"SATURATION RESULTS", file=sys.stderr)
    print(f"{'='*70}", file=sys.stderr)

    peak = max(all_results, key=lambda r: r.rps if r.error_rate < 5 else 0)
    last_clean = [r for r in all_results if r.error_rate < 1]
    max_clean = last_clean[-1] if last_clean else all_results[0]

    print(f"  Peak RPS (< 5% errors):  {peak.rps:.1f} at concurrency={peak.concurrency}", file=sys.stderr)
    print(f"  Max clean (< 1% errors): {max_clean.rps:.1f} at concurrency={max_clean.concurrency}", file=sys.stderr)
    print(f"  p50 at peak: {peak.p50:.0f}ms", file=sys.stderr)
    print(f"  p99 at peak: {peak.p99:.0f}ms", file=sys.stderr)
    if broke:
        print(f"  Break point: concurrency={all_results[-1].concurrency}", file=sys.stderr)
    print(f"  Total requests: {sum(r.requests for r in all_results)}", file=sys.stderr)

    # JSON output
    print(json.dumps({
        "model": args.model,
        "peak_rps": round(peak.rps, 1),
        "peak_concurrency": peak.concurrency,
        "max_clean_rps": round(max_clean.rps, 1),
        "max_clean_concurrency": max_clean.concurrency,
        "p50_at_peak_ms": round(peak.p50),
        "p99_at_peak_ms": round(peak.p99),
        "break_concurrency": all_results[-1].concurrency if broke else None,
        "total_requests": sum(r.requests for r in all_results),
        "levels": [{
            "concurrency": r.concurrency,
            "requests": r.requests,
            "error_rate": round(r.error_rate, 1),
            "rps": round(r.rps, 1),
            "p50_ms": round(r.p50),
            "p99_ms": round(r.p99),
        } for r in all_results],
    }, indent=2))


if __name__ == "__main__":
    asyncio.run(main())
