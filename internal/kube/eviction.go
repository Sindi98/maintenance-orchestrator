package kube

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/Sindi98/maintenance-orchestrator/api/v1alpha1"
)

// Evict requests eviction of the pod via the stable policy/v1 Eviction API,
// which honors PodDisruptionBudgets server-side. A nil gracePeriodSeconds uses
// the pod's default termination grace period.
func (c *Client) Evict(ctx context.Context, pod *corev1.Pod, gracePeriodSeconds *int64) error {
	eviction := &policyv1.Eviction{
		ObjectMeta: metav1.ObjectMeta{
			Name:      pod.Name,
			Namespace: pod.Namespace,
		},
	}
	if gracePeriodSeconds != nil {
		eviction.DeleteOptions = &metav1.DeleteOptions{GracePeriodSeconds: gracePeriodSeconds}
	}
	return c.SubResource("eviction").Create(ctx, pod, eviction)
}

// ClassifyEvictionError maps an eviction error to a block-reason constant.
//
//   - nil or NotFound       -> "" (success: the pod is gone or being removed)
//   - TooManyRequests (429) -> v1alpha1.BlockPDB (a PDB is currently blocking)
//   - anything else         -> v1alpha1.BlockEvictionError
func ClassifyEvictionError(err error) string {
	if err == nil || apierrors.IsNotFound(err) {
		return ""
	}
	if apierrors.IsTooManyRequests(err) {
		return v1alpha1.BlockPDB
	}
	return v1alpha1.BlockEvictionError
}
