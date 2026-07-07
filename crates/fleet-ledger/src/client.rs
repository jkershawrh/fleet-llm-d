//! Async gRPC client for the ARE Immutable Ledger.

use fleet_common::FleetError;
use chrono::{DateTime, Utc};

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
}

impl LedgerClient {
    /// Creates a new LedgerClient.
    pub fn new(endpoint: &str, agent_id: &str, source_id: &str) -> Self {
        Self {
            endpoint: endpoint.to_owned(),
            agent_id: agent_id.to_owned(),
            source_id: source_id.to_owned(),
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
        let _ = (entry_type, content, correlation_id);
        Err(FleetError::Internal("not implemented: record_event".into()))
    }

    /// Issues a proof receipt for an event.
    pub async fn issue_receipt(
        &self,
        entry_type: &str,
        content: &[u8],
        input_hash: &str,
    ) -> Result<ProofReceipt, FleetError> {
        let _ = (entry_type, content, input_hash);
        Err(FleetError::Internal("not implemented: issue_receipt".into()))
    }

    /// Verifies a proof receipt by its entry hash.
    pub async fn verify_receipt(
        &self,
        entry_hash: &str,
        entry_type: &str,
    ) -> Result<bool, FleetError> {
        let _ = (entry_hash, entry_type);
        Err(FleetError::Internal("not implemented: verify_receipt".into()))
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
    async fn test_record_event_not_implemented() {
        let client = LedgerClient::new("http://localhost:50051", "agent-1", "fleet-agent");
        let result = client.record_event("fleet.placement.assigned", b"{}", None).await;
        assert!(result.is_err());
    }

    #[tokio::test]
    async fn test_issue_receipt_not_implemented() {
        let client = LedgerClient::new("http://localhost:50051", "agent-1", "fleet-agent");
        let result = client.issue_receipt("fleet.kvcache.transferred", b"{}", "abc123").await;
        assert!(result.is_err());
    }

    #[tokio::test]
    async fn test_verify_receipt_not_implemented() {
        let client = LedgerClient::new("http://localhost:50051", "agent-1", "fleet-agent");
        let result = client.verify_receipt("somehash", "fleet.placement.assigned").await;
        assert!(result.is_err());
    }
}
