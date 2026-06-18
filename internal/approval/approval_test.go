package approval_test

import (
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/Sindi98/maintenance-orchestrator/api/v1alpha1"
	"github.com/Sindi98/maintenance-orchestrator/internal/approval"
)

func TestEffectivePolicy(t *testing.T) {
	if got := approval.EffectivePolicy(v1alpha1.ApprovalManualBeforeDrain, v1alpha1.ApprovalAuto); got != v1alpha1.ApprovalManualBeforeDrain {
		t.Errorf("request policy should win, got %q", got)
	}
	if got := approval.EffectivePolicy("", v1alpha1.ApprovalManualBeforeBoth); got != v1alpha1.ApprovalManualBeforeBoth {
		t.Errorf("default policy should apply, got %q", got)
	}
	if got := approval.EffectivePolicy("", ""); got != v1alpha1.ApprovalAuto {
		t.Errorf("fallback should be AutoApprove, got %q", got)
	}
}

func TestRequiresGate(t *testing.T) {
	cases := []struct {
		policy v1alpha1.ApprovalPolicy
		gate   v1alpha1.Gate
		want   bool
	}{
		{v1alpha1.ApprovalAuto, v1alpha1.GateDrain, false},
		{v1alpha1.ApprovalAuto, v1alpha1.GateUncordon, false},
		{v1alpha1.ApprovalManualBeforeDrain, v1alpha1.GateDrain, true},
		{v1alpha1.ApprovalManualBeforeDrain, v1alpha1.GateUncordon, false},
		{v1alpha1.ApprovalManualBeforeUncordon, v1alpha1.GateDrain, false},
		{v1alpha1.ApprovalManualBeforeUncordon, v1alpha1.GateUncordon, true},
		{v1alpha1.ApprovalManualBeforeBoth, v1alpha1.GateDrain, true},
		{v1alpha1.ApprovalManualBeforeBoth, v1alpha1.GateUncordon, true},
	}
	for _, c := range cases {
		if got := approval.RequiresGate(c.policy, c.gate); got != c.want {
			t.Errorf("RequiresGate(%q,%q) = %v, want %v", c.policy, c.gate, got, c.want)
		}
	}
}

func TestEvaluate(t *testing.T) {
	// AutoApprove never gates.
	if res, dec := approval.Evaluate(v1alpha1.ApprovalAuto, v1alpha1.ApprovalSpec{}, v1alpha1.GateDrain); res != approval.NotRequired || dec != nil {
		t.Errorf("AutoApprove: got (%q, %v), want (NotRequired, nil)", res, dec)
	}

	// Required but undecided -> Pending.
	spec := v1alpha1.ApprovalSpec{Policy: v1alpha1.ApprovalManualBeforeDrain}
	if res, _ := approval.Evaluate(v1alpha1.ApprovalManualBeforeDrain, spec, v1alpha1.GateDrain); res != approval.Pending {
		t.Errorf("undecided: got %q, want Pending", res)
	}

	// Approved decision.
	spec.Gates = []v1alpha1.GateDecision{{Gate: v1alpha1.GateDrain, Decision: v1alpha1.DecisionApproved, ApprovedBy: "alice"}}
	if res, dec := approval.Evaluate(v1alpha1.ApprovalManualBeforeDrain, spec, v1alpha1.GateDrain); res != approval.Approved || dec == nil {
		t.Errorf("approved: got (%q, %v), want (Approved, non-nil)", res, dec)
	}

	// Rejected decision.
	spec.Gates = []v1alpha1.GateDecision{{Gate: v1alpha1.GateDrain, Decision: v1alpha1.DecisionRejected}}
	if res, _ := approval.Evaluate(v1alpha1.ApprovalManualBeforeDrain, spec, v1alpha1.GateDrain); res != approval.Rejected {
		t.Errorf("rejected: got %q, want Rejected", res)
	}

	// A decision for a different gate is ignored.
	spec.Gates = []v1alpha1.GateDecision{{Gate: v1alpha1.GateUncordon, Decision: v1alpha1.DecisionApproved}}
	if res, _ := approval.Evaluate(v1alpha1.ApprovalManualBeforeDrain, spec, v1alpha1.GateDrain); res != approval.Pending {
		t.Errorf("other-gate decision: got %q, want Pending", res)
	}
}

func TestDecisionForLatestWins(t *testing.T) {
	older := metav1.NewTime(time.Now().Add(-time.Hour))
	newer := metav1.NewTime(time.Now())
	spec := v1alpha1.ApprovalSpec{
		Gates: []v1alpha1.GateDecision{
			{Gate: v1alpha1.GateDrain, Decision: v1alpha1.DecisionRejected, Time: &older},
			{Gate: v1alpha1.GateDrain, Decision: v1alpha1.DecisionApproved, Time: &newer},
		},
	}
	dec, ok := approval.DecisionFor(spec, v1alpha1.GateDrain)
	if !ok {
		t.Fatal("expected a decision")
	}
	if dec.Decision != v1alpha1.DecisionApproved {
		t.Errorf("latest decision = %q, want Approved", dec.Decision)
	}
}

func TestDecisionForNone(t *testing.T) {
	if _, ok := approval.DecisionFor(v1alpha1.ApprovalSpec{}, v1alpha1.GateDrain); ok {
		t.Error("expected no decision for empty spec")
	}
}

