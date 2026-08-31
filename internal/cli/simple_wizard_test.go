package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/drafter"
	"github.com/omriariav/amq-squad/v2/internal/rules"
	"github.com/omriariav/amq-squad/v2/internal/team"
	"github.com/omriariav/amq-squad/v2/internal/userconfig"
)

func TestWizardCommandIsDiscoverable(t *testing.T) {
	if _, ok := lookupCommand("wizard", "test"); !ok {
		t.Fatal("wizard is not registered")
	}
	if !containsString(completionTopCommands, "wizard") {
		t.Fatal("wizard is not included in completion")
	}
	var out, errOut bytes.Buffer
	if err := runWizardWithDependencies([]string{"--help"}, "test", simpleWizardTestDependencies(t, t.TempDir()), strings.NewReader(""), &out, &errOut); err != nil && !errors.Is(err, flag.ErrHelp) {
		t.Fatalf("wizard --help: %v", err)
	}
	if !strings.Contains(errOut.String(), "deterministic goal-to-squad setup") || !strings.Contains(errOut.String(), "final confirmation defaults") {
		t.Fatalf("wizard help missing state-machine/default-No contract:\n%s", errOut.String())
	}
}

func TestWizardDefaultNoLeavesEveryArtifactAbsent(t *testing.T) {
	project := t.TempDir()
	deps := simpleWizardTestDependencies(t, project)
	var out, errOut bytes.Buffer
	err := runWizardWithDependencies([]string{"Ship the reviewed change", "--project", project, "--profile", "review", "--session", "issue-709"}, "test", deps, strings.NewReader("\n"), &out, &errOut)
	if err != nil {
		t.Fatalf("wizard preview: %v\nstderr:\n%s", err, errOut.String())
	}
	for _, path := range []string{
		team.ProfilePath(project, "review"),
		rules.Path(project),
		briefPathForProfile(project, "review", "issue-709"),
		filepath.Join(project, rules.ClaudeFile),
		filepath.Join(project, rules.AgentsFile),
	} {
		if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
			t.Fatalf("default-No wizard mutated %s (stat err %v)", path, statErr)
		}
	}
	text := out.String()
	for _, want := range []string{"Stage 1/5 readiness", "Stage 2/5 profile", "Stage 3/5 optional custom seats", "Stage 4/5 rules", "Stage 5/5 brief and start review", "Apply these exact artifacts and start the squad? [y/N]", "wizard cancelled; nothing changed"} {
		if !strings.Contains(text, want) {
			t.Errorf("wizard output missing %q:\n%s", want, text)
		}
	}
}

func TestWizardYesWritesReviewedArtifactsThenDelegatesToStart(t *testing.T) {
	project := t.TempDir()
	deps := simpleWizardTestDependencies(t, project)
	startCalled := false
	deps.Start = func(args []string, _ simpleStartDependencies, _ io.Reader, _ io.Writer) error {
		startCalled = true
		if got := strings.Join(args, " "); !strings.Contains(got, "--profile review") || !strings.Contains(got, "--session issue-709") || !strings.Contains(got, "--yes") {
			t.Fatalf("wizard start args = %q", got)
		}
		if _, err := team.ReadProfile(project, "review"); err != nil {
			t.Fatalf("start called before profile was readable: %v", err)
		}
		for _, path := range []string{rules.Path(project), briefPathForProfile(project, "review", "issue-709"), filepath.Join(project, rules.ClaudeFile), filepath.Join(project, rules.AgentsFile)} {
			if _, err := os.Stat(path); err != nil {
				t.Fatalf("start called before reviewed artifact %s existed: %v", path, err)
			}
		}
		return nil
	}
	var out, errOut bytes.Buffer
	err := runWizardWithDependencies([]string{"Ship the reviewed change", "--project", project, "--profile", "review", "--session", "issue-709", "--yes"}, "test", deps, strings.NewReader(""), &out, &errOut)
	if err != nil {
		t.Fatalf("wizard approved run: %v\nstderr:\n%s", err, errOut.String())
	}
	if !startCalled {
		t.Fatal("wizard did not delegate the accepted plan to start")
	}
	stored, err := team.ReadProfile(project, "review")
	if err != nil {
		t.Fatal(err)
	}
	if stored.Lead != "cto" || team.EffectiveLeadMode(stored) != team.LeadModePlanner {
		t.Fatalf("stored lead contract = lead %q mode %q", stored.Lead, team.EffectiveLeadMode(stored))
	}
	implementation := 0
	for _, member := range stored.Members {
		if team.EffectiveActorMode(stored, member) == team.ActorModeImplementation {
			implementation++
		}
	}
	if implementation != 1 {
		t.Fatalf("wizard default roster has %d implementation actors, want one for launch-safe shared cwd", implementation)
	}
}

