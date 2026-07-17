package runtimecontrol

import "strings"

const (
	BackendTmux        = "tmux"
	BackendITerm2      = "iterm2"
	BackendTerminalApp = "terminal_app"
)

const ITerm2InjectionDisabledReason = "iTerm2 native input is unavailable: #374 found no verified atomic send/submit and capture/busy contract"
const ITerm2CaptureDisabledReason = "iTerm2 native capture is unavailable: #374 found no verified read-only session capture contract"
const ITerm2BusyDisabledReason = "iTerm2 busy detection is unavailable: #374 found no verified capture primitive"
const ITerm2LocalInputDisabledReason = "iTerm2 local-input detection is unavailable: #374 found no verified capture/busy primitive"
const TerminalAppInjectionDisabledReason = "Terminal.app native input is unavailable: #375 found Accessibility permission and stable tab targeting cannot be verified non-interactively"
const TerminalAppCaptureDisabledReason = "Terminal.app capture is unavailable: #375 found no stable permission-independent tab output API"
const TerminalAppBusyDisabledReason = "Terminal.app busy detection is unavailable: #375 found no stable permission-independent tab state API"
const TerminalAppLocalInputDisabledReason = "Terminal.app local-input detection is unavailable: #375 found no verified capture/busy primitive"
const TerminalAppFocusDisabledReason = "Terminal.app focus requires stable window/tab addressing; manual focus is required in v2.18.0"

type Capability string

const (
	CapabilityFocus       Capability = "focus"
	CapabilitySendPrompt  Capability = "send_prompt"
	CapabilityGoalDeliver Capability = "goal_deliver"
	CapabilityDispatch    Capability = "dispatch"
	CapabilityCapture     Capability = "capture_output"
	CapabilityBusyDetect  Capability = "busy_detect"
	CapabilityLocalInput  Capability = "local_input_detect"
)

var KnownCapabilities = []Capability{
	CapabilityFocus,
	CapabilitySendPrompt,
	CapabilityGoalDeliver,
	CapabilityDispatch,
	CapabilityCapture,
	CapabilityBusyDetect,
	CapabilityLocalInput,
}

// RawCapabilities are controller/host primitives. Goal delivery and dispatch
// are effective member actions and must be resolved from delivery evidence.
var RawCapabilities = []Capability{
	CapabilityFocus,
	CapabilitySendPrompt,
	CapabilityCapture,
	CapabilityBusyDetect,
	CapabilityLocalInput,
}

type CapabilityState struct {
	State      string   `json:"state"`
	Available  bool     `json:"available"`
	ReasonCode string   `json:"reason_code,omitempty"`
	Reason     string   `json:"reason,omitempty"`
	Evidence   []string `json:"evidence,omitempty"`
}

const (
	SupportSupported   = "supported"
	SupportForceOnly   = "force_only"
	SupportUnsupported = "unsupported"
	SupportUnknown     = "unknown"
)

type Capabilities struct {
	states map[Capability]CapabilityState
}

func NewCapabilities(states map[Capability]CapabilityState) Capabilities {
	out := Capabilities{states: make(map[Capability]CapabilityState, len(states))}
	for cap, state := range states {
		out.states[cap] = normalizeCapabilityState(cap, state)
	}
	return out
}

func (c Capabilities) State(cap Capability) CapabilityState {
	if c.states != nil {
		if state, ok := c.states[cap]; ok {
			return normalizeCapabilityState(cap, state)
		}
	}
	return normalizeCapabilityState(cap, CapabilityState{State: SupportUnknown, ReasonCode: "capability_not_reported", Reason: "runtime capability unavailable"})
}

func (c Capabilities) With(cap Capability, available bool, reason string) Capabilities {
	states := make(map[Capability]CapabilityState, len(c.states)+1)
	for k, v := range c.states {
		states[k] = v
	}
	states[cap] = normalizeCapabilityState(cap, CapabilityState{Available: available, Reason: reason})
	return Capabilities{states: states}
}

func (c Capabilities) WithState(cap Capability, state CapabilityState) Capabilities {
	states := make(map[Capability]CapabilityState, len(c.states)+1)
	for k, v := range c.states {
		states[k] = v
	}
	states[cap] = normalizeCapabilityState(cap, state)
	return Capabilities{states: states}
}

// Snapshot exposes a copy suitable for JSON/API surfaces. The controller keeps
// its internal map private so callers cannot mutate shared capability state.
func (c Capabilities) Snapshot() map[string]CapabilityState {
	out := make(map[string]CapabilityState, len(KnownCapabilities))
	for _, capability := range KnownCapabilities {
		out[string(capability)] = c.State(capability)
	}
	return out
}

func (c Capabilities) RawSnapshot() map[string]CapabilityState {
	out := make(map[string]CapabilityState, len(RawCapabilities))
	for _, capability := range RawCapabilities {
		out[string(capability)] = c.State(capability)
	}
	return out
}

func UnavailableCapabilities(reason string) Capabilities {
	states := make(map[Capability]CapabilityState, len(KnownCapabilities))
	for _, capability := range KnownCapabilities {
		states[capability] = CapabilityState{Available: false, Reason: reason}
	}
	return NewCapabilities(states)
}

func UnknownCapabilities(reason string) Capabilities {
	states := make(map[Capability]CapabilityState, len(KnownCapabilities))
	for _, capability := range KnownCapabilities {
		states[capability] = CapabilityState{State: SupportUnknown, ReasonCode: "backend_unknown", Reason: reason}
	}
	return NewCapabilities(states)
}

