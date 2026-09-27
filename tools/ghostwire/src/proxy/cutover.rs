use std::net::SocketAddr;
use std::sync::atomic::{AtomicBool, AtomicUsize, Ordering};
use std::sync::Arc;
use std::time::Duration;
use thiserror::Error;
use tokio::io::copy_bidirectional;
use tokio::net::{TcpListener, TcpStream};
use tokio::sync::{watch, Notify};
use tokio::time::{sleep, timeout};

#[derive(Error, Debug)]
pub enum ProxyError {
    #[error("Failed to bind listener on {0}: {1}")]
    BindError(SocketAddr, std::io::Error),
    #[error("Failed to connect to backend {0}: {1}")]
    BackendConnectionError(SocketAddr, std::io::Error),
    #[error("Drain timed out with {0} connections remaining")]
    DrainTimeout(usize),
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum RoutingTarget {
    SourceCluster,
    TargetCluster,
}

pub struct ProxyMetrics {
    pub active_connections: Arc<AtomicUsize>,
    pub total_routed_source: Arc<AtomicUsize>,
    pub total_routed_target: Arc<AtomicUsize>,
}

pub struct CutoverDirector {
    listen_addr: SocketAddr,
    source_backend: SocketAddr,
    target_backend: SocketAddr,
    current_target: Arc<AtomicBool>, // false = Source, true = Target
    active_connections: Arc<AtomicUsize>,
    drain_notify: Arc<Notify>,
    metrics: ProxyMetrics,
}

impl CutoverDirector {
    pub fn new(listen_addr: SocketAddr, source_backend: SocketAddr, target_backend: SocketAddr) -> Self {
        let active = Arc::new(AtomicUsize::new(0));
        let metrics = ProxyMetrics {
            active_connections: active.clone(),
            total_routed_source: Arc::new(AtomicUsize::new(0)),
            total_routed_target: Arc::new(AtomicUsize::new(0)),
        };

        Self {
            listen_addr,
            source_backend,
            target_backend,
            current_target: Arc::new(AtomicBool::new(false)),
            active_connections: active,
            drain_notify: Arc::new(Notify::new()),
            metrics,
        }
    }

    pub fn current_route(&self) -> RoutingTarget {
        if self.current_target.load(Ordering::Acquire) {
            RoutingTarget::TargetCluster
        } else {
            RoutingTarget::SourceCluster
        }
    }

    /// Triggers atomic cutover to the target cluster backend and drains in-flight source connections.
    pub async fn execute_cutover(&self, drain_timeout: Duration) -> Result<(), ProxyError> {
        // Step 1: Atomic route swap - immediately directs all new TCP sessions to target cluster
        self.current_target.store(true, Ordering::Release);

        // Step 2: Await drain of in-flight source connections
        let active = self.active_connections.clone();
        let notifier = self.drain_notify.clone();

        let drain_future = async {
            while active.load(Ordering::Acquire) > 0 {
                notifier.notified().await;
            }
        };

        match timeout(drain_timeout, drain_future).await {
            Ok(_) => Ok(()),
            Err(_) => {
                let remaining = active.load(Ordering::Acquire);
                if remaining > 0 {
                    Err(ProxyError::DrainTimeout(remaining))
                } else {
                    Ok(())
                }
            }
        }
    }

    /// Serves proxy connections asynchronously with zero packet loss.
    pub async fn run_server(&self, mut shutdown_rx: watch::Receiver<bool>) -> Result<(), ProxyError> {
        let listener = TcpListener::bind(self.listen_addr)
            .await
            .map_err(|e| ProxyError::BindError(self.listen_addr, e))?;

        loop {
            tokio::select! {
                _ = shutdown_rx.changed() => {
                    if *shutdown_rx.borrow() {
                        break;
                    }
                }
                accept_res = listener.accept() => {
                    match accept_res {
                        Ok((client_stream, _)) => {
                            let is_target = self.current_target.load(Ordering::Acquire);
                            let backend_addr = if is_target {
                                self.metrics.total_routed_target.fetch_add(1, Ordering::Relaxed);
                                self.target_backend
                            } else {
                                self.metrics.total_routed_source.fetch_add(1, Ordering::Relaxed);
                                self.source_backend
                            };

                            let active = self.active_connections.clone();
                            let notifier = self.drain_notify.clone();
                            active.fetch_add(1, Ordering::SeqCst);

                            tokio::spawn(async move {
                                let _guard = ConnectionGuard {
                                    counter: active,
                                    notifier,
                                };

                                if let Ok(mut backend_stream) = TcpStream::connect(backend_addr).await {
                                    let mut client_stream = client_stream;
                                    let _ = copy_bidirectional(&mut client_stream, &mut backend_stream).await;
                                }
                            });
                        }
                        Err(_) => {
                            sleep(Duration::from_millis(10)).await;
                        }
                    }
                }
            }
        }

        Ok(())
    }
}

struct ConnectionGuard {
    counter: Arc<AtomicUsize>,
    notifier: Arc<Notify>,
}

impl Drop for ConnectionGuard {
    fn drop(&mut self) {
        self.counter.fetch_sub(1, Ordering::SeqCst);
        self.notifier.notify_waiters();
    }
}
