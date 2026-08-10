package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/v2/internal/drafter"
	"github.com/omriariav/amq-squad/v2/internal/team"
)

func TestRunRoleDraftStagesValidatedDocumentWithoutLaunching(t *testing.T) {
	project, profile, session := setupRoleDraftTeam(t, &drafter.Config{
		Backend: drafter.BackendCustom,
		Command: []string{"fake-drafter"},
	})
	document := validRoleDraftDocument("researcher", "Research Engineer", "codex", []string{"lead", "qa"})
	var gotPrompt, gotWorkingDir string
	installRoleDraftRunner(t, func(_ context.Context, cfg *drafter.Config, request drafter.Request) (drafter.Result, error) {
		if cfg == nil || cfg.EffectiveBackend() != drafter.BackendCustom {
			t.Fatalf("drafter config = %+v", cfg)
		}
		gotPrompt, gotWorkingDir = request.Prompt, request.WorkingDirectory
		return drafter.Result{
			Text: document,
			Evidence: drafter.Evidence{
				Backend: drafter.BackendCustom, Command: []string{"fake-drafter"},
				CommandDisplay: "fake-drafter", ExitCode: 0,
			},
		}, nil
	})

	stdout, stderr, err := captureOutput(t, func() error {
		return runRoleDraft([]string{
			"researcher", "--binary", "codex", "--purpose", "Investigate ambiguous product behavior",
			"--label", "Research Engineer", "--peers", "lead,qa",
			"--project", project, "--profile", profile, "--session", session,
		})
	})
	if err != nil {
		t.Fatalf("role draft: %v\nstderr:\n%s", err, stderr)
	}
	path := team.CustomRolePath(project, "researcher")
	staged, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read staged role: %v", err)
	}
	if string(staged) != document {
		t.Fatalf("staged document differs:\n%s", staged)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("staged mode = %o, want 600", info.Mode().Perm())
	}
	if gotWorkingDir != project {
		t.Fatalf("drafter working dir = %q, want %q", gotWorkingDir, project)
	}
	for _, want := range []string{
		"id: researcher", "label: Research Engineer", "binary: codex", "peers: [lead, qa]",
		"Investigate ambiguous product behavior", "Live scope for this test comes from the dispatched task.",
		"under 45 lines", "session-neutral", "durable AMQ ACK/progress/blocker/DONE",
	} {
		if !strings.Contains(gotPrompt, want) {
			t.Fatalf("draft prompt missing %q:\n%s", want, gotPrompt)
		}
	}
	next := roleDraftNextCommand(project, profile, session, "researcher", "codex")
	if !strings.Contains(stdout, "Wrote "+path) || !strings.HasSuffix(strings.TrimSpace(stdout), next) {
		t.Fatalf("role draft output missing staged path or final next command:\n%s", stdout)
	}
	stored, err := team.ReadProfile(project, profile)
	if err != nil {
		t.Fatal(err)
	}
	if len(stored.Members) != 1 || stored.Members[0].Role != "lead" {
		t.Fatalf("role draft mutated or launched roster members: %+v", stored.Members)
	}
}

func TestRunRoleDraftWithoutConfiguredBackendPrintsManualPrompt(t *testing.T) {
	project, profile, session := setupRoleDraftTeam(t, nil)
	stdout, stderr, err := captureOutput(t, func() error {
		return runRoleDraft([]string{
			"researcher", "--binary", "codex", "--purpose", "Investigate ambiguous behavior",
			"--project", project, "--profile", profile, "--session", session,
		})
	})
	if err != nil {
		t.Fatalf("manual role draft: %v\nstderr:\n%s", err, stderr)
	}
	for _, want := range []string{"No role file was staged.", "the profile uses the in-session drafter", "Manual drafting prompt:", "id: researcher"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("manual output missing %q:\n%s", want, stdout)
		}
	}
	if _, err := os.Stat(team.CustomRolePath(project, "researcher")); !os.IsNotExist(err) {
		t.Fatalf("unset drafter staged a role: %v", err)
	}
}

