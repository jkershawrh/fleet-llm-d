//! TCP transport for KV cache transfers (ConnectLink CPU mode).
//!
//! Implements [`TransferProtocol`] using tokio TCP streams with length-prefixed
//! binary framing. This is the CPU-friendly transport for KV cache transfer
//! between clusters — NIXL handles GPU RDMA, this handles CPU-to-CPU over TCP.

use async_trait::async_trait;
use std::sync::Arc;
use tokio::io::{AsyncReadExt, AsyncWriteExt};
use tokio::net::{TcpListener, TcpStream};
use tokio::sync::RwLock;

use crate::protocol::{KvBlock, TransferProtocol};

/// Frame header: 4B length + 8B sequence + 1B is_final = 13 bytes.
const HEADER_SIZE: usize = 13;

/// Encode a [`KvBlock`] into a length-prefixed binary frame.
///
/// Layout: `[4B u32 BE: frame len][8B u64 BE: sequence][1B: is_final][payload]`
fn encode_block(block: &KvBlock) -> Vec<u8> {
    let frame_len = (HEADER_SIZE - 4 + block.data.len()) as u32;
    let mut buf = Vec::with_capacity(4 + frame_len as usize);
    buf.extend_from_slice(&frame_len.to_be_bytes());
    buf.extend_from_slice(&block.sequence.to_be_bytes());
    buf.push(if block.is_final { 1 } else { 0 });
    buf.extend_from_slice(&block.data);
    buf
}

/// Decode a [`KvBlock`] from a reader by reading one length-prefixed frame.
async fn decode_block<R: AsyncReadExt + Unpin>(reader: &mut R) -> anyhow::Result<KvBlock> {
    let mut len_buf = [0u8; 4];
    reader.read_exact(&mut len_buf).await?;
    let frame_len = u32::from_be_bytes(len_buf) as usize;

    if frame_len < 9 {
        anyhow::bail!("frame too small: {} bytes", frame_len);
    }

    let mut seq_buf = [0u8; 8];
    reader.read_exact(&mut seq_buf).await?;
    let sequence = u64::from_be_bytes(seq_buf);

    let mut flag_buf = [0u8; 1];
    reader.read_exact(&mut flag_buf).await?;
    let is_final = flag_buf[0] != 0;

    let data_len = frame_len - 9;
    let mut data = vec![0u8; data_len];
    if data_len > 0 {
        reader.read_exact(&mut data).await?;
    }

    Ok(KvBlock {
        sequence,
        data,
        is_final,
    })
}

/// TCP-based KV cache transfer protocol (ConnectLink CPU mode).
///
/// Connects to a remote endpoint via TCP and streams [`KvBlock`] frames using
/// a length-prefixed binary wire format. For CPU-to-CPU KV cache transfers
/// between clusters where RDMA/NIXL is not available.
pub struct TcpTransferProtocol {
    writer: Arc<RwLock<Option<tokio::net::tcp::OwnedWriteHalf>>>,
    reader: Arc<RwLock<Option<tokio::net::tcp::OwnedReadHalf>>>,
    remote_endpoint: Arc<RwLock<Option<String>>>,
}

impl Default for TcpTransferProtocol {
    fn default() -> Self {
        Self::new()
    }
}

impl TcpTransferProtocol {
    /// Create a new unconnected TCP transport.
    pub fn new() -> Self {
        Self {
            writer: Arc::new(RwLock::new(None)),
            reader: Arc::new(RwLock::new(None)),
            remote_endpoint: Arc::new(RwLock::new(None)),
        }
    }

    /// Create a TCP transport from an already-accepted stream.
    fn from_stream(stream: TcpStream) -> Self {
        let (read_half, write_half) = stream.into_split();
        Self {
            writer: Arc::new(RwLock::new(Some(write_half))),
            reader: Arc::new(RwLock::new(Some(read_half))),
            remote_endpoint: Arc::new(RwLock::new(Some("accepted".to_string()))),
        }
    }
}

#[async_trait]
impl TransferProtocol for TcpTransferProtocol {
    async fn connect(&self, remote_endpoint: &str) -> anyhow::Result<()> {
        let stream = TcpStream::connect(remote_endpoint).await?;
        let (read_half, write_half) = stream.into_split();
        *self.writer.write().await = Some(write_half);
        *self.reader.write().await = Some(read_half);
        *self.remote_endpoint.write().await = Some(remote_endpoint.to_string());
        tracing::info!(endpoint = remote_endpoint, "TCP KV transfer connected");
        Ok(())
    }

    async fn send_blocks(&self, blocks: Vec<KvBlock>) -> anyhow::Result<u64> {
        let mut writer_guard = self.writer.write().await;
        let writer = writer_guard
            .as_mut()
            .ok_or_else(|| anyhow::anyhow!("TCP transport not connected"))?;

        let mut total_bytes = 0u64;
        for block in &blocks {
            let frame = encode_block(block);
            writer.write_all(&frame).await?;
            total_bytes += block.data.len() as u64;
        }
        writer.flush().await?;
        tracing::debug!(
            blocks = blocks.len(),
            bytes = total_bytes,
            "sent KV blocks via TCP"
        );
        Ok(total_bytes)
    }

    async fn receive_blocks(&self) -> anyhow::Result<Vec<KvBlock>> {
        let mut reader_guard = self.reader.write().await;
        let reader = reader_guard
            .as_mut()
            .ok_or_else(|| anyhow::anyhow!("TCP transport not connected"))?;

        let mut blocks = Vec::new();
        loop {
            let block = decode_block(reader).await?;
            let is_final = block.is_final;
            blocks.push(block);
            if is_final {
                break;
            }
        }
        tracing::debug!(blocks = blocks.len(), "received KV blocks via TCP");
        Ok(blocks)
    }

