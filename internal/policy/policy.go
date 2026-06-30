// Package policy resolves the effective MaintenancePolicy for a request and
// exposes the cluster-wide guardrails as small, mostly pure helper functions so
// the preflight engine, planner and controller all evaluate the same rules.
package policy

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/Sindi98/maintenance-orchestrator/api/v1alpha1"
	"github.com/Sindi98/maintenance-orchestrator/internal/approval"
	"github.com/Sindi98/maintenance-orchestrator/internal/kube"
)

// Conservative default guardrail values used when a MaintenancePolicy omits a
// field, or when no policy object can be found at all.
const (
	DefaultMaxConcurrentDrains int32 = 1
	DefaultFailureThreshold    int32 = 1
	// DefaultMaxUnavailablePercent is the blast-radius cap applied when NO policy
	// object exists in the cluster. Combined with the concurrency-aware
	// unavailability check, a one-at-a-time rolling drain still passes; it mainly
	// bounds wide, non-uncordoning requests.
	DefaultMaxUnavailablePercent int32 = 33
)

// DefaultControlPlaneLabels identify control-plane nodes when a policy omits them.
var DefaultControlPlaneLabels = []string{
	"node-role.kubernetes.io/control-plane",
	"node-role.kubernetes.io/master",
}

// DefaultSpec returns a conservative MaintenancePolicySpec used when no policy
// object exists in the cluster: control-plane protected, serial drains, no force.
func DefaultSpec() v1alpha1.MaintenancePolicySpec {
	return v1alpha1.MaintenancePolicySpec{
		ProtectControlPlane:    true,
		ControlPlaneNodeLabels: append([]string(nil), DefaultControlPlaneLabels...),
		MaxConcurrentDrains:    DefaultMaxConcurrentDrains,
		MaxUnavailablePercent:  DefaultMaxUnavailablePercent,
		AllowForceEviction:     false,
		AllowNodeReplacement:   false,
		DefaultApprovalPolicy:  v1alpha1.ApprovalAuto,
		FailureThreshold:       DefaultFailureThreshold,
	}
}

// WithDefaults returns a copy of spec with zero-valued fields filled from DefaultSpec.
func WithDefaults(spec v1alpha1.MaintenancePolicySpec) v1alpha1.MaintenancePolicySpec {
	d := DefaultSpec()
	if len(spec.ControlPlaneNodeLabels) == 0 {
		spec.ControlPlaneNodeLabels = d.ControlPlaneNodeLabels
	}
	if spec.MaxConcurrentDrains < 1 {
		spec.MaxConcurrentDrains = d.MaxConcurrentDrains
	}
	if spec.FailureThreshold < 1 {
		spec.FailureThreshold = d.FailureThreshold
	}
	if spec.DefaultApprovalPolicy == "" {
		spec.DefaultApprovalPolicy = d.DefaultApprovalPolicy
	}
	return spec
}

// Effective is a fully-defaulted policy plus provenance metadata.
type Effective struct {
	// Spec is the resolved policy with defaults applied.
	Spec v1alpha1.MaintenancePolicySpec
	// Name is the resolved policy object name, or "" when built-in defaults are used.
	Name string
	// Found reports whether an actual MaintenancePolicy object backed this result.
	Found bool
}

// Resolve returns the effective policy for a request. Resolution order:
//
//   - spec.policyRef.Name when set (explicit). If that object is missing, an
//     error is returned because the request named a policy that does not exist.
//   - otherwise defaultName (the controller's configured default). If that object
//     is missing, built-in DefaultSpec() is used and no error is returned, so the
//     controller degrades safely on a fresh cluster.
func Resolve(ctx context.Context, c client.Client, req *v1alpha1.MaintenanceRequest, defaultName string) (*Effective, error) {
	explicit := req.Spec.PolicyRef != nil && req.Spec.PolicyRef.Name != ""
	name := defaultName
	if explicit {
		name = req.Spec.PolicyRef.Name
	}
	if name == "" {
		return &Effective{Spec: WithDefaults(DefaultSpec())}, nil
	}

	pol := &v1alpha1.MaintenancePolicy{}
	if err := c.Get(ctx, types.NamespacedName{Name: name}, pol); err != nil {
		if apierrors.IsNotFound(err) {
			if explicit {
				return nil, fmt.Errorf("referenced MaintenancePolicy %q not found", name)
			}
			return &Effective{Spec: WithDefaults(DefaultSpec()), Name: name}, nil
		}
		return nil, fmt.Errorf("get MaintenancePolicy %q: %w", name, err)
	}
	return &Effective{Spec: WithDefaults(pol.Spec), Name: name, Found: true}, nil
}

