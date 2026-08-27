import importlib.util
import sys
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


def test_level_summary_includes_routing_and_resource_evidence():
    result = MODULE.LevelResult(
        concurrency=2,
        duration_seconds=2,
        requests=4,
        completion_tokens=8,
        latencies_ms=[10, 20, 30, 40],
        routed_counts={"arena": 2, "oberon": 2},
        data_plane_counts={"llm-d-router": 4},
        resources=[MODULE.ResourceSample(target="oberon", cpu_percent=20, memory_percent=30)],
    )
    summary = result.summary()
    assert summary["rps"] == 2
    assert summary["tokens_per_second"] == 4
    assert summary["routed_counts"] == {"arena": 2, "oberon": 2}
    assert summary["resources"][0]["target"] == "oberon"
