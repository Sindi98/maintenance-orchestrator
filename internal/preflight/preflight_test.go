package preflight_test

import (
	"context"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/Sindi98/maintenance-orchestrator/api/v1alpha1"
	"github.com/Sindi98/maintenance-orchestrator/internal/kube"
	"github.com/Sindi98/maintenance-orchestrator/internal/policy"
	"github.com/Sindi98/maintenance-orchestrator/internal/preflight"
)

func mkNode(name string, labels map[string]string, ready, cordoned bool) *corev1.Node {
	status := corev1.ConditionTrue
	if !ready {
		status = corev1.ConditionFalse
	}
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: name, Labels: labels},
		Spec:       corev1.NodeSpec{Unschedulable: cordoned},
		Status: corev1.NodeStatus{
			Conditions: []corev1.NodeCondition{{Type: corev1.NodeReady, Status: status}},
		},
	}
}

func mkPod(ns, name, nodeName string, labels map[string]string, emptyDir bool) *corev1.Pod {
	p := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name, Labels: labels},
		Spec: corev1.PodSpec{
			NodeName:   nodeName,
			Containers: []corev1.Container{{Name: "c"}},
		},
		Status: corev1.PodStatus{Phase: corev1.PodRunning},
	}
	if emptyDir {
		p.Spec.Volumes = []corev1.Volume{{
			Name:         "data",
			VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}},
		}}
	}
	return p
}

func newEngine(t *testing.T, objs ...client.Object) *preflight.Engine {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		t.Fatalf("add scheme: %v", err)
	}
	cl := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(objs...).
		WithIndex(&corev1.Pod{}, kube.IndexPodNodeName, func(o client.Object) []string {
			pod, ok := o.(*corev1.Pod)
			if !ok || pod.Spec.NodeName == "" {
				return nil
			}
			return []string{pod.Spec.NodeName}
		}).
		Build()
	return preflight.NewEngine(kube.New(cl))
}

func mustCode(t *testing.T, results []v1alpha1.PreflightCheckResult, code string, want v1alpha1.CheckStatus) {
	t.Helper()
	for i := range results {
		if results[i].Code == code {
			if results[i].Status != want {
				t.Errorf("code %s: status = %s, want %s", code, results[i].Status, want)
			}
			return
		}
	}
	t.Errorf("expected preflight code %s, not found in %d results", code, len(results))
}

