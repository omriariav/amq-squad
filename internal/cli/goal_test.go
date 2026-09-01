package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/v2/internal/drafter"
	"github.com/omriariav/amq-squad/v2/internal/team"
)

func fakeGoalGh(t *testing.T, body string, returnErr error, captured *[]string) {
	t.Helper()
	prev := goalGhRun
	goalGhRun = func(args ...string) ([]byte, error) {
		if captured != nil {
			*captured = append([]string(nil), args...)
		}
		if returnErr != nil {
			return nil, returnErr
		}
		return []byte(body), nil
	}
	t.Cleanup(func() { goalGhRun = prev })
}

func TestSimpleGoalSendsOneOperatorTodoWithoutGoalState(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	writeDispatchTeam(t, dir)
	snapshotSquadState := func() string {
		var snapshot strings.Builder
		root := filepath.Join(dir, team.DirName)
		if err := filepath.Walk(root, func(path string, info os.FileInfo, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if !info.Mode().IsRegular() {
				return nil
			}
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			body, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			fmt.Fprintf(&snapshot, "%s\x00%d\x00%s\x00", rel, info.Mode().Perm(), body)
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		return snapshot.String()
	}
	before := snapshotSquadState()
	regularFiles := func(root string) []string {
		var files []string
		err := filepath.Walk(root, func(path string, info os.FileInfo, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if info.Mode().IsRegular() {
				files = append(files, path)
			}
			return nil
		})
		if err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
		return files
	}
	sessionRoot := filepath.Join(dir, ".agent-mail", "issue-96")
	if files := regularFiles(sessionRoot); len(files) != 0 {
		t.Fatalf("goal session root started with files: %v", files)
	}
	calls := withAMQCommandSeams(t,
		amqEnv{Root: filepath.Join(dir, ".agent-mail", "{session}"), BaseRoot: filepath.Join(dir, ".agent-mail")},
		"Sent goal-simple to cto\n",
	)
	previousRun := runAMQCommand
	runAMQCommand = func(req amqCommandRequest) ([]byte, error) {
		out, err := previousRun(req)
		if err != nil {
			return out, err
		}
		messageDir := filepath.Join(sessionRoot, "agents", "cto", "inbox", "new")
		if err := os.MkdirAll(messageDir, 0o755); err != nil {
			return nil, err
		}
		if err := os.WriteFile(filepath.Join(messageDir, "goal-simple.md"), []byte("one durable goal message\n"), 0o644); err != nil {
			return nil, err
		}
		return out, nil
	}
	t.Cleanup(func() { runAMQCommand = previousRun })
	goalText := "  ship the simple path\n\nAcceptance: preserve exact text.  "

	stdout, stderr, err := captureOutput(t, func() error {
		return runGoal([]string{
			"--project", dir, "--session", "issue-96", "--goal", goalText, "--json",
		})
	})
	if err != nil {
		t.Fatalf("simple goal: %v\nstderr:\n%s", err, stderr)
	}
	env := decodeJSONEnvelope[mutationResult](t, stdout)
	if env.Kind != "goal" || env.Data.Status != "sent" || env.Data.Role != "cto" || env.Data.Handle != "cto" {
		t.Fatalf("simple goal result = %+v", env)
	}
	if len(*calls) != 1 {
		t.Fatalf("AMQ calls = %d, want exactly one send: %+v", len(*calls), *calls)
	}
	call := (*calls)[0]
	for flag, want := range map[string]string{
		"me": "user", "to": "cto", "thread": "p2p/cto__user", "kind": "todo",
		"subject": "GOAL: issue-96", "body": "-",
	} {
		if got := amqFlagValue(call.Arg, flag); got != want {
			t.Fatalf("goal send --%s = %q, want %q; args=%v", flag, got, want, call.Arg)
		}
	}
	body, err := io.ReadAll(call.Stdin)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != goalText {
		t.Fatalf("goal stdin = %q", body)
	}
	if err := runGoal([]string{
		"--project", dir, "--session", "issue-96", "--role", "qa", "--goal", goalText,
	}); err == nil || !strings.Contains(err.Error(), "flag provided but not defined: -role") {
		t.Fatalf("direct goal accepted a non-lead role override: %v", err)
	}
	if len(*calls) != 1 {
		t.Fatalf("rejected role override sent AMQ: %+v", *calls)
	}
	if files := regularFiles(sessionRoot); len(files) != 1 || files[0] != filepath.Join(sessionRoot, "agents", "cto", "inbox", "new", "goal-simple.md") {
		t.Fatalf("direct goal must add exactly one session-root message file: %v", files)
	}
	if after := snapshotSquadState(); after != before {
		t.Fatalf("simple goal mutated local .amq-squad state\nbefore=%q\nafter=%q", before, after)
	}
	for _, path := range []string{
		goalAttemptDir(dir, team.DefaultProfile, "issue-96"),
		filepath.Join(dir, team.DirName, "prepared"),
	} {
		if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
			t.Fatalf("simple goal wrote forbidden local state %s: %v", path, statErr)
		}
	}
}

// TestGoalLegacySubcommandsAreGone is gh#761's third named acceptance test:
// each of the five deleted subcommands (apply/claim/deliver/retry-attempt/
// start) still prints a specific redirect and exits 1, rather than falling
// through to a generic unknown-subcommand error. runGoalWithVersion returning
// a UsageError is what the CLI entry point (Run in cli.go) turns into a
// printed message plus a non-zero exit -- asserting the UsageError here is
// the unit-level proof of "prints the redirect and exits 1".
func TestGoalLegacySubcommandsAreGone(t *testing.T) {
	for _, name := range []string{"apply", "claim", "deliver", "retry-attempt", "start"} {
		t.Run(name, func(t *testing.T) {
			err := runGoalWithVersion([]string{name}, "dev")
			if err == nil {
				t.Fatalf("goal %s: want an error, got nil (removed subcommand must not silently succeed)", name)
			}
			if _, ok := err.(UsageError); !ok {
				t.Fatalf("goal %s: error type = %T, want UsageError (the type the CLI entry point maps to a printed message + exit 1)", name, err)
			}
			if !strings.Contains(err.Error(), name) {
				t.Fatalf("goal %s: error does not name the removed subcommand: %q", name, err.Error())
			}
			if !strings.Contains(err.Error(), "removed") || !strings.Contains(err.Error(), "goal --goal TEXT") {
				t.Fatalf("goal %s: error does not redirect to the replacement verb: %q", name, err.Error())
			}
		})
	}

	// supervise-resume and draft are NOT removed -- confirm the redirect is
	// scoped to exactly the five deleted names, not a blanket refusal.
	if err := runGoalWithVersion([]string{"draft"}, "dev"); err != nil {
		if _, ok := err.(UsageError); ok && strings.Contains(err.Error(), "removed in v2.31.0") {
			t.Fatalf("goal draft must not be treated as removed: %v", err)
		}
	}
}

func TestGoalDraftJSONIncludesMilestoneIssues(t *testing.T) {
	var captured []string
	fakeGoalGh(t, `[
  {"number":216,"title":"orchestrator fast path","url":"https://github.com/o/r/issues/216","state":"OPEN"},
  {"number":215,"title":"goal draft","url":"https://github.com/o/r/issues/215","state":"OPEN"}
]`, nil, &captured)

	stdout, stderr, err := captureOutput(t, func() error {
		return runGoalDraft([]string{
			"--goal", "deliver GitHub milestone v2.7.0",
			"--repo", "omriariav/amq-squad",
			"--milestone", "v2.7.0",
			"--session", "v2-7-0",
			"--profile", "codex-v2-7-0",
			"--codex-only",
			"--json",
		})
	})
	if err != nil {
		t.Fatalf("goal draft: %v\nstderr:\n%s", err, stderr)
	}
	wantArgs := []string{"issue", "list", "--repo", "omriariav/amq-squad", "--milestone", "v2.7.0", "--state", "all", "--limit", "200", "--json", "number,title,url,state"}
	if fmt.Sprint(captured) != fmt.Sprint(wantArgs) {
		t.Fatalf("gh args = %v, want %v", captured, wantArgs)
	}
	var env jsonEnvelope
	if err := json.Unmarshal([]byte(stdout), &env); err != nil {
		t.Fatalf("decode envelope: %v\n%s", err, stdout)
	}
	if env.Kind != "goal_draft" {
		t.Fatalf("kind = %q, want goal_draft", env.Kind)
	}
	raw, err := json.Marshal(env.Data)
	if err != nil {
		t.Fatal(err)
	}
	var data goalDraftData
	if err := json.Unmarshal(raw, &data); err != nil {
		t.Fatalf("decode data: %v", err)
	}
	if !data.PreviewOnly || data.Composition != "seeded" {
		t.Fatalf("draft should be preview-only seeded: %+v", data)
	}
	if data.Session != "v2-7-0" || data.Profile != "codex-v2-7-0" {
		t.Fatalf("session/profile mismatch: %+v", data)
	}
	if data.GoalBinding.Mode != "prompt_goal_pending" || data.GoalBinding.NativeGoal || data.GoalBinding.Verified {
		t.Fatalf("goal binding mismatch: %+v", data.GoalBinding)
	}
	if len(data.IssueSources) != 2 || data.IssueSources[0].Number != 215 || data.IssueSources[1].Number != 216 {
		t.Fatalf("issues not sorted/included: %+v", data.IssueSources)
	}
	if data.BriefSkeleton != "" || data.BriefDraft == nil || !data.BriefDraft.Manual {
		t.Fatalf("unconfigured drafter must stop with only a manual prompt: %+v", data)
	}
	for _, want := range []string{"#215 goal draft", "https://github.com/o/r/issues/215", "AMQ-SQUAD PROMPT GOAL v1"} {
		if !strings.Contains(data.BriefDraft.Prompt+data.OrchestratorPrompt, want) {
			t.Errorf("draft missing %q:\n%+v", want, data)
		}
	}
}

func TestGoalDraftPlannerLeadModeSurfacesDelegationContract(t *testing.T) {
	stdout, stderr, err := captureOutput(t, func() error {
		return runGoalDraft([]string{
			"--goal", "ship planner mode",
			"--session", "issue-350",
			"--profile", "planner",
			"--lead", "cto",
			"--lead-mode", "planner",
			"--json",
		})
	})
	if err != nil {
		t.Fatalf("goal draft planner: %v\nstderr:\n%s", err, stderr)
	}
	env := decodeJSONEnvelope[goalDraftData](t, stdout)
	if env.Data.LeadMode != team.LeadModePlanner || env.Data.Execution.LeadMode != team.LeadModePlanner {
		t.Fatalf("lead mode not surfaced in draft: %+v", env.Data)
	}
	if env.Data.Execution.ImplementationAllowed {
		t.Fatalf("planner lead draft must disallow lead implementation: %+v", env.Data.Execution)
	}
	if env.Data.Execution.MutableActor != "delegated_workers" {
		t.Fatalf("mutable_actor = %q, want delegated_workers", env.Data.Execution.MutableActor)
	}
	if !strings.Contains(env.Data.OrchestratorPrompt, "lead_mode: planner") {
		t.Fatalf("orchestrator prompt missing lead mode:\n%s", env.Data.OrchestratorPrompt)
	}
	var foundMutation bool
	for _, mutation := range env.Data.ApplyableMutations {
		if strings.Contains(mutation.Command, "--lead-mode planner") {
			foundMutation = true
			break
		}
	}
	if !foundMutation {
		t.Fatalf("applyable mutations must persist planner mode: %+v", env.Data.ApplyableMutations)
	}
}

func TestBuildGoalDraftTargetRootSource(t *testing.T) {
	control := t.TempDir()
	match := filepath.Join(control, "amq-squad")
	if err := os.MkdirAll(match, 0o755); err != nil {
		t.Fatal(err)
	}
	writeGitConfig(t, match, "git@github.com:omriariav/amq-squad.git")

	base := goalDraftOptions{Goal: "g", Mode: executionModeGlobalOrchestrator, ControlRoot: control, Session: "s", Profile: "p", Lead: "cto", Composition: team.CompositionSeeded, Visibility: visibilityPlan}

	resolved := base
	resolved.Repo = "omriariav/amq-squad"
	d, err := buildGoalDraft(resolved)
	if err != nil {
		t.Fatalf("buildGoalDraft resolved: %v", err)
	}
	if d.TargetProjectRootSource != targetRootSourceResolvedUnconfirmed || d.TargetProjectRoot != match {
		t.Fatalf("resolved: source=%q target=%q, want resolved_unconfirmed %s", d.TargetProjectRootSource, d.TargetProjectRoot, match)
	}
	// resolved_unconfirmed must NOT leak into actionable start surfaces or the
	// execution contract target.
	if strings.Contains(d.OrchestratorPrompt, "target_project_root:") {
		t.Fatalf("resolved_unconfirmed must not appear in OrchestratorPrompt:\n%s", d.OrchestratorPrompt)
	}
	if mutationsContainTargetRoot(d) {
		t.Fatalf("resolved_unconfirmed must not appear in applyable mutations: %+v", d.ApplyableMutations)
	}
	if d.Execution.TargetProjectRoot != "" {
		t.Fatalf("resolved_unconfirmed execution.target_project_root must be empty, got %q", d.Execution.TargetProjectRoot)
	}

	none := base
	none.Repo = "nobody/nothing"
	d2, err := buildGoalDraft(none)
	if err != nil {
		t.Fatalf("buildGoalDraft unresolved: %v", err)
	}
	if d2.TargetProjectRootSource != targetRootSourceUnresolved || d2.TargetProjectRoot != "" {
		t.Fatalf("unresolved: source=%q target=%q, want unresolved + empty", d2.TargetProjectRootSource, d2.TargetProjectRoot)
	}
	if strings.Contains(d2.OrchestratorPrompt, "target_project_root:") || mutationsContainTargetRoot(d2) || d2.Execution.TargetProjectRoot != "" {
		t.Fatalf("unresolved must not leak target into prompt/mutations/execution: prompt=%q mut=%+v exec=%q", d2.OrchestratorPrompt, d2.ApplyableMutations, d2.Execution.TargetProjectRoot)
	}

	provided := base
	provided.TargetProjectRoot = match
	d3, err := buildGoalDraft(provided)
	if err != nil {
		t.Fatalf("buildGoalDraft provided: %v", err)
	}
	if d3.TargetProjectRootSource != targetRootSourceProvided {
		t.Fatalf("provided: source=%q, want provided", d3.TargetProjectRootSource)
	}
	// provided DOES carry into start surfaces + the execution contract.
	if !strings.Contains(d3.OrchestratorPrompt, "target_project_root:") || !mutationsContainTargetRoot(d3) {
		t.Fatalf("provided must appear in prompt + mutations: prompt=%q mut=%+v", d3.OrchestratorPrompt, d3.ApplyableMutations)
	}
	if d3.Execution.TargetProjectRoot == "" {
		t.Fatal("provided execution.target_project_root must be set")
	}
}

func mutationsContainTargetRoot(d goalDraftData) bool {
	for _, m := range d.ApplyableMutations {
		if strings.Contains(m.Command, "--target-project-root") {
			return true
		}
	}
	return false
}

func TestGoalDraftSkillInvocationCompleteness(t *testing.T) {
	control := t.TempDir()

	base := goalDraftOptions{Goal: "g", Mode: executionModeGlobalOrchestrator, ControlRoot: control, Session: "s", Profile: "p", Lead: "cto", Composition: team.CompositionSeeded, Visibility: visibilityPlan}
	d, err := buildGoalDraft(base)
	if err != nil {
		t.Fatalf("buildGoalDraft: %v", err)
	}
	inv := d.SkillInvocation
	if !strings.Contains(inv, "--register-orchestrator") {
		t.Fatalf("global_orchestrator invocation must include --register-orchestrator:\n%s", inv)
	}
	for _, line := range strings.Split(inv, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "/amq-squad-orchestrator") && strings.Contains(line, "--target-project-root") {
			t.Fatalf("unconfirmed target must not appear as an executable flag:\n%s", line)
		}
	}
	if !strings.Contains(inv, "# recommended for a Codex lead:") {
		t.Fatalf("effort must be a recommendation comment, not a silent flag:\n%s", inv)
	}
	if strings.Contains(inv, "--codex-args") && !strings.Contains(inv, "# recommended") {
		t.Fatalf("--codex-args must only appear inside a recommendation comment:\n%s", inv)
	}
	if !strings.Contains(inv, "# REQUIRED before start: --target-project-root") {
		t.Fatalf("unresolved global target must be flagged as a required comment:\n%s", inv)
	}

	provided := base
	provided.TargetProjectRoot = control
	provided.ProvidedFields = map[string]bool{"target_project_root": true}
	d2, err := buildGoalDraft(provided)
	if err != nil {
		t.Fatalf("buildGoalDraft provided: %v", err)
	}
	if !strings.Contains(d2.SkillInvocation, "--target-project-root") {
		t.Fatalf("provided target must appear in the invocation:\n%s", d2.SkillInvocation)
	}
	if strings.Contains(d2.SkillInvocation, "# REQUIRED before start: --target-project-root") {
		t.Fatalf("provided target must not also be flagged as required:\n%s", d2.SkillInvocation)
	}
}

