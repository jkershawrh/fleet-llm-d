//! Kubernetes resource watcher for local llm-d workloads.
//!
//! Watches Pods bearing llm-d labels and Node resources via kube-rs,
//! forwarding events to a [`ResourceEventHandler`].

use fleet_common::ClusterId;
use futures::TryStreamExt;
use k8s_openapi::api::core::v1::{Node, Pod};
use kube::{
    api::{Api, ListParams},
    runtime::watcher::{self, Event},
    Client,
};
use serde::{Deserialize, Serialize};
use std::collections::HashMap;
use std::sync::Arc;
use tokio::sync::RwLock;

/// Metadata about a watched Kubernetes resource.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct ResourceMeta {
    pub namespace: String,
    pub name: String,
    pub kind: String,
    pub resource_version: String,
}

/// The kinds of Kubernetes resources the watcher monitors.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum WatchedResource {
    InferencePool,
    Pod,
    Node,
}

/// Trait for handling resource lifecycle events from the Kubernetes API.
#[allow(async_fn_in_trait)]
pub trait ResourceEventHandler: Send + Sync {
    async fn on_add(&self, meta: &ResourceMeta) -> anyhow::Result<()>;
    async fn on_update(&self, old: &ResourceMeta, new: &ResourceMeta) -> anyhow::Result<()>;
    async fn on_delete(&self, meta: &ResourceMeta) -> anyhow::Result<()>;
}

type Seen = Arc<RwLock<HashMap<String, ResourceMeta>>>;

/// Watches local Kubernetes resources relevant to llm-d and dispatches events.
#[derive(Debug, Clone)]
pub struct ResourceWatcher {
    cluster_id: ClusterId,
    namespace: String,
}

impl ResourceWatcher {
    pub fn new(cluster_id: ClusterId) -> Self {
        Self {
            cluster_id,
            namespace: "default".to_string(),
        }
    }

    #[allow(dead_code)]
    pub fn with_namespace(mut self, ns: impl Into<String>) -> Self {
        self.namespace = ns.into();
        self
    }

    pub fn cluster_id(&self) -> &ClusterId {
        &self.cluster_id
    }

    /// Start watching Pods (with llm-d labels) and Nodes. Runs until cancelled.
    pub async fn run(&self, handler: impl ResourceEventHandler) -> anyhow::Result<()> {
        let client = match Client::try_default().await {
            Ok(c) => c,
            Err(e) => {
                tracing::warn!(error = %e, "kube client unavailable, falling back to heartbeat mode");
                return self.run_heartbeat_fallback(&handler).await;
            }
        };

        tracing::info!(cluster_id = %self.cluster_id, namespace = %self.namespace, "starting resource watcher");

        let pod_seen: Seen = Arc::new(RwLock::new(HashMap::new()));
        let node_seen: Seen = Arc::new(RwLock::new(HashMap::new()));

        let pods: Api<Pod> = Api::namespaced(client.clone(), &self.namespace);
        let nodes: Api<Node> = Api::all(client);

        let _pod_lp = ListParams::default().labels("app.kubernetes.io/part-of=llm-d");
        let _node_lp = ListParams::default();

        let pod_seen_c = pod_seen.clone();
        let pod_stream = async {
            let stream = watcher::watcher(pods, watcher::Config::default().labels("app.kubernetes.io/part-of=llm-d"));
            futures::pin_mut!(stream);
            while let Some(event) = stream.try_next().await? {
                handle_event(&handler, &pod_seen_c, "Pod", &self.namespace, event).await?;
            }
            Ok::<(), anyhow::Error>(())
        };

        let node_seen_c = node_seen.clone();
        let ns = self.namespace.clone();
        let node_stream = async {
            let stream = watcher::watcher(nodes, watcher::Config::default());
            futures::pin_mut!(stream);
            while let Some(event) = stream.try_next().await? {
                handle_event(&handler, &node_seen_c, "Node", &ns, event).await?;
            }
            Ok::<(), anyhow::Error>(())
        };

        tokio::select! {
            r = pod_stream => r?,
            r = node_stream => r?,
        }

        Ok(())
    }

    async fn run_heartbeat_fallback(&self, handler: &impl ResourceEventHandler) -> anyhow::Result<()> {
        tracing::info!(cluster_id = %self.cluster_id, "using heartbeat fallback (no kube API access)");
        let mut interval = tokio::time::interval(tokio::time::Duration::from_secs(30));
        loop {
            interval.tick().await;
            let meta = ResourceMeta {
                namespace: self.namespace.clone(),
                name: self.cluster_id.to_string(),
                kind: "Heartbeat".to_string(),
                resource_version: chrono::Utc::now().timestamp().to_string(),
            };
            handler.on_add(&meta).await?;
        }
    }
}

async fn handle_event<K>(
    handler: &impl ResourceEventHandler,
    seen: &Seen,
    kind: &str,
    namespace: &str,
    event: Event<K>,
) -> anyhow::Result<()>
where
    K: kube::Resource,
{
    match event {
        Event::Apply(obj) | Event::InitApply(obj) => {
            let name = obj.meta().name.clone().unwrap_or_default();
            let rv = obj.meta().resource_version.clone().unwrap_or_default();
            let ns = obj.meta().namespace.clone().unwrap_or_else(|| namespace.to_string());
            let meta = ResourceMeta {
                namespace: ns,
                name: name.clone(),
                kind: kind.to_string(),
                resource_version: rv,
            };
            let mut map = seen.write().await;
            if let Some(old) = map.get(&name) {
                let old_clone = old.clone();
                map.insert(name, meta.clone());
                drop(map);
                handler.on_update(&old_clone, &meta).await?;
            } else {
                map.insert(name, meta.clone());
                drop(map);
                handler.on_add(&meta).await?;
            }
        }
        Event::Delete(obj) => {
            let name = obj.meta().name.clone().unwrap_or_default();
            let rv = obj.meta().resource_version.clone().unwrap_or_default();
            let ns = obj.meta().namespace.clone().unwrap_or_else(|| namespace.to_string());
            let meta = ResourceMeta {
                namespace: ns,
                name: name.clone(),
                kind: kind.to_string(),
                resource_version: rv,
            };
            seen.write().await.remove(&name);
            handler.on_delete(&meta).await?;
        }
        Event::Init | Event::InitDone => {}
    }
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn resource_watcher_stores_cluster_id() {
        let watcher = ResourceWatcher::new(ClusterId("test-cluster".to_string()));
        assert_eq!(watcher.cluster_id().to_string(), "test-cluster");
    }

    #[test]
    fn watched_resource_equality() {
        assert_eq!(WatchedResource::InferencePool, WatchedResource::InferencePool);
        assert_ne!(WatchedResource::Pod, WatchedResource::Node);
    }

    #[test]
    fn resource_meta_serializes() {
        let meta = ResourceMeta {
            namespace: "default".to_string(),
            name: "pool-0".to_string(),
            kind: "InferencePool".to_string(),
            resource_version: "12345".to_string(),
        };
        let json = serde_json::to_string(&meta).unwrap();
        assert!(json.contains("InferencePool"));
    }

    #[test]
    fn watcher_with_namespace() {
        let w = ResourceWatcher::new(ClusterId("c1".to_string())).with_namespace("fleet-llm-d");
        assert_eq!(w.namespace, "fleet-llm-d");
    }
}
