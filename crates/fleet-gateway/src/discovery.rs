//! Control-plane cluster discovery for gateway health monitoring.

use std::collections::{HashMap, HashSet};
use std::time::Duration;

use fleet_common::ClusterId;
use serde::Deserialize;

use crate::health::HealthChecker;

#[derive(Debug, Deserialize)]
struct DiscoveredCluster {
    #[serde(alias = "ID")]
    id: String,
    #[serde(default, alias = "Labels")]
    labels: HashMap<String, String>,
}

/// Poll the controller for registered clusters and keep the health checker in sync.
pub async fn run(
    control_plane_url: String,
    token: Option<String>,
    health_checker: HealthChecker,
    interval: Duration,
) -> anyhow::Result<()> {
    let client = reqwest::Client::builder()
        .timeout(Duration::from_secs(5))
        .build()?;
    let mut ticker = tokio::time::interval(interval);

    loop {
        ticker.tick().await;
        if let Err(error) = sync_once(
            &client,
            &control_plane_url,
            token.as_deref(),
            &health_checker,
        )
        .await
        {
            tracing::warn!(%error, "cluster discovery failed");
        }
    }
}

async fn sync_once(
    client: &reqwest::Client,
    control_plane_url: &str,
    token: Option<&str>,
    health_checker: &HealthChecker,
) -> anyhow::Result<()> {
    let url = format!(
        "{}/api/v1/clusters",
        control_plane_url.trim_end_matches('/')
    );
    let mut request = client.get(url);
    if let Some(token) = token.filter(|value| !value.is_empty()) {
        request = request.bearer_auth(token);
    }
    let clusters: Vec<DiscoveredCluster> = request.send().await?.error_for_status()?.json().await?;

    let mut desired = HashSet::new();
    for cluster in clusters {
        let Some(health_url) = cluster.labels.get("health_url") else {
            continue;
        };
        if health_url.is_empty() {
            continue;
        }
        let cluster_id = ClusterId(cluster.id);
        desired.insert(cluster_id.clone());
        health_checker
            .register_cluster_with_url(cluster_id, health_url.clone())
            .await;
    }

    for cluster_id in health_checker.snapshot().await.keys() {
        if !desired.contains(cluster_id) {
            health_checker.unregister_cluster(cluster_id).await;
        }
    }
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;
    use axum::routing::get;
    use axum::{Json, Router};

    #[test]
    fn accepts_controller_cluster_shape() {
        let cluster: DiscoveredCluster = serde_json::from_str(
            r#"{"id":"spoke-1","labels":{"health_url":"http://spoke-1/healthz"}}"#,
        )
        .unwrap();
        assert_eq!(cluster.id, "spoke-1");
        assert_eq!(
            cluster.labels.get("health_url").map(String::as_str),
            Some("http://spoke-1/healthz")
        );
    }

    #[tokio::test]
    async fn sync_registers_gateway_reachable_health_endpoint() {
        let app = Router::new().route(
            "/api/v1/clusters",
            get(|| async {
                Json(serde_json::json!([{
                    "id": "spoke-1",
                    "labels": {"health_url": "http://spoke-1/healthz"}
                }]))
            }),
        );
        let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
        let address = listener.local_addr().unwrap();
        let server = tokio::spawn(async move { axum::serve(listener, app).await.unwrap() });

        let checker = HealthChecker::new(Duration::from_secs(10));
        let client = reqwest::Client::new();
        sync_once(&client, &format!("http://{address}"), None, &checker)
            .await
            .unwrap();

        let cluster_id = ClusterId("spoke-1".to_string());
        assert!(checker.snapshot().await.contains_key(&cluster_id));
        assert!(!checker.snapshot().await[&cluster_id].healthy);
        server.abort();
    }
}