// TestGoalDraftLaunchMutationOmitsDefaultReasoningEffort drives the REAL goal
// draft CLI (not direct struct mutation) to prove: the seeded default reasoning
// effort is never a live --codex-args in applyable launch mutations, and an
// operator-supplied --codex-args flows through to the launch mutation (#291).
func TestGoalDraftLaunchMutationOmitsDefaultReasoningEffort(t *testing.T) {
	draftViaCLI := func(t *testing.T, extra ...string) goalDraftData {
		t.Helper()
		args := append([]string{"--goal", "g", "--mode", "global_orchestrator", "--session", "s", "--json"}, extra...)
		stdout, stderr, err := captureOutput(t, func() error { return runGoalDraft(args) })
		if err != nil {
			t.Fatalf("runGoalDraft %v: %v\n%s", args, err, stderr)
		}
		return decodeJSONEnvelope[goalDraftData](t, stdout).Data
	}

	// Default (no --codex-args): no live flag, inert recommendation present.
	d := draftViaCLI(t)
	for _, m := range d.ApplyableMutations {
		if strings.Contains(m.Command, "--codex-args") {
			t.Fatalf("default effort must not be a live --codex-args in launch mutations: %q", m.Command)
		}
	}
	var sawRec bool
	for _, m := range d.ApplyableMutations {
		if strings.Contains(m.Reason, "Recommended (not applied): add --codex-args") {
			sawRec = true
		}
	}
	if !sawRec {
		t.Fatalf("expected an inert effort recommendation on the launch mutation: %+v", d.ApplyableMutations)
	}

	// Operator explicitly supplies --codex-args via the real CLI: it flows into
	// the applyable launch command and the recommendation is dropped.
	d2 := draftViaCLI(t, "--codex-args", "-c model_reasoning_effort=high")
	var sawFlag bool
	for _, m := range d2.ApplyableMutations {
		if strings.Contains(m.Command, "--codex-args") && strings.Contains(m.Command, "model_reasoning_effort=high") {
			sawFlag = true
		}
		if strings.Contains(m.Reason, "Recommended (not applied)") {
			t.Fatalf("explicit codex args must not also emit the inert recommendation: %+v", m)
		}
	}
	if !sawFlag {
		t.Fatalf("explicitly provided --codex-args must appear in the launch mutation: %+v", d2.ApplyableMutations)
	}
}

