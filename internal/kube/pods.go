package kube

import (
	"context"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// ListPodsOnNode returns all pods assigned to the named node.
//
// It relies on the "spec.nodeName" field index, which the controller registers
// on the manager's field indexer in SetupWithManager.
func (c *Client) ListPodsOnNode(ctx context.Context, nodeName string) ([]corev1.Pod, error) {
	list := &corev1.PodList{}
	if err := c.List(ctx, list, client.MatchingFields{IndexPodNodeName: nodeName}); err != nil {
		return nil, err
	}
	return list.Items, nil
}

// ListAllPods returns every pod in the cluster.
func (c *Client) ListAllPods(ctx context.Context) ([]corev1.Pod, error) {
	list := &corev1.PodList{}
	if err := c.List(ctx, list); err != nil {
		return nil, err
	}
	return list.Items, nil
}

// IsDaemonSetPod reports whether the pod is controlled by a DaemonSet.
func IsDaemonSetPod(pod *corev1.Pod) bool {
	ctrl := metav1.GetControllerOf(pod)
	return ctrl != nil && ctrl.Kind == "DaemonSet"
}

// IsMirrorPod reports whether the pod is the API mirror of a static pod.
func IsMirrorPod(pod *corev1.Pod) bool {
	_, ok := pod.Annotations[annotationMirrorPod]
	return ok
}

// IsTerminating reports whether the pod has a deletion timestamp set.
func IsTerminating(pod *corev1.Pod) bool {
	return pod.DeletionTimestamp != nil
}

// IsTerminated reports whether the pod has reached a terminal phase.
func IsTerminated(pod *corev1.Pod) bool {
	return pod.Status.Phase == corev1.PodSucceeded || pod.Status.Phase == corev1.PodFailed
}

// HasEmptyDir reports whether the pod mounts any emptyDir volume (data lost on eviction).
func HasEmptyDir(pod *corev1.Pod) bool {
	for i := range pod.Spec.Volumes {
		if pod.Spec.Volumes[i].EmptyDir != nil {
			return true
		}
	}
	return false
}

// HasHostPath reports whether the pod mounts any hostPath volume (node-local data).
func HasHostPath(pod *corev1.Pod) bool {
	for i := range pod.Spec.Volumes {
		if pod.Spec.Volumes[i].HostPath != nil {
			return true
		}
	}
	return false
}

// IsDrainBlocking reports whether the pod still occupies the node for drain
// purposes: not a DaemonSet pod, not a static/mirror pod, and not terminated.
// Terminating pods are still draining and therefore still blocking.
func IsDrainBlocking(pod *corev1.Pod) bool {
	return !IsDaemonSetPod(pod) && !IsMirrorPod(pod) && !IsTerminated(pod)
}

// IsEvictable reports whether the controller should actively POST an eviction
// for the pod: drain-blocking and not already terminating.
func IsEvictable(pod *corev1.Pod) bool {
	return IsDrainBlocking(pod) && !IsTerminating(pod)
}

// PodRequests returns the summed CPU and memory requests of the pod's regular
// containers (init containers are ignored for this heuristic).
func PodRequests(pod *corev1.Pod) (cpu, mem resource.Quantity) {
	for i := range pod.Spec.Containers {
		req := pod.Spec.Containers[i].Resources.Requests
		if req == nil {
			continue
		}
		if c := req.Cpu(); c != nil {
			cpu.Add(*c)
		}
		if m := req.Memory(); m != nil {
			mem.Add(*m)
		}
	}
	return cpu, mem
}

// Workload identifies the controller owning a pod and its declared replica count.
type Workload struct {
	Kind          string
	Namespace     string
	Name          string
	Replicas      int32
	ControllerUID types.UID
	IsController  bool
}

// ResolveWorkload walks ownerReferences to identify the controlling workload and
// its declared replica count. A bare pod is reported as a single-replica,
// non-controller workload. DaemonSet-owned pods report Replicas 0.
func (c *Client) ResolveWorkload(ctx context.Context, pod *corev1.Pod) (Workload, error) {
	ctrl := metav1.GetControllerOf(pod)
	if ctrl == nil {
		return Workload{Kind: "Pod", Namespace: pod.Namespace, Name: pod.Name, Replicas: 1}, nil
	}

	switch ctrl.Kind {
	case "ReplicaSet":
		rs := &appsv1.ReplicaSet{}
		if err := c.Get(ctx, types.NamespacedName{Namespace: pod.Namespace, Name: ctrl.Name}, rs); err != nil {
			if apierrors.IsNotFound(err) {
				return Workload{Kind: "ReplicaSet", Namespace: pod.Namespace, Name: ctrl.Name, Replicas: 1, ControllerUID: ctrl.UID, IsController: true}, nil
			}
			return Workload{}, err
		}
		if dctrl := metav1.GetControllerOf(rs); dctrl != nil && dctrl.Kind == "Deployment" {
			dep := &appsv1.Deployment{}
			if err := c.Get(ctx, types.NamespacedName{Namespace: pod.Namespace, Name: dctrl.Name}, dep); err == nil {
				return Workload{Kind: "Deployment", Namespace: dep.Namespace, Name: dep.Name, Replicas: int32OrDefault(dep.Spec.Replicas, 1), ControllerUID: dep.UID, IsController: true}, nil
			}
		}
		return Workload{Kind: "ReplicaSet", Namespace: rs.Namespace, Name: rs.Name, Replicas: int32OrDefault(rs.Spec.Replicas, 1), ControllerUID: rs.UID, IsController: true}, nil

	case "StatefulSet":
		ss := &appsv1.StatefulSet{}
		if err := c.Get(ctx, types.NamespacedName{Namespace: pod.Namespace, Name: ctrl.Name}, ss); err != nil {
			if apierrors.IsNotFound(err) {
				return Workload{Kind: "StatefulSet", Namespace: pod.Namespace, Name: ctrl.Name, Replicas: 1, ControllerUID: ctrl.UID, IsController: true}, nil
			}
			return Workload{}, err
		}
		return Workload{Kind: "StatefulSet", Namespace: ss.Namespace, Name: ss.Name, Replicas: int32OrDefault(ss.Spec.Replicas, 1), ControllerUID: ss.UID, IsController: true}, nil

	case "DaemonSet":
		return Workload{Kind: "DaemonSet", Namespace: pod.Namespace, Name: ctrl.Name, Replicas: 0, ControllerUID: ctrl.UID, IsController: true}, nil

	default:
		// Job, CronJob-spawned Job, or a custom controller.
		return Workload{Kind: ctrl.Kind, Namespace: pod.Namespace, Name: ctrl.Name, Replicas: 1, ControllerUID: ctrl.UID, IsController: true}, nil
	}
}

func int32OrDefault(p *int32, def int32) int32 {
	if p != nil {
		return *p
	}
	return def
}
