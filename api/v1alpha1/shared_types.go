package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Mode controls whether a request only analyzes the cluster or actually mutates it.
// +kubebuilder:validation:Enum=DryRun;Advisory;Execute
type Mode string

const (
	// ModeDryRun evaluates targets and preflight once, never mutates, then completes.
	ModeDryRun Mode = "DryRun"
	// ModeAdvisory continuously re-evaluates targets and preflight, never mutates.
	ModeAdvisory Mode = "Advisory"
	// ModeExecute runs the full mutating flow (cordon, drain, optional uncordon).
	ModeExecute Mode = "Execute"
)

// Strategy controls how target nodes are grouped and sequenced.
// +kubebuilder:validation:Enum=Serial;Batched;ByZone;ByPool
type Strategy string

const (
	// StrategySerial drains one node at a time.
	StrategySerial Strategy = "Serial"
	// StrategyBatched drains nodes in fixed-size batches.
	StrategyBatched Strategy = "Batched"
	// StrategyByZone groups nodes by topology zone, one zone at a time.
	StrategyByZone Strategy = "ByZone"
	// StrategyByPool groups nodes by pool key/value, one pool at a time.
	StrategyByPool Strategy = "ByPool"
)

// TargetType selects how target nodes are resolved.
// +kubebuilder:validation:Enum=Node;NodeSelector;Pool
type TargetType string

const (
	// TargetNode targets an explicit list of node names.
	TargetNode TargetType = "Node"
	// TargetNodeSelector targets nodes matching a label selector.
	TargetNodeSelector TargetType = "NodeSelector"
	// TargetPool targets nodes belonging to a pool (poolKey=poolValue).
	TargetPool TargetType = "Pool"
)

// ApprovalPolicy controls which gates require a manual decision before proceeding.
// +kubebuilder:validation:Enum=AutoApprove;ManualBeforeDrain;ManualBeforeUncordon;ManualBeforeBoth
type ApprovalPolicy string

const (
	// ApprovalAuto requires no manual approval.
	ApprovalAuto ApprovalPolicy = "AutoApprove"
	// ApprovalManualBeforeDrain requires a Drain-gate decision before draining.
	ApprovalManualBeforeDrain ApprovalPolicy = "ManualBeforeDrain"
	// ApprovalManualBeforeUncordon requires an Uncordon-gate decision before uncordoning.
	ApprovalManualBeforeUncordon ApprovalPolicy = "ManualBeforeUncordon"
	// ApprovalManualBeforeBoth requires both gate decisions.
	ApprovalManualBeforeBoth ApprovalPolicy = "ManualBeforeBoth"
)

// Gate identifies an approval checkpoint.
// +kubebuilder:validation:Enum=Drain;Uncordon
type Gate string

const (
	// GateDrain is the checkpoint evaluated before draining begins.
	GateDrain Gate = "Drain"
	// GateUncordon is the checkpoint evaluated before nodes are returned to service.
	GateUncordon Gate = "Uncordon"
)

// Decision is the outcome recorded for an approval gate.
// +kubebuilder:validation:Enum=Approved;Rejected
type Decision string

const (
	// DecisionApproved allows the gated transition to proceed.
	DecisionApproved Decision = "Approved"
	// DecisionRejected cancels the request at the gate.
	DecisionRejected Decision = "Rejected"
)

// CheckStatus is the severity of a single preflight check result.
// +kubebuilder:validation:Enum=Pass;Warn;Fail
type CheckStatus string

const (
	// CheckPass means the check found no issue.
	CheckPass CheckStatus = "Pass"
	// CheckWarn means the check found a non-blocking risk.
	CheckWarn CheckStatus = "Warn"
	// CheckFail means the check found a blocking problem.
	CheckFail CheckStatus = "Fail"
)

// Phase is the top-level state of a MaintenanceRequest.
// +kubebuilder:validation:Enum=Pending;Validating;AwaitingApproval;Planned;Executing;Paused;Blocked;Completed;Failed;Cancelled
type Phase string

const (
	PhasePending          Phase = "Pending"
	PhaseValidating       Phase = "Validating"
	PhaseAwaitingApproval Phase = "AwaitingApproval"
	PhasePlanned          Phase = "Planned"
	PhaseExecuting        Phase = "Executing"
	PhasePaused           Phase = "Paused"
	PhaseBlocked          Phase = "Blocked"
	PhaseCompleted        Phase = "Completed"
	PhaseFailed           Phase = "Failed"
	PhaseCancelled        Phase = "Cancelled"
)

