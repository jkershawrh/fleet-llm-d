//! Local inference traffic proxy.
//!
//! Intercepts inference requests on the local cluster and injects fleet-level
//! headers before forwarding to the llm-d EPP endpoint. This enables the EPP
//! scheduler to apply fleet-aware fairness and routing policies.

use serde::{Deserialize, Serialize};

/// Header name for the fleet inference fairness identifier.
pub const HEADER_FAIRNESS_ID: &str = "x-llm-d-inference-fairness-id";

/// Header name for the fleet inference objective.
pub const HEADER_INFERENCE_OBJECTIVE: &str = "x-llm-d-inference-objective";

/// Header name for the fleet tenant identifier.
pub const HEADER_TENANT_ID: &str = "x-llm-d-tenant-id";

/// Header name for the fleet cluster source.
pub const HEADER_SOURCE_CLUSTER: &str = "x-llm-d-source-cluster";

/// Inference objective hints that the fleet can inject.
#[derive(Debug, Clone, Serialize, Deserialize, PartialEq, Eq)]
pub enum InferenceObjective {
    /// Minimise time-to-first-token.
    MinLatency,
    /// Maximise throughput (tokens per second).
    MaxThroughput,
    /// Minimise cost (prefer cheaper GPU tiers).
    MinCost,
    /// Balance latency and throughput.
    Balanced,
}

impl std::fmt::Display for InferenceObjective {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        match self {
            Self::MinLatency => write!(f, "min-latency"),
            Self::MaxThroughput => write!(f, "max-throughput"),
            Self::MinCost => write!(f, "min-cost"),
            Self::Balanced => write!(f, "balanced"),
        }
    }
}

/// Headers to inject into proxied inference requests.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct FleetHeaders {
    /// Fairness identifier for the request (usually the tenant ID).
    pub fairness_id: Option<String>,
    /// Inference objective hint.
    pub objective: Option<InferenceObjective>,
    /// Tenant identifier.
    pub tenant_id: Option<String>,
    /// Source cluster identifier.
    pub source_cluster: Option<String>,
}

/// Local proxy that intercepts inference traffic and injects fleet headers.
#[derive(Debug, Clone)]
pub struct LocalProxy {
    /// Port the proxy listens on.
    listen_port: u16,
    /// Upstream EPP endpoint to forward requests to.
    upstream_url: String,
}

impl LocalProxy {
    /// Create a new [`LocalProxy`] listening on the given port.
    pub fn new(listen_port: u16) -> Self {
        Self {
            listen_port,
            // Default upstream; will be configurable.
            upstream_url: "http://localhost:8000".to_string(),
        }
    }

    /// Override the upstream URL.
    pub fn with_upstream(mut self, url: String) -> Self {
        self.upstream_url = url;
        self
    }

    /// Returns the port this proxy listens on.
    pub fn listen_port(&self) -> u16 {
        self.listen_port
    }

    /// Start the proxy server. Runs until cancelled.
    ///
    /// The proxy:
    /// 1. Accepts incoming HTTP requests on `listen_port`.
    /// 2. Injects fleet headers (fairness ID, objective, tenant, source cluster).
    /// 3. Forwards the request to the upstream EPP endpoint.
    /// 4. Returns the upstream response to the caller.
    pub async fn run(&self) -> anyhow::Result<()> {
        tracing::info!(
            port = self.listen_port,
            upstream = %self.upstream_url,
            "starting local inference proxy"
        );

        // TODO: build axum Router with a catch-all handler that:
        //   - extracts tenant/fairness info from the request or context
        //   - injects fleet headers
        //   - reverse-proxies to self.upstream_url
        std::future::pending::<()>().await;
        Ok(())
    }

    /// Build the set of fleet headers for a given request context.
    pub fn build_headers(
        &self,
        tenant_id: Option<&str>,
        objective: Option<InferenceObjective>,
        source_cluster: Option<&str>,
    ) -> FleetHeaders {
        FleetHeaders {
            fairness_id: tenant_id.map(|t| t.to_string()),
            objective,
            tenant_id: tenant_id.map(|t| t.to_string()),
            source_cluster: source_cluster.map(|s| s.to_string()),
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn proxy_stores_port() {
        let proxy = LocalProxy::new(8090);
        assert_eq!(proxy.listen_port(), 8090);
    }

    #[test]
    fn proxy_with_upstream() {
        let proxy = LocalProxy::new(8090).with_upstream("http://epp:8000".to_string());
        assert_eq!(proxy.upstream_url, "http://epp:8000");
    }

    #[test]
    fn build_headers_with_all_fields() {
        let proxy = LocalProxy::new(8090);
        let headers = proxy.build_headers(
            Some("tenant-a"),
            Some(InferenceObjective::MinLatency),
            Some("cluster-1"),
        );
        assert_eq!(headers.fairness_id.as_deref(), Some("tenant-a"));
        assert_eq!(headers.objective, Some(InferenceObjective::MinLatency));
        assert_eq!(headers.source_cluster.as_deref(), Some("cluster-1"));
    }

    #[test]
    fn inference_objective_display() {
        assert_eq!(InferenceObjective::MinLatency.to_string(), "min-latency");
        assert_eq!(InferenceObjective::MaxThroughput.to_string(), "max-throughput");
        assert_eq!(InferenceObjective::MinCost.to_string(), "min-cost");
        assert_eq!(InferenceObjective::Balanced.to_string(), "balanced");
    }
}
