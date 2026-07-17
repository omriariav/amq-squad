package runtimecontrol

import "testing"

func TestTmuxControllerCapabilities(t *testing.T) {
	ctrl := TmuxController{}
	if ctrl.Backend() != BackendTmux {
		t.Fatalf("backend = %q, want %q", ctrl.Backend(), BackendTmux)
	}

	live := ctrl.Capabilities(Identity{Backend: BackendTmux, PaneID: "%1"}, Liveness{PaneAlive: true})
	if !live.State(CapabilityFocus).Available || !live.State(CapabilitySendPrompt).Available {
		t.Fatalf("live tmux capabilities should allow raw pane-scoped controls")
	}
	for _, capability := range []Capability{CapabilityGoalDeliver, CapabilityDispatch} {
		if state := live.State(capability); state.State != SupportUnknown || state.ReasonCode != "effective_action_requires_member_evidence" {
			t.Fatalf("raw controller must not claim effective %s: %+v", capability, state)
		}
	}

	dead := ctrl.Capabilities(Identity{Backend: BackendTmux, PaneID: "%1"}, Liveness{PaneAlive: false})
	for _, cap := range []Capability{CapabilityFocus, CapabilitySendPrompt} {
		state := dead.State(cap)
		if state.Available || state.Reason != "agent pane is not live" {
			t.Fatalf("%s dead-pane state = %+v", cap, state)
		}
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
	for _, cap := range []Capability{CapabilitySendPrompt} {
		state := caps.State(cap)
		if state.Available || state.Reason != ITerm2InjectionDisabledReason {
			t.Fatalf("%s state = %+v", cap, state)
		}
	}
	if state := caps.State(CapabilityGoalDeliver); state.State != SupportUnknown || state.Available {
		t.Fatalf("iTerm2 controller must defer effective goal delivery: %+v", state)
	}
	for capability, reason := range map[Capability]string{
		CapabilityCapture: ITerm2CaptureDisabledReason, CapabilityBusyDetect: ITerm2BusyDisabledReason, CapabilityLocalInput: ITerm2LocalInputDisabledReason,
	} {
		state := caps.State(capability)
		if state.State != SupportUnsupported || state.Available || state.Reason != reason || state.ReasonCode == "" {
			t.Fatalf("%s state = %+v", capability, state)
		}
	}
	if state := caps.State(CapabilityDispatch); state.State != SupportUnknown || state.Available {
		t.Fatalf("iTerm2 controller must defer effective dispatch: %+v", state)
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

func TestTerminalAppControllerCapabilities(t *testing.T) {
	ctrl := TerminalAppController{}
	if ctrl.Backend() != BackendTerminalApp {
		t.Fatalf("backend = %q, want %q", ctrl.Backend(), BackendTerminalApp)
	}

	caps := ctrl.Capabilities(Identity{Backend: BackendTerminalApp, WindowID: "401", TabID: "1"}, Liveness{AgentAlive: true, BinaryMatch: true})
	if state := caps.State(CapabilityFocus); state.Available || state.Reason != TerminalAppFocusDisabledReason {
		t.Fatalf("Terminal.app focus state = %+v", state)
	}
	for _, cap := range []Capability{CapabilitySendPrompt} {
		state := caps.State(cap)
		if state.Available || state.Reason != TerminalAppInjectionDisabledReason {
			t.Fatalf("%s state = %+v", cap, state)
		}
	}
	if state := caps.State(CapabilityGoalDeliver); state.State != SupportUnknown || state.Available {
		t.Fatalf("Terminal.app controller must defer effective goal delivery: %+v", state)
	}
	for capability, reason := range map[Capability]string{
		CapabilityCapture: TerminalAppCaptureDisabledReason, CapabilityBusyDetect: TerminalAppBusyDisabledReason, CapabilityLocalInput: TerminalAppLocalInputDisabledReason,
	} {
		state := caps.State(capability)
		if state.State != SupportUnsupported || state.Available || state.Reason != reason || state.ReasonCode == "" {
			t.Fatalf("%s state = %+v", capability, state)
		}
	}
	if state := caps.State(CapabilityDispatch); state.State != SupportUnknown || state.Available {
		t.Fatalf("Terminal.app controller must defer effective dispatch: %+v", state)
	}
}

func TestResolveEffectiveActionsRequiresMemberEvidence(t *testing.T) {
	raw := ITerm2Capabilities(Identity{WindowID: "101"}, Liveness{AgentAlive: true, BinaryMatch: true})
	withoutEvidence := ResolveEffectiveActions(raw, DeliveryEvidence{})
	for _, capability := range []Capability{CapabilityGoalDeliver, CapabilityDispatch} {
		state := withoutEvidence.State(capability)
		if state.State != SupportUnsupported || state.Available || state.ReasonCode == "" || state.Reason == "" {
			t.Fatalf("unevidenced %s must fail closed: %+v", capability, state)
		}
	}

	withAMQ := ResolveEffectiveActions(raw, DeliveryEvidence{DurableAMQ: true})
	if state := withAMQ.State(CapabilityGoalDeliver); state.State != SupportUnsupported || state.Available || state.ReasonCode != "goal_delivery_path_unavailable" {
		t.Fatalf("durable AMQ alone must not claim the current goal-deliver path: %+v", state)
	}
	if state := withAMQ.State(CapabilityDispatch); state.State != SupportSupported || !state.Available || len(state.Evidence) != 1 || state.Evidence[0] != "durable_amq" {
		t.Fatalf("AMQ-evidenced dispatch = %+v", state)
	}

	tmux := ResolveEffectiveActions(TmuxCapabilities(true), DeliveryEvidence{})
	if state := tmux.State(CapabilityGoalDeliver); state.State != SupportSupported || len(state.Evidence) != 1 || state.Evidence[0] != "native_prompt" {
		t.Fatalf("tmux native prompt should evidence goal delivery: %+v", state)
	}
	if state := tmux.State(CapabilityDispatch); state.State != SupportUnsupported {
		t.Fatalf("native prompt alone must not evidence durable dispatch: %+v", state)
	}
}

func TestDefaultRegistryIncludesTerminalAppController(t *testing.T) {
	ctrl, ok := DefaultRegistry().Lookup(BackendTerminalApp)
	if !ok {
		t.Fatalf("default registry missing Terminal.app controller")
	}
	if ctrl.Backend() != BackendTerminalApp {
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

func TestCapabilityStateEnumAndSnapshotAreStable(t *testing.T) {
	caps := NewCapabilities(map[Capability]CapabilityState{
		CapabilityFocus:      {Available: true},
		CapabilitySendPrompt: {State: SupportForceOnly, ReasonCode: "operator_override_required", Reason: "requires explicit operator override"},
	})
	if got := caps.State(CapabilityFocus); got.State != SupportSupported || !got.Available || got.Reason != "" {
		t.Fatalf("supported state = %+v", got)
	}
	if got := caps.State(CapabilitySendPrompt); got.State != SupportForceOnly || got.Available || got.ReasonCode != "operator_override_required" {
		t.Fatalf("force-only state = %+v", got)
	}
	missing := caps.Snapshot()[string(CapabilityLocalInput)]
	if missing.State != SupportUnknown || missing.Available || missing.ReasonCode == "" || missing.Reason == "" {
		t.Fatalf("missing capability must serialize as explicit unknown: %+v", missing)
	}
}
