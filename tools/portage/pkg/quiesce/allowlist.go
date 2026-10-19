package quiesce

import (
	"context"
	"fmt"
	"strings"
	"time"

	v1alpha1 "github.com/aegis-migrate/portage/api/v1alpha1"
)

// HookAllowlist defines approved commands per database type.
type HookAllowlist struct {
	PostgreSQL []string
	MySQL      []string
	MongoDB    []string
	Custom     map[string][]string
}

// DefaultHookAllowlist returns a pre-configured allowlist with safe defaults.
func DefaultHookAllowlist() *HookAllowlist {
	return &HookAllowlist{
		PostgreSQL: []string{
			"psql", "pg_dump", "pg_restore", "pg_basebackup",
			"pg_isready", "pg_config", "postgres",
			"checkpoint", "backup_start", "backup_stop", "wal_backup",
		},
		MySQL: []string{
			"mysql", "mysqldump", "mysqladmin", "mysqlcheck",
		},
		MongoDB: []string{
			"mongo", "mongodump", "mongorestore", "mongosh",
			"fsync", "lock", "unlock",
		},
		Custom: make(map[string][]string),
	}
}

// IsAllowed checks if a command matches the allowlist for the given DB type.
func (h *HookAllowlist) IsAllowed(dbType string, command string) bool {
	var allowed []string
	switch dbType {
	case "postgresql", "postgres", "pg":
		allowed = h.PostgreSQL
	case "mysql", "mariadb":
		allowed = h.MySQL
	case "mongodb", "mongo":
		allowed = h.MongoDB
	default:
		if custom, ok := h.Custom[dbType]; ok {
			allowed = custom
		}
	}
	if len(allowed) == 0 {
		return true
	}
	parts := strings.Fields(command)
	if len(parts) == 0 {
		return false
	}
	baseCmd := strings.ToLower(parts[0])
	for _, pattern := range allowed {
		if strings.Contains(strings.ToLower(pattern), baseCmd) ||
			strings.Contains(baseCmd, strings.ToLower(pattern)) {
			return true
		}
	}
	return false
}

// AuditLogEntry represents a single hook execution for audit purposes.
type AuditLogEntry struct {
	Name        string
	Type        v1alpha1.HookType
	Namespace   string
	PodSelector string
	Command     string
	Allowlisted bool
	StartTime   time.Time
	EndTime     time.Time
	Output      string
	Status      string
}

// AuditLog maintains a log of hook executions.
type AuditLog struct {
	entries []AuditLogEntry
}

// NewAuditLog creates a new audit log.
func NewAuditLog() *AuditLog {
	return &AuditLog{entries: make([]AuditLogEntry, 0)}
}

// Record adds an entry to the audit log.
func (a *AuditLog) Record(entry AuditLogEntry) {
	a.entries = append(a.entries, entry)
}

// Entries returns all audit entries.
func (a *AuditLog) Entries() []AuditLogEntry {
	return a.entries
}

// LastEntry returns the most recent audit entry.
func (a *AuditLog) LastEntry() *AuditLogEntry {
	if len(a.entries) == 0 {
		return nil
	}
	return &a.entries[len(a.entries)-1]
}

// Export returns the audit log as a slice of maps for JSON serialization.
func (a *AuditLog) Export() []map[string]interface{} {
	result := make([]map[string]interface{}, len(a.entries))
	for i, e := range a.entries {
		result[i] = map[string]interface{}{
			"name":         e.Name,
			"type":         string(e.Type),
			"namespace":    e.Namespace,
			"pod_selector": e.PodSelector,
			"command":      e.Command,
			"allowlisted":  e.Allowlisted,
			"start_time":   e.StartTime.Format(time.RFC3339),
			"end_time":     e.EndTime.Format(time.RFC3339),
			"duration_ms":  e.EndTime.Sub(e.StartTime).Milliseconds(),
			"output":       e.Output,
			"status":       e.Status,
		}
	}
	return result
}

