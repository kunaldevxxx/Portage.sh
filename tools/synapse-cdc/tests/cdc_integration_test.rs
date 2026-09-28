use async_trait::async_trait;
use std::sync::atomic::{AtomicUsize, Ordering};
use std::sync::Arc;
use synapse_cdc::engine::cdc::*;
use synapse_cdc::verifier::merkle::*;
use tokio::sync::Mutex;

struct MockWalStreamer {
    records: Vec<CdcRecord>,
    cursor: usize,
}

#[async_trait]
impl WalStreamer for MockWalStreamer {
    async fn next_record(&mut self) -> Result<Option<CdcRecord>, EngineError> {
        if self.cursor < self.records.len() {
            let record = self.records[self.cursor].clone();
            self.cursor += 1;
            Ok(Some(record))
        } else {
            Ok(None)
        }
    }
}

struct MockTargetApplier {
    applied_records: Arc<Mutex<Vec<CdcRecord>>>,
    last_checkpoint: Arc<Mutex<u64>>,
    failure_counter: Arc<AtomicUsize>,
    fail_every_n: usize,
}

#[async_trait]
impl TargetApplier for MockTargetApplier {
    async fn apply_record(&mut self, record: &CdcRecord) -> Result<(), EngineError> {
        let count = self.failure_counter.fetch_add(1, Ordering::SeqCst);
        if self.fail_every_n > 0 && count % self.fail_every_n == 0 {
            return Err(EngineError::TargetError(
                "Simulated transient socket reset".into(),
            ));
        }
        self.applied_records.lock().await.push(record.clone());
        Ok(())
    }

    async fn commit_checkpoint(&mut self, confirmed_lsn: u64) -> Result<(), EngineError> {
        *self.last_checkpoint.lock().await = confirmed_lsn;
        Ok(())
    }
}

#[tokio::test]
async fn test_merkle_tree_mathematical_verification() {
    // 1. Source database records
    let source_data = vec![
        b"user:1001:balance:50000".to_vec(),
        b"user:1002:balance:12000".to_vec(),
        b"user:1003:balance:98500".to_vec(),
        b"user:1004:balance:3400".to_vec(),
    ];

    let source_merkle =
        MerkleTree::build(&source_data).expect("Failed building source Merkle tree");
    let source_root = source_merkle.root_hash();

    // 2. Identical target dataset
    let target_data_identical = source_data.clone();
    let target_merkle =
        MerkleTree::build(&target_data_identical).expect("Failed building target Merkle tree");
    assert!(
        source_merkle.verify_integrity(&target_merkle.root_hash()),
        "Identical datasets must yield matching Merkle root hashes"
    );

    // 3. Mutated target dataset (1 byte tampered, e.g. balance modified)
    let mut target_data_tampered = source_data.clone();
    target_data_tampered[2] = b"user:1003:balance:98501".to_vec();
    let tampered_merkle =
        MerkleTree::build(&target_data_tampered).expect("Failed building tampered Merkle tree");

    assert!(
        !source_merkle.verify_integrity(&tampered_merkle.root_hash()),
        "Tampered dataset must be mathematically detected by Merkle divergence"
    );
}

#[tokio::test]
async fn test_cdc_streaming_pipeline_resilience_and_ordering() {
    let num_records = 500;
    let mut records = Vec::with_capacity(num_records);
    for i in 1..=num_records {
        let payload = format!("state_blob_data_{}", i).into_bytes();
        records.push(CdcRecord::new(
            i as u64,
            1710000000 + i as u64,
            format!("key_{}", i),
            Operation::Insert,
            payload,
        ));
    }

    let streamer = MockWalStreamer {
        records: records.clone(),
        cursor: 0,
    };

    let applied_records = Arc::new(Mutex::new(Vec::new()));
    let last_checkpoint = Arc::new(Mutex::new(0u64));
    let applier = MockTargetApplier {
        applied_records: applied_records.clone(),
        last_checkpoint: last_checkpoint.clone(),
        failure_counter: Arc::new(AtomicUsize::new(1)),
        fail_every_n: 10, // Induce transient failures every 10 attempts
    };

    let config = ReplicatorConfig {
        buffer_capacity: 64,
        max_retry_attempts: 5,
        initial_backoff_ms: 1,
        max_backoff_ms: 10,
    };
    let engine = CdcReplicationEngine::new(config);
    let (_shutdown_tx, shutdown_rx) = tokio::sync::broadcast::channel(1);

    let committed_lsn = engine
        .run_pipeline(streamer, applier, shutdown_rx)
        .await
        .expect("CDC pipeline must complete successfully with retries");

    assert_eq!(committed_lsn, num_records as u64);
    assert_eq!(*last_checkpoint.lock().await, num_records as u64);

    let applied = applied_records.lock().await;
    assert_eq!(applied.len(), num_records);
    for (idx, record) in applied.iter().enumerate() {
        assert_eq!(
            record.lsn,
            (idx + 1) as u64,
            "Strict LSN ordering must be preserved"
        );
    }
}
