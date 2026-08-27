"""Bounded multi-cluster inference capacity qualification.

The harness stops before saturation. It samples Kubernetes container usage and
never advances when CPU or memory reaches the configured utilization guardrail.
GPU deployments should additionally be monitored through DCGM; absence of a
GPU utilization source is reported explicitly in the result.
"""

from __future__ import annotations

import argparse
import asyncio
import json
import os
import re
import subprocess
import time
from dataclasses import asdict, dataclass, field
from typing import Any

import httpx


CPU_RE = re.compile(r"^(\d+(?:\.\d+)?)(m?)$")
MEMORY_UNITS = {"Ki": 1024, "Mi": 1024**2, "Gi": 1024**3, "Ti": 1024**4}


def percentile(values: list[float], quantile: float) -> float:
    if not values:
        return 0.0
    ordered = sorted(values)
    index = min(len(ordered) - 1, max(0, int((len(ordered) - 1) * quantile)))
    return ordered[index]


def parse_cpu(value: str) -> float:
    match = CPU_RE.match(value)
    if not match:
        raise ValueError(f"unsupported CPU quantity {value!r}")
    amount = float(match.group(1))
    return amount / 1000 if match.group(2) == "m" else amount


def parse_memory(value: str) -> float:
    for suffix, multiplier in MEMORY_UNITS.items():
        if value.endswith(suffix):
            return float(value[: -len(suffix)]) * multiplier
    return float(value)


def concurrency_levels(maximum: int) -> list[int]:
    levels: list[int] = []
    value = 1
    while value <= maximum:
        levels.append(value)
        value *= 2
    return levels


def validate_resource_mode(traffic_only: bool, targets: list[ResourceTarget]) -> None:
    if traffic_only and targets:
        raise ValueError("--traffic-only cannot be combined with --resource-target")
    if not traffic_only and not targets:
        raise ValueError("at least one --resource-target is required unless --traffic-only is set")


def response_error(body: bytes, status: int) -> str:
    """Return a bounded structured error label without retaining response data."""
    try:
        payload = json.loads(body)
    except (json.JSONDecodeError, UnicodeDecodeError):
        return f"HTTP {status}"
    error = payload.get("error") if isinstance(payload, dict) else None
    if isinstance(error, dict):
        code = str(error.get("code", "")).strip()
        message = str(error.get("message", "")).strip()
        if code and message:
            return f"HTTP {status} {code}: {message[:160]}"
        if code:
            return f"HTTP {status} {code}"
    return f"HTTP {status}"


@dataclass
class ResourceTarget:
    name: str
    namespace: str
    selector: str
    kubeconfig: str = ""
    context: str = ""
    gpu: bool = False


@dataclass
class ResourceSample:
    target: str
    cpu_percent: float = 0.0
    memory_percent: float = 0.0
    restarts: int = 0
    error: str = ""


@dataclass
class RequestResult:
    status: int
    latency_ms: float
    ttft_ms: float
    completion_tokens: int
    routed_to: str = ""
    actual_model: str = ""
    data_plane: str = ""
    request_id: str = ""
    error: str = ""


