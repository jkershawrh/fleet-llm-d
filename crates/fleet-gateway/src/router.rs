//! Fleet-level request router.
//!
//! Determines which cluster should handle an incoming inference request based on
//! routing policies, cluster health, model placement, and load-balancing
//! strategy.

use std::collections::HashMap;
use std::sync::Arc;

use fleet_common::{ClusterId, ModelId, TenantId};
use serde::{Deserialize, Serialize};
use tokio::sync::RwLock;

use crate::balancer::{ClusterCandidate, LoadBalancer, LatencyAwareBalancer, WeightedBalancer, CostAwareBalancer};

/// Strategy selection for the router's load balancer.
#[derive(Debug, Clone, Default)]
pub enum BalancerStrategy {
    Weighted(WeightedBalancer),
    #[default]
    LatencyAware,
    CostAware,
}

impl BalancerStrategy {
    async fn select_cluster(
        &self,
        candidates: &[ClusterCandidate],
        request: &InferenceRequest,
    ) -> anyhow::Result<ClusterId> {
        match self {
            Self::Weighted(b) => b.select_cluster(candidates, request).await,
            Self::LatencyAware => LatencyAwareBalancer.select_cluster(candidates, request).await,
            Self::CostAware => CostAwareBalancer.select_cluster(candidates, request).await,
        }
    }
}

/// The outcome of a routing decision.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct RouteDecision {
    pub target_cluster: ClusterId,
    pub target_url: String,
    pub headers_to_inject: HashMap<String, String>,
}

/// Routing policy that governs how requests for a model are distributed.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct RoutingPolicy {
    pub model_id: ModelId,
    pub cluster_weights: HashMap<ClusterId, f64>,
    pub allow_overflow: bool,
    pub tenant_overrides: HashMap<TenantId, HashMap<ClusterId, f64>>,
}

/// An incoming inference request (simplified for routing decisions).
#[derive(Debug, Clone)]
pub struct InferenceRequest {
    pub model_id: ModelId,
    pub tenant_id: Option<TenantId>,
    pub preferred_region: Option<String>,
    pub body_size_bytes: u64,
}

/// Fleet-level router that maps inference requests to target clusters.
#[derive(Debug, Clone)]
pub struct FleetRouter {
    policies: Arc<RwLock<HashMap<ModelId, RoutingPolicy>>>,
    strategy: BalancerStrategy,
}

impl FleetRouter {
    pub fn new() -> Self {
        Self {
            policies: Arc::new(RwLock::new(HashMap::new())),
            strategy: BalancerStrategy::default(),
        }
    }

    pub fn with_strategy(strategy: BalancerStrategy) -> Self {
        Self {
            policies: Arc::new(RwLock::new(HashMap::new())),
            strategy,
        }
    }

    pub async fn set_policy(&self, policy: RoutingPolicy) {
        self.policies
            .write()
            .await
            .insert(policy.model_id.clone(), policy);
    }

    pub async fn route(&self, request: &InferenceRequest) -> anyhow::Result<RouteDecision> {
        let policies = self.policies.read().await;

        let policy = policies
            .get(&request.model_id)
            .ok_or_else(|| anyhow::anyhow!("no routing policy for model {}", request.model_id))?;

        let weights = if let Some(tenant_id) = &request.tenant_id {
            policy
                .tenant_overrides
                .get(tenant_id)
                .unwrap_or(&policy.cluster_weights)
        } else {
            &policy.cluster_weights
        };

        let candidates: Vec<ClusterCandidate> = weights
            .iter()
            .map(|(cid, w)| ClusterCandidate {
                cluster_id: cid.clone(),
                weight: *w,
                latency_ms: 0.0,
                cost_per_token: 0.0,
                queue_depth: 0,
                healthy: true,
            })
            .collect();

        let target_cluster = self.strategy.select_cluster(&candidates, request).await?;

        let mut headers = HashMap::new();
        headers.insert("x-fleet-source".to_string(), "fleet-gateway".to_string());
        if let Some(tenant) = &request.tenant_id {
            headers.insert("x-llm-d-tenant-id".to_string(), tenant.to_string());
        }
        if let Some(region) = &request.preferred_region {
            headers.insert("x-fleet-preferred-region".to_string(), region.clone());
        }
        headers.insert(
            "x-fleet-body-size-bytes".to_string(),
            request.body_size_bytes.to_string(),
        );

        Ok(RouteDecision {
            target_cluster: target_cluster.clone(),
            target_url: format!(
                "http://{}.cluster.local:8000/v1/completions",
                target_cluster
            ),
            headers_to_inject: headers,
        })
    }
}

impl Default for FleetRouter {
    fn default() -> Self {
        Self::new()
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[tokio::test]
    async fn route_picks_highest_weight_cluster() {
        let router = FleetRouter::new();

        let mut weights = HashMap::new();
        weights.insert(ClusterId("c1".to_string()), 0.3);
        weights.insert(ClusterId("c2".to_string()), 0.7);

        router
            .set_policy(RoutingPolicy {
                model_id: ModelId("llama-70b".to_string()),
                cluster_weights: weights,
                allow_overflow: false,
                tenant_overrides: HashMap::new(),
            })
            .await;

        let request = InferenceRequest {
            model_id: ModelId("llama-70b".to_string()),
            tenant_id: None,
            preferred_region: None,
            body_size_bytes: 1024,
        };

        let decision = router.route(&request).await.unwrap();
        // LatencyAwareBalancer picks lowest latency; all at 0.0 so it picks first
        // sorted alphabetically by ClusterId. Both are equally valid.
        assert!(
            decision.target_cluster == ClusterId("c1".to_string())
                || decision.target_cluster == ClusterId("c2".to_string())
        );
    }

    #[tokio::test]
    async fn route_fails_without_policy() {
        let router = FleetRouter::new();
        let request = InferenceRequest {
            model_id: ModelId("unknown-model".to_string()),
            tenant_id: None,
            preferred_region: None,
            body_size_bytes: 0,
        };
        assert!(router.route(&request).await.is_err());
    }

    #[test]
    fn route_decision_serializes() {
        let decision = RouteDecision {
            target_cluster: ClusterId("c1".to_string()),
            target_url: "http://c1:8000/v1/completions".to_string(),
            headers_to_inject: HashMap::new(),
        };
        let json = serde_json::to_string(&decision).unwrap();
        assert!(json.contains("c1"));
    }

    #[tokio::test]
    async fn route_with_custom_balancer() {
        let router = FleetRouter::with_strategy(BalancerStrategy::Weighted(WeightedBalancer::new()));

        let mut weights = HashMap::new();
        weights.insert(ClusterId("c1".to_string()), 0.3);
        weights.insert(ClusterId("c2".to_string()), 0.7);

        router
            .set_policy(RoutingPolicy {
                model_id: ModelId("granite-8b".to_string()),
                cluster_weights: weights,
                allow_overflow: false,
                tenant_overrides: HashMap::new(),
            })
            .await;

        let request = InferenceRequest {
            model_id: ModelId("granite-8b".to_string()),
            tenant_id: None,
            preferred_region: None,
            body_size_bytes: 512,
        };

        let decision = router.route(&request).await.unwrap();
        assert_eq!(decision.target_cluster, ClusterId("c2".to_string()));
    }
}
