package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/v2/internal/launch"
	"github.com/omriariav/amq-squad/v2/internal/team"
	"github.com/omriariav/amq-squad/v2/internal/tmuxpane"
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
	if data.GoalBinding.Mode != "native_goal_pending" || !data.GoalBinding.NativeGoal || data.GoalBinding.Verified {
		t.Fatalf("goal binding mismatch: %+v", data.GoalBinding)
	}
	if len(data.IssueSources) != 2 || data.IssueSources[0].Number != 215 || data.IssueSources[1].Number != 216 {
		t.Fatalf("issues not sorted/included: %+v", data.IssueSources)
	}
	for _, want := range []string{"#215 goal draft", "https://github.com/o/r/issues/215", "/goal --goal"} {
		if !strings.Contains(data.BriefSkeleton+data.OrchestratorPrompt, want) {
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
	if !strings.Contains(env.Data.OrchestratorPrompt, "--lead-mode planner") {
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
	if strings.Contains(d.OrchestratorPrompt, "--target-project-root") {
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
	if strings.Contains(d2.OrchestratorPrompt, "--target-project-root") || mutationsContainTargetRoot(d2) || d2.Execution.TargetProjectRoot != "" {
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
	if !strings.Contains(d3.OrchestratorPrompt, "--target-project-root") || !mutationsContainTargetRoot(d3) {
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

func TestNormalizeOptionalStringFlagDefaultsAndConsumesValue(t *testing.T) {
	got := normalizeOptionalStringFlag([]string{"deliver", "--register-orchestrator", "--json"}, "--register-orchestrator", "orchestrator")
	want := []string{"deliver", "--register-orchestrator=orchestrator", "--json"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("bare optional flag = %v, want %v", got, want)
	}
	got = normalizeOptionalStringFlag([]string{"deliver", "--register-orchestrator", "global", "--json"}, "--register-orchestrator", "orchestrator")
	want = []string{"deliver", "--register-orchestrator=global", "--json"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("valued optional flag = %v, want %v", got, want)
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
		"## Brief Skeleton",
		"amq send --to user --thread gate/spawn-fullstack",
		"amq-squad team init",
		"amq-squad agent up codex",
		"-- '/goal --goal",
		"amq-squad dispatch --profile issue-225 --session issue-225",
		"Default visibility is sibling-tabs",
		"Seeded composition remains the default",
		"Visible lead binding: native_goal_pending",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("markdown missing %q:\n%s", want, stdout)
		}
	}
}

func TestGoalStartDryRunJSONIsReadOnlyPlan(t *testing.T) {
	setupFakeAMQSessionRoots(t)
	dir := seedTeam(t, team.Team{
		Members:       []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-96"}},
		Orchestrated:  true,
		Lead:          "cto",
		ExecutionMode: executionModeProjectLead,
	})
	oldSend := sendPromptToPane
	sendPromptToPane = func(string, string) error {
		t.Fatal("goal start --dry-run must not send to a pane")
		return nil
	}
	t.Cleanup(func() { sendPromptToPane = oldSend })

	stdout, stderr, err := captureOutput(t, func() error {
		return runGoal([]string{"start", "--project", dir, "--session", "issue-96", "--goal", "ship safely", "--dry-run", "--json"})
	})
	if err != nil {
		t.Fatalf("goal start dry-run: %v\nstderr:\n%s", err, stderr)
	}
	env := decodeJSONEnvelope[goalStartData](t, stdout)
	if env.Kind != "goal_start" || !env.Data.DryRun || env.Data.Status != "planned" {
		t.Fatalf("goal start envelope = %+v", env)
	}
	if env.Data.Project != dir || env.Data.Profile != team.DefaultProfile || env.Data.Session != "issue-96" || env.Data.Role != "cto" {
		t.Fatalf("goal start plan fields = %+v", env.Data)
	}
	if env.Data.Mode != executionModeProjectLead || env.Data.Goal != "ship safely" {
		t.Fatalf("goal start mode/goal = %+v", env.Data)
	}
	if len(env.Data.Actions) != 1 || env.Data.Actions[0].ID != "goal_deliver" || env.Data.Actions[0].ActionKind != "run" || !env.Data.Actions[0].Available {
		t.Fatalf("goal start actions = %+v", env.Data.Actions)
	}
	for _, want := range []string{"amq-squad goal deliver", "--project " + dir, "--session issue-96", "--role cto", "--goal 'ship safely'", "--json"} {
		if !strings.Contains(env.Data.DeliverCmd, want) {
			t.Fatalf("deliver command missing %q: %q", want, env.Data.DeliverCmd)
		}
	}
}

func TestGoalStartRequiresYesForDelivery(t *testing.T) {
	setupFakeAMQSessionRoots(t)
	dir := seedTeam(t, team.Team{
		Members:      []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-96"}},
		Orchestrated: true,
		Lead:         "cto",
	})
	_, _, err := captureOutput(t, func() error {
		return runGoal([]string{"start", "--project", dir, "--session", "issue-96", "--goal", "ship safely"})
	})
	if err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("goal start without --yes should be confirm-gated, got %v", err)
	}
}

func TestGoalStartRegisterOrchestratorDefersRosterWriteUntilYes(t *testing.T) {
	setupFakeAMQSessionRoots(t)
	dir := seedTeam(t, team.Team{
		Members:      []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-96"}},
		Orchestrated: true,
		Lead:         "cto",
	})
	_, _, err := captureOutput(t, func() error {
		return runGoal([]string{"start", "--project", dir, "--session", "issue-96", "--goal", "ship safely", "--register-orchestrator=global-orch"})
	})
	if err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("goal start --register-orchestrator without --yes should be confirm-gated, got %v", err)
	}
	cfg, err := team.Read(dir)
	if err != nil {
		t.Fatalf("read team: %v", err)
	}
	if _, ok := teamMemberByRole(cfg, goalOrchestratorRole); ok {
		t.Fatal("orchestrator roster write must not happen before --yes is confirmed")
	}
	if cfg.Lead != "cto" {
		t.Fatalf("team lead should be unchanged before --yes, got %q", cfg.Lead)
	}
}

func TestGoalStartYesJSONDeliversThroughGoalDeliverPath(t *testing.T) {
	base := setupFakeAMQSessionRoots(t)
	dir := seedTeam(t, team.Team{
		Members:       []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-96"}},
		Orchestrated:  true,
		Lead:          "cto",
		ExecutionMode: executionModeProjectLead,
	})
	agentDir := seedAgentRecord(t, base, "issue-96", "cto", launch.Record{
		CWD:      dir,
		Binary:   "codex",
		Handle:   "cto",
		Role:     "cto",
		Session:  "issue-96",
		AgentPID: 4242,
		Tmux:     &launch.TmuxInfo{PaneID: "%7"},
	})
	oldLister := statusPaneLister
	statusPaneLister = func() ([]tmuxpane.TmuxPane, error) {
		return []tmuxpane.TmuxPane{{PaneID: "%7", CWD: dir, Command: "codex", Title: "amq:issue-96:cto"}}, nil
	}
	oldSend := sendPromptToPane
	var events []string
	sendPromptToPane = func(paneID, prompt string) error {
		events = append(events, "send:"+paneID+"\x00"+prompt)
		return nil
	}
	t.Cleanup(func() {
		statusPaneLister = oldLister
		sendPromptToPane = oldSend
	})

	stdout, stderr, err := captureOutput(t, func() error {
		return runGoal([]string{"start", "--project", dir, "--session", "issue-96", "--role", "cto", "--goal", "ship safely", "--yes", "--json"})
	})
	if err != nil {
		t.Fatalf("goal start --yes: %v\nstderr:\n%s", err, stderr)
	}
	if len(events) != 1 || !strings.HasPrefix(events[0], "send:%7\x00/goal --goal") || !strings.Contains(events[0], "ship safely") {
		t.Fatalf("goal start delivery events = %+v", events)
	}
	env := decodeJSONEnvelope[goalStartData](t, stdout)
	if env.Kind != "goal_start" || env.Data.DryRun || env.Data.Status != "native_goal_delivered" {
		t.Fatalf("goal start delivery envelope = %+v", env)
	}
	if env.Data.DeliveryReceipt == nil || env.Data.DeliveryReceipt.PaneID != "%7" || env.Data.DeliveryReceipt.Status != "native_goal_delivered" {
		t.Fatalf("goal start delivery receipt = %+v", env.Data.DeliveryReceipt)
	}
	rec, err := launch.Read(agentDir)
	if err != nil {
		t.Fatal(err)
	}
	if rec.GoalBinding == nil || rec.GoalBinding.Source != "goal-control" || !rec.GoalBinding.NativeGoal {
		t.Fatalf("launch goal binding not updated: %+v", rec.GoalBinding)
	}
}

func TestGoalDeliverRegistersExternalOrchestrator(t *testing.T) {
	base := setupFakeAMQSessionRoots(t)
	dir := seedTeam(t, team.Team{
		Members:      []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-96"}},
		Orchestrated: true,
		Lead:         "cto",
	})
	seedAgentRecord(t, base, "issue-96", "cto", launch.Record{
		CWD:      dir,
		Binary:   "codex",
		Handle:   "cto",
		Role:     "cto",
		Session:  "issue-96",
		AgentPID: 4242,
		Tmux:     &launch.TmuxInfo{PaneID: "%7"},
	})
	prevPane := currentPaneIdentity
	currentPaneIdentity = func() (*tmuxpane.PaneIdentity, error) {
		return &tmuxpane.PaneIdentity{Session: "global", WindowID: "@1", WindowName: "orch", PaneID: "%99"}, nil
	}
	prevWake := leadWakeStarter
	var wakeOpts []leadWakeOptions
	leadWakeStarter = func(opts leadWakeOptions) (leadWakeResult, error) {
		wakeOpts = append(wakeOpts, opts)
		return leadWakeResult{PID: 9876, Started: true, Detail: "ready"}, nil
	}
	oldLister := statusPaneLister
	statusPaneLister = func() ([]tmuxpane.TmuxPane, error) {
		return []tmuxpane.TmuxPane{{PaneID: "%7", CWD: dir, Command: "codex", Title: "amq:issue-96:cto"}}, nil
	}
	oldSend := sendPromptToPane
	var sent []string
	sendPromptToPane = func(paneID, prompt string) error {
		sent = append(sent, paneID+"\x00"+prompt)
		return nil
	}
	t.Cleanup(func() {
		currentPaneIdentity = prevPane
		leadWakeStarter = prevWake
		statusPaneLister = oldLister
		sendPromptToPane = oldSend
	})

	stdout, stderr, err := captureOutput(t, func() error {
		return runGoal([]string{"deliver", "--project", dir, "--session", "issue-96", "--role", "cto", "--goal", "ship safely", "--register-orchestrator=global-orch", "--json"})
	})
	if err != nil {
		t.Fatalf("goal deliver --register-orchestrator: %v\nstderr:\n%s", err, stderr)
	}
	env := decodeJSONEnvelope[mutationResult](t, stdout)
	if env.Kind != "goal_deliver" || env.Data.Status != "native_goal_delivered" {
		t.Fatalf("goal deliver envelope = %+v", env)
	}
	if len(sent) != 1 || !strings.HasPrefix(sent[0], "%7\x00/goal --goal") {
		t.Fatalf("delivery should still target explicit cto pane, sent = %+v", sent)
	}
	cfg, err := team.Read(dir)
	if err != nil {
		t.Fatalf("read team: %v", err)
	}
	if cfg.Lead != goalOrchestratorRole || !cfg.Orchestrated {
		t.Fatalf("team lead/orchestrated = %q/%v, want orchestrator/true", cfg.Lead, cfg.Orchestrated)
	}
	orch, ok := teamMemberByRole(cfg, goalOrchestratorRole)
	if !ok || orch.Handle != "global-orch" || orch.Binary != "codex" || orch.Session != "issue-96" {
		t.Fatalf("orchestrator member = %+v, ok=%v", orch, ok)
	}
	if len(wakeOpts) != 1 || wakeOpts[0].Handle != "global-orch" || !wakeOpts[0].Require {
		t.Fatalf("wake opts = %+v", wakeOpts)
	}
	rec, err := launch.Read(filepath.Join(base, "issue-96", "agents", "global-orch"))
	if err != nil {
		t.Fatalf("read orchestrator launch record: %v", err)
	}
	if !rec.External || rec.Role != goalOrchestratorRole || rec.Handle != "global-orch" || rec.WakePID != 9876 || rec.Tmux == nil || rec.Tmux.PaneID != "%99" {
		t.Fatalf("orchestrator launch record = %+v", rec)
	}
}

type orchestratorRegStubs struct {
	sent     *[]string
	wakeOpts *[]leadWakeOptions
}

// setupOrchestratorRegStubs wires the pane identity, wake starter, pane lister,
// and prompt sender that goal start/deliver --register-orchestrator depends on,
// so #287 tests exercise the single-command path without touching real tmux.
func setupOrchestratorRegStubs(t *testing.T, dir string) orchestratorRegStubs {
	t.Helper()
	prevPane := currentPaneIdentity
	currentPaneIdentity = func() (*tmuxpane.PaneIdentity, error) {
		return &tmuxpane.PaneIdentity{Session: "global", WindowID: "@1", WindowName: "orch", PaneID: "%99"}, nil
	}
	prevWake := leadWakeStarter
	wakeOpts := &[]leadWakeOptions{}
	leadWakeStarter = func(opts leadWakeOptions) (leadWakeResult, error) {
		*wakeOpts = append(*wakeOpts, opts)
		return leadWakeResult{PID: 9876, Started: true, Detail: "ready"}, nil
	}
	oldLister := statusPaneLister
	statusPaneLister = func() ([]tmuxpane.TmuxPane, error) {
		return []tmuxpane.TmuxPane{{PaneID: "%7", CWD: dir, Command: "codex", Title: "amq:issue-96:cto"}}, nil
	}
	oldSend := sendPromptToPane
	sent := &[]string{}
	sendPromptToPane = func(paneID, prompt string) error {
		*sent = append(*sent, paneID+"\x00"+prompt)
		return nil
	}
	t.Cleanup(func() {
		currentPaneIdentity = prevPane
		leadWakeStarter = prevWake
		statusPaneLister = oldLister
		sendPromptToPane = oldSend
	})
	return orchestratorRegStubs{sent: sent, wakeOpts: wakeOpts}
}

func seedCtoLeadTeamForOrchestrator(t *testing.T) (string, string) {
	t.Helper()
	base := setupFakeAMQSessionRoots(t)
	dir := seedTeam(t, team.Team{
		Members:      []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-96"}},
		Orchestrated: true,
		Lead:         "cto",
	})
	seedAgentRecord(t, base, "issue-96", "cto", launch.Record{
		CWD:      dir,
		Binary:   "codex",
		Handle:   "cto",
		Role:     "cto",
		Session:  "issue-96",
		AgentPID: 4242,
		Tmux:     &launch.TmuxInfo{PaneID: "%7"},
	})
	return base, dir
}

// TestGoalStartRegisterOrchestratorProducesWakeableIdentity proves #287: a single
// gated command yields the full wakeable orchestrator identity (durable member +
// launch record bound to the live pane + lead set + wake sidecar requested).
func TestGoalStartRegisterOrchestratorProducesWakeableIdentity(t *testing.T) {
	base, dir := seedCtoLeadTeamForOrchestrator(t)
	stubs := setupOrchestratorRegStubs(t, dir)

	stdout, stderr, err := captureOutput(t, func() error {
		return runGoal([]string{"start", "--project", dir, "--session", "issue-96", "--role", "cto", "--goal", "ship safely", "--register-orchestrator=global-orch", "--yes", "--json"})
	})
	if err != nil {
		t.Fatalf("goal start --register-orchestrator --yes: %v\nstderr:\n%s", err, stderr)
	}
	env := decodeJSONEnvelope[goalStartData](t, stdout)
	if env.Kind != "goal_start" || env.Data.DryRun || env.Data.Status != "native_goal_delivered" {
		t.Fatalf("goal start envelope = %+v", env)
	}
	cfg, err := team.Read(dir)
	if err != nil {
		t.Fatalf("read team: %v", err)
	}
	if cfg.Lead != goalOrchestratorRole || !cfg.Orchestrated {
		t.Fatalf("team lead/orchestrated = %q/%v", cfg.Lead, cfg.Orchestrated)
	}
	orch, ok := teamMemberByRole(cfg, goalOrchestratorRole)
	if !ok || orch.Handle != "global-orch" {
		t.Fatalf("orchestrator member = %+v ok=%v", orch, ok)
	}
	rec, err := launch.Read(filepath.Join(base, "issue-96", "agents", "global-orch"))
	if err != nil {
		t.Fatalf("read orchestrator launch record: %v", err)
	}
	if !rec.External || rec.Role != goalOrchestratorRole || rec.WakePID != 9876 || rec.Tmux == nil || rec.Tmux.PaneID != "%99" {
		t.Fatalf("orchestrator launch record = %+v", rec)
	}
	if len(*stubs.wakeOpts) != 1 || (*stubs.wakeOpts)[0].Handle != "global-orch" || !(*stubs.wakeOpts)[0].Require {
		t.Fatalf("wake opts = %+v", *stubs.wakeOpts)
	}
}

// TestGoalStartRegisterOrchestratorIdempotentOnRerun proves #287 idempotency:
// re-running the same gated command does not error and does not duplicate the
// orchestrator member or launch record.
func TestGoalStartRegisterOrchestratorIdempotentOnRerun(t *testing.T) {
	base, dir := seedCtoLeadTeamForOrchestrator(t)
	setupOrchestratorRegStubs(t, dir)

	run := func() error {
		_, _, err := captureOutput(t, func() error {
			return runGoal([]string{"start", "--project", dir, "--session", "issue-96", "--role", "cto", "--goal", "ship safely", "--register-orchestrator=global-orch", "--yes", "--json"})
		})
		return err
	}
	if err := run(); err != nil {
		t.Fatalf("first run: %v", err)
	}
	if err := run(); err != nil {
		t.Fatalf("rerun must be idempotent, got: %v", err)
	}
	cfg, err := team.Read(dir)
	if err != nil {
		t.Fatalf("read team: %v", err)
	}
	count := 0
	for _, m := range cfg.Members {
		if strings.EqualFold(m.Role, goalOrchestratorRole) {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("rerun must not duplicate orchestrator member, got %d (members=%+v)", count, cfg.Members)
	}
	rec, err := launch.Read(filepath.Join(base, "issue-96", "agents", "global-orch"))
	if err != nil {
		t.Fatalf("read orchestrator launch record: %v", err)
	}
	if !rec.External || rec.Tmux == nil || rec.Tmux.PaneID != "%99" {
		t.Fatalf("orchestrator launch record after rerun = %+v", rec)
	}
}

// TestGoalStartRegisterOrchestratorWakeFailureIsHonest proves the wake sidecar is
// required: when the sidecar cannot start, the command surfaces the failure
// instead of reporting a wakeable identity it did not actually produce.
func TestGoalStartRegisterOrchestratorWakeFailureIsHonest(t *testing.T) {
	_, dir := seedCtoLeadTeamForOrchestrator(t)
	setupOrchestratorRegStubs(t, dir)
	prevWake := leadWakeStarter
	leadWakeStarter = func(opts leadWakeOptions) (leadWakeResult, error) {
		return leadWakeResult{}, fmt.Errorf("wake sidecar lock busy")
	}
	t.Cleanup(func() { leadWakeStarter = prevWake })

	_, _, err := captureOutput(t, func() error {
		return runGoal([]string{"start", "--project", dir, "--session", "issue-96", "--role", "cto", "--goal", "ship safely", "--register-orchestrator=global-orch", "--yes", "--json"})
	})
	if err == nil || !strings.Contains(err.Error(), "wake") {
		t.Fatalf("wake sidecar failure must surface honestly, got: %v", err)
	}
}

func seedCtoDeliverTeamForOrchestrator(t *testing.T) (string, string) {
	t.Helper()
	base := setupFakeAMQSessionRoots(t)
	dir := seedTeam(t, team.Team{
		Members:      []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-96"}},
		Orchestrated: true,
		Lead:         "cto",
	})
	seedAgentRecord(t, base, "issue-96", "cto", launch.Record{
		CWD: dir, Binary: "codex", Handle: "cto", Role: "cto", Session: "issue-96",
		AgentPID: 4242, Tmux: &launch.TmuxInfo{PaneID: "%7"},
	})
	prevPane := currentPaneIdentity
	currentPaneIdentity = func() (*tmuxpane.PaneIdentity, error) {
		return &tmuxpane.PaneIdentity{Session: "global", WindowID: "@1", WindowName: "orch", PaneID: "%99"}, nil
	}
	oldLister := statusPaneLister
	statusPaneLister = func() ([]tmuxpane.TmuxPane, error) {
		return []tmuxpane.TmuxPane{{PaneID: "%7", CWD: dir, Command: "codex", Title: "amq:issue-96:cto"}}, nil
	}
	oldSend := sendPromptToPane
	sendPromptToPane = func(string, string) error { return nil }
	t.Cleanup(func() {
		currentPaneIdentity = prevPane
		statusPaneLister = oldLister
		sendPromptToPane = oldSend
	})
	return base, dir
}

// TestGoalDeliverRegisterOrchestratorStartsDrainInjectingWakeSidecar proves the
// #283/#288 plumbing for the orchestrator caller: the wake sidecar is started
// with the standard drain inject-cmd and the launch record persists it.
func TestGoalDeliverRegisterOrchestratorStartsDrainInjectingWakeSidecar(t *testing.T) {
	base, dir := seedCtoDeliverTeamForOrchestrator(t)
	var wakeOpts []leadWakeOptions
	prevWake := leadWakeStarter
	leadWakeStarter = func(opts leadWakeOptions) (leadWakeResult, error) {
		wakeOpts = append(wakeOpts, opts)
		return leadWakeResult{PID: 9876, Started: true, Detail: "ready"}, nil
	}
	t.Cleanup(func() { leadWakeStarter = prevWake })

	if _, stderr, err := captureOutput(t, func() error {
		return runGoal([]string{"deliver", "--project", dir, "--session", "issue-96", "--role", "cto", "--goal", "ship safely", "--register-orchestrator=global-orch", "--json"})
	}); err != nil {
		t.Fatalf("goal deliver --register-orchestrator: %v\n%s", err, stderr)
	}
	if len(wakeOpts) != 1 || wakeOpts[0].WakeInjectCmd != wakeDrainInject() {
		t.Fatalf("orchestrator wake sidecar must be started with the drain inject-cmd: %+v", wakeOpts)
	}
	rec, err := launch.Read(filepath.Join(base, "issue-96", "agents", "global-orch"))
	if err != nil {
		t.Fatalf("read orchestrator launch record: %v", err)
	}
	if rec.WakeInjectCmd != wakeDrainInject() {
		t.Fatalf("orchestrator launch record must persist WakeInjectCmd, got %q", rec.WakeInjectCmd)
	}
}

// TestGoalDeliverRegisterOrchestratorWakeFailureIsHonest proves the wake sidecar
// is required: when it cannot start, registration surfaces the failure instead
// of reporting a drain-injecting identity it did not create.
func TestGoalDeliverRegisterOrchestratorWakeFailureIsHonest(t *testing.T) {
	_, dir := seedCtoDeliverTeamForOrchestrator(t)
	prevWake := leadWakeStarter
	leadWakeStarter = func(opts leadWakeOptions) (leadWakeResult, error) {
		return leadWakeResult{}, fmt.Errorf("wake sidecar lock busy")
	}
	t.Cleanup(func() { leadWakeStarter = prevWake })

	_, _, err := captureOutput(t, func() error {
		return runGoal([]string{"deliver", "--project", dir, "--session", "issue-96", "--role", "cto", "--goal", "ship safely", "--register-orchestrator=global-orch", "--json"})
	})
	if err == nil || !strings.Contains(err.Error(), "wake") {
		t.Fatalf("wake sidecar failure must surface honestly, got: %v", err)
	}
}

func TestGoalApplyRefusesWithoutApprovedGate(t *testing.T) {
	base := setupFakeAMQSessionRoots(t)
	dir := seedTeam(t, team.Team{
		Members:       []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-96"}},
		Orchestrated:  true,
		Lead:          "cto",
		ExecutionMode: executionModeProjectLead,
	})
	tm := team.Team{ExecutionMode: executionModeProjectLead}
	seedAgentRecord(t, base, "issue-96", "cto", launch.Record{
		CWD:      dir,
		Binary:   "codex",
		Handle:   "cto",
		Role:     "cto",
		Session:  "issue-96",
		AgentPID: 4242,
		Tmux:     &launch.TmuxInfo{PaneID: "%7"},
		GoalBinding: &launch.GoalBinding{
			Mode:       "native_goal",
			NativeGoal: true,
			Source:     "goal-control",
			Command:    nativeGoalControlPrompt("ship safely", tm, team.DefaultProfile, "issue-96", "cto"),
		},
	})
	_, _, err := captureOutput(t, func() error {
		return runGoal([]string{"apply", "--project", dir, "--session", "issue-96", "--gate", "release", "--yes"})
	})
	if err == nil || !strings.Contains(err.Error(), "APPROVED") {
		t.Fatalf("goal apply without approved gate should fail, got %v", err)
	}
}

func TestGoalApplyRequiresYesAfterApprovedGate(t *testing.T) {
	base := setupFakeAMQSessionRoots(t)
	dir := seedTeam(t, team.Team{
		Members:       []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-96"}},
		Orchestrated:  true,
		Lead:          "cto",
		ExecutionMode: executionModeProjectLead,
	})
	tm := team.Team{ExecutionMode: executionModeProjectLead}
	seedAgentRecord(t, base, "issue-96", "cto", launch.Record{
		CWD:      dir,
		Binary:   "codex",
		Handle:   "cto",
		Role:     "cto",
		Session:  "issue-96",
		AgentPID: 4242,
		Tmux:     &launch.TmuxInfo{PaneID: "%7"},
		GoalBinding: &launch.GoalBinding{
			Mode:       "native_goal",
			NativeGoal: true,
			Source:     "goal-control",
			Command:    nativeGoalControlPrompt("ship safely", tm, team.DefaultProfile, "issue-96", "cto"),
		},
	})
	seedNotifyMessage(t, base, "issue-96", "cto", "new", notifyMsg{
		ID: "approval-1", From: team.DefaultOperatorHandle, To: "cto", Thread: "gate/release",
		Subject: "APPROVED: release", Kind: "answer", Created: notifyNow,
	})

	oldSend := sendPromptToPane
	sendPromptToPane = func(string, string) error {
		t.Fatal("goal apply without --yes must not deliver")
		return nil
	}
	t.Cleanup(func() { sendPromptToPane = oldSend })

	_, _, err := captureOutput(t, func() error {
		return runGoal([]string{"apply", "--project", dir, "--session", "issue-96", "--gate", "release"})
	})
	if err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("goal apply without --yes should fail after approval check, got %v", err)
	}
}

func TestGoalApplyYesJSONVerifiesGateAndDelivers(t *testing.T) {
	base := setupFakeAMQSessionRoots(t)
	dir := seedTeam(t, team.Team{
		Members:       []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-96"}},
		Orchestrated:  true,
		Lead:          "cto",
		ExecutionMode: executionModeProjectLead,
	})
	tm := team.Team{ExecutionMode: executionModeProjectLead}
	agentDir := seedAgentRecord(t, base, "issue-96", "cto", launch.Record{
		CWD:      dir,
		Binary:   "codex",
		Handle:   "cto",
		Role:     "cto",
		Session:  "issue-96",
		AgentPID: 4242,
		Tmux:     &launch.TmuxInfo{PaneID: "%7"},
		GoalBinding: &launch.GoalBinding{
			Mode:       "native_goal",
			NativeGoal: true,
			Source:     "goal-control",
			Command:    nativeGoalControlPrompt("ship safely", tm, team.DefaultProfile, "issue-96", "cto"),
		},
	})
	seedNotifyMessage(t, base, "issue-96", "cto", "new", notifyMsg{
		ID: "approval-1", From: team.DefaultOperatorHandle, To: "cto", Thread: "gate/release",
		Subject: "APPROVED: release", Kind: "answer", Created: notifyNow,
	})
	oldLister := statusPaneLister
	statusPaneLister = func() ([]tmuxpane.TmuxPane, error) {
		return []tmuxpane.TmuxPane{{PaneID: "%7", CWD: dir, Command: "codex", Title: "amq:issue-96:cto"}}, nil
	}
	oldSend := sendPromptToPane
	var sent []string
	sendPromptToPane = func(paneID, prompt string) error {
		sent = append(sent, paneID+"\x00"+prompt)
		return nil
	}
	t.Cleanup(func() {
		statusPaneLister = oldLister
		sendPromptToPane = oldSend
	})

	stdout, stderr, err := captureOutput(t, func() error {
		return runGoal([]string{"apply", "--project", dir, "--session", "issue-96", "--gate", "release", "--goal-id", "g1", "--yes", "--json"})
	})
	if err != nil {
		t.Fatalf("goal apply --yes: %v\nstderr:\n%s", err, stderr)
	}
	if len(sent) != 1 || !strings.Contains(sent[0], "/goal --goal") || !strings.Contains(sent[0], "ship safely") {
		t.Fatalf("goal apply sent = %+v", sent)
	}
	env := decodeJSONEnvelope[goalApplyData](t, stdout)
	if env.Kind != "goal_apply" || env.Data.Status != "native_goal_delivered" || env.Data.GoalID != "g1" {
		t.Fatalf("goal apply envelope = %+v", env)
	}
	if env.Data.ApprovalEvidence == nil || env.Data.ApprovalEvidence.MessageID != "approval-1" || env.Data.Gate != "gate/release" {
		t.Fatalf("approval evidence = %+v gate=%q", env.Data.ApprovalEvidence, env.Data.Gate)
	}
	if env.Data.DeliveryReceipt == nil || env.Data.DeliveryReceipt.PaneID != "%7" {
		t.Fatalf("delivery receipt = %+v", env.Data.DeliveryReceipt)
	}
	rec, err := launch.Read(agentDir)
	if err != nil {
		t.Fatal(err)
	}
	if rec.GoalBinding == nil || rec.GoalBinding.Source != "goal-control" || !rec.GoalBinding.NativeGoal {
		t.Fatalf("launch goal binding not preserved/updated: %+v", rec.GoalBinding)
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
	if env.Data.GoalBinding.Mode != "native_goal_pending" || env.Data.GoalBinding.Source != "orchestrator-prompt" {
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
		case "write brief", "add t1", "add t2", "add t3":
			if !strings.Contains(mutation.Command, "--profile release") {
				t.Fatalf("%s mutation dropped profile: %s", mutation.Title, mutation.Command)
			}
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
	if !strings.Contains(env.Data.OrchestratorPrompt, "--lead release-lead") {
		t.Fatalf("orchestrator prompt dropped custom lead: %s", env.Data.OrchestratorPrompt)
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
	if !strings.Contains(env.Data.OrchestratorPrompt, "--mode global_orchestrator") || !strings.Contains(env.Data.OrchestratorPrompt, "--target-contract 2.10.0") {
		t.Fatalf("orchestrator prompt dropped execution metadata: %s", env.Data.OrchestratorPrompt)
	}
	for _, want := range []string{
		"Global orchestrator board",
		"name/repo/profile/session/lead/pane",
		"closed-run demotion",
	} {
		if !strings.Contains(env.Data.BriefSkeleton+env.Data.SkillInvocation, want) {
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
	if !strings.Contains(env.Data.BriefSkeleton, "## Autonomous policy") || !strings.Contains(env.Data.OrchestratorPrompt, "--composition autonomous") {
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
	if got := nativeGoalBindingFromArgs([]string{env.Data.OrchestratorPrompt}); got == nil || !got.NativeGoal {
		t.Fatalf("generated visible lead prompt must be launch-record detectable as native /goal: %+v", got)
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
			"-- '/goal --goal",
		} {
			if !strings.Contains(mutation.Command, want) {
				t.Fatalf("visible launch command missing %q: %q", want, mutation.Command)
			}
		}
		if !strings.Contains(mutation.Command, "ship visible setup handoff") {
			t.Fatalf("visible launch command = %q", mutation.Command)
		}
		if !strings.Contains(mutation.Reason, "native /goal prompt") {
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
		`/goal --goal "ship visible setup handoff"`,
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
		{"detached", "launch detached visible lead", "-- '/goal --goal"},
		{"current", "launch visible lead in current pane", "-- '/goal --goal"},
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
