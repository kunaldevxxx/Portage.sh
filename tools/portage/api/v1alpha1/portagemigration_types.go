package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Phase represents the current state in the migration lifecycle.
type Phase string

const (
	PhasePending         Phase = "Pending"
	PhaseValidating      Phase = "Validating"
	PhaseTranslating     Phase = "Translating"
	PhaseReplicatingData Phase = "ReplicatingData"
	PhaseQuiescing       Phase = "Quiescing"
	PhaseSnapshotting    Phase = "Snapshotting"
	PhaseUnquiescing     Phase = "Unquiescing"
	PhaseApplyingTarget  Phase = "ApplyingTarget"
	PhaseCutover         Phase = "Cutover"
	PhaseCompleted       Phase = "Completed"
	PhaseFailed          Phase = "Failed"
	PhaseRollback        Phase = "Rollback"
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
	Name           string       `json:"name"`
	Type           HookType     `json:"type"`
	PodExec        *PodExecHook `json:"podExec,omitempty"`
	HTTP           *HTTPHook    `json:"http,omitempty"`
	TimeoutSeconds int32        `json:"timeoutSeconds,omitempty"`
	MaxRetries     int32        `json:"maxRetries,omitempty"`
}

type PodExecHook struct {
	Selector  metav1.LabelSelector `json:"selector"`
	Container string               `json:"container,omitempty"`
	Command   []string             `json:"command"`
}

type HTTPHook struct {
	URL     string            `json:"url"`
	Method  string            `json:"method"`
	Headers map[string]string `json:"headers,omitempty"`
}

// APITranslationRule defines AST transformations for deprecated Kubernetes APIs.
type APITranslationRule struct {
	SourceAPIVersion    string `json:"sourceAPIVersion"`
	TargetAPIVersion    string `json:"targetAPIVersion"`
	Kind                string `json:"kind"`
	CustomTransformName string `json:"customTransformName,omitempty"`
}

// ClusterEndpoint defines connection credentials for source and target clusters in air-gapped environments.
type ClusterEndpoint struct {
	APIServerURL          string                 `json:"apiServerUrl"`
	SecretRef             corev1.SecretReference `json:"secretRef"`
	InsecureSkipTLSVerify bool                   `json:"insecureSkipTlsVerify,omitempty"`
}

// VolumeMigrationSpec configures CSI snapshotting and volume replication.
type VolumeMigrationSpec struct {
	VolumeSnapshotClassName    string   `json:"volumeSnapshotClassName"`
	TargetStorageClassName     string   `json:"targetStorageClassName,omitempty"`
	PersistentVolumeClaimNames []string `json:"persistentVolumeClaimNames"`
	RetainOnSource             bool     `json:"retainOnSource"`
}

// CDCReplicationSpec defines integration parameters for Synapse-CDC streaming replication.
type CDCReplicationSpec struct {
	Driver                    string `json:"driver,omitempty"`
	Enabled                   bool   `json:"enabled"`
	Endpoint                  string `json:"endpoint"`
	StreamID                  string `json:"streamID"`
	RequireMerkleVerification bool   `json:"requireMerkleVerification,omitempty"`
	MaxAllowedLagSeconds      int64  `json:"maxAllowedLagSeconds,omitempty"`
}

// TrafficCutoverSpec defines integration parameters for Ghostwire zero-drop traffic cutover.
type TrafficCutoverSpec struct {
	Driver              string `json:"driver,omitempty"`
	Enabled             bool   `json:"enabled"`
	DirectorEndpoint    string `json:"directorEndpoint"`
	RouteID             string `json:"routeID"`
	TargetEndpoint      string `json:"targetEndpoint"`
	DrainTimeoutSeconds int32  `json:"drainTimeoutSeconds,omitempty"`
}

// PortageMigrationSpec defines the desired state of PortageMigration.
type PortageMigrationSpec struct {
	SourceWorkload   corev1.TypedLocalObjectReference `json:"sourceWorkload"`
	SourceNamespace  string                           `json:"sourceNamespace"`
	TargetNamespace  string                           `json:"targetNamespace"`
	TargetCluster    *ClusterEndpoint                 `json:"targetCluster,omitempty"`
	PreQuiesceHooks  []QuiescenceHook                 `json:"preQuiesceHooks,omitempty"`
	PostQuiesceHooks []QuiescenceHook                 `json:"postQuiesceHooks,omitempty"`
	VolumeMigration  VolumeMigrationSpec              `json:"volumeMigration"`
	CDCReplication   *CDCReplicationSpec              `json:"cdcReplication,omitempty"`
	TrafficCutover   *TrafficCutoverSpec              `json:"trafficCutover,omitempty"`
	TranslationRules []APITranslationRule             `json:"translationRules,omitempty"`
}

// PortageMigrationStatus defines the observed state of PortageMigration.
type PortageMigrationStatus struct {
	Phase                 Phase              `json:"phase"`
	ObservedGeneration    int64              `json:"observedGeneration,omitempty"`
	Conditions            []metav1.Condition `json:"conditions,omitempty"`
	CreatedSnapshots      []string           `json:"createdSnapshots,omitempty"`
	TranslatedObjects     []string           `json:"translatedObjects,omitempty"`
	ReplicationLagSeconds int64              `json:"replicationLagSeconds,omitempty"`
	MerkleVerified        bool               `json:"merkleVerified,omitempty"`
	CutoverCompleted      bool               `json:"cutoverCompleted,omitempty"`
	TargetWorkloadReady   bool               `json:"targetWorkloadReady,omitempty"`
	StartTime             *metav1.Time       `json:"startTime,omitempty"`
	CompletionTime        *metav1.Time       `json:"completionTime,omitempty"`
	ErrorMessage          string             `json:"errorMessage,omitempty"`
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
