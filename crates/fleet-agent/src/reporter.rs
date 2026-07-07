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
    pub async fn run(&self) -> anyhow::Result<()> {
        tracing::info!(
            cluster_id = %self.cluster_id,
            prometheus = %self.local_prometheus_url,
            interval_secs = self.collect_interval_secs,
            "starting metrics reporter"
        );

        // TODO: implement periodic scrape -> aggregate -> gRPC report loop
        let mut interval =
            tokio::time::interval(std::time::Duration::from_secs(self.collect_interval_secs));
        loop {
            interval.tick().await;
            tracing::debug!("collecting metrics (stub)");
            // TODO: scrape local_prometheus_url, build InferenceMetrics, call
            // report_metrics(). Also build ClusterStatus and call report_status().
        }
    }
}

impl FleetReporter for MetricsReporter {
    async fn report_status(&self, status: &ClusterStatus) -> Result<(), FleetError> {
        tracing::info!(
            cluster_id = %status.id,
            healthy = status.healthy,
            gpus = format!("{}/{}", status.gpu_available, status.gpu_total),
            "reporting cluster status"
        );

        // TODO: send status via gRPC to self.control_plane_url
        Ok(())
    }

    async fn report_metrics(&self, metrics: &InferenceMetrics) -> Result<(), FleetError> {
        tracing::info!(
            throughput = metrics.throughput_tps,
            ttft_p50 = metrics.ttft_p50_ms,
            queue_depth = metrics.queue_depth,
            "reporting inference metrics"
        );

        // TODO: send metrics via gRPC to self.control_plane_url
        Ok(())
    }

    async fn report_event(&self, event: &FleetEvent) -> Result<(), FleetError> {
        tracing::info!(?event, "reporting fleet event");

        // TODO: send event via gRPC to self.control_plane_url
        Ok(())
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
        assert_eq!(reporter.collect_interval_secs, DEFAULT_COLLECT_INTERVAL_SECS);
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
