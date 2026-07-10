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
