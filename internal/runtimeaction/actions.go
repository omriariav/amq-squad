package runtimeaction

import (
	"strings"

	squadnamespace "github.com/omriariav/amq-squad/v2/internal/namespace"
	"github.com/omriariav/amq-squad/v2/internal/runtimecontrol"
)

type Action struct {
	Kind              string `json:"kind"`
	Label             string `json:"label"`
	Scope             string `json:"scope"`
	NamespaceID       string `json:"namespace_id,omitempty"`
	Command           string `json:"command"`
	Mutates           bool   `json:"mutates"`
	NeedsConfirmation bool   `json:"needs_confirmation"`
	Available         bool   `json:"available"`
	Reason            string `json:"reason,omitempty"`
	// Canonical action-object contract fields (v2.12.0).
	// ID mirrors Kind so consumers reading either field get the stable action
	// type identifier. ActionKind classifies execution semantics ("run" vs
	// "display"). UnavailableReason carries Reason when Available is false.
	// Existing consumers may continue to read Kind/NeedsConfirmation/Reason.
	ID                string `json:"id,omitempty"`
	ActionKind        string `json:"action_kind,omitempty"`
	UnavailableReason string `json:"unavailable_reason,omitempty"`
}

// classifyActionKind returns the canonical action_kind for a known action type.
// "run" means an executable CLI command (may mutate, check needs_confirmation).
// "display" means read-only or navigational and never mutates.
func classifyActionKind(kind string) string {
	switch kind {
	case "focus", "status", "resume_preview", "task_list", "thread", "attach_control":
		return "display"
	default:
		return "run"
	}
}

// ApplyCanonical fills the canonical contract fields (ID, ActionKind,
// UnavailableReason) on a slice of actions. It is called by each constructor
// so consumers always receive a fully-populated canonical object. Callers that
// modify Available/Reason after construction (e.g. policy filters) must call
// SyncUnavailableReason on the affected actions.
func ApplyCanonical(actions []Action) []Action {
	for i := range actions {
		actions[i].ID = actions[i].Kind
		actions[i].ActionKind = classifyActionKind(actions[i].Kind)
		if !actions[i].Available && actions[i].Reason != "" {
			actions[i].UnavailableReason = actions[i].Reason
		}
	}
	return actions
}

// SyncUnavailableReason updates UnavailableReason to match Reason when
// Available is false. Call this after mutating Available or Reason on an
// already-constructed action (e.g. inside a policy filter).
func SyncUnavailableReason(a *Action) {
	if !a.Available && a.Reason != "" {
		a.UnavailableReason = a.Reason
	} else if a.Available {
		a.UnavailableReason = ""
	}
}

func Member(projectDir, profile, session, role string, paneAlive bool) []Action {
	caps := runtimecontrol.ResolveEffectiveActions(
		runtimecontrol.TmuxCapabilities(paneAlive),
		runtimecontrol.DeliveryEvidence{DurableAMQ: true},
	)
	return MemberForCapabilities(projectDir, profile, session, role, caps)
}

func MemberForCapabilities(projectDir, profile, session, role string, caps runtimecontrol.Capabilities) []Action {
	scope := commandScope(projectDir, profile, session)
	namespaceID := squadnamespace.ID(profile, session)
	roleArg := " --role " + ShellQuote(role)
	focus := caps.State(runtimecontrol.CapabilityFocus)
	send := caps.State(runtimecontrol.CapabilitySendPrompt)
	goal := caps.State(runtimecontrol.CapabilityGoalDeliver)
	dispatch := caps.State(runtimecontrol.CapabilityDispatch)
	return ApplyCanonical([]Action{
		{Kind: "focus", Label: "focus pane", Scope: "agent", NamespaceID: namespaceID, Mutates: false, NeedsConfirmation: false, Available: focus.Available, Reason: focus.Reason, Command: "amq-squad focus" + scope + roleArg},
		{Kind: "send", Label: "send a prompt", Scope: "agent", NamespaceID: namespaceID, Mutates: true, NeedsConfirmation: true, Available: send.Available, Reason: send.Reason, Command: "amq-squad send" + scope + roleArg + " --body-file -"},
		{Kind: "goal_deliver", Label: "deliver native /goal", Scope: "agent", NamespaceID: namespaceID, Mutates: true, NeedsConfirmation: true, Available: goal.Available, Reason: goal.Reason, Command: "amq-squad goal deliver" + scope + roleArg + " --goal <goal>"},
		{Kind: "dispatch", Label: "dispatch task", Scope: "agent", NamespaceID: namespaceID, Mutates: true, NeedsConfirmation: true, Available: dispatch.Available, Reason: dispatch.Reason, Command: "amq-squad dispatch" + scope + roleArg + " --subject <subject> --body-file <file>"},
		{Kind: "resume", Label: "resume session", Scope: "session", NamespaceID: namespaceID, Mutates: true, NeedsConfirmation: true, Available: true, Command: "amq-squad resume" + scope + " --exec"},
		{Kind: "status", Label: "show session status", Scope: "session", NamespaceID: namespaceID, Mutates: false, NeedsConfirmation: false, Available: true, Command: "amq-squad status" + scope + " --json"},
		{Kind: "task_list", Label: "list tasks", Scope: "session", NamespaceID: namespaceID, Mutates: false, NeedsConfirmation: false, Available: true, Command: "amq-squad task list" + scope},
	})
}

