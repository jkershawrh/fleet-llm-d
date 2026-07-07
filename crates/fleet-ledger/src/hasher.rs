//! SHA-256 hashing utilities for computing input hashes.
//!
//! Used for KV cache transfer verification and other tamper-detection scenarios.

use sha2::{Sha256, Digest};

/// Computes a hex-encoded SHA-256 hash of the given data.
pub fn compute_hash(data: &[u8]) -> String {
    let mut hasher = Sha256::new();
    hasher.update(data);
    let result = hasher.finalize();
    hex::encode(result)
}

/// Verifies that data matches the expected hex-encoded SHA-256 hash.
pub fn verify_hash(data: &[u8], expected_hash: &str) -> bool {
    compute_hash(data) == expected_hash
}

// We need hex encoding - use a simple implementation to avoid another dependency.
mod hex {
    pub fn encode(bytes: impl AsRef<[u8]>) -> String {
        bytes.as_ref().iter().map(|b| format!("{:02x}", b)).collect()
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_compute_hash_empty() {
        let hash = compute_hash(b"");
        // SHA-256 of empty input is well-known
        assert_eq!(hash, "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855");
    }

    #[test]
    fn test_compute_hash_hello() {
        let hash = compute_hash(b"hello");
        assert_eq!(hash, "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824");
    }

    #[test]
    fn test_verify_hash_valid() {
        let data = b"fleet-llm-d kv cache data";
        let hash = compute_hash(data);
        assert!(verify_hash(data, &hash));
    }

    #[test]
    fn test_verify_hash_invalid() {
        let data = b"fleet-llm-d kv cache data";
        assert!(!verify_hash(data, "0000000000000000000000000000000000000000000000000000000000000000"));
    }

    #[test]
    fn test_verify_hash_tampered() {
        let original = b"original data";
        let hash = compute_hash(original);
        let tampered = b"tampered data";
        assert!(!verify_hash(tampered, &hash));
    }

    #[test]
    fn test_compute_hash_deterministic() {
        let data = b"deterministic test";
        let hash1 = compute_hash(data);
        let hash2 = compute_hash(data);
        assert_eq!(hash1, hash2);
    }
}
