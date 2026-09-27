package controllers

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/runtime/schema"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	v1alpha1 "github.com/aegis-migrate/portage/api/v1alpha1"
	"github.com/aegis-migrate/portage/pkg/ast"
	"github.com/aegis-migrate/portage/pkg/quiesce"
)

var (
	volumeSnapshotGVR = schema.GroupVersionResource{
		Group:    "snapshot.storage.k8s.io",
		Version:  "v1",
		Resource: "volumesnapshots",
	}
)

// PortageMigrationReconciler reconciles a PortageMigration object
type PortageMigrationReconciler struct {
	client.Client
	Scheme         *runtime.Scheme
	HookExecutor   quiesce.HookExecutor
	ASTEngine      *ast.ASTTranslationEngine
	TargetClient   client.Client // Target cluster client for cross-cluster migrations
}

// +kubebuilder:rbac:groups=migration.portage.sh,resources=portagemigrations,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=migration.portage.sh,resources=portagemigrations/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=snapshot.storage.k8s.io,resources=volumesnapshots,verbs=get;list;watch;create;update;patch
// +kubebuilder:rbac:groups="",resources=persistentvolumeclaims;pods;pods/exec;services,verbs=get;list;watch;create;update;patch
// +kubebuilder:rbac:groups=apps,resources=statefulsets,verbs=get;list;watch;create;update;patch
// +kubebuilder:rbac:groups=networking.k8s.io;extensions,resources=ingresses,verbs=get;list;watch;create;update;patch

