"""Cost optimisation and chargeback reporting for fleet-llm-d GPU clusters.

Provides per-token cost calculations, cost-aware routing weight computation,
and multi-tenant chargeback report generation.
"""

from __future__ import annotations

from dataclasses import dataclass, field

import numpy as np
import pandas as pd


# Baseline cost per GPU-hour for each SKU (USD).
BASE_GPU_COSTS: dict[str, float] = {
    "H100": 3.50,
    "A100": 2.10,
    "H200": 4.50,
    "L40S": 1.80,
}

# Approximate tokens generated per GPU-hour at 100 % utilisation.
TOKENS_PER_GPU_HOUR_FULL_UTIL: float = 3_600_000.0  # 3600s * 1000 tok/s


@dataclass
class ClusterCost:
    """Cost and utilisation snapshot for a single cluster."""

    cluster_id: str
    gpu_type: str
    cost_per_gpu_hour: float
    current_utilization: float
    capacity: int  # total GPU count


@dataclass
class RoutingWeights:
    """Normalised routing weights that steer traffic toward cheaper clusters."""

    weights: dict[str, float]
    estimated_cost_per_hour: float


@dataclass
class ChargebackReport:
    """Per-tenant cost breakdown for a billing period."""

    tenant_costs: list[dict[str, str | float]]
    total_cost: float
    period: str


class CostOptimizer:
    """Optimises inference cost across a heterogeneous GPU fleet.

    Methods cover three concerns:

    1. **Per-token costing** -- translates GPU-hour pricing and utilisation into
       a per-token cost metric suitable for dashboards and billing.
    2. **Cost-aware routing** -- produces normalised weights that a load-balancer
       can use to prefer cheaper clusters.
    3. **Chargeback reporting** -- aggregates per-tenant GPU-hour and token
       consumption into a cost report.
    """

    def __init__(self) -> None:
        pass

    # ------------------------------------------------------------------
    # Per-token cost
    # ------------------------------------------------------------------

    def compute_cost_per_token(
        self,
        cluster_id: str,
        gpu_type: str,
        utilization: float,
    ) -> float:
        """Return the estimated cost per token (USD) for *cluster_id*.

        The formula is::

            cost_per_token = base_cost_per_gpu_hour
                             / (tokens_per_gpu_hour * utilization)

        Args:
            cluster_id: Identifier of the cluster (used for logging/context).
            gpu_type: GPU SKU name (must be a key in :data:`BASE_GPU_COSTS`).
            utilization: Current GPU utilisation as a fraction in (0, 1].

        Returns:
            Cost per token in USD.

        Raises:
            ValueError: If *gpu_type* is unknown or *utilization* is non-positive.
        """
        if gpu_type not in BASE_GPU_COSTS:
            raise ValueError(
                f"Unknown gpu_type '{gpu_type}'. "
                f"Supported: {', '.join(sorted(BASE_GPU_COSTS))}"
            )

        if utilization <= 0:
            raise ValueError("Utilization must be greater than 0.")

        base_cost = BASE_GPU_COSTS[gpu_type]
        tokens_per_hour = TOKENS_PER_GPU_HOUR_FULL_UTIL * utilization
        cost_per_token = base_cost / tokens_per_hour

        return cost_per_token

    # ------------------------------------------------------------------
    # Cost-aware routing
    # ------------------------------------------------------------------

    def optimize_routing_for_cost(
        self,
        models: list[str],
        clusters: list[ClusterCost],
    ) -> RoutingWeights:
        """Compute normalised routing weights that favour cheaper clusters.

        The weight for each cluster is proportional to the inverse of its
        effective cost-per-token.  Clusters with zero or negative available
        capacity are excluded.

        Args:
            models: Model names being served (used for context; all clusters
                are assumed to serve all listed models).
            clusters: Current cost and utilisation snapshot per cluster.

        Returns:
            A :class:`RoutingWeights` instance with weights summing to 1.0.
        """
        efficiencies: dict[str, float] = {}
        for cluster in clusters:
            if cluster.current_utilization <= 0 or cluster.capacity <= 0:
                continue
            cost_per_token = self.compute_cost_per_token(
                cluster.cluster_id,
                cluster.gpu_type,
                cluster.current_utilization,
            )
            # Inverse cost: cheaper clusters get a higher score.
            efficiencies[cluster.cluster_id] = 1.0 / cost_per_token

        if not efficiencies:
            raise ValueError("No clusters with positive utilisation and capacity.")

        total_efficiency = sum(efficiencies.values())
        weights = {
            cid: round(eff / total_efficiency, 6)
            for cid, eff in efficiencies.items()
        }

        # Estimate total fleet cost per hour (weighted by routing share).
        estimated_cost = 0.0
        cluster_map = {c.cluster_id: c for c in clusters}
        for cid, weight in weights.items():
            c = cluster_map[cid]
            # Cost contribution = weight * base_cost * capacity (active GPUs).
            estimated_cost += weight * BASE_GPU_COSTS.get(c.gpu_type, 0.0) * c.capacity

        return RoutingWeights(
            weights=weights,
            estimated_cost_per_hour=round(estimated_cost, 4),
        )

    # ------------------------------------------------------------------
    # Chargeback reporting
    # ------------------------------------------------------------------

    def generate_chargeback_report(
        self,
        tenant_usage: pd.DataFrame,
        pricing: dict[str, float],
        period: str = "monthly",
    ) -> ChargebackReport:
        """Generate a per-tenant cost report from usage data.

        Args:
            tenant_usage: Must contain columns ``tenant_id``, ``cluster_id``,
                ``gpu_hours``, and ``tokens_consumed``.  Each row represents a
                usage record for one tenant on one cluster.
            pricing: Maps GPU type name to cost-per-GPU-hour.  The cluster's GPU
                type is inferred from the ``cluster_id`` column using the
                ``gpu_type`` column if present; otherwise the first matching key
                in *pricing* is used.
            period: Human-readable billing period label.

        Returns:
            A :class:`ChargebackReport`.
        """
        tenant_costs: list[dict[str, str | float]] = []
        total_cost = 0.0

        # If the dataframe has a gpu_type column, use it for pricing lookup.
        has_gpu_type = "gpu_type" in tenant_usage.columns

        grouped = tenant_usage.groupby("tenant_id")
        for tenant_id, group in grouped:
            tenant_gpu_hours = 0.0
            tenant_tokens = 0
            tenant_cost = 0.0

            for _, row in group.iterrows():
                gpu_hours = float(row["gpu_hours"])
                tokens = int(row["tokens_consumed"])

                if has_gpu_type:
                    gpu_type = str(row["gpu_type"])
                    rate = pricing.get(gpu_type, 0.0)
                else:
                    # Fallback: use the first pricing entry as a default.
                    rate = next(iter(pricing.values()), 0.0)

                row_cost = gpu_hours * rate
                tenant_gpu_hours += gpu_hours
                tenant_tokens += tokens
                tenant_cost += row_cost

            tenant_costs.append(
                {
                    "tenant_id": str(tenant_id),
                    "gpu_hours": round(tenant_gpu_hours, 4),
                    "tokens_consumed": tenant_tokens,
                    "cost": round(tenant_cost, 4),
                }
            )
            total_cost += tenant_cost

        return ChargebackReport(
            tenant_costs=tenant_costs,
            total_cost=round(total_cost, 4),
            period=period,
        )
