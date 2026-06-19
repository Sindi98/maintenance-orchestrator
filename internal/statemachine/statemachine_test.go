package statemachine_test

import (
	"testing"

	"github.com/Sindi98/maintenance-orchestrator/api/v1alpha1"
	sm "github.com/Sindi98/maintenance-orchestrator/internal/statemachine"
)

func TestInitialPhase(t *testing.T) {
	if got := sm.InitialPhase(); got != v1alpha1.PhasePending {
		t.Fatalf("InitialPhase() = %q, want %q", got, v1alpha1.PhasePending)
	}
}

func TestIsTerminal(t *testing.T) {
	cases := map[v1alpha1.Phase]bool{
		v1alpha1.PhaseCompleted:        true,
		v1alpha1.PhaseFailed:           true,
		v1alpha1.PhaseCancelled:        true,
		v1alpha1.PhasePending:          false,
		v1alpha1.PhaseValidating:       false,
		v1alpha1.PhaseAwaitingApproval: false,
		v1alpha1.PhasePlanned:          false,
		v1alpha1.PhaseExecuting:        false,
		v1alpha1.PhasePaused:           false,
		v1alpha1.PhaseBlocked:          false,
	}
	for phase, want := range cases {
		if got := sm.IsTerminal(phase); got != want {
			t.Errorf("IsTerminal(%q) = %v, want %v", phase, got, want)
		}
	}
}

func TestIsActive(t *testing.T) {
	if sm.IsActive("") {
		t.Error("empty phase must not be active")
	}
	if !sm.IsActive(v1alpha1.PhaseExecuting) {
		t.Error("Executing must be active")
	}
	if sm.IsActive(v1alpha1.PhaseCompleted) {
		t.Error("Completed must not be active")
	}
	if sm.IsActive("Bogus") {
		t.Error("unknown phase must not be active")
	}
}

func TestCanTransition(t *testing.T) {
	cases := []struct {
		name     string
		from, to v1alpha1.Phase
		want     bool
	}{
		{"new to pending", "", v1alpha1.PhasePending, true},
		{"new to validating", "", v1alpha1.PhaseValidating, true},
		{"new to executing illegal", "", v1alpha1.PhaseExecuting, false},
		{"pending to validating", v1alpha1.PhasePending, v1alpha1.PhaseValidating, true},
		{"validating to planned", v1alpha1.PhaseValidating, v1alpha1.PhasePlanned, true},
		{"validating to awaiting", v1alpha1.PhaseValidating, v1alpha1.PhaseAwaitingApproval, true},
		{"validating to blocked", v1alpha1.PhaseValidating, v1alpha1.PhaseBlocked, true},
		{"planned to executing", v1alpha1.PhasePlanned, v1alpha1.PhaseExecuting, true},
		{"executing to completed", v1alpha1.PhaseExecuting, v1alpha1.PhaseCompleted, true},
		{"executing to uncordon gate", v1alpha1.PhaseExecuting, v1alpha1.PhaseAwaitingApproval, true},
		{"paused to executing", v1alpha1.PhasePaused, v1alpha1.PhaseExecuting, true},
		{"blocked to executing", v1alpha1.PhaseBlocked, v1alpha1.PhaseExecuting, true},
		{"self transition", v1alpha1.PhaseExecuting, v1alpha1.PhaseExecuting, true},
		{"any to cancelled", v1alpha1.PhaseExecuting, v1alpha1.PhaseCancelled, true},
		// illegal jumps
		{"pending to executing", v1alpha1.PhasePending, v1alpha1.PhaseExecuting, false},
		{"validating to completed", v1alpha1.PhaseValidating, v1alpha1.PhaseCompleted, false},
		{"planned to completed", v1alpha1.PhasePlanned, v1alpha1.PhaseCompleted, false},
		{"completed to executing", v1alpha1.PhaseCompleted, v1alpha1.PhaseExecuting, false},
		{"failed to pending", v1alpha1.PhaseFailed, v1alpha1.PhasePending, false},
	}
	for _, c := range cases {
		if got := sm.CanTransition(c.from, c.to); got != c.want {
			t.Errorf("%s: CanTransition(%q,%q) = %v, want %v", c.name, c.from, c.to, got, c.want)
		}
	}
}

func TestValidate(t *testing.T) {
	if err := sm.Validate(v1alpha1.PhasePending, v1alpha1.PhaseValidating); err != nil {
		t.Errorf("legal transition returned error: %v", err)
	}
	if err := sm.Validate(v1alpha1.PhaseCompleted, v1alpha1.PhaseExecuting); err == nil {
		t.Error("illegal transition did not return an error")
	}
}

func TestTerminalPhasesHaveNoOutgoing(t *testing.T) {
	for _, p := range []v1alpha1.Phase{v1alpha1.PhaseCompleted, v1alpha1.PhaseFailed, v1alpha1.PhaseCancelled} {
		if got := sm.AllowedFrom(p); len(got) != 0 {
			t.Errorf("terminal phase %q has outgoing transitions: %v", p, got)
		}
	}
}
