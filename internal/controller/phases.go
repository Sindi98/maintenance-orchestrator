package controller

import (
	"context"
	"fmt"
	"strconv"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/Sindi98/maintenance-orchestrator/api/v1alpha1"
	"github.com/Sindi98/maintenance-orchestrator/internal/approval"
	"github.com/Sindi98/maintenance-orchestrator/internal/audit"
	"github.com/Sindi98/maintenance-orchestrator/internal/metrics"
	"github.com/Sindi98/maintenance-orchestrator/internal/planner"
	"github.com/Sindi98/maintenance-orchestrator/internal/policy"
	"github.com/Sindi98/maintenance-orchestrator/internal/preflight"
)

// reconcileValidating resolves the target, runs preflight, builds the plan and
// routes to Blocked / DryRun-complete / AwaitingApproval / Planned.
func (r *MaintenanceRequestReconciler) reconcileValidating(ctx context.Context, mr *v1alpha1.MaintenanceRequest, pol *policy.Effective) (ctrl.Result, error) {
	mr.Status.Phase = v1alpha1.PhaseValidating

	target, err := r.resolveTarget(ctx, mr)
	if err != nil {
		return r.fail(ctx, mr, "TargetResolution", err.Error())
	}
	if len(target.Nodes) == 0 && len(target.Missing) == 0 {
		setCondition(mr, v1alpha1.CondValidated, metav1.ConditionTrue, "NoTargets", "no nodes matched the target")
		return r.complete(ctx, mr, "no matching nodes; nothing to do")
	}

	results, err := r.preflight.Run(ctx, preflight.Input{
		Request:      mr,
		Policy:       pol,
		Nodes:        target.Nodes,
		Universe:     target.Universe,
		MissingNodes: target.Missing,
		Now:          time.Now(),
	})
	if err != nil {
		return ctrl.Result{}, err
	}
	mr.Status.Preflight = results
	for _, code := range preflight.FailCodes(results) {
		metrics.PreflightFailuresTotal.WithLabelValues(code).Inc()
	}
	r.audit.Record(mr, corev1.EventTypeNormal, audit.ActionPreflightCompleted,
		fmt.Sprintf("preflight completed: %d checks, worst=%s", len(results), preflight.Worst(results)), nil)

	if preflight.HasFail(results) {
		setCondition(mr, v1alpha1.CondGuardrailViolation, metav1.ConditionTrue, "PreflightFailed", "one or more preflight checks failed")
		setCondition(mr, v1alpha1.CondValidated, metav1.ConditionFalse, "PreflightFailed", "blocked by failed preflight checks")
		mr.Status.Phase = v1alpha1.PhaseBlocked
		mr.Status.Message = "blocked by failed preflight checks"
		metrics.BlockedDrainsTotal.WithLabelValues("Preflight").Inc()
		r.audit.Record(mr, corev1.EventTypeWarning, audit.ActionBlocked, "blocked by failed preflight checks", nil)
		if err := r.updateStatus(ctx, mr); err != nil {
			return ctrl.Result{}, err
		}
		r.refreshActiveGauge(ctx)
		return r.requeue(r.Config.GlobalRequeueInterval.Duration), nil
	}
	setCondition(mr, v1alpha1.CondValidated, metav1.ConditionTrue, "Validated", "preflight checks passed")

	plan, err := r.planner.Build(ctx, planner.Input{Request: mr, Policy: pol, Nodes: target.Nodes})
	if err != nil {
		return ctrl.Result{}, err
	}
	mr.Status.Plan = plan
	r.audit.Record(mr, corev1.EventTypeNormal, audit.ActionPlanGenerated, "execution plan generated", planFields(plan))

	// DryRun: the plan is the deliverable; finish.
	if mr.Spec.Mode == v1alpha1.ModeDryRun {
		setCondition(mr, v1alpha1.CondPlanned, metav1.ConditionTrue, "DryRun", "dry-run plan generated")
		return r.complete(ctx, mr, "dry-run completed")
	}

	// Drain approval gate.
	effPolicy := pol.ApprovalPolicy(mr.Spec.Approval.Policy)
	if approval.RequiresGate(effPolicy, v1alpha1.GateDrain) {
		res, decision := approval.Evaluate(effPolicy, mr.Spec.Approval, v1alpha1.GateDrain)
		switch res {
		case approval.Rejected:
			r.recordDecision(mr, decision, audit.ActionApprovalDenied)
			return r.cancel(ctx, mr, "drain rejected at approval gate")
		case approval.Approved:
			r.recordDecision(mr, decision, audit.ActionApprovalGranted)
			setCondition(mr, v1alpha1.CondApproved, metav1.ConditionTrue, "Approved", "drain gate approved")
		default: // Pending
			mr.Status.ApprovalGate = v1alpha1.GateDrain
			mr.Status.Phase = v1alpha1.PhaseAwaitingApproval
			mr.Status.Message = "awaiting drain approval"
			setCondition(mr, v1alpha1.CondApproved, metav1.ConditionFalse, "AwaitingApproval", "drain gate pending")
			r.audit.Record(mr, corev1.EventTypeNormal, audit.ActionApprovalRequested, "awaiting drain approval", map[string]string{"gate": string(v1alpha1.GateDrain)})
			if err := r.updateStatus(ctx, mr); err != nil {
				return ctrl.Result{}, err
			}
			r.refreshActiveGauge(ctx)
			return r.requeue(r.Config.GlobalRequeueInterval.Duration), nil
		}
	}

	mr.Status.Phase = v1alpha1.PhasePlanned
	mr.Status.Message = "validated; planning"
	if err := r.updateStatus(ctx, mr); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{Requeue: true}, nil
}

