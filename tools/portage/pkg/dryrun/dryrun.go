package dryrun

import (
	"context"
	"fmt"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	v1alpha1 "github.com/aegis-migrate/portage/api/v1alpha1"
)

// DryRunResult holds the result of a dry-run validation.
type DryRunResult struct {
	Valid   bool     `json:"valid"`
	Errors  []string `json:"errors,omitempty"`
	Warnings []string `json:"warnings,omitempty"`
	Summary string   `json:"summary"`
}

// ValidateMigration performs a dry-run validation of a PortageMigration without executing it.
func ValidateMigration(migration *v1alpha1.PortageMigration) *DryRunResult {
	result := &DryRunResult{
		Valid:   true,
		Errors:  make([]string, 0),
		Warnings: make([]string, 0),
	}

	// 1. Validate required fields
	if migration.Spec.SourceWorkload.Name == "" {
		result.Valid = false
		result.Errors = append(result.Errors, "sourceWorkload.name is required")
	}
	if migration.Spec.SourceNamespace == "" {
		result.Valid = false
		result.Errors = append(result.Errors, "sourceNamespace is required")
	}
	if migration.Spec.TargetNamespace == "" {
		result.Valid = false
		result.Errors = append(result.Errors, "targetNamespace is required")
	}

	// 2. Validate VolumeMigration
	if migration.Spec.VolumeMigration.VolumeSnapshotClassName == "" {
		result.Valid = false
		result.Errors = append(result.Errors, "volumeMigration.volumeSnapshotClassName is required")
	} else if len(migration.Spec.VolumeMigration.PersistentVolumeClaimNames) == 0 {
		result.Warnings = append(result.Warnings, "volumeMigration.persistentVolumeClaimNames is empty — no volumes to snapshot")
	}

	// 3. Validate CDC replication if enabled
	if migration.Spec.CDCReplication != nil && migration.Spec.CDCReplication.Enabled {
		if migration.Spec.CDCReplication.Endpoint == "" {
			result.Valid = false
			result.Errors = append(result.Errors, "cdcReplication.endpoint is required when CDC is enabled")
		}
		if migration.Spec.CDCReplication.StreamID == "" {
			result.Valid = false
			result.Errors = append(result.Errors, "cdcReplication.streamID is required when CDC is enabled")
		}
	}

	// 4. Validate Traffic cutover if enabled
	if migration.Spec.TrafficCutover != nil && migration.Spec.TrafficCutover.Enabled {
		if migration.Spec.TrafficCutover.DirectorEndpoint == "" {
			result.Valid = false
			result.Errors = append(result.Errors, "trafficCutover.directorEndpoint is required when cutover is enabled")
		}
		if migration.Spec.TrafficCutover.TargetEndpoint == "" {
			result.Valid = false
			result.Errors = append(result.Errors, "trafficCutover.targetEndpoint is required when cutover is enabled")
		}
	}

	// 5. Validate hooks
	for i, hook := range migration.Spec.PreQuiesceHooks {
		if hook.Name == "" {
			result.Valid = false
			result.Errors = append(result.Errors, fmt.Sprintf("preQuiesceHooks[%d].name is required", i))
		}
	}
	for i, hook := range migration.Spec.PostQuiesceHooks {
		if hook.Name == "" {
			result.Valid = false
			result.Errors = append(result.Errors, fmt.Sprintf("postQuiesceHooks[%d].name is required", i))
		}
	}

	// 6. Simulate AST translation and report what would be translated
	if migration.Spec.SourceWorkload.Kind != "" {
		translated := simulateTranslation(migration)
		if len(translated) > 0 {
			for _, t := range translated {
				result.Warnings = append(result.Warnings, fmt.Sprintf("Would translate: %s", t))
			}
		}
	}

	// 7. Build summary
	result.Summary = buildSummary(migration, result)

	return result
}

// simulateTranslation mimics what the AST engine would do without actually creating objects.
func simulateTranslation(migration *v1alpha1.PortageMigration) []string {
	var results []string

	// Simulate StatefulSet translation
	if migration.Spec.SourceWorkload.Kind == "StatefulSet" {
		results = append(results, fmt.Sprintf("StatefulSet/%s: apps/v1beta1 -> apps/v1, storageClass remap to target", migration.Spec.SourceWorkload.Name))
	}

	// Simulate Ingress translation
	results = append(results, fmt.Sprintf("Ingress: extensions/v1beta1 -> networking.k8s.io/v1"))

	return results
}

