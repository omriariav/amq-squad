package wizard

import (
	"reflect"
	"testing"
)

func TestSpecArgsStableAndPreviewOnly(t *testing.T) {
	s := Spec{
		Project:      "/tmp/my repo",
		Profile:      "review",
		Session:      "issue-393",
		Roles:        "cto,qa",
		Binary:       "qa=claude",
		Model:        "cto=gpt-5",
		CodexArgs:    "-c model_reasoning_effort=high",
		ClaudeArgs:   "--effort high",
		Lead:         "cto",
		LeadMode:     "planner",
		Visibility:   "current",
		ExternalLead: true,
		Goal:         "ship it",
		SeedFrom:     "issue:393",
	}
	want := []string{
		"--project", "/tmp/my repo",
		"--profile", "review",
		"--session", "issue-393",
		"--roles", "cto,qa",
		"--binary", "qa=claude",
		"--model", "cto=gpt-5",
		"--codex-args", "-c model_reasoning_effort=high",
		"--claude-args", "--effort high",
		"--lead", "cto",
		"--lead-mode", "planner",
		"--visibility", "current",
		"--external-lead",
		"--goal", "ship it",
		"--seed-from", "issue:393",
	}
	if got := s.Args(); !reflect.DeepEqual(got, want) {
		t.Fatalf("Args() = %#v, want %#v", got, want)
	}
	for _, arg := range s.Args() {
		if arg == "--go" || arg == "--interactive" {
			t.Fatalf("preview-only spec emitted forbidden flag %q", arg)
		}
	}
}

func TestSpecArgsOmitsEmptyFields(t *testing.T) {
	got := (Spec{Project: "/repo", Session: "s"}).Args()
	want := []string{"--project", "/repo", "--session", "s"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Args() = %#v, want %#v", got, want)
	}
}
