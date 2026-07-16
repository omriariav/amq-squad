package wizard

import (
	"fmt"
	"sort"
	"strings"
)

// TerminalOperation is the CLI-owned projection of one runtime terminal
// operation. The wizard renders this evidence but never infers a backend.
type TerminalOperation struct {
	State      string
	ReasonCode string
	Reason     string
}

// TerminalContext is the bounded diagnostic contract supplied by the CLI to
// both wizard adapters. It intentionally excludes raw environment values,
// socket paths, client addresses, and other host identifiers.
type TerminalContext struct {
	SchemaVersion int
	Backend       string
	HostProgram   string
	InsideTmux    bool
	ControlMode   bool
	Remote        bool
	Tier          string
	Operations    map[string]TerminalOperation
}

func (c TerminalContext) Available() bool {
	return c.SchemaVersion > 0 && strings.TrimSpace(c.Backend) != ""
}

func (c TerminalContext) Summary() string {
	if !c.Available() {
		return "Terminal context: unavailable; topology choices remain explicit"
	}
	return fmt.Sprintf("Terminal context: backend=%s host=%s tier=%s inside_tmux=%t control_mode=%t remote=%t",
		boundedTerminalValue(c.Backend), boundedTerminalValue(c.HostProgram), boundedTerminalValue(c.Tier), c.InsideTmux, c.ControlMode, c.Remote)
}

func (c TerminalContext) operationSummary() string {
	if len(c.Operations) == 0 {
		return "host_operations=unreported"
	}
	keys := make([]string, 0, len(c.Operations))
	for key := range c.Operations {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, boundedTerminalValue(key)+"="+boundedTerminalValue(c.Operations[key].State))
	}
	return "host_operations=" + strings.Join(parts, ",")
}

func boundedTerminalValue(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "unknown"
	}
	if len(value) > 64 {
		return "other"
	}
	for _, r := range value {
		if !(r == '_' || r == '-' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')) {
			return "other"
		}
	}
	return value
}

func recommendedTopology(current string, explicit bool, context TerminalContext) string {
	if explicit || !context.Available() {
		return defaultString(strings.TrimSpace(current), "sibling-tabs")
	}
	if !context.InsideTmux {
		return "detached"
	}
	return defaultString(strings.TrimSpace(current), "sibling-tabs")
}

func annotateTopologyChoice(context TerminalContext, visibility, label string) string {
	if !context.Available() {
		return label
	}
	if !context.InsideTmux {
		switch visibility {
		case "current", "sibling-tabs":
			return label + " · warning: requires a visible tmux pane; detected host recommends detached"
		case "detached":
			return label + " · recommended: starts a tmux session without requiring a visible tmux pane"
		}
	}
	if context.ControlMode && (visibility == "current" || visibility == "sibling-tabs") {
		return label + " · " + controlModeGuidance(context)
	}
	return label
}

func topologyDiagnostic(context TerminalContext, visibility string) string {
	if !context.Available() {
		return context.Summary()
	}
	guidance := "selected topology is compatible with the detected visible tmux context"
	if !context.InsideTmux {
		if visibility == "detached" {
			guidance = "detached is recommended because no visible tmux pane was detected"
		} else {
			guidance = "explicit selection retained; launch will require a visible tmux pane or a change to detached"
		}
	} else if context.ControlMode {
		guidance = controlModeGuidance(context)
	}
	return context.Summary() + "; " + context.operationSummary() + "; " + guidance
}

func controlModeGuidance(context TerminalContext) string {
	if strings.EqualFold(strings.TrimSpace(context.HostProgram), "iterm2") {
		return "iTerm2 tmux -CC detected; stagger/retry safeguards cover control-client pauses"
	}
	return "tmux control-mode client detected; stagger/retry safeguards cover control-client pauses"
}
