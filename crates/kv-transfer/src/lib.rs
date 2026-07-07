//! # kv-transfer
//!
//! KV cache cross-cluster transfer coordinator for fleet-llm-d.
//!
//! This crate provides the coordination logic for transferring KV cache data
//! between clusters, enabling hot failover, warm migration, and prefix tree
//! synchronisation scenarios. The actual GPU-to-GPU transport is delegated to
//! the NIXL SDK via [`nixl_bridge`].

pub mod coordinator;
pub mod nixl_bridge;
pub mod protocol;
