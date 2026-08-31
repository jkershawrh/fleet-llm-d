//! # fleet-ledger
//!
//! ARE (Agentic Runtime Environment) Immutable Ledger integration for fleet-llm-d.
//!
//! Provides async gRPC client and SHA-256 hashing utilities for recording
//! fleet events to a tamper-evident, append-only ledger.

pub mod client;
pub mod hasher;
