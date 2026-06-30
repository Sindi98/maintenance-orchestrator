package kube

import (
	"context"
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/Sindi98/maintenance-orchestrator/api/v1alpha1"
)

const (
	// annOpenShiftMachine is the node annotation OpenShift sets to the
	// "namespace/name" of the Machine backing the node.
	annOpenShiftMachine = "machine.openshift.io/machine"
	// annClusterAPIMachine is the node annotation Cluster API sets to the Machine name.
	annClusterAPIMachine = "cluster.x-k8s.io/machine"
)

var (
	gvkOpenShiftMachine  = schema.GroupVersionKind{Group: "machine.openshift.io", Version: "v1beta1", Kind: "Machine"}
	gvkClusterAPIMachine = schema.GroupVersionKind{Group: "cluster.x-k8s.io", Version: "v1beta1", Kind: "Machine"}
)

// MachineRef identifies a Machine object backing a node.
type MachineRef struct {
	GVK       schema.GroupVersionKind
	Namespace string
	Name      string
}

// String renders the ref for audit/logging.
func (r MachineRef) String() string {
	if r.Namespace != "" {
		return fmt.Sprintf("%s %s/%s", r.GVK.Kind, r.Namespace, r.Name)
	}
	return fmt.Sprintf("%s %s", r.GVK.Kind, r.Name)
}

// machineGVK maps an API preference to a concrete Machine GVK.
func machineGVK(api v1alpha1.MachineAPI) (schema.GroupVersionKind, bool) {
	switch api {
	case v1alpha1.MachineAPIOpenShift:
		return gvkOpenShiftMachine, true
	case v1alpha1.MachineAPIClusterAPI:
		return gvkClusterAPIMachine, true
	default:
		return schema.GroupVersionKind{}, false
	}
}

// ResolveMachineAPI returns the concrete Machine API for a node given the request
// preference. When pref is Auto it is inferred from node annotations (OpenShift
// first). ok is false when Auto cannot determine an API from the node.
func ResolveMachineAPI(node *corev1.Node, pref v1alpha1.MachineAPI) (v1alpha1.MachineAPI, bool) {
	switch pref {
	case v1alpha1.MachineAPIOpenShift, v1alpha1.MachineAPIClusterAPI:
		return pref, true
	}
	if node.Annotations[annOpenShiftMachine] != "" {
		return v1alpha1.MachineAPIOpenShift, true
	}
	if node.Annotations[annClusterAPIMachine] != "" {
		return v1alpha1.MachineAPIClusterAPI, true
	}
	return "", false
}

// FindMachine locates the Machine backing a node. It returns (nil, nil) when no
// Machine is found — including when the Machine CRD is not installed — so callers
// can treat "not found" uniformly. The match uses the canonical providerID link
// (falling back to status.nodeRef.name), with a fast path on the OpenShift node
// annotation which already carries the Machine's namespace and name.
func (c *Client) FindMachine(ctx context.Context, node *corev1.Node, pref v1alpha1.MachineAPI) (*MachineRef, error) {
	api, ok := ResolveMachineAPI(node, pref)
	if !ok {
		return nil, nil
	}
	gvk, _ := machineGVK(api)

	if api == v1alpha1.MachineAPIOpenShift {
		if ns, name, ok := splitNamespacedName(node.Annotations[annOpenShiftMachine]); ok {
			return &MachineRef{GVK: gvk, Namespace: ns, Name: name}, nil
		}
	}

	list := &unstructured.UnstructuredList{}
	list.SetGroupVersionKind(schema.GroupVersionKind{Group: gvk.Group, Version: gvk.Version, Kind: gvk.Kind + "List"})
	if err := c.reader().List(ctx, list); err != nil {
		if apimeta.IsNoMatchError(err) || apierrors.IsNotFound(err) {
			return nil, nil // Machine CRD not installed in this cluster.
		}
		return nil, fmt.Errorf("list %s: %w", gvk.Kind, err)
	}
	for i := range list.Items {
		m := &list.Items[i]
		pid, _, _ := unstructured.NestedString(m.Object, "spec", "providerID")
		nodeRef, _, _ := unstructured.NestedString(m.Object, "status", "nodeRef", "name")
		if (node.Spec.ProviderID != "" && pid == node.Spec.ProviderID) || nodeRef == node.Name {
			return &MachineRef{GVK: gvk, Namespace: m.GetNamespace(), Name: m.GetName()}, nil
		}
	}
	return nil, nil
}

// DeleteMachine deletes the referenced Machine. A missing Machine is a no-op.
func (c *Client) DeleteMachine(ctx context.Context, ref MachineRef) error {
	u := &unstructured.Unstructured{}
	u.SetGroupVersionKind(ref.GVK)
	u.SetNamespace(ref.Namespace)
	u.SetName(ref.Name)
	if err := c.Delete(ctx, u); err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("delete %s: %w", ref, err)
	}
	return nil
}

// KubeletVersion returns the node's reported kubelet version (e.g. "v1.30.2").
func KubeletVersion(node *corev1.Node) string {
	return node.Status.NodeInfo.KubeletVersion
}

// ReadyNodeAtVersion reports whether any node is Ready and reports exactly the
// given kubelet version. It is a best-effort post-check that a replacement node
// came up at the target version.
func (c *Client) ReadyNodeAtVersion(ctx context.Context, version string) (bool, error) {
	nodes, err := c.ListAllNodes(ctx)
	if err != nil {
		return false, err
	}
	for i := range nodes {
		if IsReady(&nodes[i]) && KubeletVersion(&nodes[i]) == version {
			return true, nil
		}
	}
	return false, nil
}

func splitNamespacedName(s string) (ns, name string, ok bool) {
	parts := strings.SplitN(s, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", false
	}
	return parts[0], parts[1], true
}
