package cutover

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/aegis-migrate/portage/pkg/driver"
)

func init() {
	driver.GlobalRegistry().RegisterCutoverDriver(NewHTTPCutoverClient(30 * time.Second))
}

// CutoverClient defines methods to orchestrate traffic redirection via Ghostwire.
type CutoverClient interface {
	ExecuteCutover(ctx context.Context, directorEndpoint, routeID, targetEndpoint string, drainTimeoutSec int32) error
	CheckTargetHealth(ctx context.Context, directorEndpoint, targetEndpoint string) (bool, error)
}

// HTTPCutoverClient communicates with Ghostwire cutover director daemon via HTTP.
type HTTPCutoverClient struct {
	httpClient *http.Client
}

func NewHTTPCutoverClient(timeout time.Duration) *HTTPCutoverClient {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	return &HTTPCutoverClient{
		httpClient: &http.Client{Timeout: timeout},
	}
}

func (c *HTTPCutoverClient) Name() string {
	return "enterprise-ghostwire"
}

type CutoverRequest struct {
	RouteID         string `json:"route_id"`
	TargetEndpoint  string `json:"target_endpoint"`
	DrainTimeoutSec int32  `json:"drain_timeout_sec"`
}

type CutoverResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message,omitempty"`
}

func (c *HTTPCutoverClient) ExecuteCutover(ctx context.Context, directorEndpoint, routeID, targetEndpoint string, drainTimeoutSec int32) error {
	reqBody, _ := json.Marshal(CutoverRequest{
		RouteID:         routeID,
		TargetEndpoint:  targetEndpoint,
		DrainTimeoutSec: drainTimeoutSec,
	})
	url := fmt.Sprintf("%s/v1/cutover", directorEndpoint)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewBuffer(reqBody))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("Ghostwire cutover request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("Ghostwire cutover failed with status %d", resp.StatusCode)
	}
	return nil
}

type HealthResponse struct {
	Healthy bool `json:"healthy"`
}

func (c *HTTPCutoverClient) CheckTargetHealth(ctx context.Context, directorEndpoint, targetEndpoint string) (bool, error) {
	url := fmt.Sprintf("%s/v1/health?target=%s", directorEndpoint, targetEndpoint)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return false, err
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return false, fmt.Errorf("health check failed: %w", err)
	}
	defer resp.Body.Close()

	var health HealthResponse
	if err := json.NewDecoder(resp.Body).Decode(&health); err != nil {
		return false, err
	}
	return health.Healthy, nil
}

// MockCutoverClient for testing
type MockCutoverClient struct {
	CutoverExecuted    bool
	LastRouteID        string
	LastTargetEndpoint string
	Healthy            bool
	CutoverErr         error
	HealthErr          error
}

func (m *MockCutoverClient) Name() string {
	return "mock-ghostwire"
}

func (m *MockCutoverClient) ExecuteCutover(ctx context.Context, directorEndpoint, routeID, targetEndpoint string, drainTimeoutSec int32) error {
	if m.CutoverErr != nil {
		return m.CutoverErr
	}
	m.CutoverExecuted = true
	m.LastRouteID = routeID
	m.LastTargetEndpoint = targetEndpoint
	return nil
}

func (m *MockCutoverClient) CheckTargetHealth(ctx context.Context, directorEndpoint, targetEndpoint string) (bool, error) {
	if m.HealthErr != nil {
		return false, m.HealthErr
	}
	return m.Healthy, nil
}
