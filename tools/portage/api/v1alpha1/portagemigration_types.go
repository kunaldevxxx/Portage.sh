package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Phase represents the current state in the migration lifecycle.
type Phase string

const (
	PhasePending           Phase = "Pending"
	PhaseValidating        Phase = "Validating"
	PhaseQuiescing         Phase = "Quiescing"
	PhaseSnapshotting      Phase = "Snapshotting"
	PhaseTranslating       Phase = "Translating"
	PhaseApplyingTarget    Phase = "ApplyingTarget"
	PhaseReplicatingData   Phase = "ReplicatingData"
	PhaseUnquiescing       Phase = "Unquiescing"
	PhaseCutover           Phase = "Cutover"
	PhaseCompleted         Phase = "Completed"
	PhaseFailed            Phase = "Failed"
	PhaseRollback          Phase = "Rollback"
)

// HookType defines how a quiescence hook is executed.
type HookType string

const (
	HookTypePodExec HookType = "PodExec"
	HookTypeHTTP    HookType = "HTTP"
	HookTypeGRPC    HookType = "gRPC"
)

// QuiescenceHook defines pre- and post-snapshot hooks for ensuring database flush/lock.
type QuiescenceHook struct {
	// Name of the hook
	Name string `json:"name"`
	// Type of execution
	Type HookType `json:"type"`
	// PodExec specifies command to run inside a container
	// +optional
	PodExec *PodExecHook `json:"podExec,omitempty"`
	// HTTP specifies an HTTP endpoint to hit
	// +optional
	HTTP *HTTPHook `json:"http,omitempty"`
	// TimeoutSeconds before failing the hook
	// +kubebuilder:default=30
	TimeoutSeconds int32 `json:"timeoutSeconds,omitempty"`
	// MaxRetries on transient errors
	// +kubebuilder:default=5
	MaxRetries int32 `json:"maxRetries,omitempty"`
}

type PodExecHook struct {
	// Selector for target pods
	Selector metav1.LabelSelector `json:"selector"`
	// Container name (optional, defaults to first container)
	Container string `json:"container,omitempty"`
	// Command to execute (e.g. ["mysql", "-e", "FLUSH TABLES WITH READ LOCK;"])
	Command []string `json:"command"`
}

type HTTPHook struct {
	URL     string            `json:"url"`
	Method  string            `json:"method"`
	Headers map[string]string `json:"headers,omitempty"`
}

// APITranslationRule defines AST transformations for deprecated Kubernetes APIs.
type APITranslationRule struct {
	// SourceAPIVersion to match (e.g. "extensions/v1beta1", "networking.k8s.io/v1beta1")
	SourceAPIVersion string `json:"sourceAPIVersion"`
	// TargetAPIVersion to transform into (e.g. "networking.k8s.io/v1")
	TargetAPIVersion string `json:"targetAPIVersion"`
	// TargetKind being transformed (e.g. "Ingress")
	Kind string `json:"kind"`
	// CustomTransformName refers to built-in transformer or jq-like expression
	CustomTransformName string `json:"customTransformName,omitempty"`
}

// ClusterEndpoint defines connection credentials for source and target clusters in air-gapped environments.
type ClusterEndpoint struct {
	// APIServerURL for the remote cluster
	APIServerURL string `json:"apiServerUrl"`
	// SecretRef contains kubeconfig or bearer token
	SecretRef corev1.SecretReference `json:"secretRef"`
	// InsecureSkipTLSVerify is strictly discouraged in production, but available for test harnesses
	InsecureSkipTLSVerify bool `json:"insecureSkipTlsVerify,omitempty"`
}

// VolumeMigrationSpec configures CSI snapshotting and volume replication.
type VolumeMigrationSpec struct {
	// VolumeSnapshotClassName for CSI driver
	VolumeSnapshotClassName string `json:"volumeSnapshotClassName"`
	// TargetStorageClassName to bind on target cluster
	TargetStorageClassName string `json:"targetStorageClassName,omitempty"`
	// PersistentVolumeClaimNames to snapshot and migrate
	PersistentVolumeClaimNames []string `json:"persistentVolumeClaimNames"`
	// RetainOnSource leaves snapshot and original PVC intact
	// +kubebuilder:default=true
	RetainOnSource bool `json:"retainOnSource"`
}

// PortageMigrationSpec defines the desired state of PortageMigration.
type PortageMigrationSpec struct {
	// SourceWorkload identifies the stateful application to migrate
	SourceWorkload corev1.TypedLocalObjectReference `json:"sourceWorkload"`
	// SourceNamespace where the workload currently lives
	SourceNamespace string `json:"sourceNamespace"`
	// TargetNamespace where the workload will be ported
	TargetNamespace string `json:"targetNamespace"`
	// TargetCluster credentials; if nil, in-cluster migration
	// +optional
	TargetCluster *ClusterEndpoint `json:"targetCluster,omitempty"`
	// PreQuiesceHooks run before CSI snapshot (e.g., FLUSH TABLES WITH READ LOCK)
	PreQuiesceHooks []QuiescenceHook `json:"preQuiesceHooks,omitempty"`
	// PostQuiesceHooks run immediately after CSI snapshot creation (e.g., UNLOCK TABLES)
	PostQuiesceHooks []QuiescenceHook `json:"postQuiesceHooks,omitempty"`
	// VolumeMigration settings
	VolumeMigration VolumeMigrationSpec `json:"volumeMigration"`
	// TranslationRules for mutating manifests from legacy to modern schemas
	TranslationRules []APITranslationRule `json:"translationRules,omitempty"`
}

// PortageMigrationStatus defines the observed state of PortageMigration.
type PortageMigrationStatus struct {
	Phase              Phase              `json:"phase"`
	ObservedGeneration int64              `json:"observedGeneration,omitempty"`
	Conditions         []metav1.Condition `json:"conditions,omitempty"`
	CreatedSnapshots   []string           `json:"createdSnapshots,omitempty"`
	TranslatedObjects  []string           `json:"translatedObjects,omitempty"`
	StartTime          *metav1.Time       `json:"startTime,omitempty"`
	CompletionTime     *metav1.Time       `json:"completionTime,omitempty"`
	ErrorMessage       string             `json:"errorMessage,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Phase",type="string",JSONPath=".status.phase"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"

// PortageMigration is the Schema for the portagemigrations API
type PortageMigration struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   PortageMigrationSpec   `json:"spec,omitempty"`
	Status PortageMigrationStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// PortageMigrationList contains a list of PortageMigration
type PortageMigrationList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []PortageMigration `json:"items"`
}
