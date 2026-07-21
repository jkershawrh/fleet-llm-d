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
use fleet_common::{ClusterId, ModelId, TenantId};
use tracing::info;

struct LoggingResourceHandler;

impl watcher::ResourceEventHandler for LoggingResourceHandler {
    async fn on_add(&self, meta: &watcher::ResourceMeta) -> anyhow::Result<()> {
        tracing::info!(?meta, "resource added");
        Ok(())
    }

    async fn on_update(
        &self,
        old: &watcher::ResourceMeta,
        new: &watcher::ResourceMeta,
    ) -> anyhow::Result<()> {
        tracing::info!(?old, ?new, "resource updated");
        Ok(())
    }

    async fn on_delete(&self, meta: &watcher::ResourceMeta) -> anyhow::Result<()> {
        tracing::info!(?meta, "resource deleted");
        Ok(())
    }
}

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

    /// Optional bearer token for authenticated control-plane ingestion.
    #[arg(long, env = "FLEET_CONTROL_PLANE_TOKEN")]
    pub control_plane_token: Option<String>,

    /// Gateway-reachable health endpoint advertised to the control plane.
    #[arg(long, default_value = "", env = "FLEET_CLUSTER_HEALTH_URL")]
    pub cluster_health_url: String,

    /// Gateway-reachable inference proxy base URL advertised to the control plane.
    #[arg(long, default_value = "", env = "FLEET_CLUSTER_INFERENCE_URL")]
    pub cluster_inference_url: String,

    /// Local llm-d EPP endpoint that receives proxied inference requests.
    #[arg(
        long,
        default_value = "http://localhost:8000",
        env = "FLEET_UPSTREAM_URL"
    )]
    pub upstream_url: String,

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

    let cluster_id = ClusterId(config.cluster_id.clone());

    // Build components
    let watcher = watcher::ResourceWatcher::new(cluster_id.clone());
    let _ = watcher.cluster_id();
    let _watched_resources = [
        watcher::WatchedResource::InferencePool,
        watcher::WatchedResource::Pod,
        watcher::WatchedResource::Node,
    ];
    let reporter = reporter::MetricsReporter::new(
        config.control_plane_url.clone(),
        cluster_id.clone(),
        config.local_prometheus_url.clone(),
    )
    .with_interval(15)
    .with_token(config.control_plane_token.clone())
    .with_health_url(config.cluster_health_url.clone())
    .with_inference_url(config.cluster_inference_url.clone());
    let enforcer = enforcer::PolicyEnforcerImpl::new(cluster_id.clone());
    let _ = enforcer.cluster_id();
    enforcer
        .set_quota(
            TenantId("bootstrap".to_string()),
            enforcer::TenantQuota {
                max_rps: 100.0,
                max_concurrent: 100,
                max_tokens_per_minute: 1_000_000,
            },
        )
        .await;
    enforcer
        .set_placement(enforcer::PlacementConstraint {
            allowed_models: vec![ModelId("bootstrap-model".to_string())],
            denied_models: Vec::new(),
        })
        .await;
    let proxy =
        proxy::LocalProxy::new(config.proxy_port).with_upstream(config.upstream_url.clone());
    let _ = proxy.listen_port();

    // Spawn tasks
    let watcher_handle = tokio::spawn(async move {
        info!("resource watcher task started");
        watcher.run(LoggingResourceHandler).await
    });

    let reporter_handle = tokio::spawn(async move {
        info!("metrics reporter task started");
        reporter.run().await
    });

    let enforcer_cp_url = config.control_plane_url.clone();
    let enforcer_token = config.control_plane_token.clone().unwrap_or_default();
    let enforcer_handle = tokio::spawn(async move {
        info!("policy enforcer task started");
        enforcer.run(&enforcer_cp_url, &enforcer_token).await
    });

    let proxy_handle = tokio::spawn(async move {
        info!("local proxy task started");
        proxy.run().await
    });

    tokio::select! {
        result = watcher_handle => info!(?result, "watcher exited"),
        result = reporter_handle => info!(?result, "reporter exited"),
        result = enforcer_handle => info!(?result, "enforcer exited"),
        result = proxy_handle => info!(?result, "proxy exited"),
    }

    Ok(())
}
