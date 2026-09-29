package controllers

import (
	"context"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	v1alpha1 "github.com/aegis-migrate/portage/api/v1alpha1"
	"github.com/aegis-migrate/portage/pkg/ast"
	"github.com/aegis-migrate/portage/pkg/cdc"
	"github.com/aegis-migrate/portage/pkg/cutover"
)

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

func TestPortageMigrationFullLifecycle(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	_ = appsv1.AddToScheme(scheme)
	_ = networkingv1.AddToScheme(scheme)
	_ = v1alpha1.AddToScheme(scheme)

	srcNamespace := "source-prod"
	tgtNamespace := "target-prod"
	pvcName := "data-postgres-0"
	workloadName := "postgres"

	// 1. Source PVC
	sourcePVC := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      pvcName,
			Namespace: srcNamespace,
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
		},
	}

	// 2. Source StatefulSet (legacy spec needing AST translation)
	sourceStatefulSet := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "apps/v1",
			"kind":       "StatefulSet",
			"metadata": map[string]interface{}{
				"name":      workloadName,
				"namespace": srcNamespace,
				"labels": map[string]interface{}{
					"app.kubernetes.io/name": "postgres",
				},
				"resourceVersion": "987654",
			},
			"spec": map[string]interface{}{
				"serviceName": workloadName,
				"replicas":    int64(2),
				"selector": map[string]interface{}{
					"matchLabels": map[string]interface{}{
						"app.kubernetes.io/name": "postgres",
					},
				},
				"template": map[string]interface{}{
					"metadata": map[string]interface{}{
						"labels": map[string]interface{}{
							"app.kubernetes.io/name": "postgres",
						},
					},
					"spec": map[string]interface{}{
						"containers": []interface{}{
							map[string]interface{}{
								"name":  "postgres",
								"image": "postgres:15-alpine",
							},
						},
					},
				},
				"volumeClaimTemplates": []interface{}{
					map[string]interface{}{
						"metadata": map[string]interface{}{
							"name": pvcName,
						},
						"spec": map[string]interface{}{
							"accessModes":      []interface{}{"ReadWriteOnce"},
							"storageClassName": "old-gp2",
						},
					},
				},
			},
		},
	}

	// 3. Source Ingress (extensions/v1beta1 needing translation)
	sourceIngress := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "extensions/v1beta1",
			"kind":       "Ingress",
			"metadata": map[string]interface{}{
				"name":      "postgres-ingress",
				"namespace": srcNamespace,
				"annotations": map[string]interface{}{
					"kubernetes.io/ingress.class": "nginx",
				},
			},
			"spec": map[string]interface{}{
				"backend": map[string]interface{}{
					"serviceName": "postgres-svc",
					"servicePort": int64(5432),
				},
			},
		},
	}

	// 4. PortageMigration Custom Resource
	migration := &v1alpha1.PortageMigration{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "migrate-postgres-e2e",
			Namespace: srcNamespace,
		},
		Spec: v1alpha1.PortageMigrationSpec{
			SourceWorkload: corev1.TypedLocalObjectReference{
				Kind: "StatefulSet",
				Name: workloadName,
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
			CDCReplication: &v1alpha1.CDCReplicationSpec{
				Enabled:                   true,
				Endpoint:                  "http://synapse-cdc.migration.svc:8080",
				StreamID:                  "stream-pg-01",
				RequireMerkleVerification: true,
				MaxAllowedLagSeconds:      5,
			},
			TrafficCutover: &v1alpha1.TrafficCutoverSpec{
				Enabled:             true,
				DirectorEndpoint:    "http://ghostwire.migration.svc:8080",
				RouteID:             "route-pg-prod",
				TargetEndpoint:      "postgres.target-prod.svc:5432",
				DrainTimeoutSeconds: 10,
			},
			VolumeMigration: v1alpha1.VolumeMigrationSpec{
				VolumeSnapshotClassName:    "csi-aws-vsc",
				TargetStorageClassName:     "fast-nvme-sc",
				PersistentVolumeClaimNames: []string{pvcName},
			},
		},
		Status: v1alpha1.PortageMigrationStatus{
			Phase: v1alpha1.PhasePending,
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(sourcePVC, migration, sourceStatefulSet, sourceIngress).
		WithStatusSubresource(migration).
		Build()

	ctx := context.Background()

	mockHooks := &MockHookExecutor{}
	mockCDC := &cdc.MockCDCClient{LagSeconds: 1}
	mockCutover := &cutover.MockCutoverClient{}

	reconciler := &PortageMigrationReconciler{
		Client:        fakeClient,
		Scheme:        scheme,
		HookExecutor:  mockHooks,
		ASTEngine:     ast.NewASTTranslationEngine(),
		TargetClient:  fakeClient,
		CDCClient:     mockCDC,
		CutoverClient: mockCutover,
	}

	req := ctrl.Request{
		NamespacedName: types.NamespacedName{
			Namespace: srcNamespace,
			Name:      migration.Name,
		},
	}

	// Step 1: Pending -> Validating
	_, err := reconciler.Reconcile(ctx, req)
	if err != nil {
		t.Fatalf("reconcile pending failed: %v", err)
	}
	_ = fakeClient.Get(ctx, req.NamespacedName, migration)
	if migration.Status.Phase != v1alpha1.PhaseValidating {
		t.Fatalf("expected Validating, got %s", migration.Status.Phase)
	}

	// Step 2: Validating -> Translating
	_, err = reconciler.Reconcile(ctx, req)
	if err != nil {
		t.Fatalf("reconcile validating failed: %v", err)
	}
	_ = fakeClient.Get(ctx, req.NamespacedName, migration)
	if migration.Status.Phase != v1alpha1.PhaseTranslating {
		t.Fatalf("expected Translating, got %s", migration.Status.Phase)
	}

	// Step 3: Translating -> ReplicatingData (AST Translation executed)
	_, err = reconciler.Reconcile(ctx, req)
	if err != nil {
		t.Fatalf("reconcile translating failed: %v", err)
	}
	_ = fakeClient.Get(ctx, req.NamespacedName, migration)
	if migration.Status.Phase != v1alpha1.PhaseReplicatingData {
		t.Fatalf("expected ReplicatingData, got %s", migration.Status.Phase)
	}
	if len(migration.Status.TranslatedObjects) == 0 {
		t.Fatalf("expected translated objects to be recorded, got empty")
	}

	// Step 4: ReplicatingData -> Quiescing (CDC replication & Merkle verification executed)
	_, err = reconciler.Reconcile(ctx, req)
	if err != nil {
		t.Fatalf("reconcile replicating data failed: %v", err)
	}
	if !mockCDC.ReplicationStarted {
		t.Fatalf("expected CDC replication to have been started")
	}
	if !mockCDC.Verified {
		t.Fatalf("expected Merkle verification to have been invoked")
	}
	_ = fakeClient.Get(ctx, req.NamespacedName, migration)
	if migration.Status.Phase != v1alpha1.PhaseQuiescing {
		t.Fatalf("expected Quiescing, got %s", migration.Status.Phase)
	}
	if !migration.Status.MerkleVerified {
		t.Fatalf("expected status.merkleVerified to be true")
	}

	// Step 5: Quiescing -> Snapshotting (PreQuiesce lock executed, VolumeSnapshot created)
	_, err = reconciler.Reconcile(ctx, req)
	if err != nil {
		t.Fatalf("reconcile quiescing failed: %v", err)
	}
	if !mockHooks.PreQuiesceCalled {
		t.Fatalf("expected PreQuiesce hook to run")
	}
	_ = fakeClient.Get(ctx, req.NamespacedName, migration)
	if migration.Status.Phase != v1alpha1.PhaseSnapshotting {
		t.Fatalf("expected Snapshotting, got %s", migration.Status.Phase)
	}
	if len(migration.Status.CreatedSnapshots) != 1 {
		t.Fatalf("expected 1 snapshot, got %d", len(migration.Status.CreatedSnapshots))
	}
	snapName := migration.Status.CreatedSnapshots[0]

	// Simulate CSI VolumeSnapshot becoming Ready
	var snap unstructured.Unstructured
	snap.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "snapshot.storage.k8s.io",
		Version: "v1",
		Kind:    "VolumeSnapshot",
	})
	_ = fakeClient.Get(ctx, types.NamespacedName{Namespace: srcNamespace, Name: snapName}, &snap)
	_ = unstructured.SetNestedField(snap.Object, true, "status", "readyToUse")
	_ = fakeClient.Update(ctx, &snap)

	// Step 6: Snapshotting -> Unquiescing
	_, err = reconciler.Reconcile(ctx, req)
	if err != nil {
		t.Fatalf("reconcile snapshotting failed: %v", err)
	}
	_ = fakeClient.Get(ctx, req.NamespacedName, migration)
	if migration.Status.Phase != v1alpha1.PhaseUnquiescing {
		t.Fatalf("expected Unquiescing, got %s", migration.Status.Phase)
	}

	// Step 7: Unquiescing -> ApplyingTarget (PostQuiesce unlock executed immediately!)
	_, err = reconciler.Reconcile(ctx, req)
	if err != nil {
		t.Fatalf("reconcile unquiescing failed: %v", err)
	}
	if !mockHooks.PostQuiesceCalled {
		t.Fatalf("expected PostQuiesce unlock to have been executed immediately")
	}
	_ = fakeClient.Get(ctx, req.NamespacedName, migration)
	if migration.Status.Phase != v1alpha1.PhaseApplyingTarget {
		t.Fatalf("expected ApplyingTarget, got %s", migration.Status.Phase)
	}

	// Step 8: ApplyingTarget -> Cutover (Target PVC & translated manifests deployed)
	_, err = reconciler.Reconcile(ctx, req)
	if err != nil {
		t.Fatalf("reconcile applying target failed: %v", err)
	}
	_ = fakeClient.Get(ctx, req.NamespacedName, migration)
	if migration.Status.Phase != v1alpha1.PhaseCutover {
		t.Fatalf("expected Cutover, got %s", migration.Status.Phase)
	}

	// Verify Target PVC exists and has snapshot dataSource and target storage class
	var targetPVC corev1.PersistentVolumeClaim
	if err := fakeClient.Get(ctx, types.NamespacedName{Namespace: tgtNamespace, Name: pvcName}, &targetPVC); err != nil {
		t.Fatalf("target PVC not created: %v", err)
	}
	if targetPVC.Spec.DataSource == nil || targetPVC.Spec.DataSource.Name != snapName {
		t.Fatalf("expected target PVC to reference snapshot %s", snapName)
	}
	if *targetPVC.Spec.StorageClassName != "fast-nvme-sc" {
		t.Fatalf("expected target storage class fast-nvme-sc, got %v", *targetPVC.Spec.StorageClassName)
	}

	// Verify Target Translated StatefulSet exists in target namespace
	var targetWorkload unstructured.Unstructured
	targetWorkload.SetGroupVersionKind(schema.GroupVersionKind{Group: "apps", Version: "v1", Kind: "StatefulSet"})
	if err := fakeClient.Get(ctx, types.NamespacedName{Namespace: tgtNamespace, Name: workloadName}, &targetWorkload); err != nil {
		t.Fatalf("target StatefulSet was not created: %v", err)
	}

	// Step 9: Cutover -> Completed (Ghostwire cutover executed, traffic drained)
	_, err = reconciler.Reconcile(ctx, req)
	if err != nil {
		t.Fatalf("reconcile cutover failed: %v", err)
	}
	if !mockCutover.CutoverExecuted {
		t.Fatalf("expected Ghostwire traffic cutover to have been executed")
	}
	if mockCutover.LastRouteID != "route-pg-prod" {
		t.Fatalf("expected route ID route-pg-prod, got %s", mockCutover.LastRouteID)
	}

	_ = fakeClient.Get(ctx, req.NamespacedName, migration)
	if migration.Status.Phase != v1alpha1.PhaseCompleted {
		t.Fatalf("expected Completed, got %s", migration.Status.Phase)
	}
	if migration.Status.CompletionTime == nil {
		t.Fatalf("expected completionTime to be recorded")
	}
	if !migration.Status.CutoverCompleted {
		t.Fatalf("expected status.cutoverCompleted to be true")
	}
}

