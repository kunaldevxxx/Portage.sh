use sha2::{Digest, Sha256};
use thiserror::Error;

#[derive(Error, Debug)]
pub enum MerkleError {
    #[error("Empty dataset cannot construct a Merkle tree")]
    EmptyDataset,
    #[error("Proof verification failed at index {0}")]
    InvalidProof(usize),
}

/// A node in the cryptographic Merkle Tree
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct MerkleNode {
    pub hash: [u8; 32],
    pub left: Option<Box<MerkleNode>>,
    pub right: Option<Box<MerkleNode>>,
}

/// MerkleTree provides mathematically verifiable state comparison between source and target
/// storage volumes or database partitions in air-gapped environments.
pub struct MerkleTree {
    root: MerkleNode,
    leaves_count: usize,
}

impl MerkleTree {
    /// Constructs a Merkle tree from raw state items (e.g. table rows or storage blocks).
    pub fn build<T: AsRef<[u8]>>(items: &[T]) -> Result<Self, MerkleError> {
        if items.is_empty() {
            return Err(MerkleError::EmptyDataset);
        }

        let mut nodes: Vec<MerkleNode> = items
            .iter()
            .map(|item| {
                let mut hasher = Sha256::new();
                hasher.update(b"LEAF:");
                hasher.update(item.as_ref());
                let result = hasher.finalize();
                let mut hash = [0u8; 32];
                hash.copy_from_slice(&result);
                MerkleNode {
                    hash,
                    left: None,
                    right: None,
                }
            })
            .collect();

        let leaves_count = nodes.len();

        while nodes.len() > 1 {
            let mut next_level = Vec::with_capacity((nodes.len() + 1) / 2);
            for chunk in nodes.chunks(2) {
                if chunk.len() == 2 {
                    let left = &chunk[0];
                    let right = &chunk[1];
                    let mut hasher = Sha256::new();
                    hasher.update(b"NODE:");
                    hasher.update(&left.hash);
                    hasher.update(&right.hash);
                    let result = hasher.finalize();
                    let mut parent_hash = [0u8; 32];
                    parent_hash.copy_from_slice(&result);

                    next_level.push(MerkleNode {
                        hash: parent_hash,
                        left: Some(Box::new(left.clone())),
                        right: Some(Box::new(right.clone())),
                    });
                } else {
                    // Odd element: promote without hashing, or hash with itself
                    let left = &chunk[0];
                    let mut hasher = Sha256::new();
                    hasher.update(b"NODE:");
                    hasher.update(&left.hash);
                    hasher.update(&left.hash);
                    let result = hasher.finalize();
                    let mut parent_hash = [0u8; 32];
                    parent_hash.copy_from_slice(&result);

                    next_level.push(MerkleNode {
                        hash: parent_hash,
                        left: Some(Box::new(left.clone())),
                        right: None,
                    });
                }
            }
            nodes = next_level;
        }

        Ok(Self {
            root: nodes.into_iter().next().unwrap(),
            leaves_count,
        })
    }

    /// Returns the 256-bit cryptographic root state digest
    pub fn root_hash(&self) -> [u8; 32] {
        self.root.hash
    }

    /// Returns hex-encoded representation of the root hash
    pub fn root_hex(&self) -> String {
        hex_encode(&self.root.hash)
    }

    /// Validates if a target Merkle tree's root matches the source root.
    /// Returns true if and only if state is bit-for-bit identical.
    pub fn verify_integrity(&self, target_root: &[u8; 32]) -> bool {
        self.root.hash == *target_root
    }
}

pub fn hex_encode(bytes: &[u8]) -> String {
    bytes.iter().map(|b| format!("{:02x}", b)).collect()
}
