package controller

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/Sindi98/maintenance-orchestrator/api/v1alpha1"
	"github.com/Sindi98/maintenance-orchestrator/internal/approval"
	"github.com/Sindi98/maintenance-orchestrator/internal/audit"
	"github.com/Sindi98/maintenance-orchestrator/internal/executor"
	"github.com/Sindi98/maintenance-orchestrator/internal/kube"
	"github.com/Sindi98/maintenance-orchestrator/internal/metrics"
	"github.com/Sindi98/maintenance-orchestrator/internal/policy"
	"github.com/Sindi98/maintenance-orchestrator/internal/preflight"
)

// reconcileExecuting advances the current batch by one step, honoring global
// timeout, failure threshold, the uncordon gate, the window and concurrency.
func (r *MaintenanceRequestReconciler) reconcileExecuting(ctx context.Context, mr *v1alpha1.MaintenanceRequest, pol *policy.Effective) (ctrl.Result, error) {
	if r.globalTimedOut(mr) {
		return r.fail(ctx, mr, "Timeout", "global timeout exceeded")
	}

	plan := mr.Status.Plan
	if plan == nil {
		return r.fail(ctx, mr, "NoPlan", "execution plan missing")
	}

	failureThreshold := pol.Spec.FailureThreshold
	if failureThreshold < 1 {
		failureThreshold = 1
	}
	if countNodePhase(mr, v1alpha1.NodeFailed) >= failureThreshold {
		return r.fail(ctx, mr, "FailureThreshold",
			fmt.Sprintf("%d node failure(s) reached threshold %d", countNodePhase(mr, v1alpha1.NodeFailed), failureThreshold))
	}

	// Uncordon gate: hold the whole request as soon as a node is drained and
	// waiting to be returned to service, until the gate is approved.
	// Replacement requests destroy the node instead of returning it, so the
	// uncordon gate does not apply to them.
	effPolicy := pol.ApprovalPolicy(mr.Spec.Approval.Policy)
	uncordonGated := approval.RequiresGate(effPolicy, v1alpha1.GateUncordon) && !mr.Spec.ReplaceNodes()
	if uncordonGated && !uncordonApproved(mr, pol) && anyNodeAtPostCheck(mr) {
		mr.Status.ApprovalGate = v1alpha1.GateUncordon
		mr.Status.Phase = v1alpha1.PhaseAwaitingApproval
		mr.Status.Message = "awaiting uncordon approval"
		setCondition(mr, v1alpha1.CondApproved, metav1.ConditionFalse, "AwaitingApproval", "uncordon gate pending")
		r.audit.Record(mr, corev1.EventTypeNormal, audit.ActionApprovalRequested, "awaiting uncordon approval",
			map[string]string{"gate": string(v1alpha1.GateUncordon)})
		if err := r.updateStatus(ctx, mr); err != nil {
			return ctrl.Result{}, err
		}
		r.refreshActiveGauge(ctx)
		return r.requeue(r.Config.GlobalRequeueInterval.Duration), nil
	}

	// The window keeps gating the START of new nodes; in-flight nodes finish.
	windowOpen, werr := preflight.WindowsOpen(mr, pol.Spec, time.Now())
	if werr != nil {
		// Treat an unparseable window as closed so in-flight nodes still finish,
		// but surface it: reconcilePlanned fails the request on the same error, so
		// silently swallowing it would hide the misconfiguration during execution.
		log.FromContext(ctx).Error(werr, "evaluating maintenance window during execution; treating as closed")
		windowOpen = false
	}

	batchIdx := currentBatch(mr)
	if batchIdx < 0 {
		return r.completeExecution(ctx, mr)
	}

	concurrency := pol.Concurrency(mr.Spec.MaxConcurrent)
	inFlight := countInFlight(mr)

	// Honor the policy's cluster-wide MaxConcurrentDrains across all active
	// requests, not just this one. Best-effort: with reconcileConcurrency > 1 a
	// brief overshoot is possible; run with reconcileConcurrency: 1 for a strict
	// guarantee.
	if policyCap := pol.Spec.MaxConcurrentDrains; policyCap > 0 {
		if budget := policyCap - r.globalDrainsInFlightExcept(ctx, mr.Name); budget < concurrency {
			concurrency = budget
		}
	}

	for i := range mr.Status.Nodes {
		ns := &mr.Status.Nodes[i]
		if ns.Batch != int32(batchIdx) || isNodeTerminal(ns.Phase) {
			continue
		}
		if err := r.stepNode(ctx, mr, pol, ns, &inFlight, concurrency, windowOpen, uncordonGated); err != nil {
			// Persist progress already applied to earlier nodes in this loop (their
			// cluster side-effects already happened) before surfacing the error, so
			// they are not re-driven from a stale phase on requeue.
			recomputeSummary(mr)
			_ = r.updateStatus(ctx, mr)
			return ctrl.Result{}, err
		}
	}

	if currentBatch(mr) < 0 {
		return r.completeExecution(ctx, mr)
	}
	recomputeSummary(mr)
	if err := r.updateStatus(ctx, mr); err != nil {
		return ctrl.Result{}, err
	}
	return r.requeue(r.Config.EvictionPollInterval.Duration), nil
}

