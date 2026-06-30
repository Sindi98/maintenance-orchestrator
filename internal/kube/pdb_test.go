package kube

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func mkPDB(name string, matchLabels map[string]string, disruptionsAllowed int32) *policyv1.PodDisruptionBudget {
	return &policyv1.PodDisruptionBudget{
		ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: name},
		Spec:       policyv1.PodDisruptionBudgetSpec{Selector: &metav1.LabelSelector{MatchLabels: matchLabels}},
		Status:     policyv1.PodDisruptionBudgetStatus{DisruptionsAllowed: disruptionsAllowed},
	}
}

func mkPodWithLabels(labels map[string]string) *corev1.Pod {
	return &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "p", Labels: labels}}
}

// TestPDBForPodReturnsTightest verifies that when a pod matches several PDBs the
// blocking one is returned regardless of list order, so a non-blocking PDB does
// not mask a blocking one (the bug was returning the first selector match).
func TestPDBForPodReturnsTightest(t *testing.T) {
	pod := mkPodWithLabels(map[string]string{"app": "web", "tier": "frontend"})
	loose := mkPDB("loose", map[string]string{"app": "web"}, 5)       // not tight
	tight := mkPDB("tight", map[string]string{"tier": "frontend"}, 0) // tight (blocks)

	// Provide both orderings; the tight one must win either way.
	for _, order := range [][]*policyv1.PodDisruptionBudget{
		{loose, tight},
		{tight, loose},
	} {
		c := New(fakeClient(t, pod, order[0], order[1]))
		got, err := c.PDBForPod(context.Background(), pod)
		if err != nil {
			t.Fatalf("PDBForPod: %v", err)
		}
		if got == nil || got.Name != "tight" {
			t.Fatalf("PDBForPod returned %v, want the tight PDB", got)
		}
		if !PDBIsTight(got) {
			t.Error("returned PDB should be reported tight")
		}
	}
}

func TestPDBForPodNoMatchReturnsNil(t *testing.T) {
	pod := mkPodWithLabels(map[string]string{"app": "web"})
	other := mkPDB("other", map[string]string{"app": "db"}, 0)
	c := New(fakeClient(t, pod, other))
	got, err := c.PDBForPod(context.Background(), pod)
	if err != nil {
		t.Fatalf("PDBForPod: %v", err)
	}
	if got != nil {
		t.Errorf("PDBForPod = %v, want nil (no selector match)", got)
	}
}

func TestPDBForPodSingleLooseMatch(t *testing.T) {
	pod := mkPodWithLabels(map[string]string{"app": "web"})
	loose := mkPDB("loose", map[string]string{"app": "web"}, 3)
	c := New(fakeClient(t, pod, loose))
	got, err := c.PDBForPod(context.Background(), pod)
	if err != nil {
		t.Fatalf("PDBForPod: %v", err)
	}
	if got == nil || got.Name != "loose" {
		t.Fatalf("PDBForPod = %v, want the single matching PDB", got)
	}
}
