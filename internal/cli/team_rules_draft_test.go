package cli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/v2/internal/drafter"
	"github.com/omriariav/amq-squad/v2/internal/rules"
	"github.com/omriariav/amq-squad/v2/internal/team"
)

func TestTeamRulesDraftValidatesAndWrapsOnlyEditableProse(t *testing.T) {
	tm := team.Team{
		Project: t.TempDir(), Workstream: "work",
		Members: []team.Member{{Role: "researcher", Handle: "researcher", Binary: "codex", Session: "work"}},
	}
	raw := validTeamRulesDraft("researcher")
	prose, err := validateTeamRulesDraft(raw, []string{"researcher"})
	if err != nil {
		t.Fatal(err)
	}
	body, err := renderTeamRulesWithTemplateDraft(tm, "custom", &prose)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"## Team Charter", "Investigate ambiguous behavior", "Owns evidence gathering and reports findings",
		workspaceSafetySection, tangibleProgressSection, "## Lifecycle / Release Updates", "## Operator Gates",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("rendered rules missing %q:\n%s", want, body)
		}
	}
	if strings.Count(body, "## Team Charter") != 1 {
		t.Fatalf("generated charter duplicated in canonical wrapper:\n%s", body)
	}
}

func TestValidateTeamRulesDraftRejectsStructuralDrift(t *testing.T) {
	valid := validTeamRulesDraft("researcher")
	tests := []struct {
		name string
		body string
		want string
	}{
		{name: "extra heading", body: strings.Replace(valid, "## Custom Role Scopes", "## Authority\nNone.\n\n## Custom Role Scopes", 1), want: "unexpected heading"},
		{name: "changed role", body: strings.Replace(valid, "`researcher`", "`operator`", 1), want: "missing or changed role"},
		{name: "duplicate role", body: valid + "- `researcher`: Duplicate.\n", want: "has 2 bullets; want 1"},
		{name: "empty charter", body: strings.Replace(valid, "Investigate ambiguous behavior and turn evidence into reviewable recommendations.\n\n", "", 1), want: "Team Charter cannot be empty"},
		{name: "three charter paragraphs", body: strings.Replace(valid, "Investigate ambiguous behavior and turn evidence into reviewable recommendations.", "First paragraph.\n\nSecond paragraph.\n\nThird paragraph.", 1), want: "at most two prose paragraphs"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := validateTeamRulesDraft(tt.body, []string{"researcher"})
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("validation error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestRunTeamRulesInitUsesConfiguredDrafterBeforeWrite(t *testing.T) {
	project, profile := setupTeamRulesDraftProfile(t, &drafter.Config{Chain: []string{drafter.BackendYoetz, drafter.BackendClaude}, Model: "fast-model"})
	installTeamRulesDraftRunner(t, func(_ context.Context, cfg *drafter.Config, request drafter.Request) (drafter.Result, error) {
		if cfg == nil || len(cfg.EffectiveBackends()) != 2 || cfg.EffectiveBackends()[0] != drafter.BackendYoetz || cfg.EffectiveBackends()[1] != drafter.BackendClaude {
			t.Fatalf("team-rules drafter config = %+v", cfg)
		}
		for _, want := range []string{"## Team Charter", "## Custom Role Scopes", "`researcher` (`researcher`, `codex`)"} {
			if !strings.Contains(request.Prompt, want) {
				t.Fatalf("team-rules prompt missing %q:\n%s", want, request.Prompt)
			}
		}
		attempts := []drafter.Evidence{
			{Backend: drafter.BackendYoetz, CommandDisplay: "yoetz ask", ExitCode: 17, Failure: "missing credentials"},
			{Backend: drafter.BackendClaude, CommandDisplay: "claude -p", ExitCode: 0},
		}
		return drafter.Result{Text: validTeamRulesDraft("researcher"), Evidence: attempts[1], Attempts: attempts}, nil
	})
	stdout, stderr, err := captureOutput(t, func() error {
		return runTeamRules([]string{"init", "--project", project, "--profile", profile, "--template", "custom", "--force"})
	})
	if err != nil {
		t.Fatalf("team rules init: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
	}
	body, err := os.ReadFile(rules.Path(project))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"## Team Charter", "Owns evidence gathering and reports findings", workspaceSafetySection} {
		if !strings.Contains(string(body), want) {
			t.Fatalf("staged team rules missing %q:\n%s", want, body)
		}
	}
	for _, want := range []string{
		"Drafter config source: profile", "Drafter attempt (yoetz): yoetz ask", "Fall-through: missing credentials",
		"Drafter attempt (claude): claude -p", "Wrote " + rules.Path(project),
	} {
		if !strings.Contains(stderr, want) {
			t.Fatalf("team-rules evidence missing %q:\n%s", want, stderr)
		}
	}
}

func TestRunTeamRulesInitFallbackPrintsPromptAndWritesNothing(t *testing.T) {
	project, profile := setupTeamRulesDraftProfile(t, nil)
	stdout, stderr, err := captureOutput(t, func() error {
		return runTeamRules([]string{"init", "--project", project, "--profile", profile, "--template", "custom", "--force"})
	})
	if err != nil {
		t.Fatalf("manual team rules init: %v\nstderr:\n%s", err, stderr)
	}
	for _, want := range []string{"No team rules were written.", "Drafter config source: in_session", "Manual drafting prompt:", "## Team Charter", "`researcher`"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("manual team-rules output missing %q:\n%s", want, stdout)
		}
	}
	if _, err := os.Stat(rules.Path(project)); !os.IsNotExist(err) {
		t.Fatalf("manual team-rules fallback wrote a file: %v", err)
	}
}

func TestRunTeamRulesInitInvalidDraftPreservesExistingRules(t *testing.T) {
	project, profile := setupTeamRulesDraftProfile(t, &drafter.Config{Backend: drafter.BackendClaude})
	const original = "# existing rules\n"
	if err := os.MkdirAll(filepath.Dir(rules.Path(project)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(rules.Path(project), []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	installTeamRulesDraftRunner(t, func(context.Context, *drafter.Config, drafter.Request) (drafter.Result, error) {
		attempts := []drafter.Evidence{
			{Backend: drafter.BackendYoetz, CommandDisplay: "yoetz ask", ExitCode: 17, Failure: "missing credentials"},
			{Backend: drafter.BackendClaude, CommandDisplay: "claude -p", ExitCode: 0},
		}
		return drafter.Result{Text: "## Team Charter\nUnsafe incomplete prose.\n", Evidence: attempts[1], Attempts: attempts}, nil
	})
	_, _, err := captureOutput(t, func() error {
		return runTeamRules([]string{"init", "--project", project, "--profile", profile, "--template", "custom", "--force"})
	})
	if err == nil || !strings.Contains(err.Error(), "no team rules were written") {
		t.Fatalf("invalid team-rules draft error = %v", err)
	}
	for _, want := range []string{
		"drafter config source: profile",
		"attempt[1] backend=yoetz", `command="yoetz ask"`, `fall-through="missing credentials"`,
		"attempt[2] backend=claude", `command="claude -p"`,
	} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("invalid team-rules error missing %q: %v", want, err)
		}
	}
	got, readErr := os.ReadFile(rules.Path(project))
	if readErr != nil || string(got) != original {
		t.Fatalf("invalid draft changed rules: body=%q err=%v", got, readErr)
	}

	installTeamRulesDraftRunner(t, func(context.Context, *drafter.Config, drafter.Request) (drafter.Result, error) {
		attempts := []drafter.Evidence{
			{Backend: drafter.BackendYoetz, CommandDisplay: "yoetz ask", ExitCode: 17, Failure: "missing credentials"},
			{Backend: drafter.BackendClaude, CommandDisplay: "claude -p", ExitCode: 2, Failure: "provider unavailable"},
		}
		return drafter.Result{Evidence: attempts[1], Attempts: attempts}, errors.New("configured chain exhausted")
	})
	_, _, err = captureOutput(t, func() error {
		return runTeamRules([]string{"init", "--project", project, "--profile", profile, "--template", "custom", "--force"})
	})
	if err == nil || !strings.Contains(err.Error(), "configured chain exhausted") {
		t.Fatalf("fail-closed team-rules error = %v", err)
	}
	for _, want := range []string{
		"drafter config source: profile",
		"attempt[1] backend=yoetz", `fall-through="missing credentials"`,
		"attempt[2] backend=claude", `fall-through="provider unavailable"`,
	} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("fail-closed team-rules error missing %q: %v", want, err)
		}
	}
}

func setupTeamRulesDraftProfile(t *testing.T, cfg *drafter.Config) (string, string) {
	t.Helper()
	project := t.TempDir()
	const profile = "custom-team"
	if err := team.WriteProfile(project, profile, team.Team{
		Project: project, Workstream: "work", Drafter: cfg,
		Members: []team.Member{{Role: "researcher", Handle: "researcher", Binary: "codex", Session: "work"}},
	}); err != nil {
		t.Fatal(err)
	}
	return project, profile
}

func installTeamRulesDraftRunner(t *testing.T, runner cliDrafterRunner) {
	t.Helper()
	previous := runTeamRulesDrafter
	runTeamRulesDrafter = runner
	t.Cleanup(func() { runTeamRulesDrafter = previous })
}

func validTeamRulesDraft(role string) string {
	return "## Team Charter\n" +
		"Investigate ambiguous behavior and turn evidence into reviewable recommendations.\n\n" +
		"## Custom Role Scopes\n" +
		"- `" + role + "`: Owns evidence gathering and reports findings to the task sender.\n"
}
