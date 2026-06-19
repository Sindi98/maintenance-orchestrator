// Package preflight runs the safety checks performed before any node is touched.
// Each check yields a PreflightCheckResult (Pass/Warn/Fail) with a machine code,
// a human message and structured details. The engine itself performs read-only
// cluster I/O through internal/kube; the scoring/aggregation helpers are pure.
package preflight

import (
	"context"
	"fmt"
	"strconv"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/Sindi98/maintenance-orchestrator/api/v1alpha1"
	"github.com/Sindi98/maintenance-orchestrator/internal/kube"
	"github.com/Sindi98/maintenance-orchestrator/internal/policy"
	"github.com/Sindi98/maintenance-orchestrator/internal/window"
)

// MaxResults bounds how many results the engine returns, so a huge cluster cannot
// bloat the object status. Fails are always kept; warns are truncated first.
const MaxResults = 200

// Engine runs preflight checks against the cluster.
type Engine struct {
	Client *kube.Client
}

// NewEngine constructs a preflight Engine.
func NewEngine(c *kube.Client) *Engine {
	return &Engine{Client: c}
}

// Input is everything the engine needs to evaluate a request.
type Input struct {
	// Request is the MaintenanceRequest being validated.
	Request *v1alpha1.MaintenanceRequest
	// Policy is the resolved, defaulted effective policy.
	Policy *policy.Effective
	// Nodes are the resolved, existing target nodes.
	Nodes []corev1.Node
	// Universe is the pool/selector scope used for unavailability math. When
	// empty it defaults to Nodes (correct for an explicit node-name target).
	Universe []corev1.Node
	// MissingNodes are requested node names that do not exist in the cluster.
	MissingNodes []string
	// Now is the evaluation time (injected for testability of window checks).
	Now time.Time
}

// Run executes all checks and returns their results.
func (e *Engine) Run(ctx context.Context, in Input) ([]v1alpha1.PreflightCheckResult, error) {
	now := in.Now
	if now.IsZero() {
		now = time.Now()
	}
	ts := metav1.NewTime(now)
	var out []v1alpha1.PreflightCheckResult
	add := func(code, node string, status v1alpha1.CheckStatus, msg string, details map[string]string) {
		out = append(out, v1alpha1.PreflightCheckResult{
			Code:    code,
			Node:    node,
			Status:  status,
			Message: msg,
			Details: details,
			Time:    ts,
		})
	}

	// Missing nodes are a hard failure for an explicit node target.
	for _, name := range in.MissingNodes {
		add(v1alpha1.CodeNodeNotFound, name, v1alpha1.CheckFail, "node does not exist in the cluster", nil)
	}

	// Cross-node dedup sets so a workload/PDB spanning many nodes warns once.
	seenSingle := sets.New[string]()
	seenPDB := sets.New[string]()
	removal := sets.New[string]()
	for i := range in.Nodes {
		removal.Insert(in.Nodes[i].Name)
	}

	for i := range in.Nodes {
		node := &in.Nodes[i]

		if !kube.IsReady(node) {
			add(v1alpha1.CodeNodeNotReady, node.Name, v1alpha1.CheckWarn, "node is not Ready", nil)
		}
		if node.Spec.Unschedulable {
			add(v1alpha1.CodeAlreadyCordoned, node.Name, v1alpha1.CheckWarn, "node is already cordoned", nil)
		}
		if in.Policy.IsControlPlaneNode(node) && in.Policy.ControlPlaneBlocked(in.Request.Spec.AllowControlPlane) {
			add(v1alpha1.CodeControlPlane, node.Name, v1alpha1.CheckFail,
				"control-plane node is protected by policy; set spec.allowControlPlane and a permissive policy to override", nil)
		}
		if k, ok := in.Policy.ReservedLabel(node); ok {
			add(v1alpha1.CodeReservedLabel, node.Name, v1alpha1.CheckFail, "node carries a reserved label", map[string]string{"label": k})
		}
		if k, ok := in.Policy.ReservedTaint(node); ok {
			add(v1alpha1.CodeReservedTaint, node.Name, v1alpha1.CheckFail, "node carries a reserved taint", map[string]string{"taint": k})
		}
		if kube.IsMCOManaged(node) {
			add(v1alpha1.CodeMCOManaged, node.Name, v1alpha1.CheckWarn,
				"node is being reconfigured by the Machine Config Operator and will be skipped", nil)
		}

		pods, err := e.Client.ListPodsOnNode(ctx, node.Name)
		if err != nil {
			return nil, fmt.Errorf("list pods on node %s: %w", node.Name, err)
		}
		e.checkPods(ctx, node.Name, pods, add, seenSingle, seenPDB)
	}

	e.checkCapacity(ctx, in, removal, add)
	e.checkUnavailability(in, add)
	checkWindow(in, now, add)

	return truncate(out), nil
}

