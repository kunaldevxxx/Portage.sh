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
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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

// Run starts the Ghostwire proxy with the given configuration.
// In standalone mode, this is the main entry point.
// In operator mode, UseBinary returns the path to the binary for subprocess management.
func Run() error {
	// In standalone mode, the actual Rust binary handles this.
	// This Go package is a wrapper that the operator uses to manage the binary.
	return fmt.Errorf("ghostwire.Run() is a placeholder — the actual proxy is the Rust binary in portage-enterprise-backend/crates/ghostwire")
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