func TestRunRoleDraftBackendFallbackReportsEvidenceWithoutStaging(t *testing.T) {
	project, profile, session := setupRoleDraftTeam(t, &drafter.Config{
		Backend: drafter.BackendYoetz,
		Model:   "fast-model",
	})
	installRoleDraftRunner(t, func(context.Context, *drafter.Config, drafter.Request) (drafter.Result, error) {
		return drafter.Result{
			UseInSession: true,
			Fallback:     true,
			Reason:       "provider API key is missing",
			Remedy:       "configure credentials or complete the prompt manually",
			Evidence: drafter.Evidence{
				Backend: drafter.BackendYoetz, Command: []string{"yoetz", "ask", "--model", "fast-model"},
				CommandDisplay: "yoetz ask --model fast-model", ExitCode: 1, Stderr: "provider API key is missing",
			},
		}, nil
	})

	stdout, stderr, err := captureOutput(t, func() error {
		return runRoleDraft([]string{
			"researcher", "--binary", "codex", "--purpose", "Investigate ambiguous behavior",
			"--project", project, "--profile", profile, "--session", session,
		})
	})
	if err != nil {
		t.Fatalf("fallback role draft: %v\nstderr:\n%s", err, stderr)
	}
	for _, want := range []string{"No role file was staged.", "provider API key is missing", "Drafter command: yoetz ask --model fast-model", "Manual drafting prompt:"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("fallback output missing %q:\n%s", want, stdout)
		}
	}
	if _, err := os.Stat(team.CustomRolePath(project, "researcher")); !os.IsNotExist(err) {
		t.Fatalf("fallback staged a role: %v", err)
	}
}

func TestRunRoleDraftJSONIncludesStructuredEvidence(t *testing.T) {
	project, profile, session := setupRoleDraftTeam(t, &drafter.Config{
		Backend: drafter.BackendCustom,
		Command: []string{"fake-drafter"},
	})
	installRoleDraftRunner(t, func(context.Context, *drafter.Config, drafter.Request) (drafter.Result, error) {
		return drafter.Result{
			Text: validRoleDraftDocument("researcher", "researcher", "codex", nil),
			Evidence: drafter.Evidence{
				Backend: drafter.BackendCustom, Command: []string{"fake-drafter"},
				CommandDisplay: "fake-drafter", TimeoutSeconds: 30, ExitCode: 0,
			},
		}, nil
	})

	stdout, stderr, err := captureOutput(t, func() error {
		return runRoleDraft([]string{
			"researcher", "--binary", "codex", "--purpose", "Investigate ambiguous behavior",
			"--project", project, "--profile", profile, "--session", session, "--json",
		})
	})
	if err != nil {
		t.Fatalf("JSON role draft: %v\nstderr:\n%s", err, stderr)
	}
	var envelope struct {
		SchemaVersion int                   `json:"schema_version"`
		Kind          string                `json:"kind"`
		Data          roleDraftEnvelopeData `json:"data"`
	}
	if err := json.Unmarshal([]byte(stdout), &envelope); err != nil {
		t.Fatalf("decode JSON role draft: %v\n%s", err, stdout)
	}
	if envelope.SchemaVersion != JSONSchemaVersion || envelope.Kind != "role_draft" {
		t.Fatalf("envelope = %+v", envelope)
	}
	if !envelope.Data.Staged || envelope.Data.Manual || envelope.Data.ID != "researcher" {
		t.Fatalf("role draft data = %+v", envelope.Data)
	}
	if envelope.Data.Evidence.CommandDisplay != "fake-drafter" || envelope.Data.Evidence.TimeoutSeconds != 30 {
		t.Fatalf("role draft evidence = %+v", envelope.Data.Evidence)
	}
	if envelope.Data.NextCommand == "" || envelope.Data.Path != team.CustomRolePath(project, "researcher") {
		t.Fatalf("role draft path/next = %q / %q", envelope.Data.Path, envelope.Data.NextCommand)
	}
}

