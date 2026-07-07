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

/// The outcome of a routing decision.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct RouteDecision {
    /// Cluster selected to handle the request.
    pub target_cluster: ClusterId,
    /// Full URL to forward the request to.
    pub target_url: String,
    /// Additional headers to inject into the forwarded request.
    pub headers_to_inject: HashMap<String, String>,
}

/// Routing policy that governs how requests for a model are distributed.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct RoutingPolicy {
    /// Model this policy applies to.
    pub model_id: ModelId,
    /// Weighted distribution across clusters (cluster_id -> weight 0.0..1.0).
    pub cluster_weights: HashMap<ClusterId, f64>,
    /// Whether to allow overflow routing to non-primary clusters.
    pub allow_overflow: bool,
    /// Tenant-specific overrides.
    pub tenant_overrides: HashMap<TenantId, HashMap<ClusterId, f64>>,
}

/// An incoming inference request (simplified for routing decisions).
#[derive(Debug, Clone)]
pub struct InferenceRequest {
    /// Model being requested.
    pub model_id: ModelId,
    /// Tenant making the request.
    pub tenant_id: Option<TenantId>,
    /// Preferred region, if any.
    pub preferred_region: Option<String>,
    /// Request body size in bytes (used for cost estimation).
    pub body_size_bytes: u64,
}

/// Fleet-level router that maps inference requests to target clusters.
#[derive(Debug, Clone)]
pub struct FleetRouter {
    /// Routing policies keyed by model ID.
    policies: Arc<RwLock<HashMap<ModelId, RoutingPolicy>>>,
}

impl FleetRouter {
    /// Create a new [`FleetRouter`] with no policies loaded.
    pub fn new() -> Self {
        Self {
            policies: Arc::new(RwLock::new(HashMap::new())),
        }
    }

    /// Add or update a routing policy.
    pub async fn set_policy(&self, policy: RoutingPolicy) {
        self.policies
            .write()
            .await
            .insert(policy.model_id.clone(), policy);
    }

    /// Route an inference request to a target cluster.
    ///
    /// Uses the routing policy for the requested model, cluster health data,
    /// and the configured load-balancing strategy to produce a [`RouteDecision`].
    pub async fn route(&self, request: &InferenceRequest) -> anyhow::Result<RouteDecision> {
        let policies = self.policies.read().await;

        let policy = policies.get(&request.model_id).ok_or_else(|| {
            anyhow::anyhow!("no routing policy for model {}", request.model_id)
        })?;

        // Determine weights (tenant override or default).
        let weights = if let Some(tenant_id) = &request.tenant_id {
            policy
                .tenant_overrides
                .get(tenant_id)
                .unwrap_or(&policy.cluster_weights)
        } else {
            &policy.cluster_weights
        };

        // Pick the cluster with the highest weight (placeholder logic).
        // A real implementation would use the LoadBalancer trait.
        let (target_cluster, _weight) = weights
            .iter()
            .max_by(|a, b| a.1.partial_cmp(b.1).unwrap_or(std::cmp::Ordering::Equal))
            .ok_or_else(|| anyhow::anyhow!("no clusters available for model {}", request.model_id))?;

        let mut headers = HashMap::new();
        headers.insert(
            "x-fleet-source".to_string(),
            "fleet-gateway".to_string(),
        );
        if let Some(tenant) = &request.tenant_id {
            headers.insert("x-llm-d-tenant-id".to_string(), tenant.to_string());
        }

        Ok(RouteDecision {
            target_cluster: target_cluster.clone(),
            target_url: format!("http://{}.cluster.local:8000/v1/completions", target_cluster),
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
        assert_eq!(decision.target_cluster, ClusterId("c2".to_string()));
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
}
