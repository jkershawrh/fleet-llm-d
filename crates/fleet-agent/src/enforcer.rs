//! Local policy enforcement for fleet-wide rules.
//!
//! Enforces tenant quotas and model placement constraints on the local cluster,
//! ensuring that fleet policies are respected even when the control plane is
//! temporarily unreachable.

use std::collections::HashMap;
use std::sync::Arc;

use fleet_common::{ClusterId, FleetError, ModelId, PolicyEnforcer, TenantId};
use tokio::sync::RwLock;

/// Tenant-level quota configuration.
#[derive(Debug, Clone)]
pub struct TenantQuota {
    /// Maximum requests per second allowed for this tenant.
    pub max_rps: f64,
    /// Maximum concurrent requests.
    pub max_concurrent: u64,
    /// Maximum tokens per minute.
    pub max_tokens_per_minute: u64,
}

/// Current usage counters for a tenant.
#[derive(Debug, Clone, Default)]
pub struct TenantUsage {
    /// Current requests per second.
    pub current_rps: f64,
    /// Current concurrent requests.
    pub current_concurrent: u64,
    /// Tokens consumed in the current minute.
    pub tokens_this_minute: u64,
}

/// Placement constraint for a model on a cluster.
#[derive(Debug, Clone)]
pub struct PlacementConstraint {
    /// Models that are allowed on this cluster (empty = all allowed).
    pub allowed_models: Vec<ModelId>,
    /// Models that are explicitly denied on this cluster.
    pub denied_models: Vec<ModelId>,
}

/// Local policy enforcer that implements [`PolicyEnforcer`].
///
/// Maintains an in-memory view of tenant quotas and placement constraints,
/// synced periodically from the control plane.
#[derive(Debug, Clone)]
pub struct PolicyEnforcerImpl {
    /// The cluster this enforcer runs on.
    cluster_id: ClusterId,
    /// Tenant quota configuration, keyed by tenant ID.
    quotas: Arc<RwLock<HashMap<TenantId, TenantQuota>>>,
    /// Current tenant usage counters.
    usage: Arc<RwLock<HashMap<TenantId, TenantUsage>>>,
    /// Placement constraints for this cluster.
    placement: Arc<RwLock<PlacementConstraint>>,
}

impl PolicyEnforcerImpl {
    /// Create a new [`PolicyEnforcerImpl`] for the given cluster.
    pub fn new(cluster_id: ClusterId) -> Self {
        Self {
            cluster_id,
            quotas: Arc::new(RwLock::new(HashMap::new())),
            usage: Arc::new(RwLock::new(HashMap::new())),
            placement: Arc::new(RwLock::new(PlacementConstraint {
                allowed_models: Vec::new(),
                denied_models: Vec::new(),
            })),
        }
    }

    /// Returns the cluster this enforcer is bound to.
    pub fn cluster_id(&self) -> &ClusterId {
        &self.cluster_id
    }

    /// Update the quota configuration for a tenant.
    pub async fn set_quota(&self, tenant_id: TenantId, quota: TenantQuota) {
        self.quotas.write().await.insert(tenant_id, quota);
    }

    /// Update the placement constraints for this cluster.
    pub async fn set_placement(&self, constraint: PlacementConstraint) {
        *self.placement.write().await = constraint;
    }

    /// Start the policy sync loop that periodically fetches updated policies
    /// from the control plane. Runs until cancelled.
    pub async fn run(&self) -> anyhow::Result<()> {
        tracing::info!(cluster_id = %self.cluster_id, "starting policy enforcer sync");

        // TODO: periodically fetch updated quotas and placement constraints
        // from the control plane via gRPC.
        std::future::pending::<()>().await;
        Ok(())
    }
}

impl PolicyEnforcer for PolicyEnforcerImpl {
    async fn enforce_tenant_quota(
        &self,
        tenant_id: &TenantId,
        model_id: &ModelId,
    ) -> Result<bool, FleetError> {
        let quotas = self.quotas.read().await;
        let usage = self.usage.read().await;

        let Some(quota) = quotas.get(tenant_id) else {
            // No quota configured -- allow by default.
            return Ok(true);
        };

        if let Some(current) = usage.get(tenant_id) {
            if current.current_rps > quota.max_rps {
                tracing::warn!(
                    tenant = %tenant_id,
                    model = %model_id,
                    rps = current.current_rps,
                    limit = quota.max_rps,
                    "tenant RPS quota exceeded"
                );
                return Err(FleetError::QuotaExceeded {
                    tenant_id: tenant_id.clone(),
                    model_id: model_id.clone(),
                });
            }
            if current.current_concurrent >= quota.max_concurrent {
                return Err(FleetError::QuotaExceeded {
                    tenant_id: tenant_id.clone(),
                    model_id: model_id.clone(),
                });
            }
        }

        Ok(true)
    }

    async fn enforce_placement_constraints(
        &self,
        model_id: &ModelId,
        cluster_id: &ClusterId,
    ) -> Result<bool, FleetError> {
        let placement = self.placement.read().await;

        // Check explicit deny list.
        if placement.denied_models.contains(model_id) {
            return Err(FleetError::PlacementViolation(format!(
                "model {} is denied on cluster {}",
                model_id, cluster_id
            )));
        }

        // If an allow list is configured, check membership.
        if !placement.allowed_models.is_empty() && !placement.allowed_models.contains(model_id) {
            return Err(FleetError::PlacementViolation(format!(
                "model {} is not in the allow list for cluster {}",
                model_id, cluster_id
            )));
        }

        Ok(true)
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[tokio::test]
    async fn allow_when_no_quota_set() {
        let enforcer = PolicyEnforcerImpl::new(ClusterId("c1".to_string()));
        let allowed = enforcer
            .enforce_tenant_quota(
                &TenantId("t1".to_string()),
                &ModelId("m1".to_string()),
            )
            .await
            .unwrap();
        assert!(allowed);
    }

    #[tokio::test]
    async fn allow_when_no_placement_constraints() {
        let enforcer = PolicyEnforcerImpl::new(ClusterId("c1".to_string()));
        let allowed = enforcer
            .enforce_placement_constraints(
                &ModelId("m1".to_string()),
                &ClusterId("c1".to_string()),
            )
            .await
            .unwrap();
        assert!(allowed);
    }

    #[tokio::test]
    async fn deny_model_on_denied_list() {
        let enforcer = PolicyEnforcerImpl::new(ClusterId("c1".to_string()));
        enforcer
            .set_placement(PlacementConstraint {
                allowed_models: Vec::new(),
                denied_models: vec![ModelId("banned-model".to_string())],
            })
            .await;

        let result = enforcer
            .enforce_placement_constraints(
                &ModelId("banned-model".to_string()),
                &ClusterId("c1".to_string()),
            )
            .await;
        assert!(result.is_err());
    }
}
