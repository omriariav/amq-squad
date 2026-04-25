package cli

import (
	"strings"
	"testing"
)

func TestBuildBootstrapPrompt(t *testing.T) {
	got, err := buildBootstrapPrompt(bootstrapContext{
		Role:          "cto",
		Handle:        "cto",
		Binary:        "codex",
		Session:       "fresh-cto",
		CWD:           "/repo",
		Root:          "/repo/.agent-mail/fresh-cto",
		TeamRulesPath: "/repo/.amq-squad/team-rules.md",
		RolePath:      "/repo/.agent-mail/fresh-cto/agents/cto/role.md",
		LaunchPath:    "/repo/.agent-mail/fresh-cto/agents/cto/launch.json",
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"You are a fresh amq-squad agent.",
		"Role: cto",
		"Handle: cto",
		"Team rules: /repo/.amq-squad/team-rules.md",
		"Role file: /repo/.agent-mail/fresh-cto/agents/cto/role.md",
		"Launch record: /repo/.agent-mail/fresh-cto/agents/cto/launch.json",
		"Stop and wait for instructions.",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("bootstrap prompt missing %q in:\n%s", want, got)
		}
	}
}

func TestBuildBootstrapPromptWithoutRules(t *testing.T) {
	got, err := buildBootstrapPrompt(bootstrapContext{
		Handle: "claude",
		Binary: "claude",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "Team rules: not configured") {
		t.Errorf("bootstrap prompt should mention missing team rules:\n%s", got)
	}
	if !strings.Contains(got, "Role: (none)") {
		t.Errorf("bootstrap prompt should default empty role:\n%s", got)
	}
}