func TestRunRoleDraftRejectsSessionBoundOutputWithoutStaging(t *testing.T) {
	project, profile, session := setupRoleDraftTeam(t, &drafter.Config{
		Backend: drafter.BackendCustom,
		Command: []string{"fake-drafter"},
	})
	document := validRoleDraftDocument("researcher", "researcher", "codex", nil)
	document = strings.Replace(document, "Take live scope only", "For issue-665, take live scope only", 1)
	installRoleDraftRunner(t, func(context.Context, *drafter.Config, drafter.Request) (drafter.Result, error) {
		return drafter.Result{
			Text:     document,
			Evidence: drafter.Evidence{Backend: drafter.BackendCustom, CommandDisplay: "fake-drafter", ExitCode: 0},
		}, nil
	})

	_, _, err := captureOutput(t, func() error {
		return runRoleDraft([]string{
			"researcher", "--binary", "codex", "--purpose", "Investigate ambiguous behavior",
			"--project", project, "--profile", profile, "--session", session,
		})
	})
	if err == nil || !strings.Contains(err.Error(), `names active session "issue-665"`) || !strings.Contains(err.Error(), "no file was staged") {
		t.Fatalf("session-bound draft error = %v", err)
	}
	if _, err := os.Stat(team.CustomRolePath(project, "researcher")); !os.IsNotExist(err) {
		t.Fatalf("invalid draft was staged: %v", err)
	}
}

func TestRunRoleDraftRefusesExistingPathBeforeInvokingDrafter(t *testing.T) {
	project, profile, session := setupRoleDraftTeam(t, &drafter.Config{
		Backend: drafter.BackendCustom,
		Command: []string{"fake-drafter"},
	})
	path := team.CustomRolePath(project, "researcher")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	const original = "existing role\n"
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	called := false
	installRoleDraftRunner(t, func(context.Context, *drafter.Config, drafter.Request) (drafter.Result, error) {
		called = true
		return drafter.Result{}, nil
	})

	_, _, err := captureOutput(t, func() error {
		return runRoleDraft([]string{
			"researcher", "--binary", "codex", "--purpose", "Investigate ambiguous behavior",
			"--project", project, "--profile", profile, "--session", session,
		})
	})
	if err == nil || !strings.Contains(err.Error(), "refuses to overwrite") {
		t.Fatalf("existing path error = %v", err)
	}
	if called {
		t.Fatal("drafter ran before the existing-path refusal")
	}
	got, readErr := os.ReadFile(path)
	if readErr != nil || string(got) != original {
		t.Fatalf("existing role changed: body=%q err=%v", got, readErr)
	}
}

func TestValidateRoleDraftDocumentDeterministicShapeAndNeutrality(t *testing.T) {
	valid := validRoleDraftDocument("researcher", "Researcher", "codex", []string{"lead"})
	lines := strings.Split(strings.TrimSuffix(valid, "\n"), "\n")
	for len(lines) < roleDraftLineLimit {
		lines = append(lines, "Additional reusable guidance.")
	}
	tests := []struct {
		name     string
		document string
		session  string
		branch   string
		want     string
	}{
		{name: "line limit", document: strings.Join(lines, "\n") + "\n", want: "must be under 45"},
		{name: "task id", document: strings.Replace(valid, "reusable discipline", "reusable discipline for t42", 1), want: "contains a task id"},
		{name: "version", document: strings.Replace(valid, "reusable discipline", "reusable discipline in v2.29.3", 1), want: "contains a version"},
		{name: "session", document: strings.Replace(valid, "reusable discipline", "reusable discipline in current-run", 1), session: "current-run", want: "names active session"},
		{name: "branch", document: strings.Replace(valid, "reusable discipline", "reusable discipline on feature/roles", 1), branch: "feature/roles", want: "names active branch"},
		{name: "first heading", document: strings.Replace(valid, "# Role: Researcher", "# Notes\n\n# Role: Researcher", 1), want: "first role heading"},
		{name: "extra heading", document: strings.Replace(valid, "## Protocol", "## Operations\nAvoid side effects.\n\n## Protocol", 1), want: "unexpected role heading"},
		{name: "duplicate section", document: strings.Replace(valid, "## Protocol", "## Boundaries\nRemain generic.\n\n## Protocol", 1), want: `heading "## Boundaries" must appear exactly once`},
		{name: "extra frontmatter", document: strings.Replace(valid, "peers: [lead]", "peers: [lead]\nskills: [release]", 1), want: "frontmatter may contain only"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := validateRoleDraftDocument(tt.document, "researcher.md", "researcher", "Researcher", "codex", []string{"lead"}, tt.session, tt.branch)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("validation error = %v, want substring %q", err, tt.want)
			}
		})
	}
	if got, err := validateRoleDraftDocument(valid, "researcher.md", "researcher", "Researcher", "codex", []string{"lead"}, "current-run", "feature/roles"); err != nil || got != valid {
		t.Fatalf("valid neutral document = %q, %v", got, err)
	}
}

