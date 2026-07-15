//! Local inference traffic proxy.
//!
//! Intercepts inference requests on the local cluster and injects fleet-level
//! headers before forwarding to the llm-d EPP endpoint. This enables the EPP
//! scheduler to apply fleet-aware fairness and routing policies.

use serde::{Deserialize, Serialize};
use std::net::SocketAddr;

use axum::body::{to_bytes, Body};
use axum::extract::State;
use axum::http::header::{CONNECTION, HOST, TRANSFER_ENCODING};
use axum::http::{HeaderMap, HeaderValue, Request, Response, StatusCode};
use axum::response::IntoResponse;
use axum::routing::{any, get};
use axum::Router;

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
    /// Shared HTTP client used for readiness probes and request forwarding.
    http: reqwest::Client,
}

impl LocalProxy {
    /// Create a new [`LocalProxy`] listening on the given port.
    pub fn new(listen_port: u16) -> Self {
        Self {
            listen_port,
            // Default upstream; will be configurable.
            upstream_url: "http://localhost:8000".to_string(),
            http: reqwest::Client::builder()
                .connect_timeout(std::time::Duration::from_secs(5))
                .build()
                .unwrap_or_default(),
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

        let app = Router::new()
            .route("/healthz", get(|| async { "ok" }))
            .route("/readyz", get(proxy_ready))
            .fallback(any(proxy_request))
            .with_state(self.clone());

        let addr = SocketAddr::from(([0, 0, 0, 0], self.listen_port));
        let listener = tokio::net::TcpListener::bind(addr).await?;
        axum::serve(listener, app).await?;
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

async fn proxy_ready(State(proxy): State<LocalProxy>) -> impl IntoResponse {
    let url = format!("{}/healthz", proxy.upstream_url.trim_end_matches('/'));
    match tokio::time::timeout(
        std::time::Duration::from_secs(5),
        proxy.http.get(url).send(),
    )
    .await
    {
        Ok(Ok(response)) if response.status().is_success() => (StatusCode::OK, "ready"),
        Ok(Ok(_)) => (StatusCode::SERVICE_UNAVAILABLE, "upstream is not ready"),
        Ok(Err(_)) | Err(_) => (StatusCode::SERVICE_UNAVAILABLE, "upstream is unreachable"),
    }
}

async fn proxy_request(State(proxy): State<LocalProxy>, request: Request<Body>) -> Response<Body> {
    let (parts, body) = request.into_parts();
    let headers = &parts.headers;
    let tenant = headers
        .get(HEADER_TENANT_ID)
        .and_then(|v| v.to_str().ok())
        .or_else(|| {
            headers
                .get(HEADER_FAIRNESS_ID)
                .and_then(|v| v.to_str().ok())
        });
    let injected = proxy.build_headers(tenant, Some(InferenceObjective::Balanced), None);

    let mut upstream_headers = headers.clone();
    remove_hop_by_hop_headers(&mut upstream_headers);
    if let Some(fairness_id) = injected.fairness_id {
        if let Ok(value) = HeaderValue::from_str(&fairness_id) {
            upstream_headers.insert(HEADER_FAIRNESS_ID, value);
        }
    }
    if let Some(tenant_id) = injected.tenant_id {
        if let Ok(value) = HeaderValue::from_str(&tenant_id) {
            upstream_headers.insert(HEADER_TENANT_ID, value);
        }
    }
    upstream_headers.insert(HEADER_SOURCE_CLUSTER, HeaderValue::from_static("local"));
    upstream_headers.insert(
        HEADER_INFERENCE_OBJECTIVE,
        HeaderValue::from_static("balanced"),
    );

    let body = match to_bytes(body, 16 * 1024 * 1024).await {
        Ok(body) => body,
        Err(error) => return proxy_error(StatusCode::PAYLOAD_TOO_LARGE, error.to_string()),
    };
    let path = parts
        .uri
        .path_and_query()
        .map(|value| value.as_str())
        .unwrap_or("/");
    let target = format!("{}{}", proxy.upstream_url.trim_end_matches('/'), path);

    tracing::debug!(
        upstream = %target,
        body_bytes = body.len(),
        "forwarding local proxy request"
    );

    let upstream = match proxy
        .http
        .request(parts.method, target)
        .headers(upstream_headers)
        .body(body)
        .send()
        .await
    {
        Ok(response) => response,
        Err(error) => return proxy_error(StatusCode::BAD_GATEWAY, error.to_string()),
    };

    let status = upstream.status();
    let mut response_headers = upstream.headers().clone();
    remove_hop_by_hop_headers(&mut response_headers);
    let mut response = Response::new(Body::from_stream(upstream.bytes_stream()));
    *response.status_mut() = status;
    *response.headers_mut() = response_headers;
    response
}

fn remove_hop_by_hop_headers(headers: &mut HeaderMap) {
    headers.remove(HOST);
    headers.remove(CONNECTION);
    headers.remove(TRANSFER_ENCODING);
}

fn proxy_error(status: StatusCode, message: String) -> Response<Body> {
    let mut response = Response::new(Body::from(message));
    *response.status_mut() = status;
    response
}

#[cfg(test)]
mod tests {
    use super::*;
    use axum::routing::{any, get};
    use axum::Router;

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
        assert_eq!(
            InferenceObjective::MaxThroughput.to_string(),
            "max-throughput"
        );
        assert_eq!(InferenceObjective::MinCost.to_string(), "min-cost");
        assert_eq!(InferenceObjective::Balanced.to_string(), "balanced");
    }

    #[tokio::test]
    async fn proxy_forwards_method_path_body_and_fleet_headers() {
        let upstream = Router::new()
            .route("/healthz", get(|| async { "ok" }))
            .route(
                "/v1/completions",
                any(|headers: HeaderMap, body: axum::body::Bytes| async move {
                    let tenant = headers
                        .get(HEADER_TENANT_ID)
                        .and_then(|value| value.to_str().ok())
                        .unwrap_or("");
                    format!("{tenant}:{}", String::from_utf8_lossy(&body))
                }),
            );
        let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
        let address = listener.local_addr().unwrap();
        let server = tokio::spawn(async move { axum::serve(listener, upstream).await.unwrap() });
        let proxy = LocalProxy::new(8090).with_upstream(format!("http://{address}"));

        let request = Request::builder()
            .method("POST")
            .uri("/v1/completions")
            .header(HEADER_TENANT_ID, "tenant-a")
            .body(Body::from("prompt"))
            .unwrap();
        let response = proxy_request(State(proxy.clone()), request).await;
        assert_eq!(response.status(), StatusCode::OK);
        let body = to_bytes(response.into_body(), 1024).await.unwrap();
        assert_eq!(&body[..], b"tenant-a:prompt");

        assert_eq!(
            proxy_ready(State(proxy)).await.into_response().status(),
            StatusCode::OK
        );
        server.abort();
    }
}
