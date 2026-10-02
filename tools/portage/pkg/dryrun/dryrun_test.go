package dryrun

import (
	"testing"
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	v1alpha1 "github.com/aegis-migrate/portage/api/v1alpha1"
)

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
				Enabled:   true,
				Endpoint:  "http://cdc:8080",
				StreamID:  "stream-1",
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
				// Missing endpoint
			},
		},
	}

	result := ValidateMigration(m)
	if result.Valid {
		t.Fatal("expected invalid due to missing CDC endpoint")
	}
	if result.Errors[0] != "cdcReplication.endpoint is required when CDC is enabled" {
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
				// Missing directorEndpoint and targetEndpoint
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
				PersistentVolumeClaimNames: []string{}, // explicitly empty
			},
		},
	}

	result := ValidateMigration(m)
	if !result.Valid {
		t.Fatalf("expected valid (empty PVCs with snapshot class is a warning, not error), got errors: %v", result.Errors)
	}
	// Should have a warning about empty PVCs
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