// NodePhase is the per-node state during execution.
// +kubebuilder:validation:Enum=Pending;Cordoning;Draining;PostCheck;Uncordoning;Replacing;AwaitingReplacement;Completed;Blocked;Failed;Skipped
type NodePhase string

const (
	NodePending     NodePhase = "Pending"
	NodeCordoning   NodePhase = "Cordoning"
	NodeDraining    NodePhase = "Draining"
	NodePostCheck   NodePhase = "PostCheck"
	NodeUncordoning NodePhase = "Uncordoning"
	// NodeReplacing deletes the node's backing Machine after a successful drain.
	NodeReplacing NodePhase = "Replacing"
	// NodeAwaitingReplacement waits for the old node to be removed and (when a
	// target version is set) for a replacement node to come up at that version.
	NodeAwaitingReplacement NodePhase = "AwaitingReplacement"
	NodeCompleted           NodePhase = "Completed"
	NodeBlocked             NodePhase = "Blocked"
	NodeFailed              NodePhase = "Failed"
	NodeSkipped             NodePhase = "Skipped"
)

// UpgradeStrategy selects how a node's Kubernetes version is upgraded.
// +kubebuilder:validation:Enum=ReplaceNode
type UpgradeStrategy string

const (
	// UpgradeReplaceNode deletes the node's backing Machine so the owning
	// MachineSet/MachineDeployment recreates it at the pool's version.
	UpgradeReplaceNode UpgradeStrategy = "ReplaceNode"
)

// MachineAPI selects which Machine API backs the nodes being replaced.
// +kubebuilder:validation:Enum=Auto;ClusterAPI;OpenShift
type MachineAPI string

const (
	// MachineAPIAuto infers the API from node annotations (OpenShift first).
	MachineAPIAuto MachineAPI = "Auto"
	// MachineAPIClusterAPI uses cluster.x-k8s.io/v1beta1 Machines.
	MachineAPIClusterAPI MachineAPI = "ClusterAPI"
	// MachineAPIOpenShift uses machine.openshift.io/v1beta1 Machines.
	MachineAPIOpenShift MachineAPI = "OpenShift"
)

// Preflight check codes (machine-readable, recorded in status.preflight[].code).
const (
	CodeNodeNotFound       = "NODE_NOT_FOUND"
	CodeNodeNotReady       = "NODE_NOT_READY"
	CodeAlreadyCordoned    = "ALREADY_CORDONED"
	CodeControlPlane       = "CONTROL_PLANE_PROTECTED"
	CodeReservedLabel      = "RESERVED_LABEL"
	CodeReservedTaint      = "RESERVED_TAINT"
	CodePDBBlocks          = "PDB_BLOCKS"
	CodeSingleReplica      = "SINGLE_REPLICA_WORKLOAD"
	CodeEmptyDir           = "EMPTYDIR_DATA_LOSS"
	CodeLocalStorage       = "LOCAL_STORAGE_RISK"
	CodeDaemonSetPods      = "DAEMONSET_PODS"
	CodeStaticPod          = "STATIC_POD"
	CodeInsufficientCap    = "INSUFFICIENT_CAPACITY"
	CodeTooManyUnavailable = "TOO_MANY_UNAVAILABLE"
	CodeWindowClosed       = "WINDOW_CLOSED"
	CodeMCOManaged         = "MCO_MANAGED"
	CodeMachineNotFound    = "MACHINE_NOT_FOUND"
	CodeAlreadyAtVersion   = "ALREADY_AT_TARGET_VERSION"
	CodeReplacementDenied  = "REPLACEMENT_NOT_ALLOWED"
)

// Block reasons (machine-readable, recorded in status.nodes[].blockReason).
const (
	BlockPDB              = "PDB"
	BlockFinalizer        = "Finalizer"
	BlockStuckTermination = "StuckTermination"
	BlockCapacity         = "Capacity"
	BlockDaemonSet        = "DaemonSetIgnored"
	BlockLocalStorage     = "LocalStorageRisk"
	BlockEvictionError    = "EvictionError"
	BlockTimeout          = "Timeout"
	BlockMachineNotFound  = "MachineNotFound"
	BlockReplaceTimeout   = "ReplacementTimeout"
)

