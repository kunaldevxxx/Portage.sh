use std::net::SocketAddr;
use std::sync::Arc;
use ghostwire::proxy::cutover::CutoverDirector;
use ghostwire::server::GhostwireServer;
use tokio::sync::watch;

#[tokio::main]
async fn main() -> Result<(), Box<dyn std::error::Error>> {
    let proxy_port = std::env::var("PROXY_PORT").unwrap_or_else(|_| "80".into());
    let mgmt_port = std::env::var("MGMT_PORT").unwrap_or_else(|_| "8080".into());
    let source_backend = std::env::var("SOURCE_BACKEND").unwrap_or_else(|_| "127.0.0.1:8001".into());
    let target_backend = std::env::var("TARGET_BACKEND").unwrap_or_else(|_| "127.0.0.1:8002".into());

    let proxy_addr: SocketAddr = format!("0.0.0.0:{}", proxy_port).parse()?;
    let mgmt_addr: SocketAddr = format!("0.0.0.0:{}", mgmt_port).parse()?;
    let src_addr: SocketAddr = source_backend.parse()?;
    let tgt_addr: SocketAddr = target_backend.parse()?;

    let director = Arc::new(CutoverDirector::new(proxy_addr, src_addr, tgt_addr));

    // Spawn proxy engine
    let director_clone = director.clone();
    let (_shutdown_tx, shutdown_rx) = watch::channel(false);
    tokio::spawn(async move {
        if let Err(e) = director_clone.run_server(shutdown_rx).await {
            eprintln!("Proxy server error: {}", e);
        }
    });

    // Run management API
    let mgmt_server = GhostwireServer::new(mgmt_addr, director);
    mgmt_server.run().await?;

    Ok(())
}
