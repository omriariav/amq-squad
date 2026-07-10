package runtimecontrol

import "testing"

func TestTmuxControllerCapabilities(t *testing.T) {
	ctrl := TmuxController{}
	if ctrl.Backend() != BackendTmux {
		t.Fatalf("backend = %q, want %q", ctrl.Backend(), BackendTmux)
	}

	live := ctrl.Capabilities(Identity{Backend: BackendTmux, PaneID: "%1"}, Liveness{PaneAlive: true})
	if !live.State(CapabilityFocus).Available || !live.State(CapabilitySendPrompt).Available || !live.State(CapabilityGoalDeliver).Available {
		t.Fatalf("live tmux capabilities should allow pane-scoped controls")
	}
	if !live.State(CapabilityDispatch).Available {
		t.Fatalf("tmux dispatch should remain available independent of pane liveness")
	}

	dead := ctrl.Capabilities(Identity{Backend: BackendTmux, PaneID: "%1"}, Liveness{PaneAlive: false})
	for _, cap := range []Capability{CapabilityFocus, CapabilitySendPrompt, CapabilityGoalDeliver} {
		state := dead.State(cap)
		if state.Available || state.Reason != "agent pane is not live" {
			t.Fatalf("%s dead-pane state = %+v", cap, state)
		}
	}
	if !dead.State(CapabilityDispatch).Available {
		t.Fatalf("tmux dispatch must stay available for dead panes to preserve existing behavior")
	}
}

func TestDefaultRegistryIncludesTmuxController(t *testing.T) {
	ctrl, ok := DefaultRegistry().Lookup(BackendTmux)
	if !ok {
		t.Fatalf("default registry missing tmux controller")
	}
	if ctrl.Backend() != BackendTmux {
		t.Fatalf("lookup backend = %q", ctrl.Backend())
	}
}

func TestITerm2ControllerCapabilities(t *testing.T) {
	ctrl := ITerm2Controller{}
	if ctrl.Backend() != BackendITerm2 {
		t.Fatalf("backend = %q, want %q", ctrl.Backend(), BackendITerm2)
	}

	caps := ctrl.Capabilities(Identity{Backend: BackendITerm2, WindowID: "101"}, Liveness{AgentAlive: true, BinaryMatch: true})
	if !caps.State(CapabilityFocus).Available {
		t.Fatalf("iTerm2 focus should be available with a recorded window id")
	}
	for _, cap := range []Capability{CapabilitySendPrompt, CapabilityGoalDeliver} {
		state := caps.State(cap)
		if state.Available || state.Reason != ITerm2InjectionDisabledReason {
			t.Fatalf("%s state = %+v", cap, state)
		}
	}
	if !caps.State(CapabilityDispatch).Available {
		t.Fatalf("iTerm2 dispatch should remain available because durable AMQ dispatch does not require pane injection")
	}

	missing := ctrl.Capabilities(Identity{Backend: BackendITerm2}, Liveness{AgentAlive: true, BinaryMatch: true})
	if state := missing.State(CapabilityFocus); state.Available || state.Reason != "iTerm2 window id is unavailable" {
		t.Fatalf("missing-window focus state = %+v", state)
	}
	dead := ctrl.Capabilities(Identity{Backend: BackendITerm2, WindowID: "101"}, Liveness{})
	if state := dead.State(CapabilityFocus); state.Available || state.Reason != "iTerm2 focus requires verified agent PID liveness" {
		t.Fatalf("dead-agent focus state = %+v", state)
	}
}

func TestDefaultRegistryIncludesITerm2Controller(t *testing.T) {
	ctrl, ok := DefaultRegistry().Lookup(BackendITerm2)
	if !ok {
		t.Fatalf("default registry missing iTerm2 controller")
	}
	if ctrl.Backend() != BackendITerm2 {
		t.Fatalf("lookup backend = %q", ctrl.Backend())
	}
}

func TestZeroValueRegistryCanRegister(t *testing.T) {
	var r Registry
	r.Register(TmuxController{})
	ctrl, ok := r.Lookup(BackendTmux)
	if !ok {
		t.Fatalf("zero-value registry should accept registrations")
	}
	if ctrl.Backend() != BackendTmux {
		t.Fatalf("lookup backend = %q", ctrl.Backend())
	}
}
