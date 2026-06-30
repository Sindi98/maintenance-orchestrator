package kube

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// PDBForPod returns a PodDisruptionBudget in the pod's namespace whose selector
// matches the pod, or nil if none applies. When several PDBs match the same pod
// (which the API permits), the tightest one is returned — the first that
// currently allows no voluntary disruptions, else any match — so the caller is
// not misled by a non-blocking PDB when a blocking one also applies. PDB list
// order is not guaranteed, so returning the first match alone would be
// nondeterministic.
func (c *Client) PDBForPod(ctx context.Context, pod *corev1.Pod) (*policyv1.PodDisruptionBudget, error) {
	list := &policyv1.PodDisruptionBudgetList{}
	if err := c.List(ctx, list, client.InNamespace(pod.Namespace)); err != nil {
		return nil, err
	}
	podLabels := labels.Set(pod.Labels)
	var match *policyv1.PodDisruptionBudget
	for i := range list.Items {
		pdb := &list.Items[i]
		if pdb.Spec.Selector == nil {
			continue
		}
		sel, err := metav1.LabelSelectorAsSelector(pdb.Spec.Selector)
		if err != nil {
			continue
		}
		if !sel.Matches(podLabels) {
			continue
		}
		if PDBIsTight(pdb) {
			return pdb, nil
		}
		if match == nil {
			match = pdb
		}
	}
	return match, nil
}

// PDBIsTight reports whether the PDB currently allows no voluntary disruptions
// (DisruptionsAllowed == 0). A nil PDB is not tight.
func PDBIsTight(pdb *policyv1.PodDisruptionBudget) bool {
	return pdb != nil && pdb.Status.DisruptionsAllowed == 0
}
