//! Cluster health checker.
//!
//! Periodically probes cluster endpoints to assess reachability and latency,
//! maintaining an in-memory health map used by the router and balancer.

use std::collections::HashMap;
use std::sync::Arc;
use std::time::Duration;

use chrono::{DateTime, Utc};
use fleet_common::ClusterId;
use serde::{Deserialize, Serialize};
use tokio::sync::RwLock;

/// Health state of a single cluster as observed by the gateway.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct ClusterHealth {
    /// Cluster being monitored.
    pub cluster_id: ClusterId,
    /// Whether the cluster is considered healthy.
    pub healthy: bool,
    /// Last observed round-trip latency in milliseconds.
    pub latency_ms: f64,
    /// Timestamp of the most recent health check.
    pub last_check: DateTime<Utc>,
    /// Number of consecutive probe failures.
    pub consecutive_failures: u32,
}

/// Configuration for declaring a cluster unhealthy.
#[derive(Debug, Clone)]
pub struct HealthPolicy {
    /// Number of consecutive failures before marking unhealthy.
    pub failure_threshold: u32,
    /// Timeout for each probe request.
    pub probe_timeout: Duration,
}

/// Gateway-reachable endpoints advertised by a cluster agent.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct ClusterEndpoint {
    /// Endpoint used to decide whether the inference proxy is ready.
    pub health_url: String,
    /// Base URL used to forward inference traffic.
    pub inference_url: String,
}

impl Default for HealthPolicy {
    fn default() -> Self {
        Self {
            failure_threshold: 3,
            probe_timeout: Duration::from_secs(5),
        }
    }
}

/// Periodically probes cluster health endpoints and maintains a health map.
#[derive(Debug, Clone)]
pub struct HealthChecker {
    /// Interval between health probes.
    interval: Duration,
    /// Health policy configuration.
    policy: HealthPolicy,
    /// Current health state of all known clusters.
    state: Arc<RwLock<HashMap<ClusterId, ClusterHealth>>>,
    /// Map of cluster ID to its health and inference endpoints.
    endpoints: Arc<RwLock<HashMap<ClusterId, ClusterEndpoint>>>,
}

impl HealthChecker {
    /// Create a new [`HealthChecker`] with the given probe interval.
    pub fn new(interval: Duration) -> Self {
        Self {
            interval,
            policy: HealthPolicy::default(),
            state: Arc::new(RwLock::new(HashMap::new())),
            endpoints: Arc::new(RwLock::new(HashMap::new())),
        }
    }

    /// Get a snapshot of the current health state for all clusters.
    pub async fn snapshot(&self) -> HashMap<ClusterId, ClusterHealth> {
        self.state.read().await.clone()
    }

    /// Register a cluster with health and inference endpoints.
    pub async fn register_cluster(
        &self,
        cluster_id: ClusterId,
        health_url: String,
        inference_url: String,
    ) {
        let has_endpoint = !health_url.is_empty() && !inference_url.is_empty();
        let endpoint = ClusterEndpoint {
            health_url,
            inference_url,
        };
        let endpoint_changed = if has_endpoint {
            let previous = self
                .endpoints
                .write()
                .await
                .insert(cluster_id.clone(), endpoint.clone());
            previous.as_ref() != Some(&endpoint)
        } else {
            self.endpoints.write().await.remove(&cluster_id);
            false
        };
        let mut state = self.state.write().await;
        let health = state
            .entry(cluster_id.clone())
            .or_insert_with(|| ClusterHealth {
                cluster_id: cluster_id.clone(),
                healthy: false,
                latency_ms: 0.0,
                last_check: Utc::now(),
                consecutive_failures: 0,
            });
        if endpoint_changed {
            health.healthy = false;
            health.consecutive_failures = 0;
        }
    }

    /// Remove a cluster from monitoring.
    pub async fn unregister_cluster(&self, cluster_id: &ClusterId) {
        self.endpoints.write().await.remove(cluster_id);
        self.state.write().await.remove(cluster_id);
    }

    /// Return routable endpoints whose latest readiness probe succeeded.
    pub async fn healthy_inference_endpoints(&self) -> Vec<(ClusterId, String)> {
        let endpoints = self.endpoints.read().await.clone();
        let state = self.state.read().await;
        let mut healthy: Vec<_> = endpoints
            .into_iter()
            .filter_map(|(cluster_id, endpoint)| {
                state
                    .get(&cluster_id)
                    .filter(|health| health.healthy)
                    .map(|health| (health.latency_ms, cluster_id, endpoint.inference_url))
            })
            .collect();
        healthy.sort_by(|left, right| {
            left.0
                .partial_cmp(&right.0)
                .unwrap_or(std::cmp::Ordering::Equal)
        });
        healthy
            .into_iter()
            .map(|(_, cluster_id, inference_url)| (cluster_id, inference_url))
            .collect()
    }