func TestRoleCommandIsPublicAndCompletable(t *testing.T) {
	if _, ok := lookupCommand("role", "v-test"); !ok {
		t.Fatal("command registry missing role")
	}
	if !containsString(completionTopCommands, "role") {
		t.Fatal("completion missing role command")
	}
	stdout, _, err := captureOutput(t, func() error { return Run([]string{"--help"}, "v-test") })
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout, "role") || !strings.Contains(stdout, "Draft and validate reusable custom role personas") {
		t.Fatalf("top-level help missing role draft command:\n%s", stdout)
	}
	for _, tt := range []struct {
		shell string
		want  string
	}{
		{shell: "bash", want: `local role_subcommands="draft"`},
		{shell: "zsh", want: `role_subcommands=('draft')`},
		{shell: "fish", want: `__fish_seen_subcommand_from role" -a 'draft'`},
	} {
		output, _, err := captureOutput(t, func() error { return runCompletion([]string{tt.shell}) })
		if err != nil {
			t.Fatalf("%s completion: %v", tt.shell, err)
		}
		if !strings.Contains(output, tt.want) {
			t.Fatalf("%s completion missing role draft contract %q:\n%s", tt.shell, tt.want, output)
		}
	}
}

func setupRoleDraftTeam(t *testing.T, cfg *drafter.Config) (string, string, string) {
	t.Helper()
	project := t.TempDir()
	const profile = "review"
	const session = "issue-665"
	if err := team.WriteProfile(project, profile, team.Team{
		Project: project, Workstream: session, Orchestrated: true, Lead: "lead", Drafter: cfg,
		Members: []team.Member{{Role: "lead", Handle: "lead", Binary: "claude", Session: session, CWD: project}},
	}); err != nil {
		t.Fatalf("write role draft profile: %v", err)
	}
	briefPath := briefPathForProfile(project, profile, session)
	if err := os.MkdirAll(filepath.Dir(briefPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(briefPath, []byte("# Test brief\n\nLive scope for this test comes from the dispatched task.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return project, profile, session
}

func installRoleDraftRunner(t *testing.T, runner func(context.Context, *drafter.Config, drafter.Request) (drafter.Result, error)) {
	t.Helper()
	old := runRoleDrafter
	runRoleDrafter = runner
	t.Cleanup(func() { runRoleDrafter = old })
}

func validRoleDraftDocument(id, label, binary string, peers []string) string {
	return "---\n" +
		"id: " + id + "\n" +
		"label: " + label + "\n" +
		"binary: " + binary + "\n" +
		"peers: [" + strings.Join(peers, ", ") + "]\n" +
		"---\n" +
		"# Role: " + label + "\n\n" +
		"## Mission\n" +
		"Investigate ambiguous product behavior within a reusable discipline.\n\n" +
		"## Boundaries\n" +
		"Take live scope only from the active brief and dispatched task.\n" +
		"Do not merge, release, send externally, delete files, grant approvals, or delegate work.\n\n" +
		"## Protocol\n" +
		"Use durable AMQ to ACK the sender, report progress and blockers, and send DONE with evidence.\n" +
		"Follow the current team routing block.\n"
}
