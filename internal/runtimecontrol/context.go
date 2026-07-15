package runtimecontrol

import "strings"

const HostContextSchemaVersion = 1

const (
	TierA           = "A"
	TierB           = "B"
	TierC           = "C"
	TierUnsupported = "unsupported"
)

const (
	OperationLaunchCurrentWindow = "launch_current_window"
	OperationLaunchNewWindow     = "launch_new_window"
	OperationLaunchNewSession    = "launch_new_session"
)

type ContextEvidence struct {
	Source string `json:"source"`
	Value  string `json:"value"`
}

type LegacyTmuxJSONPolicy struct {
	State         string `json:"state"`
	Authoritative string `json:"authoritative"`
	Removal       string `json:"removal"`
}

// HostContext is the backend-neutral, read-only terminal capability contract
// consumed by status/NOC and run-start surfaces. Evidence is deliberately
// normalized: it never publishes raw SSH endpoints, socket paths, or other
// environment values.
type HostContext struct {
	SchemaVersion int                        `json:"schema_version"`
	Backend       string                     `json:"backend"`
	HostProgram   string                     `json:"host_program"`
	InsideTmux    bool                       `json:"inside_tmux"`
	ControlMode   bool                       `json:"control_mode"`
	Remote        bool                       `json:"remote"`
	Tier          string                     `json:"tier"`
	Capabilities  map[string]CapabilityState `json:"capabilities"`
	Operations    map[string]CapabilityState `json:"operations"`
	Evidence      []ContextEvidence          `json:"evidence"`
	LegacyTmux    LegacyTmuxJSONPolicy       `json:"legacy_tmux_json"`
}

func TierForBackend(backend string) string {
	switch strings.TrimSpace(backend) {
	case BackendTmux:
		return TierA
	case BackendITerm2:
		return TierB
	case BackendTerminalApp:
		return TierC
	default:
		return TierUnsupported
	}
}

func DetectHostContext(environ []string, controlModeObserved bool) HostContext {
	env := environmentMap(environ)
	hostProgram := normalizeHostProgram(env["TERM_PROGRAM"])
	insideTmux := strings.TrimSpace(env["TMUX"]) != ""
	remote := firstPresent(env, "SSH_CONNECTION", "SSH_CLIENT", "SSH_TTY")
	backend := backendForHost(hostProgram, insideTmux)
	context := HostContext{
		SchemaVersion: HostContextSchemaVersion,
		Backend:       backend,
		HostProgram:   hostProgram,
		InsideTmux:    insideTmux,
		ControlMode:   insideTmux && controlModeObserved,
		Remote:        remote,
		Tier:          TierForBackend(backend),
		LegacyTmux: LegacyTmuxJSONPolicy{
			State:         "compatibility_alias",
			Authoritative: "records[].terminal",
			Removal:       "not_before_v3",
		},
	}
	context.Capabilities, context.Operations = hostCapabilityContract(context)
	context.Evidence = []ContextEvidence{
		{Source: "env:TERM_PROGRAM", Value: hostProgram},
		{Source: "env:TMUX", Value: presenceValue(insideTmux)},
		{Source: "tmux:list-clients", Value: controlModeValue(context)},
		{Source: "env:SSH_*", Value: presenceValue(remote)},
	}
	return context
}