// stepNode advances a single node by one step of the per-node phase machine.
func (r *MaintenanceRequestReconciler) stepNode(
	ctx context.Context,
	mr *v1alpha1.MaintenanceRequest,
	pol *policy.Effective,
	ns *v1alpha1.NodeExecutionStatus,
	inFlight *int32,
	concurrency int32,
	windowOpen bool,
	uncordonGated bool,
) error {
	switch ns.Phase {
	case v1alpha1.NodePending:
		if *inFlight >= concurrency || !windowOpen {
			return nil
		}
		node, err := r.kube.GetNode(ctx, ns.Node)
		if err != nil {
			if apierrors.IsNotFound(err) {
				setNodeTerminal(ns, v1alpha1.NodeSkipped, "", "node not found; skipped")
				return nil
			}
			return err
		}
		if kube.IsMCOManaged(node) {
			setNodeTerminal(ns, v1alpha1.NodeSkipped, "", "skipped: node managed by the Machine Config Operator")
			return nil
		}
		if v := targetVersion(mr); v != "" && kube.KubeletVersion(node) == v {
			setNodeTerminal(ns, v1alpha1.NodeSkipped, "", "skipped: node already at target version "+v)
			return nil
		}
		now := metav1.Now()
		ns.StartTime = &now
		ns.EndTime = nil
		ns.Phase = v1alpha1.NodeCordoning
		*inFlight++
		return nil

	case v1alpha1.NodeCordoning:
		if err := r.executor.Cordon(ctx, ns.Node); err != nil {
			return err
		}
		ns.Phase = v1alpha1.NodeDraining
		r.audit.Record(mr, corev1.EventTypeNormal, audit.ActionNodeCordoned, "node cordoned", map[string]string{"node": ns.Node})
		return nil

	case v1alpha1.NodeDraining:
		res, err := r.executor.EvictOnce(ctx, ns.Node, executor.Options{
			Force: pol.ForceAllowed(mr.Spec.Force),
			Now:   time.Now(),
		})
		if err != nil {
			return err
		}
		if ns.TotalPods == 0 && res.Blocking > 0 {
			ns.TotalPods = res.Blocking
		}
		ns.EvictedPods += res.Evicted
		ns.RemainingPods = res.Blocking
		if res.Message != "" {
			ns.Message = res.Message
		}
		if res.Blocking == 0 {
			ns.Phase = v1alpha1.NodePostCheck
			r.audit.Record(mr, corev1.EventTypeNormal, audit.ActionNodeDrained, "node drained", map[string]string{"node": ns.Node})
			return nil
		}
		if r.drainTimedOut(mr, ns) {
			reason := res.BlockReason
			if reason == "" {
				reason = v1alpha1.BlockTimeout
			}
			setNodeBlocked(ns, reason, "drain timed out: "+res.Message)
			metrics.BlockedDrainsTotal.WithLabelValues(reason).Inc()
			r.audit.Record(mr, corev1.EventTypeWarning, audit.ActionNodeBlocked, "node drain blocked",
				map[string]string{"node": ns.Node, "reason": reason})
			*inFlight--
		}
		return nil

	case v1alpha1.NodePostCheck:
		// Replacement supersedes uncordon: the drained node is destroyed, not
		// returned to service.
		if mr.Spec.ReplaceNodes() {
			ns.Phase = v1alpha1.NodeReplacing
			return nil
		}
		if !mr.Spec.UncordonAfter {
			r.observeDrainDuration(ns, "success")
			setNodeTerminal(ns, v1alpha1.NodeCompleted, "", "drained; left cordoned per spec")
			*inFlight--
			return nil
		}
		if uncordonGated && !uncordonApproved(mr, pol) {
			// Held by the request-level uncordon gate in reconcileExecuting.
			return nil
		}
		ns.Phase = v1alpha1.NodeUncordoning
		return nil

	case v1alpha1.NodeUncordoning:
		if err := r.executor.Uncordon(ctx, ns.Node); err != nil {
			return err
		}
		r.observeDrainDuration(ns, "success")
		setNodeTerminal(ns, v1alpha1.NodeCompleted, "", "drained and uncordoned")
		r.audit.Record(mr, corev1.EventTypeNormal, audit.ActionNodeUncordoned, "node uncordoned", map[string]string{"node": ns.Node})
		*inFlight--
		return nil

	case v1alpha1.NodeReplacing:
		// Capture how many Ready nodes are already at the target version before we
		// delete the Machine, so the post-check can require the count to grow
		// (a genuinely new node) rather than matching a pre-existing one.
		if v := targetVersion(mr); v != "" {
			baseline, err := r.kube.CountReadyNodesAtVersion(ctx, v)
			if err != nil {
				return err
			}
			ns.ReplacementBaseline = baseline
		}
		ref, found, err := r.executor.Replace(ctx, ns.Node, mr.Spec.EffectiveMachineAPI())
		if err != nil {
			return err
		}
		if !found {
			// No Machine backs this node, so replacement can never succeed; this is
			// a permanent failure, not a retryable block. Marking it Failed (rather
			// than Blocked) lets the failure threshold count it and stops the
			// re-drain/re-replace retry loop that would otherwise spin until the
			// global timeout.
			setNodeFailed(ns, v1alpha1.BlockMachineNotFound, "no Machine backs this node; cannot replace")
			metrics.NodeReplacementsTotal.WithLabelValues("no_machine").Inc()
			r.audit.Record(mr, corev1.EventTypeWarning, audit.ActionNodeBlocked, "node replacement failed: no backing Machine",
				map[string]string{"node": ns.Node})
			*inFlight--
			return nil
		}
		ns.Message = "deleted " + ref.String() + "; awaiting replacement"
		ns.Phase = v1alpha1.NodeAwaitingReplacement
		r.audit.Record(mr, corev1.EventTypeNormal, audit.ActionNodeReplacing, "node Machine deleted",
			map[string]string{"node": ns.Node, "machine": ref.String()})
		return nil

	case v1alpha1.NodeAwaitingReplacement:
		gone, err := r.executor.NodeGone(ctx, ns.Node)
		if err != nil {
			return err
		}
		if !gone {
			if r.replacementTimedOut(mr, ns) {
				r.blockReplacement(mr, ns, "replacement timed out: old node still present", inFlight)
			}
			return nil
		}
		// Old node removed. If a target version is set, require the count of Ready
		// nodes at that version to exceed the pre-replacement baseline, i.e. a new
		// node actually joined at the target version (best-effort verification).
		if v := targetVersion(mr); v != "" {
			ready, err := r.kube.CountReadyNodesAtVersion(ctx, v)
			if err != nil {
				return err
			}
			if ready <= ns.ReplacementBaseline {
				if r.replacementTimedOut(mr, ns) {
					r.blockReplacement(mr, ns, "replacement timed out: no new Ready node at "+v, inFlight)
				}
				return nil
			}
		}
		r.observeDrainDuration(ns, "replaced")
		metrics.NodeReplacementsTotal.WithLabelValues("success").Inc()
		setNodeTerminal(ns, v1alpha1.NodeCompleted, "", "node replaced")
		r.audit.Record(mr, corev1.EventTypeNormal, audit.ActionNodeReplaced, "node replaced", map[string]string{"node": ns.Node})
		*inFlight--
		return nil

	default:
		return nil
	}
}

