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
    /// Gateway-reachable inference proxy base URL advertised with cluster status.
    inference_url: String,
    /// Accept invalid TLS certificates (for cross-cluster OpenShift Routes).
    /// Only takes effect when no CA cert is configured via `tls_ca_cert`.
    tls_insecure: bool,
    /// Optional path to a PEM-encoded CA certificate file for proper TLS
    /// verification of the control plane.
    tls_ca_cert: Option<String>,
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
            inference_url: String::new(),
            tls_insecure: false,
            tls_ca_cert: None,
            http: reqwest::Client::builder()
                .timeout(std::time::Duration::from_secs(5))
                .build()
                .unwrap_or_else(|e| {
                    tracing::error!(error = %e, "reporter: failed to build default HTTP client");
                    reqwest::Client::default()
                }),
        }
    }

    /// Build an HTTP client with the appropriate TLS configuration.
    /// When a CA cert path is provided, the certificate is loaded and added
    /// as a trusted root. Otherwise, if `insecure` is true, invalid
    /// certificates are accepted.
    fn build_http_client(insecure: bool, ca_cert_path: Option<&str>) -> reqwest::Client {
        let mut builder = reqwest::Client::builder().timeout(std::time::Duration::from_secs(5));

        if let Some(path) = ca_cert_path {
            match std::fs::read(path) {
                Ok(pem_bytes) => match reqwest::Certificate::from_pem(&pem_bytes) {
                    Ok(cert) => {
                        builder = builder.add_root_certificate(cert);
                        tracing::info!(path = %path, "loaded CA certificate for TLS verification");
                    }
                    Err(e) => {
                        tracing::error!(path = %path, error = %e, "failed to parse CA certificate; falling back to system roots");
                    }
                },
                Err(e) => {
                    tracing::error!(path = %path, error = %e, "failed to read CA certificate file; falling back to system roots");
                }
            }
        } else if insecure {
            builder = builder.danger_accept_invalid_certs(true);
        }

        builder.build().unwrap_or_else(|e| {
            tracing::error!(error = %e, "reporter: failed to build HTTP client, falling back to defaults");
            reqwest::Client::default()
        })
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

    /// Advertise the cluster inference proxy URL used by the fleet gateway.
    pub fn with_inference_url(mut self, inference_url: String) -> Self {
        self.inference_url = inference_url;
        self
    }

    /// Set a PEM-encoded CA certificate file for proper TLS verification.
    /// When set, this takes precedence over `tls_insecure`.
    pub fn with_tls_ca_cert(mut self, path: Option<String>) -> Self {
        self.tls_ca_cert = path;
        self
    }

    /// Accept invalid TLS certificates when connecting to the control plane.
    /// Only falls back to `danger_accept_invalid_certs` when no CA cert is
    /// configured via `with_tls_ca_cert`.
    pub fn with_tls_insecure(mut self, insecure: bool) -> Self {
        self.tls_insecure = insecure;
        self.http = Self::build_http_client(insecure, self.tls_ca_cert.as_deref());
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

            let scrape_ok = match self.scrape_metrics().await {
                Ok(metrics) => {
                    if let Err(e) = self.report_metrics(&metrics).await {
                        tracing::warn!(error = %e, "failed to report metrics");
                    }
                    true
                }
                Err(e) => {
                    tracing::warn!(error = %e, "failed to scrape local prometheus");
                    false
                }
            };

            let status = ClusterStatus {
                id: self.cluster_id.clone(),
                name: format!("cluster-{}", self.cluster_id),
                region: String::new(),
                phase: if scrape_ok { "Running" } else { "Degraded" }.to_string(),
                gpu_available: 0,
                gpu_total: 0,
                healthy: scrape_ok,
            };

            if let Err(e) = self.report_status(&status).await {
                tracing::warn!(error = %e, "failed to report status");
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
    let mut pool_saturation = 0.0_f64;
    let mut ready_endpoints = 0_u32;
    let mut kv_cache_utilization = 0.0_f64;
    let mut inflight_requests = 0_u32;

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

        // llm-d EPP native metrics (exact name match)
        if name == "llm_d_epp_flow_control_pool_saturation" {
            pool_saturation = value;
        } else if name == "llm_d_epp_ready_endpoints" {
            ready_endpoints = value.max(0.0) as u32;
        } else if name == "llm_d_epp_average_kv_cache_utilization" {
            kv_cache_utilization = value;
        } else if name == "llm_d_epp_request_running" {
            inflight_requests = value.max(0.0) as u32;
        } else if name == "llm_d_epp_flow_control_queue_size"
            || name == "llm_d_epp_average_queue_size"
        {
            queue_depth = queue_depth.saturating_add(value.max(0.0) as u64);
        } else if name == "llm_d_epp_prefix_indexer_hit_ratio" {
            kv_cache_hit_rate_total += value;
            kv_cache_hit_rate_samples += 1;
        } else if name == "llm_d_epp_request_ttft_seconds" {
            // EPP reports TTFT in seconds; convert to ms for consistency
            let ms = value * 1000.0;
            if raw_name.contains("quantile=\"0.5\"") {
                ttft_p50 = ttft_p50.max(ms);
            } else if raw_name.contains("quantile=\"0.99\"") {
                ttft_p99 = ttft_p99.max(ms);
            }
        } else if name == "llm_d_epp_request_total" {
            throughput += value;
        // Legacy / fuzzy metric matching (backward compatible)
        } else if name.contains("throughput") && !name.ends_with("_total") {
            throughput += value;
        } else if name.contains("ttft") && name.contains("p50") {
            ttft_p50 = ttft_p50.max(value);
        } else if name.contains("ttft") && name.contains("p99") {
            ttft_p99 = ttft_p99.max(value);
        } else if name.contains("queue_depth") {
            queue_depth = queue_depth.saturating_add(value.max(0.0) as u64);
        } else if name.contains("gpu_utilization") || name == "habana_device_utilization" {
            gpu_util_total += value;
            gpu_util_samples += 1;
        } else if name == "habana_device_memory_used_bytes" {
            gpu_util_total += value / (96.0 * 1024.0 * 1024.0 * 1024.0);
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
        pool_saturation,
        ready_endpoints,
        kv_cache_utilization,
        inflight_requests,
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
            "inference_url": self.inference_url,
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
            "pool_saturation": metrics.pool_saturation,
            "ready_endpoints": metrics.ready_endpoints,
            "kv_cache_utilization": metrics.kv_cache_utilization,
            "inflight_requests": metrics.inflight_requests,
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

    #[test]
    fn prometheus_parser_recognizes_epp_metrics() {
        let metrics = parse_prometheus_metrics(
            r#"
            # llm-d EPP native metrics
            llm_d_epp_flow_control_pool_saturation{inference_pool="default"} 0.72
            llm_d_epp_ready_endpoints{name="default"} 4
            llm_d_epp_average_kv_cache_utilization{name="default"} 0.65
            llm_d_epp_request_running{name="default"} 12
            llm_d_epp_flow_control_queue_size{inference_pool="default"} 8
            llm_d_epp_prefix_indexer_hit_ratio{} 0.45
            llm_d_epp_request_ttft_seconds{quantile="0.5"} 0.025
            llm_d_epp_request_ttft_seconds{quantile="0.99"} 0.090
            llm_d_epp_request_total{flow_id="model-a"} 150
            "#,
        );
        assert!((metrics.pool_saturation - 0.72).abs() < 0.01);
        assert_eq!(metrics.ready_endpoints, 4);
        assert!((metrics.kv_cache_utilization - 0.65).abs() < 0.01);
        assert_eq!(metrics.inflight_requests, 12);
        assert_eq!(metrics.queue_depth, 8);
        assert!((metrics.kv_cache_hit_rate - 0.45).abs() < 0.01);
        assert!((metrics.ttft_p50_ms - 25.0).abs() < 0.1);
        assert!((metrics.ttft_p99_ms - 90.0).abs() < 0.1);
        assert!((metrics.throughput_tps - 150.0).abs() < 0.1);
    }

    #[test]
    fn prometheus_parser_recognizes_gaudi_metrics() {
        let metrics = parse_prometheus_metrics(
            r#"
            habana_device_utilization{device="0"} 0.75
            habana_device_utilization{device="1"} 0.85
            inference_throughput_tps 50.0
            inference_ttft_p50_ms 15
            inference_ttft_p99_ms 45
            inference_queue_depth 3
            kv_cache_hit_rate 0.6
            "#,
        );
        assert_eq!(metrics.throughput_tps, 50.0);
        assert!((metrics.gpu_utilization - 0.8).abs() < 0.01);
        assert_eq!(metrics.queue_depth, 3);
    }
}
