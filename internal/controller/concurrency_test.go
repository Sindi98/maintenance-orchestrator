package controller

import (
	"context"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/Sindi98/maintenance-orchestrator/api/v1alpha1"
)

func mkExecuting(name string, phase v1alpha1.Phase, nodePhases ...v1alpha1.NodePhase) *v1alpha1.MaintenanceRequest {
	mr := &v1alpha1.MaintenanceRequest{ObjectMeta: metav1.ObjectMeta{Name: name}}
	mr.Status.Phase = phase
	for i, p := range nodePhases {
		mr.Status.Nodes = append(mr.Status.Nodes, v1alpha1.NodeExecutionStatus{
			Node:  name + "-" + string(rune('a'+i)),
			Phase: p,
		})
	}
	return mr
}

func TestGlobalDrainsInFlightExcept(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}

	// r1: 2 in-flight (Draining + Cordoning), 1 Completed (not counted).
	r1 := mkExecuting("r1", v1alpha1.PhaseExecuting, v1alpha1.NodeDraining, v1alpha1.NodeCordoning, v1alpha1.NodeCompleted)
	// r2: 1 in-flight (Replacing).
	r2 := mkExecuting("r2", v1alpha1.PhaseExecuting, v1alpha1.NodeReplacing)
	// r3: terminal phase — its in-flight-looking node must be ignored entirely.
	r3 := mkExecuting("r3", v1alpha1.PhaseCompleted, v1alpha1.NodeDraining)

	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(r1, r2, r3).Build()
	r := &MaintenanceRequestReconciler{Client: cl}
	ctx := context.Background()

	// From r1's perspective, only r2 contributes (r1 excluded as self, r3 terminal).
	if got := r.globalDrainsInFlightExcept(ctx, "r1"); got != 1 {
		t.Errorf("globalDrainsInFlightExcept(r1) = %d, want 1 (r2 only)", got)
	}
	// From r2's perspective, r1 contributes its 2 in-flight nodes.
	if got := r.globalDrainsInFlightExcept(ctx, "r2"); got != 2 {
		t.Errorf("globalDrainsInFlightExcept(r2) = %d, want 2 (r1 only)", got)
	}
}
