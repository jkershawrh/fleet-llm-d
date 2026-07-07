//! Prometheus metrics for the fleet-gateway.
//!
//! Exposes request counters, latency histograms, and connection gauges that
//! give visibility into cross-cluster routing behaviour.

use prometheus_client::encoding::EncodeLabelSet;
use prometheus_client::metrics::counter::Counter;
use prometheus_client::metrics::family::Family;
use prometheus_client::metrics::gauge::Gauge;
use prometheus_client::metrics::histogram::{exponential_buckets, Histogram};
use prometheus_client::registry::Registry;

/// Label set for per-request metrics (requests_total, request_duration).
#[derive(Clone, Debug, Hash, PartialEq, Eq, EncodeLabelSet)]
pub struct RequestLabels {
    /// Target cluster that handled the request.
    pub cluster: String,
    /// Model that was requested.
    pub model: String,
    /// Tenant that issued the request.
    pub tenant: String,
}

/// Label set for routing-decision counters.
#[derive(Clone, Debug, Hash, PartialEq, Eq, EncodeLabelSet)]
pub struct RoutingLabels {
    /// Cluster selected by the router.
    pub cluster: String,
    /// Balancing strategy used.
    pub strategy: String,
}

/// Prometheus metrics exported by the fleet-gateway.
#[derive(Debug)]
pub struct GatewayMetrics {
    /// Total number of requests routed, labelled by cluster, model, and tenant.
    pub requests_total: Family<RequestLabels, Counter>,

    /// Histogram of request durations in seconds, labelled by cluster, model,
    /// and tenant.
    pub request_duration_seconds: Family<RequestLabels, Histogram>,

    /// Number of currently active connections across all clusters.
    pub active_connections: Gauge,

    /// Total number of routing decisions made, labelled by cluster and strategy.
    pub routing_decisions_total: Family<RoutingLabels, Counter>,

    /// Prometheus registry holding all metrics.
    pub registry: Registry,
}

impl GatewayMetrics {
    /// Create a new [`GatewayMetrics`] and register all metrics with the
    /// internal registry.
    pub fn new() -> Self {
        let mut registry = Registry::default();

        let requests_total = Family::<RequestLabels, Counter>::default();
        registry.register(
            "fleet_gateway_requests_total",
            "Total number of inference requests routed",
            requests_total.clone(),
        );

        let request_duration_seconds =
            Family::<RequestLabels, Histogram>::new_with_constructor(|| {
                Histogram::new(exponential_buckets(0.001, 2.0, 15))
            });
        registry.register(
            "fleet_gateway_request_duration_seconds",
            "Histogram of request durations in seconds",
            request_duration_seconds.clone(),
        );

        let active_connections = Gauge::default();
        registry.register(
            "fleet_gateway_active_connections",
            "Number of currently active connections",
            active_connections.clone(),
        );

        let routing_decisions_total = Family::<RoutingLabels, Counter>::default();
        registry.register(
            "fleet_gateway_routing_decisions_total",
            "Total routing decisions by cluster and strategy",
            routing_decisions_total.clone(),
        );

        Self {
            requests_total,
            request_duration_seconds,
            active_connections,
            routing_decisions_total,
            registry,
        }
    }

    /// Record a completed request.
    pub fn record_request(
        &self,
        cluster: &str,
        model: &str,
        tenant: &str,
        duration_secs: f64,
    ) {
        let labels = RequestLabels {
            cluster: cluster.to_string(),
            model: model.to_string(),
            tenant: tenant.to_string(),
        };
        self.requests_total.get_or_create(&labels).inc();
        self.request_duration_seconds
            .get_or_create(&labels)
            .observe(duration_secs);
    }

    /// Record a routing decision.
    pub fn record_routing_decision(&self, cluster: &str, strategy: &str) {
        let labels = RoutingLabels {
            cluster: cluster.to_string(),
            strategy: strategy.to_string(),
        };
        self.routing_decisions_total.get_or_create(&labels).inc();
    }
}

impl Default for GatewayMetrics {
    fn default() -> Self {
        Self::new()
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn metrics_can_be_created() {
        let metrics = GatewayMetrics::new();
        // Verify the registry has registered families.
        assert_eq!(metrics.active_connections.get(), 0);
    }

    #[test]
    fn record_request_increments_counter() {
        let metrics = GatewayMetrics::new();
        metrics.record_request("c1", "llama-70b", "acme", 0.5);
        metrics.record_request("c1", "llama-70b", "acme", 0.3);

        let labels = RequestLabels {
            cluster: "c1".to_string(),
            model: "llama-70b".to_string(),
            tenant: "acme".to_string(),
        };
        assert_eq!(metrics.requests_total.get_or_create(&labels).get(), 2);
    }

    #[test]
    fn record_routing_decision_increments() {
        let metrics = GatewayMetrics::new();
        metrics.record_routing_decision("c1", "weighted");
        let labels = RoutingLabels {
            cluster: "c1".to_string(),
            strategy: "weighted".to_string(),
        };
        assert_eq!(
            metrics.routing_decisions_total.get_or_create(&labels).get(),
            1
        );
    }

    #[test]
    fn active_connections_gauge() {
        let metrics = GatewayMetrics::new();
        metrics.active_connections.inc();
        metrics.active_connections.inc();
        assert_eq!(metrics.active_connections.get(), 2);
        metrics.active_connections.dec();
        assert_eq!(metrics.active_connections.get(), 1);
    }
}
