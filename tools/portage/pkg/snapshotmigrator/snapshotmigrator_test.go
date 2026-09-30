package snapshotmigrator

import (
	"context"
	"testing"

	"gopkg.in/yaml.v3"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestSameBackendExtractSnapshotID(t *testing.T) {
	// Test that we can extract snapshot IDs from different CSI driver formats
	tests := []struct {
		name     string
		snapYAML string
		want     string
		wantErr  bool
	}{
		{
			name: "EBS CSI snapshotHandle",
			snapYAML: `
metadata:
  name: test-snap
  namespace: default
status:
  readyToUse: true
  snapshotHandle: snap-0123456789abcdef0
`,
			want:    "snap-0123456789abcdef0",
			wantErr: false,
		},
		{
			name: "Ceph CSI contentName",
			snapYAML: `
metadata:
  name: test-snap
  namespace: default
status:
  readyToUse: true
  contentName: pvc-abc123-def456-snap
`,
			want:    "pvc-abc123-def456-snap",
			wantErr: false,
		},
		{
			name: "GCP PD CSI snapshotHandle",
			snapYAML: `
metadata:
  name: test-snap
  namespace: default
status:
  readyToUse: true
  snapshotHandle: projects/my-project/global/snapshots/my-snapshot
`,
			want:    "projects/my-project/global/snapshots/my-snapshot",
			wantErr: false,
		},
		{
			name: "No status",
			snapYAML: `
metadata:
  name: test-snap
  namespace: default
`,
			wantErr: true,
		},
		{
			name: "Empty snapshotHandle",
			snapYAML: `
metadata:
  name: test-snap
  namespace: default
status:
  readyToUse: true
`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			snap := &unstructured.Unstructured{}
			if err := yaml.Unmarshal([]byte(tt.snapYAML), &snap.Object); err != nil {
				t.Fatalf("failed to unmarshal test YAML: %v", err)
			}

			migrator := &SameBackendSnapshotMigrator{}
			got, err := migrator.ExtractSnapshotID(context.Background(), snap)

			if tt.wantErr {
				if err == nil {
					t.Errorf("expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}

			if got != tt.want {
				t.Errorf("ExtractSnapshotID() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestEBSCanMigrate(t *testing.T) {
	migrator := NewEBSSnapshotMigrator("us-east-1", "")

	tests := []struct {
		name          string
		snapshotClass string
		want          bool
	}{
		{
			name:          "EBS CSI class",
			snapshotClass: "ebs.csi.aws.com",
			want:          true,
		},
		{
			name:          "Ceph CSI class",
			snapshotClass: "rbd.csi.ceph.com",
			want:          false,
		},
		{
			name:          "Empty class",
			snapshotClass: "",
			want:          false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pvc := &unstructured.Unstructured{}
			if got := migrator.CanMigrate(pvc, tt.snapshotClass); got != tt.want {
				t.Errorf("CanMigrate() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSameBackendCanMigrate(t *testing.T) {
	migrator := &SameBackendSnapshotMigrator{}

	// SameBackend migrator accepts any PVC (it's a fallback)
	pvc := &unstructured.Unstructured{}
	if !migrator.CanMigrate(pvc, "") {
		t.Error("SameBackend migrator should accept any PVC")
	}
	if !migrator.CanMigrate(pvc, "ebs.csi.aws.com") {
		t.Error("SameBackend migrator should accept EBS PVCs too")
	}
	if !migrator.CanMigrate(pvc, "rbd.csi.ceph.com") {
		t.Error("SameBackend migrator should accept Ceph PVCs too")
	}
}
