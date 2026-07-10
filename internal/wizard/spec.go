// Package wizard contains the interactive front-end state for run start.
// It deliberately returns canonical flag arguments and never launches agents.
package wizard

import "strings"

// Spec is the headless, serializable answer model for a project run. Later UI
// adapters may add richer choices, but execution must always flow through Args
// and the existing run start parser.
type Spec struct {
	Project      string
	Profile      string
	Session      string
	Roles        string
	Binary       string
	Model        string
	Effort       string
	OperatorMode string
	CodexArgs    string
	ClaudeArgs   string
	Lead         string
	LeadMode     string
	Visibility   string
	ExternalLead bool
	Goal         string
	SeedFrom     string
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
	appendValue("--codex-args", s.CodexArgs)
	appendValue("--claude-args", s.ClaudeArgs)
	appendValue("--lead", s.Lead)
	appendValue("--lead-mode", s.LeadMode)
	appendValue("--visibility", s.Visibility)
	if s.ExternalLead {
		args = append(args, "--external-lead")
	}
	appendValue("--goal", s.Goal)
	appendValue("--seed-from", s.SeedFrom)
	return args
}