// Condition types surfaced on status.conditions[].type.
const (
	CondValidated          = "Validated"
	CondApproved           = "Approved"
	CondPlanned            = "Planned"
	CondExecuting          = "Executing"
	CondWindowOpen         = "WindowOpen"
	CondGuardrailViolation = "GuardrailViolation"
	CondCompleted          = "Completed"
	CondBlocked            = "Blocked"
	CondFailed             = "Failed"
)

// TargetRef selects the nodes a request operates on.
type TargetRef struct {
	// Type selects the resolution mode for target nodes.
	// +kubebuilder:validation:Required
	Type TargetType `json:"type"`

	// NodeNames is the explicit node list, used when Type is Node.
	// +optional
	NodeNames []string `json:"nodeNames,omitempty"`

	// Selector matches nodes by label, used when Type is NodeSelector.
	// +optional
	Selector *metav1.LabelSelector `json:"selector,omitempty"`

	// PoolKey is the node label key identifying a pool, used when Type is Pool.
	// +optional
	PoolKey string `json:"poolKey,omitempty"`

	// PoolValue is the node label value identifying a pool, used when Type is Pool.
	// +optional
	PoolValue string `json:"poolValue,omitempty"`
}

// UpgradeSpec configures node replacement after a successful drain. When set,
// each drained node is replaced (its backing Machine is deleted) instead of being
// uncordoned, so the owning MachineSet recreates it at the pool's version.
type UpgradeSpec struct {
	// Strategy selects how the node version is upgraded.
	// +optional
	// +kubebuilder:default=ReplaceNode
	Strategy UpgradeStrategy `json:"strategy,omitempty"`

	// MachineAPI selects which Machine API backs the nodes. Auto infers it from
	// node annotations (OpenShift machine.openshift.io, then Cluster API).
	// +optional
	// +kubebuilder:default=Auto
	MachineAPI MachineAPI `json:"machineAPI,omitempty"`

	// TargetKubeletVersion, when set, is the kubelet version a replacement node
	// must report before the node is marked complete (post-check), e.g. "v1.30.2".
	// When empty, the node completes as soon as the old node is removed.
	// +optional
	TargetKubeletVersion string `json:"targetKubeletVersion,omitempty"`

	// ReplacementTimeout bounds the wait for a replacement node to come up after
	// the Machine is deleted; zero uses the controller default.
	// +optional
	ReplacementTimeout metav1.Duration `json:"replacementTimeout,omitempty"`
}

// Window describes a recurring time window during which mutation is allowed.
type Window struct {
	// Cron is a 5-field cron expression marking the START of the window.
	// +kubebuilder:validation:Required
	Cron string `json:"cron"`

	// Duration is how long the window stays open after each start.
	// +kubebuilder:validation:Required
	Duration metav1.Duration `json:"duration"`

	// TimeZone is an IANA name (e.g. "Europe/Rome"); empty means UTC.
	// +optional
	TimeZone string `json:"timeZone,omitempty"`
}

// ApprovalSpec configures the manual-approval workflow for a request.
type ApprovalSpec struct {
	// Policy selects which gates require a manual decision.
	// +kubebuilder:default=AutoApprove
	Policy ApprovalPolicy `json:"policy"`

	// Gates carries human decisions for the gates required by Policy.
	// +optional
	Gates []GateDecision `json:"gates,omitempty"`
}

// GateDecision records a human decision for a single approval gate.
type GateDecision struct {
	// Gate identifies the checkpoint this decision applies to.
	Gate Gate `json:"gate"`

	// Decision is Approved or Rejected.
	Decision Decision `json:"decision"`

	// ApprovedBy identifies the user or system that recorded the decision.
	// +optional
	ApprovedBy string `json:"approvedBy,omitempty"`

	// Reason is an optional free-text justification.
	// +optional
	Reason string `json:"reason,omitempty"`

	// Time is when the decision was recorded.
	// +optional
	Time *metav1.Time `json:"time,omitempty"`
}

// PolicyRef references a MaintenancePolicy by name.
type PolicyRef struct {
	// Name is the cluster-scoped MaintenancePolicy name.
	// +kubebuilder:validation:Required
	Name string `json:"name"`
}

