package controller

import (
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/Sindi98/maintenance-orchestrator/api/v1alpha1"
)

// TestResumePhase verifies the phase a paused request resumes into, and in
// particular that a request paused while an approval gate is pending resumes
// back into AwaitingApproval so the gate cannot be skipped by pause/resume.
func TestResumePhase(t *testing.T) {
	plan := &v1alpha1.ExecutionPlan{TotalNodes: 1}
	nodes := []v1alpha1.NodeExecutionStatus{{Node: "n1", Phase: v1alpha1.NodePending}}

	cases := []struct {
		name string
		st   v1alpha1.MaintenanceStatus
		want v1alpha1.Phase
	}{
		{
			name: "pending drain gate resumes to AwaitingApproval (no bypass)",
			st:   v1alpha1.MaintenanceStatus{ApprovalGate: v1alpha1.GateDrain, Plan: plan},
			want: v1alpha1.PhaseAwaitingApproval,
		},
		{
			name: "pending uncordon gate mid-execution resumes to AwaitingApproval",
			st:   v1alpha1.MaintenanceStatus{ApprovalGate: v1alpha1.GateUncordon, Plan: plan, Nodes: nodes},
			want: v1alpha1.PhaseAwaitingApproval,
		},
		{
			name: "executing (no gate) resumes to Executing",
			st:   v1alpha1.MaintenanceStatus{Plan: plan, Nodes: nodes},
			want: v1alpha1.PhaseExecuting,
		},
		{
			name: "planned (no gate, no nodes) resumes to Planned",
			st:   v1alpha1.MaintenanceStatus{Plan: plan},
			want: v1alpha1.PhasePlanned,
		},
		{
			name: "no plan resumes to Validating",
			st:   v1alpha1.MaintenanceStatus{},
			want: v1alpha1.PhaseValidating,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mr := &v1alpha1.MaintenanceRequest{
				ObjectMeta: metav1.ObjectMeta{Name: "r"},
				Status:     tc.st,
			}
			if got := resumePhase(mr); got != tc.want {
				t.Errorf("resumePhase() = %q, want %q", got, tc.want)
			}
		})
	}
}