@dataclass
class LevelResult:
    concurrency: int
    duration_seconds: float = 0.0
    requests: int = 0
    errors: int = 0
    completion_tokens: int = 0
    latencies_ms: list[float] = field(default_factory=list)
    ttft_ms: list[float] = field(default_factory=list)
    routed_counts: dict[str, int] = field(default_factory=dict)
    model_counts: dict[str, int] = field(default_factory=dict)
    data_plane_counts: dict[str, int] = field(default_factory=dict)
    status_counts: dict[str, int] = field(default_factory=dict)
    error_samples: list[str] = field(default_factory=list)
    resources: list[ResourceSample] = field(default_factory=list)
    stop_reasons: list[str] = field(default_factory=list)

    @property
    def error_percent(self) -> float:
        return 100 * self.errors / max(1, self.requests)

    @property
    def rps(self) -> float:
        return self.requests / max(0.001, self.duration_seconds)

    def record(self, request: RequestResult) -> None:
        self.requests += 1
        status_key = str(request.status) if request.status else "transport_error"
        self.status_counts[status_key] = self.status_counts.get(status_key, 0) + 1
        if request.status != 200:
            self.errors += 1
            if len(self.error_samples) < 5:
                self.error_samples.append(request.error or f"HTTP {request.status}")
        else:
            self.latencies_ms.append(request.latency_ms)
            self.ttft_ms.append(request.ttft_ms)
            self.completion_tokens += request.completion_tokens
        for value, counts in (
            (request.routed_to, self.routed_counts),
            (request.actual_model, self.model_counts),
            (request.data_plane, self.data_plane_counts),
        ):
            counts[value or "missing"] = counts.get(value or "missing", 0) + 1

    def summary(self) -> dict[str, Any]:
        return {
            "concurrency": self.concurrency,
            "duration_seconds": round(self.duration_seconds, 3),
            "requests": self.requests,
            "errors": self.errors,
            "error_percent": round(self.error_percent, 3),
            "rps": round(self.rps, 3),
            "tokens_per_second": round(self.completion_tokens / max(0.001, self.duration_seconds), 3),
            "latency_ms": {
                "p50": round(percentile(self.latencies_ms, 0.50), 3),
                "p95": round(percentile(self.latencies_ms, 0.95), 3),
                "p99": round(percentile(self.latencies_ms, 0.99), 3),
            },
            "ttft_ms": {
                "p50": round(percentile(self.ttft_ms, 0.50), 3),
                "p95": round(percentile(self.ttft_ms, 0.95), 3),
                "p99": round(percentile(self.ttft_ms, 0.99), 3),
            },
            "routed_counts": self.routed_counts,
            "model_counts": self.model_counts,
            "data_plane_counts": self.data_plane_counts,
            "status_counts": self.status_counts,
            "error_samples": self.error_samples,
            "resources": [asdict(sample) for sample in self.resources],
            "stop_reasons": self.stop_reasons,
        }


def _oc(target: ResourceTarget, *args: str) -> str:
    env = os.environ.copy()
    if target.kubeconfig:
        env["KUBECONFIG"] = target.kubeconfig
    command = ["oc"]
    if target.context:
        command.extend(("--context", target.context))
    command.extend(("-n", target.namespace, *args))
    return subprocess.run(command, env=env, check=True, capture_output=True, text=True, timeout=20).stdout


def sample_resources(target: ResourceTarget) -> ResourceSample:
    sample = ResourceSample(target=target.name)
    try:
        pods = json.loads(_oc(target, "get", "pods", "-l", target.selector, "-o", "json"))
        usage = _oc(target, "adm", "top", "pods", "-l", target.selector, "--containers", "--no-headers")
        limits: dict[tuple[str, str], tuple[float, float]] = {}
        for pod in pods.get("items", []):
            pod_name = pod["metadata"]["name"]
            statuses = {status["name"]: status for status in pod.get("status", {}).get("containerStatuses", [])}
            sample.restarts += sum(int(status.get("restartCount", 0)) for status in statuses.values())
            for container in pod.get("spec", {}).get("containers", []):
                resources = container.get("resources", {}).get("limits", {})
                if resources.get("cpu") and resources.get("memory"):
                    limits[(pod_name, container["name"])] = (
                        parse_cpu(resources["cpu"]), parse_memory(resources["memory"])
                    )
        cpu_percentages: list[float] = []
        memory_percentages: list[float] = []
        for line in usage.splitlines():
            fields = line.split()
            if len(fields) < 4 or (fields[0], fields[1]) not in limits:
                continue
            cpu_limit, memory_limit = limits[(fields[0], fields[1])]
            cpu_percentages.append(100 * parse_cpu(fields[2]) / cpu_limit)
            memory_percentages.append(100 * parse_memory(fields[3]) / memory_limit)
        if not cpu_percentages:
            raise RuntimeError("no usage samples with CPU and memory limits")
        sample.cpu_percent = round(max(cpu_percentages), 3)
        sample.memory_percent = round(max(memory_percentages), 3)
    except Exception as exc:  # resource telemetry failure is a stop condition
        sample.error = str(exc)
    return sample