func TestGoalDraftFieldSourcesAndSteps(t *testing.T) {
	d, err := buildGoalDraft(goalDraftOptions{
		Goal: "g", Mode: executionModeProjectLead, Session: "s", Profile: "p", Lead: "cto",
		Composition: team.CompositionSeeded, Visibility: visibilityPlan,
		ProvidedFields: map[string]bool{"session": true, "mode": true},
	})
	if err != nil {
		t.Fatalf("buildGoalDraft: %v", err)
	}
	if d.FieldSources["session"] != targetRootSourceProvided || d.FieldSources["mode"] != targetRootSourceProvided {
		t.Fatalf("provided fields mislabeled: %+v", d.FieldSources)
	}
	if d.FieldSources["profile"] != targetRootSourceDefault || d.FieldSources["lead"] != targetRootSourceDefault {
		t.Fatalf("unset fields must be default: %+v", d.FieldSources)
	}
	if d.FieldSources["target_project_root"] != targetRootSourceDefault {
		t.Fatalf("target_project_root source = %q, want default", d.FieldSources["target_project_root"])
	}
	if len(d.Steps) != 3 || d.Steps[0].Title != "Preview" || d.Steps[1].Title != "Create / launch the visible lead" || d.Steps[2].Title != "Monitor through the lead" {
		t.Fatalf("steps = %+v, want Preview/Create/Monitor", d.Steps)
	}
	for _, s := range d.Steps {
		if s.AboutToHappen == "" || s.NextGate == "" {
			t.Fatalf("step %d missing guidance: %+v", s.Number, s)
		}
	}
}

