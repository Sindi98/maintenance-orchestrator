package kube

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// PDBForPod returns the first PodDisruptionBudget in the pod's namespace whose
// selector matches the pod, or nil if none applies.
func (c *Client) PDBForPod(ctx context.Context, pod *corev1.Pod) (*policyv1.PodDisruptionBudget, error) {
	list := &policyv1.PodDisruptionBudgetList{}
	if err := c.List(ctx, list, client.InNamespace(pod.Namespace)); err != nil {
		return nil, err
	}
	podLabels := labels.Set(pod.Labels)
	for i := range list.Items {
		pdb := &list.Items[i]
		if pdb.Spec.Selector == nil {
			continue
		}
		sel, err := metav1.LabelSelectorAsSelector(pdb.Spec.Selector)
		if err != nil {
			continue
		}
		if sel.Matches(podLabels) {
			return pdb, nil
		}
	}
	return nil, nil
}

// PDBIsTight reports whether the PDB currently allows no voluntary disruptions
// (DisruptionsAllowed == 0). A nil PDB is not tight.
func PDBIsTight(pdb *policyv1.PodDisruptionBudget) bool {
	return pdb != nil && pdb.Status.DisruptionsAllowed == 0
}
