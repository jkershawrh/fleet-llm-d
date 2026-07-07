//! # fleet-common
//!
//! Shared types, traits, and utilities for the fleet-llm-d orchestration platform.
//!
//! This crate provides the foundational types used across fleet-agent, fleet-gateway,
//! and kv-transfer, including cluster identifiers, configuration, metrics, events,
//! and core traits for reporting and policy enforcement.

use std::fmt;
use std::str::FromStr;

use chrono::{DateTime, Utc};
use serde::{Deserialize, Serialize};
use uuid::Uuid;

// ---------------------------------------------------------------------------
// Newtype wrappers
// ---------------------------------------------------------------------------

/// Unique identifier for a cluster within the fleet.
#[derive(Debug, Clone, PartialEq, Eq, Hash, Serialize, Deserialize)]
pub struct ClusterId(pub String);

impl fmt::Display for ClusterId {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        write!(f, "{}", self.0)
    }
}

impl FromStr for ClusterId {
    type Err = std::convert::Infallible;

    fn from_str(s: &str) -> Result<Self, Self::Err> {
        Ok(ClusterId(s.to_owned()))
    }
}

/// Unique identifier for a tenant consuming inference resources.
#[derive(Debug, Clone, PartialEq, Eq, Hash, Serialize, Deserialize)]
pub struct TenantId(pub String);

impl fmt::Display for TenantId {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        write!(f, "{}", self.0)
    }
}

impl FromStr for TenantId {
    type Err = std::convert::Infallible;

    fn from_str(s: &str) -> Result<Self, Self::Err> {
        Ok(TenantId(s.to_owned()))
    }
}

/// Unique identifier for a model served by the fleet.
#[derive(Debug, Clone, PartialEq, Eq, Hash, Serialize, Deserialize)]
pub struct ModelId(pub String);

impl fmt::Display for ModelId {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        write!(f, "{}", self.0)
    }
}

impl FromStr for ModelId {
    type Err = std::convert::Infallible;

    fn from_str(s: &str) -> Result<Self, Self::Err> {
        Ok(ModelId(s.to_owned()))
    }
}

// ---------------------------------------------------------------------------
// Configuration
// ---------------------------------------------------------------------------

/// Top-level configuration for a fleet-llm-d component.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct FleetConfig {
    /// URL of the fleet control plane (e.g. `https://fleet-cp.example.com`).
    pub control_plane_url: String,
    /// Identity of the local cluster this component belongs to.
    pub cluster_id: ClusterId,
    /// Port the fleet-agent listens on for gRPC / health.
    pub agent_port: u16,
    /// Port the Prometheus metrics server listens on.
    pub metrics_port: u16,
}

// ---------------------------------------------------------------------------
// Cluster status
// ---------------------------------------------------------------------------

/// Snapshot of a cluster's operational status as reported by its fleet-agent.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct ClusterStatus {
    /// Cluster identifier.
    pub id: ClusterId,
    /// Human-readable cluster name.
    pub name: String,
    /// Cloud / data-center region (e.g. `us-east-1`).
    pub region: String,
    /// Lifecycle phase (e.g. `Running`, `Draining`, `Offline`).
    pub phase: String,
    /// Number of GPUs currently available for scheduling.
    pub gpu_available: u32,
    /// Total number of GPUs in the cluster.
    pub gpu_total: u32,
    /// Whether the cluster is considered healthy by the control plane.
    pub healthy: bool,
}

// ---------------------------------------------------------------------------
// Inference metrics
// ---------------------------------------------------------------------------

/// Key inference-performance metrics collected from a cluster.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct InferenceMetrics {
    /// Aggregate throughput in tokens per second.
    pub throughput_tps: f64,
    /// Median time-to-first-token in milliseconds.
    pub ttft_p50_ms: f64,
    /// 99th-percentile time-to-first-token in milliseconds.
    pub ttft_p99_ms: f64,
    /// KV-cache hit rate (0.0 .. 1.0).
    pub kv_cache_hit_rate: f64,
    /// Number of requests currently waiting in the queue.
    pub queue_depth: u64,
    /// GPU utilisation percentage (0.0 .. 100.0).
    pub gpu_utilization: f64,
}

// ---------------------------------------------------------------------------
// Fleet events
// ---------------------------------------------------------------------------

