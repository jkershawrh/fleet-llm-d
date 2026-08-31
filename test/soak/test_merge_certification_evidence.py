import importlib.util
import sys
from pathlib import Path


MODULE_PATH = Path(__file__).with_name("merge_certification_evidence.py")
SPEC = importlib.util.spec_from_file_location("merge_certification_evidence", MODULE_PATH)
MODULE = importlib.util.module_from_spec(SPEC)
assert SPEC.loader
sys.modules[SPEC.name] = MODULE
SPEC.loader.exec_module(MODULE)


def traffic(errors=0, stop_reasons=None):
    return {"schema_version": "fleet-scale-v1", "levels": [{"errors": errors, "stop_reasons": stop_reasons or []}]}


def resource(target, cpu=20, memory=30, restarts=0, ready=True):
    return {"schema_version": "fleet-cluster-resource-v1", "target": target, "max_cpu_percent": cpu,
            "max_memory_percent": memory, "restarts": restarts, "all_ready": ready}


def test_evaluate_passes_only_with_complete_clean_evidence():
    report = MODULE.evaluate(traffic(), [resource("hubcluster"), resource("cpucluster")], {"hubcluster", "cpucluster"}, 70)
    assert report["passed"] is True
    assert report["failures"] == []


def test_evaluate_fails_closed_for_missing_or_unhealthy_provider():
    report = MODULE.evaluate(traffic(), [resource("hubcluster", restarts=1)], {"hubcluster", "cpucluster"}, 70)
    assert report["passed"] is False
    assert "missing resource evidence: cpucluster" in report["failures"]
    assert "hubcluster recorded container restarts" in report["failures"]


def test_evaluate_rejects_traffic_errors_and_guardrail_breach():
    report = MODULE.evaluate(traffic(errors=1), [resource("hubcluster", memory=70)], {"hubcluster"}, 70)
    assert report["passed"] is False
    assert "traffic level 0 recorded errors" in report["failures"]
    assert "hubcluster reached the 70% resource guardrail" in report["failures"]
