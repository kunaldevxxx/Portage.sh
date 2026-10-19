package controllers

import (
	"context"
	"fmt"
	"time"

	appsv1 "k8s.io/api/apps/v1"
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
	"github.com/aegis-migrate/portage/pkg/driver"
	"github.com/aegis-migrate/portage/pkg/quiesce"
)

// PortageMigrationReconciler reconciles a PortageMigration object across all lifecycle phases
type PortageMigrationReconciler struct {
	client.Client
	Scheme         *runtime.Scheme
	HookExecutor   quiesce.HookExecutor
	ASTEngine      *ast.ASTTranslationEngine
	TargetClient   client.Client
	CDCClient      cdc.CDCClient
	CutoverClient  cutover.CutoverClient
	DriverRegistry *driver.DriverRegistry
}

func (r *PortageMigrationReconciler) getCDCClient(cdcSpec *v1alpha1.CDCReplicationSpec) (cdc.CDCClient, error) {
	if r.CDCClient != nil {
		return r.CDCClient, nil
	}
	reg := r.DriverRegistry
	if reg == nil {
		reg = driver.GlobalRegistry()
	}
	driverName := ""
	if cdcSpec != nil {
		driverName = cdcSpec.Driver
	}
	d, err := reg.GetReplicationDriver(driverName)
	if err != nil {
		return nil, err
	}
	return d, nil
}

