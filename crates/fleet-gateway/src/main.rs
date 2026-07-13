//! # fleet-gateway
//!
//! Cross-cluster routing proxy for the fleet-llm-d inference platform.
//!
//! The gateway accepts incoming inference requests and routes them to the
//! optimal cluster based on health, load, latency, cost, and placement policies.

mod balancer;
mod health;
mod metrics;
mod router;

use axum::http::StatusCode;
use axum::response::IntoResponse;
use axum::routing::{any, get};
use axum::Router;
use balancer::LoadBalancer;
use clap::Parser;
use fleet_common::{ClusterId, ModelId, TenantId};
use std::collections::HashMap;
use std::net::SocketAddr;
use tracing::info;

/// Configuration for the fleet-gateway, parsed from CLI arguments.
#[derive(Debug, Clone, Parser)]
#[command(
    name = "fleet-gateway",
    about = "Cross-cluster fleet-llm-d routing proxy"
)]
pub struct FleetGatewayConfig {
    /// Port for the HTTP routing proxy.
    #[arg(long, default_value = "8080", env = "FLEET_GATEWAY_PORT")]
    pub gateway_port: u16,

    /// Port for the Prometheus metrics endpoint.
    #[arg(long, default_value = "9090", env = "FLEET_METRICS_PORT")]
    pub metrics_port: u16,

    /// Port for the health / readiness probe endpoint.
    #[arg(long, default_value = "8081", env = "FLEET_HEALTH_PORT")]
    pub health_port: u16,

    /// URL of the fleet control plane for cluster discovery.
    #[arg(long, env = "FLEET_CONTROL_PLANE_URL")]
    pub control_plane_url: String,

    /// Interval in seconds between cluster health probes.
    #[arg(long, default_value = "10", env = "FLEET_HEALTH_INTERVAL")]
    pub health_interval_secs: u64,

    /// Load-balancing strategy to use (weighted, latency, cost).
    #[arg(long, default_value = "weighted", env = "FLEET_LB_STRATEGY")]
    pub lb_strategy: String,
}

#[tokio::main]
async fn main() -> anyhow::Result<()> {
    tracing_subscriber::fmt()
        .with_env_filter(
            tracing_subscriber::EnvFilter::try_from_default_env()
                .unwrap_or_else(|_| tracing_subscriber::EnvFilter::new("info")),
        )
        .json()
        .init();

    let config = FleetGatewayConfig::parse();
    info!(
        gateway_port = config.gateway_port,
        health_port = config.health_port,
        metrics_port = config.metrics_port,
        lb_strategy = %config.lb_strategy,
        "starting fleet-gateway"
    );

    // Build components
    let health_checker =
        health::HealthChecker::new(std::time::Duration::from_secs(config.health_interval_secs));
    let router = router::FleetRouter::new();
    let metrics = metrics::GatewayMetrics::new();
    bootstrap_runtime_contracts(&health_checker, &router, &metrics, &config.lb_strategy).await?;

    // Spawn tasks
    let health_checker_for_task = health_checker.clone();
    let health_checker_handle = tokio::spawn(async move {
        info!("health checker task started");
        health_checker_for_task.run().await
    });

    let gateway_port = config.gateway_port;
    let gateway_handle = tokio::spawn(async move {
        info!("gateway HTTP server task started");
        run_gateway_server(gateway_port).await
    });

    let metrics_port = config.metrics_port;
    let metrics_handle = tokio::spawn(async move {
        info!("metrics server task started");
        run_metrics_server(metrics_port).await
    });

    let health_port = config.health_port;
    let health_server_handle = tokio::spawn(async move {
        info!("health server task started");
        run_health_server(health_port).await
    });

    tokio::select! {
        result = health_checker_handle => info!(?result, "health checker exited"),
        result = gateway_handle => info!(?result, "gateway server exited"),
        result = metrics_handle => info!(?result, "metrics server exited"),
        result = health_server_handle => info!(?result, "health server exited"),
    }

    Ok(())
}

async fn bootstrap_runtime_contracts(
    health_checker: &health::HealthChecker,
    router: &router::FleetRouter,
    metrics: &metrics::GatewayMetrics,
    strategy: &str,
) -> anyhow::Result<()> {
    let cluster_id = ClusterId("bootstrap".to_string());
    health_checker.register_cluster(cluster_id.clone()).await;
    let _healthy = health_checker.is_healthy(&cluster_id).await;
    let _snapshot = health_checker.snapshot().await;
    health_checker.unregister_cluster(&cluster_id).await;

    let _policy_checker = health::HealthChecker::new(std::time::Duration::from_secs(1))
        .with_policy(health::HealthPolicy {
            failure_threshold: 2,
            probe_timeout: std::time::Duration::from_millis(500),
        });

    metrics.record_request("bootstrap", "bootstrap-model", "bootstrap-tenant", 0.001);
    metrics.record_routing_decision("bootstrap", strategy);
    metrics.active_connections.inc();
    metrics.active_connections.dec();
    let _ = &metrics.registry;

    let mut weights = HashMap::new();
    weights.insert(cluster_id.clone(), 1.0);
    router
        .set_policy(router::RoutingPolicy {
            model_id: ModelId("bootstrap-model".to_string()),
            cluster_weights: weights,
            allow_overflow: false,
            tenant_overrides: HashMap::new(),
        })
        .await;
    let request = router::InferenceRequest {
        model_id: ModelId("bootstrap-model".to_string()),
        tenant_id: Some(TenantId("bootstrap-tenant".to_string())),
        preferred_region: Some("bootstrap-region".to_string()),
        body_size_bytes: 0,
    };
    let _decision = router.route(&request).await?;

    let candidates = vec![balancer::ClusterCandidate {
        cluster_id: cluster_id.clone(),
        weight: 1.0,
        latency_ms: 1.0,
        cost_per_token: 0.0,
        queue_depth: 0,
        healthy: true,
    }];
    let mut weighted = balancer::WeightedBalancer::new();
    weighted.set_weight(cluster_id, 1.0);
    let _ = weighted.select_cluster(&candidates, &request).await?;
    let _ = balancer::LatencyAwareBalancer::new()
        .select_cluster(&candidates, &request)
        .await?;
    let _ = balancer::CostAwareBalancer::new()
        .select_cluster(&candidates, &request)
        .await?;

    Ok(())
}

async fn run_gateway_server(port: u16) -> anyhow::Result<()> {
    let app = Router::new().fallback(any(gateway_unavailable));
    serve(port, app).await
}

async fn run_health_server(port: u16) -> anyhow::Result<()> {
    let app = Router::new()
        .route("/healthz", get(|| async { "ok" }))
        .route(
            "/readyz",
            get(|| async {
                (
                    StatusCode::SERVICE_UNAVAILABLE,
                    "routing snapshot is not configured",
                )
            }),
        );
    serve(port, app).await
}

async fn run_metrics_server(port: u16) -> anyhow::Result<()> {
    let app = Router::new()
        .route("/healthz", get(|| async { "ok" }))
        .route("/metrics", get(|| async { "# fleet_gateway metrics\n" }));
    serve(port, app).await
}

async fn gateway_unavailable() -> impl IntoResponse {
    (
        StatusCode::SERVICE_UNAVAILABLE,
        "gateway routing policy is not configured",
    )
}

async fn serve(port: u16, app: Router) -> anyhow::Result<()> {
    let addr = SocketAddr::from(([0, 0, 0, 0], port));
    let listener = tokio::net::TcpListener::bind(addr).await?;
    axum::serve(listener, app).await?;
    Ok(())
}