// completeExecution decides the terminal/blocked outcome once no batch remains.
func (r *MaintenanceRequestReconciler) completeExecution(ctx context.Context, mr *v1alpha1.MaintenanceRequest) (ctrl.Result, error) {
	recomputeSummary(mr)
	failed := countNodePhase(mr, v1alpha1.NodeFailed)
	blocked := countNodePhase(mr, v1alpha1.NodeBlocked)

	switch {
	case failed > 0:
		return r.fail(ctx, mr, "NodeFailures", fmt.Sprintf("%d node(s) failed", failed))
	case blocked > 0:
		setCondition(mr, v1alpha1.CondExecuting, metav1.ConditionFalse, "Blocked", "execution halted by blocked nodes")
		setCondition(mr, v1alpha1.CondBlocked, metav1.ConditionTrue, "NodesBlocked", fmt.Sprintf("%d node(s) blocked", blocked))
		mr.Status.Phase = v1alpha1.PhaseBlocked
		mr.Status.Message = fmt.Sprintf("%d node(s) blocked", blocked)
		r.audit.Record(mr, corev1.EventTypeWarning, audit.ActionBlocked, mr.Status.Message, nil)
		if err := r.updateStatus(ctx, mr); err != nil {
			return ctrl.Result{}, err
		}
		r.refreshActiveGauge(ctx)
		return r.requeue(r.Config.GlobalRequeueInterval.Duration), nil
	default:
		return r.complete(ctx, mr, "all nodes completed")
	}
}

