import importlib.util
import sys
from pathlib import Path

import pytest


MODULE_PATH = Path(__file__).with_name("cluster_resource_snapshot.py")
SPEC = importlib.util.spec_from_file_location("cluster_resource_snapshot", MODULE_PATH)
MODULE = importlib.util.module_from_spec(SPEC)
assert SPEC.loader
sys.modules[SPEC.name] = MODULE
SPEC.loader.exec_module(MODULE)


def test_quantity_parsing_supports_metrics_api_units():
    assert MODULE.parse_cpu("250m") == 0.25
    assert MODULE.parse_cpu("500000n") == 0.0005
    assert MODULE.parse_memory("2Gi") == 2 * 1024**3


def test_summarize_reports_limits_restarts_and_readiness():
    pods = {"items": [{
        "metadata": {"name": "model-0"},
        "spec": {"containers": [{"name": "model", "resources": {"limits": {"cpu": "2", "memory": "2Gi"}}}]},
        "status": {"containerStatuses": [{"name": "model", "ready": True, "restartCount": 0}]},
    }]}
    metrics = {"items": [{
        "metadata": {"name": "model-0"},
        "containers": [{"name": "model", "usage": {"cpu": "500m", "memory": "1Gi"}}],
    }]}
    report = MODULE.summarize(pods, metrics, "cpucluster-cpu")
    assert report["schema_version"] == "fleet-cluster-resource-v1"
    assert report["max_cpu_percent"] == 25
    assert report["max_memory_percent"] == 50
    assert report["restarts"] == 0
    assert report["all_ready"] is True


def test_summarize_fails_closed_without_bounded_resource_evidence():
    with pytest.raises(RuntimeError, match="no containers"):
        MODULE.summarize({"items": []}, {"items": []}, "missing")