func normalizeCapabilityState(capability Capability, state CapabilityState) CapabilityState {
	if state.State == "" {
		if state.Available {
			state.State = SupportSupported
		} else if state.Reason != "" {
			state.State = SupportUnsupported
		} else {
			state.State = SupportUnknown
		}
	}
	state.Available = state.State == SupportSupported
	if state.State == SupportSupported {
		state.ReasonCode = ""
		state.Reason = ""
		return state
	}
	if state.ReasonCode == "" {
		state.ReasonCode = string(capability) + "_" + state.State
	}
	if state.Reason == "" {
		state.Reason = "runtime capability is " + state.State
	}
	return state
}

func TmuxCapabilities(paneAlive bool) Capabilities {
	deadReason := ""
	if !paneAlive {
		deadReason = "agent pane is not live"
	}
	return NewCapabilities(map[Capability]CapabilityState{
		CapabilityFocus:       {Available: paneAlive, Reason: deadReason},
		CapabilitySendPrompt:  {Available: paneAlive, Reason: deadReason},
		CapabilityGoalDeliver: {State: SupportUnknown, ReasonCode: "effective_action_requires_member_evidence", Reason: "effective goal delivery requires member delivery evidence"},
		CapabilityDispatch:    {State: SupportUnknown, ReasonCode: "effective_action_requires_member_evidence", Reason: "effective dispatch requires member delivery evidence"},
		CapabilityCapture:     {Available: paneAlive, Reason: deadReason},
		CapabilityBusyDetect:  {Available: paneAlive, Reason: deadReason},
		CapabilityLocalInput:  {Available: paneAlive, Reason: deadReason},
	})
}

func ITerm2Capabilities(identity Identity, live Liveness) Capabilities {
	focusReason := ""
	focusAvailable := strings.TrimSpace(identity.WindowID) != "" && live.AgentAlive && live.BinaryMatch
	switch {
	case strings.TrimSpace(identity.WindowID) == "":
		focusReason = "iTerm2 window id is unavailable"
	case !live.AgentAlive || !live.BinaryMatch:
		focusReason = "iTerm2 focus requires verified agent PID liveness"
	}
	return NewCapabilities(map[Capability]CapabilityState{
		CapabilityFocus:       {Available: focusAvailable, Reason: focusReason},
		CapabilitySendPrompt:  {Available: false, Reason: ITerm2InjectionDisabledReason},
		CapabilityGoalDeliver: {State: SupportUnknown, ReasonCode: "effective_action_requires_member_evidence", Reason: "effective goal delivery requires member delivery evidence"},
		CapabilityDispatch:    {State: SupportUnknown, ReasonCode: "effective_action_requires_member_evidence", Reason: "effective dispatch requires member delivery evidence"},
		CapabilityCapture:     {Available: false, Reason: ITerm2CaptureDisabledReason},
		CapabilityBusyDetect:  {Available: false, Reason: ITerm2BusyDisabledReason},
		CapabilityLocalInput:  {Available: false, Reason: ITerm2LocalInputDisabledReason},
	})
}

func TerminalAppCapabilities() Capabilities {
	return NewCapabilities(map[Capability]CapabilityState{
		CapabilityFocus:       {Available: false, Reason: TerminalAppFocusDisabledReason},
		CapabilitySendPrompt:  {Available: false, Reason: TerminalAppInjectionDisabledReason},
		CapabilityGoalDeliver: {State: SupportUnknown, ReasonCode: "effective_action_requires_member_evidence", Reason: "effective goal delivery requires member delivery evidence"},
		CapabilityDispatch:    {State: SupportUnknown, ReasonCode: "effective_action_requires_member_evidence", Reason: "effective dispatch requires member delivery evidence"},
		CapabilityCapture:     {Available: false, Reason: TerminalAppCaptureDisabledReason},
		CapabilityBusyDetect:  {Available: false, Reason: TerminalAppBusyDisabledReason},
		CapabilityLocalInput:  {Available: false, Reason: TerminalAppLocalInputDisabledReason},
	})
}

// DeliveryEvidence is member-scoped evidence. It must not be inferred from the
// host program alone.
type DeliveryEvidence struct {
	DurableAMQ bool
}

// ResolveEffectiveActions derives squad actions from raw controller
// primitives plus evidence for the specific member. Missing evidence fails
// closed instead of turning an unknown native path into an availability claim.
func ResolveEffectiveActions(raw Capabilities, evidence DeliveryEvidence) Capabilities {
	resolved := raw
	goalEvidence := []string{}
	if raw.State(CapabilitySendPrompt).State == SupportSupported {
		goalEvidence = append(goalEvidence, "native_prompt")
	}
	if len(goalEvidence) > 0 {
		resolved = resolved.WithState(CapabilityGoalDeliver, CapabilityState{State: SupportSupported, Evidence: goalEvidence})
	} else {
		resolved = resolved.WithState(CapabilityGoalDeliver, CapabilityState{State: SupportUnsupported, ReasonCode: "goal_delivery_path_unavailable", Reason: "the current goal-deliver command requires a live native prompt target"})
	}

	dispatchEvidence := []string{}
	if evidence.DurableAMQ {
		dispatchEvidence = append(dispatchEvidence, "durable_amq")
	}
	if len(dispatchEvidence) > 0 {
		resolved = resolved.WithState(CapabilityDispatch, CapabilityState{State: SupportSupported, Evidence: dispatchEvidence})
	} else {
		resolved = resolved.WithState(CapabilityDispatch, CapabilityState{State: SupportUnsupported, ReasonCode: "dispatch_route_unverified", Reason: "no exact durable AMQ namespace and member mailbox route is verified"})
	}
	return resolved
}
