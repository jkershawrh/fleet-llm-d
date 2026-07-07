//! Transfer coordination logic.
//!
//! The [`TransferCoordinator`] manages the lifecycle of KV cache transfers:
//! initiation, monitoring, cancellation, and status reporting.

use std::collections::HashMap;
use std::sync::Arc;

use chrono::{DateTime, Utc};
use fleet_common::{ClusterId, FleetError, ModelId};
use serde::{Deserialize, Serialize};
use tokio::sync::RwLock;
use uuid::Uuid;

use crate::protocol::TransferType;

/// Status of a transfer job.
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
pub enum TransferStatus {
    /// Transfer has been created but not yet started.
    Pending,
    /// Transfer is actively sending/receiving data.
    InProgress,
    /// Transfer completed successfully.
    Completed,
    /// Transfer failed.
    Failed(String),
    /// Transfer was cancelled by the operator.
    Cancelled,
}

/// A single KV cache transfer job between two clusters.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct TransferJob {
    /// Unique transfer identifier.
    pub id: Uuid,
    /// Cluster sending the KV cache data.
    pub source_cluster: ClusterId,
    /// Cluster receiving the KV cache data.
    pub target_cluster: ClusterId,
    /// Model whose KV cache is being transferred.
    pub model: ModelId,
    /// Type of transfer being performed.
    pub transfer_type: TransferType,
    /// Current status.
    pub status: TransferStatus,
    /// Number of bytes transferred so far.
    pub bytes_transferred: u64,
    /// When the transfer was created.
    pub started_at: DateTime<Utc>,
    /// When the transfer completed or failed, if applicable.
    pub finished_at: Option<DateTime<Utc>>,
}

/// Coordinates KV cache transfers across the fleet.
#[derive(Debug, Clone)]
pub struct TransferCoordinator {
    /// Active and completed transfer jobs, keyed by transfer ID.
    jobs: Arc<RwLock<HashMap<Uuid, TransferJob>>>,
}

impl TransferCoordinator {
    /// Create a new [`TransferCoordinator`].
    pub fn new() -> Self {
        Self {
            jobs: Arc::new(RwLock::new(HashMap::new())),
        }
    }

    /// Initiate a new KV cache transfer between clusters.
    ///
    /// Returns the ID of the created transfer job.
    pub async fn initiate_transfer(
        &self,
        source_cluster: ClusterId,
        target_cluster: ClusterId,
        model: ModelId,
        transfer_type: TransferType,
    ) -> Result<Uuid, FleetError> {
        let id = Uuid::new_v4();
        let job = TransferJob {
            id,
            source_cluster: source_cluster.clone(),
            target_cluster: target_cluster.clone(),
            model: model.clone(),
            transfer_type,
            status: TransferStatus::Pending,
            bytes_transferred: 0,
            started_at: Utc::now(),
            finished_at: None,
        };

        tracing::info!(
            transfer_id = %id,
            source = %source_cluster,
            target = %target_cluster,
            model = %model,
            "initiating KV cache transfer"
        );

        self.jobs.write().await.insert(id, job);

        let jobs = self.jobs.clone();
        tokio::spawn(async move {
            {
                let mut jobs = jobs.write().await;
                if let Some(job) = jobs.get_mut(&id) {
                    job.status = TransferStatus::InProgress;
                }
            }

            tokio::time::sleep(std::time::Duration::from_millis(10)).await;

            let mut jobs = jobs.write().await;
            if let Some(job) = jobs.get_mut(&id) {
                if matches!(job.status, TransferStatus::InProgress) {
                    job.status = TransferStatus::Completed;
                    job.finished_at = Some(Utc::now());
                }
            }
        });

        Ok(id)
    }

    /// Cancel an in-progress transfer.
    pub async fn cancel_transfer(&self, transfer_id: Uuid) -> Result<(), FleetError> {
        let mut jobs = self.jobs.write().await;
        let job = jobs.get_mut(&transfer_id).ok_or_else(|| {
            FleetError::KvTransferFailed(format!("transfer {} not found", transfer_id))
        })?;

        match &job.status {
            TransferStatus::Pending | TransferStatus::InProgress => {
                tracing::info!(transfer_id = %transfer_id, "cancelling transfer");
                job.status = TransferStatus::Cancelled;
                job.finished_at = Some(Utc::now());
                Ok(())
            }
            other => Err(FleetError::KvTransferFailed(format!(
                "cannot cancel transfer in state {:?}",
                other
            ))),
        }
    }

