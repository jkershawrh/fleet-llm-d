"""Tests for fleet_analytics.anomaly_detector."""

from datetime import datetime, timedelta

import numpy as np
import pandas as pd
import pytest

from fleet_analytics.anomaly_detector import AnomalyDetector, Anomaly


@pytest.fixture
def detector() -> AnomalyDetector:
    return AnomalyDetector(sensitivity=2.0)


# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------


def _timestamps(n: int, start: datetime | None = None) -> list[datetime]:
    """Generate *n* evenly-spaced timestamps."""
    start = start or datetime(2025, 6, 1)
    return [start + timedelta(minutes=i) for i in range(n)]


# ---------------------------------------------------------------------------
# Latency anomalies
# ---------------------------------------------------------------------------


class TestDetectLatencyAnomalies:
    """Tests for AnomalyDetector.detect_latency_anomalies."""

    def test_detect_latency_spike(self, detector: AnomalyDetector) -> None:
        """Normal data with a single large TTFT spike should yield at least one anomaly."""
        n = 100
        rng = np.random.default_rng(42)
        ttft = rng.normal(loc=50.0, scale=5.0, size=n)
        tpot = rng.normal(loc=10.0, scale=1.0, size=n)

        # Inject a spike at index 50.
        ttft[50] = 200.0  # well above 50 +/- 10

        df = pd.DataFrame(
            {
                "timestamp": _timestamps(n),
                "cluster_id": ["cluster-a"] * n,
                "ttft_ms": ttft,
                "tpot_ms": tpot,
            }
        )

        anomalies = detector.detect_latency_anomalies(df)

        # At least the injected spike should be detected.
        ttft_anomalies = [a for a in anomalies if a.metric_name == "ttft_ms"]
        assert len(ttft_anomalies) >= 1

        # The spike should correspond to the injected value.
        spike = next(a for a in ttft_anomalies if a.observed_value == pytest.approx(200.0))
        assert spike.z_score > 2.0
        assert spike.severity in ("warning", "critical")

    def test_no_anomalies_in_perfectly_uniform_data(
        self, detector: AnomalyDetector
    ) -> None:
        """Constant data has zero std, so no anomalies should be reported."""
        n = 50
        df = pd.DataFrame(
            {
                "timestamp": _timestamps(n),
                "cluster_id": ["c1"] * n,
                "ttft_ms": [50.0] * n,
                "tpot_ms": [10.0] * n,
            }
        )
        anomalies = detector.detect_latency_anomalies(df)
        assert anomalies == []


# ---------------------------------------------------------------------------
# Throughput drops
# ---------------------------------------------------------------------------


class TestDetectThroughputDrops:
    """Tests for AnomalyDetector.detect_throughput_drops."""

    def test_detect_throughput_drop(self, detector: AnomalyDetector) -> None:
        """A sudden drop in throughput should be flagged."""
        n = 100
        rng = np.random.default_rng(7)
        throughput = rng.normal(loc=1000.0, scale=20.0, size=n)

        # Inject a sharp drop at indices 78-82 (wider window for reliable detection).
        throughput[78:83] = 200.0

        df = pd.DataFrame(
            {
                "timestamp": _timestamps(n),
                "cluster_id": ["cluster-b"] * n,
                "throughput_tps": throughput,
            }
        )

        anomalies = detector.detect_throughput_drops(df)
        assert len(anomalies) >= 1

        drop = next(
            (a for a in anomalies if a.observed_value == pytest.approx(200.0, abs=1.0)),
            None,
        )
        assert drop is not None
        assert drop.z_score < -2.0

    def test_no_anomalies_in_normal_throughput(self, detector: AnomalyDetector) -> None:
        """Stable throughput data should produce no anomalies."""
        n = 100
        rng = np.random.default_rng(99)
        throughput = rng.normal(loc=1000.0, scale=10.0, size=n)

        df = pd.DataFrame(
            {
                "timestamp": _timestamps(n),
                "cluster_id": ["cluster-c"] * n,
                "throughput_tps": throughput,
            }
        )

        anomalies = detector.detect_throughput_drops(df)
        # With tight normal data and default sensitivity=2.0, very few or no
        # false positives should be raised.  We allow up to 2 given randomness.
        assert len(anomalies) <= 2


# ---------------------------------------------------------------------------
# KV-cache degradation
# ---------------------------------------------------------------------------


class TestDetectKvCacheDegradation:
    """Tests for AnomalyDetector.detect_kv_cache_degradation."""

    def test_detect_cache_degradation(self, detector: AnomalyDetector) -> None:
        """A drop in KV-cache hit rate should be detected."""
        n = 80
        rng = np.random.default_rng(123)
        hit_rate = rng.normal(loc=0.95, scale=0.01, size=n)

        # Inject a degradation at index 60.
        hit_rate[60] = 0.50

        df = pd.DataFrame(
            {
                "timestamp": _timestamps(n),
                "cluster_id": ["cluster-d"] * n,
                "kv_cache_hit_rate": hit_rate,
            }
        )

        anomalies = detector.detect_kv_cache_degradation(df)
        assert len(anomalies) >= 1

        degraded = next(
            (a for a in anomalies if a.observed_value == pytest.approx(0.50, abs=0.01)),
            None,
        )
        assert degraded is not None
        assert degraded.z_score < -2.0
        assert degraded.metric_name == "kv_cache_hit_rate"

    def test_no_anomalies_in_normal_data(self, detector: AnomalyDetector) -> None:
        """Stable KV-cache data should produce no anomalies."""
        n = 60
        rng = np.random.default_rng(42)
        hit_rate = rng.normal(loc=0.90, scale=0.01, size=n)
        hit_rate = np.clip(hit_rate, 0.0, 1.0)

        df = pd.DataFrame(
            {
                "timestamp": _timestamps(n),
                "cluster_id": ["cluster-e"] * n,
                "kv_cache_hit_rate": hit_rate,
            }
        )

        anomalies = detector.detect_kv_cache_degradation(df)
        # Allow a small number of false positives due to random sampling.
        assert len(anomalies) <= 2


# ---------------------------------------------------------------------------
# Severity classification
# ---------------------------------------------------------------------------


class TestSeverity:
    """Tests for severity classification logic."""

    def test_critical_severity_at_high_z_score(self) -> None:
        """Z-score > 3*sensitivity should be classified as critical."""
        detector = AnomalyDetector(sensitivity=2.0)
        assert detector._classify_severity(7.0) == "critical"
        assert detector._classify_severity(-7.0) == "critical"

    def test_warning_severity_at_moderate_z_score(self) -> None:
        """Z-score between sensitivity and 3*sensitivity should be warning."""
        detector = AnomalyDetector(sensitivity=2.0)
        assert detector._classify_severity(3.0) == "warning"
        assert detector._classify_severity(-3.0) == "warning"
