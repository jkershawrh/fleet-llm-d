//! Transfer protocol abstraction.
//!
//! Defines the [`TransferProtocol`] trait that transport backends must implement,
//! along with the [`TransferType`] enum describing the semantics of a transfer.

use serde::{Deserialize, Serialize};

/// The type of KV cache transfer, which determines the transfer strategy and
/// urgency.
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
pub enum TransferType {
    /// Urgent transfer for failover scenarios. Prioritises speed over
    /// bandwidth efficiency.
    HotFailover,
    /// Planned migration of KV cache data, e.g. during cluster drain or
    /// rebalancing. Allows background streaming.
    WarmMigration,
    /// Synchronisation of shared prefix trees across clusters to improve
    /// cache hit rates for common prompts.
    PrefixTreeSync,
}

impl std::fmt::Display for TransferType {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        match self {
            Self::HotFailover => write!(f, "hot-failover"),
            Self::WarmMigration => write!(f, "warm-migration"),
            Self::PrefixTreeSync => write!(f, "prefix-tree-sync"),
        }
    }
}

/// A block of KV cache data being transferred.
#[derive(Debug, Clone)]
pub struct KvBlock {
    /// Sequence number within the transfer stream.
    pub sequence: u64,
    /// Raw data payload.
    pub data: Vec<u8>,
    /// Whether this is the last block in the stream.
    pub is_final: bool,
}

/// Trait for KV cache transport backends.
///
/// Implementations handle the low-level mechanics of moving KV cache data
/// between clusters, whether via RDMA (NIXL), gRPC streaming, or other
/// mechanisms.
#[allow(async_fn_in_trait)]
pub trait TransferProtocol: Send + Sync {
    /// Establish a connection to the remote cluster for data transfer.
    async fn connect(&self, remote_endpoint: &str) -> anyhow::Result<()>;

    /// Send a stream of KV cache blocks to the connected remote.
    async fn send_blocks(&self, blocks: Vec<KvBlock>) -> anyhow::Result<u64>;

    /// Receive KV cache blocks from the connected remote.
    async fn receive_blocks(&self) -> anyhow::Result<Vec<KvBlock>>;

    /// Close the transport connection gracefully.
    async fn close(&self) -> anyhow::Result<()>;
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn transfer_type_display() {
        assert_eq!(TransferType::HotFailover.to_string(), "hot-failover");
        assert_eq!(TransferType::WarmMigration.to_string(), "warm-migration");
        assert_eq!(TransferType::PrefixTreeSync.to_string(), "prefix-tree-sync");
    }

    #[test]
    fn transfer_type_serializes() {
        let tt = TransferType::HotFailover;
        let json = serde_json::to_string(&tt).unwrap();
        let deser: TransferType = serde_json::from_str(&json).unwrap();
        assert_eq!(deser, tt);
    }

    #[test]
    fn kv_block_construction() {
        let block = KvBlock {
            sequence: 0,
            data: vec![1, 2, 3, 4],
            is_final: false,
        };
        assert_eq!(block.data.len(), 4);
        assert!(!block.is_final);
    }
}
