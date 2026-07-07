//! # fleet-agent
//!
//! Per-cluster agent that watches local llm-d resources (InferencePool, Pods,
//! Nodes) and reports cluster status, inference metrics, and fleet events to the
//! control plane. Also enforces fleet policies locally and proxies inference
//! traffic to inject fleet headers.

mod enforcer;
mod proxy;
mod reporter;
mod watcher;

use clap::Parser;
use tracing::info;

/// Configuration for the fleet-agent, parsed from CLI arguments.
#[derive(Debug, Clone, Parser)]
#[command(name = "fleet-agent", about = "Per-cluster fleet-llm-d agent")]
pub struct FleetAgentConfig {
    /// URL of the fleet control plane.
    #[arg(long, env = "FLEET_CONTROL_PLANE_URL")]
    pub control_plane_url: String,

    /// Identity of this cluster.
    #[arg(long, env = "FLEET_CLUSTER_ID")]
    pub cluster_id: String,

    /// Port for the agent gRPC / health server.
    #[arg(long, default_value = "8080", env = "FLEET_AGENT_PORT")]
    pub agent_port: u16,

    /// Port for the Prometheus metrics endpoint.
    #[arg(long, default_value = "9090", env = "FLEET_METRICS_PORT")]
    pub metrics_port: u16,

    /// Local Prometheus endpoint to scrape llm-d EPP metrics from.
    #[arg(
        long,
        default_value = "http://localhost:9090",
        env = "FLEET_LOCAL_PROMETHEUS_URL"
    )]
    pub local_prometheus_url: String,

    /// Port for the local inference proxy.
    #[arg(long, default_value = "8090", env = "FLEET_PROXY_PORT")]
    pub proxy_port: u16,
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

    let config = FleetAgentConfig::parse();
    info!(
        cluster_id = %config.cluster_id,
        agent_port = config.agent_port,
        metrics_port = config.metrics_port,
        "starting fleet-agent"
    );

    let cluster_id = fleet_common::ClusterId(config.cluster_id.clone());

    // Build components
    let _watcher = watcher::ResourceWatcher::new(cluster_id.clone());
    let _reporter = reporter::MetricsReporter::new(
        config.control_plane_url.clone(),
        cluster_id.clone(),
        config.local_prometheus_url.clone(),
    );
    let _enforcer = enforcer::PolicyEnforcerImpl::new(cluster_id.clone());
    let _proxy = proxy::LocalProxy::new(config.proxy_port);

    // Spawn tasks
    let watcher_handle = tokio::spawn(async move {
        info!("resource watcher task started");
        // TODO: start kube-rs watchers
        std::future::pending::<()>().await;
    });

    let reporter_handle = tokio::spawn(async move {
        info!("metrics reporter task started");
        // TODO: periodic metrics collection and reporting
        std::future::pending::<()>().await;
    });

    let enforcer_handle = tokio::spawn(async move {
        info!("policy enforcer task started");
        // TODO: policy sync loop
        std::future::pending::<()>().await;
    });

    let proxy_handle = tokio::spawn(async move {
        info!("local proxy task started");
        // TODO: start axum proxy server
        std::future::pending::<()>().await;
    });

    tokio::select! {
        _ = watcher_handle => info!("watcher exited"),
        _ = reporter_handle => info!("reporter exited"),
        _ = enforcer_handle => info!("enforcer exited"),
        _ = proxy_handle => info!("proxy exited"),
    }

    Ok(())
}