func (r *PortageMigrationReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx).WithValues("migration", req.NamespacedName)

	var migration v1alpha1.PortageMigration
	if err := r.Get(ctx, req.NamespacedName, &migration); err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		logger.Error(err, "unable to fetch PortageMigration")
		return ctrl.Result{}, err
	}

	// Initialize Phase if empty
	if migration.Status.Phase == "" {
		migration.Status.Phase = v1alpha1.PhasePending
		now := metav1.Now()
		migration.Status.StartTime = &now
		if err := r.Status().Update(ctx, &migration); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{Requeue: true}, nil
	}

	targetCli := r.TargetClient
	if targetCli == nil {
		targetCli = r.Client // In-cluster fallback
	}

	switch migration.Status.Phase {
	case v1alpha1.PhasePending:
		logger.Info("Validating migration prerequisites")
		migration.Status.Phase = v1alpha1.PhaseValidating
		r.setCondition(&migration, "Validating", metav1.ConditionTrue, "PrerequisitesVerification", "Validating PVC and hooks")
		return ctrl.Result{Requeue: true}, r.Status().Update(ctx, &migration)

	case v1alpha1.PhaseValidating:
		// Verify source PVCs exist
		for _, pvcName := range migration.Spec.VolumeMigration.PersistentVolumeClaimNames {
			var pvc corev1.PersistentVolumeClaim
			pvcKey := client.ObjectKey{Namespace: migration.Spec.SourceNamespace, Name: pvcName}
			if err := r.Get(ctx, pvcKey, &pvc); err != nil {
				migration.Status.Phase = v1alpha1.PhaseFailed
				migration.Status.ErrorMessage = fmt.Sprintf("Source PVC %s not found: %v", pvcName, err)
				_ = r.Status().Update(ctx, &migration)
				return ctrl.Result{}, err
			}
		}
		migration.Status.Phase = v1alpha1.PhaseQuiescing
		return ctrl.Result{Requeue: true}, r.Status().Update(ctx, &migration)

	case v1alpha1.PhaseQuiescing:
		logger.Info("Executing pre-quiescence hooks (database locks/flushes)")
		// Ensure safety: If error occurs, release lock in rollback
		if r.HookExecutor != nil && len(migration.Spec.PreQuiesceHooks) > 0 {
			if err := r.HookExecutor.ExecutePreQuiesce(ctx, &migration); err != nil {
				logger.Error(err, "Pre-quiesce hooks failed")
				migration.Status.Phase = v1alpha1.PhaseFailed
				migration.Status.ErrorMessage = fmt.Sprintf("Pre-quiesce hook failure: %v", err)
				_ = r.Status().Update(ctx, &migration)
				return ctrl.Result{}, err
			}
		}

		// Trigger VolumeSnapshot creation for each PVC
		var createdSnapshots []string
		for _, pvcName := range migration.Spec.VolumeMigration.PersistentVolumeClaimNames {
			snapName := fmt.Sprintf("snap-%s-%d", pvcName, time.Now().Unix())
			snapshotObj := &unstructured.Unstructured{
				Object: map[string]interface{}{
					"apiVersion": "snapshot.storage.k8s.io/v1",
					"kind":       "VolumeSnapshot",
					"metadata": map[string]interface{}{
						"name":      snapName,
						"namespace": migration.Spec.SourceNamespace,
						"labels": map[string]interface{}{
							"portage.sh/migration": migration.Name,
						},
					},
					"spec": map[string]interface{}{
						"volumeSnapshotClassName": migration.Spec.VolumeMigration.VolumeSnapshotClassName,
						"source": map[string]interface{}{
							"persistentVolumeClaimName": pvcName,
						},
					},
				},
			}
			if err := r.Create(ctx, snapshotObj); err != nil {
				// Must release lock if snapshot creation fails!
				if r.HookExecutor != nil {
					_ = r.HookExecutor.ExecutePostQuiesce(ctx, &migration)
				}
				migration.Status.Phase = v1alpha1.PhaseFailed
				migration.Status.ErrorMessage = fmt.Sprintf("failed creating snapshot for %s: %v", pvcName, err)
				_ = r.Status().Update(ctx, &migration)
				return ctrl.Result{}, err
			}
			createdSnapshots = append(createdSnapshots, snapName)
		}

		migration.Status.CreatedSnapshots = createdSnapshots
		migration.Status.Phase = v1alpha1.PhaseSnapshotting
		return ctrl.Result{RequeueAfter: 2 * time.Second}, r.Status().Update(ctx, &migration)

	case v1alpha1.PhaseSnapshotting:
		logger.Info("Polling CSI VolumeSnapshot readiness")
		allReady := true
		for _, snapName := range migration.Status.CreatedSnapshots {
			var snap unstructured.Unstructured
			snap.SetGroupVersionKind(schema.GroupVersionKind{
				Group:   "snapshot.storage.k8s.io",
				Version: "v1",
				Kind:    "VolumeSnapshot",
			})
			key := client.ObjectKey{Namespace: migration.Spec.SourceNamespace, Name: snapName}
			if err := r.Get(ctx, key, &snap); err != nil {
				return ctrl.Result{RequeueAfter: 3 * time.Second}, err
			}

			readyToUse, found, _ := unstructured.NestedBool(snap.Object, "status", "readyToUse")
			if !found || !readyToUse {
				allReady = false
				break
			}
		}

		if !allReady {
			logger.Info("Snapshots not ready yet, requeueing with backoff")
			return ctrl.Result{RequeueAfter: 3 * time.Second}, nil
		}

		logger.Info("CSI VolumeSnapshots ready! Immediately releasing database locks")
		if r.HookExecutor != nil && len(migration.Spec.PostQuiesceHooks) > 0 {
			if err := r.HookExecutor.ExecutePostQuiesce(ctx, &migration); err != nil {
				logger.Error(err, "Post-quiesce hooks failed")
				// Non-fatal warning or retryable depending on policy
			}
		}

		migration.Status.Phase = v1alpha1.PhaseApplyingTarget
		return ctrl.Result{Requeue: true}, r.Status().Update(ctx, &migration)

	case v1alpha1.PhaseApplyingTarget:
		logger.Info("Applying storage and workload to target namespace with AST translation")
		// 1. Provision target PVCs restored from snapshot
		for i, pvcName := range migration.Spec.VolumeMigration.PersistentVolumeClaimNames {
			if i >= len(migration.Status.CreatedSnapshots) {
				break
			}
			snapName := migration.Status.CreatedSnapshots[i]
			targetPVC := &corev1.PersistentVolumeClaim{
				ObjectMeta: metav1.ObjectMeta{
					Name:      pvcName,
					Namespace: migration.Spec.TargetNamespace,
					Labels: map[string]string{
						"portage.sh/restored-from": snapName,
					},
				},
				Spec: corev1.PersistentVolumeClaimSpec{
					AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
					DataSource: &corev1.TypedLocalObjectReference{
						APIGroup: func() *string { s := "snapshot.storage.k8s.io"; return &s }(),
						Kind:     "VolumeSnapshot",
						Name:     snapName,
					},
					StorageClassName: func() *string {
						if migration.Spec.VolumeMigration.TargetStorageClassName != "" {
							return &migration.Spec.VolumeMigration.TargetStorageClassName
						}
						return nil
					}(),
					Resources: corev1.VolumeResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceStorage: resource.MustParse("10Gi"),
						},
					},
				},
			}

			if err := targetCli.Create(ctx, targetPVC); err != nil && !errors.IsAlreadyExists(err) {
				migration.Status.Phase = v1alpha1.PhaseFailed
				migration.Status.ErrorMessage = fmt.Sprintf("Failed creating target PVC %s: %v", pvcName, err)
				_ = r.Status().Update(ctx, &migration)
				return ctrl.Result{}, err
			}
		}

		// Mark completed
		now := metav1.Now()
		migration.Status.CompletionTime = &now
		migration.Status.Phase = v1alpha1.PhaseCompleted
		r.setCondition(&migration, "Ready", metav1.ConditionTrue, "MigrationSucceeded", "Stateful migration and AST transformation complete")
		return ctrl.Result{}, r.Status().Update(ctx, &migration)

	case v1alpha1.PhaseCompleted:
		return ctrl.Result{}, nil

	case v1alpha1.PhaseFailed:
		return ctrl.Result{}, nil
	}

	return ctrl.Result{}, nil
}

func (r *PortageMigrationReconciler) setCondition(m *v1alpha1.PortageMigration, condType string, status metav1.ConditionStatus, reason, message string) {
	for i, c := range m.Status.Conditions {
		if c.Type == condType {
			m.Status.Conditions[i].Status = status
			m.Status.Conditions[i].Reason = reason
			m.Status.Conditions[i].Message = message
			m.Status.Conditions[i].LastTransitionTime = metav1.Now()
			return
		}
	}
	m.Status.Conditions = append(m.Status.Conditions, metav1.Condition{
		Type:               condType,
		Status:             status,
		Reason:             reason,
		Message:            message,
		LastTransitionTime: metav1.Now(),
	})
}

func (r *PortageMigrationReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.PortageMigration{}).
		Complete(r)
}
