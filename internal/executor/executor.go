// Package executor performs the mechanical side of a node drain: cordon, a single
// round of policy/v1 evictions (optionally force-delete when policy permits),
// progress accounting with block-reason classification, and uncordon. The
// controller owns the per-node phase machine and timeouts and calls these
// operations once per reconcile (poll-and-requeue).
package executor

import (
	"context"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/Sindi98/maintenance-orchestrator/api/v1alpha1"
	"github.com/Sindi98/maintenance-orchestrator/internal/kube"
)

// stuckGraceSlack is added on top of a pod's deletion grace period before we
// classify it as stuck terminating.
const stuckGraceSlack = 30 * time.Second

// defaultGracePeriod is assumed when a terminating pod exposes no grace period.
const defaultGracePeriod int64 = 30

// Executor performs cordon/evict/uncordon operations.
type Executor struct {
	Client *kube.Client
}

// New constructs an Executor.
func New(c *kube.Client) *Executor {
	return &Executor{Client: c}
}

// Options tunes a single eviction round.
type Options struct {
	// Force switches from the eviction API to delete-based removal (grace 0).
	// The caller must have verified the policy permits it.
	Force bool
	// GracePeriodSeconds overrides the pods' default termination grace period.
	GracePeriodSeconds *int64
	// Now is injected for deterministic stuck-termination detection in tests.
	Now time.Time
}

// EvictResult reports the outcome of one EvictOnce round.
type EvictResult struct {
	// Blocking is the number of drain-blocking pods still present on the node
	// (managed/naked pods that are not DaemonSet/mirror and not yet gone),
	// including pods already terminating.
	Blocking int32
	// Evictable is how many of those were eligible for an eviction POST this round.
	Evictable int32
	// Evicted is how many evictions/deletes were successfully requested this round.
	Evicted int32
	// BlockReason classifies why the node is not progressing, when it is not.
	// Empty means the node is draining normally or already drained.
	BlockReason string
	// Message is a human-readable detail accompanying BlockReason.
	Message string
}

// Cordon marks the node unschedulable (idempotent).
func (e *Executor) Cordon(ctx context.Context, nodeName string) error {
	node, err := e.Client.GetNode(ctx, nodeName)
	if err != nil {
		return err
	}
	return e.Client.Cordon(ctx, node)
}

// Uncordon marks the node schedulable again (idempotent). A missing node is a no-op.
func (e *Executor) Uncordon(ctx context.Context, nodeName string) error {
	node, err := e.Client.GetNode(ctx, nodeName)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return err
	}
	return e.Client.Uncordon(ctx, node)
}

// Drained reports whether the node has no drain-blocking pods left, plus the
// current blocking count.
func (e *Executor) Drained(ctx context.Context, nodeName string) (bool, int32, error) {
	pods, err := e.Client.ListPodsOnNode(ctx, nodeName)
	if err != nil {
		return false, 0, err
	}
	var blocking int32
	for i := range pods {
		if kube.IsDrainBlocking(&pods[i]) {
			blocking++
		}
	}
	return blocking == 0, blocking, nil
}

// EvictOnce evicts every currently-evictable pod on the node once and reports
// progress. It never blocks waiting for terminations; the controller re-checks on
// the next reconcile. A 429 from the eviction API (a PDB) is recorded, not retried
// in a hot loop.
func (e *Executor) EvictOnce(ctx context.Context, nodeName string, opts Options) (EvictResult, error) {
	now := opts.Now
	if now.IsZero() {
		now = time.Now()
	}

	pods, err := e.Client.ListPodsOnNode(ctx, nodeName)
	if err != nil {
		return EvictResult{}, err
	}

	var res EvictResult
	var pdbBlocked, evictErrors, stuck int32

	for i := range pods {
		pod := &pods[i]
		if !kube.IsDrainBlocking(pod) {
			continue
		}
		res.Blocking++

		if kube.IsTerminating(pod) {
			if terminatingTooLong(pod, now) {
				stuck++
			}
			continue
		}

		res.Evictable++
		if err := e.removePod(ctx, pod, opts); err != nil {
			switch kube.ClassifyEvictionError(err) {
			case v1alpha1.BlockPDB:
				pdbBlocked++
			case "":
				// NotFound: the pod is already gone; count it as progress.
				res.Evicted++
			default:
				evictErrors++
			}
			continue
		}
		res.Evicted++
	}

	res.BlockReason, res.Message = classify(res.Blocking, res.Evictable, pdbBlocked, evictErrors, stuck)
	return res, nil
}

// removePod evicts (or, when Force is set, deletes) a single pod.
func (e *Executor) removePod(ctx context.Context, pod *corev1.Pod, opts Options) error {
	if opts.Force {
		grace := int64(0)
		if opts.GracePeriodSeconds != nil {
			grace = *opts.GracePeriodSeconds
		}
		err := e.Client.Delete(ctx, pod, client.GracePeriodSeconds(grace))
		if apierrors.IsNotFound(err) {
			return nil
		}
		return err
	}
	return e.Client.Evict(ctx, pod, opts.GracePeriodSeconds)
}

// classify maps eviction-round counters to a block reason. When the node is still
// making normal progress (pods evicted, none stuck) the reason is empty.
func classify(blocking, evictable, pdbBlocked, evictErrors, stuck int32) (string, string) {
	switch {
	case blocking == 0:
		return "", ""
	case pdbBlocked > 0:
		return v1alpha1.BlockPDB, "eviction denied by a PodDisruptionBudget"
	case evictErrors > 0:
		return v1alpha1.BlockEvictionError, "the eviction API returned errors"
	case stuck > 0:
		return v1alpha1.BlockStuckTermination, "pods are stuck terminating past their grace period"
	case evictable == 0:
		// Pods remain but none are evictable and none are classified stuck:
		// they are terminating within grace; keep draining.
		return "", "waiting for pods to terminate"
	default:
		return "", "eviction in progress"
	}
}

// terminatingTooLong reports whether a terminating pod has exceeded its grace
// period plus a fixed slack and should be treated as stuck.
func terminatingTooLong(pod *corev1.Pod, now time.Time) bool {
	if pod.DeletionTimestamp == nil {
		return false
	}
	grace := defaultGracePeriod
	if pod.DeletionGracePeriodSeconds != nil {
		grace = *pod.DeletionGracePeriodSeconds
	}
	deadline := pod.DeletionTimestamp.Add(time.Duration(grace)*time.Second + stuckGraceSlack)
	return now.After(deadline)
}