// checkPods evaluates pod-level risks on a single node.
func (e *Engine) checkPods(
	ctx context.Context,
	nodeName string,
	pods []corev1.Pod,
	add func(code, node string, status v1alpha1.CheckStatus, msg string, details map[string]string),
	seenSingle, seenPDB sets.Set[string],
) {
	var dsCount int
	for i := range pods {
		pod := &pods[i]

		if kube.IsDaemonSetPod(pod) {
			dsCount++
			continue
		}
		if kube.IsMirrorPod(pod) {
			add(v1alpha1.CodeStaticPod, nodeName, v1alpha1.CheckWarn,
				"static/mirror pod cannot be evicted and will be ignored", map[string]string{"pod": podKey(pod)})
			continue
		}
		if kube.IsTerminated(pod) {
			continue
		}

		if kube.HasEmptyDir(pod) {
			add(v1alpha1.CodeEmptyDir, nodeName, v1alpha1.CheckWarn,
				"pod uses an emptyDir volume; its data is lost on eviction", map[string]string{"pod": podKey(pod)})
		}
		if kube.HasHostPath(pod) {
			add(v1alpha1.CodeLocalStorage, nodeName, v1alpha1.CheckWarn,
				"pod uses a hostPath volume (node-local storage)", map[string]string{"pod": podKey(pod)})
		}

		if pdb, err := e.Client.PDBForPod(ctx, pod); err == nil && kube.PDBIsTight(pdb) {
			key := pdb.Namespace + "/" + pdb.Name
			if !seenPDB.Has(key) {
				seenPDB.Insert(key)
				add(v1alpha1.CodePDBBlocks, nodeName, v1alpha1.CheckWarn,
					"a PodDisruptionBudget currently allows no voluntary disruptions", map[string]string{"pdb": key})
			}
		}

		if wl, err := e.Client.ResolveWorkload(ctx, pod); err == nil {
			key := wl.Kind + "/" + wl.Namespace + "/" + wl.Name
			switch {
			case !wl.IsController:
				if !seenSingle.Has(key) {
					seenSingle.Insert(key)
					add(v1alpha1.CodeSingleReplica, nodeName, v1alpha1.CheckWarn,
						"unmanaged (naked) pod will not be rescheduled after eviction", map[string]string{"pod": podKey(pod)})
				}
			case wl.Kind != "DaemonSet" && wl.Replicas <= 1:
				if !seenSingle.Has(key) {
					seenSingle.Insert(key)
					add(v1alpha1.CodeSingleReplica, nodeName, v1alpha1.CheckWarn,
						"workload has a single replica; eviction causes downtime",
						map[string]string{"workload": key})
				}
			}
		}
	}
	if dsCount > 0 {
		add(v1alpha1.CodeDaemonSetPods, nodeName, v1alpha1.CheckPass,
			"DaemonSet pods will be ignored, as kubectl drain does", map[string]string{"count": strconv.Itoa(dsCount)})
	}
}

// checkCapacity runs the heuristic, request-based headroom check.
func (e *Engine) checkCapacity(
	ctx context.Context,
	in Input,
	removal sets.Set[string],
	add func(code, node string, status v1alpha1.CheckStatus, msg string, details map[string]string),
) {
	if in.Policy.Spec.MinCapacityHeadroomPercent <= 0 {
		return
	}
	sel, err := in.Policy.ScopeSelector()
	if err != nil {
		add(v1alpha1.CodeInsufficientCap, "", v1alpha1.CheckWarn, "invalid policy node selector: "+err.Error(), nil)
		return
	}
	report, err := e.Client.ComputeHeadroom(ctx, removal, sel)
	if err != nil {
		add(v1alpha1.CodeInsufficientCap, "", v1alpha1.CheckWarn, "capacity check failed: "+err.Error(), nil)
		return
	}
	if report.HeadroomPercent < in.Policy.Spec.MinCapacityHeadroomPercent {
		add(v1alpha1.CodeInsufficientCap, "", v1alpha1.CheckFail,
			fmt.Sprintf("estimated cluster headroom %d%% is below the required %d%%",
				report.HeadroomPercent, in.Policy.Spec.MinCapacityHeadroomPercent),
			map[string]string{
				"headroomPercent": strconv.Itoa(int(report.HeadroomPercent)),
				"requiredPercent": strconv.Itoa(int(in.Policy.Spec.MinCapacityHeadroomPercent)),
			})
	}
}

