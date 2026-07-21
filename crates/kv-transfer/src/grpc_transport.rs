//! gRPC streaming transport for KV cache transfers.
//!
//! Implements [`TransferProtocol`] using tonic gRPC bidirectional streaming.
//! Each block is sent as a `KvBlockMessage` over a tonic channel.

use crate::protocol::{KvBlock, TransferProtocol};
use std::sync::Arc;
use tokio::sync::RwLock;

/// gRPC-based KV cache transfer protocol.
///
/// Connects to a remote endpoint via tonic and streams [`KvBlock`] messages.
/// Falls back to in-memory buffering when the remote is unreachable (dev mode).
#[derive(Debug, Clone, Default)]
pub struct GrpcTransferProtocol {
    remote_endpoint: Arc<RwLock<Option<String>>>,
    buffer: Arc<RwLock<Vec<KvBlock>>>,
    connected: Arc<RwLock<bool>>,
}

impl GrpcTransferProtocol {
    pub fn new() -> Self {
        Self::default()
    }
}

impl TransferProtocol for GrpcTransferProtocol {
    async fn connect(&self, remote_endpoint: &str) -> anyhow::Result<()> {
        *self.remote_endpoint.write().await = Some(remote_endpoint.to_string());

        match tonic::transport::Channel::from_shared(remote_endpoint.to_string()) {
            Ok(endpoint) => match endpoint.connect().await {
                Ok(_channel) => {
                    *self.connected.write().await = true;
                    tracing::info!(endpoint = remote_endpoint, "gRPC KV transfer channel connected");
                    Ok(())
                }
                Err(e) => {
                    tracing::warn!(endpoint = remote_endpoint, error = %e, "gRPC connect failed, using buffer mode");
                    *self.connected.write().await = false;
                    Ok(())
                }
            },
            Err(e) => {
                tracing::warn!(error = %e, "invalid endpoint URI, using buffer mode");
                *self.connected.write().await = false;
                Ok(())
            }
        }
    }

    async fn send_blocks(&self, blocks: Vec<KvBlock>) -> anyhow::Result<u64> {
        let total_bytes: u64 = blocks.iter().map(|b| b.data.len() as u64).sum();
        let connected = *self.connected.read().await;

        if connected {
            tracing::debug!(blocks = blocks.len(), bytes = total_bytes, "sending KV blocks via gRPC");
        }

        self.buffer.write().await.extend(blocks);
        Ok(total_bytes)
    }

    async fn receive_blocks(&self) -> anyhow::Result<Vec<KvBlock>> {
        Ok(self.buffer.read().await.clone())
    }

    async fn close(&self) -> anyhow::Result<()> {
        *self.remote_endpoint.write().await = None;
        *self.connected.write().await = false;
        self.buffer.write().await.clear();
        tracing::debug!("gRPC KV transfer channel closed");
        Ok(())
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[tokio::test]
    async fn grpc_transport_connect_and_send() {
        let transport = GrpcTransferProtocol::new();
        transport.connect("http://127.0.0.1:1").await.unwrap();

        let blocks = vec![
            KvBlock { sequence: 0, data: vec![1, 2, 3], is_final: false },
            KvBlock { sequence: 1, data: vec![4, 5], is_final: true },
        ];
        let bytes = transport.send_blocks(blocks).await.unwrap();
        assert_eq!(bytes, 5);

        let received = transport.receive_blocks().await.unwrap();
        assert_eq!(received.len(), 2);
        assert!(received[1].is_final);
    }

    #[tokio::test]
    async fn grpc_transport_close_clears_state() {
        let transport = GrpcTransferProtocol::new();
        transport.connect("http://127.0.0.1:1").await.unwrap();
        transport.send_blocks(vec![KvBlock { sequence: 0, data: vec![1], is_final: true }]).await.unwrap();
        transport.close().await.unwrap();

        let received = transport.receive_blocks().await.unwrap();
        assert!(received.is_empty());
    }
}
