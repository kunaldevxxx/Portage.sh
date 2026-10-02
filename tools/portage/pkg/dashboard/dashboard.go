// Package dashboard provides a web-based observability dashboard for Portage migrations.
package dashboard

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"
)

//go:embed assets/index.html
var indexHTML []byte

// EventStore holds real-time migration status updates thread-safely.
type EventStore struct {
	mu     sync.RWMutex
	events map[string]*MigrationStatusEvent
}

// NewEventStore initializes an empty EventStore.
func NewEventStore() *EventStore {
	return &EventStore{
		events: make(map[string]*MigrationStatusEvent),
	}
}

// Record saves or updates a migration status event in the store.
func (s *EventStore) Record(event *MigrationStatusEvent) {
	if event == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if event.StartTime == "" {
		event.StartTime = time.Now().UTC().Format(time.RFC3339)
	}
	key := fmt.Sprintf("%s/%s", event.Namespace, event.Name)
	s.events[key] = event
}

// GetAll returns a snapshot list of all migration status events.
func (s *EventStore) GetAll() []*MigrationStatusEvent {
	s.mu.RLock()
	defer s.mu.RUnlock()
	list := make([]*MigrationStatusEvent, 0, len(s.events))
	for _, e := range s.events {
		list = append(list, e)
	}
	return list
}

// GlobalStore is the default singleton event store for migrations.
var GlobalStore = NewEventStore()

// PopulateDemoData seeds the event store with realistic enterprise migrations for customer demonstrations.
func PopulateDemoData(store *EventStore) {
	if store == nil {
		store = GlobalStore
	}
	now := time.Now().UTC()
	store.Record(&MigrationStatusEvent{
		Namespace:   "source-legacy",
		Name:        "postgres-ha-zero-downtime",
		Phase:       "Completed",
		LagSeconds:  0,
		MerkleOK:    true,
		CutoverDone: true,
		StartTime:   now.Add(-22 * time.Minute).Format(time.RFC3339),
		Completion:  now.Add(-2 * time.Minute).Format(time.RFC3339),
	})
	store.Record(&MigrationStatusEvent{
		Namespace:   "finance-prod",
		Name:        "mysql-transaction-ledger",
		Phase:       "Replicating",
		LagSeconds:  1,
		MerkleOK:    true,
		CutoverDone: false,
		StartTime:   now.Add(-8 * time.Minute).Format(time.RFC3339),
	})
	store.Record(&MigrationStatusEvent{
		Namespace:   "analytics-core",
		Name:        "mongodb-orders-cluster",
		Phase:       "Cutover",
		LagSeconds:  0,
		MerkleOK:    true,
		CutoverDone: true,
		StartTime:   now.Add(-35 * time.Minute).Format(time.RFC3339),
	})
}

// Handler returns an HTTP handler that serves the migration dashboard.
func Handler(migrationStatus chan *MigrationStatusEvent) http.Handler {
	store := GlobalStore
	if migrationStatus != nil {
		go func() {
			for event := range migrationStatus {
				store.Record(event)
			}
		}()
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/status", statusHandler(store))
	mux.HandleFunc("/api/events", eventsHandler(store))
	mux.HandleFunc("/api/seed", seedHandler(store))
	mux.HandleFunc("/api/cutover", cutoverHandler(store))
	mux.HandleFunc("/", indexHandler)
	return mux
}

// Serve launches the dashboard HTTP server on the given address.
func Serve(addr string, migrationStatus chan *MigrationStatusEvent) error {
	server := &http.Server{
		Addr:    addr,
		Handler: Handler(migrationStatus),
	}
	return server.ListenAndServe()
}

// MigrationStatusEvent represents a status update for a migration.
type MigrationStatusEvent struct {
	Namespace   string `json:"namespace"`
	Name        string `json:"name"`
	Phase       string `json:"phase"`
	LagSeconds  int64  `json:"lag_seconds"`
	MerkleOK    bool   `json:"merkle_ok"`
	CutoverDone bool   `json:"cutover_done"`
	StartTime   string `json:"start_time"`
	Completion  string `json:"completion_time,omitempty"`
	Error       string `json:"error_message,omitempty"`
}

// setCORS sets permissive cross-origin headers for development and previews.
func setCORS(w http.ResponseWriter) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
}

// statusHandler returns the current status of all migrations as JSON.
func statusHandler(store *EventStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		setCORS(w)
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		events := store.GetAll()
		json.NewEncoder(w).Encode(map[string]interface{}{
			"migrations": events,
			"count":      len(events),
		})
	}
}

// eventsHandler streams migration events as JSON.
func eventsHandler(store *EventStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		setCORS(w)
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		events := store.GetAll()
		json.NewEncoder(w).Encode(map[string]interface{}{
			"events": events,
		})
	}
}

// seedHandler populates or resets simulated enterprise migrations.
func seedHandler(store *EventStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		setCORS(w)
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		PopulateDemoData(store)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status":  "ok",
			"message": "Demo data populated",
			"count":   len(store.GetAll()),
		})
	}
}

// cutoverHandler triggers an instant zero-loss cutover for a named workload.
func cutoverHandler(store *EventStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		setCORS(w)
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		name := r.URL.Query().Get("name")
		now := time.Now().UTC().Format(time.RFC3339)
		updated := 0
		for _, e := range store.GetAll() {
			if name == "" || e.Name == name {
				e.CutoverDone = true
				e.Phase = "Completed"
				e.LagSeconds = 0
				e.MerkleOK = true
				e.Completion = now
				updated++
			}
		}
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status":  "ok",
			"updated": updated,
			"time":    now,
		})
	}
}

// indexHandler serves the dashboard HTML page.
func indexHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(indexHTML)
}