// checkUnavailability enforces the max-unavailable guardrail over the scope
// universe. It compares the policy limit against the PEAK number of nodes this
// request leaves unavailable at the same time, not its whole target scope:
//
//   - When the request returns nodes to service (UncordonAfter), only the
//     effective per-batch concurrency is ever cordoned at once, so a Serial or
//     Batched rolling maintenance is evaluated by that peak. This lets a
//     one-at-a-time rolling drain of a whole pool pass a small limit, which the
//     executor's concurrency control already enforces at runtime.
//   - When the request does NOT uncordon, cordoned nodes accumulate over its
//     lifetime, so the peak is the whole target scope (the conservative case).
func (e *Engine) checkUnavailability(
	in Input,
	add func(code, node string, status v1alpha1.CheckStatus, msg string, details map[string]string),
) {
	if !in.Policy.UnavailabilityCapped() {
		return
	}
	universe := in.Universe
	if len(universe) == 0 {
		universe = in.Nodes
	}
	total := int32(len(universe))
	var already int32
	for i := range universe {
		if universe[i].Spec.Unschedulable {
			already++
		}
	}
	var willAdd int32
	for i := range in.Nodes {
		if !in.Nodes[i].Spec.Unschedulable {
			willAdd++
		}
	}

	peak := willAdd
	if in.Request.Spec.UncordonAfter {
		if concurrency := in.Policy.Concurrency(in.Request.Spec.MaxConcurrent); concurrency < peak {
			peak = concurrency
		}
	}

	limit := in.Policy.MaxUnavailable(total)
	if already+peak > limit {
		add(v1alpha1.CodeTooManyUnavailable, "", v1alpha1.CheckFail,
			fmt.Sprintf("this request would leave up to %d/%d node(s) unavailable at once (%d already cordoned), exceeding the limit of %d",
				already+peak, total, already, limit),
			map[string]string{
				"alreadyUnavailable": strconv.Itoa(int(already)),
				"peakConcurrent":     strconv.Itoa(int(peak)),
				"targeted":           strconv.Itoa(int(willAdd)),
				"total":              strconv.Itoa(int(total)),
				"limit":              strconv.Itoa(int(limit)),
			})
	}
}

// checkWindow surfaces (as a non-blocking warning) that the request is currently
// outside its maintenance window. The controller enforces the actual gating.
func checkWindow(in Input, now time.Time, add func(code, node string, status v1alpha1.CheckStatus, msg string, details map[string]string)) {
	open, err := WindowsOpen(in.Request, in.Policy.Spec, now)
	if err != nil {
		add(v1alpha1.CodeWindowClosed, "", v1alpha1.CheckWarn, "invalid maintenance window: "+err.Error(), nil)
		return
	}
	if !open {
		add(v1alpha1.CodeWindowClosed, "", v1alpha1.CheckWarn, "request is currently outside its maintenance window", nil)
	}
}

// WindowsOpen reports whether now is inside both the request window (if any) and
// an allowed policy window (if any). Absent windows mean "always open".
func WindowsOpen(req *v1alpha1.MaintenanceRequest, spec v1alpha1.MaintenancePolicySpec, now time.Time) (bool, error) {
	if req.Spec.MaintenanceWindow != nil {
		ok, err := window.IsOpen(*req.Spec.MaintenanceWindow, now)
		if err != nil {
			return false, err
		}
		if !ok {
			return false, nil
		}
	}
	if len(spec.AllowedWindows) > 0 {
		ok, err := window.AnyOpen(spec.AllowedWindows, now)
		if err != nil {
			return false, err
		}
		if !ok {
			return false, nil
		}
	}
	return true, nil
}

// Worst returns the most severe status across results (Fail > Warn > Pass).
func Worst(results []v1alpha1.PreflightCheckResult) v1alpha1.CheckStatus {
	worst := v1alpha1.CheckPass
	for i := range results {
		switch results[i].Status {
		case v1alpha1.CheckFail:
			return v1alpha1.CheckFail
		case v1alpha1.CheckWarn:
			worst = v1alpha1.CheckWarn
		}
	}
	return worst
}

// HasFail reports whether any result is a Fail.
func HasFail(results []v1alpha1.PreflightCheckResult) bool {
	for i := range results {
		if results[i].Status == v1alpha1.CheckFail {
			return true
		}
	}
	return false
}

// FailCodes returns the distinct Fail check codes, for metrics/audit.
func FailCodes(results []v1alpha1.PreflightCheckResult) []string {
	seen := sets.New[string]()
	var out []string
	for i := range results {
		if results[i].Status == v1alpha1.CheckFail && !seen.Has(results[i].Code) {
			seen.Insert(results[i].Code)
			out = append(out, results[i].Code)
		}
	}
	return out
}

// truncate caps the result slice to MaxResults, keeping all Fails first.
func truncate(results []v1alpha1.PreflightCheckResult) []v1alpha1.PreflightCheckResult {
	if len(results) <= MaxResults {
		return results
	}
	kept := make([]v1alpha1.PreflightCheckResult, 0, MaxResults)
	for i := range results {
		if results[i].Status == v1alpha1.CheckFail {
			kept = append(kept, results[i])
		}
	}
	for i := range results {
		if len(kept) >= MaxResults {
			break
		}
		if results[i].Status != v1alpha1.CheckFail {
			kept = append(kept, results[i])
		}
	}
	return kept
}

func podKey(pod *corev1.Pod) string {
	return pod.Namespace + "/" + pod.Name
}
