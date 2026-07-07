//! Load-balancing strategies for cross-cluster request distribution.
//!
//! Provides pluggable balancer implementations that select the optimal cluster
//! from a set of candidates based on different criteria: static weights,
//! observed latency, or estimated cost.

use fleet_common::ClusterId;
use serde::{Deserialize, Serialize};
use std::collections::HashMap;

use crate::router::InferenceRequest;

/// Metadata about a candidate cluster used for balancing decisions.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct ClusterCandidate {
    /// Cluster identity.
    pub cluster_id: ClusterId,
    /// Static routing weight (0.0 .. 1.0).
    pub weight: f64,
    /// Last observed round-trip latency in milliseconds.
    pub latency_ms: f64,
    /// Estimated per-token cost for this cluster.
    pub cost_per_token: f64,
    /// Current queue depth.
    pub queue_depth: u64,
    /// Whether the cluster is currently healthy.
    pub healthy: bool,
}

/// Trait for load-balancing strategies.
#[allow(async_fn_in_trait)]
pub trait LoadBalancer: Send + Sync {
    /// Select the best cluster from the given candidates for the request.
    async fn select_cluster(
        &self,
        candidates: &[ClusterCandidate],
        request: &InferenceRequest,
    ) -> anyhow::Result<ClusterId>;
}

/// Weighted round-robin balancer that distributes traffic according to static
/// weights assigned to each cluster.
#[derive(Debug, Clone, Default)]
pub struct WeightedBalancer {
    /// Per-cluster weight overrides (applied on top of candidate weights).
    weight_overrides: HashMap<ClusterId, f64>,
}

impl WeightedBalancer {
    /// Create a new [`WeightedBalancer`].
    pub fn new() -> Self {
        Self::default()
    }

    /// Set a weight override for a specific cluster.
    pub fn set_weight(&mut self, cluster_id: ClusterId, weight: f64) {
        self.weight_overrides.insert(cluster_id, weight);
    }
}

impl LoadBalancer for WeightedBalancer {
    async fn select_cluster(
        &self,
        candidates: &[ClusterCandidate],
        _request: &InferenceRequest,
    ) -> anyhow::Result<ClusterId> {
        let healthy: Vec<_> = candidates.iter().filter(|c| c.healthy).collect();
        if healthy.is_empty() {
            anyhow::bail!("no healthy clusters available");
        }

        // Pick cluster with the highest effective weight.
        let best = healthy
            .iter()
            .max_by(|a, b| {
                let wa = self
                    .weight_overrides
                    .get(&a.cluster_id)
                    .unwrap_or(&a.weight);
                let wb = self
                    .weight_overrides
                    .get(&b.cluster_id)
                    .unwrap_or(&b.weight);
                wa.partial_cmp(wb).unwrap_or(std::cmp::Ordering::Equal)
            })
            .unwrap();

        Ok(best.cluster_id.clone())
    }
}

/// Latency-aware balancer that prefers the cluster with the lowest observed
/// round-trip latency.
#[derive(Debug, Clone, Default)]
pub struct LatencyAwareBalancer;

impl LatencyAwareBalancer {
    /// Create a new [`LatencyAwareBalancer`].
    pub fn new() -> Self {
        Self
    }
}

impl LoadBalancer for LatencyAwareBalancer {
    async fn select_cluster(
        &self,
        candidates: &[ClusterCandidate],
        _request: &InferenceRequest,
    ) -> anyhow::Result<ClusterId> {
        let healthy: Vec<_> = candidates.iter().filter(|c| c.healthy).collect();
        if healthy.is_empty() {
            anyhow::bail!("no healthy clusters available");
        }

        let best = healthy
            .iter()
            .min_by(|a, b| {
                a.latency_ms
                    .partial_cmp(&b.latency_ms)
                    .unwrap_or(std::cmp::Ordering::Equal)
            })
            .unwrap();

        Ok(best.cluster_id.clone())
    }
}

/// Cost-aware balancer that prefers the cluster with the lowest per-token cost,
/// weighted by queue depth to avoid overloading cheap clusters.
#[derive(Debug, Clone, Default)]
pub struct CostAwareBalancer;

impl CostAwareBalancer {
    /// Create a new [`CostAwareBalancer`].
    pub fn new() -> Self {
        Self
    }
}

impl LoadBalancer for CostAwareBalancer {
    async fn select_cluster(
        &self,
        candidates: &[ClusterCandidate],
        _request: &InferenceRequest,
    ) -> anyhow::Result<ClusterId> {
        let healthy: Vec<_> = candidates.iter().filter(|c| c.healthy).collect();
        if healthy.is_empty() {
            anyhow::bail!("no healthy clusters available");
        }

        // Score = cost_per_token * (1 + queue_depth / 100). Lower is better.
        let best = healthy
            .iter()
            .min_by(|a, b| {
                let score_a = a.cost_per_token * (1.0 + a.queue_depth as f64 / 100.0);
                let score_b = b.cost_per_token * (1.0 + b.queue_depth as f64 / 100.0);
                score_a
                    .partial_cmp(&score_b)
                    .unwrap_or(std::cmp::Ordering::Equal)
            })
            .unwrap();

        Ok(best.cluster_id.clone())
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use fleet_common::ModelId;

    fn make_request() -> InferenceRequest {
        InferenceRequest {
            model_id: ModelId("test-model".to_string()),
            tenant_id: None,
            preferred_region: None,
            body_size_bytes: 512,
        }
    }

    fn make_candidates() -> Vec<ClusterCandidate> {
        vec![
            ClusterCandidate {
                cluster_id: ClusterId("fast-expensive".to_string()),
                weight: 0.3,
                latency_ms: 10.0,
                cost_per_token: 0.01,
                queue_depth: 5,
                healthy: true,
            },
            ClusterCandidate {
                cluster_id: ClusterId("slow-cheap".to_string()),
                weight: 0.7,
                latency_ms: 50.0,
                cost_per_token: 0.001,
                queue_depth: 2,
                healthy: true,
            },
        ]
    }

    #[tokio::test]
    async fn weighted_picks_highest_weight() {
        let mut balancer = WeightedBalancer::new();
        balancer.set_weight(ClusterId("fast-expensive".to_string()), 0.9);
        let result = balancer
            .select_cluster(&make_candidates(), &make_request())
            .await
            .unwrap();
        assert_eq!(result, ClusterId("fast-expensive".to_string()));
    }

    #[tokio::test]
    async fn latency_picks_lowest_latency() {
        let balancer = LatencyAwareBalancer::new();
        let result = balancer
            .select_cluster(&make_candidates(), &make_request())
            .await
            .unwrap();
        assert_eq!(result, ClusterId("fast-expensive".to_string()));
    }

    #[tokio::test]
    async fn cost_picks_cheapest() {
        let balancer = CostAwareBalancer::new();
        let result = balancer
            .select_cluster(&make_candidates(), &make_request())
            .await
            .unwrap();
        assert_eq!(result, ClusterId("slow-cheap".to_string()));
    }

    #[tokio::test]
    async fn fails_with_no_healthy_clusters() {
        let balancer = WeightedBalancer::new();
        let unhealthy = vec![ClusterCandidate {
            cluster_id: ClusterId("down".to_string()),
            weight: 1.0,
            latency_ms: 100.0,
            cost_per_token: 0.01,
            queue_depth: 0,
            healthy: false,
        }];
        assert!(balancer
            .select_cluster(&unhealthy, &make_request())
            .await
            .is_err());
    }
}
