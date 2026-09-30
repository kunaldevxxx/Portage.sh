package quiesce

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// PGQuiescence provides database-aware quiescence for PostgreSQL using
// native pg_backup_start/pg_backup_stop APIs instead of generic hooks.
type PGQuiescence struct {
	MinBackupDuration time.Duration
	MaxBackupWait     time.Duration
}

func NewPGQuiescence() *PGQuiescence {
	return &PGQuiescence{
		MinBackupDuration: 2 * time.Second,
		MaxBackupWait:     30 * time.Second,
	}
}

// PreQuiesce performs PostgreSQL-native backup start.
func (p *PGQuiescence) PreQuiesce(ctx context.Context, namespace string, podName string, container string, runner PodCommandRunner) error {
	cmd := []string{"psql", "-c", "SELECT pg_backup_start('portage-migration', false);"}
	output, err := runWithRunner(ctx, runner, namespace, podName, container, cmd)
	if err != nil {
		return fmt.Errorf("pg_backup_start failed: %w (output: %s)", err, output)
	}

	checkCmd := []string{"psql", "-c", "SELECT pg_is_in_recovery();"}
	checkOutput, err := runWithRunner(ctx, runner, namespace, podName, container, checkCmd)
	if err != nil {
		return fmt.Errorf("backup verification failed: %w", err)
	}
	if strings.TrimSpace(checkOutput) != "t" {
		return fmt.Errorf("expected pg_is_in_recovery() to return 't' after backup start, got: %s", checkOutput)
	}

	time.Sleep(p.MinBackupDuration)
	return nil
}

// PostQuiesce performs PostgreSQL-native backup stop.
func (p *PGQuiescence) PostQuiesce(ctx context.Context, namespace string, podName string, container string, runner PodCommandRunner) error {
	cmd := []string{"psql", "-c", "SELECT pg_backup_stop(false);"}
	output, err := runWithRunner(ctx, runner, namespace, podName, container, cmd)
	if err != nil {
		return fmt.Errorf("pg_backup_stop failed: %w (output: %s)", err, output)
	}
	return nil
}

// CheckBackupStatus verifies the current backup state.
func (p *PGQuiescence) CheckBackupStatus(ctx context.Context, namespace string, podName string, container string, runner PodCommandRunner) (string, error) {
	cmd := []string{"psql", "-c", "SELECT pg_backup_label(), pg_is_in_backup();"}
	output, err := runWithRunner(ctx, runner, namespace, podName, container, cmd)
	if err != nil {
		return "", fmt.Errorf("backup status check failed: %w", err)
	}
	return strings.TrimSpace(output), nil
}

func runWithRunner(ctx context.Context, runner PodCommandRunner, namespace, podName, container string, cmd []string) (string, error) {
	if runner == nil {
		return "", fmt.Errorf("no PodCommandRunner available")
	}
	return runner.RunCommand(ctx, namespace, podName, container, cmd)
}