func hostCapabilityContract(context HostContext) (map[string]CapabilityState, map[string]CapabilityState) {
	capabilities := map[string]CapabilityState{}
	operations := map[string]CapabilityState{}
	set := func(target map[string]CapabilityState, name string, available bool, reason string) {
		target[name] = normalizeCapabilityState(Capability(name), CapabilityState{Available: available, Reason: reason})
	}
	setState := func(target map[string]CapabilityState, name string, state CapabilityState) {
		target[name] = normalizeCapabilityState(Capability(name), state)
	}
	raw := []Capability{CapabilityFocus, CapabilitySendPrompt, CapabilityCapture, CapabilityBusyDetect, CapabilityLocalInput}
	switch context.Backend {
	case BackendTmux:
		set(operations, OperationLaunchCurrentWindow, true, "")
		set(operations, OperationLaunchNewWindow, true, "")
		set(operations, OperationLaunchNewSession, true, "")
		all := TmuxCapabilities(true)
		for _, capability := range raw {
			setState(capabilities, string(capability), all.State(capability))
		}
	case BackendITerm2:
		set(operations, OperationLaunchCurrentWindow, false, "iTerm2 native backend supports new-window launches only")
		set(operations, OperationLaunchNewWindow, !context.Remote, remoteGUIReason(context.Remote))
		set(operations, OperationLaunchNewSession, false, "iTerm2 native backend does not create tmux sessions")
		all := ITerm2Capabilities(Identity{WindowID: "host-capability"}, Liveness{AgentAlive: true, BinaryMatch: true})
		for _, capability := range raw {
			state := all.State(capability)
			if context.Remote && state.Available {
				state = CapabilityState{State: SupportUnsupported, ReasonCode: "remote_gui_unavailable", Reason: remoteGUIReason(true)}
			}
			setState(capabilities, string(capability), state)
		}
	case BackendTerminalApp:
		set(operations, OperationLaunchCurrentWindow, false, "Terminal.app native backend supports new-window launches only")
		set(operations, OperationLaunchNewWindow, !context.Remote, remoteGUIReason(context.Remote))
		set(operations, OperationLaunchNewSession, false, "Terminal.app native backend does not create tmux sessions")
		all := TerminalAppCapabilities()
		for _, capability := range raw {
			state := all.State(capability)
			if context.Remote && state.Available {
				state = CapabilityState{State: SupportUnsupported, ReasonCode: "remote_gui_unavailable", Reason: remoteGUIReason(true)}
			}
			setState(capabilities, string(capability), state)
		}
	default:
		reason := "host terminal is unsupported or could not be detected"
		for _, name := range []string{OperationLaunchCurrentWindow, OperationLaunchNewWindow, OperationLaunchNewSession} {
			operations[name] = normalizeCapabilityState(Capability(name), CapabilityState{State: SupportUnknown, ReasonCode: "host_terminal_unknown", Reason: reason})
		}
		for _, capability := range raw {
			capabilities[string(capability)] = normalizeCapabilityState(capability, CapabilityState{State: SupportUnknown, ReasonCode: "host_terminal_unknown", Reason: reason})
		}
	}
	return capabilities, operations
}

func environmentMap(environ []string) map[string]string {
	out := make(map[string]string, len(environ))
	for _, entry := range environ {
		key, value, ok := strings.Cut(entry, "=")
		if ok {
			out[key] = value
		}
	}
	return out
}

func normalizeHostProgram(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "iterm.app", "iterm2":
		return "iterm2"
	case "apple_terminal", "terminal.app":
		return "terminal_app"
	case "vscode":
		return "vscode"
	case "wezterm":
		return "wezterm"
	case "warpterminal", "warp":
		return "warp"
	case "":
		return "unknown"
	default:
		return "other"
	}
}

func backendForHost(hostProgram string, insideTmux bool) string {
	if insideTmux {
		return BackendTmux
	}
	switch hostProgram {
	case "iterm2":
		return BackendITerm2
	case "terminal_app":
		return BackendTerminalApp
	default:
		return "unknown"
	}
}

func firstPresent(env map[string]string, keys ...string) bool {
	for _, key := range keys {
		if strings.TrimSpace(env[key]) != "" {
			return true
		}
	}
	return false
}

func presenceValue(present bool) string {
	if present {
		return "present"
	}
	return "absent"
}

func controlModeValue(context HostContext) string {
	if !context.InsideTmux {
		return "not_applicable"
	}
	if context.ControlMode {
		return "observed"
	}
	return "not_observed"
}

func remoteGUIReason(remote bool) string {
	if remote {
		return "native GUI terminal control is unavailable in a remote SSH context"
	}
	return ""
}
