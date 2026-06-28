package runtimeaction

import (
	"strings"

	squadnamespace "github.com/omriariav/amq-squad/v2/internal/namespace"
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
}

func Member(projectDir, profile, session, role string, paneAlive bool) []Action {
	scope := commandScope(projectDir, profile, session)
	namespaceID := squadnamespace.ID(profile, session)
	roleArg := " --role " + ShellQuote(role)
	deadReason := ""
	if !paneAlive {
		deadReason = "agent pane is not live"
	}
	return []Action{
		{Kind: "focus", Label: "focus pane", Scope: "agent", NamespaceID: namespaceID, Mutates: false, NeedsConfirmation: false, Available: paneAlive, Reason: deadReason, Command: "amq-squad focus" + scope + roleArg},
		{Kind: "send", Label: "send a prompt", Scope: "agent", NamespaceID: namespaceID, Mutates: true, NeedsConfirmation: true, Available: paneAlive, Reason: deadReason, Command: "amq-squad send" + scope + roleArg + " --body-file -"},
		{Kind: "dispatch", Label: "dispatch task", Scope: "agent", NamespaceID: namespaceID, Mutates: true, NeedsConfirmation: true, Available: true, Command: "amq-squad dispatch" + scope + roleArg + " --subject <subject> --body-file <file>"},
		{Kind: "resume", Label: "resume session", Scope: "session", NamespaceID: namespaceID, Mutates: true, NeedsConfirmation: true, Available: true, Command: "amq-squad resume" + scope + " --exec"},
		{Kind: "status", Label: "show session status", Scope: "session", NamespaceID: namespaceID, Mutates: false, NeedsConfirmation: false, Available: true, Command: "amq-squad status" + scope + " --json"},
		{Kind: "task_list", Label: "list tasks", Scope: "session", NamespaceID: namespaceID, Mutates: false, NeedsConfirmation: false, Available: true, Command: "amq-squad task list" + scope},
	}
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
	return actions
}

func Thread(projectDir, profile, session, threadID string) []Action {
	scope := commandScope(projectDir, profile, session)
	namespaceID := squadnamespace.ID(profile, session)
	return []Action{
		{Kind: "thread", Label: "read thread", Scope: "thread", NamespaceID: namespaceID, Mutates: false, NeedsConfirmation: false, Available: true, Command: "amq-squad thread" + scope + " --id " + ShellQuote(threadID)},
		{Kind: "task_list", Label: "list tasks", Scope: "session", NamespaceID: namespaceID, Mutates: false, NeedsConfirmation: false, Available: true, Command: "amq-squad task list" + scope},
	}
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
