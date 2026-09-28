use std::net::SocketAddr;
use synapse_cdc::server::CdcHttpServer;

#[tokio::main]
async fn main() -> Result<(), Box<dyn std::error::Error>> {
    let port = std::env::var("SYNAPSE_PORT").unwrap_or_else(|_| "8080".into());
    let addr: SocketAddr = format!("0.0.0.0:{}", port).parse()?;
    let server = CdcHttpServer::new(addr);
    server.run().await?;
    Ok(())
}
