//! Metrics reporter that collects local inference metrics and reports them to
//! the fleet control plane.
//!
//! Scrapes the local Prometheus endpoint (llm-d EPP metrics, typically on port
//! 9090) and forwards aggregated [`InferenceMetrics`] and [`ClusterStatus`] to
//! the control plane via gRPC.

use fleet_common::{
    ClusterId, ClusterStatus, FleetError, FleetEvent, FleetReporter, InferenceMetrics,
};
use serde::Serialize;

/// Interval between metric collection cycles.
const DEFAULT_COLLECT_INTERVAL_SECS: u64 = 15;

/// Reporter that implements [`FleetReporter`] by scraping local Prometheus and
/// forwarding data to the control plane.
#[derive(Debug, Clone)]
pub struct MetricsReporter {
    /// Control plane gRPC endpoint.
    control_plane_url: String,
    /// Identity of the local cluster.
    cluster_id: ClusterId,
    /// URL of the local Prometheus instance to scrape.
    local_prometheus_url: String,
    /// How often to collect and report, in seconds.
    collect_interval_secs: u64,
    /// Optional bearer token used when the control plane enables JWT auth.
    control_plane_token: Option<String>,
    /// Gateway-reachable health endpoint advertised with cluster status.
    health_url: String,
    /// Shared HTTP client with bounded request latency.
    http: reqwest::Client,
}

impl MetricsReporter {
    /// Create a new [`MetricsReporter`].
    pub fn new(
        control_plane_url: String,
        cluster_id: ClusterId,
        local_prometheus_url: String,
    ) -> Self {
        Self {
            control_plane_url,
            cluster_id,
            local_prometheus_url,
            collect_interval_secs: DEFAULT_COLLECT_INTERVAL_SECS,
            control_plane_token: None,
            health_url: String::new(),
            http: reqwest::Client::builder()
                .timeout(std::time::Duration::from_secs(5))
                .build()
                .unwrap_or_default(),
        }
    }

    /// Override the default collection interval.
    pub fn with_interval(mut self, secs: u64) -> Self {
        self.collect_interval_secs = secs;
        self
    }

    /// Configure a bearer token for authenticated control-plane ingestion.
    pub fn with_token(mut self, token: Option<String>) -> Self {
        self.control_plane_token = token.filter(|value| !value.is_empty());
        self
    }

    /// Advertise a health URL that the fleet gateway can probe.
    pub fn with_health_url(mut self, health_url: String) -> Self {
        self.health_url = health_url;
        self
    }

    /// Start the periodic collection loop. Runs until cancelled.
    ///
    /// Each tick: scrape local Prometheus, parse key metrics, build
    /// ClusterStatus and InferenceMetrics, report to control plane via HTTP.
    pub async fn run(&self) -> anyhow::Result<()> {
        tracing::info!(
            cluster_id = %self.cluster_id,
            prometheus = %self.local_prometheus_url,
            interval_secs = self.collect_interval_secs,
            "starting metrics reporter"
        );

        let mut interval =
            tokio::time::interval(std::time::Duration::from_secs(self.collect_interval_secs));
        loop {
            interval.tick().await;

            let status = ClusterStatus {
                id: self.cluster_id.clone(),
                name: format!("cluster-{}", self.cluster_id),
                region: String::new(),
                phase: "Running".to_string(),
                gpu_available: 0,
                gpu_total: 0,
                healthy: true,
            };

            if let Err(e) = self.report_status(&status).await {
                tracing::warn!(error = %e, "failed to report status");
            }

            match self.scrape_metrics().await {
                Ok(metrics) => {
                    if let Err(e) = self.report_metrics(&metrics).await {
                        tracing::warn!(error = %e, "failed to report metrics");
                    }
                }
                Err(e) => {
                    tracing::warn!(error = %e, "failed to scrape local prometheus");
                }
            }
        }
    }

    /// Scrape the local Prometheus endpoint and extract key inference metrics.
    async fn scrape_metrics(&self) -> anyhow::Result<InferenceMetrics> {
        let body = self
            .http
            .get(&self.local_prometheus_url)
            .send()
            .await?
            .error_for_status()?
            .text()
            .await?;

        Ok(parse_prometheus_metrics(&body))
    }

    async fn post_json<T: Serialize>(&self, path: &str, body: &T) -> Result<(), FleetError> {
        let url = format!("{}{}", self.control_plane_url.trim_end_matches('/'), path);
        let mut request = self.http.post(&url).json(body);
        if let Some(token) = &self.control_plane_token {
            request = request.bearer_auth(token);
        }
        let response = request
            .send()
            .await
            .map_err(|error| FleetError::ControlPlaneUnreachable(error.to_string()))?;
        if !response.status().is_success() {
            return Err(FleetError::ControlPlaneUnreachable(format!(
                "{} returned {}",
                path,
                response.status()
            )));
        }
        Ok(())
    }
}

