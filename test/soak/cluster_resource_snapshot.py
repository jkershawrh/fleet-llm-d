"""Collect a bounded resource snapshot from the pod's local Kubernetes API.

This collector is intended to run as a short-lived Job in each inference
cluster. It uses only the mounted service-account token and read-only pod and
metrics APIs; no kubeconfig or cross-cluster administrator credential is used.
"""

from __future__ import annotations

import argparse
import json
import os
import ssl
import urllib.parse
import urllib.request
from dataclasses import asdict, dataclass
from pathlib import Path
from typing import Any


CPU_SUFFIXES = {"n": 1e-9, "u": 1e-6, "m": 1e-3}
MEMORY_SUFFIXES = {"Ki": 1024, "Mi": 1024**2, "Gi": 1024**3, "Ti": 1024**4}


def parse_cpu(value: str) -> float:
    for suffix, multiplier in CPU_SUFFIXES.items():
        if value.endswith(suffix):
            return float(value[: -len(suffix)]) * multiplier
    return float(value)


def parse_memory(value: str) -> float:
    for suffix, multiplier in MEMORY_SUFFIXES.items():
        if value.endswith(suffix):
            return float(value[: -len(suffix)]) * multiplier
    return float(value)


@dataclass
class ContainerSnapshot:
    pod: str
    container: str
    cpu_cores: float
    cpu_limit_cores: float
    cpu_percent: float
    memory_bytes: float
    memory_limit_bytes: float
    memory_percent: float
    restarts: int
    ready: bool


def summarize(pods: dict[str, Any], metrics: dict[str, Any], target: str) -> dict[str, Any]:
    metric_index: dict[tuple[str, str], dict[str, str]] = {}
    for pod in metrics.get("items", []):
        pod_name = pod.get("metadata", {}).get("name", "")
        for container in pod.get("containers", []):
            metric_index[(pod_name, container.get("name", ""))] = container.get("usage", {})

    snapshots: list[ContainerSnapshot] = []
    for pod in pods.get("items", []):
        pod_name = pod.get("metadata", {}).get("name", "")
        statuses = {item.get("name", ""): item for item in pod.get("status", {}).get("containerStatuses", [])}
        for container in pod.get("spec", {}).get("containers", []):
            name = container.get("name", "")
            limits = container.get("resources", {}).get("limits", {})
            usage = metric_index.get((pod_name, name))
            if not usage or not limits.get("cpu") or not limits.get("memory"):
                continue
            cpu, cpu_limit = parse_cpu(usage["cpu"]), parse_cpu(limits["cpu"])
            memory, memory_limit = parse_memory(usage["memory"]), parse_memory(limits["memory"])
            status = statuses.get(name, {})
            snapshots.append(ContainerSnapshot(
                pod=pod_name,
                container=name,
                cpu_cores=round(cpu, 6),
                cpu_limit_cores=cpu_limit,
                cpu_percent=round(100 * cpu / cpu_limit, 3),
                memory_bytes=memory,
                memory_limit_bytes=memory_limit,
                memory_percent=round(100 * memory / memory_limit, 3),
                restarts=int(status.get("restartCount", 0)),
                ready=bool(status.get("ready", False)),
            ))
    if not snapshots:
        raise RuntimeError("no containers had metrics plus CPU and memory limits")
    return {
        "schema_version": "fleet-cluster-resource-v1",
        "target": target,
        "containers": [asdict(item) for item in snapshots],
        "max_cpu_percent": max(item.cpu_percent for item in snapshots),
        "max_memory_percent": max(item.memory_percent for item in snapshots),
        "restarts": sum(item.restarts for item in snapshots),
        "all_ready": all(item.ready for item in snapshots),
    }


def api_get(api: str, path: str, token: str, ca_file: str) -> dict[str, Any]:
    request = urllib.request.Request(api.rstrip("/") + path, headers={"Authorization": f"Bearer {token}"})
    context = ssl.create_default_context(cafile=ca_file)
    with urllib.request.urlopen(request, context=context, timeout=20) as response:
        if response.status != 200:
            raise RuntimeError(f"Kubernetes API returned HTTP {response.status}")
        return json.load(response)


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--target", required=True)
    parser.add_argument("--namespace", required=True)
    parser.add_argument("--selector", required=True)
    parser.add_argument("--output", default="/dev/termination-log")
    args = parser.parse_args()
    service_host = os.environ.get("KUBERNETES_SERVICE_HOST", "kubernetes.default.svc")
    service_port = os.environ.get("KUBERNETES_SERVICE_PORT_HTTPS", "443")
    api = f"https://{service_host}:{service_port}"
    service_account = Path("/var/run/secrets/kubernetes.io/serviceaccount")
    token = (service_account / "token").read_text().strip()
    ca_file = str(service_account / "ca.crt")
    namespace = urllib.parse.quote(args.namespace, safe="")
    selector = urllib.parse.quote(args.selector, safe="")
    pods = api_get(api, f"/api/v1/namespaces/{namespace}/pods?labelSelector={selector}", token, ca_file)
    metrics = api_get(api, f"/apis/metrics.k8s.io/v1beta1/namespaces/{namespace}/pods?labelSelector={selector}", token, ca_file)
    encoded = json.dumps(summarize(pods, metrics, args.target), indent=2, sort_keys=True) + "\n"
    Path(args.output).write_text(encoded)
    print(encoded, end="")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