func Session(projectDir, profile, session, tmuxSession string) []Action {
	scope := commandScope(projectDir, profile, session)
	namespaceID := squadnamespace.ID(profile, session)
	actions := []Action{
		{Kind: "status", Label: "show session status", Scope: "session", NamespaceID: namespaceID, Mutates: false, NeedsConfirmation: false, Available: true, Command: "amq-squad status" + scope + " --json"},
		{Kind: "resume_preview", Label: "preview resume plan", Scope: "session", NamespaceID: namespaceID, Mutates: false, NeedsConfirmation: false, Available: true, Command: "amq-squad resume" + scope + " --json"},
		{Kind: "resume_current_window", Label: "resume in current window", Scope: "session", NamespaceID: namespaceID, Mutates: true, NeedsConfirmation: true, Available: true, Command: "amq-squad resume" + scope + " --exec --target current-window"},
		{Kind: "resume_new_session", Label: "resume in new tmux session", Scope: "session", NamespaceID: namespaceID, Mutates: true, NeedsConfirmation: true, Available: true, Command: "amq-squad resume" + scope + " --exec --target new-session"},
		{Kind: "stop", Label: "stop the session", Scope: "session", NamespaceID: namespaceID, Mutates: true, NeedsConfirmation: true, Available: true, Command: "amq-squad stop" + scope + " --all"},
		{Kind: "stop_close_panes", Label: "stop session and close managed panes", Scope: "session", NamespaceID: namespaceID, Mutates: true, NeedsConfirmation: true, Available: true, Command: "amq-squad stop" + scope + " --all --close-panes"},
		{Kind: "task_list", Label: "list tasks", Scope: "session", NamespaceID: namespaceID, Mutates: false, NeedsConfirmation: false, Available: true, Command: "amq-squad task list" + scope},
	}
	if tmuxSession != "" {
		actions = append(actions, Action{
			Kind:              "attach_control",
			Label:             "open in iTerm2 (tmux -CC)",
			Scope:             "session",
			NamespaceID:       namespaceID,
			Mutates:           false,
			NeedsConfirmation: false,
			Available:         true,
			Command:           "tmux -CC attach -t " + ShellQuote(tmuxSession),
		})
	}
	return ApplyCanonical(actions)
}

func Thread(projectDir, profile, session, threadID string) []Action {
	scope := commandScope(projectDir, profile, session)
	namespaceID := squadnamespace.ID(profile, session)
	return ApplyCanonical([]Action{
		{Kind: "thread", Label: "read thread", Scope: "thread", NamespaceID: namespaceID, Mutates: false, NeedsConfirmation: false, Available: true, Command: "amq-squad thread" + scope + " --id " + ShellQuote(threadID)},
		{Kind: "task_list", Label: "list tasks", Scope: "session", NamespaceID: namespaceID, Mutates: false, NeedsConfirmation: false, Available: true, Command: "amq-squad task list" + scope},
	})
}

func commandScope(projectDir, profile, session string) string {
	scope := ""
	if strings.TrimSpace(projectDir) != "" {
		scope += " --project " + ShellQuote(projectDir)
	}
	if profile != "" && profile != "default" {
		scope += " --profile " + ShellQuote(profile)
	}
	if strings.TrimSpace(session) != "" {
		scope += " --session " + ShellQuote(session)
	}
	return scope
}

func ShellQuote(s string) string {
	if s == "" {
		return "''"
	}
	if !strings.ContainsAny(s, " \t'\"\\$`*?[]{}();&|<>~#") {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
