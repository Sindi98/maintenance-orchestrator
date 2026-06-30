package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// MaintenancePolicySpec defines cluster-wide guardrails applied to every
// MaintenanceRequest that references this policy (or the default policy).
type MaintenancePolicySpec struct {
	// ProtectControlPlane forbids draining control-plane nodes unless a request
	// sets AllowControlPlane and this stays false-overridable per deployment policy.
	// +kubebuilder:default=true
	ProtectControlPlane bool `json:"protectControlPlane"`

	// ControlPlaneNodeLabels are the label keys that identify control-plane nodes.
	// +optional
	// +kubebuilder:default={"node-role.kubernetes.io/control-plane","node-role.kubernetes.io/master"}
	ControlPlaneNodeLabels []string `json:"controlPlaneNodeLabels,omitempty"`

	// MaxConcurrentDrains caps how many nodes a single MaintenanceRequest may
	// drain at once (its effective per-request concurrency is the minimum of this
	// and the request's spec.maxConcurrent). It is enforced per request, not
	// summed across concurrently-running requests; use MaxUnavailableNodes /
	// MaxUnavailablePercent to bound cluster-wide blast radius across requests.
	// +kubebuilder:default=1
	// +kubebuilder:validation:Minimum=1
	MaxConcurrentDrains int32 `json:"maxConcurrentDrains"`

	// MaxUnavailableNodes caps how many matched nodes may be unschedulable at once.
	// Zero disables the absolute cap.
	// +optional
	// +kubebuilder:validation:Minimum=0
	MaxUnavailableNodes int32 `json:"maxUnavailableNodes,omitempty"`

	// MaxUnavailablePercent caps unavailable matched nodes as a percentage. The
	// preflight check compares this against the PEAK simultaneous unavailability,
	// i.e. the per-batch concurrency for requests that uncordon, so a one-at-a-time
	// rolling drain of a whole pool is not blocked by a small cap. On a policy
	// object zero disables the percentage cap; the built-in default when no policy
	// exists is 33. Set 100 to allow the whole scope (effectively uncapped).
	// +optional
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=100
	MaxUnavailablePercent int32 `json:"maxUnavailablePercent,omitempty"`

	// ReservedNodeLabels block any node carrying one of these label keys.
	// +optional
	ReservedNodeLabels []string `json:"reservedNodeLabels,omitempty"`

	// ReservedTaints block any node carrying one of these taint keys.
	// +optional
	ReservedTaints []string `json:"reservedTaints,omitempty"`

	// MinCapacityHeadroomPercent is the minimum request-based headroom that must
	// remain after a node is removed. Zero disables the heuristic capacity check.
	// +optional
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=100
	MinCapacityHeadroomPercent int32 `json:"minCapacityHeadroomPercent,omitempty"`

	// AllowForceEviction permits deletion-based eviction (bypassing the eviction
	// API) when a request also sets Force. Off by default.
	// +kubebuilder:default=false
	AllowForceEviction bool `json:"allowForceEviction"`

	// AllowNodeReplacement permits requests with spec.upgrade to delete a node's
	// backing Machine after draining (node replacement). Off by default: node
	// replacement is destructive, so it must be explicitly enabled by policy.
	// +kubebuilder:default=false
	AllowNodeReplacement bool `json:"allowNodeReplacement"`

	// DefaultApprovalPolicy is applied to requests that do not specify one.
	// +optional
	// +kubebuilder:default=AutoApprove
	DefaultApprovalPolicy ApprovalPolicy `json:"defaultApprovalPolicy,omitempty"`

	// AllowedWindows restricts mutation to these recurring windows when set.
	// +optional
	AllowedWindows []Window `json:"allowedWindows,omitempty"`

	// FailureThreshold is the number of node failures that aborts a request.
	// +optional
	// +kubebuilder:default=1
	// +kubebuilder:validation:Minimum=1
	FailureThreshold int32 `json:"failureThreshold,omitempty"`

	// NodeSelector limits the scope of this policy to matching nodes; empty
	// means the policy applies cluster-wide.
	// +optional
	NodeSelector *metav1.LabelSelector `json:"nodeSelector,omitempty"`
}

// MaintenancePolicyStatus is the observed state of a MaintenancePolicy.
type MaintenancePolicyStatus struct {
	// Conditions report validation and readiness of the policy.
	// +optional
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type"`

	// ObservedGeneration is the spec generation last processed by the controller.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster,shortName=mpol
// +kubebuilder:printcolumn:name="ProtectCP",type=boolean,JSONPath=`.spec.protectControlPlane`
// +kubebuilder:printcolumn:name="MaxConcurrent",type=integer,JSONPath=`.spec.maxConcurrentDrains`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// MaintenancePolicy holds cluster-wide guardrails for maintenance operations.
type MaintenancePolicy struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   MaintenancePolicySpec   `json:"spec,omitempty"`
	Status MaintenancePolicyStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// MaintenancePolicyList contains a list of MaintenancePolicy.
type MaintenancePolicyList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []MaintenancePolicy `json:"items"`
}

func init() {
	SchemeBuilder.Register(&MaintenancePolicy{}, &MaintenancePolicyList{})
}
