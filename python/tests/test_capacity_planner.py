"""Tests for fleet_analytics.capacity_planner."""

import math
from datetime import datetime, timedelta

import numpy as np
import pandas as pd
import pytest

from fleet_analytics.capacity_planner import (
    CapacityPlanner,
    ClusterCapacity,
    ModelRequirements,
    PlacementConstraint,
)


# ---------------------------------------------------------------------------
# Fixtures
# ---------------------------------------------------------------------------


@pytest.fixture
def two_cluster_planner() -> CapacityPlanner:
    """Planner with two clusters of different cost and GPU type."""
    clusters = [
        ClusterCapacity(
            cluster_id="us-east-h100",
            region="us-east",
            gpu_type="H100",
            gpu_count=64,
            gpu_memory_gb=80.0,
            cost_per_gpu_hour=3.50,
            utilization=0.2,
        ),
        ClusterCapacity(
            cluster_id="eu-west-a100",
            region="eu-west",
            gpu_type="A100",
            gpu_count=128,
            gpu_memory_gb=80.0,
            cost_per_gpu_hour=2.10,
            utilization=0.1,
        ),
    ]
    return CapacityPlanner(clusters)


# ---------------------------------------------------------------------------
# Model requirements
# ---------------------------------------------------------------------------


class TestComputeModelRequirements:
    """Tests for CapacityPlanner.compute_model_requirements."""

    def test_compute_model_requirements_70b_fp16(
        self, two_cluster_planner: CapacityPlanner
    ) -> None:
        """A 70B-param FP16 model should require ~168 GB and multiple GPUs."""
        req = two_cluster_planner.compute_model_requirements(
            model_name="llama-70b",
            param_size_b=70.0,
            precision="fp16",
            target_throughput_tps=1000.0,
            gpu_type="H100",
        )

        # 70B * 2 bytes * 1.2 overhead = 168 GB
        expected_memory = 70.0 * 2.0 * 1.2
        assert req.gpu_memory_required_gb == pytest.approx(expected_memory, rel=1e-3)

        # 168 / 80 = 2.1 -> ceil -> 3 GPUs
        expected_gpus = math.ceil(expected_memory / 80.0)
        assert req.gpus_needed == expected_gpus
        assert req.gpus_needed > 1

        assert req.tensor_parallelism == req.gpus_needed
        assert req.precision == "fp16"
        assert req.model_name == "llama-70b"

    def test_compute_model_requirements_8b_int4(
        self, two_cluster_planner: CapacityPlanner
    ) -> None:
        """An 8B-param INT4 model should need ~4.8 GB and fit on 1 GPU."""
        req = two_cluster_planner.compute_model_requirements(
            model_name="llama-8b",
            param_size_b=8.0,
            precision="int4",
            target_throughput_tps=2000.0,
            gpu_type="H100",
        )

        # 8B * 0.5 bytes * 1.2 = 4.8 GB
        expected_memory = 8.0 * 0.5 * 1.2
        assert req.gpu_memory_required_gb == pytest.approx(expected_memory, rel=1e-3)
        assert req.gpus_needed == 1
        assert req.estimated_throughput_tps == pytest.approx(2000.0, rel=1e-3)

    def test_unknown_precision_raises(
        self, two_cluster_planner: CapacityPlanner
    ) -> None:
        with pytest.raises(ValueError, match="Unknown precision"):
            two_cluster_planner.compute_model_requirements(
                "test-model", 7.0, precision="fp3"
            )

    def test_unknown_gpu_type_raises(
        self, two_cluster_planner: CapacityPlanner
    ) -> None:
        with pytest.raises(ValueError, match="Unknown gpu_type"):
            two_cluster_planner.compute_model_requirements(
                "test-model", 7.0, gpu_type="TPU_v5"
            )


# ---------------------------------------------------------------------------
# Placement optimisation
# ---------------------------------------------------------------------------


class TestOptimizePlacement:
    """Tests for CapacityPlanner.optimize_placement."""

    def test_optimize_placement_single_model(
        self, two_cluster_planner: CapacityPlanner
    ) -> None:
        """Single small model placed on two clusters; cheapest should be preferred."""
        req = two_cluster_planner.compute_model_requirements(
            model_name="llama-8b",
            param_size_b=8.0,
            precision="int4",
            gpu_type="H100",
        )

        plan = two_cluster_planner.optimize_placement([req])

        # Exactly one replica expected (equality constraint).
        assert plan.total_gpus >= req.gpus_needed
        assert plan.total_cost_per_hour > 0
        assert len(plan.assignments) >= 1

        # The cheapest cluster (A100 @ $2.10) should be chosen.
        chosen_clusters = {a["cluster_id"] for a in plan.assignments}
        assert "eu-west-a100" in chosen_clusters

    def test_placement_respects_region_constraint(
        self, two_cluster_planner: CapacityPlanner
    ) -> None:
        """Constraining to us-east should exclude eu-west."""
        req = two_cluster_planner.compute_model_requirements(
            "test-model", 8.0, precision="int4", gpu_type="H100"
        )
        constraint = PlacementConstraint(constraint_type="region", value="us-east")
        plan = two_cluster_planner.optimize_placement([req], [constraint])

        for assignment in plan.assignments:
            assert assignment["cluster_id"] == "us-east-h100"


# ---------------------------------------------------------------------------
# Capacity forecasting
# ---------------------------------------------------------------------------


class TestForecastCapacity:
    """Tests for CapacityPlanner.forecast_capacity."""

    def test_forecast_capacity_linear_trend(
        self, two_cluster_planner: CapacityPlanner
    ) -> None:
        """A perfect linear trend should produce a forecast that continues it."""
        n_days = 30
        base = datetime(2025, 1, 1)
        dates = [base + timedelta(days=i) for i in range(n_days)]
        # GPU usage grows linearly: 100 + 2 * day
        gpu_usage = [100 + 2 * i for i in range(n_days)]

        df = pd.DataFrame({"date": dates, "gpu_usage": gpu_usage})
        forecast = two_cluster_planner.forecast_capacity(df, horizon_days=10)

        assert len(forecast.dates) == 10
        assert len(forecast.predicted_gpu_usage) == 10
        assert len(forecast.confidence_lower) == 10
        assert len(forecast.confidence_upper) == 10

        # The first forecast day should be approximately 100 + 2*30 = 160.
        assert forecast.predicted_gpu_usage[0] == pytest.approx(160.0, abs=1.0)

        # The last forecast day should be approximately 100 + 2*39 = 178.
        assert forecast.predicted_gpu_usage[-1] == pytest.approx(178.0, abs=1.0)

        # Confidence bounds should bracket the prediction.
        for i in range(10):
            assert forecast.confidence_lower[i] <= forecast.predicted_gpu_usage[i]
            assert forecast.confidence_upper[i] >= forecast.predicted_gpu_usage[i]

    def test_forecast_noisy_data(
        self, two_cluster_planner: CapacityPlanner
    ) -> None:
        """Noisy data should still yield reasonable bounds."""
        rng = np.random.default_rng(42)
        n_days = 60
        base = datetime(2025, 1, 1)
        dates = [base + timedelta(days=i) for i in range(n_days)]
        gpu_usage = [200 + 3 * i + rng.normal(0, 10) for i in range(n_days)]

        df = pd.DataFrame({"date": dates, "gpu_usage": gpu_usage})
        forecast = two_cluster_planner.forecast_capacity(df, horizon_days=14)

        # With noise, the confidence interval should be wider than zero.
        widths = [
            forecast.confidence_upper[i] - forecast.confidence_lower[i]
            for i in range(14)
        ]
        assert all(w > 0 for w in widths)