// --- node helpers ---

func initNodeStatuses(plan *v1alpha1.ExecutionPlan) []v1alpha1.NodeExecutionStatus {
	var out []v1alpha1.NodeExecutionStatus
	for bi := range plan.Batches {
		b := &plan.Batches[bi]
		for _, name := range b.Nodes {
			out = append(out, v1alpha1.NodeExecutionStatus{
				Node:  name,
				Phase: v1alpha1.NodePending,
				Batch: b.Index,
			})
		}
	}
	return out
}

func recomputeSummary(mr *v1alpha1.MaintenanceRequest) {
	var s v1alpha1.ProgressSummary
	for i := range mr.Status.Nodes {
		s.Total++
		switch mr.Status.Nodes[i].Phase {
		case v1alpha1.NodePending:
			s.Pending++
		case v1alpha1.NodeCordoning, v1alpha1.NodeDraining, v1alpha1.NodePostCheck, v1alpha1.NodeUncordoning,
			v1alpha1.NodeReplacing, v1alpha1.NodeAwaitingReplacement:
			s.InProgress++
		case v1alpha1.NodeCompleted:
			s.Completed++
		case v1alpha1.NodeBlocked:
			s.Blocked++
		case v1alpha1.NodeFailed:
			s.Failed++
		case v1alpha1.NodeSkipped:
			s.Skipped++
		}
	}
	mr.Status.Summary = s
}

// currentBatch is the lowest batch index that still has a non-terminal node, or
// -1 when every node is terminal. Batches are thus processed strictly in order.
func currentBatch(mr *v1alpha1.MaintenanceRequest) int {
	best := -1
	for i := range mr.Status.Nodes {
		ns := &mr.Status.Nodes[i]
		if isNodeTerminal(ns.Phase) {
			continue
		}
		idx := int(ns.Batch)
		if best == -1 || idx < best {
			best = idx
		}
	}
	return best
}

func countInFlight(mr *v1alpha1.MaintenanceRequest) int32 {
	var n int32
	for i := range mr.Status.Nodes {
		switch mr.Status.Nodes[i].Phase {
		case v1alpha1.NodeCordoning, v1alpha1.NodeDraining, v1alpha1.NodePostCheck, v1alpha1.NodeUncordoning,
			v1alpha1.NodeReplacing, v1alpha1.NodeAwaitingReplacement:
			n++
		}
	}
	return n
}

func countNodePhase(mr *v1alpha1.MaintenanceRequest, phase v1alpha1.NodePhase) int32 {
	var n int32
	for i := range mr.Status.Nodes {
		if mr.Status.Nodes[i].Phase == phase {
			n++
		}
	}
	return n
}

func isNodeTerminal(p v1alpha1.NodePhase) bool {
	switch p {
	case v1alpha1.NodeCompleted, v1alpha1.NodeBlocked, v1alpha1.NodeFailed, v1alpha1.NodeSkipped:
		return true
	default:
		return false
	}
}

func setNodeTerminal(ns *v1alpha1.NodeExecutionStatus, phase v1alpha1.NodePhase, blockReason, msg string) {
	now := metav1.Now()
	ns.Phase = phase
	ns.BlockReason = blockReason
	ns.Message = msg
	if ns.EndTime == nil {
		ns.EndTime = &now
	}
}

