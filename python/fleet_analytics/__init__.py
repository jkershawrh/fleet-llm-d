"""Fleet Analytics - GPU capacity planning, cost optimization, and anomaly detection for fleet-llm-d."""

__version__ = "0.1.0"

from fleet_analytics.capacity_planner import (
    CapacityPlanner,
    ClusterCapacity,
    ModelRequirements,
    PlacementConstraint,
    PlacementPlan,
    CapacityForecast,
)
from fleet_analytics.cost_optimizer import (
    CostOptimizer,
    ClusterCost,
    RoutingWeights,
    ChargebackReport,
)
from fleet_analytics.anomaly_detector import AnomalyDetector, Anomaly