async def infer(client: httpx.AsyncClient, url: str, model: str, prompt: str, max_tokens: int, stream: bool) -> RequestResult:
    payload = {
        "model": model,
        "messages": [{"role": "user", "content": prompt}],
        "max_tokens": max_tokens,
        "temperature": 0,
        "stream": stream,
    }
    start = time.monotonic()
    first_token = 0.0
    completion_tokens = 0
    try:
        async with client.stream("POST", url, json=payload, timeout=60.0) as response:
            body = b""
            if response.status_code != 200:
                body = await response.aread()
            elif stream:
                async for line in response.aiter_lines():
                    if line.startswith("data:") and line != "data: [DONE]" and not first_token:
                        first_token = time.monotonic()
            else:
                body = await response.aread()
                try:
                    completion_tokens = int(json.loads(body).get("usage", {}).get("completion_tokens", 0))
                except (ValueError, TypeError, AttributeError):
                    pass
            finished = time.monotonic()
            return RequestResult(
                status=response.status_code,
                latency_ms=(finished - start) * 1000,
                ttft_ms=((first_token or finished) - start) * 1000,
                completion_tokens=completion_tokens,
                routed_to=response.headers.get("x-fleet-routed-to", ""),
                actual_model=response.headers.get("x-fleet-actual-model", ""),
                data_plane=response.headers.get("x-fleet-data-plane", ""),
                request_id=response.headers.get("x-request-id", ""),
                error=response_error(body, response.status_code) if response.status_code != 200 else "",
            )
    except Exception as exc:
        finished = time.monotonic()
        return RequestResult(0, (finished - start) * 1000, 0, 0, error=str(exc))


async def run_level(client: httpx.AsyncClient, args: argparse.Namespace, concurrency: int) -> LevelResult:
    result = LevelResult(concurrency=concurrency)
    prompts = ["Reply with fleet-ok.", "What is inference?", "Define Kubernetes in one sentence."]
    started = time.monotonic()
    index = 0
    while time.monotonic() - started < args.duration_per_level:
        remaining = args.duration_per_level - (time.monotonic() - started)
        if remaining <= 0:
            break
        batch = [
            infer(client, args.url, args.model, prompts[(index + offset) % len(prompts)], args.max_tokens, args.stream)
            for offset in range(concurrency)
        ]
        index += concurrency
        try:
            requests = await asyncio.wait_for(asyncio.gather(*batch), timeout=remaining)
        except asyncio.TimeoutError:
            break
        for request in requests:
            result.record(request)
        if args.request_interval > 0:
            remaining = args.duration_per_level - (time.monotonic() - started)
            if remaining > 0:
                await asyncio.sleep(min(args.request_interval, remaining))
    result.duration_seconds = time.monotonic() - started
    result.resources = await asyncio.gather(*[asyncio.to_thread(sample_resources, target) for target in args.resource_targets])
    return result