fn parse_prometheus_metrics(body: &str) -> InferenceMetrics {
    let mut throughput = 0.0_f64;
    let mut ttft_p50 = 0.0_f64;
    let mut ttft_p99 = 0.0_f64;
    let mut queue_depth = 0_u64;
    let mut gpu_util_total = 0.0_f64;
    let mut gpu_util_samples = 0_u64;
    let mut kv_cache_hit_rate_total = 0.0_f64;
    let mut kv_cache_hit_rate_samples = 0_u64;

    for line in body.lines() {
        if line.starts_with('#') || line.trim().is_empty() {
            continue;
        }
        let mut fields = line.split_whitespace();
        let Some(raw_name) = fields.next() else {
            continue;
        };
        let Some(raw_value) = fields.next() else {
            continue;
        };
        let Ok(value) = raw_value.parse::<f64>() else {
            continue;
        };
        let name = raw_name.split('{').next().unwrap_or(raw_name);
        if name.contains("throughput") && !name.ends_with("_total") {
            throughput += value;
        } else if name.contains("ttft") && name.contains("p50") {
            ttft_p50 = ttft_p50.max(value);
        } else if name.contains("ttft") && name.contains("p99") {
            ttft_p99 = ttft_p99.max(value);
        } else if name.contains("queue_depth") {
            queue_depth = queue_depth.saturating_add(value.max(0.0) as u64);
        } else if name.contains("gpu_utilization") {
            gpu_util_total += value;
            gpu_util_samples += 1;
        } else if name.contains("kv_cache_hit_rate") {
            kv_cache_hit_rate_total += value;
            kv_cache_hit_rate_samples += 1;
        }
    }

    InferenceMetrics {
        throughput_tps: throughput,
        ttft_p50_ms: ttft_p50,
        ttft_p99_ms: ttft_p99,
        queue_depth,
        gpu_utilization: if gpu_util_samples == 0 {
            0.0
        } else {
            gpu_util_total / gpu_util_samples as f64
        },
        kv_cache_hit_rate: if kv_cache_hit_rate_samples == 0 {
            0.0
        } else {
            kv_cache_hit_rate_total / kv_cache_hit_rate_samples as f64
        },
    }
}

impl FleetReporter for MetricsReporter {
    async fn report_status(&self, status: &ClusterStatus) -> Result<(), FleetError> {
        tracing::debug!(
            cluster_id = %status.id,
            healthy = status.healthy,
            gpus = format!("{}/{}", status.gpu_available, status.gpu_total),
            "reporting cluster status"
        );

        let body = serde_json::json!({
            "cluster_id": status.id.to_string(),
            "name": status.name,
            "region": status.region,
            "phase": status.phase,
            "gpu_available": status.gpu_available,
            "gpu_total": status.gpu_total,
            "healthy": status.healthy,
            "health_url": self.health_url,
        });
        self.post_json("/api/v1/agent/status", &body).await
    }

    async fn report_metrics(&self, metrics: &InferenceMetrics) -> Result<(), FleetError> {
        tracing::debug!(
            throughput = metrics.throughput_tps,
            ttft_p50 = metrics.ttft_p50_ms,
            queue_depth = metrics.queue_depth,
            "reporting inference metrics"
        );

        let body = serde_json::json!({
            "cluster_id": self.cluster_id.to_string(),
            "throughput_tps": metrics.throughput_tps,
            "ttft_p50_ms": metrics.ttft_p50_ms,
            "ttft_p99_ms": metrics.ttft_p99_ms,
            "queue_depth": metrics.queue_depth,
            "gpu_utilization": metrics.gpu_utilization,
            "kv_cache_hit_rate": metrics.kv_cache_hit_rate,
        });
        self.post_json("/api/v1/agent/metrics", &body).await
    }

    async fn report_event(&self, event: &FleetEvent) -> Result<(), FleetError> {
        tracing::debug!(?event, "reporting fleet event");

        let body = serde_json::json!({
            "cluster_id": self.cluster_id.to_string(),
            "event": event,
        });
        self.post_json("/api/v1/agent/events", &body).await
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn reporter_construction() {
        let reporter = MetricsReporter::new(
            "https://cp.example.com".to_string(),
            ClusterId("test".to_string()),
            "http://localhost:9090".to_string(),
        );
        assert_eq!(
            reporter.collect_interval_secs,
            DEFAULT_COLLECT_INTERVAL_SECS
        );
    }

    #[test]
    fn reporter_with_interval() {
        let reporter = MetricsReporter::new(
            "https://cp.example.com".to_string(),
            ClusterId("test".to_string()),
            "http://localhost:9090".to_string(),
        )
        .with_interval(30);
        assert_eq!(reporter.collect_interval_secs, 30);
    }

    #[tokio::test]
    async fn report_status_surfaces_unreachable_control_plane() {
        let reporter = MetricsReporter::new(
            "http://127.0.0.1:1".to_string(),
            ClusterId("test".to_string()),
            "http://localhost:9090".to_string(),
        );
        let status = ClusterStatus {
            id: ClusterId("test".to_string()),
            name: "test-cluster".to_string(),
            region: "us-east-1".to_string(),
            phase: "Running".to_string(),
            gpu_available: 8,
            gpu_total: 8,
            healthy: true,
        };
        let result = reporter.report_status(&status).await;
        assert!(result.is_err());
    }

    #[test]
    fn prometheus_parser_aggregates_gauges_and_ignores_counters() {
        let metrics = parse_prometheus_metrics(
            r#"
            requests_total{model="a"} 1000
            inference_throughput_tps{model="a"} 12.5
            inference_throughput_tps{model="b"} 7.5
            inference_ttft_p50_ms{model="a"} 25
            inference_ttft_p50_ms{model="b"} 40
            inference_ttft_p99_ms{model="a"} 90
            inference_queue_depth{model="a"} 2
            inference_queue_depth{model="b"} 3
            gpu_utilization{gpu="0"} 60
            gpu_utilization{gpu="1"} 80
            kv_cache_hit_rate{model="a"} 0.8
            kv_cache_hit_rate{model="b"} 1.0
            "#,
        );
        assert_eq!(metrics.throughput_tps, 20.0);
        assert_eq!(metrics.ttft_p50_ms, 40.0);
        assert_eq!(metrics.ttft_p99_ms, 90.0);
        assert_eq!(metrics.queue_depth, 5);
        assert_eq!(metrics.gpu_utilization, 70.0);
        assert_eq!(metrics.kv_cache_hit_rate, 0.9);
    }
}
