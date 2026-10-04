package dashboard

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestEventStore_RecordAndGetAll(t *testing.T) {
	store := NewEventStore()

	event1 := &MigrationStatusEvent{
		Namespace:  "default",
		Name:       "pg-migration",
		Phase:      "Replicating",
		LagSeconds: 12,
		MerkleOK:   true,
	}

	store.Record(event1)

	events := store.GetAll()
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Name != "pg-migration" {
		t.Errorf("expected name pg-migration, got %s", events[0].Name)
	}
	if events[0].StartTime == "" {
		t.Errorf("expected StartTime to be populated automatically")
	}

	// Nil record should be safe
	store.Record(nil)
	if len(store.GetAll()) != 1 {
		t.Fatalf("expected 1 event after nil record, got %d", len(store.GetAll()))
	}
}

func TestDashboard_HTTPHandlers(t *testing.T) {
	statusChan := make(chan *MigrationStatusEvent, 2)
	handler := Handler(statusChan)

	statusChan <- &MigrationStatusEvent{
		Namespace: "prod",
		Name:      "db-cluster",
		Phase:     "CutoverReady",
	}
	close(statusChan)

	// Test GET /
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("expected GET / to return 200, got %d", w.Code)
	}

	// Test GET /api/status
	reqStatus := httptest.NewRequest(http.MethodGet, "/api/status", nil)
	wStatus := httptest.NewRecorder()
	handler.ServeHTTP(wStatus, reqStatus)
	if wStatus.Code != http.StatusOK {
		t.Errorf("expected GET /api/status to return 200, got %d", wStatus.Code)
	}

	var statusResp map[string]interface{}
	if err := json.Unmarshal(wStatus.Body.Bytes(), &statusResp); err != nil {
		t.Fatalf("failed to decode /api/status response: %v", err)
	}
	if _, ok := statusResp["migrations"]; !ok {
		t.Errorf("expected migrations key in /api/status response")
	}

	// Test GET /api/events
	reqEvents := httptest.NewRequest(http.MethodGet, "/api/events", nil)
	wEvents := httptest.NewRecorder()
	handler.ServeHTTP(wEvents, reqEvents)
	if wEvents.Code != http.StatusOK {
		t.Errorf("expected GET /api/events to return 200, got %d", wEvents.Code)
	}

	// Test GET /metrics (Prometheus exposition)
	reqMetrics := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	wMetrics := httptest.NewRecorder()
	handler.ServeHTTP(wMetrics, reqMetrics)
	if wMetrics.Code != http.StatusOK {
		t.Errorf("expected GET /metrics to return 200, got %d", wMetrics.Code)
	}
	bodyMetrics := wMetrics.Body.String()
	if !strings.Contains(bodyMetrics, "portage_migration_phase") {
		t.Errorf("expected portage_migration_phase in /metrics, got:\n%s", bodyMetrics)
	}
	if !strings.Contains(bodyMetrics, "portage_data_replicated_bytes") {
		t.Errorf("expected portage_data_replicated_bytes in /metrics")
	}
}

func TestPopulateDemoData(t *testing.T) {
	store := NewEventStore()
	PopulateDemoData(store)
	events := store.GetAll()
	if len(events) != 4 {
		t.Fatalf("expected 4 demo migrations (including 100GB enterprise shard), got %d", len(events))
	}
}
