"""Common test fixtures for fleet-analytics."""

import pytest


@pytest.fixture
def sample_cluster_data():
    """Sample cluster inventory matching the fleet-api.yaml ClusterInfo schema."""
    return [
        {
            "id": "cluster-us-east-1a",
            "name": "us-east-prod",
            "region": "us-east-1",
            "gpu_capacity": {"available": 6, "total": 8, "types": ["A100"]},
            "utilization": 0.75,
        },
        {
            "id": "cluster-eu-west-1b",
            "name": "eu-west-staging",
            "region": "eu-west-1",
            "gpu_capacity": {"available": 4, "total": 4, "types": ["H100"]},
            "utilization": 0.0,
        },
    ]


@pytest.fixture
def sample_tenant_usage():
    """Sample tenant usage record matching the TenantUsage schema."""
    return {
        "tenant_id": "tenant-acme",
        "tokens_consumed": 1_250_000,
        "total_cost": "42.50",
        "request_count": 8700,
        "avg_latency_ms": 320,
    }


@pytest.fixture
def sample_model_metrics():
    """Sample per-model metrics matching the ModelMetrics schema."""
    return {
        "model": "granite-3b",
        "clusters": ["cluster-us-east-1a", "cluster-eu-west-1b"],
        "throughput": 4200.0,
        "ttft_p50_ms": 62.1,
        "ttft_p95_ms": 145.8,
        "ttft_p99_ms": 310.2,
        "cache_hit_rate": 0.73,
    }


@pytest.fixture
def sample_fleet_metrics():
    """Sample fleet-wide metrics matching the FleetMetrics schema."""
    return {
        "total_gpus": 64,
        "active_models": 5,
        "total_throughput": 12500.0,
        "avg_ttft_ms": 85.3,
    }


@pytest.fixture
def sample_time_series():
    """Sample time-series data for analytics testing. Requires numpy."""
    np = pytest.importorskip("numpy")
    rng = np.random.default_rng(42)
    n = 100
    timestamps = np.arange(n, dtype=float)
    values = 50.0 + 10.0 * np.sin(timestamps / 10.0) + rng.normal(0, 2, n)
    return {"timestamps": timestamps, "values": values}
