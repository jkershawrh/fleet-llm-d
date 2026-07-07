"""Capacity planning for GPU clusters in fleet-llm-d.

Provides tools for computing model hardware requirements, optimizing model
placement across heterogeneous GPU clusters, and forecasting future capacity
needs based on historical usage data.
"""

from __future__ import annotations

import math
from dataclasses import dataclass, field
from datetime import datetime, timedelta

import numpy as np
import pandas as pd
from scipy import optimize, stats


# Standard GPU memory sizes in GB, used when no explicit cluster data is available.
GPU_MEMORY_MAP: dict[str, float] = {
    "H100": 80.0,
    "A100": 80.0,
    "H200": 141.0,
    "B200": 192.0,
    "L40S": 48.0,
}

# Bytes consumed per parameter for each precision format.
PRECISION_BYTES: dict[str, float] = {
    "fp32": 4.0,
    "fp16": 2.0,
    "bf16": 2.0,
    "int8": 1.0,
    "fp8": 1.0,
    "int4": 0.5,
}

# Memory overhead multiplier to account for KV-cache, activations, and runtime buffers.
MEMORY_OVERHEAD: float = 1.2


@dataclass
class ClusterCapacity:
    """Represents the GPU capacity of a single cluster."""

    cluster_id: str
    region: str
    gpu_type: str
    gpu_count: int
    gpu_memory_gb: float
    cost_per_gpu_hour: float
    utilization: float = 0.0


@dataclass
class ModelRequirements:
    """Hardware requirements for serving a single model."""

    model_name: str
    param_size_b: float
    precision: str
    gpu_memory_required_gb: float
    gpus_needed: int
    tensor_parallelism: int
    estimated_throughput_tps: float


@dataclass
class PlacementConstraint:
    """A constraint applied when choosing where to place a model."""

    constraint_type: str  # "region", "gpu_type", or "cost"
    value: str | float


@dataclass
class PlacementPlan:
    """The result of an optimization run that assigns models to clusters."""

    assignments: list[dict[str, str | int]]
    total_cost_per_hour: float
    total_gpus: int


@dataclass
class CapacityForecast:
    """A time-series forecast of future GPU usage with confidence bounds."""

    dates: list[datetime]
    predicted_gpu_usage: list[float]
    confidence_lower: list[float]
    confidence_upper: list[float]


