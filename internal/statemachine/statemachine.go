// Package statemachine encodes the allowed MaintenanceRequest phase transitions
// as a single table, so the controller and the tests share one source of truth.
// It is pure (no cluster I/O) and therefore directly unit-testable.
package statemachine

import (
	"fmt"
	"sort"

	"github.com/Sindi98/maintenance-orchestrator/api/v1alpha1"
)

// transitions maps each phase to the phases it may move to. Staying in the same
// phase is always allowed (handled in CanTransition) and is not listed here.
var transitions = map[v1alpha1.Phase]map[v1alpha1.Phase]struct{}{
	v1alpha1.PhasePending: set(
		v1alpha1.PhaseValidating,
		v1alpha1.PhaseCancelled,
		v1alpha1.PhaseFailed,
	),
	v1alpha1.PhaseValidating: set(
		v1alpha1.PhaseAwaitingApproval,
		v1alpha1.PhasePlanned,
		v1alpha1.PhaseBlocked,
		// Validating completes directly when the target resolves to no nodes or
		// the request is a DryRun (the plan is the deliverable).
		v1alpha1.PhaseCompleted,
		v1alpha1.PhaseFailed,
		v1alpha1.PhaseCancelled,
	),
	v1alpha1.PhaseAwaitingApproval: set(
		v1alpha1.PhasePlanned,
		v1alpha1.PhaseExecuting,
		v1alpha1.PhasePaused,
		v1alpha1.PhaseCancelled,
		v1alpha1.PhaseFailed,
	),
	v1alpha1.PhasePlanned: set(
		v1alpha1.PhaseExecuting,
		v1alpha1.PhasePaused,
		v1alpha1.PhaseBlocked,
		// Planned completes directly when the (re)built plan covers no nodes.
		v1alpha1.PhaseCompleted,
		v1alpha1.PhaseCancelled,
		v1alpha1.PhaseFailed,
	),
	v1alpha1.PhaseExecuting: set(
		v1alpha1.PhaseAwaitingApproval,
		v1alpha1.PhasePaused,
		v1alpha1.PhaseBlocked,
		v1alpha1.PhaseCompleted,
		v1alpha1.PhaseFailed,
		v1alpha1.PhaseCancelled,
	),
	v1alpha1.PhasePaused: set(
		v1alpha1.PhaseExecuting,
		v1alpha1.PhasePlanned,
		// A plan-less paused request resumes by re-validating.
		v1alpha1.PhaseValidating,
		v1alpha1.PhaseAwaitingApproval,
		v1alpha1.PhaseCancelled,
		v1alpha1.PhaseFailed,
	),
	v1alpha1.PhaseBlocked: set(
		v1alpha1.PhaseValidating,
		v1alpha1.PhasePlanned,
		v1alpha1.PhaseExecuting,
		// A blocked request is interruptible, so it may be paused.
		v1alpha1.PhasePaused,
		v1alpha1.PhaseCompleted,
		v1alpha1.PhaseFailed,
		v1alpha1.PhaseCancelled,
	),
	v1alpha1.PhaseCompleted: {},
	v1alpha1.PhaseFailed:    {},
	v1alpha1.PhaseCancelled: {},
}

// terminalPhases is the set of phases from which no further transition occurs.
var terminalPhases = set(
	v1alpha1.PhaseCompleted,
	v1alpha1.PhaseFailed,
	v1alpha1.PhaseCancelled,
)

func set(phases ...v1alpha1.Phase) map[v1alpha1.Phase]struct{} {
	m := make(map[v1alpha1.Phase]struct{}, len(phases))
	for _, p := range phases {
		m[p] = struct{}{}
	}
	return m
}

// InitialPhase is the phase a freshly created request starts in.
func InitialPhase() v1alpha1.Phase { return v1alpha1.PhasePending }

// IsTerminal reports whether the phase is terminal.
func IsTerminal(p v1alpha1.Phase) bool {
	_, ok := terminalPhases[p]
	return ok
}

// IsActive reports whether the phase is known and non-terminal. The empty phase
// (object just created) is not yet active.
func IsActive(p v1alpha1.Phase) bool {
	if p == "" {
		return false
	}
	if _, known := transitions[p]; !known {
		return false
	}
	return !IsTerminal(p)
}

// CanTransition reports whether moving from -> to is allowed. Staying put is
// always allowed; an empty 'from' (no phase yet) may enter Pending or Validating.
func CanTransition(from, to v1alpha1.Phase) bool {
	if from == to {
		return true
	}
	if from == "" {
		return to == v1alpha1.PhasePending || to == v1alpha1.PhaseValidating
	}
	allowed, ok := transitions[from]
	if !ok {
		return false
	}
	_, ok = allowed[to]
	return ok
}

// Validate returns a non-nil error when the transition is not allowed.
func Validate(from, to v1alpha1.Phase) error {
	if !CanTransition(from, to) {
		return fmt.Errorf("illegal phase transition %q -> %q", from, to)
	}
	return nil
}

// AllowedFrom returns the phases reachable from p (excluding p), sorted for
// stable output.
func AllowedFrom(p v1alpha1.Phase) []v1alpha1.Phase {
	allowed := transitions[p]
	out := make([]v1alpha1.Phase, 0, len(allowed))
	for ph := range allowed {
		out = append(out, ph)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}