func TestGoalDraftMarkdownIsPreviewOnly(t *testing.T) {
	stdout, stderr, err := captureOutput(t, func() error {
		return runGoalDraft([]string{
			"--goal", "fix flaky launch targeting",
			"--session", "issue-225",
		})
	})
	if err != nil {
		t.Fatalf("goal draft: %v\nstderr:\n%s", err, stderr)
	}
	for _, want := range []string{
		"# preview_only: true",
		"# composition: seeded",
		"# visibility: sibling-tabs",
		"## Brief Drafting Prompt (manual completion required)",
		"Config source: in_session",
		"amq send --to user --thread gate/spawn-fullstack",
		"amq-squad init",
		"amq-squad agent up codex",
		"AMQ-SQUAD PROMPT GOAL v1",
		"None. Add native tasks, send ordinary AMQ todo messages",
		"Default visibility is sibling-tabs",
		"Seeded composition remains the default",
		"Use exactly these level-two sections",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("markdown missing %q:\n%s", want, stdout)
		}
	}
	if strings.Contains(stdout, "amq-squad dispatch") {
		t.Fatalf("simple goal draft must not generate prepared dispatch commands:\n%s", stdout)
	}
}

func TestGoalDraftNamedProfileCommandsCarryNamespace(t *testing.T) {
	stdout, stderr, err := captureOutput(t, func() error {
		return runGoalDraft([]string{
			"--goal", "ship deterministic namespaces",
			"--session", "main",
			"--profile", "release",
			"--json",
		})
	})
	if err != nil {
		t.Fatalf("goal draft: %v\nstderr:\n%s", err, stderr)
	}
	env := decodeJSONEnvelope[goalDraftData](t, stdout)
	if env.Data.Namespace.ID != "release/main" {
		t.Fatalf("namespace = %+v, want release/main", env.Data.Namespace)
	}
	if env.Data.GoalBinding.Mode != "prompt_goal_pending" || env.Data.GoalBinding.Source != "orchestrator-prompt" {
		t.Fatalf("goal binding = %+v", env.Data.GoalBinding)
	}
	for _, dispatch := range env.Data.Dispatches {
		for _, want := range []string{"--profile release", "--session main"} {
			if !strings.Contains(dispatch.Command, want) {
				t.Fatalf("dispatch command missing %q: %s", want, dispatch.Command)
			}
		}
	}
	for _, mutation := range env.Data.ApplyableMutations {
		switch mutation.Title {
		case "add t1", "add t2", "add t3":
			if !strings.Contains(mutation.Command, "--profile release") {
				t.Fatalf("%s mutation dropped profile: %s", mutation.Title, mutation.Command)
			}
		}
		if mutation.Title == "write brief" || strings.Contains(mutation.Command, "amq-squad brief") {
			t.Fatalf("goal draft retained removed brief mutation: %+v", mutation)
		}
	}
}

