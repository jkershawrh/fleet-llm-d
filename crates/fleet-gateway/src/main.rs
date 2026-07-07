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

use clap::Parser;
use tracing::info;

/// Configuration for the fleet-gateway, parsed from CLI arguments.
#[derive(Debug, Clone, Parser)]
#[command(name = "fleet-gateway", about = "Cross-cluster fleet-llm-d routing proxy")]
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
        metrics_port = config.metrics_port,
        lb_strategy = %config.lb_strategy,
        "starting fleet-gateway"
    );

    // Build components
    let _health_checker = health::HealthChecker::new(
        std::time::Duration::from_secs(config.health_interval_secs),
    );
    let _router = router::FleetRouter::new();
    let _metrics = metrics::GatewayMetrics::new();

    // Spawn tasks
    let health_handle = tokio::spawn(async move {
        info!("health checker task started");
        // TODO: start periodic health probing
        std::future::pending::<()>().await;
    });

    let gateway_handle = tokio::spawn(async move {
        info!("gateway HTTP server task started");
        // TODO: start axum server on gateway_port
        std::future::pending::<()>().await;
    });

    let metrics_handle = tokio::spawn(async move {
        info!("metrics server task started");
        // TODO: start metrics endpoint on metrics_port
        std::future::pending::<()>().await;
    });

    tokio::select! {
        _ = health_handle => info!("health checker exited"),
        _ = gateway_handle => info!("gateway server exited"),
        _ = metrics_handle => info!("metrics server exited"),
    }

    Ok(())
}