async def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--fleet-url", required=True)
    parser.add_argument("--token", default=os.environ.get("FLEET_AUTH_TOKEN", ""), help="Bearer token; prefer FLEET_AUTH_TOKEN")
    parser.add_argument("--model", required=True)
    parser.add_argument("--data-plane", choices=("praxis", "llm-d-router"), required=True)
    parser.add_argument("--max-concurrency", type=int, default=16)
    parser.add_argument("--duration-per-level", type=int, default=300)
    parser.add_argument("--max-tokens", type=int, default=16)
    parser.add_argument("--resource-guardrail", type=float, default=70.0)
    parser.add_argument("--error-guardrail", type=float, default=2.0)
    parser.add_argument("--stream", action="store_true")
    parser.add_argument("--insecure", action="store_true")
    parser.add_argument("--ca-file", default="", help="PEM CA bundle used to verify the gateway")
    parser.add_argument("--request-interval", type=float, default=0.0, help="Seconds to wait between request batches")
    parser.add_argument("--resource-target", action="append", default=[], help="JSON ResourceTarget; repeat per cluster")
    parser.add_argument("--traffic-only", action="store_true", help="Run traffic in-cluster; resource evidence is collected by cluster-local Jobs")
    parser.add_argument("--output", default="")
    args = parser.parse_args()
    args.url = args.fleet_url.rstrip("/") + "/v1/chat/completions"
    args.resource_targets = [ResourceTarget(**json.loads(raw)) for raw in args.resource_target]
    try:
        validate_resource_mode(args.traffic_only, args.resource_targets)
    except ValueError as exc:
        parser.error(str(exc))
    if args.insecure and args.ca_file:
        parser.error("--insecure and --ca-file are mutually exclusive")
    if args.request_interval < 0:
        parser.error("--request-interval cannot be negative")

    headers = {"Authorization": f"Bearer {args.token}"} if args.token else {}
    results: list[LevelResult] = []
    baseline_p99 = 0.0
    latency_breaches = 0
    verify: bool | str = args.ca_file or not args.insecure
    async with httpx.AsyncClient(verify=verify, http2=True, headers=headers) as client:
        for concurrency in concurrency_levels(args.max_concurrency):
            result = await run_level(client, args, concurrency)
            p99 = percentile(result.latencies_ms, 0.99)
            if not baseline_p99 and result.latencies_ms:
                baseline_p99 = p99
            if result.error_percent >= args.error_guardrail:
                result.stop_reasons.append(f"error rate {result.error_percent:.2f}% reached guardrail")
            if baseline_p99 and p99 > baseline_p99 * 2:
                latency_breaches += 1
            else:
                latency_breaches = 0
            if latency_breaches >= 2:
                result.stop_reasons.append("p99 exceeded twice the clean baseline for two levels")
            for sample in result.resources:
                if sample.error:
                    result.stop_reasons.append(f"resource telemetry unavailable for {sample.target}: {sample.error}")
                if max(sample.cpu_percent, sample.memory_percent) >= args.resource_guardrail:
                    result.stop_reasons.append(f"{sample.target} reached resource guardrail")
            observed_planes = set(result.data_plane_counts) - {"missing"}
            if observed_planes != {args.data_plane}:
                result.stop_reasons.append(f"data-plane evidence mismatch: {sorted(observed_planes)}")
            results.append(result)
            print(json.dumps(result.summary(), sort_keys=True), flush=True)
            if result.stop_reasons:
                break

    clean = [result for result in results if not result.stop_reasons and result.error_percent < 1.0]
    report = {
        "schema_version": "fleet-scale-v1",
        "model": args.model,
        "data_plane": args.data_plane,
        "resource_guardrail_percent": args.resource_guardrail,
        "safe_concurrency": clean[-1].concurrency if clean else 0,
        "safe_rps": round(clean[-1].rps, 3) if clean else 0,
        "certification_rps": round(clean[-1].rps * 0.5, 3) if clean else 0,
        "gpu_utilization_external_required": any(target.gpu for target in args.resource_targets),
        "cluster_local_resource_evidence_required": args.traffic_only,
        "levels": [result.summary() for result in results],
    }
    encoded = json.dumps(report, indent=2, sort_keys=True)
    if args.output:
        with open(args.output, "w", encoding="utf-8") as output:
            output.write(encoded + "\n")
    print(encoded)
    return 0 if clean else 2


if __name__ == "__main__":
    raise SystemExit(asyncio.run(main()))
