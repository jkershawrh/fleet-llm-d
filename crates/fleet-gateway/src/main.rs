//! # fleet-gateway
//!
//! Cross-cluster routing proxy for the fleet-llm-d inference platform.
//!
//! The gateway accepts incoming inference requests and routes them to the
//! optimal cluster based on health, load, latency, cost, and placement policies.

mod balancer;
mod discovery;
mod health;
mod metrics;
mod router;

use axum::body::{to_bytes, Body};
use axum::extract::State;
use axum::http::header::{CONNECTION, HOST, TRANSFER_ENCODING};
use axum::http::{HeaderMap, HeaderValue, Request, Response, StatusCode};
use axum::response::IntoResponse;
use axum::routing::{any, get};
use axum::Router;
use balancer::LoadBalancer;
use clap::Parser;
use fleet_common::{ClusterId, ModelId, TenantId};
use std::collections::HashMap;
use std::net::SocketAddr;
use std::sync::atomic::{AtomicUsize, Ordering};
use std::sync::Arc;
use std::time::Duration;
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

    /// Optional bearer token for authenticated control-plane discovery.
    #[arg(long, env = "FLEET_CONTROL_PLANE_TOKEN")]
    pub control_plane_token: Option<String>,
}

#[derive(Debug, Clone)]
struct GatewayState {
    health_checker: health::HealthChecker,
    http: reqwest::Client,
    next_cluster: Arc<AtomicUsize>,
}

impl GatewayState {
    fn new(health_checker: health::HealthChecker) -> Self {
        Self {
            health_checker,
            http: build_gateway_client(Duration::from_secs(5), Duration::from_secs(120))
                .unwrap_or_default(),
            next_cluster: Arc::new(AtomicUsize::new(0)),
        }
    }
}

fn build_gateway_client(
    connect_timeout: Duration,
    idle_read_timeout: Duration,
) -> Result<reqwest::Client, reqwest::Error> {
    reqwest::Client::builder()
        .connect_timeout(connect_timeout)
        .read_timeout(idle_read_timeout)
        .build()
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
    let metrics = std::sync::Arc::new(metrics::GatewayMetrics::new());
    let gateway_state = GatewayState::new(health_checker.clone());
    bootstrap_runtime_contracts(&router, &metrics, &config.lb_strategy).await?;

    // Spawn tasks
    let health_checker_for_task = health_checker.clone();
    let health_checker_handle = tokio::spawn(async move {
        info!("health checker task started");
        health_checker_for_task.run().await
    });

    let discovery_checker = health_checker.clone();
    let discovery_url = config.control_plane_url.clone();
    let discovery_token = config.control_plane_token.clone();
    let discovery_handle = tokio::spawn(async move {
        info!(control_plane = %discovery_url, "cluster discovery task started");
        discovery::run(
            discovery_url,
            discovery_token,
            discovery_checker,
            std::time::Duration::from_secs(10),
        )
        .await
    });

    let gateway_port = config.gateway_port;
    let gateway_server_state = gateway_state.clone();
    let gateway_handle = tokio::spawn(async move {
        info!("gateway HTTP server task started");
        run_gateway_server(gateway_port, gateway_server_state).await
    });

    let metrics_port = config.metrics_port;
    let metrics_for_server = metrics.clone();
    let metrics_handle = tokio::spawn(async move {
        info!("metrics server task started");
        run_metrics_server(metrics_port, metrics_for_server).await
    });

    let health_port = config.health_port;
    let health_server_checker = health_checker.clone();
    let health_server_handle = tokio::spawn(async move {
        info!("health server task started");
        run_health_server(health_port, health_server_checker).await
    });

    tokio::select! {
        result = health_checker_handle => info!(?result, "health checker exited"),
        result = discovery_handle => info!(?result, "cluster discovery exited"),
        result = gateway_handle => info!(?result, "gateway server exited"),
        result = metrics_handle => info!(?result, "metrics server exited"),
        result = health_server_handle => info!(?result, "health server exited"),
    }

    Ok(())
}

