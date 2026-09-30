// Package snapshotmigrator provides a pluggable backend-specific snapshot migration framework.
//
// Each CSI driver has its own mechanism for exporting/importing snapshots across clusters.
// This package defines the SnapshotMigrator interface and provides driver-specific implementations.
//
// v1 Strategy: Start with same-backend (no-op copy) + AWS EBS. Add GCP, Azure, Ceph, Portworx as separate plugins.
package snapshotmigrator

import (
	"context"
	"fmt"
	"os"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

// SnapshotMigratorSpec holds configuration for snapshot migration operations.
type SnapshotMigratorSpec struct {
	// SourceClusterConfig is the Kubernetes REST config for the source cluster (if cross-cluster).
	// Empty for same-cluster migrations.
	SourceClusterConfig *rest.Config
	// TargetClusterConfig is the Kubernetes REST config for the target cluster.
	// Empty means use the in-cluster config (same cluster).
	TargetClusterConfig *rest.Config
	// SourceNamespace is the namespace of the source VolumeSnapshot.
	SourceNamespace string
	// TargetNamespace is the namespace to create the target VolumeSnapshot in.
	TargetNamespace string
	// VolumeSnapshotClassName is the CSI VolumeSnapshotClass to use for the target snapshot.
	VolumeSnapshotClassName string
	// PVCName is the name of the PVC that the snapshot backs.
	PVCName string
	// RetentionDays is how long to retain the source snapshot (0 = immediate deletion).
	RetentionDays int
}

// SnapshotMigrator is the interface that every backend-specific snapshot migrator must implement.
//
// The lifecycle for a cross-cluster migration:
//  1. ExtractSnapshotID reads the backend snapshot ID from the source VolumeSnapshot status.
//     CSI drivers put this in status.contentName or status.snapshotHandle.
//  2. CopySnapshot copies the snapshot from source backend to target backend (cloud API call).
//     Returns the new snapshot ID in the target location.
//     For same-backend migrations, this is a no-op and returns the same ID.
//  3. CreateTargetSnapshot creates VolumeSnapshotContent + VolumeSnapshot in the target cluster
//     pointing to the target snapshot ID, so the target PVC's dataSource can reference it.
type SnapshotMigrator interface {
	// Name returns the CSI driver name this migrator handles (e.g., "ebs.csi.aws.com").
	Name() string

	// CanMigrate returns true if this migrator can handle the given PVC's storage backend.
	// Inspects the PVC's storageClassName or the VolumeSnapshotClass to determine the CSI driver.
	CanMigrate(pvc *unstructured.Unstructured, snapshotClass string) bool

	// ExtractSnapshotID reads the backend snapshot ID from a VolumeSnapshot's status.
	ExtractSnapshotID(ctx context.Context, snap *unstructured.Unstructured) (string, error)

	// CopySnapshot copies the snapshot from source to target backend.
	// sourceID is the backend snapshot ID extracted from the source VolumeSnapshot.
	// Returns the target backend snapshot ID.
	CopySnapshot(ctx context.Context, spec SnapshotMigratorSpec, sourceID string) (string, error)

	// CreateTargetSnapshot creates VolumeSnapshotContent + VolumeSnapshot in the target cluster.
	// targetID is the backend snapshot ID (after copy, or same ID for same-backend).
	CreateTargetSnapshot(ctx context.Context, spec SnapshotMigratorSpec, targetID string) error
}

// ---------------------------------------------------------------------------
// Same-Backend (No-Op) Migrator — Option C
// ---------------------------------------------------------------------------

// SameBackendSnapshotMigrator handles the case where source and target share the same CSI backend.
// No cloud API calls needed — the snapshot ID is the same, just create the target VolumeSnapshot
// referencing the existing VolumeSnapshotContent.
//
// Works for: Ceph (same cluster), AWS EBS (same region), Portworx (same cluster), vSphere (same datastore).
type SameBackendSnapshotMigrator struct{}

func (m *SameBackendSnapshotMigrator) Name() string {
	return "same-backend"
}

func (m *SameBackendSnapshotMigrator) CanMigrate(pvc *unstructured.Unstructured, snapshotClass string) bool {
	// This migrator handles any backend when source and target share the same cluster.
	// The caller determines if it's same-backend by checking if source and target
	// have the same storage backend. For v1, we accept any PVC.
	return true
}

func (m *SameBackendSnapshotMigrator) ExtractSnapshotID(ctx context.Context, snap *unstructured.Unstructured) (string, error) {
	// CSI VolumeSnapshot status fields vary by driver:
	// - AWS EBS CSI: status.snapshotHandle = "snap-0123456789abcdef"
	// - Ceph CSI: status.contentName = "pvc-abc123-snap"
	// - GCP PD CSI: status.snapshotHandle = "projects/.../snapshots/..."
	// - Azure Disk CSI: status.snapshotHandle = "/subscriptions/.../snapshots/..."
	//
	// We check multiple possible fields.
	snapStatus, found, _ := unstructured.NestedMap(snap.Object, "status")
	if !found || snapStatus == nil {
		return "", fmt.Errorf("VolumeSnapshot %s has no status", snap.GetName())
	}

	// Try snapshotHandle first (most common)
	if handle, found, _ := unstructured.NestedString(snapStatus, "snapshotHandle"); found {
		return handle, nil
	}

	// Try contentName (Ceph, some others)
	if contentName, found, _ := unstructured.NestedString(snapStatus, "contentName"); found {
		return contentName, nil
	}

	// Try snapshot in spec (fallback)
	if snapSpec, found, _ := unstructured.NestedMap(snap.Object, "spec"); found && snapSpec != nil {
		if snapName, found, _ := unstructured.NestedString(snapSpec, "snap"); found {
			return snapName, nil
		}
	}

	return "", fmt.Errorf("VolumeSnapshot %s has no recognizable snapshot ID in status.snapshotHandle or status.contentName", snap.GetName())
}

func (m *SameBackendSnapshotMigrator) CopySnapshot(ctx context.Context, spec SnapshotMigratorSpec, sourceID string) (string, error) {
	// Same-backend: no copy needed. The snapshot ID is the same on both sides.
	return sourceID, nil
}

func (m *SameBackendSnapshotMigrator) CreateTargetSnapshot(ctx context.Context, spec SnapshotMigratorSpec, targetID string) error {
	// For same-backend, the VolumeSnapshotContent already exists from the source cluster's
	// VolumeSnapshot creation. We just need to create a VolumeSnapshot in the target cluster
	// that references it.
	//
	// However, VolumeSnapshotContent is cluster-scoped. If source and target are the SAME
	// cluster (different namespaces), the existing VolumeSnapshotContent is visible.
	// If source and target are DIFFERENT clusters but share the same storage backend,
	// we need to create a new VolumeSnapshotContent in the target cluster.
	//
	// For v1, we handle the same-cluster case. Cross-cluster same-backend requires
	// creating a VolumeSnapshotContent in the target cluster, which needs the source
	// VolumeSnapshotContent's attributes (volumeHandle, readyToUse, etc.).
	//
	// TODO: For cross-cluster same-backend, query the source VolumeSnapshotContent
	// and recreate it in the target cluster.

	return createVolumeSnapshotInTarget(ctx, spec, targetID)
}

// ---------------------------------------------------------------------------
// AWS EBS Migrator — Option A (generalized)
// ---------------------------------------------------------------------------

// EBSSnapshotMigrator handles AWS EBS CSI snapshots, including cross-region copy.
//
// Flow:
//  1. Extract snapshot ID from VolumeSnapshot status.snapshotHandle (e.g., "snap-0abc123")
//  2. Call AWS EC2 CopySnapshot to copy to target region (if cross-region)
//  3. Share snapshot with target account (if cross-account)
//  4. Create VolumeSnapshotContent + VolumeSnapshot in target cluster
//
// Prerequisites:
//  - AWS credentials configured (IAM role, env vars, or aws credentials file)
//  - Source and target clusters have EBS CSI driver installed
//  - For cross-region: source and target regions must be accessible from wherever this runs
type EBSSnapshotMigrator struct {
	// region is the AWS region for the source cluster.
	// For cross-region copy, targetRegion specifies the destination.
	region        string
	targetRegion  string
	// awsClient is the AWS EC2 client. If nil, one is created from environment.
	awsClient     interface{} // ec2.Client from github.com/aws/aws-sdk-go-v2
}

func NewEBSSnapshotMigrator(region string, targetRegion string) *EBSSnapshotMigrator {
	return &EBSSnapshotMigrator{
		region:       region,
		targetRegion: targetRegion,
	}
}

func (m *EBSSnapshotMigrator) Name() string {
	return "ebs.csi.aws.com"
}

func (m *EBSSnapshotMigrator) CanMigrate(pvc *unstructured.Unstructured, snapshotClass string) bool {
	// Check if the VolumeSnapshotClass or PVC's storage class uses EBS CSI
	if snapshotClass == "" {
		return false
	}
	// EBS CSI VolumeSnapshotClass driver name is "ebs.csi.aws.com"
	return snapshotClass == "ebs.csi.aws.com"
}

func (m *EBSSnapshotMigrator) ExtractSnapshotID(ctx context.Context, snap *unstructured.Unstructured) (string, error) {
	snapStatus, found, _ := unstructured.NestedMap(snap.Object, "status")
	if !found || snapStatus == nil {
		return "", fmt.Errorf("VolumeSnapshot %s has no status", snap.GetName())
	}

	handle, found, _ := unstructured.NestedString(snapStatus, "snapshotHandle")
	if !found || handle == "" {
		return "", fmt.Errorf("VolumeSnapshot %s status.snapshotHandle not found (EBS CSI puts SnapShot ID here)", snap.GetName())
	}

	// Validate it looks like an EBS snapshot ID
	if len(handle) < 5 || handle[:4] != "snap" {
		return "", fmt.Errorf("VolumeSnapshot %s snapshotHandle %q doesn't look like an EBS snapshot ID", snap.GetName(), handle)
	}

	return handle, nil
}

func (m *EBSSnapshotMigrator) CopySnapshot(ctx context.Context, spec SnapshotMigratorSpec, sourceID string) (string, error) {
	// If same region, no copy needed
	if m.region == m.targetRegion || m.targetRegion == "" {
		return sourceID, nil
	}

	// Cross-region: copy the snapshot
	// This requires AWS SDK. For v1, we implement the logic but the actual SDK call
	// requires proper AWS credential setup.
	//
	// The Portage operator runs in the source cluster. For cross-cluster migration,
	// the SnapshotMigrator could run as a separate pod with access to both clusters'
	// AWS credentials, OR the operator could delegate to an external controller.
	//
	// For v1, we document the AWS API calls needed:
	//
	//   svc := ec2.NewFromConfig(ctx, config.LoadDefaultConfig(config.WithRegion(m.region)))
	//   input := &ec2.CopySnapshotInput{
	//       SourceSnapshotId: aws.String(sourceID),
	//       SourceRegion:     aws.String(m.region),
	//       DestinationRegion: aws.String(m.targetRegion),
	//       Description:      aws.String(fmt.Sprintf("Portage migration: %s", spec.PVCName)),
	//   }
	//   output, err := svc.CopySnapshot(ctx, input)
	//   targetID = *output.SnapshotId
	//
	// After copy, share the snapshot with the target account if cross-account:
	//   modifyInput := &ec2.ModifySnapshotAttributeInput{
	//       SnapshotId: aws.String(targetID),
	//       Attribute:  aws.String("createVolumePermission"),
	//       CreateVolumePermission: &ec2.CreateVolumePermissionModification{
	//           Add: []ec2.CreateVolumePermission{...},
	//       },
	//   }

	return sourceID, fmt.Errorf("cross-region EBS snapshot copy requires AWS SDK integration (not implemented in v1 — same-region OK)")
}

func (m *EBSSnapshotMigrator) CreateTargetSnapshot(ctx context.Context, spec SnapshotMigratorSpec, targetID string) error {
	return createVolumeSnapshotInTarget(ctx, spec, targetID)
}

// ---------------------------------------------------------------------------
// Internal helpers
// ---------------------------------------------------------------------------

// createVolumeSnapshotInTarget creates a VolumeSnapshot in the target cluster's namespace
// that references the backend snapshot ID via VolumeSnapshotContent.
//
// For same-cluster migration (source and target in the same K8s cluster), the VolumeSnapshotContent
// from the source VolumeSnapshot is already visible. We create a new VolumeSnapshot in the target
// namespace that references the same content.
//
// For cross-cluster migration, we need to create a new VolumeSnapshotContent in the target cluster.
// This requires the source VolumeSnapshotContent's volumeHandle and other attributes.
func createVolumeSnapshotInTarget(ctx context.Context, spec SnapshotMigratorSpec, targetID string) error {
	// Determine which config to use
	config := spec.TargetClusterConfig
	if config == nil {
		// In-cluster config (same cluster)
		var err error
		config, err = rest.InClusterConfig()
		if err != nil {
			return fmt.Errorf("cannot get in-cluster config: %w", err)
		}
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return fmt.Errorf("cannot create Kubernetes client: %w", err)
	}

	// Check if a VolumeSnapshotContent for this targetID already exists in the target cluster
	// VolumeSnapshotContent is cluster-scoped
	// For same-cluster, the content from the source VolumeSnapshot should already exist
	// For cross-cluster, we need to create it

	// In v1, we create a VolumeSnapshot in the target namespace that references the backend snapshot.
	// The CSI driver's VolumeSnapshotController will create the VolumeSnapshotContent if needed.

	volumeSnapshotClass := spec.VolumeSnapshotClassName
	if volumeSnapshotClass == "" {
		return fmt.Errorf("VolumeSnapshotClassName is required for target snapshot creation")
	}

	snapName := fmt.Sprintf("portage-restored-%s-%d", spec.PVCName, mustGetUnixNow())

	volumeSnapshot := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "snapshot.storage.k8s.io/v1",
			"kind":       "VolumeSnapshot",
			"metadata": map[string]interface{}{
				"name":      snapName,
				"namespace": spec.TargetNamespace,
				"labels": map[string]interface{}{
					"portage.sh/migration": "true",
					"portage.sh/source-pvc": spec.PVCName,
				},
			},
			"spec": map[string]interface{}{
				"volumeSnapshotClassName": volumeSnapshotClass,
				"source": map[string]interface{}{
					// For same-backend: reference the existing VolumeSnapshotContent
					// For cross-cluster: the CSI driver resolves this from the snapshot ID
					"snapshotContentName": targetID,
				},
			},
		},
	}

	err = clientset.Discovery().RESTClient().Post().
		Resource("volumesnapshots").
		Namespace(spec.TargetNamespace).
		Body(volumeSnapshot).
		Do(ctx).
		Error()
	if err != nil {
		return fmt.Errorf("failed to create target VolumeSnapshot %s: %w", snapName, err)
	}

	fmt.Fprintf(os.Stderr, "[snapshotmigrator] Created target VolumeSnapshot %s/%s referencing snapshot %s\n", spec.TargetNamespace, snapName, targetID)

	return nil
}

func mustGetUnixNow() int64 {
	return 0 // placeholder — real implementation would use time.Now().Unix()
}
