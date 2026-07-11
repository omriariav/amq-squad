package wizard

import (
	"reflect"
	"strings"
	"testing"
)

func TestSpecArgsStableAndPreviewOnly(t *testing.T) {
	s := Spec{
		Project:               "/tmp/my repo",
		Profile:               "review",
		Session:               "issue-393",
		Roles:                 "cto,qa",
		Binary:                "qa=claude",
		Model:                 "cto=gpt-5",
		Effort:                "cto=high,qa=medium",
		OperatorMode:          "separate_terminal",
		OperatorNotifications: true,
		CodexArgs:             "-c model_reasoning_effort=high",
		ClaudeArgs:            "--effort high",
		Lead:                  "cto",
		LeadMode:              "planner",
		Visibility:            "current",
		LayoutPreset:          "lead-left",
		LauncherPane:          "close-after-start",
		ExternalLead:          true,
		Goal:                  "ship it",
		SeedFrom:              "issue:393",
	}
	want := []string{
		"--project", "/tmp/my repo",
		"--profile", "review",
		"--session", "issue-393",
		"--roles", "cto,qa",
		"--binary", "qa=claude",
		"--model", "cto=gpt-5",
		"--effort", "cto=high,qa=medium",
		"--operator-mode", "separate_terminal",
		"--operator-notifications",
		"--codex-args", "-c model_reasoning_effort=high",
		"--claude-args", "--effort high",
		"--lead", "cto",
		"--lead-mode", "planner",
		"--visibility", "current",
		"--layout-preset", "lead-left",
		"--launcher-pane", "close-after-start",
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

func TestSpecGlobalArgsNeverLeakProjectRunFlags(t *testing.T) {
	s := Spec{
		Scope: "global", GlobalRoot: "/neutral", GlobalAgent: "codex", GlobalModel: "gpt",
		GlobalEffort: "high", GlobalCodexArgs: "--search", GlobalClaudeArgs: "--debug", GlobalWindow: "noc",
		Project: "/project", Profile: "release", Session: "issue-393", Roles: "cto,qa",
		Visibility: "current", LayoutPreset: "lead-left", LauncherPane: "close-after-start",
	}
	got := strings.Join(s.GlobalArgs(), " ")
	for _, want := range []string{"--root /neutral", "--agent codex", "--model gpt", "--codex-args --search -c model_reasoning_effort=high", "--name noc"} {
		if !strings.Contains(got, want) {
			t.Fatalf("global argv %q missing %q", got, want)
		}
	}
	for _, forbidden := range []string{"--project", "--profile", "--session", "--roles", "--visibility", "--layout-preset", "--launcher-pane"} {
		if strings.Contains(got, forbidden) {
			t.Fatalf("global argv leaked %q: %s", forbidden, got)
		}
	}
	if strings.Contains(got, "--claude-args") {
		t.Fatalf("inactive native args leaked: %s", got)
	}
}

func TestSpecGlobalClaudeEffortUsesOnlyClaudeNativeArgs(t *testing.T) {
	got := strings.Join((Spec{GlobalRoot: "/n", GlobalAgent: "claude", GlobalEffort: "medium", GlobalCodexArgs: "--search", GlobalClaudeArgs: "--chrome"}).GlobalArgs(), " ")
	if !strings.Contains(got, "--claude-args --chrome --effort medium") || strings.Contains(got, "--codex-args") {
		t.Fatalf("Claude global args = %s", got)
	}
}

func TestSpecArgsOmitsEmptyFields(t *testing.T) {
	got := (Spec{Project: "/repo", Session: "s"}).Args()
	want := []string{"--project", "/repo", "--session", "s"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Args() = %#v, want %#v", got, want)
	}
}

func TestSpecArgsOmitsLegacyUnspecifiedOperatorMode(t *testing.T) {
	got := (Spec{Project: "/repo", Session: "s", OperatorMode: "unspecified"}).Args()
	want := []string{"--project", "/repo", "--session", "s"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Args() = %#v, want %#v", got, want)
	}
}
