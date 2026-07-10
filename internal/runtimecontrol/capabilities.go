package runtimecontrol

import "strings"

const (
	BackendTmux   = "tmux"
	BackendITerm2 = "iterm2"
)

const ITerm2InjectionDisabledReason = "iTerm2 prompt/native-goal injection is disabled until #374 proves safe send/capture/busy support"

type Capability string

const (
	CapabilityFocus       Capability = "focus"
	CapabilitySendPrompt  Capability = "send_prompt"
	CapabilityGoalDeliver Capability = "goal_deliver"
	CapabilityDispatch    Capability = "dispatch"
)

type CapabilityState struct {
	Available bool
	Reason    string
}

type Capabilities struct {
	states map[Capability]CapabilityState
}

func NewCapabilities(states map[Capability]CapabilityState) Capabilities {
	out := Capabilities{states: make(map[Capability]CapabilityState, len(states))}
	for cap, state := range states {
		out.states[cap] = state
	}
	return out
}

func (c Capabilities) State(cap Capability) CapabilityState {
	if c.states != nil {
		if state, ok := c.states[cap]; ok {
			return state
		}
	}
	return CapabilityState{Available: false, Reason: "runtime capability unavailable"}
}

func (c Capabilities) With(cap Capability, available bool, reason string) Capabilities {
	states := make(map[Capability]CapabilityState, len(c.states)+1)
	for k, v := range c.states {
		states[k] = v
	}
	states[cap] = CapabilityState{Available: available, Reason: reason}
	return Capabilities{states: states}
}

func TmuxCapabilities(paneAlive bool) Capabilities {
	deadReason := ""
	if !paneAlive {
		deadReason = "agent pane is not live"
	}
	return NewCapabilities(map[Capability]CapabilityState{
		CapabilityFocus:       {Available: paneAlive, Reason: deadReason},
		CapabilitySendPrompt:  {Available: paneAlive, Reason: deadReason},
		CapabilityGoalDeliver: {Available: paneAlive, Reason: deadReason},
		CapabilityDispatch:    {Available: true},
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
		CapabilityGoalDeliver: {Available: false, Reason: ITerm2InjectionDisabledReason},
		CapabilityDispatch:    {Available: true},
	})
}