async fn bootstrap_runtime_contracts(
    router: &router::FleetRouter,
    metrics: &metrics::GatewayMetrics,
    strategy: &str,
) -> anyhow::Result<()> {
    let cluster_id = ClusterId("bootstrap".to_string());

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

async fn run_gateway_server(port: u16, state: GatewayState) -> anyhow::Result<()> {
    let app = Router::new().fallback(any(gateway_proxy)).with_state(state);
    serve(port, app).await
}

async fn run_health_server(port: u16, health_checker: health::HealthChecker) -> anyhow::Result<()> {
    let app = Router::new()
        .route("/healthz", get(|| async { "ok" }))
        .route("/readyz", get(gateway_ready))
        .with_state(health_checker);
    serve(port, app).await
}

async fn gateway_ready(State(health_checker): State<health::HealthChecker>) -> impl IntoResponse {
    if health_checker
        .healthy_inference_endpoints()
        .await
        .is_empty()
    {
        (StatusCode::SERVICE_UNAVAILABLE, "no routable clusters")
    } else {
        (StatusCode::OK, "ready")
    }
}

async fn run_metrics_server(port: u16, metrics: std::sync::Arc<metrics::GatewayMetrics>) -> anyhow::Result<()> {
    let app = Router::new()
        .route("/healthz", get(|| async { "ok" }))
        .route(
            "/metrics",
            get(move || {
                let m = metrics.clone();
                async move {
                    let mut buf = String::new();
                    if prometheus_client::encoding::text::encode(&mut buf, &m.registry).is_ok() {
                        (
                            [(axum::http::header::CONTENT_TYPE, "text/plain; version=0.0.4; charset=utf-8")],
                            buf,
                        )
                    } else {
                        (
                            [(axum::http::header::CONTENT_TYPE, "text/plain")],
                            "# error encoding metrics\n".to_string(),
                        )
                    }
                }
            }),
        );
    serve(port, app).await
}

async fn gateway_proxy(
    State(state): State<GatewayState>,
    request: Request<Body>,
) -> Response<Body> {
    let endpoints = state.health_checker.healthy_inference_endpoints().await;
    if endpoints.is_empty() {
        return gateway_error(StatusCode::SERVICE_UNAVAILABLE, "no routable clusters");
    }
    let index = state.next_cluster.fetch_add(1, Ordering::Relaxed) % endpoints.len();
    let (cluster_id, base_url) = &endpoints[index];
    let (parts, body) = request.into_parts();
    let body = match to_bytes(body, 16 * 1024 * 1024).await {
        Ok(body) => body,
        Err(error) => return gateway_error(StatusCode::PAYLOAD_TOO_LARGE, &error.to_string()),
    };
    let path = parts
        .uri
        .path_and_query()
        .map(|value| value.as_str())
        .unwrap_or("/");
    let target = format!("{}{}", base_url.trim_end_matches('/'), path);
    let mut headers = parts.headers;
    remove_hop_by_hop_headers(&mut headers);
    if let Ok(value) = HeaderValue::from_str(&cluster_id.to_string()) {
        headers.insert("x-fleet-target-cluster", value);
    }

    let upstream = match state
        .http
        .request(parts.method, target)
        .headers(headers)
        .body(body)
        .send()
        .await
    {
        Ok(response) => response,
        Err(error) => return gateway_error(StatusCode::BAD_GATEWAY, &error.to_string()),
    };
    let status = upstream.status();
    let mut headers = upstream.headers().clone();
    remove_hop_by_hop_headers(&mut headers);
    let mut response = Response::new(Body::from_stream(upstream.bytes_stream()));
    *response.status_mut() = status;
    *response.headers_mut() = headers;
    response
}

fn remove_hop_by_hop_headers(headers: &mut HeaderMap) {
    headers.remove(HOST);
    headers.remove(CONNECTION);
    headers.remove(TRANSFER_ENCODING);
}

fn gateway_error(status: StatusCode, message: &str) -> Response<Body> {
    let mut response = Response::new(Body::from(message.to_string()));
    *response.status_mut() = status;
    response
}

async fn serve(port: u16, app: Router) -> anyhow::Result<()> {
    let addr = SocketAddr::from(([0, 0, 0, 0], port));
    let listener = tokio::net::TcpListener::bind(addr).await?;
    axum::serve(listener, app).await?;
    Ok(())
}

#[cfg(test)]
mod integration_tests {
    use super::*;
    use axum::routing::{any, get};
    use tokio::io::{AsyncReadExt, AsyncWriteExt};

    #[tokio::test]
    async fn ready_gateway_forwards_to_a_probed_cluster() {
        let upstream = Router::new()
            .route("/readyz", get(|| async { "ready" }))
            .route(
                "/v1/models",
                any(|headers: HeaderMap| async move {
                    headers
                        .get("x-fleet-target-cluster")
                        .and_then(|value| value.to_str().ok())
                        .unwrap_or("")
                        .to_string()
                }),
            );
        let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
        let address = listener.local_addr().unwrap();
        let server = tokio::spawn(async move { axum::serve(listener, upstream).await.unwrap() });
        let checker = health::HealthChecker::new(std::time::Duration::from_secs(10));
        checker
            .register_cluster(
                ClusterId("spoke-1".to_string()),
                format!("http://{address}/readyz"),
                format!("http://{address}"),
            )
            .await;
        checker.probe_once().await;

        assert_eq!(
            gateway_ready(State(checker.clone()))
                .await
                .into_response()
                .status(),
            StatusCode::OK
        );
        let request = Request::builder()
            .uri("/v1/models")
            .body(Body::empty())
            .unwrap();
        let response = gateway_proxy(State(GatewayState::new(checker)), request).await;
        assert_eq!(response.status(), StatusCode::OK);
        let body = to_bytes(response.into_body(), 1024).await.unwrap();
        assert_eq!(&body[..], b"spoke-1");
        server.abort();
    }

    #[tokio::test]
    async fn gateway_client_uses_idle_timeout_for_streaming_responses() {
        let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
        let address = listener.local_addr().unwrap();
        let server = tokio::spawn(async move {
            let (mut stream, _) = listener.accept().await.unwrap();
            let mut request = [0_u8; 1024];
            let _ = stream.read(&mut request).await.unwrap();
            stream
                .write_all(
                    b"HTTP/1.1 200 OK\r\nTransfer-Encoding: chunked\r\nConnection: close\r\n\r\n",
                )
                .await
                .unwrap();
            for chunk in [b"a", b"b", b"c", b"d"] {
                tokio::time::sleep(Duration::from_millis(150)).await;
                stream.write_all(b"1\r\n").await.unwrap();
                stream.write_all(chunk).await.unwrap();
                stream.write_all(b"\r\n").await.unwrap();
                stream.flush().await.unwrap();
            }
            stream.write_all(b"0\r\n\r\n").await.unwrap();
        });

        let client =
            build_gateway_client(Duration::from_secs(1), Duration::from_millis(500)).unwrap();
        let body = client
            .get(format!("http://{address}"))
            .send()
            .await
            .unwrap()
            .bytes()
            .await
            .unwrap();

        assert_eq!(&body[..], b"abcd");
        server.await.unwrap();
    }
}
