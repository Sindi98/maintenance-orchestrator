package controller

import (
	"context"
	"fmt"
	"sort"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"

	"github.com/Sindi98/maintenance-orchestrator/api/v1alpha1"
)

// resolvedTarget is the outcome of resolving a TargetRef into concrete nodes.
type resolvedTarget struct {
	// Nodes are the existing target nodes to maintain.
	Nodes []corev1.Node
	// Missing are requested node names that do not exist (Node target only).
	Missing []string
	// Universe is the pool/selector scope used for unavailability math. For an
	// explicit node-name target it equals Nodes.
	Universe []corev1.Node
}

// resolveTarget turns spec.target into concrete nodes.
func (r *MaintenanceRequestReconciler) resolveTarget(ctx context.Context, mr *v1alpha1.MaintenanceRequest) (resolvedTarget, error) {
	t := mr.Spec.Target
	switch t.Type {
	case v1alpha1.TargetNode:
		return r.resolveNodeNames(ctx, t.NodeNames)
	case v1alpha1.TargetNodeSelector:
		return r.resolveSelector(ctx, t.Selector)
	case v1alpha1.TargetPool:
		return r.resolvePool(ctx, t.PoolKey, t.PoolValue)
	default:
		return resolvedTarget{}, fmt.Errorf("unsupported target type %q", t.Type)
	}
}

func (r *MaintenanceRequestReconciler) resolveNodeNames(ctx context.Context, names []string) (resolvedTarget, error) {
	if len(names) == 0 {
		return resolvedTarget{}, fmt.Errorf("node target requires spec.target.nodeNames")
	}
	var res resolvedTarget
	for _, name := range names {
		node, err := r.kube.GetNode(ctx, name)
		if err != nil {
			if apierrors.IsNotFound(err) {
				res.Missing = append(res.Missing, name)
				continue
			}
			return resolvedTarget{}, fmt.Errorf("get node %s: %w", name, err)
		}
		res.Nodes = append(res.Nodes, *node)
	}
	sortNodes(res.Nodes)
	res.Universe = res.Nodes
	return res, nil
}

func (r *MaintenanceRequestReconciler) resolveSelector(ctx context.Context, sel *metav1.LabelSelector) (resolvedTarget, error) {
	if sel == nil {
		return resolvedTarget{}, fmt.Errorf("nodeSelector target requires spec.target.selector")
	}
	s, err := metav1.LabelSelectorAsSelector(sel)
	if err != nil {
		return resolvedTarget{}, fmt.Errorf("invalid node selector: %w", err)
	}
	nodes, err := r.kube.ListNodes(ctx, s)
	if err != nil {
		return resolvedTarget{}, fmt.Errorf("list nodes by selector: %w", err)
	}
	sortNodes(nodes)
	return resolvedTarget{Nodes: nodes, Universe: nodes}, nil
}

func (r *MaintenanceRequestReconciler) resolvePool(ctx context.Context, key, value string) (resolvedTarget, error) {
	if key == "" || value == "" {
		return resolvedTarget{}, fmt.Errorf("pool target requires spec.target.poolKey and poolValue")
	}
	s := labels.SelectorFromSet(labels.Set{key: value})
	nodes, err := r.kube.ListNodes(ctx, s)
	if err != nil {
		return resolvedTarget{}, fmt.Errorf("list nodes in pool %s=%s: %w", key, value, err)
	}
	sortNodes(nodes)
	return resolvedTarget{Nodes: nodes, Universe: nodes}, nil
}

func sortNodes(nodes []corev1.Node) {
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].Name < nodes[j].Name })
}
