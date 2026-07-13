"""Anomaly detection for fleet-llm-d inference metrics.

Detects latency spikes, throughput drops, and KV-cache degradation using
z-score based statistical methods with configurable sensitivity.
"""

from __future__ import annotations

from dataclasses import dataclass
from datetime import datetime

import numpy as np
import pandas as pd


@dataclass
class Anomaly:
    """A single detected anomaly."""

    timestamp: datetime
    metric_name: str
    cluster_id: str
    observed_value: float
    expected_value: float
    z_score: float
    severity: str  # "warning" or "critical"


class AnomalyDetector:
    """Statistical anomaly detector for GPU inference telemetry.

    Uses z-score thresholding to flag individual observations that deviate
    significantly from the per-cluster mean.

    Args:
        sensitivity: Number of standard deviations that constitutes an anomaly.
            Defaults to 2.0.
    """

    def __init__(self, sensitivity: float = 2.0) -> None:
        if sensitivity <= 0:
            raise ValueError("Sensitivity must be positive.")
        self.sensitivity = sensitivity

    # ------------------------------------------------------------------
    # Helpers
    # ------------------------------------------------------------------

    def _classify_severity(self, z_score: float) -> str:
        """Return ``'critical'`` when *z_score* exceeds 3x sensitivity, else ``'warning'``."""
        if abs(z_score) > 3 * self.sensitivity:
            return "critical"
        return "warning"

    # ------------------------------------------------------------------
    # Latency anomalies
    # ------------------------------------------------------------------

    def detect_latency_anomalies(self, metrics: pd.DataFrame) -> list[Anomaly]:
        """Detect latency spikes in TTFT and TPOT metrics.

        Args:
            metrics: Must contain columns ``timestamp``, ``cluster_id``,
                ``ttft_ms``, and ``tpot_ms``.

        Returns:
            List of :class:`Anomaly` instances for every observation whose
            z-score exceeds the configured sensitivity.
        """
        anomalies: list[Anomaly] = []
        metrics = metrics.copy()
        metrics["timestamp"] = pd.to_datetime(metrics["timestamp"])

        for metric_col in ("ttft_ms", "tpot_ms"):
            for cluster_id, group in metrics.groupby("cluster_id"):
                values = group[metric_col].values.astype(float)
                mean = np.mean(values)
                std = np.std(values, ddof=1) if len(values) > 1 else 0.0

                if std == 0:
                    continue

                z_scores = (values - mean) / std

                for idx, z in enumerate(z_scores):
                    if abs(z) > self.sensitivity:
                        row = group.iloc[idx]
                        anomalies.append(
                            Anomaly(
                                timestamp=row["timestamp"].to_pydatetime()
                                if hasattr(row["timestamp"], "to_pydatetime")
                                else row["timestamp"],
                                metric_name=metric_col,
                                cluster_id=str(cluster_id),
                                observed_value=float(values[idx]),
                                expected_value=round(float(mean), 4),
                                z_score=round(float(z), 4),
                                severity=self._classify_severity(z),
                            )
                        )

        return anomalies

    # ------------------------------------------------------------------
    # Throughput drops
    # ------------------------------------------------------------------

    def detect_throughput_drops(
        self,
        metrics: pd.DataFrame,
        window: int = 30,
    ) -> list[Anomaly]:
        """Detect sudden throughput drops using a rolling window.

        A drop is flagged when the observed throughput falls more than
        ``sensitivity`` standard deviations below the rolling mean.

        Args:
            metrics: Must contain columns ``timestamp``, ``cluster_id``,
                and ``throughput_tps``.
            window: Rolling window size (number of observations).

        Returns:
            List of detected throughput-drop anomalies.
        """
        anomalies: list[Anomaly] = []
        metrics = metrics.copy()
        metrics["timestamp"] = pd.to_datetime(metrics["timestamp"])

        for cluster_id, group in metrics.groupby("cluster_id"):
            group = group.sort_values("timestamp").reset_index(drop=True)
            values = group["throughput_tps"].values.astype(float)

            if len(values) < window:
                # Not enough data for a rolling window; fall back to global stats.
                mean = np.mean(values)
                std = np.std(values, ddof=1) if len(values) > 1 else 0.0
                if std == 0:
                    continue
                for idx, val in enumerate(values):
                    z = (val - mean) / std
                    if z < -self.sensitivity:
                        row = group.iloc[idx]
                        anomalies.append(
                            Anomaly(
                                timestamp=row["timestamp"].to_pydatetime()
                                if hasattr(row["timestamp"], "to_pydatetime")
                                else row["timestamp"],
                                metric_name="throughput_tps",
                                cluster_id=str(cluster_id),
                                observed_value=float(val),
                                expected_value=round(float(mean), 4),
                                z_score=round(float(z), 4),
                                severity=self._classify_severity(z),
                            )
                        )
                continue

            series = pd.Series(values)
            rolling_mean = series.rolling(window=window, min_periods=window).mean().shift(1).values
            rolling_std = series.rolling(window=window, min_periods=window).std(ddof=1).shift(1).values

            for idx in range(len(values)):
                rm = rolling_mean[idx]
                rs = rolling_std[idx]
                if rm is None or np.isnan(rm) or rs is None or np.isnan(rs) or rs == 0:
                    continue
                z = (values[idx] - rm) / rs
                if z < -self.sensitivity:
                    row = group.iloc[idx]
                    anomalies.append(
                        Anomaly(
                            timestamp=row["timestamp"].to_pydatetime()
                            if hasattr(row["timestamp"], "to_pydatetime")
                            else row["timestamp"],
                            metric_name="throughput_tps",
                            cluster_id=str(cluster_id),
                            observed_value=float(values[idx]),
                            expected_value=round(float(rolling_mean[idx]), 4),
                            z_score=round(float(z), 4),
                            severity=self._classify_severity(z),
                        )
                    )

        return anomalies

    # ------------------------------------------------------------------
    # KV-cache degradation
    # ------------------------------------------------------------------

    def detect_kv_cache_degradation(self, metrics: pd.DataFrame) -> list[Anomaly]:
        """Detect significant drops in KV-cache hit rate.

        A degradation is flagged when the observed hit rate falls more than
        ``sensitivity`` standard deviations below the per-cluster historical
        mean.

        Args:
            metrics: Must contain columns ``timestamp``, ``cluster_id``,
                and ``kv_cache_hit_rate``.

        Returns:
            List of KV-cache degradation anomalies.
        """
        anomalies: list[Anomaly] = []
        metrics = metrics.copy()
        metrics["timestamp"] = pd.to_datetime(metrics["timestamp"])

        for cluster_id, group in metrics.groupby("cluster_id"):
            group = group.sort_values("timestamp").reset_index(drop=True)
            values = group["kv_cache_hit_rate"].values.astype(float)

            mean = np.mean(values)
            std = np.std(values, ddof=1) if len(values) > 1 else 0.0

            if std == 0:
                continue

            z_scores = (values - mean) / std

            for idx, z in enumerate(z_scores):
                # Only flag drops (negative z-scores).
                if z < -self.sensitivity:
                    row = group.iloc[idx]
                    anomalies.append(
                        Anomaly(
                            timestamp=row["timestamp"].to_pydatetime()
                            if hasattr(row["timestamp"], "to_pydatetime")
                            else row["timestamp"],
                            metric_name="kv_cache_hit_rate",
                            cluster_id=str(cluster_id),
                            observed_value=float(values[idx]),
                            expected_value=round(float(mean), 4),
                            z_score=round(float(z), 4),
                            severity=self._classify_severity(z),
                        )
                    )

        return anomalies
