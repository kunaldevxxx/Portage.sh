use async_trait::async_trait;
use serde::{Deserialize, Serialize};
use sha2::{Digest, Sha256};
use std::sync::atomic::{AtomicU64, Ordering};
use std::sync::Arc;
use std::time::Duration;
use thiserror::Error;
use tokio::sync::mpsc;
use tokio::time::sleep;

#[derive(Error, Debug)]
pub enum EngineError {
    #[error("Source stream disconnected: {0}")]
    SourceDisconnected(String),
    #[error("Target replication failed: {0}")]
    TargetError(String),
    #[error("Integrity check failed for record LSN {0}")]
    ChecksumMismatch(u64),
    #[error("Engine channel closed")]
    ChannelClosed,
}

#[derive(Debug, Clone, Serialize, Deserialize, PartialEq, Eq)]
pub enum Operation {
    Insert,
    Update,
    Delete,
}

#[derive(Debug, Clone, Serialize, Deserialize, PartialEq, Eq)]
pub struct CdcRecord {
    pub lsn: u64,
    pub timestamp_ms: u64,
    pub entity_key: String,
    pub operation: Operation,
    pub payload: Vec<u8>,
    pub checksum: [u8; 32],
}

impl CdcRecord {
    pub fn new(
        lsn: u64,
        timestamp_ms: u64,
        entity_key: String,
        operation: Operation,
        payload: Vec<u8>,
    ) -> Self {
        let mut hasher = Sha256::new();
        hasher.update(lsn.to_be_bytes());
        hasher.update(entity_key.as_bytes());
        hasher.update(&payload);
        let result = hasher.finalize();
        let mut checksum = [0u8; 32];
        checksum.copy_from_slice(&result);

        Self {
            lsn,
            timestamp_ms,
            entity_key,
            operation,
            payload,
            checksum,
        }
    }

    pub fn verify_checksum(&self) -> bool {
        let mut hasher = Sha256::new();
        hasher.update(self.lsn.to_be_bytes());
        hasher.update(self.entity_key.as_bytes());
        hasher.update(&self.payload);
        let result = hasher.finalize();
        self.checksum == result.as_slice()
    }
}

#[async_trait]
pub trait WalStreamer: Send + Sync {
    async fn next_record(&mut self) -> Result<Option<CdcRecord>, EngineError>;
}

#[async_trait]
pub trait TargetApplier: Send + Sync {
    async fn apply_record(&mut self, record: &CdcRecord) -> Result<(), EngineError>;
    async fn commit_checkpoint(&mut self, confirmed_lsn: u64) -> Result<(), EngineError>;
}

pub struct ReplicatorConfig {
    pub buffer_capacity: usize,
    pub max_retry_attempts: u32,
    pub initial_backoff_ms: u64,
    pub max_backoff_ms: u64,
}

impl Default for ReplicatorConfig {
    fn default() -> Self {
        Self {
            buffer_capacity: 10_000,
            max_retry_attempts: 5,
            initial_backoff_ms: 100,
            max_backoff_ms: 5000,
        }
    }
}

pub struct CdcReplicationEngine {
    config: ReplicatorConfig,
    last_committed_lsn: Arc<AtomicU64>,
}

impl CdcReplicationEngine {
    pub fn new(config: ReplicatorConfig) -> Self {
        Self {
            config,
            last_committed_lsn: Arc::new(AtomicU64::new(0)),
        }
    }

    pub fn last_committed_lsn(&self) -> u64 {
        self.last_committed_lsn.load(Ordering::Acquire)
    }

    /// Runs continuous zero-downtime replication pipeline with backpressure and retry resiliency.
    pub async fn run_pipeline<S, A>(
        &self,
        mut source: S,
        mut applier: A,
        mut shutdown_rx: tokio::sync::broadcast::Receiver<()>,
    ) -> Result<u64, EngineError>
    where
        S: WalStreamer + 'static,
        A: TargetApplier + 'static,
    {
        let (tx, mut rx) = mpsc::channel::<CdcRecord>(self.config.buffer_capacity);
        let last_lsn = self.last_committed_lsn.clone();
        let max_retries = self.config.max_retry_attempts;
        let base_backoff = self.config.initial_backoff_ms;
        let max_backoff = self.config.max_backoff_ms;

        // Producer task: reads from source WAL and pushes to bounded ring channel
        let producer_handle = tokio::spawn(async move {
            while let Ok(Some(record)) = source.next_record().await {
                if !record.verify_checksum() {
                    return Err(EngineError::ChecksumMismatch(record.lsn));
                }
                if tx.send(record).await.is_err() {
                    break; // Consumer terminated
                }
            }
            Ok::<(), EngineError>(())
        });

        // Consumer loop: drains queue, applies to target, commits checkpoints with backoff
        loop {
            tokio::select! {
                _ = shutdown_rx.recv() => {
                    break;
                }
                maybe_record = rx.recv() => {
                    match maybe_record {
                        Some(record) => {
                            let mut attempt = 0;
                            let mut applied = false;
                            while attempt < max_retries {
                                match applier.apply_record(&record).await {
                                    Ok(()) => {
                                        applied = true;
                                        break;
                                    }
                                    Err(_) => {
                                        attempt += 1;
                                        let wait = std::cmp::min(
                                            base_backoff * 2u64.pow(attempt.min(6)),
                                            max_backoff,
                                        );
                                        sleep(Duration::from_millis(wait)).await;
                                    }
                                }
                            }

                            if !applied {
                                return Err(EngineError::TargetError(format!(
                                    "Failed to replicate record LSN {} after {} retries",
                                    record.lsn, max_retries
                                )));
                            }

                            applier.commit_checkpoint(record.lsn).await?;
                            last_lsn.store(record.lsn, Ordering::Release);
                        }
                        None => {
                            // Upstream closed cleanly
                            break;
                        }
                    }
                }
            }
        }

        let _ = producer_handle.await;
        Ok(self.last_committed_lsn.load(Ordering::Acquire))
    }
}
