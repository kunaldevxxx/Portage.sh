// Package dashboard provides a simple web-based observability dashboard for Portage migrations.
package dashboard

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"
)

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

// statusHandler returns the current status of all migrations as JSON.
func statusHandler(store *EventStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
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
		w.Header().Set("Content-Type", "application/json")
		events := store.GetAll()
		json.NewEncoder(w).Encode(map[string]interface{}{
			"events": events,
		})
	}
}

// indexHandler serves the dashboard HTML page.
func indexHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(`<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <title>Portage Migration Dashboard</title>
  <style>
    :root {
      --bg: #0d1117;
      --fg: #e6edf3;
      --accent: #58a6ff;
      --border: #30363d;
      --success: #3fb950;
      --warning: #d29922;
      --danger: #f85149;
      --card: #161b22;
    }
    * { margin: 0; padding: 0; box-sizing: border-box; }
    body {
      font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif;
      background: var(--bg);
      color: var(--fg);
      padding: 20px;
    }
    .header {
      display: flex;
      align-items: center;
      justify-content: space-between;
      margin-bottom: 24px;
      padding-bottom: 16px;
      border-bottom: 1px solid var(--border);
    }
    .header h1 {
      font-size: 24px;
      font-weight: 600;
      display: flex;
      align-items: center;
      gap: 10px;
    }
    .header h1 .logo {
      width: 28px;
      height: 28px;
      background: var(--accent);
      border-radius: 6px;
      display: flex;
      align-items: center;
      justify-content: center;
      font-weight: 700;
      font-size: 16px;
    }
    .stats {
      display: grid;
      grid-template-columns: repeat(auto-fit, minmax(180px, 1fr));
      gap: 16px;
      margin-bottom: 24px;
    }
    .stat-card {
      background: var(--card);
      border: 1px solid var(--border);
      border-radius: 8px;
      padding: 16px;
    }
    .stat-card .label {
      font-size: 12px;
      color: #8b949e;
      text-transform: uppercase;
      letter-spacing: 0.5px;
      margin-bottom: 8px;
    }
    .stat-card .value {
      font-size: 28px;
      font-weight: 600;
    }
    .stat-card .value.success { color: var(--success); }
    .stat-card .value.warning { color: var(--warning); }
    .stat-card .value.danger { color: var(--danger); }
    .migration-list {
      background: var(--card);
      border: 1px solid var(--border);
      border-radius: 8px;
      overflow: hidden;
    }
    .migration-list .header-row {
      display: flex;
      padding: 12px 16px;
      background: #21262d;
      border-bottom: 1px solid var(--border);
      font-size: 12px;
      color: #8b949e;
      text-transform: uppercase;
      letter-spacing: 0.5px;
    }
    .migration-list .header-row span {
      flex: 1;
    }
    .migration-row {
      display: flex;
      padding: 12px 16px;
      border-bottom: 1px solid var(--border);
      align-items: center;
      font-size: 14px;
    }
    .migration-row .phase-badge {
      padding: 2px 8px;
      border-radius: 4px;
      font-size: 11px;
      font-weight: 600;
      text-transform: uppercase;
    }
    .phase-pending { background: #1f6feb20; color: #58a6ff; }
    .phase-validating { background: #1f6feb20; color: #58a6ff; }
    .phase-translating { background: #1f6feb20; color: #58a6ff; }
    .phase-replicating { background: #d2992220; color: #d29922; }
    .phase-quiescing { background: #d2992220; color: #d29922; }
    .phase-snapshotting { background: #d2992220; color: #d29922; }
    .phase-applying { background: #1f6feb20; color: #58a6ff; }
    .phase-cutover { background: #3fb95020; color: #3fb950; }
    .phase-completed { background: #3fb95020; color: #3fb950; }
    .phase-failed { background: #f8514920; color: #f85149; }
    .migration-row .name { flex: 2; font-weight: 500; }
    .migration-row .detail { flex: 1; color: #8b949e; font-size: 13px; }
    .empty-state {
      padding: 40px;
      text-align: center;
      color: #8b949e;
    }
    .empty-state .icon {
      font-size: 48px;
      margin-bottom: 12px;
    }
  </style>
</head>
<body>
  <div class="header">
    <h1><span class="logo">P</span> Portage Migration Dashboard</h1>
    <div style="color: #8b949e; font-size: 13px;">Live monitoring for zero-downtime stateful migrations</div>
  </div>

  <div class="stats">
    <div class="stat-card">
      <div class="label">Active Migrations</div>
      <div class="value" id="active-count">0</div>
    </div>
    <div class="stat-card">
      <div class="label">Completed</div>
      <div class="value success" id="completed-count">0</div>
    </div>
    <div class="stat-card">
      <div class="label">Failed</div>
      <div class="value danger" id="failed-count">0</div>
    </div>
    <div class="stat-card">
      <div class="label">Avg Duration</div>
      <div class="value" id="avg-duration">--</div>
    </div>
  </div>

  <div class="migration-list">
    <div class="header-row">
      <span>Migration</span>
      <span>Phase</span>
      <span>Lag</span>
      <span>Status</span>
    </div>
    <div id="migration-rows">
      <div class="empty-state">
        <div class="icon">📡</div>
        <div>No migrations in progress</div>
        <div style="font-size: 12px; margin-top: 8px;">Run a migration to see it here</div>
      </div>
    </div>
  </div>

  <script>
    // Poll for status updates every 5 seconds
    async function refresh() {
      try {
        const resp = await fetch('/api/status');
        const data = await resp.json();
        const rows = document.getElementById('migration-rows');
        const activeEl = document.getElementById('active-count');
        const completedEl = document.getElementById('completed-count');
        const failedEl = document.getElementById('failed-count');

        if (data.migrations.length === 0) {
          rows.innerHTML = '<div class="empty-state"><div class="icon">📡</div><div>No migrations in progress</div></div>';
          activeEl.textContent = '0';
          completedEl.textContent = '0';
          failedEl.textContent = '0';
          return;
        }

        let active = 0, completed = 0, failed = 0;
        rows.innerHTML = data.migrations.map(m => {
          if (m.phase === 'Completed') completed++;
          else if (m.phase === 'Failed') failed++;
          else active++;

          const lagStr = m.lag_seconds > 0 ? m.lag_seconds + 's' : '--';
          const statusStr = m.merkle_ok ? (m.cutover_done ? '✅ Cutover done' : '🔄 Replicating') : '⚠️ Merkle pending';

          return '<div class="migration-row">' +
            '<span class="name">' + m.name + '</span>' +
            '<span class="phase-badge phase-' + m.phase.toLowerCase() + '">' + m.phase + '</span>' +
            '<span class="detail">' + lagStr + '</span>' +
            '<span class="detail">' + statusStr + '</span>' +
          '</div>';
        }).join('');

        activeEl.textContent = active;
        completedEl.textContent = completed;
        failedEl.textContent = failed;
      } catch (e) {
        console.error('Dashboard refresh error:', e);
      }
    }

    setInterval(refresh, 5000);
    refresh();
  </script>
</body>
</html>`))
}
