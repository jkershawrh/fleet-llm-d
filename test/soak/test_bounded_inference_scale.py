import importlib.util
import subprocess
import sys
from unittest.mock import patch
from argparse import Namespace
from pathlib import Path

import pytest


MODULE_PATH = Path(__file__).with_name("bounded_inference_scale.py")
SPEC = importlib.util.spec_from_file_location("bounded_inference_scale", MODULE_PATH)
MODULE = importlib.util.module_from_spec(SPEC)
assert SPEC.loader
sys.modules[SPEC.name] = MODULE
SPEC.loader.exec_module(MODULE)


def test_concurrency_levels_are_bounded_powers_of_two():
    assert MODULE.concurrency_levels(1) == [1]
    assert MODULE.concurrency_levels(10) == [1, 2, 4, 8]


def test_traffic_only_requires_separate_cluster_local_evidence():
    MODULE.validate_resource_mode(True, [])
    with pytest.raises(ValueError, match="cannot be combined"):
        MODULE.validate_resource_mode(True, [MODULE.ResourceTarget("x", "n", "s")])
    with pytest.raises(ValueError, match="at least one"):
        MODULE.validate_resource_mode(False, [])


def test_quantity_parsing():
    assert MODULE.parse_cpu("250m") == 0.25
    assert MODULE.parse_cpu("2") == 2
    assert MODULE.parse_memory("128Mi") == 128 * 1024**2


def test_percentile_is_deterministic():
    assert MODULE.percentile([5, 1, 4, 2, 3], 0.50) == 3
    assert MODULE.percentile([], 0.99) == 0


def test_response_error_captures_structured_code_without_unbounded_body():
    body = b'{"error":{"code":"quota_exceeded","message":"token budget exhausted"}}'
    assert MODULE.response_error(body, 429) == "HTTP 429 quota_exceeded: token budget exhausted"
    assert MODULE.response_error(b"not-json", 502) == "HTTP 502"


def test_level_summary_includes_routing_and_resource_evidence():
    result = MODULE.LevelResult(
        concurrency=2,
        duration_seconds=2,
        requests=4,
        completion_tokens=8,
        latencies_ms=[10, 20, 30, 40],
        routed_counts={"cpucluster": 2, "hubcluster": 2},
        data_plane_counts={"llm-d-router": 4},
        status_counts={"200": 4},
        resources=[MODULE.ResourceSample(target="hubcluster", cpu_percent=20, memory_percent=30)],
    )
    summary = result.summary()
    assert summary["rps"] == 2
    assert summary["tokens_per_second"] == 4
    assert summary["routed_counts"] == {"cpucluster": 2, "hubcluster": 2}
    assert summary["status_counts"] == {"200": 4}
    assert summary["resources"][0]["target"] == "hubcluster"


def test_request_interval_throttles_batches(monkeypatch):
    sleeps = []

    async def fake_infer(*_args, **_kwargs):
        return MODULE.RequestResult(200, 1, 1, 1, data_plane="llm-d-router")

    async def fake_sleep(seconds):
        sleeps.append(seconds)
        raise RuntimeError("stop")

    args = Namespace(
        duration_per_level=60,
        url="https://gateway.example/v1/chat/completions",
        model="model",
        max_tokens=1,
        stream=False,
        request_interval=2.5,
        resource_targets=[],
    )
    monkeypatch.setattr(MODULE, "infer", fake_infer)
    monkeypatch.setattr(MODULE.asyncio, "sleep", fake_sleep)
    try:
        MODULE.asyncio.run(MODULE.run_level(object(), args, 1))
    except RuntimeError as exc:
        assert str(exc) == "stop"
    assert sleeps == [2.5]


def test_level_drains_admitted_batch_after_deadline(monkeypatch):
    async def slow_infer(*_args, **_kwargs):
        await MODULE.asyncio.sleep(0.02)
        return MODULE.RequestResult(200, 1, 1, 1, data_plane="llm-d-router")

    args = Namespace(
        duration_per_level=0.01,
        url="https://gateway.example/v1/chat/completions",
        model="model",
        max_tokens=1,
        stream=False,
        request_interval=0,
        resource_targets=[],
    )
    monkeypatch.setattr(MODULE, "infer", slow_infer)
    started = MODULE.time.monotonic()
    result = MODULE.asyncio.run(MODULE.run_level(object(), args, 1))
    assert MODULE.time.monotonic() - started < 0.5
    assert result.requests == 1
    assert result.status_counts == {"200": 1}


def test_level_records_bounded_transport_error_samples():
    result = MODULE.LevelResult(concurrency=1)
    for index in range(8):
        result.record(MODULE.RequestResult(0, 1, 0, 0, error=f"timeout-{index + 1}"))
    assert result.status_counts == {"transport_error": 8}
    assert len(result.error_samples) == 5
    assert len(result.error_events) == 8


def test_level_records_timestamped_error_evidence():
    result = MODULE.LevelResult(concurrency=1)
    result.record(MODULE.RequestResult(
        503, 12, 0, 0,
        routed_to="cpucluster-xeon6",
        request_id="req-123",
        error="HTTP 503 no_compatible_capacity",
        observed_at="2026-08-28T19:46:20+00:00",
    ))
    assert result.summary()["error_events"] == [{
        "observed_at": "2026-08-28T19:46:20+00:00",
        "status": 503,
        "error": "HTTP 503 no_compatible_capacity",
        "request_id": "req-123",
        "routed_to": "cpucluster-xeon6",
        "actual_model": "",
        "data_plane": "",
    }]


def test_oc_uses_explicit_context(monkeypatch):
    captured = {}

    def fake_run(command, **kwargs):
        captured["command"] = command
        captured["env"] = kwargs["env"]
        return subprocess.CompletedProcess(command, 0, stdout="ok")

    monkeypatch.setattr(MODULE.subprocess, "run", fake_run)
    target = MODULE.ResourceTarget(
        name="hubcluster",
        namespace="fleet-llm-d",
        selector="app=model",
        kubeconfig="/tmp/kubeconfig",
        context="hubcluster-admin",
    )

    assert MODULE._oc(target, "get", "pods") == "ok"
    assert captured["command"] == [
        "oc",
        "--context",
        "hubcluster-admin",
        "-n",
        "fleet-llm-d",
        "get",
        "pods",
    ]
    assert captured["env"]["KUBECONFIG"] == "/tmp/kubeconfig"


def test_report_identifies_source_transport_and_gateway(capsys, monkeypatch):
    async def fake_run_level(_client, _args, concurrency):
        return MODULE.LevelResult(
            concurrency=concurrency,
            duration_seconds=1,
            requests=1,
            latencies_ms=[10],
            ttft_ms=[10],
            routed_counts={"cpucluster-xeon6": 1},
            model_counts={"granite-2b-cpu": 1},
            data_plane_counts={"llm-d-router": 1},
            status_counts={"200": 1},
        )

    monkeypatch.setattr(MODULE, "run_level", fake_run_level)
    argv = [
        "bounded_inference_scale.py",
        "--fleet-url=https://router.example.test",
        "--model=granite-2b-cpu",
        "--data-plane=llm-d-router",
        "--source-cluster=cpucluster",
        "--transport=external-route",
        "--max-concurrency=1",
        "--duration-per-level=1",
        "--traffic-only",
    ]
    with patch.object(sys, "argv", argv):
        assert MODULE.asyncio.run(MODULE.main()) == 0
    output = capsys.readouterr().out
    report = MODULE.json.loads(output[output.index("{\n") :])
    assert report["source_cluster"] == "cpucluster"
    assert report["transport"] == "external-route"
    assert report["gateway_host"] == "router.example.test"
