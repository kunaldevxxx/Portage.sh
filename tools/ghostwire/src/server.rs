use crate::proxy::cutover::CutoverDirector;
use serde::{Deserialize, Serialize};
use std::net::SocketAddr;
use std::sync::Arc;
use std::time::Duration;
use tokio::io::{AsyncReadExt, AsyncWriteExt};
use tokio::net::{TcpListener, TcpStream};

#[allow(dead_code)]
#[derive(Deserialize)]
pub struct CutoverApiRequest {
    pub route_id: String,
    pub target_endpoint: String,
    pub drain_timeout_sec: Option<u64>,
}

#[derive(Serialize)]
pub struct CutoverApiResponse {
    pub success: bool,
    pub message: String,
}

#[derive(Serialize)]
pub struct HealthApiResponse {
    pub healthy: bool,
}

pub struct GhostwireServer {
    mgmt_addr: SocketAddr,
    director: Arc<CutoverDirector>,
}

impl GhostwireServer {
    pub fn new(mgmt_addr: SocketAddr, director: Arc<CutoverDirector>) -> Self {
        Self {
            mgmt_addr,
            director,
        }
    }

    pub async fn run(&self) -> Result<(), std::io::Error> {
        let listener = TcpListener::bind(self.mgmt_addr).await?;
        println!("Ghostwire management API listening on {}", self.mgmt_addr);

        loop {
            let (stream, _) = listener.accept().await?;
            let director = self.director.clone();
            tokio::spawn(async move {
                let _ = handle_mgmt_request(stream, director).await;
            });
        }
    }
}

async fn handle_mgmt_request(
    mut stream: TcpStream,
    director: Arc<CutoverDirector>,
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
        ("POST", p) if p.starts_with("/v1/cutover") => {
            let timeout_duration = Duration::from_secs(15);
            match director.execute_cutover(timeout_duration).await {
                Ok(_) => {
                    let resp = CutoverApiResponse {
                        success: true,
                        message: "Cutover and drain complete".into(),
                    };
                    (200, serde_json::to_string(&resp).unwrap())
                }
                Err(e) => {
                    let resp = CutoverApiResponse {
                        success: false,
                        message: format!("Cutover error: {}", e),
                    };
                    (500, serde_json::to_string(&resp).unwrap())
                }
            }
        }
        ("GET", p) if p.starts_with("/v1/health") => {
            let resp = HealthApiResponse { healthy: true };
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