// HookExecutorWithAllowlist wraps a HookExecutor with allowlist validation and audit logging.
type HookExecutorWithAllowlist struct {
	wrapped   HookExecutor
	allowlist *HookAllowlist
	auditLog  *AuditLog
	dbType    string
}

// NewHookExecutorWithAllowlist creates a HookExecutor that validates commands against an allowlist.
func NewHookExecutorWithAllowlist(
	wrapped HookExecutor,
	allowlist *HookAllowlist,
	dbType string,
) *HookExecutorWithAllowlist {
	if allowlist == nil {
		allowlist = DefaultHookAllowlist()
	}
	return &HookExecutorWithAllowlist{
		wrapped:   wrapped,
		allowlist: allowlist,
		auditLog:  NewAuditLog(),
		dbType:    dbType,
	}
}

// ExecutePreQuiesce runs pre-quiesce hooks with allowlist validation and audit logging.
func (h *HookExecutorWithAllowlist) ExecutePreQuiesce(ctx context.Context, migration *v1alpha1.PortageMigration) error {
	for i := range migration.Spec.PreQuiesceHooks {
		hook := migration.Spec.PreQuiesceHooks[i]
		command := hook.CommandString()

		entry := AuditLogEntry{
			Name:        hook.Name,
			Type:        hook.Type,
			Namespace:   migration.Spec.SourceNamespace,
			PodSelector: podSelectorString(hook),
			Command:     command,
			Allowlisted: h.allowlist.IsAllowed(h.dbType, command),
			StartTime:   time.Now(),
			Status:      "pending",
		}

		if !entry.Allowlisted {
			entry.Status = "denied"
			entry.EndTime = time.Now()
			h.auditLog.Record(entry)
			return fmt.Errorf("hook %q command not in allowlist for %s: %s", hook.Name, h.dbType, command)
		}

		err := h.wrapped.ExecutePreQuiesce(ctx, migration)
		entry.EndTime = time.Now()
		if err != nil {
			entry.Status = "failed"
			entry.Output = err.Error()
		} else {
			entry.Status = "success"
		}
		h.auditLog.Record(entry)

		if err != nil {
			return err
		}
	}
	return nil
}

// ExecutePostQuiesce runs post-quiesce hooks with allowlist validation and audit logging.
func (h *HookExecutorWithAllowlist) ExecutePostQuiesce(ctx context.Context, migration *v1alpha1.PortageMigration) error {
	for i := range migration.Spec.PostQuiesceHooks {
		hook := migration.Spec.PostQuiesceHooks[i]
		command := hook.CommandString()

		entry := AuditLogEntry{
			Name:        hook.Name,
			Type:        hook.Type,
			Namespace:   migration.Spec.SourceNamespace,
			PodSelector: podSelectorString(hook),
			Command:     command,
			Allowlisted: h.allowlist.IsAllowed(h.dbType, command),
			StartTime:   time.Now(),
			Status:      "pending",
		}

		if !entry.Allowlisted {
			entry.Status = "denied"
			entry.EndTime = time.Now()
			h.auditLog.Record(entry)
			return fmt.Errorf("hook %q command not in allowlist for %s: %s", hook.Name, h.dbType, command)
		}

		err := h.wrapped.ExecutePostQuiesce(ctx, migration)
		entry.EndTime = time.Now()
		if err != nil {
			entry.Status = "failed"
			entry.Output = err.Error()
		} else {
			entry.Status = "success"
		}
		h.auditLog.Record(entry)

		if err != nil {
			return err
		}
	}
	return nil
}

// AuditLog returns the audit log for inspection.
func (h *HookExecutorWithAllowlist) AuditLog() *AuditLog {
	return h.auditLog
}

// podSelectorString converts a PodExecHook's label selector to a string.
func podSelectorString(hook v1alpha1.QuiescenceHook) string {
	if hook.PodExec != nil {
		labels := hook.PodExec.Selector.MatchLabels
		if len(labels) > 0 {
			parts := make([]string, 0, len(labels))
			for k, v := range labels {
				parts = append(parts, fmt.Sprintf("%s=%s", k, v))
			}
			return strings.Join(parts, ",")
		}
	}
	return ""
}
