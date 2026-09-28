package controllers

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	v1alpha1 "github.com/aegis-migrate/portage/api/v1alpha1"
	"github.com/aegis-migrate/portage/pkg/ast"
	"github.com/aegis-migrate/portage/pkg/cdc"
	"github.com/aegis-migrate/portage/pkg/cutover"
	"github.com/aegis-migrate/portage/pkg/quiesce"
)

// PortageMigrationReconciler reconciles a PortageMigration object across all lifecycle phases
type PortageMigrationReconciler struct {
	client.Client
	Scheme        *runtime.Scheme
	HookExecutor  quiesce.HookExecutor
	ASTEngine     *ast.ASTTranslationEngine
	TargetClient  client.Client
	CDCClient     cdc.CDCClient
	CutoverClient cutover.CutoverClient
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

	targetCli := r.TargetClient
	if targetCli == nil {
		targetCli = r.Client
	}

	switch migration.Status.Phase {
	case "", v1alpha1.PhasePending:
		logger.Info("Starting Portage migration lifecycle", "phase", v1alpha1.PhasePending)
		now := metav1.Now()
		migration.Status.StartTime = &now
		migration.Status.Phase = v1alpha1.PhaseValidating
		r.setCondition(&migration, "Validating", metav1.ConditionTrue, "PrerequisitesVerification", "Validating source workloads and PVCs")
		return ctrl.Result{Requeue: true}, r.Status().Update(ctx, &migration)

	case v1alpha1.PhaseValidating:
		logger.Info("Validating source workloads, storage, and companion services")
		// 1. Verify Source PVCs
		for _, pvcName := range migration.Spec.VolumeMigration.PersistentVolumeClaimNames {
			var pvc corev1.PersistentVolumeClaim
			if err := r.Get(ctx, client.ObjectKey{Namespace: migration.Spec.SourceNamespace, Name: pvcName}, &pvc); err != nil {
				return r.failMigration(ctx, &migration, fmt.Sprintf("Source PVC %s not found: %v", pvcName, err))
			}
		}

		// 2. Verify Source Workload
		workloadGVK := schema.GroupVersionKind{
			Group:   "apps",
			Version: "v1",
			Kind:    migration.Spec.SourceWorkload.Kind,
		}
		if workloadGVK.Kind == "" {
			workloadGVK.Kind = "StatefulSet"
		}
		var workloadObj unstructured.Unstructured
		workloadObj.SetGroupVersionKind(workloadGVK)
		if err := r.Get(ctx, client.ObjectKey{Namespace: migration.Spec.SourceNamespace, Name: migration.Spec.SourceWorkload.Name}, &workloadObj); err != nil {
			// Also probe older apps/v1beta1 if apps/v1 failed
			workloadGVK.Version = "v1beta1"
			workloadObj.SetGroupVersionKind(workloadGVK)
			if errLegacy := r.Get(ctx, client.ObjectKey{Namespace: migration.Spec.SourceNamespace, Name: migration.Spec.SourceWorkload.Name}, &workloadObj); errLegacy != nil {
				return r.failMigration(ctx, &migration, fmt.Sprintf("Source workload %s/%s not found: %v", workloadGVK.Kind, migration.Spec.SourceWorkload.Name, err))
			}
		}

		migration.Status.Phase = v1alpha1.PhaseTranslating
		r.setCondition(&migration, "Translating", metav1.ConditionTrue, "ASTTranslationStarted", "Commencing manifest AST morphing")
		return ctrl.Result{Requeue: true}, r.Status().Update(ctx, &migration)

	case v1alpha1.PhaseTranslating:
		logger.Info("Executing AST translation on source manifests")
		var translatedNames []string

		// 1. Translate Source Workload (StatefulSet)
		var srcWorkload unstructured.Unstructured
		srcWorkload.SetGroupVersionKind(schema.GroupVersionKind{Group: "apps", Version: "v1", Kind: migration.Spec.SourceWorkload.Kind})
		if err := r.Get(ctx, client.ObjectKey{Namespace: migration.Spec.SourceNamespace, Name: migration.Spec.SourceWorkload.Name}, &srcWorkload); err != nil {
			srcWorkload.SetGroupVersionKind(schema.GroupVersionKind{Group: "apps", Version: "v1beta1", Kind: migration.Spec.SourceWorkload.Kind})
			if errLegacy := r.Get(ctx, client.ObjectKey{Namespace: migration.Spec.SourceNamespace, Name: migration.Spec.SourceWorkload.Name}, &srcWorkload); errLegacy != nil {
				return r.failMigration(ctx, &migration, fmt.Sprintf("failed to get workload for translation: %v", errLegacy))
			}
		}

		opts := ast.TranslationOptions{
			TargetNamespace:        migration.Spec.TargetNamespace,
			TargetStorageClassName: migration.Spec.VolumeMigration.TargetStorageClassName,
		}

		translatedWorkload, err := r.ASTEngine.TranslateResource(ctx, &srcWorkload, opts)
		if err != nil {
			return r.failMigration(ctx, &migration, fmt.Sprintf("AST translation failed for workload: %v", err))
		}
		translatedNames = append(translatedNames, fmt.Sprintf("%s/%s", translatedWorkload.GetKind(), translatedWorkload.GetName()))

		// 2. Find and translate associated Ingresses in source namespace
		var ingressList unstructured.UnstructuredList
		ingressList.SetGroupVersionKind(schema.GroupVersionKind{Group: "networking.k8s.io", Version: "v1", Kind: "IngressList"})
		if err := r.List(ctx, &ingressList, client.InNamespace(migration.Spec.SourceNamespace)); err != nil {
			ingressList.SetGroupVersionKind(schema.GroupVersionKind{Group: "extensions", Version: "v1beta1", Kind: "IngressList"})
			_ = r.List(ctx, &ingressList, client.InNamespace(migration.Spec.SourceNamespace))
		}

		for _, item := range ingressList.Items {
			translatedIngress, err := r.ASTEngine.TranslateResource(ctx, &item, opts)
			if err == nil {
				translatedNames = append(translatedNames, fmt.Sprintf("Ingress/%s", translatedIngress.GetName()))
			}
		}

		migration.Status.TranslatedObjects = translatedNames
		migration.Status.Phase = v1alpha1.PhaseReplicatingData
		r.setCondition(&migration, "ReplicatingData", metav1.ConditionTrue, "DataReplication", "Starting or verifying data stream sync")
		return ctrl.Result{Requeue: true}, r.Status().Update(ctx, &migration)

	case v1alpha1.PhaseReplicatingData:
		logger.Info("Checking data plane stream replication & consistency")
		if migration.Spec.CDCReplication != nil && migration.Spec.CDCReplication.Enabled {
			cdcSpec := migration.Spec.CDCReplication
			if r.CDCClient != nil {
				// Start replication if needed
				_ = r.CDCClient.StartReplication(ctx, cdcSpec.Endpoint, cdcSpec.StreamID)

				// Verify Merkle tree consistency if required
				if cdcSpec.RequireMerkleVerification {
					verified, rootHash, err := r.CDCClient.VerifyState(ctx, cdcSpec.Endpoint, cdcSpec.StreamID)
					if err != nil || !verified {
						logger.Info("Merkle state verification in progress or divergent, retrying", "err", err)
						return ctrl.Result{RequeueAfter: 3 * time.Second}, nil
					}
					migration.Status.MerkleVerified = true
					logger.Info("Merkle state verification verified identical", "rootHash", rootHash)
				}

				// Check lag
				lag, err := r.CDCClient.GetLag(ctx, cdcSpec.Endpoint, cdcSpec.StreamID)
				if err == nil {
					migration.Status.ReplicationLagSeconds = lag
					if cdcSpec.MaxAllowedLagSeconds > 0 && lag > cdcSpec.MaxAllowedLagSeconds {
						logger.Info("Replication lag above threshold, waiting for catchup", "currentLag", lag, "maxAllowed", cdcSpec.MaxAllowedLagSeconds)
						return ctrl.Result{RequeueAfter: 2 * time.Second}, r.Status().Update(ctx, &migration)
					}
				}
			}
		}

		migration.Status.Phase = v1alpha1.PhaseQuiescing
		r.setCondition(&migration, "Quiescing", metav1.ConditionTrue, "QuiesceLocking", "Acquiring database locks and flushing tables")
		return ctrl.Result{Requeue: true}, r.Status().Update(ctx, &migration)

	case v1alpha1.PhaseQuiescing:
		logger.Info("Executing pre-quiescence locks (database flushes)")
		if r.HookExecutor != nil && len(migration.Spec.PreQuiesceHooks) > 0 {
			if err := r.HookExecutor.ExecutePreQuiesce(ctx, &migration); err != nil {
				return r.failMigration(ctx, &migration, fmt.Sprintf("Pre-quiesce hook failure: %v", err))
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
				if r.HookExecutor != nil {
					_ = r.HookExecutor.ExecutePostQuiesce(ctx, &migration)
				}
				return r.failMigration(ctx, &migration, fmt.Sprintf("failed creating snapshot for %s: %v", pvcName, err))
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
			if err := r.Get(ctx, client.ObjectKey{Namespace: migration.Spec.SourceNamespace, Name: snapName}, &snap); err != nil {
				return ctrl.Result{RequeueAfter: 2 * time.Second}, err
			}

			readyToUse, found, _ := unstructured.NestedBool(snap.Object, "status", "readyToUse")
			if !found || !readyToUse {
				allReady = false
				break
			}
		}

		if !allReady {
			return ctrl.Result{RequeueAfter: 2 * time.Second}, nil
		}

		// Advance immediately to Unquiescing to release locks with minimum window!
		migration.Status.Phase = v1alpha1.PhaseUnquiescing
		return ctrl.Result{Requeue: true}, r.Status().Update(ctx, &migration)

	case v1alpha1.PhaseUnquiescing:
		logger.Info("Snapshots ready: immediately releasing database locks (Unquiescing)")
		if r.HookExecutor != nil && len(migration.Spec.PostQuiesceHooks) > 0 {
			if err := r.HookExecutor.ExecutePostQuiesce(ctx, &migration); err != nil {
				logger.Error(err, "Post-quiesce unlock warning")
			}
		}

		migration.Status.Phase = v1alpha1.PhaseApplyingTarget
		r.setCondition(&migration, "ApplyingTarget", metav1.ConditionTrue, "ApplyingWorkload", "Deploying target storage and translated manifests")
		return ctrl.Result{Requeue: true}, r.Status().Update(ctx, &migration)

	case v1alpha1.PhaseApplyingTarget:
		logger.Info("Applying storage and translated workload to target namespace")
		// 1. Provision target PVCs from snapshots
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
				return r.triggerRollback(ctx, &migration, fmt.Sprintf("Failed creating target PVC %s: %v", pvcName, err))
			}
		}

		// 2. Fetch and apply translated workload to target namespace
		var srcWorkload unstructured.Unstructured
		srcWorkload.SetGroupVersionKind(schema.GroupVersionKind{Group: "apps", Version: "v1", Kind: migration.Spec.SourceWorkload.Kind})
		if err := r.Get(ctx, client.ObjectKey{Namespace: migration.Spec.SourceNamespace, Name: migration.Spec.SourceWorkload.Name}, &srcWorkload); err != nil {
			srcWorkload.SetGroupVersionKind(schema.GroupVersionKind{Group: "apps", Version: "v1beta1", Kind: migration.Spec.SourceWorkload.Kind})
			_ = r.Get(ctx, client.ObjectKey{Namespace: migration.Spec.SourceNamespace, Name: migration.Spec.SourceWorkload.Name}, &srcWorkload)
		}

		opts := ast.TranslationOptions{
			TargetNamespace:        migration.Spec.TargetNamespace,
			TargetStorageClassName: migration.Spec.VolumeMigration.TargetStorageClassName,
		}
		translatedWorkload, err := r.ASTEngine.TranslateResource(ctx, &srcWorkload, opts)
		if err == nil {
			if err := targetCli.Create(ctx, translatedWorkload); err != nil && !errors.IsAlreadyExists(err) {
				logger.Error(err, "Warning applying target workload")
			}
		}

		// 3. Translate and apply Ingresses
		var srcIngressList unstructured.UnstructuredList
		srcIngressList.SetGroupVersionKind(schema.GroupVersionKind{Group: "networking.k8s.io", Version: "v1", Kind: "IngressList"})
		if err := r.List(ctx, &srcIngressList, client.InNamespace(migration.Spec.SourceNamespace)); err != nil {
			srcIngressList.SetGroupVersionKind(schema.GroupVersionKind{Group: "extensions", Version: "v1beta1", Kind: "IngressList"})
			_ = r.List(ctx, &srcIngressList, client.InNamespace(migration.Spec.SourceNamespace))
		}

		for _, item := range srcIngressList.Items {
			translatedIngress, err := r.ASTEngine.TranslateResource(ctx, &item, opts)
			if err == nil {
				_ = targetCli.Create(ctx, translatedIngress)
			}
		}

		migration.Status.TargetWorkloadReady = true
		migration.Status.Phase = v1alpha1.PhaseCutover
		r.setCondition(&migration, "Cutover", metav1.ConditionTrue, "TrafficCutover", "Executing Ghostwire zero-drop traffic redirection")
		return ctrl.Result{Requeue: true}, r.Status().Update(ctx, &migration)

	case v1alpha1.PhaseCutover:
		logger.Info("Executing zero-drop traffic cutover via Ghostwire")
		if migration.Spec.TrafficCutover != nil && migration.Spec.TrafficCutover.Enabled {
			cutSpec := migration.Spec.TrafficCutover
			if r.CutoverClient != nil {
				timeoutSec := cutSpec.DrainTimeoutSeconds
				if timeoutSec <= 0 {
					timeoutSec = 30
				}
				if err := r.CutoverClient.ExecuteCutover(ctx, cutSpec.DirectorEndpoint, cutSpec.RouteID, cutSpec.TargetEndpoint, timeoutSec); err != nil {
					return r.triggerRollback(ctx, &migration, fmt.Sprintf("Ghostwire traffic cutover failed: %v", err))
				}
				migration.Status.CutoverCompleted = true
				logger.Info("Ghostwire cutover and connection drain succeeded!")
			}
		}

		now := metav1.Now()
		migration.Status.CompletionTime = &now
		migration.Status.Phase = v1alpha1.PhaseCompleted
		r.setCondition(&migration, "Ready", metav1.ConditionTrue, "MigrationSucceeded", "Complete zero-downtime stateful migration finished")
		return ctrl.Result{}, r.Status().Update(ctx, &migration)

	case v1alpha1.PhaseRollback:
		logger.Info("Executing rollback sequence to restore source consistency")
		if r.HookExecutor != nil {
			_ = r.HookExecutor.ExecutePostQuiesce(ctx, &migration)
		}
		migration.Status.Phase = v1alpha1.PhaseFailed
		r.setCondition(&migration, "Failed", metav1.ConditionTrue, "RollbackExecuted", migration.Status.ErrorMessage)
		return ctrl.Result{}, r.Status().Update(ctx, &migration)

	case v1alpha1.PhaseCompleted, v1alpha1.PhaseFailed:
		return ctrl.Result{}, nil
	}

	return ctrl.Result{}, nil
}

func (r *PortageMigrationReconciler) failMigration(ctx context.Context, m *v1alpha1.PortageMigration, msg string) (ctrl.Result, error) {
	m.Status.Phase = v1alpha1.PhaseFailed
	m.Status.ErrorMessage = msg
	r.setCondition(m, "Failed", metav1.ConditionTrue, "MigrationFailed", msg)
	_ = r.Status().Update(ctx, m)
	return ctrl.Result{}, fmt.Errorf(msg)
}

func (r *PortageMigrationReconciler) triggerRollback(ctx context.Context, m *v1alpha1.PortageMigration, msg string) (ctrl.Result, error) {
	m.Status.Phase = v1alpha1.PhaseRollback
	m.Status.ErrorMessage = msg
	r.setCondition(m, "Rollback", metav1.ConditionTrue, "RollbackInitiated", msg)
	_ = r.Status().Update(ctx, m)
	return ctrl.Result{Requeue: true}, nil
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
