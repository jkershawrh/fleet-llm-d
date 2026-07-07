//! Kubernetes resource watcher for local llm-d workloads.
//!
//! Watches InferencePool custom resources, Pods bearing llm-d labels, and Node
//! resources via kube-rs, forwarding events to a [`ResourceEventHandler`].

use fleet_common::ClusterId;
use serde::{Deserialize, Serialize};

/// Metadata about a watched Kubernetes resource.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct ResourceMeta {
    /// Kubernetes namespace.
    pub namespace: String,
    /// Resource name.
    pub name: String,
    /// Resource kind (e.g. `InferencePool`, `Pod`, `Node`).
    pub kind: String,
    /// Resource version from the Kubernetes API.
    pub resource_version: String,
}

/// The kinds of Kubernetes resources the watcher monitors.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum WatchedResource {
    /// An llm-d InferencePool custom resource.
    InferencePool,
    /// A Pod with llm-d labels (e.g. `app.kubernetes.io/part-of=llm-d`).
    Pod,
    /// A cluster Node.
    Node,
}

/// Trait for handling resource lifecycle events from the Kubernetes API.
#[allow(async_fn_in_trait)]
pub trait ResourceEventHandler: Send + Sync {
    /// Called when a new resource is observed.
    async fn on_add(&self, meta: &ResourceMeta) -> anyhow::Result<()>;

    /// Called when an existing resource is modified.
    async fn on_update(&self, old: &ResourceMeta, new: &ResourceMeta) -> anyhow::Result<()>;

    /// Called when a resource is deleted.
    async fn on_delete(&self, meta: &ResourceMeta) -> anyhow::Result<()>;
}

/// Watches local Kubernetes resources relevant to llm-d and dispatches events.
#[derive(Debug, Clone)]
pub struct ResourceWatcher {
    /// The cluster this watcher belongs to.
    cluster_id: ClusterId,
}

impl ResourceWatcher {
    /// Create a new [`ResourceWatcher`] for the given cluster.
    pub fn new(cluster_id: ClusterId) -> Self {
        Self { cluster_id }
    }

    /// Returns the cluster this watcher is bound to.
    pub fn cluster_id(&self) -> &ClusterId {
        &self.cluster_id
    }

    /// Start watching all resource types. This method runs until cancelled.
    ///
    /// # Errors
    ///
    /// Returns an error if the Kubernetes client cannot be initialised or the
    /// watch stream terminates unexpectedly.
    pub async fn run(&self, _handler: impl ResourceEventHandler) -> anyhow::Result<()> {
        tracing::info!(cluster_id = %self.cluster_id, "starting resource watcher");

        // TODO: initialise kube::Client, set up watchers for InferencePool,
        // Pod (with label selector `app.kubernetes.io/part-of=llm-d`), and
        // Node resources. Forward events to the handler.
        std::future::pending::<()>().await;
        Ok(())
    }
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
}
