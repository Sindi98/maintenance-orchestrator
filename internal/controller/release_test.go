package controller

import (
	"testing"

	"github.com/Sindi98/maintenance-orchestrator/api/v1alpha1"
)

func mkReq(uncordonAfter bool, upgrade *v1alpha1.UpgradeSpec, phases ...v1alpha1.NodePhase) *v1alpha1.MaintenanceRequest {
	mr := &v1alpha1.MaintenanceRequest{}
	mr.Spec.UncordonAfter = uncordonAfter
	mr.Spec.Upgrade = upgrade
	for i, p := range phases {
		mr.Status.Nodes = append(mr.Status.Nodes, v1alpha1.NodeExecutionStatus{
			Node:  string(rune('a'+i)) + "-node",
			Phase: p,
		})
	}
	return mr
}

func TestShouldReleaseNode(t *testing.T) {
	release := map[v1alpha1.NodePhase]bool{
		v1alpha1.NodeCordoning:           true,
		v1alpha1.NodeDraining:            true,
		v1alpha1.NodePostCheck:           true,
		v1alpha1.NodeUncordoning:         true,
		v1alpha1.NodeBlocked:             true,
		v1alpha1.NodePending:             false,
		v1alpha1.NodeReplacing:           false,
		v1alpha1.NodeAwaitingReplacement: false,
		v1alpha1.NodeCompleted:           false,
		v1alpha1.NodeFailed:              false,
		v1alpha1.NodeSkipped:             false,
	}
	for p, want := range release {
		if got := shouldReleaseNode(p); got != want {
			t.Errorf("shouldReleaseNode(%q) = %v, want %v", p, got, want)
		}
	}
}

func TestNodesToRelease(t *testing.T) {
	// UncordonAfter=true: cordoned/draining/blocked nodes are released, others not.
	mr := mkReq(true, nil,
		v1alpha1.NodeDraining, v1alpha1.NodeBlocked, v1alpha1.NodePending, v1alpha1.NodeCompleted)
	got := nodesToRelease(mr)
	if len(got) != 2 {
		t.Fatalf("nodesToRelease returned %v, want 2 nodes (draining+blocked)", got)
	}

	// UncordonAfter=false: never release (the request chose to keep nodes cordoned).
	if got := nodesToRelease(mkReq(false, nil, v1alpha1.NodeDraining)); got != nil {
		t.Errorf("nodesToRelease with UncordonAfter=false = %v, want nil", got)
	}

	// Replacement requests destroy nodes, so there is nothing to return to service.
	up := &v1alpha1.UpgradeSpec{Strategy: v1alpha1.UpgradeReplaceNode}
	if got := nodesToRelease(mkReq(true, up, v1alpha1.NodeDraining, v1alpha1.NodeBlocked)); got != nil {
		t.Errorf("nodesToRelease for a replacement request = %v, want nil", got)
	}
}
