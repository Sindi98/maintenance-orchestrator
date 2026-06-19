// Package planner turns a set of target nodes and a strategy into an explicit,
// ordered ExecutionPlan: batches to process in order, an estimated workload
// impact, and a 0-100 risk score with human-readable factors. Batch building
// and scoring are pure functions; impact analysis performs read-only pod I/O.
package planner

import (
	"context"
	"fmt"
	"sort"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/Sindi98/maintenance-orchestrator/api/v1alpha1"
	"github.com/Sindi98/maintenance-orchestrator/internal/kube"
	"github.com/Sindi98/maintenance-orchestrator/internal/policy"
)

// Planner builds execution plans.
type Planner struct {
	Client   *kube.Client
	PoolKeys []string
}

// NewPlanner constructs a Planner.
func NewPlanner(c *kube.Client, poolKeys []string) *Planner {
	return &Planner{Client: c, PoolKeys: poolKeys}
}

// Input is everything needed to build a plan.
type Input struct {
	Request *v1alpha1.MaintenanceRequest
	Policy  *policy.Effective
	Nodes   []corev1.Node
	// Now is injected for deterministic GeneratedAt in tests; zero means time.Now().
	Now time.Time
}

// Build computes the full ExecutionPlan for the request.
func (p *Planner) Build(ctx context.Context, in Input) (*v1alpha1.ExecutionPlan, error) {
	strategy := in.Request.Spec.Strategy
	if strategy == "" {
		strategy = v1alpha1.StrategySerial
	}

	concurrency := in.Request.Spec.MaxConcurrent
	if in.Policy != nil {
		concurrency = in.Policy.Concurrency(in.Request.Spec.MaxConcurrent)
	} else if concurrency < 1 {
		concurrency = 1
	}

	batches := BuildBatches(strategy, in.Nodes, in.Request.Spec.BatchSize, concurrency, p.PoolKeys)

	impact, controlPlane, err := p.analyze(ctx, in)
	if err != nil {
		return nil, err
	}
	risk, factors := Score(int32(len(in.Nodes)), impact, controlPlane, concurrency)

	now := in.Now
	if now.IsZero() {
		now = time.Now()
	}

	return &v1alpha1.ExecutionPlan{
		Strategy:      strategy,
		Batches:       batches,
		TotalNodes:    int32(len(in.Nodes)),
		MaxConcurrent: concurrency,
		RiskScore:     risk,
		RiskFactors:   factors,
		Impact:        impact,
		GeneratedAt:   metav1.NewTime(now),
	}, nil
}

// BuildBatches groups nodes into ordered batches according to the strategy. It is
// pure and deterministic (node names are sorted within and across batches).
func BuildBatches(strategy v1alpha1.Strategy, nodes []corev1.Node, batchSize, concurrency int32, poolKeys []string) []v1alpha1.Batch {
	switch strategy {
	case v1alpha1.StrategyBatched:
		size := batchSize
		if size < 1 {
			size = concurrency
		}
		if size < 1 {
			size = 1
		}
		names := sortedNames(nodes)
		return chunk(names, size)
	case v1alpha1.StrategyByZone:
		return group(nodes, func(n *corev1.Node) string {
			if z := kube.NodeZone(n); z != "" {
				return z
			}
			return "unzoned"
		})
	case v1alpha1.StrategyByPool:
		return group(nodes, func(n *corev1.Node) string {
			if _, v, ok := kube.PoolValue(n, poolKeys); ok {
				return v
			}
			return "unpooled"
		})
	default: // Serial
		return chunk(sortedNames(nodes), 1)
	}
}

