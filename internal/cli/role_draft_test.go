package cli

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/v3/internal/drafter"
	"github.com/omriariav/amq-squad/v3/internal/team"
)

// setupRoleDraftProject returns a repository root whose owner drafter config
// (the global config; squad.yaml has no drafter) is cfg, or none.
func setupRoleDraftProject(t *testing.T, cfg *drafter.Config) string {
	t.Helper()
	project := t.TempDir()
	path := filepath.Join(project, "missing-user-config.json")
	if cfg != nil {
		raw, err := json.Marshal(map[string]any{"drafter": cfg})
		if err != nil {
			t.Fatal(err)
		}
		path = filepath.Join(project, "user-config.json")
		if err := os.WriteFile(path, raw, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("AMQ_SQUAD_CONFIG", path)
	return project
}

func installRoleDraftRunner(t *testing.T, runner cliDrafterRunner) {
	t.Helper()
	old := runDraft
	runDraft = runner
	t.Cleanup(func() { runDraft = old })
}

func validRoleDraftDocument(id string) string {
	return "# Role: " + id + "\n\n## Mission\nInvestigate ambiguous behavior and report evidence.\n\n" +
		"## Boundaries\n- Take live scope only from the active brief and the dispatched task.\n- Grant no merge or release authority.\n\n" +
		"## Protocol\n- Report ACK, progress, blockers and DONE to the task sender over AMQ.\n"
}

func roleDraft(t *testing.T, project string, extra ...string) (string, error) {
	t.Helper()
	stdout, _, err := captureOutput(t, func() error {
		return runRoleDraft(append([]string{"researcher", "--purpose", "Investigate ambiguous behavior", "--project", project}, extra...))
	})
	return stdout, err
}

func assertNotStaged(t *testing.T, project string) {
	t.Helper()
	if _, err := os.Stat(team.CustomRolePath(project, "researcher")); !os.IsNotExist(err) {
		t.Fatalf("a role was staged: %v", err)
	}
}

func TestRunRoleDraftWithoutConfiguredBackendPrintsManualPrompt(t *testing.T) {
	project := setupRoleDraftProject(t, nil)
	stdout, err := roleDraft(t, project)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"No role file was staged.", "Manual drafting prompt:", "# Role: researcher", "## Mission, ## Boundaries, ## Protocol"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("manual output missing %q:\n%s", want, stdout)
		}
	}
	assertNotStaged(t, project)
}

func TestRunRoleDraftChainFallbackReportsEveryAttempt(t *testing.T) {
	project := setupRoleDraftProject(t, &drafter.Config{Chain: []string{drafter.BackendYoetz, drafter.BackendClaude}, Model: "fast-model"})
	installRoleDraftRunner(t, func(context.Context, *drafter.Config, drafter.Request) (drafter.Result, error) {
		attempts := []drafter.Evidence{
			{Backend: drafter.BackendYoetz, CommandDisplay: "yoetz ask", ExitCode: 17, Failure: "missing credentials"},
			{Backend: drafter.BackendClaude, CommandDisplay: "claude -p", ExitCode: 2, Failure: "provider unavailable"},
		}
		return drafter.Result{UseInSession: true, Fallback: true, Reason: "configured chain exhausted", Remedy: "complete the prompt manually", Evidence: attempts[1], Attempts: attempts}, nil
	})
	stdout, err := roleDraft(t, project)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"No role file was staged.", "configured chain exhausted", "Drafter attempt (yoetz): yoetz ask", "Fall-through: missing credentials", "Drafter attempt (claude): claude -p"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("fallback output missing %q:\n%s", want, stdout)
		}
	}
	assertNotStaged(t, project)
}

func TestRunRoleDraftFailureModeErrorReportsEveryAttempt(t *testing.T) {
	project := setupRoleDraftProject(t, &drafter.Config{Chain: []string{drafter.BackendYoetz, drafter.BackendClaude}, Model: "fast", OnFailure: drafter.FailureError})
	installRoleDraftRunner(t, func(context.Context, *drafter.Config, drafter.Request) (drafter.Result, error) {
		attempts := []drafter.Evidence{
			{Backend: drafter.BackendYoetz, CommandDisplay: "yoetz ask --model fast", ExitCode: 17, Failure: "missing credentials"},
			{Backend: drafter.BackendClaude, CommandDisplay: "claude -p --model fable", ExitCode: 2, Failure: "provider unavailable"},
		}
		return drafter.Result{Evidence: attempts[1], Attempts: attempts}, &drafter.RunError{Cause: errors.New("configured chain exhausted"), Evidence: attempts[1], Attempts: attempts}
	})
	_, err := roleDraft(t, project)
	for _, want := range []string{"drafter config source: global", "attempt[1] backend=yoetz", `fall-through="missing credentials"`, "attempt[2] backend=claude"} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("fail-closed error missing %q: %v", want, err)
		}
	}
	assertNotStaged(t, project)
}