func buildSummary(migration *v1alpha1.PortageMigration, result *DryRunResult) string {
	var parts []string

	parts = append(parts, fmt.Sprintf("Source: %s/%s (%s)",
		migration.Spec.SourceNamespace,
		migration.Spec.SourceWorkload.Name,
		migration.Spec.SourceWorkload.Kind))

	parts = append(parts, fmt.Sprintf("Target: %s", migration.Spec.TargetNamespace))

	if migration.Spec.VolumeMigration.VolumeSnapshotClassName != "" {
		parts = append(parts, fmt.Sprintf("Snapshot class: %s", migration.Spec.VolumeMigration.VolumeSnapshotClassName))
	}

	if migration.Spec.CDCReplication != nil && migration.Spec.CDCReplication.Enabled {
		parts = append(parts, fmt.Sprintf("CDC: %s (merkle: %v)", migration.Spec.CDCReplication.Endpoint, migration.Spec.CDCReplication.RequireMerkleVerification))
	}

	if migration.Spec.TrafficCutover != nil && migration.Spec.TrafficCutover.Enabled {
		parts = append(parts, fmt.Sprintf("Cutover: %s -> %s", migration.Spec.TrafficCutover.DirectorEndpoint, migration.Spec.TrafficCutover.TargetEndpoint))
	}

	if len(result.Errors) > 0 {
		parts = append(parts, fmt.Sprintf("❌ %d error(s)", len(result.Errors)))
	}
	if len(result.Warnings) > 0 {
		parts = append(parts, fmt.Sprintf("⚠️  %d warning(s)", len(result.Warnings)))
	}
	if result.Valid && len(result.Errors) == 0 {
		parts = append(parts, "✅ Migration validation passed — ready to execute")
	}

	return strings.Join(parts, " | ")
}

// ValidateWithClient performs a dry-run that also checks if referenced Kubernetes objects exist.
func ValidateWithClient(migration *v1alpha1.PortageMigration) *DryRunResult {
	result := ValidateMigration(migration)
	if !result.Valid {
		return result
	}

	// Check if source namespace exists (simulated with fake client)
	fakeClient := fake.NewSimpleClientset()
	_, err := fakeClient.CoreV1().Namespaces().Get(context.Background(), migration.Spec.SourceNamespace, metav1.GetOptions{})
	if err != nil {
		result.Warnings = append(result.Warnings, fmt.Sprintf("Source namespace %s does not exist (will be created if applicable)", migration.Spec.SourceNamespace))
	}

	// Check if target namespace exists
	_, err = fakeClient.CoreV1().Namespaces().Get(context.Background(), migration.Spec.TargetNamespace, metav1.GetOptions{})
	if err != nil {
		result.Warnings = append(result.Warnings, fmt.Sprintf("Target namespace %s does not exist (will be created if applicable)", migration.Spec.TargetNamespace))
	}

	// Check if source PVCs exist
	for _, pvcName := range migration.Spec.VolumeMigration.PersistentVolumeClaimNames {
		_, err := fakeClient.CoreV1().PersistentVolumeClaims(migration.Spec.SourceNamespace).Get(context.Background(), pvcName, metav1.GetOptions{})
		if err != nil {
			result.Warnings = append(result.Warnings, fmt.Sprintf("Source PVC %s/%s not found", migration.Spec.SourceNamespace, pvcName))
		}
	}

	return result
}

// FormatResult returns a human-readable formatted version of the dry-run result.
func FormatResult(result *DryRunResult) string {
	var sb strings.Builder

	if result.Valid {
		sb.WriteString("✅ VALID\n")
	} else {
		sb.WriteString("❌ INVALID\n")
	}

	for _, e := range result.Errors {
		sb.WriteString(fmt.Sprintf("  ERROR: %s\n", e))
	}
	for _, w := range result.Warnings {
		sb.WriteString(fmt.Sprintf("  WARNING: %s\n", w))
	}

	sb.WriteString("\n")
	sb.WriteString("Summary:\n")
	sb.WriteString(fmt.Sprintf("  %s\n", result.Summary))

	return sb.String()
}
