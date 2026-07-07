#!/usr/bin/env python3
"""Analyze soak test results and generate a report."""
import json
import statistics
import sys


def analyze(results_file: str) -> int:
    """Load results JSON, compute summary statistics, check pass criteria.

    Returns 0 if all criteria pass, 1 otherwise.
    """
    with open(results_file) as f:
        data = json.load(f)

    results = data.get("results", {})
    config = data.get("config", {})
    snapshots = data.get("snapshots", [])

    total_requests = results.get("total_requests", 0)
    total_errors = results.get("total_errors", 0)
    error_rate = results.get("error_rate_pct", 0.0)
    latency = results.get("latency", {})
    memory = results.get("memory", {})

    p50 = latency.get("p50_s", 0.0)
    p95 = latency.get("p95_s", 0.0)
    p99 = latency.get("p99_s", 0.0)

    mem_start = memory.get("start_bytes", 0)
    mem_end = memory.get("end_bytes", 0)
    mem_max = memory.get("max_bytes", 0)
    mem_growth = memory.get("growth_pct", 0.0)

    # Compute snapshot-level statistics if available.
    snapshot_error_rates = [s.get("error_rate_pct", 0.0) for s in snapshots if s]
    snapshot_memories = [s.get("memory_bytes", 0) for s in snapshots if s]

    avg_snapshot_error_rate = (
        statistics.mean(snapshot_error_rates) if snapshot_error_rates else 0.0
    )
    max_snapshot_error_rate = max(snapshot_error_rates) if snapshot_error_rates else 0.0
    avg_snapshot_memory = (
        statistics.mean(snapshot_memories) if snapshot_memories else 0.0
    )

    # Memory trend: compute linear slope over snapshots.
    mem_trend = "stable"
    if len(snapshot_memories) >= 3:
        first_third = statistics.mean(snapshot_memories[: len(snapshot_memories) // 3])
        last_third = statistics.mean(
            snapshot_memories[2 * len(snapshot_memories) // 3 :]
        )
        if first_third > 0:
            trend_pct = ((last_third - first_third) / first_third) * 100
            if trend_pct > 10:
                mem_trend = f"increasing (+{trend_pct:.1f}%)"
            elif trend_pct < -10:
                mem_trend = f"decreasing ({trend_pct:.1f}%)"
            else:
                mem_trend = f"stable ({trend_pct:+.1f}%)"

    # Pass criteria.
    pass_error_rate = error_rate < 0.1
    pass_memory = abs(mem_growth) < 50.0
    all_pass = pass_error_rate and pass_memory

    # Format byte values for display.
    def fmt_bytes(b: float) -> str:
        if b == 0:
            return "0 B"
        for unit in ("B", "KB", "MB", "GB"):
            if abs(b) < 1024:
                return f"{b:.1f} {unit}"
            b /= 1024
        return f"{b:.1f} TB"

    # Print report.
    width = 60
    print()
    print("=" * width)
    print("  SOAK TEST ANALYSIS REPORT")
    print("=" * width)
    print()

    print("  Configuration:")
    print(f"    URL:           {config.get('url', 'N/A')}")
    print(f"    Duration:      {config.get('duration_s', 'N/A')}s")
    print(f"    Target RPS:    {config.get('target_rps', 'N/A')}")
    print()

    print("  Request Summary:")
    print(f"    Total:         {total_requests:,}")
    print(f"    Errors:        {total_errors:,}")
    print(f"    Error Rate:    {error_rate:.4f}%", end="")
    print(f"  {'PASS' if pass_error_rate else 'FAIL'} (threshold: <0.1%)")
    if total_requests > 0:
        actual_rps = total_requests / config.get("duration_s", 1)
        print(f"    Actual RPS:    {actual_rps:.1f}")
    print()

    print("  Latency:")
    print(f"    p50:           {p50:.3f}s ({p50 * 1000:.1f}ms)")
    print(f"    p95:           {p95:.3f}s ({p95 * 1000:.1f}ms)")
    print(f"    p99:           {p99:.3f}s ({p99 * 1000:.1f}ms)")
    print()

    print("  Memory:")
    print(f"    Start:         {fmt_bytes(mem_start)}")
    print(f"    End:           {fmt_bytes(mem_end)}")
    print(f"    Max:           {fmt_bytes(mem_max)}")
    print(f"    Growth:        {mem_growth:.1f}%", end="")
    print(f"  {'PASS' if pass_memory else 'FAIL'} (threshold: <50%)")
    print(f"    Trend:         {mem_trend}")
    print()

    if snapshots:
        print("  Snapshot Analysis:")
        print(f"    Snapshots:     {len(snapshots)}")
        print(f"    Avg Error Rate:{avg_snapshot_error_rate:.4f}%")
        print(f"    Max Error Rate:{max_snapshot_error_rate:.4f}%")
        print(f"    Avg Memory:    {fmt_bytes(avg_snapshot_memory)}")
        print()

    print("-" * width)
    if all_pass:
        print("  OVERALL RESULT:  PASS")
    else:
        print("  OVERALL RESULT:  FAIL")
        if not pass_error_rate:
            print(f"    - Error rate {error_rate:.4f}% >= 0.1% threshold")
        if not pass_memory:
            print(f"    - Memory growth {mem_growth:.1f}% >= 50% threshold")
    print("=" * width)
    print()

    return 0 if all_pass else 1


def main():
    if len(sys.argv) < 2:
        print(f"Usage: {sys.argv[0]} <results.json>", file=sys.stderr)
        print()
        print("Analyze soak test results and generate a formatted report.")
        print("Pass criteria: error rate < 0.1%, memory growth < 50%.")
        sys.exit(2)

    results_file = sys.argv[1]

    try:
        exit_code = analyze(results_file)
    except FileNotFoundError:
        print(f"Error: results file not found: {results_file}", file=sys.stderr)
        sys.exit(2)
    except json.JSONDecodeError as e:
        print(f"Error: invalid JSON in {results_file}: {e}", file=sys.stderr)
        sys.exit(2)
    except Exception as e:
        print(f"Error analyzing results: {e}", file=sys.stderr)
        sys.exit(2)

    sys.exit(exit_code)


if __name__ == "__main__":
    main()
