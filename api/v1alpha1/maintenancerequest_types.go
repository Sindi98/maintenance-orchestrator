package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// MaintenanceSpec is the desired state of a MaintenanceRequest.
type MaintenanceSpec struct {
	// Mode controls whether this request mutates the cluster (Execute) or only
	// analyzes it (DryRun/Advisory).
	// +kubebuilder:validation:Required
	Mode Mode `json:"mode"`

	// Reason is a human-readable justification, recorded in the audit trail.
	// +kubebuilder:validation:Required
	Reason string `json:"reason"`

	// RequestedBy identifies who requested the maintenance.
	// +kubebuilder:validation:Required
	RequestedBy string `json:"requestedBy"`

	// Target selects the nodes this request operates on.
	// +kubebuilder:validation:Required
	Target TargetRef `json:"target"`

	// Strategy controls how nodes are grouped and sequenced.
	// omitempty lets the apiserver apply the default when the field is omitted by
	// a typed client (which would otherwise send "" and fail the enum).
	// +optional
	// +kubebuilder:default=Serial
	Strategy Strategy `json:"strategy,omitempty"`

	// MaxConcurrent caps how many nodes are drained at once within a batch.
	// The cluster-wide MaintenancePolicy.MaxConcurrentDrains still applies on top.
	// omitempty lets the apiserver apply the default when the field is omitted by
	// a typed client (which would otherwise send 0 and fail the minimum).
	// +optional
	// +kubebuilder:default=1
	// +kubebuilder:validation:Minimum=1
	MaxConcurrent int32 `json:"maxConcurrent,omitempty"`

	// BatchSize is the number of nodes per batch when Strategy is Batched.
	// +optional
	// +kubebuilder:validation:Minimum=1
	BatchSize int32 `json:"batchSize,omitempty"`

	// DrainTimeout bounds the drain of a single node; zero means use the
	// controller default.
	// +optional
	DrainTimeout metav1.Duration `json:"drainTimeout,omitempty"`

	// GlobalTimeout bounds the entire request; zero means use the controller default.
	// +optional
	GlobalTimeout metav1.Duration `json:"globalTimeout,omitempty"`

	// UncordonAfter returns each node to service after a successful drain and
	// post-check. Subject to the Uncordon approval gate when configured.
	// +kubebuilder:default=true
	UncordonAfter bool `json:"uncordonAfter"`

	// MaintenanceWindow restricts mutation to a recurring time window.
	// +optional
	MaintenanceWindow *Window `json:"maintenanceWindow,omitempty"`

	// Approval configures the manual-approval workflow.
	// +kubebuilder:validation:Required
	Approval ApprovalSpec `json:"approval"`

	// Pause requests the controller to hold execution at the next safe point.
	// +optional
	Pause bool `json:"pause,omitempty"`

	// Cancel requests the controller to stop and finalize the request as Cancelled.
	// +optional
	Cancel bool `json:"cancel,omitempty"`

	// PolicyRef overrides the default cluster MaintenancePolicy for this request.
	// +optional
	PolicyRef *PolicyRef `json:"policyRef,omitempty"`

	// AllowControlPlane opts this request out of control-plane protection.
	// It is only honored if the effective policy also permits it.
	// +optional
	AllowControlPlane bool `json:"allowControlPlane,omitempty"`

	// Force allows deletion-based eviction when policy-permitted (AllowForceEviction).
	// +optional
	Force bool `json:"force,omitempty"`
}

// MaintenanceStatus is the observed state of a MaintenanceRequest.
type MaintenanceStatus struct {
	// Phase is the top-level state of the request.
	// +optional
	Phase Phase `json:"phase,omitempty"`

	// Conditions are the detailed, machine-readable state transitions.
	// +optional
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type"`

	// ObservedGeneration is the spec generation last processed by the controller.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// StartTime is when execution began.
	// +optional
	StartTime *metav1.Time `json:"startTime,omitempty"`

	// CompletionTime is when the request reached a terminal phase.
	// +optional
	CompletionTime *metav1.Time `json:"completionTime,omitempty"`

	// ApprovalGate, when Phase is AwaitingApproval, indicates which gate is pending.
	// +optional
	ApprovalGate Gate `json:"approvalGate,omitempty"`

	// Preflight holds the latest safety-check results.
	// +optional
	Preflight []PreflightCheckResult `json:"preflight,omitempty"`

	// Plan holds the resolved execution plan once computed.
	// +optional
	Plan *ExecutionPlan `json:"plan,omitempty"`

	// Nodes holds per-node execution status.
	// +optional
	Nodes []NodeExecutionStatus `json:"nodes,omitempty"`

	// Summary is a roll-up of per-node phases.
	// +optional
	Summary ProgressSummary `json:"summary,omitempty"`

	// Message is a human-readable summary of the current state.
	// +optional
	Message string `json:"message,omitempty"`

	// LastError is the most recent error encountered, if any.
	// +optional
	LastError string `json:"lastError,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster,shortName=mreq
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Mode",type=string,JSONPath=`.spec.mode`
// +kubebuilder:printcolumn:name="Strategy",type=string,JSONPath=`.spec.strategy`
// +kubebuilder:printcolumn:name="Total",type=integer,JSONPath=`.status.summary.total`
// +kubebuilder:printcolumn:name="Done",type=integer,JSONPath=`.status.summary.completed`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// MaintenanceRequest is a single, auditable node/pool maintenance operation.
type MaintenanceRequest struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   MaintenanceSpec   `json:"spec,omitempty"`
	Status MaintenanceStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// MaintenanceRequestList contains a list of MaintenanceRequest.
type MaintenanceRequestList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []MaintenanceRequest `json:"items"`
}

func init() {
	SchemeBuilder.Register(&MaintenanceRequest{}, &MaintenanceRequestList{})
}