func TestPortageMigrationRollbackOnFailure(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	_ = v1alpha1.AddToScheme(scheme)

	srcNamespace := "source-prod"
	tgtNamespace := "target-prod"

	migration := &v1alpha1.PortageMigration{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "migrate-fail-rollback",
			Namespace: srcNamespace,
		},
		Spec: v1alpha1.PortageMigrationSpec{
			SourceWorkload:  corev1.TypedLocalObjectReference{Kind: "StatefulSet", Name: "nonexistent"},
			SourceNamespace: srcNamespace,
			TargetNamespace: tgtNamespace,
			PostQuiesceHooks: []v1alpha1.QuiescenceHook{
				{
					Name: "emergency_unlock",
					Type: v1alpha1.HookTypePodExec,
					PodExec: &v1alpha1.PodExecHook{
						Command: []string{"psql", "-c", "SELECT pg_wal_replay_resume();"},
					},
				},
			},
			VolumeMigration: v1alpha1.VolumeMigrationSpec{
				VolumeSnapshotClassName:    "csi-aws-vsc",
				PersistentVolumeClaimNames: []string{"missing-pvc"},
			},
		},
		Status: v1alpha1.PortageMigrationStatus{
			Phase: v1alpha1.PhaseValidating,
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(migration).
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
	req := ctrl.Request{NamespacedName: types.NamespacedName{Namespace: srcNamespace, Name: migration.Name}}

	// Reconcile Validating when PVC is missing -> must fail
	_, err := reconciler.Reconcile(ctx, req)
	if err == nil {
		t.Fatalf("expected reconcile to return error on missing PVC")
	}

	_ = fakeClient.Get(ctx, req.NamespacedName, migration)
	if migration.Status.Phase != v1alpha1.PhaseFailed {
		t.Fatalf("expected PhaseFailed, got %s", migration.Status.Phase)
	}
}

func TestPortageMigrationDriverRegistryFallback(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	_ = appsv1.AddToScheme(scheme)
	_ = networkingv1.AddToScheme(scheme)
	_ = v1alpha1.AddToScheme(scheme)

	migration := &v1alpha1.PortageMigration{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-open-core-migration",
			Namespace: "default",
		},
		Spec: v1alpha1.PortageMigrationSpec{
			CDCReplication: &v1alpha1.CDCReplicationSpec{
				Driver:   "community-cdc",
				Enabled:  true,
				Endpoint: "http://localhost:8080",
				StreamID: "stream-01",
			},
			TrafficCutover: &v1alpha1.TrafficCutoverSpec{
				Driver:           "community-cutover",
				Enabled:          true,
				DirectorEndpoint: "http://localhost:9090",
				RouteID:          "route-01",
				TargetEndpoint:   "target:5432",
			},
		},
		Status: v1alpha1.PortageMigrationStatus{
			Phase: v1alpha1.PhaseReplicatingData,
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(migration).
		WithStatusSubresource(migration).
		Build()

	// Intentionally leave CDCClient and CutoverClient nil to test registry resolution
	reconciler := &PortageMigrationReconciler{
		Client:       fakeClient,
		Scheme:       scheme,
		TargetClient: fakeClient,
	}

	ctx := context.Background()
	req := ctrl.Request{NamespacedName: types.NamespacedName{Namespace: "default", Name: migration.Name}}

	// 1. PhaseReplicatingData -> resolves community-cdc
	_, err := reconciler.Reconcile(ctx, req)
	if err != nil {
		t.Fatalf("reconcile ReplicatingData failed: %v", err)
	}
	_ = fakeClient.Get(ctx, req.NamespacedName, migration)
	if migration.Status.Phase != v1alpha1.PhaseQuiescing {
		t.Fatalf("expected PhaseQuiescing, got %s", migration.Status.Phase)
	}

	// 2. Fast-forward to PhaseCutover -> resolves community-cutover
	migration.Status.Phase = v1alpha1.PhaseCutover
	_ = fakeClient.Status().Update(ctx, migration)

	_, err = reconciler.Reconcile(ctx, req)
	if err != nil {
		t.Fatalf("reconcile Cutover failed: %v", err)
	}
	_ = fakeClient.Get(ctx, req.NamespacedName, migration)
	if migration.Status.Phase != v1alpha1.PhaseCompleted {
		t.Fatalf("expected PhaseCompleted, got %s", migration.Status.Phase)
	}
	if !migration.Status.CutoverCompleted {
		t.Fatalf("expected CutoverCompleted=true")
	}
}
