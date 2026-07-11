// Package wizard contains the interactive front-end state for run start.
// It deliberately returns canonical flag arguments and never launches agents.
package wizard

import "strings"

// Spec is the headless, serializable answer model for a project run. Later UI
// adapters may add richer choices, but execution must always flow through Args
// and the existing run start parser.
type Spec struct {
	Scope                          string
	Project                        string
	Profile                        string
	Session                        string
	Roles                          string
	Binary                         string
	Model                          string
	Effort                         string
	OperatorMode                   string
	OperatorNotifications          bool
	OperatorNotificationsRequested bool
	OperatorNotificationsSet       bool
	CodexArgs                      string
	ClaudeArgs                     string
	Lead                           string
	LeadMode                       string
	Visibility                     string
	LayoutPreset                   string
	LauncherPane                   string
	ExternalLead                   bool
	Goal                           string
	SeedFrom                       string
	GlobalRoot                     string
	GlobalAgent                    string
	GlobalModel                    string
	GlobalEffort                   string
	GlobalCodexArgs                string
	GlobalClaudeArgs               string
	GlobalWindow                   string
}

// GlobalArgs renders only global-start flags. Project roster and topology
// fields can never leak into this argv.
func (s Spec) GlobalArgs() []string {
	args := make([]string, 0, 12)
	appendValue := func(name, value string) {
		if value = strings.TrimSpace(value); value != "" {
			args = append(args, name, value)
		}
	}
	appendValue("--root", s.GlobalRoot)
	appendValue("--agent", s.GlobalAgent)
	appendValue("--model", s.GlobalModel)
	effort := strings.ToLower(strings.TrimSpace(s.GlobalEffort))
	if effort == "automatic" {
		effort = ""
	}
	if strings.EqualFold(strings.TrimSpace(s.GlobalAgent), "codex") {
		native := strings.TrimSpace(s.GlobalCodexArgs)
		if effort != "" {
			native = strings.TrimSpace(native + " -c model_reasoning_effort=" + effort)
		}
		appendValue("--codex-args", native)
	} else {
		native := strings.TrimSpace(s.GlobalClaudeArgs)
		if effort != "" {
			native = strings.TrimSpace(native + " --effort " + effort)
		}
		appendValue("--claude-args", native)
	}
	appendValue("--name", s.GlobalWindow)
	return args
}

// Args renders the canonical run start argv in a stable order. It never emits
// --interactive or --go, which keeps this package preview-only by construction.
func (s Spec) Args() []string {
	args := make([]string, 0, 28)
	appendValue := func(name, value string) {
		if value = strings.TrimSpace(value); value != "" {
			args = append(args, name, value)
		}
	}
	appendValue("--project", s.Project)
	appendValue("--profile", s.Profile)
	appendValue("--session", s.Session)
	appendValue("--roles", s.Roles)
	appendValue("--binary", s.Binary)
	appendValue("--model", s.Model)
	appendValue("--effort", s.Effort)
	if strings.TrimSpace(s.OperatorMode) != "unspecified" {
		appendValue("--operator-mode", s.OperatorMode)
	}
	if s.OperatorNotifications {
		args = append(args, "--operator-notifications")
	}
	appendValue("--codex-args", s.CodexArgs)
	appendValue("--claude-args", s.ClaudeArgs)
	appendValue("--lead", s.Lead)
	appendValue("--lead-mode", s.LeadMode)
	appendValue("--visibility", s.Visibility)
	appendValue("--layout-preset", s.LayoutPreset)
	appendValue("--launcher-pane", s.LauncherPane)
	if s.ExternalLead {
		args = append(args, "--external-lead")
	}
	appendValue("--goal", s.Goal)
	appendValue("--seed-from", s.SeedFrom)
	return args
}