    async fn close(&self) -> anyhow::Result<()> {
        *self.writer.write().await = None;
        *self.reader.write().await = None;
        *self.remote_endpoint.write().await = None;
        tracing::debug!("TCP KV transfer closed");
        Ok(())
    }
}

/// TCP listener that accepts inbound KV cache transfer connections.
pub struct TcpTransferListener {
    listener: TcpListener,
}

impl TcpTransferListener {
    /// Bind a TCP listener on the given address (e.g. "127.0.0.1:0" for a
    /// random port).
    pub async fn bind(addr: &str) -> anyhow::Result<Self> {
        let listener = TcpListener::bind(addr).await?;
        tracing::info!(addr = %listener.local_addr()?, "TCP KV transfer listener bound");
        Ok(Self { listener })
    }

    /// Returns the local address this listener is bound to.
    pub fn local_addr(&self) -> anyhow::Result<std::net::SocketAddr> {
        Ok(self.listener.local_addr()?)
    }

    /// Accept one inbound connection and return a [`TcpTransferProtocol`]
    /// wired to the accepted stream.
    pub async fn accept(&self) -> anyhow::Result<TcpTransferProtocol> {
        let (stream, peer) = self.listener.accept().await?;
        tracing::info!(peer = %peer, "accepted TCP KV transfer connection");
        Ok(TcpTransferProtocol::from_stream(stream))
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn encode_decode_roundtrip() {
        let block = KvBlock {
            sequence: 42,
            data: vec![1, 2, 3, 4, 5],
            is_final: true,
        };
        let encoded = encode_block(&block);
        assert_eq!(encoded.len(), 4 + 9 + 5);

        let rt = tokio::runtime::Runtime::new().unwrap();
        let decoded = rt.block_on(async {
            let mut cursor = std::io::Cursor::new(encoded);
            decode_block(&mut cursor).await.unwrap()
        });
        assert_eq!(decoded.sequence, 42);
        assert_eq!(decoded.data, vec![1, 2, 3, 4, 5]);
        assert!(decoded.is_final);
    }

    #[tokio::test]
    async fn tcp_loopback_transfer() {
        let listener = TcpTransferListener::bind("127.0.0.1:0").await.unwrap();
        let addr = listener.local_addr().unwrap();

        let send_blocks = vec![
            KvBlock {
                sequence: 0,
                data: vec![10, 20, 30],
                is_final: false,
            },
            KvBlock {
                sequence: 1,
                data: vec![40, 50],
                is_final: true,
            },
        ];

        let sender = tokio::spawn(async move {
            let client = TcpTransferProtocol::new();
            client.connect(&addr.to_string()).await.unwrap();
            let bytes = client.send_blocks(send_blocks).await.unwrap();
            assert_eq!(bytes, 5);
            client.close().await.unwrap();
        });

        let server = listener.accept().await.unwrap();
        let received = server.receive_blocks().await.unwrap();
        assert_eq!(received.len(), 2);
        assert_eq!(received[0].sequence, 0);
        assert_eq!(received[0].data, vec![10, 20, 30]);
        assert!(!received[0].is_final);
        assert_eq!(received[1].sequence, 1);
        assert_eq!(received[1].data, vec![40, 50]);
        assert!(received[1].is_final);

        sender.await.unwrap();
    }

    #[tokio::test]
    async fn tcp_empty_data() {
        let listener = TcpTransferListener::bind("127.0.0.1:0").await.unwrap();
        let addr = listener.local_addr().unwrap();

        let sender = tokio::spawn(async move {
            let client = TcpTransferProtocol::new();
            client.connect(&addr.to_string()).await.unwrap();
            let bytes = client
                .send_blocks(vec![KvBlock {
                    sequence: 0,
                    data: vec![],
                    is_final: true,
                }])
                .await
                .unwrap();
            assert_eq!(bytes, 0);
        });

        let server = listener.accept().await.unwrap();
        let received = server.receive_blocks().await.unwrap();
        assert_eq!(received.len(), 1);
        assert!(received[0].data.is_empty());
        assert!(received[0].is_final);

        sender.await.unwrap();
    }

    #[tokio::test]
    async fn tcp_large_block() {
        let listener = TcpTransferListener::bind("127.0.0.1:0").await.unwrap();
        let addr = listener.local_addr().unwrap();

        let large_data: Vec<u8> = (0..1_000_000).map(|i| (i % 256) as u8).collect();
        let expected = large_data.clone();

        let sender = tokio::spawn(async move {
            let client = TcpTransferProtocol::new();
            client.connect(&addr.to_string()).await.unwrap();
            let bytes = client
                .send_blocks(vec![KvBlock {
                    sequence: 0,
                    data: large_data,
                    is_final: true,
                }])
                .await
                .unwrap();
            assert_eq!(bytes, 1_000_000);
        });

        let server = listener.accept().await.unwrap();
        let received = server.receive_blocks().await.unwrap();
        assert_eq!(received.len(), 1);
        assert_eq!(received[0].data.len(), 1_000_000);
        assert_eq!(received[0].data, expected);

        sender.await.unwrap();
    }

    #[tokio::test]
    async fn tcp_close_clears_state() {
        let transport = TcpTransferProtocol::new();
        assert!(transport.send_blocks(vec![]).await.is_err());
        transport.close().await.unwrap();
        assert!(transport.writer.read().await.is_none());
        assert!(transport.reader.read().await.is_none());
        assert!(transport.remote_endpoint.read().await.is_none());
    }
}
