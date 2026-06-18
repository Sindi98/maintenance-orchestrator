// Package approval implements the manual-approval gate logic as pure functions,
// so it can be unit-tested without a cluster. The reconciler resolves the
// effective approval policy (request override or cluster default) and asks this
// package what to do at the current gate (Drain or Uncordon).
package approval

import (
	"github.com/Sindi98/maintenance-orchestrator/api/v1alpha1"
)

// Result is the outcome of evaluating a gate.
type Result string

const (
	// NotRequired means the effective policy does not gate this checkpoint.
	NotRequired Result = "NotRequired"
	// Pending means a decision is required but none has been recorded yet.
	Pending Result = "Pending"
	// Approved means the gate has an Approved decision.
	Approved Result = "Approved"
	// Rejected means the gate has a Rejected decision.
	Rejected Result = "Rejected"
)

// EffectivePolicy returns the request's approval policy if set, otherwise the
// cluster default.
func EffectivePolicy(specPolicy, defaultPolicy v1alpha1.ApprovalPolicy) v1alpha1.ApprovalPolicy {
	if specPolicy != "" {
		return specPolicy
	}
	if defaultPolicy != "" {
		return defaultPolicy
	}
	return v1alpha1.ApprovalAuto
}

// RequiresGate reports whether the given policy gates the given checkpoint.
func RequiresGate(policy v1alpha1.ApprovalPolicy, gate v1alpha1.Gate) bool {
	switch gate {
	case v1alpha1.GateDrain:
		return policy == v1alpha1.ApprovalManualBeforeDrain || policy == v1alpha1.ApprovalManualBeforeBoth
	case v1alpha1.GateUncordon:
		return policy == v1alpha1.ApprovalManualBeforeUncordon || policy == v1alpha1.ApprovalManualBeforeBoth
	default:
		return false
	}
}

// DecisionFor returns the most recent recorded decision for the given gate, if any.
func DecisionFor(spec v1alpha1.ApprovalSpec, gate v1alpha1.Gate) (*v1alpha1.GateDecision, bool) {
	var found *v1alpha1.GateDecision
	for i := range spec.Gates {
		g := &spec.Gates[i]
		if g.Gate != gate {
			continue
		}
		if found == nil {
			found = g
			continue
		}
		// Prefer the decision with the later timestamp; treat a nil time as oldest.
		if g.Time != nil && (found.Time == nil || g.Time.After(found.Time.Time)) {
			found = g
		}
	}
	return found, found != nil
}

// Evaluate returns the gate result for the effective policy and the recorded
// decisions. When a decision exists, it also returns the matching GateDecision.
func Evaluate(policy v1alpha1.ApprovalPolicy, spec v1alpha1.ApprovalSpec, gate v1alpha1.Gate) (Result, *v1alpha1.GateDecision) {
	if !RequiresGate(policy, gate) {
		return NotRequired, nil
	}
	decision, ok := DecisionFor(spec, gate)
	if !ok {
		return Pending, nil
	}
	switch decision.Decision {
	case v1alpha1.DecisionApproved:
		return Approved, decision
	case v1alpha1.DecisionRejected:
		return Rejected, decision
	default:
		// An unrecognized/empty decision value is treated as still pending.
		return Pending, decision
	}
}
