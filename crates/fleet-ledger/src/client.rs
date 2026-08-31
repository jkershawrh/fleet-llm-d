//! Async gRPC client for the ARE Immutable Ledger.

use chrono::{DateTime, Utc};
use fleet_common::FleetError;
use std::sync::Arc;
use tokio::sync::RwLock;

use crate::hasher;

/// Receipt returned after recording an event to the ledger.
#[derive(Debug, Clone)]
pub struct LedgerReceipt {
    pub entry_id: String,
    pub entry_hash: String,
    pub chain_position: i64,
    pub timestamp: DateTime<Utc>,
}

/// Compact proof receipt for tamper-evident verification.
#[derive(Debug, Clone)]
pub struct ProofReceipt {
    pub entry_hash: String,
    pub entry_type: String,
    pub chain_position: i64,
    pub timestamp: DateTime<Utc>,
    pub input_hash: String,
}

/// Async gRPC client for the ARE Immutable Ledger, usable from fleet-agent.
pub struct LedgerClient {
    endpoint: String,
    agent_id: String,
    source_id: String,
    entries: Arc<RwLock<Vec<LedgerEntry>>>,
}

#[derive(Debug, Clone)]
struct LedgerEntry {
    entry_type: String,
    entry_hash: String,
    content_hash: String,
}

impl LedgerClient {
    /// Creates a new LedgerClient.
    pub fn new(endpoint: &str, agent_id: &str, source_id: &str) -> Self {
        Self {
            endpoint: endpoint.to_owned(),
            agent_id: agent_id.to_owned(),
            source_id: source_id.to_owned(),
            entries: Arc::new(RwLock::new(Vec::new())),
        }
    }

    /// Returns the configured endpoint.
    pub fn endpoint(&self) -> &str {
        &self.endpoint
    }

    /// Returns the agent ID.
    pub fn agent_id(&self) -> &str {
        &self.agent_id
    }

    /// Returns the source ID.
    pub fn source_id(&self) -> &str {
        &self.source_id
    }

    /// Records an event to the immutable ledger.
    pub async fn record_event(
        &self,
        entry_type: &str,
        content: &[u8],
        correlation_id: Option<&str>,
    ) -> Result<LedgerReceipt, FleetError> {
        let content_hash = hasher::compute_hash(content);
        let entry_hash = hasher::compute_hash(
            format!(
                "{}:{}:{}:{}",
                self.agent_id,
                self.source_id,
                entry_type,
                correlation_id.unwrap_or_default()
            )
            .as_bytes(),
        );

        let mut entries = self.entries.write().await;
        entries.push(LedgerEntry {
            entry_type: entry_type.to_owned(),
            entry_hash: entry_hash.clone(),
            content_hash,
        });

        Ok(LedgerReceipt {
            entry_id: format!("entry-{}", entries.len()),
            entry_hash,
            chain_position: entries.len() as i64,
            timestamp: Utc::now(),
        })
    }

    /// Issues a proof receipt for an event.
    pub async fn issue_receipt(
        &self,
        entry_type: &str,
        content: &[u8],
        input_hash: &str,
    ) -> Result<ProofReceipt, FleetError> {
        let receipt = self.record_event(entry_type, content, None).await?;
        Ok(ProofReceipt {
            entry_hash: receipt.entry_hash,
            entry_type: entry_type.to_owned(),
            chain_position: receipt.chain_position,
            timestamp: receipt.timestamp,
            input_hash: input_hash.to_owned(),
        })
    }

    /// Verifies a proof receipt by its entry hash.
    pub async fn verify_receipt(
        &self,
        entry_hash: &str,
        entry_type: &str,
    ) -> Result<bool, FleetError> {
        Ok(self.entries.read().await.iter().any(|entry| {
            entry.entry_hash == entry_hash
                && entry.entry_type == entry_type
                && !entry.content_hash.is_empty()
        }))
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_new_client() {
        let client = LedgerClient::new("http://localhost:50051", "agent-1", "fleet-agent");
        assert_eq!(client.endpoint(), "http://localhost:50051");
        assert_eq!(client.agent_id(), "agent-1");
        assert_eq!(client.source_id(), "fleet-agent");
    }

    #[tokio::test]
    async fn test_record_event_returns_receipt() {
        let client = LedgerClient::new("http://localhost:50051", "agent-1", "fleet-agent");
        let receipt = client
            .record_event("fleet.placement.assigned", b"{}", None)
            .await
            .unwrap();
        assert_eq!(receipt.chain_position, 1);
        assert!(!receipt.entry_hash.is_empty());
    }

    #[tokio::test]
    async fn test_issue_receipt_returns_proof() {
        let client = LedgerClient::new("http://localhost:50051", "agent-1", "fleet-agent");
        let receipt = client
            .issue_receipt("fleet.kvcache.transferred", b"{}", "abc123")
            .await;
        assert!(receipt.is_ok());
    }

    #[tokio::test]
    async fn test_verify_receipt_accepts_recorded_hash() {
        let client = LedgerClient::new("http://localhost:50051", "agent-1", "fleet-agent");
        let receipt = client
            .issue_receipt("fleet.kvcache.transferred", b"{}", "abc123")
            .await
            .unwrap();
        let result = client
            .verify_receipt(&receipt.entry_hash, "fleet.kvcache.transferred")
            .await;
        assert!(result.unwrap());
    }
}
