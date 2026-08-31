"""Tests for fleet_analytics.cost_optimizer."""

import pandas as pd
import pytest

from fleet_analytics.cost_optimizer import (
    BASE_GPU_COSTS,
    TOKENS_PER_GPU_HOUR_FULL_UTIL,
    ChargebackReport,
    ClusterCost,
    CostOptimizer,
)


@pytest.fixture
def optimizer() -> CostOptimizer:
    return CostOptimizer()


# ---------------------------------------------------------------------------
# Per-token cost
# ---------------------------------------------------------------------------


class TestComputeCostPerToken:
    """Tests for CostOptimizer.compute_cost_per_token."""

    def test_cost_per_token_h100_full_utilization(self, optimizer: CostOptimizer) -> None:
        """At 100 % utilisation the cost should be base_cost / tokens_per_hour."""
        cost = optimizer.compute_cost_per_token("cluster-1", "H100", 1.0)
        expected = BASE_GPU_COSTS["H100"] / TOKENS_PER_GPU_HOUR_FULL_UTIL
        assert cost == pytest.approx(expected, rel=1e-6)

    def test_cost_per_token_a100_half_utilization(self, optimizer: CostOptimizer) -> None:
        """Halving utilisation should double the per-token cost."""
        full = optimizer.compute_cost_per_token("c", "A100", 1.0)
        half = optimizer.compute_cost_per_token("c", "A100", 0.5)
        assert half == pytest.approx(2.0 * full, rel=1e-6)

    def test_cost_per_token_various_utilizations(self, optimizer: CostOptimizer) -> None:
        """Cost should decrease monotonically as utilisation increases."""
        costs = [
            optimizer.compute_cost_per_token("c", "H100", util)
            for util in [0.25, 0.5, 0.75, 1.0]
        ]
        for i in range(len(costs) - 1):
            assert costs[i] > costs[i + 1]

    def test_zero_utilization_raises(self, optimizer: CostOptimizer) -> None:
        with pytest.raises(ValueError, match="greater than 0"):
            optimizer.compute_cost_per_token("c", "H100", 0.0)

    def test_unknown_gpu_type_raises(self, optimizer: CostOptimizer) -> None:
        with pytest.raises(ValueError, match="Unknown gpu_type"):
            optimizer.compute_cost_per_token("c", "RTX_4090", 0.5)


# ---------------------------------------------------------------------------
# Cost-aware routing
# ---------------------------------------------------------------------------


class TestOptimizeRoutingForCost:
    """Tests for CostOptimizer.optimize_routing_for_cost."""

    def test_optimize_routing_for_cost(self, optimizer: CostOptimizer) -> None:
        """Cheaper cluster should receive a higher routing weight."""
        clusters = [
            ClusterCost("expensive", "H100", 3.50, 0.8, 64),
            ClusterCost("cheap", "A100", 2.10, 0.8, 64),
        ]
        result = optimizer.optimize_routing_for_cost(["model-a"], clusters)

        assert len(result.weights) == 2
        assert sum(result.weights.values()) == pytest.approx(1.0, abs=1e-4)
        # A100 is cheaper per token, so it should get higher weight.
        assert result.weights["cheap"] > result.weights["expensive"]

    def test_routing_weights_sum_to_one(self, optimizer: CostOptimizer) -> None:
        clusters = [
            ClusterCost("a", "H100", 3.50, 0.5, 32),
            ClusterCost("b", "A100", 2.10, 0.7, 64),
            ClusterCost("c", "L40S", 1.80, 0.9, 128),
        ]
        result = optimizer.optimize_routing_for_cost(["m1", "m2"], clusters)
        assert sum(result.weights.values()) == pytest.approx(1.0, abs=1e-4)

    def test_routing_excludes_zero_utilization(self, optimizer: CostOptimizer) -> None:
        """Clusters with zero utilisation should be excluded."""
        clusters = [
            ClusterCost("active", "H100", 3.50, 0.5, 32),
            ClusterCost("idle", "A100", 2.10, 0.0, 64),
        ]
        result = optimizer.optimize_routing_for_cost(["m"], clusters)
        assert "idle" not in result.weights
        assert result.weights["active"] == pytest.approx(1.0, abs=1e-4)


# ---------------------------------------------------------------------------
# Chargeback report
# ---------------------------------------------------------------------------


class TestChargebackReport:
    """Tests for CostOptimizer.generate_chargeback_report."""

    def test_chargeback_report(self, optimizer: CostOptimizer) -> None:
        """Multi-tenant usage should produce correct per-tenant costs."""
        usage = pd.DataFrame(
            {
                "tenant_id": ["t1", "t1", "t2"],
                "cluster_id": ["c1", "c2", "c1"],
                "gpu_type": ["H100", "A100", "H100"],
                "gpu_hours": [10.0, 5.0, 20.0],
                "tokens_consumed": [1_000_000, 500_000, 2_000_000],
            }
        )
        pricing = {"H100": 3.50, "A100": 2.10}

        report = optimizer.generate_chargeback_report(usage, pricing, period="2025-01")

        assert report.period == "2025-01"
        assert len(report.tenant_costs) == 2

        t1 = next(tc for tc in report.tenant_costs if tc["tenant_id"] == "t1")
        t2 = next(tc for tc in report.tenant_costs if tc["tenant_id"] == "t2")

        # t1: 10 * 3.50 + 5 * 2.10 = 35 + 10.5 = 45.5
        assert t1["cost"] == pytest.approx(45.5, rel=1e-4)
        # t2: 20 * 3.50 = 70
        assert t2["cost"] == pytest.approx(70.0, rel=1e-4)

        assert report.total_cost == pytest.approx(45.5 + 70.0, rel=1e-4)

    def test_chargeback_empty_usage(self, optimizer: CostOptimizer) -> None:
        """An empty usage frame should produce an empty report."""
        usage = pd.DataFrame(
            columns=["tenant_id", "cluster_id", "gpu_type", "gpu_hours", "tokens_consumed"]
        )
        pricing = {"H100": 3.50}
        report = optimizer.generate_chargeback_report(usage, pricing)
        assert report.total_cost == 0.0
        assert len(report.tenant_costs) == 0
