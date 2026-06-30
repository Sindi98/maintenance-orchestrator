package kube

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/Sindi98/maintenance-orchestrator/api/v1alpha1"
)

func TestResolveMachineAPI(t *testing.T) {
	cases := []struct {
		name string
		ann  map[string]string
		pref v1alpha1.MachineAPI
		want v1alpha1.MachineAPI
		ok   bool
	}{
		{"explicit openshift", nil, v1alpha1.MachineAPIOpenShift, v1alpha1.MachineAPIOpenShift, true},
		{"explicit capi", nil, v1alpha1.MachineAPIClusterAPI, v1alpha1.MachineAPIClusterAPI, true},
		{"auto -> openshift annotation", map[string]string{annOpenShiftMachine: "ns/m"}, v1alpha1.MachineAPIAuto, v1alpha1.MachineAPIOpenShift, true},
		{"auto -> capi annotation", map[string]string{annClusterAPIMachine: "m"}, v1alpha1.MachineAPIAuto, v1alpha1.MachineAPIClusterAPI, true},
		{"auto -> none", nil, v1alpha1.MachineAPIAuto, "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Annotations: c.ann}}
			got, ok := ResolveMachineAPI(node, c.pref)
			if got != c.want || ok != c.ok {
				t.Errorf("ResolveMachineAPI = (%q,%v), want (%q,%v)", got, ok, c.want, c.ok)
			}
		})
	}
}

func TestSplitNamespacedName(t *testing.T) {
	if ns, n, ok := splitNamespacedName("openshift-machine-api/worker-1"); !ok || ns != "openshift-machine-api" || n != "worker-1" {
		t.Errorf("split = (%q,%q,%v)", ns, n, ok)
	}
	for _, bad := range []string{"", "noslash", "/name", "ns/"} {
		if _, _, ok := splitNamespacedName(bad); ok {
			t.Errorf("split(%q) should be invalid", bad)
		}
	}
}

func TestKubeletVersionAndReadyAtVersion(t *testing.T) {
	n1 := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "n1"},
		Status: corev1.NodeStatus{
			NodeInfo:   corev1.NodeSystemInfo{KubeletVersion: "v1.30.2"},
			Conditions: []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionTrue}},
		},
	}
	n2 := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "n2"},
		Status: corev1.NodeStatus{
			NodeInfo:   corev1.NodeSystemInfo{KubeletVersion: "v1.29.5"},
			Conditions: []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionFalse}},
		},
	}
	if got := KubeletVersion(n1); got != "v1.30.2" {
		t.Errorf("KubeletVersion = %q", got)
	}

	c := New(fakeClient(t, n1, n2))
	if got, _ := c.CountReadyNodesAtVersion(context.Background(), "v1.30.2"); got != 1 {
		t.Errorf("CountReadyNodesAtVersion(v1.30.2) = %d, want 1", got)
	}
	// n2 is at v1.29.5 but NotReady, so it must not be counted.
	if got, _ := c.CountReadyNodesAtVersion(context.Background(), "v1.29.5"); got != 0 {
		t.Errorf("CountReadyNodesAtVersion(v1.29.5) = %d, want 0 (NotReady excluded)", got)
	}
}

func TestFindMachine(t *testing.T) {
	// OpenShift annotation fast path: no list required.
	osNode := &corev1.Node{ObjectMeta: metav1.ObjectMeta{
		Name:        "os-1",
		Annotations: map[string]string{annOpenShiftMachine: "openshift-machine-api/os-machine-1"},
	}}
	c := New(fakeClient(t))
	ref, err := c.FindMachine(context.Background(), osNode, v1alpha1.MachineAPIOpenShift)
	if err != nil {
		t.Fatalf("FindMachine openshift: %v", err)
	}
	if ref == nil || ref.Namespace != "openshift-machine-api" || ref.Name != "os-machine-1" {
		t.Fatalf("openshift fast path ref = %+v", ref)
	}

	// Cluster API providerID match via list.
	machine := newMachine(gvkClusterAPIMachine, "default", "capi-machine-1", "aws:///zone/i-123", "")
	capiNode := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "capi-1", Annotations: map[string]string{annClusterAPIMachine: "capi-machine-1"}},
		Spec:       corev1.NodeSpec{ProviderID: "aws:///zone/i-123"},
	}
	c2 := New(fakeClient(t, machine))
	ref2, err := c2.FindMachine(context.Background(), capiNode, v1alpha1.MachineAPIClusterAPI)
	if err != nil {
		t.Fatalf("FindMachine capi: %v", err)
	}
	if ref2 == nil || ref2.Name != "capi-machine-1" {
		t.Fatalf("capi providerID ref = %+v", ref2)
	}

	// No matching machine -> nil, nil.
	other := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "x"}, Spec: corev1.NodeSpec{ProviderID: "aws:///zone/i-999"}}
	ref3, err := c2.FindMachine(context.Background(), other, v1alpha1.MachineAPIClusterAPI)
	if err != nil {
		t.Fatalf("FindMachine no-match: %v", err)
	}
	if ref3 != nil {
		t.Errorf("expected no machine, got %+v", ref3)
	}
}

// --- helpers ---

func machineScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		t.Fatalf("add scheme: %v", err)
	}
	for _, g := range []struct{ group, version, kind string }{
		{"machine.openshift.io", "v1beta1", "Machine"},
		{"cluster.x-k8s.io", "v1beta1", "Machine"},
	} {
		single := schema.GroupVersionKind{Group: g.group, Version: g.version, Kind: g.kind}
		list := schema.GroupVersionKind{Group: g.group, Version: g.version, Kind: g.kind + "List"}
		scheme.AddKnownTypeWithName(single, &unstructured.Unstructured{})
		scheme.AddKnownTypeWithName(list, &unstructured.UnstructuredList{})
	}
	return scheme
}

func fakeClient(t *testing.T, objs ...client.Object) client.Client {
	t.Helper()
	return fake.NewClientBuilder().WithScheme(machineScheme(t)).WithObjects(objs...).Build()
}

func newMachine(gvk schema.GroupVersionKind, ns, name, providerID, nodeRef string) *unstructured.Unstructured {
	u := &unstructured.Unstructured{}
	u.SetGroupVersionKind(gvk)
	u.SetNamespace(ns)
	u.SetName(name)
	if providerID != "" {
		_ = unstructured.SetNestedField(u.Object, providerID, "spec", "providerID")
	}
	if nodeRef != "" {
		_ = unstructured.SetNestedField(u.Object, nodeRef, "status", "nodeRef", "name")
	}
	return u
}
