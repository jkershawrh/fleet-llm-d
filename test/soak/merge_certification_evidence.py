"""Merge in-cluster traffic and provider-local resource evidence."""

from __future__ import annotations

import argparse
import json
from pathlib import Path
from typing import Any


def evaluate(traffic: dict[str, Any], resources: list[dict[str, Any]], required: set[str], guardrail: float) -> dict[str, Any]:
    failures: list[str] = []
    if traffic.get("schema_version") != "fleet-scale-v1":
        failures.append("traffic schema is not fleet-scale-v1")
    levels = traffic.get("levels", [])
    if not levels:
        failures.append("traffic report contains no levels")
    for index, level in enumerate(levels):
        if level.get("errors", 0):
            failures.append(f"traffic level {index} recorded errors")
        if level.get("stop_reasons"):
            failures.append(f"traffic level {index} recorded stop reasons")
    by_target = {item.get("target", ""): item for item in resources}
    missing = sorted(required - set(by_target))
    if missing:
        failures.append("missing resource evidence: " + ", ".join(missing))
    for target in sorted(required & set(by_target)):
        report = by_target[target]
        if report.get("schema_version") != "fleet-cluster-resource-v1":
            failures.append(f"{target} resource schema is invalid")
        if not report.get("all_ready", False):
            failures.append(f"{target} has an unready container")
        if report.get("restarts", 0):
            failures.append(f"{target} recorded container restarts")
        if max(float(report.get("max_cpu_percent", 0)), float(report.get("max_memory_percent", 0))) >= guardrail:
            failures.append(f"{target} reached the {guardrail:g}% resource guardrail")
    return {
        "schema_version": "fleet-certification-v1",
        "passed": not failures,
        "failures": failures,
        "traffic": traffic,
        "resources": [by_target[target] for target in sorted(by_target)],
        "required_resource_targets": sorted(required),
        "resource_guardrail_percent": guardrail,
    }


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--traffic", required=True)
    parser.add_argument("--resource", action="append", default=[])
    parser.add_argument("--require-target", action="append", default=[])
    parser.add_argument("--resource-guardrail", type=float, default=70.0)
    parser.add_argument("--output", default="")
    args = parser.parse_args()
    traffic = json.loads(Path(args.traffic).read_text())
    resources = [json.loads(Path(path).read_text()) for path in args.resource]
    report = evaluate(traffic, resources, set(args.require_target), args.resource_guardrail)
    encoded = json.dumps(report, indent=2, sort_keys=True) + "\n"
    if args.output:
        Path(args.output).write_text(encoded)
    print(encoded, end="")
    return 0 if report["passed"] else 2


if __name__ == "__main__":
    raise SystemExit(main())