func TestPreflightChecks(t *testing.T) {
	cp := mkNode("cp-1", map[string]string{"node-role.kubernetes.io/control-plane": ""}, true, false)
	w1 := mkNode("worker-1", nil, true, false)
	w2 := mkNode("worker-2", nil, false, true) // not Ready + cordoned
	wr := mkNode("worker-reserved", map[string]string{"team/reserved": "yes"}, true, false)

	podED := mkPod("default", "app-emptydir", "worker-1", map[string]string{"app": "cache"}, true)
	podPDB := mkPod("default", "app-web", "worker-1", map[string]string{"app": "web"}, false)
	pdb := &policyv1.PodDisruptionBudget{
		ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "web-pdb"},
		Spec: policyv1.PodDisruptionBudgetSpec{
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "web"}},
		},
		Status: policyv1.PodDisruptionBudgetStatus{DisruptionsAllowed: 0},
	}

	eng := newEngine(t, cp, w1, w2, wr, podED, podPDB, pdb)

	pol := &policy.Effective{Spec: policy.WithDefaults(v1alpha1.MaintenancePolicySpec{
		ProtectControlPlane:    true,
		ControlPlaneNodeLabels: []string{"node-role.kubernetes.io/control-plane"},
		ReservedNodeLabels:     []string{"team/reserved"},
	})}
	mr := &v1alpha1.MaintenanceRequest{
		Spec: v1alpha1.MaintenanceSpec{
			Mode:              v1alpha1.ModeExecute,
			AllowControlPlane: false,
			Approval:          v1alpha1.ApprovalSpec{Policy: v1alpha1.ApprovalAuto},
		},
	}
	nodes := []corev1.Node{*cp, *w1, *w2, *wr}

	results, err := eng.Run(context.Background(), preflight.Input{
		Request:  mr,
		Policy:   pol,
		Nodes:    nodes,
		Universe: nodes,
		Now:      time.Now(),
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	mustCode(t, results, v1alpha1.CodeControlPlane, v1alpha1.CheckFail)
	mustCode(t, results, v1alpha1.CodeReservedLabel, v1alpha1.CheckFail)
	mustCode(t, results, v1alpha1.CodeNodeNotReady, v1alpha1.CheckWarn)
	mustCode(t, results, v1alpha1.CodeAlreadyCordoned, v1alpha1.CheckWarn)
	mustCode(t, results, v1alpha1.CodeEmptyDir, v1alpha1.CheckWarn)
	mustCode(t, results, v1alpha1.CodeSingleReplica, v1alpha1.CheckWarn)
	mustCode(t, results, v1alpha1.CodePDBBlocks, v1alpha1.CheckWarn)

	if !preflight.HasFail(results) {
		t.Error("HasFail = false, want true")
	}
	if got := preflight.Worst(results); got != v1alpha1.CheckFail {
		t.Errorf("Worst = %s, want Fail", got)
	}

	failCodes := preflight.FailCodes(results)
	if len(failCodes) < 2 {
		t.Errorf("FailCodes = %v, want at least control-plane and reserved-label", failCodes)
	}
}

func TestPreflightMissingNode(t *testing.T) {
	eng := newEngine(t)
	pol := &policy.Effective{Spec: policy.WithDefaults(v1alpha1.MaintenancePolicySpec{})}
	mr := &v1alpha1.MaintenanceRequest{Spec: v1alpha1.MaintenanceSpec{Mode: v1alpha1.ModeExecute}}

	results, err := eng.Run(context.Background(), preflight.Input{
		Request:      mr,
		Policy:       pol,
		MissingNodes: []string{"ghost"},
		Now:          time.Now(),
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	mustCode(t, results, v1alpha1.CodeNodeNotFound, v1alpha1.CheckFail)
}

func TestWindowsOpen(t *testing.T) {
	// A request window firing every minute with a one-hour duration is open now.
	mr := &v1alpha1.MaintenanceRequest{Spec: v1alpha1.MaintenanceSpec{
		MaintenanceWindow: &v1alpha1.Window{
			Cron:     "* * * * *",
			Duration: metav1.Duration{Duration: time.Hour},
		},
	}}
	open, err := preflight.WindowsOpen(mr, v1alpha1.MaintenancePolicySpec{}, time.Now())
	if err != nil {
		t.Fatalf("WindowsOpen: %v", err)
	}
	if !open {
		t.Error("expected window open")
	}

	// No windows anywhere => always open.
	open2, err := preflight.WindowsOpen(&v1alpha1.MaintenanceRequest{}, v1alpha1.MaintenancePolicySpec{}, time.Now())
	if err != nil {
		t.Fatalf("WindowsOpen (none): %v", err)
	}
	if !open2 {
		t.Error("no windows should be always open")
	}
}

func hasCode(results []v1alpha1.PreflightCheckResult, code string) bool {
	for i := range results {
		if results[i].Code == code {
			return true
		}
	}
	return false
}

// TestUnavailabilityConcurrencyAware checks that a one-at-a-time rolling drain of
// a whole pool (uncordonAfter=true, maxConcurrent=1) is NOT blocked by a cap that
// only allows one unavailable node, because the peak simultaneous unavailability
// is the concurrency, not the whole scope. The same request WITHOUT uncordon is
// evaluated by the whole scope and IS blocked.
func TestUnavailabilityConcurrencyAware(t *testing.T) {
	w1 := mkNode("w1", nil, true, false)
	w2 := mkNode("w2", nil, true, false)
	w3 := mkNode("w3", nil, true, false)
	nodes := []corev1.Node{*w1, *w2, *w3}
	eng := newEngine(t, w1, w2, w3)

	// 33% of 3 nodes => limit 1.
	pol := &policy.Effective{Spec: policy.WithDefaults(v1alpha1.MaintenancePolicySpec{
		MaxUnavailablePercent: 33,
	})}
	if got := pol.MaxUnavailable(3); got != 1 {
		t.Fatalf("precondition: MaxUnavailable(3) = %d, want 1", got)
	}

	run := func(uncordon bool, concurrency int32) []v1alpha1.PreflightCheckResult {
		mr := &v1alpha1.MaintenanceRequest{Spec: v1alpha1.MaintenanceSpec{
			Mode:          v1alpha1.ModeExecute,
			Strategy:      v1alpha1.StrategySerial,
			MaxConcurrent: concurrency,
			UncordonAfter: uncordon,
			Approval:      v1alpha1.ApprovalSpec{Policy: v1alpha1.ApprovalAuto},
		}}
		results, err := eng.Run(context.Background(), preflight.Input{
			Request: mr, Policy: pol, Nodes: nodes, Universe: nodes, Now: time.Now(),
		})
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		return results
	}

	// Rolling, one at a time, returning nodes to service: peak unavailability is 1
	// (the concurrency), within the limit of 1 => no TOO_MANY_UNAVAILABLE.
	if got := run(true, 1); hasCode(got, v1alpha1.CodeTooManyUnavailable) {
		t.Error("serial rolling drain with uncordon must not be blocked by the unavailability cap")
	}

	// Same scope but the nodes stay cordoned: they accumulate to 3/3 unavailable,
	// exceeding the limit of 1 => blocked.
	res := run(false, 1)
	if !hasCode(res, v1alpha1.CodeTooManyUnavailable) {
		t.Error("non-uncordoning whole-pool drain must trip the unavailability cap")
	}
	mustCode(t, res, v1alpha1.CodeTooManyUnavailable, v1alpha1.CheckFail)
}
