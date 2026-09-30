// Package ghostwire provides a Go wrapper for the Ghostwire zero-RST TCP proxy binary.
//
// The actual proxy is implemented in Rust (portage-enterprise-backend/crates/ghostwire).
// This package provides a Go-friendly interface for launching and managing the proxy
// as a subprocess, which is used by the Portage Migration operator for traffic cutover.
//
// Standalone usage:
//
//	ghostwire --config ghostwire.yaml
//	ghostwire --proxy-listen :5432 --source-backend db-source:5432 --target-backend db-target:5432
package ghostwire

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"time"
)

// Config represents the Ghostwire proxy configuration.
type Config struct {
	ProxyListen      string `yaml:"proxy_listen"`
	MgmtListen       string `yaml:"mgmt_listen"`
	SourceBackend    string `yaml:"source_backend"`
	TargetBackend    string `yaml:"target_backend"`
	DefaultDrainTimeoutSec int64  `yaml:"default_drain_timeout_sec"`
	TLS              *TLSConfig `yaml:"tls,omitempty"`
	Auth             *AuthConfig `yaml:"auth,omitempty"`
	MetricsPort      uint16 `yaml:"metrics_port"`
	HA               *HAConfig `yaml:"ha,omitempty"`
}

type TLSConfig struct {
	CertPath    string `yaml:"cert_path"`
	KeyPath     string `yaml:"key_path"`
	MinVersion  string `yaml:"min_version,omitempty"`
	ClientAuth  bool   `yaml:"client_auth,omitempty"`
}

type AuthConfig struct {
	APIKeyHeader string `yaml:"api_key_header,omitempty"`
	APIKey       string `yaml:"api_key,omitempty"`
	APIKeyFile   string `yaml:"api_key_file,omitempty"`
	HMACSecret   string `yaml:"hmac_secret,omitempty"`
	HMACSecretFile string `yaml:"hmac_secret_file,omitempty"`
}

type HAConfig struct {
	Enabled          bool   `yaml:"enabled,omitempty"`
	LockFile         string `yaml:"lock_file,omitempty"`
	ElectionTimeoutMs int64  `yaml:"election_timeout_ms,omitempty"`
}

// NativeDirector provides a pure-Go L4 TCP cutover proxy with zero packet loss and graceful drainage.
type NativeDirector struct {
	proxyListen    string
	mgmtListen     string
	sourceBackend  string
	targetBackend  string
	routedToTarget atomic.Bool
	activeSource   atomic.Int64
	dialFn         func(network, address string, timeout time.Duration) (net.Conn, error)
}

// NewNativeDirector creates a new pure-Go cutover director.
func NewNativeDirector(proxyListen, mgmtListen, sourceBackend, targetBackend string) *NativeDirector {
	return &NativeDirector{
		proxyListen:   proxyListen,
		mgmtListen:    mgmtListen,
		sourceBackend: sourceBackend,
		targetBackend: targetBackend,
	}
}

