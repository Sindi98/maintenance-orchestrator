package kube

import (
	"context"

	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/sets"
)

// CapacityReport summarizes the heuristic, request-based capacity check used to
// estimate whether the cluster retains enough headroom after the removal nodes
// are drained. It is NOT a scheduler simulation.
type CapacityReport struct {
	AllocatableCPU  resource.Quantity
	AllocatableMem  resource.Quantity
	DemandCPU       resource.Quantity
	DemandMem       resource.Quantity
	HeadroomPercent int32
}

// ComputeHeadroom estimates remaining cluster headroom assuming the named nodes
// are removed. Demand is the sum of pod requests already running on the
// remaining nodes plus the requests of evictable pods on the removal nodes
// (which must reschedule elsewhere). Allocatable is summed over remaining
// schedulable, Ready nodes. When sel is non-empty, only matching nodes count as
// remaining capacity (policy scope).
//
// HeadroomPercent is the smaller of the CPU and memory headroom percentages,
// clamped to [-100, 100]; negative means the remaining nodes are over-committed.
func (c *Client) ComputeHeadroom(ctx context.Context, removal sets.Set[string], sel labels.Selector) (CapacityReport, error) {
	nodes, err := c.ListAllNodes(ctx)
	if err != nil {
		return CapacityReport{}, err
	}
	pods, err := c.ListAllPods(ctx)
	if err != nil {
		return CapacityReport{}, err
	}

	var allocCPU, allocMem, demandCPU, demandMem resource.Quantity
	remaining := sets.New[string]()

	for i := range nodes {
		n := &nodes[i]
		if removal.Has(n.Name) || n.Spec.Unschedulable || !IsReady(n) {
			continue
		}
		if sel != nil && !sel.Empty() && !sel.Matches(labels.Set(n.Labels)) {
			continue
		}
		if cpu := n.Status.Allocatable.Cpu(); cpu != nil {
			allocCPU.Add(*cpu)
		}
		if mem := n.Status.Allocatable.Memory(); mem != nil {
			allocMem.Add(*mem)
		}
		remaining.Insert(n.Name)
	}

	for i := range pods {
		p := &pods[i]
		if IsTerminated(p) {
			continue
		}
		onRemaining := remaining.Has(p.Spec.NodeName)
		reschedules := removal.Has(p.Spec.NodeName) && IsEvictable(p)
		if !onRemaining && !reschedules {
			continue
		}
		cpu, mem := PodRequests(p)
		demandCPU.Add(cpu)
		demandMem.Add(mem)
	}

	cpuPct := percentFree(allocCPU.MilliValue(), demandCPU.MilliValue())
	memPct := percentFree(allocMem.Value(), demandMem.Value())
	headroom := cpuPct
	if memPct < headroom {
		headroom = memPct
	}

	return CapacityReport{
		AllocatableCPU:  allocCPU,
		AllocatableMem:  allocMem,
		DemandCPU:       demandCPU,
		DemandMem:       demandMem,
		HeadroomPercent: headroom,
	}, nil
}

func percentFree(allocatable, demand int64) int32 {
	if allocatable <= 0 {
		return 0
	}
	p := ((allocatable - demand) * 100) / allocatable
	if p > 100 {
		p = 100
	}
	if p < -100 {
		p = -100
	}
	return int32(p)
}