/// Domain events emitted by fleet components and consumed by the control plane
/// or downstream observers.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub enum FleetEvent {
    /// A new cluster has joined the fleet.
    ClusterJoined {
        cluster_id: ClusterId,
        region: String,
        timestamp: DateTime<Utc>,
    },
    /// A cluster has left the fleet (graceful deregistration).
    ClusterLeft {
        cluster_id: ClusterId,
        timestamp: DateTime<Utc>,
    },
    /// A cluster has been marked unhealthy.
    ClusterUnhealthy {
        cluster_id: ClusterId,
        reason: String,
        timestamp: DateTime<Utc>,
    },
    /// Model placement has changed (new model deployed or removed).
    PlacementChanged {
        cluster_id: ClusterId,
        model_id: ModelId,
        action: String,
        timestamp: DateTime<Utc>,
    },
    /// Traffic routing weights have been adjusted.
    RoutingShifted {
        model_id: ModelId,
        from_cluster: ClusterId,
        to_cluster: ClusterId,
        weight_delta: f64,
        timestamp: DateTime<Utc>,
    },
    /// A tenant has exceeded its allocated quota.
    TenantQuotaExceeded {
        tenant_id: TenantId,
        cluster_id: ClusterId,
        quota_type: String,
        current: f64,
        limit: f64,
        timestamp: DateTime<Utc>,
    },
    /// A KV-cache transfer has been initiated between clusters.
    KvTransferInitiated {
        source_cluster: ClusterId,
        target_cluster: ClusterId,
        model_id: ModelId,
        transfer_id: Uuid,
        timestamp: DateTime<Utc>,
    },
}

// ---------------------------------------------------------------------------
// Traits
// ---------------------------------------------------------------------------

/// Trait for components that report cluster status, metrics, and events to the
/// fleet control plane.
#[allow(async_fn_in_trait)]
pub trait FleetReporter: Send + Sync {
    /// Report the current status of the local cluster.
    async fn report_status(&self, status: &ClusterStatus) -> Result<(), FleetError>;

    /// Report collected inference metrics.
    async fn report_metrics(&self, metrics: &InferenceMetrics) -> Result<(), FleetError>;

    /// Report a domain event.
    async fn report_event(&self, event: &FleetEvent) -> Result<(), FleetError>;
}

/// Trait for components that enforce fleet-wide policies on the local cluster.
#[allow(async_fn_in_trait)]
pub trait PolicyEnforcer: Send + Sync {
    /// Enforce tenant quota limits and return whether the request is allowed.
    async fn enforce_tenant_quota(
        &self,
        tenant_id: &TenantId,
        model_id: &ModelId,
    ) -> Result<bool, FleetError>;

    /// Enforce placement constraints and return whether the given model may
    /// run on this cluster.
    async fn enforce_placement_constraints(
        &self,
        model_id: &ModelId,
        cluster_id: &ClusterId,
    ) -> Result<bool, FleetError>;
}

// ---------------------------------------------------------------------------
// Errors
// ---------------------------------------------------------------------------

/// Errors originating from fleet-llm-d components.
#[derive(Debug, thiserror::Error)]
pub enum FleetError {
    /// The control plane could not be reached.
    #[error("control plane unreachable: {0}")]
    ControlPlaneUnreachable(String),

    /// A cluster was not found in the fleet registry.
    #[error("cluster not found: {0}")]
    ClusterNotFound(ClusterId),

    /// A tenant quota has been exceeded.
    #[error("tenant quota exceeded for {tenant_id} on {model_id}")]
    QuotaExceeded {
        tenant_id: TenantId,
        model_id: ModelId,
    },

    /// A placement constraint was violated.
    #[error("placement constraint violated: {0}")]
    PlacementViolation(String),

    /// A KV-cache transfer failed.
    #[error("kv transfer failed: {0}")]
    KvTransferFailed(String),

    /// An internal error occurred.
    #[error("internal error: {0}")]
    Internal(String),

    /// Wraps an arbitrary error via `anyhow`.
    #[error(transparent)]
    Other(#[from] anyhow::Error),
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn cluster_id_display_roundtrip() {
        let id = ClusterId("us-east-1-prod".to_string());
        let s = id.to_string();
        let parsed: ClusterId = s.parse().unwrap();
        assert_eq!(id, parsed);
    }

    #[test]
    fn tenant_id_display_roundtrip() {
        let id = TenantId("acme-corp".to_string());
        let s = id.to_string();
        let parsed: TenantId = s.parse().unwrap();
        assert_eq!(id, parsed);
    }

    #[test]
    fn model_id_display_roundtrip() {
        let id = ModelId("llama-3-70b".to_string());
        let s = id.to_string();
        let parsed: ModelId = s.parse().unwrap();
        assert_eq!(id, parsed);
    }

    #[test]
    fn fleet_config_serializes() {
        let cfg = FleetConfig {
            control_plane_url: "https://cp.example.com".to_string(),
            cluster_id: ClusterId("cluster-1".to_string()),
            agent_port: 8080,
            metrics_port: 9090,
        };
        let json = serde_json::to_string(&cfg).unwrap();
        let deser: FleetConfig = serde_json::from_str(&json).unwrap();
        assert_eq!(deser.agent_port, 8080);
    }

    #[test]
    fn fleet_error_display() {
        let err = FleetError::ClusterNotFound(ClusterId("missing".to_string()));
        assert!(err.to_string().contains("missing"));
    }

    #[test]
    fn fleet_event_serializes() {
        let event = FleetEvent::ClusterJoined {
            cluster_id: ClusterId("c1".to_string()),
            region: "us-west-2".to_string(),
            timestamp: Utc::now(),
        };
        let json = serde_json::to_string(&event).unwrap();
        assert!(json.contains("ClusterJoined"));
    }
}