// ManagementHandler returns the HTTP management handler for health and cutover.
func (d *NativeDirector) ManagementHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/cutover", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var req struct {
			TargetEndpoint  string `json:"target_endpoint"`
			DrainTimeoutSec int64  `json:"drain_timeout_sec"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req.TargetEndpoint != "" {
			d.targetBackend = req.TargetEndpoint
		}
		timeoutSec := req.DrainTimeoutSec
		if timeoutSec <= 0 {
			timeoutSec = 15
		}

		// Step 1: Atomic route swap - immediately route all new incoming connections to target
		d.routedToTarget.Store(true)

		// Step 2: Await drain of in-flight source connections
		deadline := time.Now().Add(time.Duration(timeoutSec) * time.Second)
		for time.Now().Before(deadline) {
			if d.activeSource.Load() == 0 {
				break
			}
			time.Sleep(50 * time.Millisecond)
		}

		w.Header().Set("Content-Type", "application/json")
		remaining := d.activeSource.Load()
		if remaining > 0 {
			w.WriteHeader(http.StatusGatewayTimeout)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"success": false,
				"message": fmt.Sprintf("Drain timed out with %d connections remaining", remaining),
			})
			return
		}

		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"message": "Cutover and drain complete",
		})
	})

	mux.HandleFunc("/v1/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]bool{"healthy": true})
	})

	return mux
}

// Start begins serving both the management HTTP API and the L4 TCP proxy.
func (d *NativeDirector) Start(ctx context.Context) error {
	mgmtServer := &http.Server{
		Addr:    d.mgmtListen,
		Handler: d.ManagementHandler(),
	}

	go func() {
		_ = mgmtServer.ListenAndServe()
	}()

	listener, err := net.Listen("tcp", d.proxyListen)
	if err != nil {
		return fmt.Errorf("failed to bind proxy listener on %s: %w", d.proxyListen, err)
	}
	defer listener.Close()

	fmt.Fprintf(os.Stdout, "[ghostwire] Proxy listening on %s (Source: %s, Target: %s, Mgmt: %s)\n",
		d.proxyListen, d.sourceBackend, d.targetBackend, d.mgmtListen)

	go func() {
		<-ctx.Done()
		_ = listener.Close()
		_ = mgmtServer.Close()
	}()

	for {
		clientConn, err := listener.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				return nil
			default:
				time.Sleep(10 * time.Millisecond)
				continue
			}
		}

		go d.handleConnection(clientConn)
	}
}

func (d *NativeDirector) handleConnection(clientConn net.Conn) {
	defer clientConn.Close()

	isTarget := d.routedToTarget.Load()
	var backendAddr string

	if isTarget {
		backendAddr = d.targetBackend
	} else {
		backendAddr = d.sourceBackend
		d.activeSource.Add(1)
		defer d.activeSource.Add(-1)
	}

	dial := d.dialFn
	if dial == nil {
		dial = net.DialTimeout
	}
	backendConn, err := dial("tcp", backendAddr, 5*time.Second)
	if err != nil {
		return
	}
	defer backendConn.Close()

	errChan := make(chan error, 2)
	go func() {
		_, err := io.Copy(backendConn, clientConn)
		errChan <- err
	}()
	go func() {
		_, err := io.Copy(clientConn, backendConn)
		errChan <- err
	}()

	<-errChan
}

// RunArgs starts the Ghostwire proxy with the given argument slice.
func RunArgs(args []string) error {
	fs := flag.NewFlagSet("ghostwire", flag.ExitOnError)
	proxyListen := fs.String("proxy-listen", ":5432", "Proxy listener address")
	mgmtListen := fs.String("mgmt-listen", ":8000", "Management API listener address")
	sourceBackend := fs.String("source-backend", "127.0.0.1:5433", "Source backend address")
	targetBackend := fs.String("target-backend", "127.0.0.1:5434", "Target backend address")

	if err := fs.Parse(args); err != nil {
		return err
	}

	director := NewNativeDirector(*proxyListen, *mgmtListen, *sourceBackend, *targetBackend)
	return director.Start(context.Background())
}

// Run starts the Ghostwire proxy using command line arguments.
func Run() error {
	var args []string
	if len(os.Args) > 1 {
		args = os.Args[1:]
	}
	return RunArgs(args)
}

// BinaryPath returns the path to the Ghostwire binary.
// In production, this would be populated by the operator deployment.
func BinaryPath() string {
	// Default: look for ghostwire binary in PATH or well-known locations
	if path := os.Getenv("GHOSTWIRE_BINARY"); path != "" {
		return path
	}
	// Check relative to the operator binary
	if exe, err := os.Executable(); err == nil {
		possible := filepath.Join(filepath.Dir(exe), "ghostwire")
		if _, err := os.Stat(possible); err == nil {
			return possible
		}
	}
	// Fall back to PATH
	return "ghostwire"
}

// ExecOption configures a Ghostwire subprocess execution.
type ExecOption func(*exec.Cmd)

// WithConfig sets the path to a YAML configuration file.
func WithConfig(path string) ExecOption {
	return func(cmd *exec.Cmd) {
		cmd.Args = append(cmd.Args, "--config", path)
	}
}

// WithProxyListen sets the proxy listener address.
func WithProxyListen(addr string) ExecOption {
	return func(cmd *exec.Cmd) {
		cmd.Args = append(cmd.Args, "--proxy-listen", addr)
	}
}

// WithSourceBackend sets the source backend address.
func WithSourceBackend(addr string) ExecOption {
	return func(cmd *exec.Cmd) {
		cmd.Args = append(cmd.Args, "--source-backend", addr)
	}
}

// WithTargetBackend sets the target backend address.
func WithTargetBackend(addr string) ExecOption {
	return func(cmd *exec.Cmd) {
		cmd.Args = append(cmd.Args, "--target-backend", addr)
	}
}

// ExecContext runs the Ghostwire binary with the given options.
func ExecContext(ctx context.Context, binary string, opts ...ExecOption) (*exec.Cmd, error) {
	cmd := exec.CommandContext(ctx, binary, "--version")
	for _, opt := range opts {
		opt(cmd)
	}
	return cmd, nil
}

// Start starts the Ghostwire proxy as a subprocess and returns a handle to manage it.
func Start(ctx context.Context, binary string, opts ...ExecOption) (*Process, error) {
	cmd := exec.CommandContext(ctx, binary)
	cmd.Args = append(cmd.Args, "--mgmt-listen", "127.0.0.1:0") // let OS pick port
	for _, opt := range opts {
		opt(cmd)
	}
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("failed to start ghostwire: %w", err)
	}

	return &Process{
		cmd:       cmd,
		startedAt: time.Now(),
	}, nil
}

// Process represents a running Ghostwire proxy subprocess.
type Process struct {
	cmd       *exec.Cmd
	startedAt time.Time
}

// Stop gracefully stops the Ghostwire proxy.
func (p *Process) Stop() error {
	if p.cmd == nil || p.cmd.Process == nil {
		return nil
	}
	return p.cmd.Process.Signal(os.Interrupt)
}

// Wait waits for the Ghostwire proxy to exit.
func (p *Process) Wait() error {
	if p.cmd == nil {
		return nil
	}
	return p.cmd.Wait()
}

// PID returns the process ID of the running Ghostwire proxy.
func (p *Process) PID() int {
	if p.cmd == nil || p.cmd.Process == nil {
		return -1
	}
	return p.cmd.Process.Pid
}

// Elapsed returns the time since the process started.
func (p *Process) Elapsed() time.Duration {
	return time.Since(p.startedAt)
}