func TestWizardRefusesPlanChangedAtConfirmation(t *testing.T) {
	project := t.TempDir()
	deps := simpleWizardTestDependencies(t, project)
	startCalled := false
	deps.Start = func([]string, simpleStartDependencies, io.Reader, io.Writer) error { startCalled = true; return nil }
	reader := &wizardMutationReader{mutate: func() {
		if err := os.MkdirAll(filepath.Dir(rules.Path(project)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(rules.Path(project), []byte("changed during review\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}}
	var out, errOut bytes.Buffer
	err := runWizardWithDependencies([]string{"Ship the reviewed change", "--project", project, "--profile", "review", "--session", "issue-709"}, "test", deps, reader, &out, &errOut)
	if err == nil || !strings.Contains(err.Error(), "plan changed while awaiting approval") {
		t.Fatalf("wizard changed-plan error = %v", err)
	}
	if startCalled {
		t.Fatal("wizard called start after accepted inputs changed")
	}
	if team.ExistsProfile(project, "review") {
		t.Fatal("wizard wrote the profile after snapshot rejection")
	}
}

func TestWizardReadinessFailsBeforeDrafterRuns(t *testing.T) {
	project := t.TempDir()
	deps := simpleWizardTestDependencies(t, project)
	drafterCalled := false
	deps.RunGoalDraft = func(context.Context, *drafter.Config, drafter.Request) (drafter.Result, error) {
		drafterCalled = true
		return drafter.Result{}, nil
	}
	deps.LookPath = func(name string) (string, error) {
		if name == "amq" {
			return "", errors.New("not found")
		}
		return "/test/bin/" + name, nil
	}
	err := runWizardWithDependencies([]string{"Ship it", "--project", project}, "test", deps, strings.NewReader(""), io.Discard, io.Discard)
	if err == nil || !strings.Contains(err.Error(), `required executable "amq"`) {
		t.Fatalf("wizard readiness error = %v", err)
	}
	if drafterCalled {
		t.Fatal("wizard invoked the drafter before core readiness passed")
	}
}

// TestWizardReadinessFailsOnYoetzWithoutModel proves wizard readiness
// refuses a global yoetz-preset drafter config with no model up front
// (gh#760), before the drafter ever runs, instead of only surfacing yoetz's
// own opaque "provider is required" failure at invocation time.
func TestWizardReadinessFailsOnYoetzWithoutModel(t *testing.T) {
	project := t.TempDir()
	deps := simpleWizardTestDependencies(t, project)
	deps.ReadConfig = func() (userconfig.Config, error) {
		return userconfig.Config{Drafter: &drafter.Config{Backend: drafter.BackendYoetz}}, nil
	}
	drafterCalled := false
	deps.RunGoalDraft = func(context.Context, *drafter.Config, drafter.Request) (drafter.Result, error) {
		drafterCalled = true
		return drafter.Result{}, nil
	}
	err := runWizardWithDependencies([]string{"Ship it", "--project", project}, "test", deps, strings.NewReader(""), io.Discard, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "model: required for the yoetz preset backend") {
		t.Fatalf("wizard readiness error = %v, want yoetz-without-model refusal", err)
	}
	if drafterCalled {
		t.Fatal("wizard invoked the drafter before core readiness passed")
	}
}

func TestWizardImplicitProfileCollisionUsesExistingFlow(t *testing.T) {
	project := t.TempDir()
	op := team.DefaultOperator()
	for _, profile := range []string{"ship-it", "other"} {
		tm := team.Team{Operator: &op, Orchestrated: true, Lead: "cto", LeadMode: team.LeadModePlanner, ExecutionMode: executionModeProjectLead, Members: []team.Member{{Role: "cto", Handle: "cto", Binary: "codex", ActorMode: team.ActorModeReview}}}
		if err := team.WriteProfile(project, profile, tm); err != nil {
			t.Fatal(err)
		}
	}
	if err := rules.Write(project, "# Existing reviewed rules\n"); err != nil {
		t.Fatal(err)
	}
	deps := simpleWizardTestDependenciesForMembers(t, project, []team.Member{{Role: "cto", Handle: "cto", Binary: "codex", ActorMode: team.ActorModeReview}})
	deps.RunGoalDraft = func(context.Context, *drafter.Config, drafter.Request) (drafter.Result, error) {
		return drafter.Result{Text: validSimpleStartBriefDraft("fresh", "Ship it", team.Member{Role: "cto", Handle: "cto", Binary: "codex", ActorMode: team.ActorModeReview}), Evidence: drafter.Evidence{Backend: drafter.BackendCodex}}, nil
	}
	var out bytes.Buffer
	if err := runWizardWithDependencies([]string{"Ship it", "--project", project, "--session", "fresh", "--json"}, "test", deps, strings.NewReader(""), &out, io.Discard); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"flow": "existing_profile_session"`) || !strings.Contains(out.String(), `"profile": "ship-it"`) {
		t.Fatalf("implicit colliding profile did not use existing flow:\n%s", out.String())
	}
}

func TestWizardExistingProfileRejectsExplicitDefaultValuedNewProfileFlag(t *testing.T) {
	project := t.TempDir()
	op := team.DefaultOperator()
	tm := team.Team{Operator: &op, Orchestrated: true, Lead: "cto", LeadMode: team.LeadModePlanner, ExecutionMode: executionModeProjectLead, Members: []team.Member{{Role: "cto", Handle: "cto", Binary: "codex", ActorMode: team.ActorModeReview}}}
	if err := team.WriteProfile(project, "reusable", tm); err != nil {
		t.Fatal(err)
	}
	deps := simpleWizardTestDependenciesForMembers(t, project, tm.Members)
	err := runWizardWithDependencies([]string{"Fresh", "--project", project, "--profile", "reusable", "--session", "fresh", "--lead", "cto", "--json"}, "test", deps, strings.NewReader(""), io.Discard, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "new-profile options") {
		t.Fatalf("explicit --lead cto on existing profile error = %v", err)
	}
}

func TestWizardExistingReusableProfileUsesFlowBWithoutRewritingProfileOrRules(t *testing.T) {
	project := t.TempDir()
	op := team.DefaultOperator()
	tm := team.Team{Operator: &op, Orchestrated: true, Lead: "cto", LeadMode: team.LeadModePlanner, ExecutionMode: executionModeProjectLead, Members: []team.Member{{Role: "cto", Handle: "cto", Binary: "codex", ActorMode: team.ActorModeReview}}}
	if err := team.WriteProfile(project, "reusable", tm); err != nil {
		t.Fatal(err)
	}
	if err := rules.Write(project, "# Existing reviewed rules\n"); err != nil {
		t.Fatal(err)
	}
	profileBefore, _ := os.ReadFile(team.ProfilePath(project, "reusable"))
	rulesBefore, _ := os.ReadFile(rules.Path(project))
	deps := simpleWizardTestDependenciesForMembers(t, project, tm.Members)
	var out, errOut bytes.Buffer
	err := runWizardWithDependencies([]string{"A fresh workstream", "--project", project, "--profile", "reusable", "--session", "fresh", "--json"}, "test", deps, strings.NewReader(""), &out, &errOut)
	if err != nil {
		t.Fatalf("existing-profile wizard: %v", err)
	}
	var envelope struct {
		Kind string `json:"kind"`
		Data struct {
			Flow            string               `json:"flow"`
			ProfileArtifact simpleWizardArtifact `json:"profile_artifact"`
			Rules           simpleWizardArtifact `json:"rules"`
		} `json:"data"`
	}
	if err := json.Unmarshal(out.Bytes(), &envelope); err != nil {
		t.Fatalf("decode wizard JSON: %v\n%s", err, out.String())
	}
	if envelope.Kind != "wizard_plan" || envelope.Data.Flow != "existing_profile_session" || envelope.Data.ProfileArtifact.Action != "reuse" || envelope.Data.Rules.Action != "reuse" {
		t.Fatalf("existing wizard plan = %+v", envelope)
	}
	profileAfter, _ := os.ReadFile(team.ProfilePath(project, "reusable"))
	rulesAfter, _ := os.ReadFile(rules.Path(project))
	if !bytes.Equal(profileBefore, profileAfter) || !bytes.Equal(rulesBefore, rulesAfter) {
		t.Fatal("preview-only existing-profile wizard rewrote profile or rules")
	}
}

func TestWizardCustomSeatUsesDrafterInsideBinaryPlan(t *testing.T) {
	project := t.TempDir()
	deps := simpleWizardTestDependencies(t, project)
	members := []team.Member{{Role: "cto", Handle: "cto", Binary: "codex"}, {Role: "researcher", Handle: "researcher", Binary: "codex"}}
	deps.RunGoalDraft = func(_ context.Context, _ *drafter.Config, _ drafter.Request) (drafter.Result, error) {
		return drafter.Result{Text: validSimpleStartBriefDraft("research", "Investigate the behavior", members...), Evidence: drafter.Evidence{Backend: drafter.BackendCodex}}, nil
	}
	roleCalled := false
	deps.RunRole = func(_ context.Context, _ *drafter.Config, _ drafter.Request) (drafter.Result, error) {
		roleCalled = true
		return drafter.Result{Text: validRoleDraftDocument("researcher", "researcher", "codex", []string{"cto"}), Evidence: drafter.Evidence{Backend: drafter.BackendCodex}}, nil
	}
	rulesCalled := false
	deps.RunRules = func(_ context.Context, _ *drafter.Config, _ drafter.Request) (drafter.Result, error) {
		rulesCalled = true
		return drafter.Result{Text: validTeamRulesDraft("researcher"), Evidence: drafter.Evidence{Backend: drafter.BackendCodex}}, nil
	}
	var out, errOut bytes.Buffer
	err := runWizardWithDependencies([]string{"Investigate the behavior", "--project", project, "--profile", "research-team", "--session", "research", "--roles", "cto,researcher", "--binary", "researcher=codex", "--json"}, "test", deps, strings.NewReader(""), &out, &errOut)
	if err != nil {
		t.Fatalf("custom-seat wizard: %v\n%s", err, errOut.String())
	}
	if !roleCalled || !rulesCalled {
		t.Fatalf("binary drafter stages called: role=%t rules=%t", roleCalled, rulesCalled)
	}
	if !strings.Contains(out.String(), `"custom_roles"`) || !strings.Contains(out.String(), `"researcher"`) || !strings.Contains(out.String(), `"drafter"`) {
		t.Fatalf("custom-seat plan omitted exact role/evidence:\n%s", out.String())
	}
	if team.ExistsProfile(project, "research-team") {
		t.Fatal("custom-seat JSON preview mutated the profile")
	}
}

// TestInSessionFallthroughIncludesAttemptEvidence proves the wizard's
// stopped-before-mutation error (gh#760) surfaces the per-attempt drafter
// evidence -- backend, exact command, and fall-through reason -- for both
// the new-profile brief draft path (simple_wizard.go's
// buildNewSimpleWizardPlan) and the existing-profile brief draft path
// (buildExistingSimpleWizardPlan), instead of silently dropping it like the
// validation-error path never did.
func TestInSessionFallthroughIncludesAttemptEvidence(t *testing.T) {
	fallThroughDraft := func(context.Context, *drafter.Config, drafter.Request) (drafter.Result, error) {
		return drafter.Result{
			UseInSession: true,
			Attempts: []drafter.Evidence{
				{Backend: drafter.BackendYoetz, CommandDisplay: "yoetz ask --prompt-file *** --format text", Failure: "exit 17: missing provider API key"},
			},
		}, nil
	}

	t.Run("new profile", func(t *testing.T) {
		project := t.TempDir()
		deps := simpleWizardTestDependencies(t, project)
		deps.RunGoalDraft = fallThroughDraft
		err := runWizardWithDependencies([]string{"Ship the reviewed change", "--project", project, "--profile", "review", "--session", "issue-709", "--yes"}, "test", deps, strings.NewReader(""), io.Discard, io.Discard)
		if err == nil {
			t.Fatal("expected the in-session fallthrough error")
		}
		if !strings.Contains(err.Error(), "attempt[1] backend=yoetz") || !strings.Contains(err.Error(), "fall-through=") {
			t.Fatalf("new-profile fallthrough error dropped attempt evidence: %v", err)
		}
	})

	t.Run("existing profile", func(t *testing.T) {
		project := t.TempDir()
		op := team.DefaultOperator()
		tm := team.Team{Operator: &op, Orchestrated: true, Lead: "cto", LeadMode: team.LeadModePlanner, ExecutionMode: executionModeProjectLead, Members: []team.Member{{Role: "cto", Handle: "cto", Binary: "codex", ActorMode: team.ActorModeReview}}}
		if err := team.WriteProfile(project, "reusable", tm); err != nil {
			t.Fatal(err)
		}
		if err := rules.Write(project, "# Existing reviewed rules\n"); err != nil {
			t.Fatal(err)
		}
		deps := simpleWizardTestDependenciesForMembers(t, project, tm.Members)
		deps.RunGoalDraft = fallThroughDraft
		err := runWizardWithDependencies([]string{"A fresh workstream", "--project", project, "--profile", "reusable", "--session", "fresh", "--yes"}, "test", deps, strings.NewReader(""), io.Discard, io.Discard)
		if err == nil {
			t.Fatal("expected the in-session fallthrough error")
		}
		if !strings.Contains(err.Error(), "attempt[1] backend=yoetz") || !strings.Contains(err.Error(), "fall-through=") {
			t.Fatalf("existing-profile fallthrough error dropped attempt evidence: %v", err)
		}
	})
}

type wizardMutationReader struct {
	mutate func()
	done   bool
}

func (r *wizardMutationReader) Read(p []byte) (int, error) {
	if r.done {
		return 0, io.EOF
	}
	r.done = true
	r.mutate()
	return copy(p, "yes\n"), nil
}

func simpleWizardTestDependencies(t *testing.T, project string) simpleWizardDependencies {
	members := []team.Member{
		{Role: "cto", Handle: "cto", Binary: "codex"},
		{Role: "fullstack", Handle: "fullstack", Binary: "claude"},
		{Role: "senior-dev", Handle: "senior-dev", Binary: "codex"},
	}
	return simpleWizardTestDependenciesForMembers(t, project, members)
}

func simpleWizardTestDependenciesForMembers(t *testing.T, project string, members []team.Member) simpleWizardDependencies {
	t.Helper()
	configPath := filepath.Join(t.TempDir(), "config.json")
	t.Setenv("AMQ_SQUAD_CONFIG", configPath)
	cfg := userconfig.Config{Drafter: &drafter.Config{Chain: []string{drafter.BackendCodex}, OnFailure: drafter.FailureError}}
	if _, err := userconfig.Write(cfg); err != nil {
		t.Fatal(err)
	}
	goal := "Ship the reviewed change"
	if len(members) == 1 {
		goal = "A fresh workstream"
	}
	return simpleWizardDependencies{
		Now:        func() time.Time { return time.Date(2026, 8, 13, 9, 0, 0, 0, time.UTC) },
		LookPath:   func(name string) (string, error) { return "/test/bin/" + name, nil },
		ReadConfig: func() (userconfig.Config, error) { return cfg, nil },
		ConfigPath: func() (string, error) { return configPath, nil },
		RunGoalDraft: func(_ context.Context, _ *drafter.Config, _ drafter.Request) (drafter.Result, error) {
			return drafter.Result{Text: validSimpleStartBriefDraft(map[bool]string{true: "fresh", false: "issue-709"}[len(members) == 1], goal, members...), Evidence: drafter.Evidence{Backend: drafter.BackendCodex, ExitCode: 0}}, nil
		},
		RunRules: func(context.Context, *drafter.Config, drafter.Request) (drafter.Result, error) {
			t.Fatal("built-in roster should not require rules drafter")
			return drafter.Result{}, nil
		},
		RunRole: func(context.Context, *drafter.Config, drafter.Request) (drafter.Result, error) {
			t.Fatal("built-in roster should not require role drafter")
			return drafter.Result{}, nil
		},
		Start: func([]string, simpleStartDependencies, io.Reader, io.Writer) error {
			t.Fatal("preview must not call start")
			return nil
		},
	}
}
