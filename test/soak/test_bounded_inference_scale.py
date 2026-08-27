import importlib.util
import subprocess
import sys
from argparse import Namespace
from pathlib import Path


MODULE_PATH = Path(__file__).with_name("bounded_inference_scale.py")
SPEC = importlib.util.spec_from_file_location("bounded_inference_scale", MODULE_PATH)
MODULE = importlib.util.module_from_spec(SPEC)
assert SPEC.loader
sys.modules[SPEC.name] = MODULE
SPEC.loader.exec_module(MODULE)


def test_concurrency_levels_are_bounded_powers_of_two():
    assert MODULE.concurrency_levels(1) == [1]
    assert MODULE.concurrency_levels(10) == [1, 2, 4, 8]


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
        routed_counts={"arena": 2, "oberon": 2},
        data_plane_counts={"llm-d-router": 4},
        status_counts={"200": 4},
        resources=[MODULE.ResourceSample(target="oberon", cpu_percent=20, memory_percent=30)],
    )
    summary = result.summary()
    assert summary["rps"] == 2
    assert summary["tokens_per_second"] == 4
    assert summary["routed_counts"] == {"arena": 2, "oberon": 2}
    assert summary["status_counts"] == {"200": 4}
    assert summary["resources"][0]["target"] == "oberon"


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


def test_level_cancels_batch_at_deadline(monkeypatch):
    async def slow_infer(*_args, **_kwargs):
        await MODULE.asyncio.sleep(1)
        return MODULE.RequestResult(200, 1, 1, 1, data_plane="llm-d-router")

    args = Namespace(
        duration_per_level=0.05,
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
    assert result.requests == 0


def test_level_records_bounded_transport_error_samples():
    result = MODULE.LevelResult(concurrency=1)
    for index in range(8):
        result.record(MODULE.RequestResult(0, 1, 0, 0, error=f"timeout-{index + 1}"))
    assert result.status_counts == {"transport_error": 8}
    assert len(result.error_samples) == 5


def test_oc_uses_explicit_context(monkeypatch):
    captured = {}

    def fake_run(command, **kwargs):
        captured["command"] = command
        captured["env"] = kwargs["env"]
        return subprocess.CompletedProcess(command, 0, stdout="ok")

    monkeypatch.setattr(MODULE.subprocess, "run", fake_run)
    target = MODULE.ResourceTarget(
        name="oberon",
        namespace="fleet-llm-d",
        selector="app=model",
        kubeconfig="/tmp/kubeconfig",
        context="oberon-admin",
    )

    assert MODULE._oc(target, "get", "pods") == "ok"
    assert captured["command"] == [
        "oc",
        "--context",
        "oberon-admin",
        "-n",
        "fleet-llm-d",
        "get",
        "pods",
    ]
    assert captured["env"]["KUBECONFIG"] == "/tmp/kubeconfig"