// reconcileAwaitingApproval waits for the pending gate decision (Drain or Uncordon).
func (r *MaintenanceRequestReconciler) reconcileAwaitingApproval(ctx context.Context, mr *v1alpha1.MaintenanceRequest, pol *policy.Effective) (ctrl.Result, error) {
	gate := mr.Status.ApprovalGate
	if gate == "" {
		gate = v1alpha1.GateDrain
	}
	effPolicy := pol.ApprovalPolicy(mr.Spec.Approval.Policy)
	res, decision := approval.Evaluate(effPolicy, mr.Spec.Approval, gate)

	switch res {
	case approval.Approved:
		r.recordDecision(mr, decision, audit.ActionApprovalGranted)
		setCondition(mr, v1alpha1.CondApproved, metav1.ConditionTrue, "Approved", fmt.Sprintf("%s gate approved", gate))
		mr.Status.ApprovalGate = ""
		mr.Status.Phase = nextPhaseAfterGate(gate)
		mr.Status.Message = fmt.Sprintf("%s approved; proceeding", gate)
	case approval.Rejected:
		r.recordDecision(mr, decision, audit.ActionApprovalDenied)
		return r.cancel(ctx, mr, fmt.Sprintf("%s rejected at approval gate", gate))
	case approval.NotRequired:
		mr.Status.ApprovalGate = ""
		mr.Status.Phase = nextPhaseAfterGate(gate)
		mr.Status.Message = "approval no longer required; proceeding"
	default: // Pending
		return r.requeue(r.Config.GlobalRequeueInterval.Duration), nil
	}

	if err := r.updateStatus(ctx, mr); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{Requeue: true}, nil
}

// reconcilePlanned (re)builds the plan, enforces the window, and starts execution.
func (r *MaintenanceRequestReconciler) reconcilePlanned(ctx context.Context, mr *v1alpha1.MaintenanceRequest, pol *policy.Effective) (ctrl.Result, error) {
	target, err := r.resolveTarget(ctx, mr)
	if err != nil {
		return r.fail(ctx, mr, "TargetResolution", err.Error())
	}
	plan, err := r.planner.Build(ctx, planner.Input{Request: mr, Policy: pol, Nodes: target.Nodes})
	if err != nil {
		return ctrl.Result{}, err
	}
	mr.Status.Plan = plan
	setCondition(mr, v1alpha1.CondPlanned, metav1.ConditionTrue, "Planned",
		fmt.Sprintf("plan: %d node(s), risk %d/100", plan.TotalNodes, plan.RiskScore))

	// Advisory never mutates: refresh and keep advising.
	if mr.Spec.Mode == v1alpha1.ModeAdvisory {
		mr.Status.Message = "advisory: plan refreshed (no mutation)"
		if err := r.updateStatus(ctx, mr); err != nil {
			return ctrl.Result{}, err
		}
		return r.requeue(r.Config.GlobalRequeueInterval.Duration), nil
	}

	if plan.TotalNodes == 0 {
		return r.complete(ctx, mr, "no matching nodes; nothing to do")
	}

	// Maintenance window gate.
	open, err := preflight.WindowsOpen(mr, pol.Spec, time.Now())
	if err != nil {
		return r.fail(ctx, mr, "InvalidWindow", err.Error())
	}
	if !open {
		setCondition(mr, v1alpha1.CondWindowOpen, metav1.ConditionFalse, "WindowClosed", "outside maintenance window")
		mr.Status.Message = "waiting for maintenance window"
		if err := r.updateStatus(ctx, mr); err != nil {
			return ctrl.Result{}, err
		}
		return r.requeue(r.Config.GlobalRequeueInterval.Duration), nil
	}
	setCondition(mr, v1alpha1.CondWindowOpen, metav1.ConditionTrue, "WindowOpen", "within maintenance window")

	if len(mr.Status.Nodes) == 0 {
		mr.Status.Nodes = initNodeStatuses(plan)
	}
	now := metav1.Now()
	if mr.Status.StartTime == nil {
		mr.Status.StartTime = &now
	}
	mr.Status.Phase = v1alpha1.PhaseExecuting
	mr.Status.Message = "executing"
	setCondition(mr, v1alpha1.CondExecuting, metav1.ConditionTrue, "Executing", "draining nodes")
	recomputeSummary(mr)
	if err := r.updateStatus(ctx, mr); err != nil {
		return ctrl.Result{}, err
	}
	r.refreshActiveGauge(ctx)
	return ctrl.Result{Requeue: true}, nil
}

