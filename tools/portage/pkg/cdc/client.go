package cdc

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// CDCClient defines methods to interact with the Synapse-CDC replication engine.
type CDCClient interface {
	StartReplication(ctx context.Context, endpoint, streamID string) error
	VerifyState(ctx context.Context, endpoint, streamID string) (bool, string, error)
	GetLag(ctx context.Context, endpoint, streamID string) (int64, error)
}

// HTTPCDCClient communicates with Synapse-CDC daemon via HTTP.
type HTTPCDCClient struct {
	httpClient *http.Client
}

func NewHTTPCDCClient(timeout time.Duration) *HTTPCDCClient {
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	return &HTTPCDCClient{
		httpClient: &http.Client{Timeout: timeout},
	}
}

type StartReplicationRequest struct {
	StreamID string `json:"stream_id"`
}

type StartReplicationResponse struct {
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}

func (c *HTTPCDCClient) StartReplication(ctx context.Context, endpoint, streamID string) error {
	reqBody, _ := json.Marshal(StartReplicationRequest{StreamID: streamID})
	url := fmt.Sprintf("%s/v1/replication/start", endpoint)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewBuffer(reqBody))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("CDC start request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusAccepted {
		return fmt.Errorf("CDC server returned status %d", resp.StatusCode)
	}
	return nil
}

type VerifyRequest struct {
	StreamID string `json:"stream_id"`
}

type VerifyResponse struct {
	Verified  bool   `json:"verified"`
	RootHash  string `json:"root_hash"`
	Message   string `json:"message,omitempty"`
}

func (c *HTTPCDCClient) VerifyState(ctx context.Context, endpoint, streamID string) (bool, string, error) {
	reqBody, _ := json.Marshal(VerifyRequest{StreamID: streamID})
	url := fmt.Sprintf("%s/v1/replication/verify", endpoint)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewBuffer(reqBody))
	if err != nil {
		return false, "", err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return false, "", fmt.Errorf("CDC verify request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return false, "", fmt.Errorf("CDC verify returned status %d", resp.StatusCode)
	}

	var verifyResp VerifyResponse
	if err := json.NewDecoder(resp.Body).Decode(&verifyResp); err != nil {
		return false, "", err
	}
	return verifyResp.Verified, verifyResp.RootHash, nil
}

type StatusResponse struct {
	LagSeconds int64 `json:"lag_seconds"`
	LastLSN    u64   `json:"last_lsn"`
}
type u64 = uint64

func (c *HTTPCDCClient) GetLag(ctx context.Context, endpoint, streamID string) (int64, error) {
	url := fmt.Sprintf("%s/v1/replication/status?stream_id=%s", endpoint, streamID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, err
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("CDC status request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("CDC status returned code %d", resp.StatusCode)
	}

	var status StatusResponse
	if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
		return 0, err
	}
	return status.LagSeconds, nil
}

// MockCDCClient for unit and integration testing
type MockCDCClient struct {
	ReplicationStarted bool
	Verified           bool
	RootHash           string
	LagSeconds         int64
	StartErr           error
	VerifyErr          error
}

func (m *MockCDCClient) StartReplication(ctx context.Context, endpoint, streamID string) error {
	if m.StartErr != nil {
		return m.StartErr
	}
	m.ReplicationStarted = true
	return nil
}

func (m *MockCDCClient) VerifyState(ctx context.Context, endpoint, streamID string) (bool, string, error) {
	if m.VerifyErr != nil {
		return false, "", m.VerifyErr
	}
	m.Verified = true
	if m.RootHash == "" {
		m.RootHash = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	}
	return true, m.RootHash, nil
}

func (m *MockCDCClient) GetLag(ctx context.Context, endpoint, streamID string) (int64, error) {
	return m.LagSeconds, nil
}