    /// Start the periodic health check loop. Runs until cancelled.
    pub async fn run(&self) -> anyhow::Result<()> {
        tracing::info!(
            interval_ms = self.interval.as_millis() as u64,
            failure_threshold = self.policy.failure_threshold,
            probe_timeout_ms = self.policy.probe_timeout.as_millis() as u64,
            "starting health checker"
        );

        let mut ticker = tokio::time::interval(self.interval);
        loop {
            ticker.tick().await;

            self.probe_once().await;
        }
    }

    /// Probe every registered health endpoint once.
    pub async fn probe_once(&self) {
        let cluster_ids: Vec<ClusterId> = { self.state.read().await.keys().cloned().collect() };

        let endpoints = self.endpoints.read().await.clone();
        let client = reqwest::Client::builder()
            .timeout(self.policy.probe_timeout)
            .build()
            .unwrap_or_default();

        for cluster_id in &cluster_ids {
            let url = match endpoints.get(cluster_id) {
                Some(endpoint) if !endpoint.health_url.is_empty() => endpoint.health_url.clone(),
                _ => {
                    tracing::debug!(cluster = %cluster_id, "no endpoint configured, skipping probe");
                    continue;
                }
            };

            let start = std::time::Instant::now();
            let probe_result = client.get(&url).send().await;
            let elapsed_ms = start.elapsed().as_secs_f64() * 1000.0;

            let mut state = self.state.write().await;
            if let Some(health) = state.get_mut(cluster_id) {
                health.last_check = Utc::now();
                match probe_result {
                    Ok(resp) if resp.status().is_success() => {
                        health.latency_ms = elapsed_ms;
                        health.consecutive_failures = 0;
                        if !health.healthy {
                            tracing::info!(cluster = %cluster_id, latency_ms = elapsed_ms, "cluster recovered");
                        }
                        health.healthy = true;
                    }
                    Ok(resp) => {
                        health.consecutive_failures += 1;
                        tracing::warn!(
                            cluster = %cluster_id,
                            status = %resp.status(),
                            failures = health.consecutive_failures,
                            "probe returned non-success"
                        );
                        if health.consecutive_failures >= self.policy.failure_threshold {
                            health.healthy = false;
                        }
                    }
                    Err(e) => {
                        health.consecutive_failures += 1;
                        tracing::warn!(
                            cluster = %cluster_id,
                            error = %e,
                            failures = health.consecutive_failures,
                            "probe failed"
                        );
                        if health.consecutive_failures >= self.policy.failure_threshold {
                            health.healthy = false;
                        }
                    }
                }
            }
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[tokio::test]
    async fn unregistered_cluster_is_unhealthy() {
        let checker = HealthChecker::new(Duration::from_secs(10));
        let cid = ClusterId("unknown".to_string());
        assert!(!checker.snapshot().await.contains_key(&cid));
    }

    #[tokio::test]
    async fn snapshot_returns_all_clusters() {
        let checker = HealthChecker::new(Duration::from_secs(10));
        checker
            .register_cluster(
                ClusterId("c1".to_string()),
                "http://c1/readyz".to_string(),
                "http://c1".to_string(),
            )
            .await;
        checker
            .register_cluster(
                ClusterId("c2".to_string()),
                "http://c2/readyz".to_string(),
                "http://c2".to_string(),
            )
            .await;
        let snap = checker.snapshot().await;
        assert_eq!(snap.len(), 2);
    }

    #[tokio::test]
    async fn unregister_removes_cluster() {
        let checker = HealthChecker::new(Duration::from_secs(10));
        let cid = ClusterId("temp".to_string());
        checker
            .register_cluster(
                cid.clone(),
                "http://temp/readyz".to_string(),
                "http://temp".to_string(),
            )
            .await;
        checker.unregister_cluster(&cid).await;
        assert!(!checker.snapshot().await.contains_key(&cid));
        assert!(!checker.endpoints.read().await.contains_key(&cid));
    }

    #[tokio::test]
    async fn endpoint_requires_a_successful_probe_before_healthy() {
        let checker = HealthChecker::new(Duration::from_secs(10));
        let cid = ClusterId("remote".to_string());
        checker
            .register_cluster(
                cid.clone(),
                "http://remote/readyz".to_string(),
                "http://remote".to_string(),
            )
            .await;
        assert!(!checker.snapshot().await[&cid].healthy);
    }

    #[test]
    fn cluster_health_serializes() {
        let h = ClusterHealth {
            cluster_id: ClusterId("c1".to_string()),
            healthy: true,
            latency_ms: 12.5,
            last_check: Utc::now(),
            consecutive_failures: 0,
        };
        let json = serde_json::to_string(&h).unwrap();
        assert!(json.contains("c1"));
    }
}
