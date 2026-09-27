package controllers

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	v1alpha1 "github.com/aegis-migrate/portage/api/v1alpha1"
	"github.com/aegis-migrate/portage/pkg/ast"
)

// MockHookExecutor records execution of quiescence hooks
type MockHookExecutor struct {
	PreQuiesceCalled  bool
	PostQuiesceCalled bool
	PreQuiesceErr     error
	PostQuiesceErr    error
}

func (m *MockHookExecutor) ExecutePreQuiesce(ctx context.Context, migration *v1alpha1.PortageMigration) error {
	m.PreQuiesceCalled = true
	return m.PreQuiesceErr
}

func (m *MockHookExecutor) ExecutePostQuiesce(ctx context.Context, migration *v1alpha1.PortageMigration) error {
	m.PostQuiesceCalled = true
	return m.PostQuiesceErr
}

func TestQuiescenceReconcilerStatefulSetMigration(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	_ = v1alpha1.AddToScheme(scheme)

	srcNamespace := "source-db"
	tgtNamespace := "target-db"
	pvcName := "data-postgres-0"

	// 1. Setup Source PVC
	sourcePVC := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      pvcName,
			Namespace: srcNamespace,
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
		},
	}

	// 2. Setup PortageMigration CR
	migration := &v1alpha1.PortageMigration{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "migrate-postgres",
			Namespace: srcNamespace,
		},
		Spec: v1alpha1.PortageMigrationSpec{
			SourceWorkload: corev1.TypedLocalObjectReference{
				Kind: "StatefulSet",
				Name: "postgres",
			},
			SourceNamespace: srcNamespace,
			TargetNamespace: tgtNamespace,
			PreQuiesceHooks: []v1alpha1.QuiescenceHook{
				{
					Name: "pg_flush",
					Type: v1alpha1.HookTypePodExec,
					PodExec: &v1alpha1.PodExecHook{
						Command: []string{"psql", "-c", "CHECKPOINT;"},
					},
				},
			},
			PostQuiesceHooks: []v1alpha1.QuiescenceHook{
				{
					Name: "pg_unlock",
					Type: v1alpha1.HookTypePodExec,
					PodExec: &v1alpha1.PodExecHook{
						Command: []string{"psql", "-c", "SELECT pg_wal_replay_resume();"},
					},
				},
			},
			VolumeMigration: v1alpha1.VolumeMigrationSpec{
				VolumeSnapshotClassName:    "csi-aws-vsc",
				PersistentVolumeClaimNames: []string{pvcName},
			},
		},
		Status: v1alpha1.PortageMigrationStatus{
			Phase: v1alpha1.PhasePending,
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(sourcePVC, migration).
		WithStatusSubresource(migration).
		Build()

	mockHooks := &MockHookExecutor{}
	reconciler := &PortageMigrationReconciler{
		Client:       fakeClient,
		Scheme:       scheme,
		HookExecutor: mockHooks,
		ASTEngine:    ast.NewASTTranslationEngine(),
		TargetClient: fakeClient,
	}

	ctx := context.Background()
	req := ctrl.Request{
		NamespacedName: types.NamespacedName{
			Namespace: srcNamespace,
			Name:      migration.Name,
		},
	}

	// Phase 1: Pending -> Validating
	_, err := reconciler.Reconcile(ctx, req)
	if err != nil {
		t.Fatalf("reconcile pending failed: %v", err)
	}

	_ = fakeClient.Get(ctx, req.NamespacedName, migration)
	if migration.Status.Phase != v1alpha1.PhaseValidating {
		t.Fatalf("expected phase Validating, got %s", migration.Status.Phase)
	}

	// Phase 2: Validating -> Quiescing
	_, err = reconciler.Reconcile(ctx, req)
	if err != nil {
		t.Fatalf("reconcile validating failed: %v", err)
	}

	_ = fakeClient.Get(ctx, req.NamespacedName, migration)
	if migration.Status.Phase != v1alpha1.PhaseQuiescing {
		t.Fatalf("expected phase Quiescing, got %s", migration.Status.Phase)
	}

	// Phase 3: Quiescing (PreHook runs, VolumeSnapshot created) -> Snapshotting
	_, err = reconciler.Reconcile(ctx, req)
	if err != nil {
		t.Fatalf("reconcile quiescing failed: %v", err)
	}

	if !mockHooks.PreQuiesceCalled {
		t.Fatalf("expected PreQuiesce hook to have been executed")
	}

	_ = fakeClient.Get(ctx, req.NamespacedName, migration)
	if migration.Status.Phase != v1alpha1.PhaseSnapshotting {
		t.Fatalf("expected phase Snapshotting, got %s", migration.Status.Phase)
	}
	if len(migration.Status.CreatedSnapshots) != 1 {
		t.Fatalf("expected 1 snapshot created, got %d", len(migration.Status.CreatedSnapshots))
	}

	snapName := migration.Status.CreatedSnapshots[0]

	// Simulate CSI Driver marking VolumeSnapshot as readyToUse
	var snap unstructured.Unstructured
	snap.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "snapshot.storage.k8s.io",
		Version: "v1",
		Kind:    "VolumeSnapshot",
	})
	_ = fakeClient.Get(ctx, types.NamespacedName{Namespace: srcNamespace, Name: snapName}, &snap)
	_ = unstructured.SetNestedField(snap.Object, true, "status", "readyToUse")
	_ = fakeClient.Update(ctx, &snap)

	// Phase 4: Snapshotting (snapshot ready, PostHook runs) -> ApplyingTarget
	_, err = reconciler.Reconcile(ctx, req)
	if err != nil {
		t.Fatalf("reconcile snapshotting failed: %v", err)
	}

	if !mockHooks.PostQuiesceCalled {
		t.Fatalf("expected PostQuiesce hook to be executed immediately after snapshot ready")
	}

	_ = fakeClient.Get(ctx, req.NamespacedName, migration)
	if migration.Status.Phase != v1alpha1.PhaseApplyingTarget {
		t.Fatalf("expected phase ApplyingTarget, got %s", migration.Status.Phase)
	}

	// Phase 5: ApplyingTarget -> Completed (Target PVC created from VolumeSnapshot)
	_, err = reconciler.Reconcile(ctx, req)
	if err != nil {
		t.Fatalf("reconcile applying target failed: %v", err)
	}

	_ = fakeClient.Get(ctx, req.NamespacedName, migration)
	if migration.Status.Phase != v1alpha1.PhaseCompleted {
		t.Fatalf("expected phase Completed, got %s (error: %s)", migration.Status.Phase, migration.Status.ErrorMessage)
	}

	// Verify Target PVC exists with proper dataSource
	var targetPVC corev1.PersistentVolumeClaim
	err = fakeClient.Get(ctx, types.NamespacedName{Namespace: tgtNamespace, Name: pvcName}, &targetPVC)
	if err != nil {
		t.Fatalf("target PVC was not created: %v", err)
	}
	if targetPVC.Spec.DataSource == nil || targetPVC.Spec.DataSource.Name != snapName {
		t.Fatalf("expected target PVC dataSource to reference %s, got %+v", snapName, targetPVC.Spec.DataSource)
	}
}
