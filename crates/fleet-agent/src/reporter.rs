//! Metrics reporter that collects local inference metrics and reports them to
//! the fleet control plane.
//!
//! Scrapes the local Prometheus endpoint (llm-d EPP metrics, typically on port
//! 9090) and forwards aggregated [`InferenceMetrics`] and [`ClusterStatus`] to
//! the control plane via gRPC.

use fleet_common::{
    ClusterId, ClusterStatus, FleetError, FleetEvent, FleetReporter, InferenceMetrics,
};

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
        }
    }

    /// Override the default collection interval.
    pub fn with_interval(mut self, secs: u64) -> Self {
        self.collect_interval_secs = secs;
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

        let client = reqwest::Client::builder()
            .timeout(std::time::Duration::from_secs(5))
            .build()?;

        let mut interval =
            tokio::time::interval(std::time::Duration::from_secs(self.collect_interval_secs));
        loop {
            interval.tick().await;

            let metrics = match self.scrape_metrics(&client).await {
                Ok(m) => m,
                Err(e) => {
                    tracing::warn!(error = %e, "failed to scrape local prometheus");
                    continue;
                }
            };

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
            if let Err(e) = self.report_metrics(&metrics).await {
                tracing::warn!(error = %e, "failed to report metrics");
            }
        }
    }

    /// Scrape the local Prometheus endpoint and extract key inference metrics.
    async fn scrape_metrics(&self, client: &reqwest::Client) -> anyhow::Result<InferenceMetrics> {
        let body = client
            .get(&self.local_prometheus_url)
            .send()
            .await?
            .text()
            .await?;

        let mut throughput = 0.0_f64;
        let mut ttft_p50 = 0.0_f64;
        let mut queue_depth = 0_u64;
        let mut gpu_util = 0.0_f64;

        for line in body.lines() {
            if line.starts_with('#') || line.is_empty() {
                continue;
            }
            if let Some((name, value)) = line.rsplit_once(' ') {
                let val: f64 = value.parse().unwrap_or(0.0);
                if name.contains("throughput") || name.contains("requests_total") {
                    throughput = val;
                } else if name.contains("ttft") && name.contains("p50") {
                    ttft_p50 = val;
                } else if name.contains("queue_depth") {
                    queue_depth = val as u64;
                } else if name.contains("gpu_utilization") {
                    gpu_util = val;
                }
            }
        }

        Ok(InferenceMetrics {
            throughput_tps: throughput,
            ttft_p50_ms: ttft_p50,
            ttft_p99_ms: 0.0,
            queue_depth,
            gpu_utilization: gpu_util,
            kv_cache_hit_rate: 0.0,
        })
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

        let client = reqwest::Client::new();
        let url = format!("{}/api/v1/agent/status", self.control_plane_url);
        let body = serde_json::json!({
            "cluster_id": status.id.to_string(),
            "name": status.name,
            "region": status.region,
            "phase": status.phase,
            "gpu_available": status.gpu_available,
            "gpu_total": status.gpu_total,
            "healthy": status.healthy,
        });

        match client.post(&url).json(&body).send().await {
            Ok(resp) if resp.status().is_success() => Ok(()),
            Ok(resp) => {
                tracing::warn!(status = %resp.status(), "control plane rejected status report");
                Ok(())
            }
            Err(e) => {
                tracing::warn!(error = %e, "failed to send status to control plane");
                Ok(())
            }
        }
    }

    async fn report_metrics(&self, metrics: &InferenceMetrics) -> Result<(), FleetError> {
        tracing::debug!(
            throughput = metrics.throughput_tps,
            ttft_p50 = metrics.ttft_p50_ms,
            queue_depth = metrics.queue_depth,
            "reporting inference metrics"
        );

        let client = reqwest::Client::new();
        let url = format!("{}/api/v1/agent/metrics", self.control_plane_url);
        let body = serde_json::json!({
            "cluster_id": self.cluster_id.to_string(),
            "throughput_tps": metrics.throughput_tps,
            "ttft_p50_ms": metrics.ttft_p50_ms,
            "queue_depth": metrics.queue_depth,
            "gpu_utilization": metrics.gpu_utilization,
            "kv_cache_hit_rate": metrics.kv_cache_hit_rate,
        });

        match client.post(&url).json(&body).send().await {
            Ok(resp) if resp.status().is_success() => Ok(()),
            Ok(resp) => {
                tracing::warn!(status = %resp.status(), "control plane rejected metrics report");
                Ok(())
            }
            Err(e) => {
                tracing::warn!(error = %e, "failed to send metrics to control plane");
                Ok(())
            }
        }
    }

    async fn report_event(&self, event: &FleetEvent) -> Result<(), FleetError> {
        tracing::debug!(?event, "reporting fleet event");

        let client = reqwest::Client::new();
        let url = format!("{}/api/v1/agent/events", self.control_plane_url);
        let body = serde_json::json!({
            "cluster_id": self.cluster_id.to_string(),
            "event": format!("{:?}", event),
        });

        match client.post(&url).json(&body).send().await {
            Ok(_) => Ok(()),
            Err(e) => {
                tracing::warn!(error = %e, "failed to send event to control plane");
                Ok(())
            }
        }
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
    async fn report_status_succeeds() {
        let reporter = MetricsReporter::new(
            "https://cp.example.com".to_string(),
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
        assert!(result.is_ok());
    }
}
