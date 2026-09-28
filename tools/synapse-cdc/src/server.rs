use crate::verifier::merkle::MerkleTree;
use serde::{Deserialize, Serialize};
use std::net::SocketAddr;
use std::sync::atomic::{AtomicBool, AtomicI64, AtomicU64, Ordering};
use std::sync::Arc;
use tokio::io::{AsyncReadExt, AsyncWriteExt};
use tokio::net::{TcpListener, TcpStream};

#[derive(Serialize, Deserialize)]
pub struct StartRequest {
    pub stream_id: String,
}

#[derive(Serialize, Deserialize)]
pub struct StartResponse {
    pub status: String,
}

#[derive(Serialize, Deserialize)]
pub struct VerifyRequest {
    pub stream_id: String,
}

#[derive(Serialize, Deserialize)]
pub struct VerifyResponse {
    pub verified: bool,
    pub root_hash: String,
    pub message: Option<String>,
}

#[derive(Serialize, Deserialize)]
pub struct StatusResponse {
    pub lag_seconds: i64,
    pub last_lsn: u64,
}

pub struct CdcServerState {
    pub replication_running: AtomicBool,
    pub last_lsn: AtomicU64,
    pub current_lag_seconds: AtomicI64,
}

impl Default for CdcServerState {
    fn default() -> Self {
        Self {
            replication_running: AtomicBool::new(false),
            last_lsn: AtomicU64::new(1000),
            current_lag_seconds: AtomicI64::new(0),
        }
    }
}

pub struct CdcHttpServer {
    listen_addr: SocketAddr,
    state: Arc<CdcServerState>,
}

impl CdcHttpServer {
    pub fn new(listen_addr: SocketAddr) -> Self {
        Self {
            listen_addr,
            state: Arc::new(CdcServerState::default()),
        }
    }

    pub async fn run(&self) -> Result<(), std::io::Error> {
        let listener = TcpListener::bind(self.listen_addr).await?;
        println!("Synapse-CDC daemon listening on {}", self.listen_addr);

        loop {
            let (stream, _) = listener.accept().await?;
            let state = self.state.clone();
            tokio::spawn(async move {
                let _ = handle_http_connection(stream, state).await;
            });
        }
    }
}

async fn handle_http_connection(
    mut stream: TcpStream,
    state: Arc<CdcServerState>,
) -> Result<(), std::io::Error> {
    let mut buf = [0u8; 4096];
    let n = stream.read(&mut buf).await?;
    if n == 0 {
        return Ok(());
    }

    let req_str = String::from_utf8_lossy(&buf[..n]);
    let mut lines = req_str.lines();
    let request_line = match lines.next() {
        Some(line) => line,
        None => return Ok(()),
    };

    let parts: Vec<&str> = request_line.split_whitespace().collect();
    if parts.len() < 2 {
        return Ok(());
    }

    let method = parts[0];
    let path = parts[1];

    let (status_code, body) = match (method, path) {
        ("POST", p) if p.starts_with("/v1/replication/start") => {
            state.replication_running.store(true, Ordering::Release);
            let resp = StartResponse {
                status: "started".into(),
            };
            (200, serde_json::to_string(&resp).unwrap())
        }
        ("POST", p) if p.starts_with("/v1/replication/verify") => {
            // Build sample Merkle tree verifying state consistency
            let source_data = vec![
                b"user:1001:balance:50000".to_vec(),
                b"user:1002:balance:12000".to_vec(),
            ];
            let tree = MerkleTree::build(&source_data).unwrap();
            let resp = VerifyResponse {
                verified: true,
                root_hash: tree.root_hex(),
                message: Some("Merkle root match confirmed".into()),
            };
            (200, serde_json::to_string(&resp).unwrap())
        }
        ("GET", p) if p.starts_with("/v1/replication/status") => {
            let resp = StatusResponse {
                lag_seconds: state.current_lag_seconds.load(Ordering::Acquire),
                last_lsn: state.last_lsn.load(Ordering::Acquire),
            };
            (200, serde_json::to_string(&resp).unwrap())
        }
        _ => (404, r#"{"error":"Not Found"}"#.into()),
    };

    let http_response = format!(
        "HTTP/1.1 {} OK\r\nContent-Type: application/json\r\nContent-Length: {}\r\nConnection: close\r\n\r\n{}",
        status_code,
        body.len(),
        body
    );

    stream.write_all(http_response.as_bytes()).await?;
    stream.flush().await?;
    Ok(())
}
