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
}

impl HealthChecker {
    /// Create a new [`HealthChecker`] with the given probe interval.
    pub fn new(interval: Duration) -> Self {
        Self {
            interval,
            policy: HealthPolicy::default(),
            state: Arc::new(RwLock::new(HashMap::new())),
        }
    }

    /// Override the default health policy.
    pub fn with_policy(mut self, policy: HealthPolicy) -> Self {
        self.policy = policy;
        self
    }

    /// Get a snapshot of the current health state for all clusters.
    pub async fn snapshot(&self) -> HashMap<ClusterId, ClusterHealth> {
        self.state.read().await.clone()
    }

    /// Check whether a specific cluster is healthy.
    pub async fn is_healthy(&self, cluster_id: &ClusterId) -> bool {
        self.state
            .read()
            .await
            .get(cluster_id)
            .map_or(false, |h| h.healthy)
    }

    /// Register a cluster endpoint to monitor.
    pub async fn register_cluster(&self, cluster_id: ClusterId) {
        let mut state = self.state.write().await;
        state.entry(cluster_id.clone()).or_insert_with(|| ClusterHealth {
            cluster_id,
            healthy: true,
            latency_ms: 0.0,
            last_check: Utc::now(),
            consecutive_failures: 0,
        });
    }

    /// Remove a cluster from monitoring.
    pub async fn unregister_cluster(&self, cluster_id: &ClusterId) {
        self.state.write().await.remove(cluster_id);
    }

    /// Start the periodic health check loop. Runs until cancelled.
    pub async fn run(&self) -> anyhow::Result<()> {
        tracing::info!(
            interval_ms = self.interval.as_millis() as u64,
            failure_threshold = self.policy.failure_threshold,
            "starting health checker"
        );

        let mut ticker = tokio::time::interval(self.interval);
        loop {
            ticker.tick().await;

            let cluster_ids: Vec<ClusterId> = {
                self.state.read().await.keys().cloned().collect()
            };

            for cluster_id in &cluster_ids {
                // TODO: perform actual HTTP/gRPC probe against the cluster
                // endpoint. On success, reset consecutive_failures and update
                // latency. On failure, increment consecutive_failures and mark
                // unhealthy if threshold exceeded.
                tracing::debug!(cluster = %cluster_id, "probing cluster (stub)");
            }
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[tokio::test]
    async fn register_and_check_health() {
        let checker = HealthChecker::new(Duration::from_secs(10));
        let cid = ClusterId("test-cluster".to_string());
        checker.register_cluster(cid.clone()).await;
        assert!(checker.is_healthy(&cid).await);
    }

    #[tokio::test]
    async fn unregistered_cluster_is_unhealthy() {
        let checker = HealthChecker::new(Duration::from_secs(10));
        let cid = ClusterId("unknown".to_string());
        assert!(!checker.is_healthy(&cid).await);
    }

    #[tokio::test]
    async fn snapshot_returns_all_clusters() {
        let checker = HealthChecker::new(Duration::from_secs(10));
        checker
            .register_cluster(ClusterId("c1".to_string()))
            .await;
        checker
            .register_cluster(ClusterId("c2".to_string()))
            .await;
        let snap = checker.snapshot().await;
        assert_eq!(snap.len(), 2);
    }

    #[tokio::test]
    async fn unregister_removes_cluster() {
        let checker = HealthChecker::new(Duration::from_secs(10));
        let cid = ClusterId("temp".to_string());
        checker.register_cluster(cid.clone()).await;
        checker.unregister_cluster(&cid).await;
        assert!(!checker.is_healthy(&cid).await);
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
