use std::net::SocketAddr;
use std::time::Duration;
use ghostwire::proxy::cutover::*;
use tokio::io::{AsyncReadExt, AsyncWriteExt};
use tokio::net::TcpListener;
use tokio::sync::watch;

async fn spawn_echo_server(prefix: &'static str) -> SocketAddr {
    let listener = TcpListener::bind("127.0.0.1:0").await.unwrap();
    let addr = listener.local_addr().unwrap();

    tokio::spawn(async move {
        loop {
            if let Ok((mut stream, _)) = listener.accept().await {
                tokio::spawn(async move {
                    let mut buf = [0u8; 1024];
                    while let Ok(n) = stream.read(&mut buf).await {
                        if n == 0 {
                            break;
                        }
                        let mut response = prefix.as_bytes().to_vec();
                        response.extend_from_slice(&buf[..n]);
                        let _ = stream.write_all(&response).await;
                    }
                });
            }
        }
    });

    addr
}

#[tokio::test]
async fn test_zero_loss_traffic_cutover() {
    // 1. Spin up mock Source and Target backends
    let source_addr = spawn_echo_server("SOURCE:").await;
    let target_addr = spawn_echo_server("TARGET:").await;

    // 2. Bind proxy listener to ephemeral port
    let dummy_listener = TcpListener::bind("127.0.0.1:0").await.unwrap();
    let proxy_addr = dummy_listener.local_addr().unwrap();
    drop(dummy_listener);

    let director = std::sync::Arc::new(CutoverDirector::new(proxy_addr, source_addr, target_addr));
    let (shutdown_tx, shutdown_rx) = watch::channel(false);

    let director_clone = director.clone();
    tokio::spawn(async move {
        let _ = director_clone.run_server(shutdown_rx).await;
    });

    tokio::time::sleep(Duration::from_millis(50)).await;

    // 3. Connect Client A before cutover (must hit SOURCE)
    let mut client_a = tokio::net::TcpStream::connect(proxy_addr).await.expect("Client A connection failed");
    client_a.write_all(b"payload_alpha").await.unwrap();
    let mut resp_a = [0u8; 64];
    let n_a = client_a.read(&mut resp_a).await.unwrap();
    let text_a = String::from_utf8_lossy(&resp_a[..n_a]);
    assert!(text_a.starts_with("SOURCE:payload_alpha"));

    // 4. Trigger Atomic Cutover
    director
        .execute_cutover(Duration::from_secs(2))
        .await
        .expect("Cutover and connection drain must succeed");

    assert_eq!(director.current_route(), RoutingTarget::TargetCluster);

    // 5. Verify existing Client A can still finish its session gracefully (no RST)
    client_a.write_all(b"_flush").await.unwrap();
    let n_a2 = client_a.read(&mut resp_a).await.unwrap();
    let text_a2 = String::from_utf8_lossy(&resp_a[..n_a2]);
    assert!(text_a2.contains("SOURCE:"));
    drop(client_a);

    // 6. Connect Client B after cutover (must hit TARGET)
    let mut client_b = tokio::net::TcpStream::connect(proxy_addr).await.expect("Client B connection failed");
    client_b.write_all(b"payload_beta").await.unwrap();
    let mut resp_b = [0u8; 64];
    let n_b = client_b.read(&mut resp_b).await.unwrap();
    let text_b = String::from_utf8_lossy(&resp_b[..n_b]);
    assert!(text_b.starts_with("TARGET:payload_beta"));

    let _ = shutdown_tx.send(true);
}
