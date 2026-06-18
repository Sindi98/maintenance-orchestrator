package kube

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// GetNode fetches a single node by name.
func (c *Client) GetNode(ctx context.Context, name string) (*corev1.Node, error) {
	node := &corev1.Node{}
	if err := c.Get(ctx, types.NamespacedName{Name: name}, node); err != nil {
		return nil, err
	}
	return node, nil
}

// ListNodes returns nodes matching the given label selector. A nil or empty
// selector returns all nodes.
func (c *Client) ListNodes(ctx context.Context, sel labels.Selector) ([]corev1.Node, error) {
	list := &corev1.NodeList{}
	var opts []client.ListOption
	if sel != nil && !sel.Empty() {
		opts = append(opts, client.MatchingLabelsSelector{Selector: sel})
	}
	if err := c.List(ctx, list, opts...); err != nil {
		return nil, err
	}
	return list.Items, nil
}

// ListAllNodes returns every node in the cluster.
func (c *Client) ListAllNodes(ctx context.Context) ([]corev1.Node, error) {
	return c.ListNodes(ctx, labels.Everything())
}

// Cordon marks a node unschedulable. It is a no-op (nil error) if the node is
// already cordoned.
func (c *Client) Cordon(ctx context.Context, node *corev1.Node) error {
	if node.Spec.Unschedulable {
		return nil
	}
	patch := client.MergeFrom(node.DeepCopy())
	node.Spec.Unschedulable = true
	return c.Patch(ctx, node, patch)
}

// Uncordon marks a node schedulable again. It is a no-op if already schedulable.
func (c *Client) Uncordon(ctx context.Context, node *corev1.Node) error {
	if !node.Spec.Unschedulable {
		return nil
	}
	patch := client.MergeFrom(node.DeepCopy())
	node.Spec.Unschedulable = false
	return c.Patch(ctx, node, patch)
}

// IsControlPlane reports whether the node carries any of the given control-plane
// label keys.
func IsControlPlane(node *corev1.Node, labelKeys []string) bool {
	for _, k := range labelKeys {
		if _, ok := node.Labels[k]; ok {
			return true
		}
	}
	return false
}

// IsReady reports whether the node's Ready condition is True.
func IsReady(node *corev1.Node) bool {
	for _, cond := range node.Status.Conditions {
		if cond.Type == corev1.NodeReady {
			return cond.Status == corev1.ConditionTrue
		}
	}
	return false
}

// HasAnyLabel reports whether the node carries any of the given label keys.
func HasAnyLabel(node *corev1.Node, keys []string) bool {
	for _, k := range keys {
		if _, ok := node.Labels[k]; ok {
			return true
		}
	}
	return false
}

// HasAnyTaint reports whether the node carries any taint whose key is in keys.
func HasAnyTaint(node *corev1.Node, keys []string) bool {
	keySet := make(map[string]struct{}, len(keys))
	for _, k := range keys {
		keySet[k] = struct{}{}
	}
	for _, t := range node.Spec.Taints {
		if _, ok := keySet[t.Key]; ok {
			return true
		}
	}
	return false
}

// NodeZone returns the node's topology zone, or "" if unset.
func NodeZone(node *corev1.Node) string {
	return node.Labels[LabelZone]
}

// PoolValue returns the first pool key/value found on the node among poolKeys,
// in order. ok is false if the node belongs to no known pool.
func PoolValue(node *corev1.Node, poolKeys []string) (key, value string, ok bool) {
	for _, k := range poolKeys {
		if v, found := node.Labels[k]; found && v != "" {
			return k, v, true
		}
	}
	return "", "", false
}

// IsMCOManaged reports whether the OpenShift Machine Config Operator is
// currently reconfiguring this node (state != Done, or current != desired
// config). Such nodes are skipped to avoid fighting the MCO.
func IsMCOManaged(node *corev1.Node) bool {
	a := node.Annotations
	if a == nil {
		return false
	}
	if state := a[annotationMCOState]; state != "" && state != "Done" {
		return true
	}
	cur, curOK := a[annotationMCOCurrent]
	des, desOK := a[annotationMCODesired]
	return curOK && desOK && cur != des
}