func TestGoalDraftCustomLeadCarriesThroughPlan(t *testing.T) {
	stdout, stderr, err := captureOutput(t, func() error {
		return runGoalDraft([]string{
			"--goal", "ship release through visible lead",
			"--session", "v2-9-0-release",
			"--profile", "codex-v2-9-0",
			"--lead", "release-lead",
			"--json",
		})
	})
	if err != nil {
		t.Fatalf("goal draft: %v\nstderr:\n%s", err, stderr)
	}
	env := decodeJSONEnvelope[goalDraftData](t, stdout)
	if env.Data.Lead != "release-lead" {
		t.Fatalf("lead = %q, want release-lead", env.Data.Lead)
	}
	if env.Data.Roster[0].Role != "release-lead" {
		t.Fatalf("first roster member = %+v, want release-lead lead", env.Data.Roster[0])
	}
	for _, mutation := range env.Data.ApplyableMutations {
		if mutation.Title == "initialize profile" && !strings.Contains(mutation.Command, "--lead release-lead") {
			t.Fatalf("team init mutation dropped lead: %s", mutation.Command)
		}
	}
	for _, dispatch := range env.Data.Dispatches {
		if !strings.Contains(dispatch.Thread, "release-lead") || !strings.Contains(dispatch.Body, "release-lead over AMQ") {
			t.Fatalf("dispatch does not route reports to lead: %+v", dispatch)
		}
	}
	if !strings.Contains(env.Data.OrchestratorPrompt, "role: release-lead") {
		t.Fatalf("orchestrator prompt dropped custom lead: %s", env.Data.OrchestratorPrompt)
	}
	if len(env.Data.PersonaDrafts) != 1 {
		t.Fatalf("persona drafts = %+v, want one custom lead draft", env.Data.PersonaDrafts)
	}
	for _, want := range []string{"amq-squad role draft release-lead", "--binary codex", "--profile codex-v2-9-0", "--session v2-9-0-release", "--peers 'fullstack,senior-dev'"} {
		if !strings.Contains(env.Data.PersonaDrafts[0].Command, want) {
			t.Fatalf("persona draft command missing %q: %s", want, env.Data.PersonaDrafts[0].Command)
		}
	}
}