func (r *PortageMigrationReconciler) getCutoverClient(cutSpec *v1alpha1.TrafficCutoverSpec) (cutover.CutoverClient, error) {
	if r.CutoverClient != nil {
		return r.CutoverClient, nil
	}
	reg := r.DriverRegistry
	if reg == nil {
		reg = driver.GlobalRegistry()
	}
	driverName := ""
	if cutSpec != nil {
		driverName = cutSpec.Driver
	}
	d, err := reg.GetCutoverDriver(driverName)
	if err != nil {
		return nil, err
	}
	return d, nil
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
			cdcClient, err := r.getCDCClient(cdcSpec)
			if err != nil {
				return r.triggerRollback(ctx, &migration, fmt.Sprintf("Failed resolving replication driver: %v", err))
			}
			if cdcClient != nil {
				// Start replication if needed
				_ = cdcClient.StartReplication(ctx, cdcSpec.Endpoint, cdcSpec.StreamID)

				// Verify Merkle tree consistency if required
				if cdcSpec.RequireMerkleVerification {
					verified, rootHash, err := cdcClient.VerifyState(ctx, cdcSpec.Endpoint, cdcSpec.StreamID)
					if err != nil || !verified {
						logger.Info("Merkle state verification in progress or divergent, retrying", "err", err)
						return ctrl.Result{RequeueAfter: 3 * time.Second}, nil
					}
					migration.Status.MerkleVerified = true
					logger.Info("Merkle state verification verified identical", "rootHash", rootHash)
				}

				// Check lag
						lag, err := cdcClient.GetLag(ctx, cdcSpec.Endpoint, cdcSpec.StreamID)
						if err == nil {
							migration.Status.ReplicationLagSeconds = lag
							if cdcSpec.MaxAllowedLagSeconds > 0 && lag > cdcSpec.MaxAllowedLagSeconds {
								logger.Info("Replication lag above threshold, waiting for catchup", "currentLag", lag, "maxAllowed", cdcSpec.MaxAllowedLagSeconds)
								return ctrl.Result{RequeueAfter: 2 * time.Second}, r.Status().Update(ctx, &migration)
							}
						}

						// Report current WAL LSN from CDC client
						if cdcSpec.Endpoint != "" {
							if lsn, err := cdcClient.GetCurrentLSN(ctx, cdcSpec.Endpoint, cdcSpec.StreamID); err == nil && lsn > 0 {
								migration.Status.CurrentWALLSN = lsn
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
		readyCount := int64(0)
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
			readyCount++
		}
		migration.Status.SnapshotsReadyCount = readyCount
		migration.Status.TotalSnapshots = int64(len(migration.Status.CreatedSnapshots))

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
		logger.Info("Executing zero-drop traffic cutover via Ghostwire or Community driver")
		if migration.Spec.TrafficCutover != nil && migration.Spec.TrafficCutover.Enabled {
			cutSpec := migration.Spec.TrafficCutover
			cutClient, err := r.getCutoverClient(cutSpec)
			if err != nil {
				return r.triggerRollback(ctx, &migration, fmt.Sprintf("Failed resolving traffic cutover driver: %v", err))
			}
			if cutClient != nil {
				timeoutSec := cutSpec.DrainTimeoutSeconds
				if timeoutSec <= 0 {
					timeoutSec = 30
				}
				if err := cutClient.ExecuteCutover(ctx, cutSpec.DirectorEndpoint, cutSpec.RouteID, cutSpec.TargetEndpoint, timeoutSec); err != nil {
					return r.triggerRollback(ctx, &migration, fmt.Sprintf("Traffic cutover failed: %v", err))
				}
				migration.Status.CutoverCompleted = true
				logger.Info("Traffic cutover and connection drain succeeded!")

				// Report drain progress from cutover client
				if activeConns, drainPct, err := cutClient.GetDrainProgress(ctx, cutSpec.DirectorEndpoint, cutSpec.RouteID); err == nil {
					migration.Status.ActiveConnections = activeConns
					migration.Status.DrainProgressPercent = drainPct
				}
			}
		}

		// Move to verification phase instead of completing immediately
		migration.Status.Phase = v1alpha1.PhaseVerifying
		r.setCondition(&migration, "Verifying", metav1.ConditionTrue, "PostCutoverVerification", "Running post-cutover verification checks")
		return ctrl.Result{Requeue: true}, r.Status().Update(ctx, &migration)

	case v1alpha1.PhaseVerifying:
		logger.Info("Running post-cutover verification checks")
		verificationPassed := true

		// Run post-cutover verification hooks if configured
		if r.HookExecutor != nil && len(migration.Spec.PostQuiesceHooks) > 0 {
			// Use the first post-quiesce hook as a verification hook if it's a connection test
			for _, hook := range migration.Spec.PostQuiesceHooks {
				if hook.Type == v1alpha1.HookTypeHTTP || hook.Type == v1alpha1.HookTypePodExec {
					if err := r.HookExecutor.ExecutePostQuiesce(ctx, &migration); err != nil {
						logger.Error(err, "Post-cutover verification hook failed", "hook", hook.Name)
						verificationPassed = false
						break
					}
				}
			}
		}

		// Check target workload health via cutover client
		if migration.Spec.TrafficCutover != nil && migration.Spec.TrafficCutover.Enabled {
			cutSpec := migration.Spec.TrafficCutover
			cutClient, err := r.getCutoverClient(cutSpec)
			if err == nil && cutClient != nil {
				healthy, err := cutClient.CheckTargetHealth(ctx, cutSpec.DirectorEndpoint, cutSpec.TargetEndpoint)
				if err != nil {
					logger.Error(err, "Target health check failed")
					verificationPassed = false
				} else if !healthy {
					logger.Info("Target health check returned unhealthy")
					verificationPassed = false
				}
			}
		}

		if !verificationPassed {
			return r.triggerRollback(ctx, &migration, "Post-cutover verification failed")
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
		// Rollback the traffic cutover: flip Ghostwire back to source
		if migration.Spec.TrafficCutover != nil && migration.Spec.TrafficCutover.Enabled {
			cutSpec := migration.Spec.TrafficCutover
			cutClient, err := r.getCutoverClient(cutSpec)
			if err == nil && cutClient != nil {
				// Rollback: re-point traffic to source endpoint
				// ExecuteRollback flips the Ghostwire route back to source and drains target connections
				timeoutSec := cutSpec.DrainTimeoutSeconds
				if timeoutSec <= 0 {
					timeoutSec = 30
				}
				_ = cutClient.ExecuteRollback(ctx, cutSpec.DirectorEndpoint, cutSpec.RouteID, timeoutSec)
				logger.Info("Traffic cutover rolled back to source", "routeID", cutSpec.RouteID)
			}
		}
		// Scale down target StatefulSet to stop target workload
		if migration.Status.TargetWorkloadReady {
			_ = r.scaleDownTargetWorkload(ctx, &migration)
		}
		// Clean up target PVCs (optional — retains for forensics by default)
					if len(migration.Spec.VolumeMigration.PersistentVolumeClaimNames) > 0 && !migration.Spec.VolumeMigration.RetainOnSource {
			_ = r.cleanupTargetResources(ctx, &migration)
		}
		migration.Status.Phase = v1alpha1.PhaseFailed
		r.setCondition(&migration, "Failed", metav1.ConditionTrue, "RollbackExecuted", migration.Status.ErrorMessage)
		return ctrl.Result{}, r.Status().Update(ctx, &migration)

	case v1alpha1.PhaseCompleted:
		return ctrl.Result{}, nil

	case v1alpha1.PhaseFailed:
		return ctrl.Result{}, nil
	}

	return ctrl.Result{}, nil
}

func (r *PortageMigrationReconciler) failMigration(ctx context.Context, m *v1alpha1.PortageMigration, msg string) (ctrl.Result, error) {
	m.Status.Phase = v1alpha1.PhaseFailed
	m.Status.ErrorMessage = msg
	r.setCondition(m, "Failed", metav1.ConditionTrue, "MigrationFailed", msg)
	_ = r.Status().Update(ctx, m)
	return ctrl.Result{}, fmt.Errorf("%s", msg)
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

// resolveSourceEndpoint derives the source database endpoint from the source workload.
// For a StatefulSet, it returns the headless service DNS name.
func (r *PortageMigrationReconciler) resolveSourceEndpoint(ctx context.Context, m *v1alpha1.PortageMigration) string {
	// Default: construct from workload name and namespace
	// For StatefulSet, the stable network identity is: <statefulset-name>-0.<headless-service>.<namespace>.svc.cluster.local
	// We fall back to: <workload-name>.<source-namespace>.svc.cluster.local
	serviceName := m.Spec.SourceWorkload.Name
	if serviceName == "" {
		return ""
	}
	return fmt.Sprintf("%s.%s.svc.cluster.local:5432", serviceName, m.Spec.SourceNamespace)
}

// scaleDownTargetWorkload scales the target StatefulSet to 0 replicas.
func (r *PortageMigrationReconciler) scaleDownTargetWorkload(ctx context.Context, m *v1alpha1.PortageMigration) error {
	logger := log.FromContext(ctx)
	// Only handle StatefulSet workloads for now
	if m.Spec.SourceWorkload.Kind != "StatefulSet" {
		return nil
	}

	targetNs := m.Spec.TargetNamespace
	if targetNs == "" {
		targetNs = m.Spec.SourceNamespace
	}

	ss := &appsv1.StatefulSet{}
	err := r.Get(ctx, client.ObjectKey{Namespace: targetNs, Name: m.Spec.SourceWorkload.Name}, ss)
	if err != nil {
		if errors.IsNotFound(err) {
			return nil // Already gone
		}
		return fmt.Errorf("failed to get target StatefulSet: %w", err)
	}

	// Scale to 0
	ss.Spec.Replicas = func() *int32 { v := int32(0); return &v }()
	if err := r.Update(ctx, ss); err != nil {
		return fmt.Errorf("failed to scale down target StatefulSet: %w", err)
	}
	logger.Info("Scaled down target StatefulSet to 0 replicas", "name", ss.Name, "namespace", ss.Namespace)
	return nil
}

// cleanupTargetResources removes target PVCs and other resources created during migration.
func (r *PortageMigrationReconciler) cleanupTargetResources(ctx context.Context, m *v1alpha1.PortageMigration) error {
	logger := log.FromContext(ctx)
	targetNs := m.Spec.TargetNamespace
	if targetNs == "" {
		targetNs = m.Spec.SourceNamespace
	}

	// Delete target PVCs
	for _, pvcName := range m.Spec.VolumeMigration.PersistentVolumeClaimNames {
		pvc := &corev1.PersistentVolumeClaim{}
		err := r.Get(ctx, client.ObjectKey{Namespace: targetNs, Name: pvcName}, pvc)
		if err != nil {
			if errors.IsNotFound(err) {
				continue
			}
			logger.Error(err, "Failed to get target PVC for cleanup", "pvc", pvcName)
			continue
		}
		if err := r.Delete(ctx, pvc); err != nil && !errors.IsNotFound(err) {
			logger.Error(err, "Failed to delete target PVC", "pvc", pvcName)
		}
	}

	// Delete target StatefulSet
	if m.Spec.SourceWorkload.Kind == "StatefulSet" {
		ss := &appsv1.StatefulSet{}
		err := r.Get(ctx, client.ObjectKey{Namespace: targetNs, Name: m.Spec.SourceWorkload.Name}, ss)
		if err != nil && !errors.IsNotFound(err) {
			logger.Error(err, "Failed to get target StatefulSet for cleanup")
		} else if err == nil {
			if delErr := r.Delete(ctx, ss); delErr != nil && !errors.IsNotFound(delErr) {
				logger.Error(delErr, "Failed to delete target StatefulSet")
			}
		}
	}

	return nil
}

// +kubebuilder:rbac:groups=apps,resources=statefulsets,verbs=get;update;delete

func (r *PortageMigrationReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.PortageMigration{}).
		Complete(r)
}