// reconcilePaused holds the request until spec.pause clears, then resumes.
func (r *MaintenanceRequestReconciler) reconcilePaused(ctx context.Context, mr *v1alpha1.MaintenanceRequest) (ctrl.Result, error) {
	if mr.Spec.Pause {
		return r.requeue(r.Config.GlobalRequeueInterval.Duration), nil
	}
	r.audit.Record(mr, corev1.EventTypeNormal, audit.ActionResumed, "request resumed", nil)
	switch {
	case mr.Status.Plan != nil && len(mr.Status.Nodes) > 0:
		mr.Status.Phase = v1alpha1.PhaseExecuting
	case mr.Status.Plan != nil:
		mr.Status.Phase = v1alpha1.PhasePlanned
	default:
		mr.Status.Phase = v1alpha1.PhaseValidating
	}
	mr.Status.Message = "resumed"
	if err := r.updateStatus(ctx, mr); err != nil {
		return ctrl.Result{}, err
	}
	r.refreshActiveGauge(ctx)
	return ctrl.Result{Requeue: true}, nil
}

// reconcileBlocked retries execution-time blocks (within the global timeout) or
// re-validates a preflight-time block.
func (r *MaintenanceRequestReconciler) reconcileBlocked(ctx context.Context, mr *v1alpha1.MaintenanceRequest, pol *policy.Effective) (ctrl.Result, error) {
	_ = pol
	if r.globalTimedOut(mr) {
		return r.fail(ctx, mr, "Timeout", "global timeout exceeded while blocked")
	}

	if mr.Status.Plan != nil && len(mr.Status.Nodes) > 0 {
		var retried int
		now := metav1.Now()
		for i := range mr.Status.Nodes {
			ns := &mr.Status.Nodes[i]
			if ns.Phase == v1alpha1.NodeBlocked {
				ns.Phase = v1alpha1.NodeDraining
				ns.BlockReason = ""
				ns.Message = "retrying after block"
				ns.StartTime = &now
				ns.EndTime = nil
				retried++
			}
		}
		if retried == 0 {
			return r.requeue(r.Config.GlobalRequeueInterval.Duration), nil
		}
		mr.Status.Phase = v1alpha1.PhaseExecuting
		mr.Status.Message = fmt.Sprintf("retrying %d blocked node(s)", retried)
		setCondition(mr, v1alpha1.CondBlocked, metav1.ConditionFalse, "Retrying", mr.Status.Message)
		recomputeSummary(mr)
		if err := r.updateStatus(ctx, mr); err != nil {
			return ctrl.Result{}, err
		}
		return r.requeue(r.Config.EvictionPollInterval.Duration), nil
	}

	mr.Status.Phase = v1alpha1.PhaseValidating
	mr.Status.Message = "re-validating after block"
	if err := r.updateStatus(ctx, mr); err != nil {
		return ctrl.Result{}, err
	}
	return r.requeue(r.Config.GlobalRequeueInterval.Duration), nil
}

// nextPhaseAfterGate returns the phase to resume into once a gate clears.
func nextPhaseAfterGate(gate v1alpha1.Gate) v1alpha1.Phase {
	if gate == v1alpha1.GateUncordon {
		return v1alpha1.PhaseExecuting
	}
	return v1alpha1.PhasePlanned
}

// recordDecision audits a recorded approval decision.
func (r *MaintenanceRequestReconciler) recordDecision(mr *v1alpha1.MaintenanceRequest, decision *v1alpha1.GateDecision, action string) {
	if decision == nil {
		return
	}
	fields := map[string]string{"gate": string(decision.Gate), "decision": string(decision.Decision)}
	if decision.ApprovedBy != "" {
		fields["by"] = decision.ApprovedBy
	}
	if decision.Reason != "" {
		fields["reason"] = decision.Reason
	}
	r.audit.Record(mr, corev1.EventTypeNormal, action, fmt.Sprintf("%s gate %s", decision.Gate, decision.Decision), fields)
}

// planFields renders an ExecutionPlan as audit fields.
func planFields(plan *v1alpha1.ExecutionPlan) map[string]string {
	if plan == nil {
		return nil
	}
	return map[string]string{
		"strategy":      string(plan.Strategy),
		"totalNodes":    strconv.Itoa(int(plan.TotalNodes)),
		"batches":       strconv.Itoa(len(plan.Batches)),
		"maxConcurrent": strconv.Itoa(int(plan.MaxConcurrent)),
		"riskScore":     strconv.Itoa(int(plan.RiskScore)),
		"podsToEvict":   strconv.Itoa(int(plan.Impact.PodsToEvict)),
	}
}