class CapacityPlanner:
    """Plans GPU capacity across a fleet of heterogeneous clusters.

    Provides methods for:
    - Computing the hardware footprint of a model given its size and precision.
    - Optimising the placement of multiple models across clusters to minimise cost.
    - Forecasting future GPU demand from historical usage via linear regression.

    Args:
        clusters: The set of clusters available for placement.
    """

    def __init__(self, clusters: list[ClusterCapacity]) -> None:
        self.clusters = clusters

    # ------------------------------------------------------------------
    # Model requirements
    # ------------------------------------------------------------------

    def compute_model_requirements(
        self,
        model_name: str,
        param_size_b: float,
        precision: str = "fp16",
        target_throughput_tps: float = 1000.0,
        gpu_type: str = "H100",
    ) -> ModelRequirements:
        """Compute the GPU requirements for serving *model_name*.

        The calculation follows a simple but practical heuristic:
        1. Total model memory = params * bytes_per_param * overhead.
        2. GPUs needed = ceil(model_memory / per-GPU memory).
        3. Throughput is divided evenly across GPUs (tensor-parallel).

        Args:
            model_name: Human-readable model identifier.
            param_size_b: Number of parameters in billions.
            precision: Weight format (fp32, fp16, bf16, int8, fp8, int4).
            target_throughput_tps: Desired aggregate tokens per second.
            gpu_type: GPU SKU to size against.

        Returns:
            A :class:`ModelRequirements` instance.

        Raises:
            ValueError: If *precision* or *gpu_type* is not recognised.
        """
        if precision not in PRECISION_BYTES:
            raise ValueError(
                f"Unknown precision '{precision}'. "
                f"Supported: {', '.join(sorted(PRECISION_BYTES))}"
            )

        gpu_memory = GPU_MEMORY_MAP.get(gpu_type)
        if gpu_memory is None:
            raise ValueError(
                f"Unknown gpu_type '{gpu_type}'. "
                f"Supported: {', '.join(sorted(GPU_MEMORY_MAP))}"
            )

        bytes_per_param = PRECISION_BYTES[precision]
        model_memory_gb = param_size_b * bytes_per_param * MEMORY_OVERHEAD

        gpus_needed = max(1, math.ceil(model_memory_gb / gpu_memory))
        tensor_parallelism = gpus_needed
        estimated_throughput = target_throughput_tps / gpus_needed

        return ModelRequirements(
            model_name=model_name,
            param_size_b=param_size_b,
            precision=precision,
            gpu_memory_required_gb=round(model_memory_gb, 2),
            gpus_needed=gpus_needed,
            tensor_parallelism=tensor_parallelism,
            estimated_throughput_tps=round(estimated_throughput, 2),
        )

    # ------------------------------------------------------------------
    # Placement optimisation
    # ------------------------------------------------------------------

    def optimize_placement(
        self,
        models: list[ModelRequirements],
        constraints: list[PlacementConstraint] | None = None,
    ) -> PlacementPlan:
        """Find a minimum-cost assignment of *models* to clusters.

        Uses :func:`scipy.optimize.linprog` to solve a linear programme where
        the decision variables represent the number of replicas of each model
        placed on each cluster.  Constraints enforce that every model gets at
        least one replica and that no cluster is over-subscribed.

        Args:
            models: The models to place.
            constraints: Optional placement constraints to filter clusters.

        Returns:
            A :class:`PlacementPlan` with per-cluster assignments.
        """
        constraints = constraints or []

        # Filter clusters by hard constraints.
        eligible = list(self.clusters)
        for constraint in constraints:
            if constraint.constraint_type == "region":
                eligible = [c for c in eligible if c.region == constraint.value]
            elif constraint.constraint_type == "gpu_type":
                eligible = [c for c in eligible if c.gpu_type == constraint.value]
            elif constraint.constraint_type == "cost":
                max_cost = float(constraint.value)
                eligible = [c for c in eligible if c.cost_per_gpu_hour <= max_cost]

        if not eligible:
            raise ValueError("No clusters satisfy the given constraints.")

        n_models = len(models)
        n_clusters = len(eligible)
        n_vars = n_models * n_clusters  # x_{m,c} = replicas of model m on cluster c

        # Objective: minimise total cost.
        #   cost = sum over (m,c) of x_{m,c} * gpus_needed_m * cost_c
        c_vec = np.zeros(n_vars)
        for m_idx, model in enumerate(models):
            for c_idx, cluster in enumerate(eligible):
                var_idx = m_idx * n_clusters + c_idx
                c_vec[var_idx] = model.gpus_needed * cluster.cost_per_gpu_hour

        # Inequality constraints (A_ub @ x <= b_ub):
        # Each cluster's total GPU usage must not exceed available GPUs.
        A_ub = np.zeros((n_clusters, n_vars))
        b_ub = np.zeros(n_clusters)
        for c_idx, cluster in enumerate(eligible):
            available = cluster.gpu_count - int(cluster.gpu_count * cluster.utilization)
            b_ub[c_idx] = max(available, 0)
            for m_idx, model in enumerate(models):
                var_idx = m_idx * n_clusters + c_idx
                A_ub[c_idx, var_idx] = model.gpus_needed

        # Equality constraints (A_eq @ x == b_eq):
        # Each model must have at least 1 replica total.
        A_eq = np.zeros((n_models, n_vars))
        b_eq = np.ones(n_models)
        for m_idx in range(n_models):
            for c_idx in range(n_clusters):
                var_idx = m_idx * n_clusters + c_idx
                A_eq[m_idx, var_idx] = 1.0

        bounds = [(0, None) for _ in range(n_vars)]

        result = optimize.linprog(
            c_vec,
            A_ub=A_ub,
            b_ub=b_ub,
            A_eq=A_eq,
            b_eq=b_eq,
            bounds=bounds,
            method="highs",
        )

        if not result.success:
            raise RuntimeError(f"Placement optimisation failed: {result.message}")

        # Build assignments from the solution.
        assignments: list[dict[str, str | int]] = []
        total_cost = 0.0
        total_gpus = 0
        for m_idx, model in enumerate(models):
            for c_idx, cluster in enumerate(eligible):
                var_idx = m_idx * n_clusters + c_idx
                replicas = int(round(result.x[var_idx]))
                if replicas > 0:
                    gpus_used = replicas * model.gpus_needed
                    assignments.append(
                        {
                            "model": model.model_name,
                            "cluster_id": cluster.cluster_id,
                            "replicas": replicas,
                            "gpu_type": cluster.gpu_type,
                        }
                    )
                    total_cost += gpus_used * cluster.cost_per_gpu_hour
                    total_gpus += gpus_used

        return PlacementPlan(
            assignments=assignments,
            total_cost_per_hour=round(total_cost, 4),
            total_gpus=total_gpus,
        )

    # ------------------------------------------------------------------
    # Capacity forecasting
    # ------------------------------------------------------------------

    def forecast_capacity(
        self,
        historical_usage: pd.DataFrame,
        horizon_days: int = 30,
        confidence: float = 0.95,
    ) -> CapacityForecast:
        """Forecast GPU demand using ordinary least-squares linear regression.

        Args:
            historical_usage: Must contain columns ``date`` and ``gpu_usage``.
                ``date`` should be convertible to :class:`datetime`.
            horizon_days: Number of days to project forward.
            confidence: Confidence level for prediction interval.

        Returns:
            A :class:`CapacityForecast` with point predictions and bounds.
        """
        df = historical_usage.copy()
        df["date"] = pd.to_datetime(df["date"])
        df = df.sort_values("date").reset_index(drop=True)

        # Encode dates as ordinal day offsets from the first observation.
        origin = df["date"].iloc[0]
        x = np.array([(d - origin).days for d in df["date"]], dtype=float)
        y = df["gpu_usage"].values.astype(float)

        n = len(x)
        slope, intercept, r_value, p_value, std_err = stats.linregress(x, y)

        # Residual standard error.
        y_hat = intercept + slope * x
        residuals = y - y_hat
        s_e = np.sqrt(np.sum(residuals**2) / max(n - 2, 1))

        # Critical t-value for the requested confidence level.
        t_crit = stats.t.ppf((1 + confidence) / 2, df=max(n - 2, 1))

        x_mean = np.mean(x)
        ss_x = np.sum((x - x_mean) ** 2)

        last_day = x[-1]
        future_x = np.arange(last_day + 1, last_day + 1 + horizon_days, dtype=float)

        predicted = intercept + slope * future_x

        # Prediction interval width at each future point.
        margin = t_crit * s_e * np.sqrt(1 + 1 / n + (future_x - x_mean) ** 2 / ss_x)

        future_dates = [
            origin + timedelta(days=int(d)) for d in future_x
        ]

        return CapacityForecast(
            dates=future_dates,
            predicted_gpu_usage=[round(float(v), 2) for v in predicted],
            confidence_lower=[round(float(v), 2) for v in (predicted - margin)],
            confidence_upper=[round(float(v), 2) for v in (predicted + margin)],
        )
