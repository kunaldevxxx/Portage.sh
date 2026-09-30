package ghostwire

import (
	"bytes"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestNativeDirector_ManagementEndpoints(t *testing.T) {
	director := NewNativeDirector(":5432", ":8000", "source:5432", "target:5432")
	handler := director.ManagementHandler()

	// 1. Health check
	reqHealth := httptest.NewRequest(http.MethodGet, "/v1/health", nil)
	wHealth := httptest.NewRecorder()
	handler.ServeHTTP(wHealth, reqHealth)

	if wHealth.Code != http.StatusOK {
		t.Fatalf("expected 200 from /v1/health, got %d", wHealth.Code)
	}

	var healthResp map[string]bool
	if err := json.Unmarshal(wHealth.Body.Bytes(), &healthResp); err != nil || !healthResp["healthy"] {
		t.Fatalf("expected {healthy: true}, got %v", healthResp)
	}

	// 2. Cutover method not allowed for GET
	reqCutoverGet := httptest.NewRequest(http.MethodGet, "/v1/cutover", nil)
	wCutoverGet := httptest.NewRecorder()
	handler.ServeHTTP(wCutoverGet, reqCutoverGet)
	if wCutoverGet.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405 for GET /v1/cutover, got %d", wCutoverGet.Code)
	}

	// 3. Cutover POST
	cutoverPayload := []byte(`{"target_endpoint":"new-target:5432","drain_timeout_sec":1}`)
	reqCutoverPost := httptest.NewRequest(http.MethodPost, "/v1/cutover", bytes.NewReader(cutoverPayload))
	wCutoverPost := httptest.NewRecorder()
	handler.ServeHTTP(wCutoverPost, reqCutoverPost)

	if wCutoverPost.Code != http.StatusOK {
		t.Fatalf("expected 200 for POST /v1/cutover, got %d: %s", wCutoverPost.Code, wCutoverPost.Body.String())
	}

	if !director.routedToTarget.Load() {
		t.Fatalf("expected routedToTarget to be true after cutover")
	}
	if director.targetBackend != "new-target:5432" {
		t.Fatalf("expected targetBackend to be updated to new-target:5432, got %s", director.targetBackend)
	}
}

func TestNativeDirector_HandleConnection(t *testing.T) {
	director := NewNativeDirector(":5432", ":8000", "source:5432", "target:5432")

	var dialedAddr string
	director.dialFn = func(network, address string, timeout time.Duration) (net.Conn, error) {
		dialedAddr = address
		clientPipe, serverPipe := net.Pipe()

		go func() {
			buf := make([]byte, 1024)
			n, _ := serverPipe.Read(buf)
			_, _ = serverPipe.Write(append([]byte("ACK:"), buf[:n]...))
			_ = serverPipe.Close()
		}()

		return clientPipe, nil
	}

	// 1. Connection prior to cutover -> dials source
	clientConn, proxyConn := net.Pipe()
	go director.handleConnection(proxyConn)

	_, _ = clientConn.Write([]byte("ping-source"))
	buf := make([]byte, 1024)
	n, _ := clientConn.Read(buf)
	_ = clientConn.Close()

	if dialedAddr != "source:5432" {
		t.Errorf("expected connection to dial source:5432, dialed %s", dialedAddr)
	}
	if string(buf[:n]) != "ACK:ping-source" {
		t.Errorf("expected ACK:ping-source, got %s", string(buf[:n]))
	}

	// 2. Perform cutover
	director.routedToTarget.Store(true)

	// 3. Connection after cutover -> dials target
	clientConn2, proxyConn2 := net.Pipe()
	go director.handleConnection(proxyConn2)

	_, _ = clientConn2.Write([]byte("ping-target"))
	buf2 := make([]byte, 1024)
	n2, _ := clientConn2.Read(buf2)
	_ = clientConn2.Close()

	if dialedAddr != "target:5432" {
		t.Errorf("expected connection to dial target:5432, dialed %s", dialedAddr)
	}
	if string(buf2[:n2]) != "ACK:ping-target" {
		t.Errorf("expected ACK:ping-target, got %s", string(buf2[:n2]))
	}
}
