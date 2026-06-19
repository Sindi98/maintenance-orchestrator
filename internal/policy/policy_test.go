package policy_test

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/Sindi98/maintenance-orchestrator/api/v1alpha1"
	"github.com/Sindi98/maintenance-orchestrator/internal/policy"
)

func eff(spec v1alpha1.MaintenancePolicySpec) *policy.Effective {
	return &policy.Effective{Spec: policy.WithDefaults(spec)}
}

func TestWithDefaults(t *testing.T) {
	got := policy.WithDefaults(v1alpha1.MaintenancePolicySpec{})
	if got.MaxConcurrentDrains != 1 {
		t.Errorf("maxConcurrentDrains default = %d, want 1", got.MaxConcurrentDrains)
	}
	if got.FailureThreshold != 1 {
		t.Errorf("failureThreshold default = %d, want 1", got.FailureThreshold)
	}
	if got.DefaultApprovalPolicy != v1alpha1.ApprovalAuto {
		t.Errorf("approval default = %q, want AutoApprove", got.DefaultApprovalPolicy)
	}
	if len(got.ControlPlaneNodeLabels) == 0 {
		t.Error("control-plane node labels default missing")
	}

	in := v1alpha1.MaintenancePolicySpec{MaxConcurrentDrains: 5, FailureThreshold: 3}
	out := policy.WithDefaults(in)
	if out.MaxConcurrentDrains != 5 || out.FailureThreshold != 3 {
		t.Errorf("existing values overwritten: %+v", out)
	}
}

func TestControlPlaneBlocked(t *testing.T) {
	protected := eff(v1alpha1.MaintenancePolicySpec{ProtectControlPlane: true})
	if !protected.ControlPlaneBlocked(false) {
		t.Error("protected + no opt-in must block")
	}
	if !protected.ControlPlaneBlocked(true) {
		t.Error("protected + opt-in must STILL block (double gate)")
	}

	open := eff(v1alpha1.MaintenancePolicySpec{ProtectControlPlane: false})
	if !open.ControlPlaneBlocked(false) {
		t.Error("not opted in must block")
	}
	if open.ControlPlaneBlocked(true) {
		t.Error("not protected + opt-in must be allowed")
	}
}

func TestMaxUnavailable(t *testing.T) {
	pct := eff(v1alpha1.MaintenancePolicySpec{MaxUnavailablePercent: 20})
	if got := pct.MaxUnavailable(10); got != 2 {
		t.Errorf("20%% of 10 = %d, want 2", got)
	}
	if got := pct.MaxUnavailable(3); got != 1 {
		t.Errorf("20%% of 3 = %d, want 1 (floor, min 1)", got)
	}

	both := eff(v1alpha1.MaintenancePolicySpec{MaxUnavailableNodes: 1, MaxUnavailablePercent: 50})
	if got := both.MaxUnavailable(10); got != 1 {
		t.Errorf("most restrictive cap = %d, want 1", got)
	}

	none := eff(v1alpha1.MaintenancePolicySpec{})
	if got := none.MaxUnavailable(7); got != 7 {
		t.Errorf("no caps = %d, want 7 (total)", got)
	}
	if none.UnavailabilityCapped() {
		t.Error("no caps must report UnavailabilityCapped()=false")
	}
}

func TestConcurrencyAndForce(t *testing.T) {
	e := eff(v1alpha1.MaintenancePolicySpec{MaxConcurrentDrains: 2})
	if got := e.Concurrency(5); got != 2 {
		t.Errorf("concurrency capped = %d, want 2", got)
	}
	if got := e.Concurrency(0); got != 1 {
		t.Errorf("concurrency min = %d, want 1", got)
	}

	deny := eff(v1alpha1.MaintenancePolicySpec{AllowForceEviction: false})
	if deny.ForceAllowed(true) {
		t.Error("force must be denied without policy permission")
	}
	allow := eff(v1alpha1.MaintenancePolicySpec{AllowForceEviction: true})
	if !allow.ForceAllowed(true) {
		t.Error("force must be allowed when request and policy agree")
	}
	if allow.ForceAllowed(false) {
		t.Error("force off in request must stay off")
	}
}

func TestReservedAndControlPlaneDetection(t *testing.T) {
	e := eff(v1alpha1.MaintenancePolicySpec{
		ReservedNodeLabels: []string{"team/reserved"},
		ReservedTaints:     []string{"dedicated"},
	})
	cp := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"node-role.kubernetes.io/control-plane": ""}}}
	if !e.IsControlPlaneNode(cp) {
		t.Error("expected control-plane detection")
	}
	rl := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"team/reserved": "x"}}}
	if k, ok := e.ReservedLabel(rl); !ok || k != "team/reserved" {
		t.Errorf("reserved label = %q,%v", k, ok)
	}
	rt := &corev1.Node{Spec: corev1.NodeSpec{Taints: []corev1.Taint{{Key: "dedicated", Effect: corev1.TaintEffectNoSchedule}}}}
	if k, ok := e.ReservedTaint(rt); !ok || k != "dedicated" {
		t.Errorf("reserved taint = %q,%v", k, ok)
	}
}

func TestApprovalPolicyResolution(t *testing.T) {
	e := eff(v1alpha1.MaintenancePolicySpec{DefaultApprovalPolicy: v1alpha1.ApprovalManualBeforeDrain})
	if got := e.ApprovalPolicy(""); got != v1alpha1.ApprovalManualBeforeDrain {
		t.Errorf("policy default used = %q, want ManualBeforeDrain", got)
	}
	if got := e.ApprovalPolicy(v1alpha1.ApprovalManualBeforeBoth); got != v1alpha1.ApprovalManualBeforeBoth {
		t.Errorf("request override = %q, want ManualBeforeBoth", got)
	}
}