// Score computes a 0-100 risk score and its contributing factors. Pure function.
func Score(totalNodes int32, impact v1alpha1.ImpactEstimate, controlPlane bool, concurrency int32) (int32, []string) {
	var score int32
	var factors []string

	if controlPlane {
		score += 30
		factors = append(factors, "control-plane nodes involved")
	}
	if impact.SingleReplicaWorkloads > 0 {
		add := impact.SingleReplicaWorkloads * 10
		if add > 30 {
			add = 30
		}
		score += add
		factors = append(factors, fmt.Sprintf("%d single-replica/unmanaged workload(s)", impact.SingleReplicaWorkloads))
	}
	if impact.EmptyDirPods > 0 {
		add := impact.EmptyDirPods * 5
		if add > 20 {
			add = 20
		}
		score += add
		factors = append(factors, fmt.Sprintf("%d pod(s) with emptyDir data-loss risk", impact.EmptyDirPods))
	}
	if concurrency > 1 {
		score += 10
		factors = append(factors, fmt.Sprintf("concurrent drains: %d", concurrency))
	}
	switch {
	case totalNodes >= 5:
		score += 15
		factors = append(factors, fmt.Sprintf("%d nodes in scope", totalNodes))
	case totalNodes >= 2:
		score += 5
	}
	if impact.PodsToEvict >= 50 {
		score += 10
		factors = append(factors, fmt.Sprintf("%d pods to evict", impact.PodsToEvict))
	}

	if score > 100 {
		score = 100
	}
	if score < 0 {
		score = 0
	}
	return score, factors
}

// analyze sums the per-node workload impact across the target nodes.
func (p *Planner) analyze(ctx context.Context, in Input) (v1alpha1.ImpactEstimate, bool, error) {
	var podsToEvict, emptyDirPods int32
	workloads := sets.New[string]()
	single := sets.New[string]()
	controlPlane := false

	for i := range in.Nodes {
		node := &in.Nodes[i]
		if in.Policy != nil && in.Policy.IsControlPlaneNode(node) {
			controlPlane = true
		}
		pods, err := p.Client.ListPodsOnNode(ctx, node.Name)
		if err != nil {
			return v1alpha1.ImpactEstimate{}, false, fmt.Errorf("list pods on node %s: %w", node.Name, err)
		}
		for j := range pods {
			pod := &pods[j]
			if !kube.IsEvictable(pod) {
				continue
			}
			podsToEvict++
			if kube.HasEmptyDir(pod) {
				emptyDirPods++
			}
			wl, err := p.Client.ResolveWorkload(ctx, pod)
			if err != nil {
				return v1alpha1.ImpactEstimate{}, false, fmt.Errorf("resolve workload for pod %s/%s: %w", pod.Namespace, pod.Name, err)
			}
			key := wl.Kind + "/" + wl.Namespace + "/" + wl.Name
			workloads.Insert(key)
			if wl.Kind != "DaemonSet" && (!wl.IsController || wl.Replicas <= 1) {
				single.Insert(key)
			}
		}
	}

	return v1alpha1.ImpactEstimate{
		PodsToEvict:            podsToEvict,
		AppsAffected:           int32(workloads.Len()),
		SingleReplicaWorkloads: int32(single.Len()),
		EmptyDirPods:           emptyDirPods,
	}, controlPlane, nil
}

func sortedNames(nodes []corev1.Node) []string {
	names := make([]string, len(nodes))
	for i := range nodes {
		names[i] = nodes[i].Name
	}
	sort.Strings(names)
	return names
}

func chunk(names []string, size int32) []v1alpha1.Batch {
	if len(names) == 0 {
		return nil
	}
	var batches []v1alpha1.Batch
	var idx int32
	for i := 0; i < len(names); i += int(size) {
		end := i + int(size)
		if end > len(names) {
			end = len(names)
		}
		batches = append(batches, v1alpha1.Batch{
			Index: idx,
			Nodes: append([]string(nil), names[i:end]...),
		})
		idx++
	}
	return batches
}

func group(nodes []corev1.Node, keyFn func(*corev1.Node) string) []v1alpha1.Batch {
	groups := map[string][]string{}
	var order []string
	for i := range nodes {
		k := keyFn(&nodes[i])
		if _, ok := groups[k]; !ok {
			order = append(order, k)
		}
		groups[k] = append(groups[k], nodes[i].Name)
	}
	sort.Strings(order)

	batches := make([]v1alpha1.Batch, 0, len(order))
	for idx, k := range order {
		ns := groups[k]
		sort.Strings(ns)
		batches = append(batches, v1alpha1.Batch{
			Index: int32(idx),
			Group: k,
			Nodes: ns,
		})
	}
	return batches
}