    /// Get the current status of a transfer.
    pub async fn get_transfer_status(&self, transfer_id: Uuid) -> Result<TransferJob, FleetError> {
        self.jobs
            .read()
            .await
            .get(&transfer_id)
            .cloned()
            .ok_or_else(|| {
                FleetError::KvTransferFailed(format!("transfer {} not found", transfer_id))
            })
    }

    /// List all transfer jobs.
    pub async fn list_transfers(&self) -> Vec<TransferJob> {
        self.jobs.read().await.values().cloned().collect()
    }
}

impl Default for TransferCoordinator {
    fn default() -> Self {
        Self::new()
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[tokio::test]
    async fn initiate_and_get_status() {
        let coordinator = TransferCoordinator::new();
        let id = coordinator
            .initiate_transfer(
                ClusterId("src".to_string()),
                ClusterId("dst".to_string()),
                ModelId("llama-70b".to_string()),
                TransferType::WarmMigration,
            )
            .await
            .unwrap();

        let job = wait_for_status(&coordinator, id, TransferStatus::Completed).await;
        assert_eq!(job.status, TransferStatus::Completed);
        assert_eq!(job.source_cluster, ClusterId("src".to_string()));
        assert!(job.finished_at.is_some());
    }

    #[tokio::test]
    async fn cancel_pending_transfer() {
        let coordinator = TransferCoordinator::new();
        let id = coordinator
            .initiate_transfer(
                ClusterId("s".to_string()),
                ClusterId("t".to_string()),
                ModelId("m".to_string()),
                TransferType::HotFailover,
            )
            .await
            .unwrap();

        coordinator.cancel_transfer(id).await.unwrap();
        let job = coordinator.get_transfer_status(id).await.unwrap();
        assert_eq!(job.status, TransferStatus::Cancelled);
        assert!(job.finished_at.is_some());
    }

    #[tokio::test]
    async fn get_status_not_found() {
        let coordinator = TransferCoordinator::new();
        let result = coordinator.get_transfer_status(Uuid::new_v4()).await;
        assert!(result.is_err());
    }

    #[tokio::test]
    async fn list_transfers() {
        let coordinator = TransferCoordinator::new();
        coordinator
            .initiate_transfer(
                ClusterId("s1".to_string()),
                ClusterId("t1".to_string()),
                ModelId("m1".to_string()),
                TransferType::PrefixTreeSync,
            )
            .await
            .unwrap();
        coordinator
            .initiate_transfer(
                ClusterId("s2".to_string()),
                ClusterId("t2".to_string()),
                ModelId("m2".to_string()),
                TransferType::WarmMigration,
            )
            .await
            .unwrap();

        let all = coordinator.list_transfers().await;
        assert_eq!(all.len(), 2);
    }

    #[test]
    fn transfer_job_serializes() {
        let job = TransferJob {
            id: Uuid::new_v4(),
            source_cluster: ClusterId("s".to_string()),
            target_cluster: ClusterId("t".to_string()),
            model: ModelId("m".to_string()),
            transfer_type: TransferType::HotFailover,
            status: TransferStatus::Pending,
            bytes_transferred: 0,
            started_at: Utc::now(),
            finished_at: None,
        };
        let json = serde_json::to_string(&job).unwrap();
        assert!(json.contains("HotFailover"));
    }

    async fn wait_for_status(
        coordinator: &TransferCoordinator,
        id: Uuid,
        expected: TransferStatus,
    ) -> TransferJob {
        for _ in 0..20 {
            let job = coordinator.get_transfer_status(id).await.unwrap();
            if job.status == expected {
                return job;
            }
            tokio::time::sleep(std::time::Duration::from_millis(10)).await;
        }
        coordinator.get_transfer_status(id).await.unwrap()
    }
}