func TestGoalDraftUsesConfiguredDrafterAndValidatesBrief(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(configPath, []byte(`{"drafter":{"chain":["yoetz","claude"],"model":"gemini/flash"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AMQ_SQUAD_CONFIG", configPath)
	data, err := buildGoalDraft(goalDraftOptions{
		Goal: "ship reviewed prose", Session: "reviewed-prose", Profile: "reviewed-prose",
		Mode: executionModeProjectLead, Composition: team.CompositionSeeded, Visibility: visibilitySiblingTabs,
	})
	if err != nil {
		t.Fatal(err)
	}
	members := make([]team.Member, 0, len(data.Roster))
	for _, member := range data.Roster {
		members = append(members, team.Member{Role: member.Role, Handle: member.Handle, Binary: member.Binary})
	}
	document := validSimpleStartBriefDraft(data.Session, data.Goal, members...)
	attempts := []drafter.Evidence{
		{Backend: drafter.BackendYoetz, CommandDisplay: "yoetz ask", ExitCode: 17, Failure: "missing credentials"},
		{Backend: drafter.BackendClaude, CommandDisplay: "claude -p", ExitCode: 0},
	}
	previous := runGoalDrafter
	runGoalDrafter = func(_ context.Context, cfg *drafter.Config, request drafter.Request) (drafter.Result, error) {
		if cfg == nil || len(cfg.EffectiveBackends()) != 2 || cfg.EffectiveBackends()[0] != drafter.BackendYoetz || cfg.EffectiveBackends()[1] != drafter.BackendClaude {
			t.Fatalf("goal drafter config = %+v", cfg)
		}
		if !strings.Contains(request.Prompt, data.Goal) || !strings.Contains(request.Prompt, "## Team shape") {
			t.Fatalf("goal drafter prompt missing goal/team contract:\n%s", request.Prompt)
		}
		return drafter.Result{Text: document, Evidence: attempts[1], Attempts: attempts}, nil
	}
	t.Cleanup(func() { runGoalDrafter = previous })
	if err := applyGoalBriefDraft(&data); err != nil {
		t.Fatal(err)
	}
	if data.BriefSkeleton != document || data.BriefDraft == nil || data.BriefDraft.Manual || data.BriefDraft.ConfigSource != drafter.SourceGlobal {
		t.Fatalf("configured goal brief draft = %+v\n%s", data.BriefDraft, data.BriefSkeleton)
	}
	if len(data.BriefDraft.Attempts) != 2 || data.BriefDraft.Attempts[0].Failure != "missing credentials" || data.BriefDraft.Attempts[1].CommandDisplay != "claude -p" {
		t.Fatalf("configured goal brief attempts = %+v", data.BriefDraft.Attempts)
	}

	runGoalDrafter = func(context.Context, *drafter.Config, drafter.Request) (drafter.Result, error) {
		return drafter.Result{Text: strings.Replace(document, "## Source", "## Background", 1), Evidence: attempts[1], Attempts: attempts}, nil
	}
	if err := applyGoalBriefDraft(&data); err == nil || !strings.Contains(err.Error(), `unexpected level-two heading "## Background"`) {
		t.Fatalf("invalid configured goal brief error = %v", err)
	} else {
		for _, want := range []string{
			"drafter config source: global",
			"attempt[1] backend=yoetz", `command="yoetz ask"`, `fall-through="missing credentials"`,
			"attempt[2] backend=claude", `command="claude -p"`,
		} {
			if !strings.Contains(err.Error(), want) {
				t.Fatalf("invalid configured goal brief missing %q: %v", want, err)
			}
		}
	}

	runGoalDrafter = func(context.Context, *drafter.Config, drafter.Request) (drafter.Result, error) {
		return drafter.Result{Evidence: attempts[1], Attempts: attempts}, errors.New("configured chain exhausted")
	}
	if err := applyGoalBriefDraft(&data); err == nil || !strings.Contains(err.Error(), "configured chain exhausted") {
		t.Fatalf("fail-closed goal brief error = %v", err)
	} else {
		for _, want := range []string{
			"drafter config source: global",
			"attempt[1] backend=yoetz", `fall-through="missing credentials"`,
			"attempt[2] backend=claude", `command="claude -p"`,
		} {
			if !strings.Contains(err.Error(), want) {
				t.Fatalf("fail-closed goal brief missing %q: %v", want, err)
			}
		}
	}
}

func TestGoalDraftExecutionModeContract(t *testing.T) {
	stdout, stderr, err := captureOutput(t, func() error {
		return runGoalDraftWithVersion([]string{
			"--goal", "deliver mode-safe orchestration",
			"--session", "v2-10-0",
			"--profile", "codex-v2-10-0",
			"--lead", "release-lead",
			"--mode", "global_orchestrator",
			"--control-root", "/tmp/control",
			"--target-project-root", "/tmp/project",
			"--target-contract", "2.10.0",
			"--json",
		}, "2.9.0")
	})
	if err != nil {
		t.Fatalf("goal draft: %v\nstderr:\n%s", err, stderr)
	}
	env := decodeJSONEnvelope[goalDraftData](t, stdout)
	exec := env.Data.Execution
	if exec.Mode != executionModeGlobalOrchestrator || exec.ImplementationAllowed {
		t.Fatalf("execution = %+v, want global orchestrator without implementation", exec)
	}
	if exec.ControlRoot != "/tmp/control" || exec.TargetProjectRoot != "/tmp/project" {
		t.Fatalf("execution roots = %q/%q", exec.ControlRoot, exec.TargetProjectRoot)
	}
	if exec.MutableActor != "" || exec.ModeError == "" || !exec.PollingRequired {
		t.Fatalf("global orchestrator boundary missing: %+v", exec)
	}
	if exec.VersionCompatibility.Compatible || exec.VersionCompatibility.RunningVersion != "2.9.0" || exec.VersionCompatibility.TargetContract != "2.10.0" {
		t.Fatalf("version compatibility = %+v, want 2.9.0 older than 2.10.0", exec.VersionCompatibility)
	}
	foundInit := false
	for _, mutation := range env.Data.ApplyableMutations {
		if mutation.Title == "initialize profile" && !strings.Contains(mutation.Command, "--mode global_orchestrator") {
			t.Fatalf("initialize profile command dropped mode: %s", mutation.Command)
		}
		if mutation.Title == "initialize profile" {
			foundInit = true
		}
	}
	if !foundInit {
		t.Fatalf("initialize profile mutation missing: %+v", env.Data.ApplyableMutations)
	}
	if !strings.Contains(env.Data.OrchestratorPrompt, "mode: global_orchestrator") || !strings.Contains(env.Data.OrchestratorPrompt, "target_contract: 2.10.0") {
		t.Fatalf("orchestrator prompt dropped execution metadata: %s", env.Data.OrchestratorPrompt)
	}
	for _, want := range []string{
		"Global orchestrator board",
		"name/repo/profile/session/lead/pane",
		"closed-run demotion",
	} {
		if !strings.Contains(env.Data.BriefSkeleton+env.Data.BriefDraft.Prompt+env.Data.SkillInvocation, want) {
			t.Fatalf("global orchestrator draft missing board guidance %q:\nbrief:\n%s\nskill invocation:\n%s", want, env.Data.BriefSkeleton, env.Data.SkillInvocation)
		}
	}
}

func TestGoalDraftAutonomousPreviewRequiresAndEmitsPolicy(t *testing.T) {
	stdout, stderr, err := captureOutput(t, func() error {
		return runGoalDraft([]string{
			"--goal", "deliver milestone safely",
			"--session", "v2-7-0",
			"--composition", "autonomous",
			"--max-agents", "5",
			"--max-total-spawns", "4",
			"--allowed-roles", "runtime-dev,reviewer",
			"--budget-turns", "40",
			"--json",
		})
	})
	if err != nil {
		t.Fatalf("goal draft autonomous: %v\nstderr:\n%s", err, stderr)
	}
	env := decodeJSONEnvelope[goalDraftData](t, stdout)
	if env.Data.Composition != "autonomous" || env.Data.AutonomousPolicy == nil {
		t.Fatalf("autonomous policy missing: %+v", env.Data)
	}
	if env.Data.AutonomousPolicy.MaxActiveAgents != 5 || env.Data.AutonomousPolicy.MaxTotalSpawns != 4 || env.Data.AutonomousPolicy.BudgetTurns != 40 {
		t.Fatalf("autonomous policy counters mismatch: %+v", env.Data.AutonomousPolicy)
	}
	if !strings.Contains(env.Data.BriefSkeleton+env.Data.BriefDraft.Prompt, "- Autonomous policy:") || !strings.Contains(env.Data.OrchestratorPrompt, "- composition: autonomous") {
		t.Fatalf("autonomous draft missing policy/prompt:\n%s\n%s", env.Data.BriefSkeleton, env.Data.OrchestratorPrompt)
	}
}

func TestGoalDraftJSONIncludesVisibleLaunchMutation(t *testing.T) {
	stdout, stderr, err := captureOutput(t, func() error {
		return runGoalDraft([]string{
			"--goal", "ship visible setup handoff",
			"--session", "visible-setup",
			"--profile", "codex-visible-setup",
			"--json",
		})
	})
	if err != nil {
		t.Fatalf("goal draft: %v\nstderr:\n%s", err, stderr)
	}
	env := decodeJSONEnvelope[goalDraftData](t, stdout)
	if env.Data.GoalBinding.Command != env.Data.OrchestratorPrompt {
		t.Fatalf("draft binding command should be the visible lead prompt:\n%q\n%q", env.Data.GoalBinding.Command, env.Data.OrchestratorPrompt)
	}
	if got := goalBindingFromArgs("codex", []string{env.Data.OrchestratorPrompt}); got == nil || got.NativeGoal || got.Mode != "prompt_goal" || got.Goal != env.Data.Goal {
		_, _, parseErr := parseCodexGoalControlPrompt(env.Data.OrchestratorPrompt)
		t.Fatalf("generated visible lead prompt must be launch-record detectable as prompt_goal: %+v (parse: %v)\nprompt: %q", got, parseErr, env.Data.OrchestratorPrompt)
	}
	found := false
	for _, mutation := range env.Data.ApplyableMutations {
		if mutation.Title != "launch visible lead" {
			continue
		}
		found = true
		for _, want := range []string{
			"amq-squad agent up codex",
			"--session visible-setup",
			"--root .agent-mail/codex-visible-setup/visible-setup",
			"--team-profile codex-visible-setup",
			"AMQ-SQUAD PROMPT GOAL v1",
		} {
			if !strings.Contains(mutation.Command, want) {
				t.Fatalf("visible launch command missing %q: %q", want, mutation.Command)
			}
		}
		if !strings.Contains(mutation.Command, "ship visible setup handoff") {
			t.Fatalf("visible launch command = %q", mutation.Command)
		}
		if !strings.Contains(mutation.Reason, "binary-specific goal") {
			t.Fatalf("visible launch reason = %q", mutation.Reason)
		}
	}
	if !found {
		t.Fatalf("visible launch mutation missing: %+v", env.Data.ApplyableMutations)
	}
}

func TestGoalDraftSkillInvocationOutput(t *testing.T) {
	stdout, stderr, err := captureOutput(t, func() error {
		return runGoalDraft([]string{
			"--goal", "ship visible setup handoff",
			"--session", "visible-setup",
			"--profile", "codex-visible-setup",
			"--skill-invocation",
		})
	})
	if err != nil {
		t.Fatalf("goal draft --skill-invocation: %v\nstderr:\n%s", err, stderr)
	}
	for _, want := range []string{
		`/amq-squad-orchestrator --goal "ship visible setup handoff" --session "visible-setup" --profile "codex-visible-setup"`,
		`--mode "project_lead"`,
		`AMQ-SQUAD PROMPT GOAL v1`,
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("skill invocation missing %q in:\n%s", want, stdout)
		}
	}
	if strings.Contains(stdout, "# amq-squad goal draft") {
		t.Fatalf("--skill-invocation should print only the invocation block, got markdown:\n%s", stdout)
	}
}

func TestGoalDraftJSONIncludesSkillInvocation(t *testing.T) {
	stdout, stderr, err := captureOutput(t, func() error {
		return runGoalDraft([]string{
			"--goal", "ship visible setup handoff",
			"--session", "visible-setup",
			"--profile", "codex-visible-setup",
			"--json",
		})
	})
	if err != nil {
		t.Fatalf("goal draft --json: %v\nstderr:\n%s", err, stderr)
	}
	env := decodeJSONEnvelope[goalDraftData](t, stdout)
	if !strings.Contains(env.Data.SkillInvocation, `/amq-squad-orchestrator`) ||
		!strings.Contains(env.Data.SkillInvocation, env.Data.OrchestratorPrompt) {
		t.Fatalf("skill invocation missing orchestrator wrapper/prompt:\n%s", env.Data.SkillInvocation)
	}
}

func TestGoalDraftVisibilityOverrides(t *testing.T) {
	cases := []struct {
		visibility string
		wantTitle  string
		wantCmd    string
	}{
		{"detached", "launch detached visible lead", "AMQ-SQUAD PROMPT GOAL v1"},
		{"current", "launch visible lead in current pane", "AMQ-SQUAD PROMPT GOAL v1"},
		{"plan", "preview visible lead launch", "--dry-run"},
	}
	for _, tc := range cases {
		t.Run(tc.visibility, func(t *testing.T) {
			stdout, stderr, err := captureOutput(t, func() error {
				return runGoalDraft([]string{
					"--goal", "ship topology",
					"--session", "topo",
					"--visibility", tc.visibility,
					"--json",
				})
			})
			if err != nil {
				t.Fatalf("goal draft: %v\nstderr:\n%s", err, stderr)
			}
			env := decodeJSONEnvelope[goalDraftData](t, stdout)
			if env.Data.Visibility != tc.visibility {
				t.Fatalf("visibility = %q, want %q", env.Data.Visibility, tc.visibility)
			}
			found := false
			for _, mutation := range env.Data.ApplyableMutations {
				if mutation.Title == tc.wantTitle {
					found = true
					if !strings.Contains(mutation.Command, tc.wantCmd) || !strings.Contains(mutation.Command, "amq-squad agent up codex") {
						t.Fatalf("command = %q, want containing %q", mutation.Command, tc.wantCmd)
					}
				}
			}
			if !found {
				t.Fatalf("mutation %q missing: %+v", tc.wantTitle, env.Data.ApplyableMutations)
			}
		})
	}
}

func TestGoalDraftRejectsUnknownVisibility(t *testing.T) {
	_, _, err := captureOutput(t, func() error {
		return runGoalDraft([]string{"--goal", "ship topology", "--visibility", "hidden"})
	})
	if err == nil || !strings.Contains(err.Error(), "unsupported visibility") {
		t.Fatalf("want unsupported visibility error, got %v", err)
	}
}

func TestGoalDraftAutonomousRejectsMissingPolicy(t *testing.T) {
	_, _, err := captureOutput(t, func() error {
		return runGoalDraft([]string{"--goal", "deliver milestone", "--composition", "autonomous"})
	})
	if err == nil || !strings.Contains(err.Error(), "max_active_agents") {
		t.Fatalf("want missing autonomous policy error, got %v", err)
	}
}

func TestGoalDraftMilestoneRequiresRepo(t *testing.T) {
	_, _, err := captureOutput(t, func() error {
		return runGoalDraft([]string{"--goal", "deliver v2.7.0", "--milestone", "v2.7.0"})
	})
	if err == nil || !strings.Contains(err.Error(), "--milestone requires --repo") {
		t.Fatalf("want repo requirement, got %v", err)
	}
}

func TestGoalDraftMilestoneGhErrorNamesSource(t *testing.T) {
	fakeGoalGh(t, "", errors.New("not authenticated"), nil)
	_, _, err := captureOutput(t, func() error {
		return runGoalDraft([]string{"--goal", "deliver v2.7.0", "--repo", "o/r", "--milestone", "v2.7.0"})
	})
	if err == nil || !strings.Contains(err.Error(), "milestone") || !strings.Contains(err.Error(), "not authenticated") {
		t.Fatalf("error should name milestone gh source, got %v", err)
	}
}