func setNodeBlocked(ns *v1alpha1.NodeExecutionStatus, reason, msg string) {
	setNodeTerminal(ns, v1alpha1.NodeBlocked, reason, msg)
}

func setNodeFailed(ns *v1alpha1.NodeExecutionStatus, reason, msg string) {
	setNodeTerminal(ns, v1alpha1.NodeFailed, reason, msg)
}

// shouldReleaseNode reports whether a node in this phase was cordoned by us and
// is still present, so an aborting request should return it to service.
func shouldReleaseNode(p v1alpha1.NodePhase) bool {
	switch p {
	case v1alpha1.NodeCordoning, v1alpha1.NodeDraining, v1alpha1.NodePostCheck,
		v1alpha1.NodeUncordoning, v1alpha1.NodeBlocked:
		return true
	default:
		return false
	}
}

// nodesToRelease returns the node names an aborting request should uncordon: the
// nodes it cordoned, but only when the request asked for nodes to be returned to
// service (UncordonAfter) and does not replace them (replacement destroys the
// node, so there is nothing to return).
func nodesToRelease(mr *v1alpha1.MaintenanceRequest) []string {
	if !mr.Spec.UncordonAfter || mr.Spec.ReplaceNodes() {
		return nil
	}
	var out []string
	for i := range mr.Status.Nodes {
		if shouldReleaseNode(mr.Status.Nodes[i].Phase) {
			out = append(out, mr.Status.Nodes[i].Node)
		}
	}
	return out
}

// releaseCordonedNodes best-effort uncordons the nodes an aborting request had
// cordoned. Failures are logged, not fatal: the request still terminates.
func (r *MaintenanceRequestReconciler) releaseCordonedNodes(ctx context.Context, mr *v1alpha1.MaintenanceRequest) {
	logger := log.FromContext(ctx)
	for _, name := range nodesToRelease(mr) {
		if err := r.executor.Uncordon(ctx, name); err != nil {
			logger.Error(err, "best-effort uncordon on abort failed", "node", name)
			continue
		}
		r.audit.Record(mr, corev1.EventTypeNormal, audit.ActionNodeUncordoned,
			"node uncordoned (request aborted)", map[string]string{"node": name})
	}
}

func anyNodeAtPostCheck(mr *v1alpha1.MaintenanceRequest) bool {
	for i := range mr.Status.Nodes {
		if mr.Status.Nodes[i].Phase == v1alpha1.NodePostCheck {
			return true
		}
	}
	return false
}

func uncordonApproved(mr *v1alpha1.MaintenanceRequest, pol *policy.Effective) bool {
	effPolicy := pol.ApprovalPolicy(mr.Spec.Approval.Policy)
	if !approval.RequiresGate(effPolicy, v1alpha1.GateUncordon) {
		return true
	}
	res, _ := approval.Evaluate(effPolicy, mr.Spec.Approval, v1alpha1.GateUncordon)
	return res == approval.Approved
}

func (r *MaintenanceRequestReconciler) observeDrainDuration(ns *v1alpha1.NodeExecutionStatus, result string) {
	if ns.StartTime == nil {
		return
	}
	metrics.DrainDurationSeconds.WithLabelValues(result).Observe(time.Since(ns.StartTime.Time).Seconds())
}

// targetVersion returns the request's target kubelet version, or "" when none is
// set (or the request does not replace nodes).
func targetVersion(mr *v1alpha1.MaintenanceRequest) string {
	if mr.Spec.Upgrade == nil {
		return ""
	}
	return mr.Spec.Upgrade.TargetKubeletVersion
}

// blockReplacement marks a node Blocked on a replacement failure, records the
// metric/audit and releases its in-flight slot.
func (r *MaintenanceRequestReconciler) blockReplacement(mr *v1alpha1.MaintenanceRequest, ns *v1alpha1.NodeExecutionStatus, msg string, inFlight *int32) {
	setNodeBlocked(ns, v1alpha1.BlockReplaceTimeout, msg)
	metrics.BlockedDrainsTotal.WithLabelValues(v1alpha1.BlockReplaceTimeout).Inc()
	metrics.NodeReplacementsTotal.WithLabelValues("timeout").Inc()
	r.audit.Record(mr, corev1.EventTypeWarning, audit.ActionNodeBlocked, msg, map[string]string{"node": ns.Node})
	*inFlight--
}