// PreflightCheckResult is one safety-check outcome recorded in status.
type PreflightCheckResult struct {
	// Code is the machine-readable check identifier (see Code* constants).
	Code string `json:"code"`

	// Node is the node the check applies to, if node-scoped.
	// +optional
	Node string `json:"node,omitempty"`

	// Status is the severity of the result.
	Status CheckStatus `json:"status"`

	// Message is a human-readable explanation.
	Message string `json:"message"`

	// Details carries optional structured context (e.g. pdb name, pod name).
	// +optional
	Details map[string]string `json:"details,omitempty"`

	// Time is when the check was evaluated.
	Time metav1.Time `json:"time"`
}

// ExecutionPlan is the resolved, ordered plan computed by the planner.
type ExecutionPlan struct {
	// Strategy is the effective strategy used to build the batches.
	Strategy Strategy `json:"strategy"`

	// Batches are the ordered groups of nodes to process.
	Batches []Batch `json:"batches"`

	// TotalNodes is the number of distinct nodes covered by the plan.
	TotalNodes int32 `json:"totalNodes"`

	// MaxConcurrent is the effective per-batch concurrency.
	MaxConcurrent int32 `json:"maxConcurrent"`

	// RiskScore is a 0-100 heuristic of the disruption this plan may cause.
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=100
	RiskScore int32 `json:"riskScore"`

	// RiskFactors lists the human-readable factors that raised the score.
	// +optional
	RiskFactors []string `json:"riskFactors,omitempty"`

	// Impact is the estimated workload impact of executing the plan.
	Impact ImpactEstimate `json:"impact"`

	// GeneratedAt is when the plan was computed.
	GeneratedAt metav1.Time `json:"generatedAt"`
}

// Batch is one ordered group of nodes within an ExecutionPlan.
type Batch struct {
	// Index is the zero-based position of the batch in the plan.
	Index int32 `json:"index"`

	// Group is an optional label for the batch (e.g. zone or pool value).
	// +optional
	Group string `json:"group,omitempty"`

	// Nodes are the node names in this batch.
	Nodes []string `json:"nodes"`
}

// ImpactEstimate is a heuristic projection of workload disruption.
type ImpactEstimate struct {
	// PodsToEvict is the total number of evictable pods across all targets.
	PodsToEvict int32 `json:"podsToEvict"`

	// AppsAffected is the number of distinct owning workloads touched.
	AppsAffected int32 `json:"appsAffected"`

	// SingleReplicaWorkloads counts owners that would drop to zero ready replicas.
	SingleReplicaWorkloads int32 `json:"singleReplicaWorkloads"`

	// EmptyDirPods counts pods that would lose emptyDir data on eviction.
	EmptyDirPods int32 `json:"emptyDirPods"`
}

// NodeExecutionStatus tracks the progress of a single node.
type NodeExecutionStatus struct {
	// Node is the node name.
	Node string `json:"node"`

	// Phase is the current per-node phase.
	Phase NodePhase `json:"phase"`

	// Batch is the index of the batch this node belongs to.
	Batch int32 `json:"batch"`

	// StartTime is when work on this node began.
	// +optional
	StartTime *metav1.Time `json:"startTime,omitempty"`

	// EndTime is when this node reached a terminal per-node phase.
	// +optional
	EndTime *metav1.Time `json:"endTime,omitempty"`

	// TotalPods is the number of evictable pods discovered on the node.
	TotalPods int32 `json:"totalPods"`

	// EvictedPods is the number of pods successfully evicted so far.
	EvictedPods int32 `json:"evictedPods"`

	// RemainingPods is the number of evictable pods still present.
	RemainingPods int32 `json:"remainingPods"`

	// ReplacementBaseline is internal bookkeeping for node replacement: the number
	// of Ready nodes already at the target kubelet version when this node's
	// replacement began. The replacement is considered verified once the count of
	// Ready nodes at that version exceeds this baseline (i.e. a genuinely new node
	// joined), instead of matching any pre-existing node at the version.
	// +optional
	ReplacementBaseline int32 `json:"replacementBaseline,omitempty"`

	// BlockReason is the machine-readable reason the node is blocked, if any.
	// +optional
	BlockReason string `json:"blockReason,omitempty"`

	// Message is a human-readable per-node status detail.
	// +optional
	Message string `json:"message,omitempty"`
}

// ProgressSummary is a roll-up of per-node phases for quick display.
type ProgressSummary struct {
	Total      int32 `json:"total"`
	Pending    int32 `json:"pending"`
	InProgress int32 `json:"inProgress"`
	Completed  int32 `json:"completed"`
	Blocked    int32 `json:"blocked"`
	Failed     int32 `json:"failed"`
	Skipped    int32 `json:"skipped"`
}
