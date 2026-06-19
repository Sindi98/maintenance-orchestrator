package planner_test

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/Sindi98/maintenance-orchestrator/api/v1alpha1"
	"github.com/Sindi98/maintenance-orchestrator/internal/kube"
	"github.com/Sindi98/maintenance-orchestrator/internal/planner"
)

func node(name string, labels map[string]string) corev1.Node {
	return corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: name, Labels: labels}}
}

func batchNodes(batches []v1alpha1.Batch) [][]string {
	out := make([][]string, len(batches))
	for i := range batches {
		out[i] = batches[i].Nodes
	}
	return out
}

func equal(a, b [][]string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if len(a[i]) != len(b[i]) {
			return false
		}
		for j := range a[i] {
			if a[i][j] != b[i][j] {
				return false
			}
		}
	}
	return true
}

func TestBuildBatchesSerial(t *testing.T) {
	nodes := []corev1.Node{node("c", nil), node("a", nil), node("b", nil)}
	got := planner.BuildBatches(v1alpha1.StrategySerial, nodes, 0, 1, nil)
	want := [][]string{{"a"}, {"b"}, {"c"}}
	if !equal(batchNodes(got), want) {
		t.Fatalf("serial = %v, want %v", batchNodes(got), want)
	}
	for i := range got {
		if int(got[i].Index) != i {
			t.Errorf("batch %d index = %d", i, got[i].Index)
		}
	}
}

func TestBuildBatchesBatched(t *testing.T) {
	nodes := []corev1.Node{node("n5", nil), node("n1", nil), node("n3", nil), node("n2", nil), node("n4", nil)}
	got := planner.BuildBatches(v1alpha1.StrategyBatched, nodes, 2, 2, nil)
	want := [][]string{{"n1", "n2"}, {"n3", "n4"}, {"n5"}}
	if !equal(batchNodes(got), want) {
		t.Fatalf("batched = %v, want %v", batchNodes(got), want)
	}
}

func TestBuildBatchesByZone(t *testing.T) {
	z := func(zone string) map[string]string { return map[string]string{kube.LabelZone: zone} }
	nodes := []corev1.Node{
		node("b", z("z2")),
		node("a", z("z1")),
		node("c", z("z2")),
		node("d", z("z1")),
	}
	got := planner.BuildBatches(v1alpha1.StrategyByZone, nodes, 0, 0, nil)
	want := [][]string{{"a", "d"}, {"b", "c"}}
	if !equal(batchNodes(got), want) {
		t.Fatalf("byZone = %v, want %v", batchNodes(got), want)
	}
	if got[0].Group != "z1" || got[1].Group != "z2" {
		t.Errorf("groups = %q,%q want z1,z2", got[0].Group, got[1].Group)
	}
}

func TestBuildBatchesByPool(t *testing.T) {
	p := func(v string) map[string]string { return map[string]string{"pool": v} }
	nodes := []corev1.Node{
		node("x", p("blue")),
		node("y", p("green")),
		node("z", p("blue")),
		node("w", nil), // unpooled
	}
	got := planner.BuildBatches(v1alpha1.StrategyByPool, nodes, 0, 0, []string{"pool"})
	want := [][]string{{"x", "z"}, {"y"}, {"w"}} // groups sorted: blue, green, unpooled
	if !equal(batchNodes(got), want) {
		t.Fatalf("byPool = %v, want %v", batchNodes(got), want)
	}
}

func TestScore(t *testing.T) {
	if s, _ := planner.Score(1, v1alpha1.ImpactEstimate{}, false, 1); s != 0 {
		t.Errorf("baseline score = %d, want 0", s)
	}
	if s, factors := planner.Score(1, v1alpha1.ImpactEstimate{}, true, 1); s != 30 || len(factors) == 0 {
		t.Errorf("control-plane score = %d (factors %v), want 30 with factors", s, factors)
	}
	max := v1alpha1.ImpactEstimate{SingleReplicaWorkloads: 10, EmptyDirPods: 10, PodsToEvict: 100}
	if s, _ := planner.Score(10, max, true, 5); s != 100 {
		t.Errorf("over-max score = %d, want clamped 100", s)
	}
	low, _ := planner.Score(2, v1alpha1.ImpactEstimate{}, false, 1)
	high, _ := planner.Score(2, v1alpha1.ImpactEstimate{}, false, 3)
	if high <= low {
		t.Errorf("higher concurrency should raise score: low=%d high=%d", low, high)
	}
}