// IsControlPlaneNode reports whether the node is a control-plane node per the policy.
func (e *Effective) IsControlPlaneNode(node *corev1.Node) bool {
	return kube.IsControlPlane(node, e.Spec.ControlPlaneNodeLabels)
}

// ControlPlaneBlocked reports whether a control-plane node must be blocked for a
// request carrying the given allowControlPlane flag.
//
// A control-plane node is drainable only when BOTH gates open: the policy does
// not protect control-plane nodes AND the request explicitly opts in. This is a
// deliberate defense-in-depth rule; with the default policy (protect=true)
// control-plane nodes are never drained.
func (e *Effective) ControlPlaneBlocked(allowControlPlane bool) bool {
	return e.Spec.ProtectControlPlane || !allowControlPlane
}

// ReservedLabel returns the first reserved label key present on the node, if any.
func (e *Effective) ReservedLabel(node *corev1.Node) (string, bool) {
	for _, k := range e.Spec.ReservedNodeLabels {
		if _, ok := node.Labels[k]; ok {
			return k, true
		}
	}
	return "", false
}

// ReservedTaint returns the first reserved taint key present on the node, if any.
func (e *Effective) ReservedTaint(node *corev1.Node) (string, bool) {
	if len(e.Spec.ReservedTaints) == 0 {
		return "", false
	}
	reserved := make(map[string]struct{}, len(e.Spec.ReservedTaints))
	for _, k := range e.Spec.ReservedTaints {
		reserved[k] = struct{}{}
	}
	for i := range node.Spec.Taints {
		if _, ok := reserved[node.Spec.Taints[i].Key]; ok {
			return node.Spec.Taints[i].Key, true
		}
	}
	return "", false
}

// MaxUnavailable returns the absolute cap on simultaneously unschedulable matched
// nodes given the total matched count, combining the absolute and percentage caps.
// A zero field means "that cap disabled"; the most restrictive non-zero cap wins.
// The result is at least 1 so progress is always possible.
func (e *Effective) MaxUnavailable(totalMatched int32) int32 {
	limit := totalMatched
	if e.Spec.MaxUnavailableNodes > 0 && e.Spec.MaxUnavailableNodes < limit {
		limit = e.Spec.MaxUnavailableNodes
	}
	if e.Spec.MaxUnavailablePercent > 0 {
		pct := (totalMatched * e.Spec.MaxUnavailablePercent) / 100
		if pct < 1 {
			pct = 1
		}
		if pct < limit {
			limit = pct
		}
	}
	if limit < 1 {
		limit = 1
	}
	return limit
}

// UnavailabilityCapped reports whether either unavailability cap is configured.
func (e *Effective) UnavailabilityCapped() bool {
	return e.Spec.MaxUnavailableNodes > 0 || e.Spec.MaxUnavailablePercent > 0
}

// Concurrency returns the effective per-batch drain concurrency: the minimum of
// the request's MaxConcurrent and the policy's MaxConcurrentDrains, at least 1.
func (e *Effective) Concurrency(requested int32) int32 {
	c := requested
	if c < 1 {
		c = 1
	}
	if e.Spec.MaxConcurrentDrains > 0 && e.Spec.MaxConcurrentDrains < c {
		c = e.Spec.MaxConcurrentDrains
	}
	return c
}

// ForceAllowed reports whether deletion-based eviction is permitted for a request
// that sets force, i.e. the policy enables AllowForceEviction as well.
func (e *Effective) ForceAllowed(requestForce bool) bool {
	return requestForce && e.Spec.AllowForceEviction
}

// ReplacementAllowed reports whether the policy permits node replacement.
func (e *Effective) ReplacementAllowed() bool {
	return e.Spec.AllowNodeReplacement
}

// ApprovalPolicy returns the effective approval policy for a request: the
// request's own policy if set, otherwise the policy default.
func (e *Effective) ApprovalPolicy(reqPolicy v1alpha1.ApprovalPolicy) v1alpha1.ApprovalPolicy {
	return approval.EffectivePolicy(reqPolicy, e.Spec.DefaultApprovalPolicy)
}

// ScopeSelector returns the label selector that limits this policy's scope, or
// labels.Everything() when the policy applies cluster-wide.
func (e *Effective) ScopeSelector() (labels.Selector, error) {
	if e.Spec.NodeSelector == nil {
		return labels.Everything(), nil
	}
	return metav1.LabelSelectorAsSelector(e.Spec.NodeSelector)
}