func TestRunRoleDraftStagesValidatedDocumentAndJSONEvidence(t *testing.T) {
	project := setupRoleDraftProject(t, &drafter.Config{Backend: drafter.BackendCustom, Command: []string{"trusted-drafter"}})
	var gotPrompt string
	installRoleDraftRunner(t, func(_ context.Context, cfg *drafter.Config, req drafter.Request) (drafter.Result, error) {
		if cfg == nil || strings.Join(cfg.Command, " ") != "trusted-drafter" || req.WorkingDirectory != project {
			t.Fatalf("drafter config %+v, request %+v", cfg, req)
		}
		gotPrompt = req.Prompt
		return drafter.Result{Text: validRoleDraftDocument("researcher"), Evidence: drafter.Evidence{Backend: drafter.BackendCustom, CommandDisplay: "trusted-drafter"}}, nil
	})
	stdout, err := roleDraft(t, project, "--json")
	if err != nil {
		t.Fatal(err)
	}
	env := decodeJSONEnvelope[roleDraftEnvelopeData](t, stdout)
	if !env.Data.Staged || env.Data.Manual || env.Data.ConfigSource != drafter.SourceGlobal || env.Data.Evidence.CommandDisplay != "trusted-drafter" {
		t.Fatalf("envelope = %+v", env.Data)
	}
	if !strings.Contains(gotPrompt, "Durable purpose: Investigate ambiguous behavior") || !strings.Contains(gotPrompt, "declared files") {
		t.Fatalf("prompt = %s", gotPrompt)
	}
	path := team.CustomRolePath(project, "researcher")
	body, err := os.ReadFile(path)
	if info, _ := os.Stat(path); err != nil || string(body) != validRoleDraftDocument("researcher") || info.Mode().Perm() != 0o600 {
		t.Fatalf("staged %q (%v)", body, err)
	}
	// a second draft refuses before the drafter runs
	installRoleDraftRunner(t, func(context.Context, *drafter.Config, drafter.Request) (drafter.Result, error) {
		t.Fatal("drafter invoked over an existing role")
		return drafter.Result{}, nil
	})
	if _, err := roleDraft(t, project); err == nil || !strings.Contains(err.Error(), "refuses to overwrite") {
		t.Fatalf("existing role = %v", err)
	}
}

func TestRunRoleDraftRejectsInvalidOutputWithoutStaging(t *testing.T) {
	project := setupRoleDraftProject(t, &drafter.Config{Backend: drafter.BackendClaude})
	document := strings.Replace(validRoleDraftDocument("researcher"), "Investigate", "For t42, investigate", 1)
	installRoleDraftRunner(t, func(context.Context, *drafter.Config, drafter.Request) (drafter.Result, error) {
		return drafter.Result{Text: document}, nil
	})
	if _, err := roleDraft(t, project); err == nil || !strings.Contains(err.Error(), "task id") || !strings.Contains(err.Error(), "no file was staged") {
		t.Fatalf("invalid output = %v", err)
	}
	assertNotStaged(t, project)
}

func TestValidateRoleDraftDocument(t *testing.T) {
	valid := validRoleDraftDocument("researcher")
	if got, err := validateRoleDraftDocument(valid, "researcher"); err != nil || got != valid {
		t.Fatalf("valid = %q, %v", got, err)
	}
	cases := map[string]string{
		"wrong heading":    strings.Replace(valid, "# Role: researcher", "# Role: other", 1),
		"missing section":  strings.Replace(valid, "## Protocol", "Protocol", 1),
		"extra section":    valid + "\n## Authority\nAll.\n",
		"out of order":     strings.Replace(strings.Replace(valid, "## Mission", "## TMP", 1), "## Boundaries", "## Mission", 1),
		"version":          strings.Replace(valid, "evidence.", "evidence for v2.31.0.", 1),
		"no brief or task": strings.ReplaceAll(strings.ReplaceAll(valid, "brief", "plan"), "task", "job"),
		"code fence":       valid + "```\n",
		"too long":         valid + strings.Repeat("- line\n", 45),
	}
	for name, doc := range cases {
		if _, err := validateRoleDraftDocument(doc, "researcher"); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestRunRoleDraftRejectsBuiltInAndBadIDs(t *testing.T) {
	for _, args := range [][]string{{"cto", "--purpose", "x"}, {"Bad_ID!", "--purpose", "x"}, {"researcher"}, {"researcher", "--purpose", "a\nb"}} {
		if _, _, err := captureOutput(t, func() error { return runRoleDraft(args) }); err == nil {
			t.Errorf("%v accepted", args)
		}
	}
}
