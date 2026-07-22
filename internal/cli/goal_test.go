package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/launch"
	squadnamespace "github.com/omriariav/amq-squad/v2/internal/namespace"
	"github.com/omriariav/amq-squad/v2/internal/runtimecontrol"
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
	if data.GoalBinding.Mode != "prompt_goal_pending" || data.GoalBinding.NativeGoal || data.GoalBinding.Verified {
		t.Fatalf("goal binding mismatch: %+v", data.GoalBinding)
	}
	if len(data.IssueSources) != 2 || data.IssueSources[0].Number != 215 || data.IssueSources[1].Number != 216 {
		t.Fatalf("issues not sorted/included: %+v", data.IssueSources)
	}
	for _, want := range []string{"#215 goal draft", "https://github.com/o/r/issues/215", "AMQ-SQUAD PROMPT GOAL v1"} {
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
		"AMQ-SQUAD PROMPT GOAL v1",
		"amq-squad dispatch --profile issue-225 --session issue-225",
		"Default visibility is sibling-tabs",
		"Seeded composition remains the default",
		"Visible lead binding: prompt_goal_pending",
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

func TestGoalDeliveryUnknownBinaryFailsBeforeMutation(t *testing.T) {
	project := t.TempDir()
	member := team.Member{Role: "cto", Handle: "cto", Binary: "unknown-agent", Session: "issue-460"}
	tm := team.Team{Project: project, Members: []team.Member{member}, Orchestrated: true, Lead: "cto", ExecutionMode: executionModeProjectLead}
	opts := goalDeliveryOptions{
		Project: project, Profile: team.DefaultProfile, Session: "issue-460", Role: "cto", Goal: "ship",
		Team: tm, Member: member, Namespace: squadnamespace.Resolve(project, team.DefaultProfile, "issue-460"), Mode: executionModeProjectLead,
	}
	if _, err := executeGoalDelivery(opts); err == nil || !strings.Contains(err.Error(), "does not support binary") {
		t.Fatalf("unknown binary delivery = %v, want fail-closed contract error", err)
	}
	if _, err := os.Stat(goalAttemptDir(project, team.DefaultProfile, "issue-460")); !os.IsNotExist(err) {
		t.Fatalf("unknown binary delivery mutated attempt state: %v", err)
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
	if len(events) != 1 || !strings.HasPrefix(events[0], "send:%7\x00AMQ-SQUAD PROMPT GOAL v1\n") || !strings.Contains(events[0], "ship safely") {
		t.Fatalf("goal start delivery events = %+v", events)
	}
	env := decodeJSONEnvelope[goalStartData](t, stdout)
	if env.Kind != "goal_start" || env.Data.DryRun || env.Data.Status != "prompt_goal_delivered" {
		t.Fatalf("goal start delivery envelope = %+v", env)
	}
	if env.Data.DeliveryReceipt == nil || env.Data.DeliveryReceipt.PaneID != "%7" || env.Data.DeliveryReceipt.Status != "prompt_goal_delivered" {
		t.Fatalf("goal start delivery receipt = %+v", env.Data.DeliveryReceipt)
	}
	rec, err := launch.Read(agentDir)
	if err != nil {
		t.Fatal(err)
	}
	if rec.GoalBinding == nil || rec.GoalBinding.Source != "goal-control" || rec.GoalBinding.NativeGoal || rec.GoalBinding.Mode != "prompt_goal" || rec.GoalBinding.Goal != "ship safely" {
		t.Fatalf("launch goal binding not updated: %+v", rec.GoalBinding)
	}
	if rec.GoalBinding.DeliveryState != goalBindingDeliveryDelivered || !launchRecordHasGoalBinding(rec) {
		t.Fatalf("successful delivery must persist verified delivery evidence: %+v", rec.GoalBinding)
	}
}

func setupGoalDeliveryFailureTest(t *testing.T, sendErr error) (string, string, *[]amqCommandRequest, *[]string) {
	return setupGoalDeliveryFailureForBinary(t, "codex", sendErr)
}

func setupGoalDeliveryFailureForBinary(t *testing.T, binary string, sendErr error) (string, string, *[]amqCommandRequest, *[]string) {
	t.Helper()
	base := setupFakeAMQSessionRoots(t)
	dir := seedTeam(t, team.Team{
		Members:       []team.Member{{Role: "cto", Binary: binary, Handle: "cto", Session: "issue-96"}},
		Orchestrated:  true,
		Lead:          "cto",
		ExecutionMode: executionModeProjectLead,
	})
	seedAgentRecord(t, base, "issue-96", "cto", launch.Record{
		CWD: dir, Binary: binary, Handle: "cto", Role: "cto", Session: "issue-96",
		AgentPID: 4242, Tmux: &launch.TmuxInfo{PaneID: "%7"},
	})
	calls := withAMQCommandSeams(t, amqEnv{Root: filepath.Join(base, "{session}"), BaseRoot: base}, "Sent goal-msg-427 to cto\n")
	oldLister := statusPaneLister
	statusPaneLister = func() ([]tmuxpane.TmuxPane, error) {
		return []tmuxpane.TmuxPane{{PaneID: "%7", CWD: dir, Command: binary, Title: "amq:issue-96:cto"}}, nil
	}
	oldSend := sendPromptToPane
	var prompts []string
	sendPromptToPane = func(_ string, prompt string) error {
		prompts = append(prompts, prompt)
		return sendErr
	}
	t.Cleanup(func() {
		statusPaneLister = oldLister
		sendPromptToPane = oldSend
	})
	return dir, base, calls, &prompts
}

func TestGoalDeliveryFailureLeavesUnverifiedReservationAcrossBinaries(t *testing.T) {
	for _, binary := range []string{"codex", "claude"} {
		t.Run(binary, func(t *testing.T) {
			dir, base, _, _ := setupGoalDeliveryFailureForBinary(t, binary, errors.New("pane rejected delivery"))
			_, _, err := captureOutput(t, func() error {
				return runGoal([]string{"start", "--project", dir, "--session", "issue-96", "--role", "cto", "--goal", "ship safely", "--yes"})
			})
			if err == nil {
				t.Fatal("expected hard goal delivery failure")
			}
			rec, readErr := launch.Read(filepath.Join(base, "issue-96", "agents", "cto"))
			if readErr != nil {
				t.Fatal(readErr)
			}
			if rec.GoalBinding == nil || rec.GoalBinding.DeliveryState != goalBindingDeliveryReserved || launchRecordHasGoalBinding(rec) {
				t.Fatalf("failed delivery surfaced verified binding: %+v", rec.GoalBinding)
			}
		})
	}
}

func TestNativeTerminalWithoutPaneStatusMatchesGoalDeliveryFailure(t *testing.T) {
	base := setupFakeAMQSessionRoots(t)
	project := seedTeam(t, team.Team{
		Members:       []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-96"}},
		Orchestrated:  true,
		Lead:          "cto",
		ExecutionMode: executionModeProjectLead,
	})
	seedAgentRecord(t, base, "issue-96", "cto", launch.Record{
		CWD: project, Binary: "codex", Handle: "cto", Role: "cto", Session: "issue-96", AgentPID: 4242,
		Terminal: &launch.TerminalInfo{Backend: runtimecontrol.BackendITerm2, WindowID: "101", Target: "new-window"},
	})
	swapStatusPaneLister(t, nil, nil)

	statusJSON, err := runStatusExec(t, statusExecution{
		ProjectDir:       project,
		RequestedSession: "issue-96",
		ExplicitSession:  true,
		Profile:          team.DefaultProfile,
		Probe:            statusProbe(map[int]bool{4242: true}, map[int]bool{4242: true}, time.Now()),
		JSON:             true,
	})
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	env := decodeJSONEnvelope[statusEnvelopeData](t, statusJSON)
	if len(env.Data.Records) != 1 {
		t.Fatalf("status records = %d", len(env.Data.Records))
	}
	for _, action := range env.Data.Records[0].Actions {
		if action.Kind == "goal_deliver" && action.Available {
			t.Fatalf("status advertised unavailable native goal path: %+v", action)
		}
	}

	_, _, err = captureOutput(t, func() error {
		return runGoal([]string{"deliver", "--project", project, "--session", "issue-96", "--role", "cto", "--goal", "ship safely", "--json"})
	})
	if err == nil || !strings.Contains(err.Error(), runtimecontrol.ITerm2InjectionDisabledReason) {
		t.Fatalf("goal delivery without native pane = %v", err)
	}
}

func TestGoalDeliveryQueuedInputRecordsPendingWithoutActionableAMQ(t *testing.T) {
	dir, _, calls, prompts := setupGoalDeliveryFailureTest(t, &tmuxpane.QueuedInputError{PaneID: "%7"})
	stdout, stderr, err := captureOutput(t, func() error {
		return runGoal([]string{"deliver", "--project", dir, "--session", "issue-96", "--role", "cto", "--goal", "ship safely", "--json"})
	})
	if err != nil {
		t.Fatalf("known queued goal must remain a soft outcome: %v\nstderr:\n%s", err, stderr)
	}
	env := decodeJSONEnvelope[mutationResult](t, stdout)
	receipt := env.Data.DeliveryReceipt
	if env.Data.Status != "prompt_goal_queued" || receipt == nil || receipt.Fallback || receipt.MessageID != "" || !receiptHasStage(receipt, "prompt_goal_queued") || !receiptHasStage(receipt, "pending_without_amq_action") {
		t.Fatalf("queued prompt goal receipt = %+v", receipt)
	}
	if len(*calls) != 0 {
		t.Fatalf("known queued input must not emit a second actionable AMQ todo; sends=%d", len(*calls))
	}
	if len(*prompts) != 1 || !strings.Contains((*prompts)[0], "--attempt-id "+receipt.AttemptID) {
		t.Fatalf("structured prompt does not carry shared attempt id %q: %q", receipt.AttemptID, *prompts)
	}
	if !strings.Contains(stderr, "without a second actionable AMQ goal") {
		t.Fatalf("queued warning missing idempotency contract:\n%s", stderr)
	}
}

func TestGoalDeliveryUnconfirmedQueuesClaimOnceAMQFallback(t *testing.T) {
	dir, base, calls, prompts := setupGoalDeliveryFailureTest(t, &tmuxpane.SubmitUnconfirmedError{PaneID: "%7", Attempts: 3})
	stdout, stderr, err := captureOutput(t, func() error {
		return runGoal([]string{"deliver", "--project", dir, "--session", "issue-96", "--role", "cto", "--goal", "ship safely", "--json"})
	})
	if err != nil {
		t.Fatalf("unconfirmed goal submit must use claim-once fallback: %v\nstderr:\n%s", err, stderr)
	}
	env := decodeJSONEnvelope[mutationResult](t, stdout)
	receipt := env.Data.DeliveryReceipt
	if env.Data.Status != "durable_goal_fallback" || env.Data.MessageID != "goal-msg-427" || receipt == nil || !receipt.Fallback || receipt.Method != "durable_amq_goal_fallback" || !receiptHasStage(receipt, "prompt_goal_unconfirmed") || !receiptHasStage(receipt, "claim_once_contract") || !receiptHasStage(receipt, "written_to_amq") {
		t.Fatalf("claim-once fallback receipt = %+v", receipt)
	}
	if len(*prompts) != 1 || !strings.Contains((*prompts)[0], "--attempt-id "+receipt.AttemptID) {
		t.Fatalf("structured prompt does not carry attempt id %q: %q", receipt.AttemptID, *prompts)
	}
	if !strings.Contains(stderr, "Claim-once durable AMQ fallback goal-msg-427 shares attempt "+receipt.AttemptID) {
		t.Fatalf("claim-once warning missing detail:\n%s", stderr)
	}
	if len(*calls) != 1 {
		t.Fatalf("AMQ sends = %d, want exactly 1", len(*calls))
	}
	joined := strings.Join((*calls)[0].Arg, " ")
	for _, want := range []string{
		"send --root " + filepath.Join(base, "issue-96"),
		"--me cto --to cto", "--thread goal/issue-96", "--kind todo",
		"--subject Claim-once launch goal: issue-96", "Goal attempt ID: " + receipt.AttemptID,
		"goal claim --project " + dir, "--attempt-id " + receipt.AttemptID, "--route amq --json",
		"Proceed only when status is claimed", "already_claimed", "ship safely",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("durable goal send args missing %q:\n%q", want, (*calls)[0].Arg)
		}
	}
}

func TestGoalDeliveryReleasesIdentityLocksBeforeBlockedSend(t *testing.T) {
	for _, kind := range []string{"launch", "team"} {
		t.Run(kind, func(t *testing.T) {
			dir, base, _, prompts := setupGoalDeliveryFailureTest(t, nil)
			agentDir := filepath.Join(base, "issue-96", "agents", "cto")
			oldSend := sendPromptToPane
			writerDone := make(chan error, 1)
			sendPromptToPane = func(paneID, prompt string) error {
				go func() {
					switch kind {
					case "launch":
						writerDone <- launch.WithRecordLock(agentDir, func() error {
							rec, err := launch.Read(agentDir)
							if err != nil {
								return err
							}
							rec.Model = "concurrent-send-launch-update"
							return launch.WriteUnderRecordLock(agentDir, rec)
						})
					case "team":
						writerDone <- team.WithProfileLock(dir, team.DefaultProfile, func() error {
							current, err := team.ReadProfile(dir, team.DefaultProfile)
							if err != nil {
								return err
							}
							current.Members[0].Model = "concurrent-send-team-update"
							return team.WriteProfileUnderLock(dir, team.DefaultProfile, current)
						})
					}
				}()
				select {
				case err := <-writerDone:
					if err != nil {
						t.Fatalf("%s writer while send blocked: %v", kind, err)
					}
				case <-time.After(2 * time.Second):
					t.Fatalf("%s writer was blocked by external send", kind)
				}
				return oldSend(paneID, prompt)
			}
			t.Cleanup(func() { sendPromptToPane = oldSend })

			_, _, err := captureOutput(t, func() error {
				return runGoal([]string{"deliver", "--project", dir, "--session", "issue-96", "--role", "cto", "--goal", "ship safely"})
			})
			if len(*prompts) != 1 {
				t.Fatalf("ordinary delivery prompts=%v", *prompts)
			}
			if kind == "team" {
				if err == nil || !strings.Contains(err.Error(), "team generation changed after pane delivery") {
					t.Fatalf("team update must cause explicit stale-generation refusal, got %v", err)
				}
				current, readErr := team.ReadProfile(dir, team.DefaultProfile)
				if readErr != nil || current.Members[0].Model != "concurrent-send-team-update" {
					t.Fatalf("team writer did not survive blocked send: model=%q err=%v", current.Members[0].Model, readErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("launch update should merge after blocked send: %v", err)
			}
			rec, readErr := launch.Read(agentDir)
			if readErr != nil || rec.Model != "concurrent-send-launch-update" || rec.GoalBinding == nil || rec.GoalBinding.Detail != "structured Codex goal prompt delivered as a first-class claim-once control action" {
				t.Fatalf("post-send merge lost independent launch update: model=%q binding=%+v err=%v", rec.Model, rec.GoalBinding, readErr)
			}
		})
	}
}

func TestGoalClaimIsAtomicAndSecondRouteIsNoOp(t *testing.T) {
	dir := t.TempDir()
	opts := goalDeliveryOptions{
		Project: dir, Profile: "release", Session: "issue-96", Role: "cto", Goal: "ship safely",
		Member:    team.Member{Role: "cto", Handle: "cto"},
		Namespace: squadnamespace.Resolve(dir, "release", "issue-96"),
	}
	const attemptID = "20260712T120000.000000000Z-native_goal-cto-cto"
	if _, err := createGoalAttempt(opts, attemptID, time.Unix(100, 0).UTC()); err != nil {
		t.Fatalf("create goal attempt: %v", err)
	}
	stdout, stderr, err := captureOutput(t, func() error {
		return runGoal([]string{"claim", "--project", dir, "--profile", "release", "--session", "issue-96", "--attempt-id", attemptID, "--route", "native", "--json"})
	})
	if err != nil {
		t.Fatalf("native claim: %v\nstderr:\n%s", err, stderr)
	}
	first := decodeJSONEnvelope[goalAttemptClaimData](t, stdout)
	if first.Data.Status != "claimed" || first.Data.Route != "native" || first.Data.AttemptID != attemptID || first.Data.Namespace.ID != "release/issue-96" || first.Data.ClaimPath == "" || first.Data.ClaimedAt.IsZero() {
		t.Fatalf("first claim = %+v", first.Data)
	}
	stdout, stderr, err = captureOutput(t, func() error {
		return runGoal([]string{"claim", "--project", dir, "--profile", "release", "--session", "issue-96", "--attempt-id", attemptID, "--route", "amq", "--json"})
	})
	if err != nil {
		t.Fatalf("second claim should be a successful no-op: %v\nstderr:\n%s", err, stderr)
	}
	second := decodeJSONEnvelope[goalAttemptClaimData](t, stdout)
	if second.Data.Status != "already_claimed" || second.Data.ExistingRoute != "native" || second.Data.Route != "amq" || second.Data.ClaimPath == "" || second.Data.RecoveryCmd == "" || second.Data.ClaimedAt.IsZero() {
		t.Fatalf("second claim = %+v", second.Data)
	}
}

func TestGoalClaimConcurrentPublishHasOneWinnerAndValidLoser(t *testing.T) {
	dir := t.TempDir()
	opts := goalDeliveryOptions{
		Project: dir, Profile: "release", Session: "issue-96", Role: "cto", Goal: "ship safely",
		Member: team.Member{Role: "cto", Handle: "cto"}, Namespace: squadnamespace.Resolve(dir, "release", "issue-96"),
	}
	const attemptID = "race-attempt"
	if _, err := createGoalAttempt(opts, attemptID, time.Unix(100, 0).UTC()); err != nil {
		t.Fatal(err)
	}
	arrived := make(chan struct{}, 2)
	release := make(chan struct{})
	prevLink := goalAttemptLink
	goalAttemptLink = func(candidate, canonical string) error {
		arrived <- struct{}{}
		<-release
		return os.Link(candidate, canonical)
	}
	t.Cleanup(func() { goalAttemptLink = prevLink })
	type result struct {
		claimed  bool
		existing goalAttemptClaim
		err      error
	}
	results := make(chan result, 2)
	for _, route := range []string{"native", "amq"} {
		go func(route string) {
			claimed, existing, err := claimGoalAttempt(dir, "release", "issue-96", attemptID, route, time.Now().UTC())
			results <- result{claimed: claimed, existing: existing, err: err}
		}(route)
	}
	<-arrived
	<-arrived
	close(release)
	winners := 0
	losers := 0
	for range 2 {
		got := <-results
		if got.err != nil {
			t.Fatalf("concurrent claim failed: %v", got.err)
		}
		if got.claimed {
			winners++
		} else {
			losers++
			if got.existing.AttemptID != attemptID || (got.existing.Route != "native" && got.existing.Route != "amq") || got.existing.ClaimedAt.IsZero() {
				t.Fatalf("loser observed invalid canonical claim: %+v", got.existing)
			}
		}
	}
	if winners != 1 || losers != 1 {
		t.Fatalf("race results winners=%d losers=%d", winners, losers)
	}
	assertNoGoalPublishCandidates(t, goalAttemptDir(dir, "release", "issue-96"))
}

func TestGoalAttemptConcurrentPublishCanonicalIsComplete(t *testing.T) {
	dir := t.TempDir()
	opts := goalDeliveryOptions{Project: dir, Profile: "release", Session: "issue-96", Role: "cto", Goal: "ship safely", Member: team.Member{Role: "cto", Handle: "cto"}, Namespace: squadnamespace.Resolve(dir, "release", "issue-96")}
	const attemptID = "attempt-record-race"
	arrived := make(chan struct{}, 2)
	release := make(chan struct{})
	prevLink := goalAttemptLink
	goalAttemptLink = func(candidate, canonical string) error {
		arrived <- struct{}{}
		<-release
		return os.Link(candidate, canonical)
	}
	t.Cleanup(func() { goalAttemptLink = prevLink })
	results := make(chan error, 2)
	for range 2 {
		go func() {
			_, err := createGoalAttempt(opts, attemptID, time.Unix(100, 0).UTC())
			results <- err
		}()
	}
	<-arrived
	<-arrived
	close(release)
	successes := 0
	failures := 0
	for range 2 {
		if err := <-results; err == nil {
			successes++
		} else if strings.Contains(err.Error(), "canonical record already exists") {
			failures++
		} else {
			t.Fatalf("unexpected concurrent attempt error: %v", err)
		}
	}
	if successes != 1 || failures != 1 {
		t.Fatalf("attempt race successes=%d expected-existing=%d", successes, failures)
	}
	path, _ := goalAttemptPath(dir, "release", "issue-96", attemptID)
	record, err := readGoalAttempt(path, attemptID)
	if err != nil || record.Goal != "ship safely" || record.Role != "cto" {
		t.Fatalf("canonical attempt is not complete: %+v, %v", record, err)
	}
	assertNoGoalPublishCandidates(t, filepath.Dir(path))
}

func TestGoalClaimMalformedCanonicalFailsClosed(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
	}{
		{name: "truncated", body: "{\n"},
		{name: "mismatched attempt", body: `{"attempt_id":"other","route":"native","claimed_at":"2026-07-12T08:00:00Z"}`},
		{name: "invalid route", body: `{"attempt_id":"bad-claim","route":"unknown","claimed_at":"2026-07-12T08:00:00Z"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			opts := goalDeliveryOptions{Project: dir, Profile: "release", Session: "issue-96", Role: "cto", Goal: "ship safely", Member: team.Member{Role: "cto", Handle: "cto"}, Namespace: squadnamespace.Resolve(dir, "release", "issue-96")}
			attemptID := "bad-claim"
			attemptPath, err := createGoalAttempt(opts, attemptID, time.Now().UTC())
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(goalAttemptClaimPath(attemptPath), []byte(tc.body), 0o644); err != nil {
				t.Fatal(err)
			}
			_, _, err = claimGoalAttempt(dir, "release", "issue-96", attemptID, "amq", time.Now().UTC())
			var invalid *invalidExistingGoalClaimError
			if !errors.As(err, &invalid) || invalid.Status != "invalid_existing_claim" || !strings.Contains(err.Error(), "activation refused") {
				t.Fatalf("want explicit fail-closed claim status, got %T: %v", err, err)
			}
		})
	}
}

func TestGoalPublishCandidateCleanupOnFailure(t *testing.T) {
	t.Run("attempt", func(t *testing.T) {
		dir := t.TempDir()
		opts := goalDeliveryOptions{Project: dir, Profile: "release", Session: "issue-96", Role: "cto", Goal: "ship safely", Member: team.Member{Role: "cto", Handle: "cto"}, Namespace: squadnamespace.Resolve(dir, "release", "issue-96")}
		prevLink := goalAttemptLink
		goalAttemptLink = func(string, string) error { return errors.New("link denied") }
		t.Cleanup(func() { goalAttemptLink = prevLink })
		if _, err := createGoalAttempt(opts, "orphan-test", time.Now().UTC()); err == nil || !strings.Contains(err.Error(), "link denied") {
			t.Fatalf("publish error = %v", err)
		}
		attemptDir := goalAttemptDir(dir, "release", "issue-96")
		assertNoGoalPublishCandidates(t, attemptDir)
		if _, err := os.Stat(filepath.Join(attemptDir, "orphan-test.json")); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("failed publish left canonical attempt: %v", err)
		}
	})
	t.Run("claim", func(t *testing.T) {
		dir := t.TempDir()
		opts := goalDeliveryOptions{Project: dir, Profile: "release", Session: "issue-96", Role: "cto", Goal: "ship safely", Member: team.Member{Role: "cto", Handle: "cto"}, Namespace: squadnamespace.Resolve(dir, "release", "issue-96")}
		attemptPath, err := createGoalAttempt(opts, "orphan-claim", time.Now().UTC())
		if err != nil {
			t.Fatal(err)
		}
		prevLink := goalAttemptLink
		goalAttemptLink = func(string, string) error { return errors.New("link denied") }
		t.Cleanup(func() { goalAttemptLink = prevLink })
		if _, _, err := claimGoalAttempt(dir, "release", "issue-96", "orphan-claim", "native", time.Now().UTC()); err == nil || !strings.Contains(err.Error(), "link denied") {
			t.Fatalf("claim publish error = %v", err)
		}
		assertNoGoalPublishCandidates(t, filepath.Dir(attemptPath))
		if _, err := os.Stat(goalAttemptClaimPath(attemptPath)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("failed publish left canonical claim: %v", err)
		}
	})
}

func TestGoalClaimCrashWindowIsObservableAtMostOnce(t *testing.T) {
	dir := t.TempDir()
	opts := goalDeliveryOptions{Project: dir, Profile: "release", Session: "issue-96", Role: "cto", Goal: "ship safely", Member: team.Member{Role: "cto", Handle: "cto"}, Namespace: squadnamespace.Resolve(dir, "release", "issue-96")}
	const attemptID = "crash-window"
	attemptPath, err := createGoalAttempt(opts, attemptID, time.Unix(100, 0).UTC())
	if err != nil {
		t.Fatal(err)
	}
	claimed, _, err := claimGoalAttempt(dir, "release", "issue-96", attemptID, "native", time.Unix(101, 0).UTC())
	if err != nil || !claimed {
		t.Fatalf("first claim = %t, %v", claimed, err)
	}
	// Simulate a claimant crash: no activation marker/action follows the claim.
	stdout, _, err := captureOutput(t, func() error {
		return runGoal([]string{"claim", "--project", dir, "--profile", "release", "--session", "issue-96", "--attempt-id", attemptID, "--route", "amq"})
	})
	if err != nil {
		t.Fatalf("second route must no-op after consumed crash window: %v", err)
	}
	for _, want := range []string{"already claimed via native", "no-op", goalAttemptClaimPath(attemptPath), "re-deliver as a new attempt", "amq-squad goal deliver", "--goal 'ship safely'"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("crash-window output missing %q:\n%s", want, stdout)
		}
	}
	b, err := os.ReadFile(goalAttemptClaimPath(attemptPath))
	if err != nil {
		t.Fatal(err)
	}
	var evidence goalAttemptClaim
	if err := json.Unmarshal(b, &evidence); err != nil || evidence.Route != "native" {
		t.Fatalf("second route changed winner evidence: %+v, %v", evidence, err)
	}
}

func assertNoGoalPublishCandidates(t *testing.T, dir string) {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(dir, ".*.candidate-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 0 {
		t.Fatalf("publish candidate orphan(s): %v", matches)
	}
}

func TestGoalDeliveryFallbackSendFailurePreservesTypedSoftError(t *testing.T) {
	dir, _, _, _ := setupGoalDeliveryFailureTest(t, &tmuxpane.SubmitUnconfirmedError{PaneID: "%7", Attempts: 3})
	prevRun := runAMQCommand
	runAMQCommand = func(amqCommandRequest) ([]byte, error) { return nil, errors.New("mailbox unavailable") }
	t.Cleanup(func() { runAMQCommand = prevRun })
	_, _, err := captureOutput(t, func() error {
		return runGoal([]string{"deliver", "--project", dir, "--session", "issue-96", "--role", "cto", "--goal", "ship safely", "--json"})
	})
	if err == nil || !strings.Contains(err.Error(), "durable goal fallback failed") || !strings.Contains(err.Error(), "mailbox unavailable") {
		t.Fatalf("fallback send error = %v", err)
	}
	var unconfirmed *tmuxpane.SubmitUnconfirmedError
	if !errors.As(err, &unconfirmed) {
		t.Fatalf("fallback failure lost original *SubmitUnconfirmedError: %T %v", err, err)
	}
}

func TestGoalDeliveryReceiptFailureAfterSendIsTypedUnsafeToRetry(t *testing.T) {
	dir, _, _, _ := setupGoalDeliveryFailureTest(t, &tmuxpane.SubmitUnconfirmedError{PaneID: "%7", Attempts: 3})
	prevWrite := goalDeliveryReceiptWrite
	goalDeliveryReceiptWrite = func(string, string, string, *deliveryReceiptData) error {
		return errors.New("receipt disk full")
	}
	t.Cleanup(func() { goalDeliveryReceiptWrite = prevWrite })
	_, _, err := captureOutput(t, func() error {
		return runGoal([]string{"deliver", "--project", dir, "--session", "issue-96", "--role", "cto", "--goal", "ship safely", "--json"})
	})
	var sent *goalFallbackSentReceiptError
	if !errors.As(err, &sent) {
		t.Fatalf("want *goalFallbackSentReceiptError, got %T: %v", err, err)
	}
	if sent.MessageID != "goal-msg-427" || sent.Thread != "goal/issue-96" || sent.Root == "" || sent.RetrySafe() || !strings.Contains(err.Error(), "unsafe to blindly retry") {
		t.Fatalf("sent-but-receipt-failed outcome = %+v: %v", sent, err)
	}
	var unconfirmed *tmuxpane.SubmitUnconfirmedError
	if !errors.As(err, &unconfirmed) {
		t.Fatalf("sent-but-receipt failure lost original soft error: %T %v", err, err)
	}
}

func TestGoalFallbackNamedProfileUsesNamespacedAMQRoot(t *testing.T) {
	base := setupFakeAMQSessionRoots(t)
	dir := t.TempDir()
	profile := "release"
	tm := team.Team{
		Project: dir, Orchestrated: true, Lead: "cto", ExecutionMode: executionModeProjectLead,
		Members: []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-96", CWD: dir}},
	}
	if err := team.WriteProfile(dir, profile, tm); err != nil {
		t.Fatal(err)
	}
	calls := withAMQCommandSeams(t, amqEnv{Root: filepath.Join(base, "{session}"), BaseRoot: base}, "Sent named-msg to cto\n")
	opts := goalDeliveryOptions{
		Project: dir, Profile: profile, Session: "issue-96", Role: "cto", Goal: "ship safely",
		AttemptID: "attempt-named", Team: tm, Member: tm.Members[0], Namespace: squadnamespace.Resolve(dir, profile, "issue-96"), Mode: executionModeProjectLead,
	}
	got, err := sendDurableGoalFallback(opts)
	if err != nil {
		t.Fatalf("named-profile fallback: %v", err)
	}
	wantRoot := filepath.Join(dir, ".agent-mail", profile, "issue-96")
	if got.Root != wantRoot {
		t.Fatalf("fallback root = %q, want named-profile root %q", got.Root, wantRoot)
	}
	if len(*calls) != 1 || !strings.Contains(strings.Join((*calls)[0].Arg, " "), "--root "+wantRoot) {
		t.Fatalf("named-profile AMQ send args = %+v", *calls)
	}
}

func TestGoalDeliverRegistersExternalOrchestratorWithoutMutatingConfiguredLead(t *testing.T) {
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
	root := filepath.Join(base, "issue-96")
	if err := createExternalOrchestratorMailboxFixture(root, "legacy"); err != nil {
		t.Fatal(err)
	}
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
		scope, scopeErr := newExternalOrchestratorScope(dir, team.DefaultProfile, "issue-96", "global-orch")
		if scopeErr != nil {
			t.Fatal(scopeErr)
		}
		registry, registryErr := readExternalOrchestratorRegistry(scope)
		if registryErr != nil || registry.Registrations[len(registry.Registrations)-1].State != externalOrchestratorStateRegistered {
			t.Fatalf("goal invoked before external registry committed registered: registry=%+v err=%v", registry, registryErr)
		}
		sent = append(sent, paneID+"\x00"+prompt)
		return nil
	}
	oldRunAMQ := runAMQCommand
	var initReq amqCommandRequest
	runAMQCommand = func(req amqCommandRequest) ([]byte, error) {
		if len(req.Arg) > 0 && req.Arg[0] == "init" {
			initReq = req
			if err := createExternalOrchestratorMailboxFixture(amqFlagValue(req.Arg, "root"), amqFlagValue(req.Arg, "agents")); err != nil {
				return nil, err
			}
			return []byte("Initialized AMQ root\n"), nil
		}
		return oldRunAMQ(req)
	}
	t.Cleanup(func() {
		currentPaneIdentity = prevPane
		leadWakeStarter = prevWake
		statusPaneLister = oldLister
		sendPromptToPane = oldSend
		runAMQCommand = oldRunAMQ
	})
	teamBefore, err := os.ReadFile(team.Path(dir))
	if err != nil {
		t.Fatalf("read team bytes before registration: %v", err)
	}

	stdout, stderr, err := captureOutput(t, func() error {
		return runGoal([]string{"deliver", "--project", dir, "--session", "issue-96", "--goal", "ship safely", "--register-orchestrator=global-orch", "--json"})
	})
	if err != nil {
		t.Fatalf("goal deliver --register-orchestrator: %v\nstderr:\n%s", err, stderr)
	}
	env := decodeJSONEnvelope[mutationResult](t, stdout)
	if env.Kind != "goal_deliver" || env.Data.Status != "prompt_goal_delivered" {
		t.Fatalf("goal deliver envelope = %+v", env)
	}
	if len(sent) != 1 || !strings.HasPrefix(sent[0], "%7\x00AMQ-SQUAD PROMPT GOAL v1\n") {
		t.Fatalf("delivery should still target explicit cto pane, sent = %+v", sent)
	}
	if got := amqFlagValue(initReq.Arg, "agents"); got != "cto,global-orch,legacy" || !containsString(initReq.Arg, "--force") {
		t.Fatalf("AMQ init did not preserve union: args=%v", initReq.Arg)
	}
	envJoined := strings.Join(initReq.Env, "\n")
	canonicalRoot, _ := canonicalPathForReceipt(root)
	if !strings.Contains(envJoined, "AM_ROOT="+canonicalRoot) || !strings.Contains(envJoined, "AM_BASE_ROOT="+canonicalRoot) || strings.Contains(envJoined, "AM_SESSION=") || !strings.Contains(envJoined, "AM_ME=global-orch") {
		t.Fatalf("AMQ init context is not exact identity-clean root: %v", initReq.Env)
	}
	teamAfter, err := os.ReadFile(team.Path(dir))
	if err != nil {
		t.Fatalf("read team bytes after registration: %v", err)
	}
	if string(teamAfter) != string(teamBefore) {
		t.Fatalf("register-orchestrator mutated team bytes\nbefore:\n%s\nafter:\n%s", teamBefore, teamAfter)
	}
	cfg, err := team.Read(dir)
	if err != nil {
		t.Fatalf("read team: %v", err)
	}
	if cfg.Lead != "cto" || !cfg.Orchestrated || len(cfg.Members) != 1 || cfg.Members[0].Role != "cto" {
		t.Fatalf("configured lead/members changed: lead=%q members=%+v", cfg.Lead, cfg.Members)
	}
	if _, ok := teamMemberByRole(cfg, goalOrchestratorRole); ok {
		t.Fatalf("external orchestrator leaked into configured members: %+v", cfg.Members)
	}
	if len(wakeOpts) != 1 || wakeOpts[0].Handle != "global-orch" || !wakeOpts[0].Require {
		t.Fatalf("wake opts = %+v", wakeOpts)
	}
	rec, err := launch.Read(filepath.Join(base, "issue-96", "agents", "global-orch"))
	if err != nil {
		t.Fatalf("read orchestrator launch record: %v", err)
	}
	if !rec.External || rec.Role != goalOrchestratorRole || rec.Handle != "global-orch" || rec.WakePID != 9876 || rec.WakeRecordID == "" || rec.WakeRecordDigest == "" || rec.Tmux == nil || rec.Tmux.PaneID != "%99" {
		t.Fatalf("orchestrator launch record = %+v", rec)
	}
	if rec.Terminal == nil || rec.Terminal.Backend != "tmux" || rec.Terminal.PaneID != "%99" || rec.Terminal.Target != "external" {
		t.Fatalf("orchestrator launch terminal identity = %+v", rec.Terminal)
	}
	scope, err := newExternalOrchestratorScope(dir, team.DefaultProfile, "issue-96", "global-orch")
	if err != nil {
		t.Fatal(err)
	}
	registry, err := readExternalOrchestratorRegistry(scope)
	if err != nil {
		t.Fatal(err)
	}
	var states []externalOrchestratorRegistrationState
	for _, transition := range registry.Registrations[len(registry.Registrations)-1].Transitions {
		states = append(states, transition.To)
	}
	wantStates := []externalOrchestratorRegistrationState{externalOrchestratorStatePlanned, externalOrchestratorStateMailboxInvoked, externalOrchestratorStateMailboxVerified, externalOrchestratorStateRuntimeVerified, externalOrchestratorStateRegistered}
	if fmt.Sprint(states) != fmt.Sprint(wantStates) {
		t.Fatalf("registry state ordering = %v, want %v", states, wantStates)
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
	oldRunAMQ := runAMQCommand
	runAMQCommand = func(req amqCommandRequest) ([]byte, error) {
		if len(req.Arg) > 0 && req.Arg[0] == "init" {
			if err := createExternalOrchestratorMailboxFixture(amqFlagValue(req.Arg, "root"), amqFlagValue(req.Arg, "agents")); err != nil {
				return nil, err
			}
			return []byte("Initialized AMQ root\n"), nil
		}
		return oldRunAMQ(req)
	}
	t.Cleanup(func() {
		currentPaneIdentity = prevPane
		leadWakeStarter = prevWake
		statusPaneLister = oldLister
		sendPromptToPane = oldSend
		runAMQCommand = oldRunAMQ
	})
	return orchestratorRegStubs{sent: sent, wakeOpts: wakeOpts}
}

func createExternalOrchestratorMailboxFixture(root, handles string) error {
	agents := strings.Split(handles, ",")
	for _, handle := range agents {
		for _, relative := range []string{"inbox/new", "inbox/cur", "inbox/tmp", "outbox/sent", "receipts", "dlq/new", "dlq/cur", "dlq/tmp"} {
			if err := os.MkdirAll(filepath.Join(root, "agents", handle, filepath.FromSlash(relative)), 0o700); err != nil {
				return err
			}
		}
	}
	if err := os.MkdirAll(filepath.Join(root, "meta"), 0o700); err != nil {
		return err
	}
	b, err := json.Marshal(struct {
		Version int      `json:"version"`
		Agents  []string `json:"agents"`
	}{Version: 1, Agents: agents})
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(root, "meta", "config.json"), b, 0o600)
}

func seedCtoLeadTeamForOrchestrator(t *testing.T) (string, string) {
	t.Helper()
	base := setupFakeAMQSessionRoots(t)
	t.Setenv("AMQ_FAKE_VERSION", "0.42.0")
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
// gated command yields the external wakeable identity without changing native
// team authority.
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
	if env.Kind != "goal_start" || env.Data.DryRun || env.Data.Status != "prompt_goal_delivered" {
		t.Fatalf("goal start envelope = %+v", env)
	}
	cfg, err := team.Read(dir)
	if err != nil {
		t.Fatalf("read team: %v", err)
	}
	if cfg.Lead != "cto" || !cfg.Orchestrated {
		t.Fatalf("team lead/orchestrated = %q/%v", cfg.Lead, cfg.Orchestrated)
	}
	if _, ok := teamMemberByRole(cfg, goalOrchestratorRole); ok {
		t.Fatalf("external orchestrator leaked into team members: %+v", cfg.Members)
	}
	rec, err := launch.Read(filepath.Join(base, "issue-96", "agents", "global-orch"))
	if err != nil {
		t.Fatalf("read orchestrator launch record: %v", err)
	}
	if !rec.External || rec.Role != goalOrchestratorRole || rec.WakePID != 9876 || rec.WakeInjectMode != "raw" || rec.Tmux == nil || rec.Tmux.PaneID != "%99" {
		t.Fatalf("orchestrator launch record = %+v", rec)
	}
	if rec.Terminal == nil || rec.Terminal.Backend != "tmux" || rec.Terminal.PaneID != "%99" || rec.Terminal.Target != "external" {
		t.Fatalf("orchestrator launch terminal identity = %+v", rec.Terminal)
	}
	if len(*stubs.wakeOpts) != 1 || (*stubs.wakeOpts)[0].Handle != "global-orch" || !(*stubs.wakeOpts)[0].Require || (*stubs.wakeOpts)[0].WakeInjectMode != "raw" {
		t.Fatalf("wake opts = %+v", *stubs.wakeOpts)
	}
}

func TestGoalStartRegisterOrchestratorNoneModeIsZeroInput(t *testing.T) {
	base, dir := seedCtoLeadTeamForOrchestrator(t)
	t.Setenv("AMQ_FAKE_VERSION", "0.42.0")
	stubs := setupOrchestratorRegStubs(t, dir)

	_, stderr, err := captureOutput(t, func() error {
		return runGoal([]string{"start", "--project", dir, "--session", "issue-96", "--role", "cto", "--goal", "ship safely", "--register-orchestrator=global-orch", "--wake-inject-mode", "none", "--yes", "--json"})
	})
	if err != nil {
		t.Fatalf("goal start none mode: %v\nstderr:\n%s", err, stderr)
	}
	if len(*stubs.wakeOpts) != 1 {
		t.Fatalf("wake opts = %+v", *stubs.wakeOpts)
	}
	wake := (*stubs.wakeOpts)[0]
	if wake.WakeInjectMode != "none" || wake.WakeInjectCmd != "" {
		t.Fatalf("none-mode wake opts = %+v", wake)
	}
	rec, err := launch.Read(filepath.Join(base, "issue-96", "agents", "global-orch"))
	if err != nil {
		t.Fatalf("read orchestrator launch record: %v", err)
	}
	if rec.WakeInjectMode != "none" || rec.WakeInjectCmd != "" {
		t.Fatalf("none-mode launch record = %+v", rec)
	}
	if _, stderr, err = captureOutput(t, func() error {
		return runGoal([]string{"start", "--project", dir, "--session", "issue-96", "--role", "cto", "--goal", "ship safely", "--register-orchestrator=global-orch", "--yes", "--json"})
	}); err != nil {
		t.Fatalf("goal repair without mode: %v\nstderr:\n%s", err, stderr)
	}
	if len(*stubs.wakeOpts) != 1 {
		t.Fatalf("registered rerun must not restart wake: %+v", *stubs.wakeOpts)
	}
	if _, stderr, err = captureOutput(t, func() error {
		return runGoal([]string{"start", "--project", dir, "--session", "issue-96", "--role", "cto", "--goal", "ship safely", "--register-orchestrator=global-orch", "--wake-inject-mode", "raw", "--yes", "--json"})
	}); err != nil {
		t.Fatalf("goal repair explicit raw: %v\nstderr:\n%s", err, stderr)
	}
	if len(*stubs.wakeOpts) != 1 {
		t.Fatalf("registered rerun must remain external-action idempotent: %+v", *stubs.wakeOpts)
	}
	rec, err = launch.Read(filepath.Join(base, "issue-96", "agents", "global-orch"))
	if err != nil || rec.WakeInjectMode != "none" || rec.WakeInjectCmd != "" {
		t.Fatalf("registered rerun mutated launch record = %+v, %v", rec, err)
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
	if count != 0 {
		t.Fatalf("external orchestrator must not enter native members, got %d (members=%+v)", count, cfg.Members)
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
	t.Setenv("AMQ_FAKE_VERSION", "0.42.0")
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
	oldRunAMQ := runAMQCommand
	runAMQCommand = func(req amqCommandRequest) ([]byte, error) {
		if len(req.Arg) > 0 && req.Arg[0] == "init" {
			if err := createExternalOrchestratorMailboxFixture(amqFlagValue(req.Arg, "root"), amqFlagValue(req.Arg, "agents")); err != nil {
				return nil, err
			}
			return []byte("Initialized AMQ root\n"), nil
		}
		return oldRunAMQ(req)
	}
	t.Cleanup(func() {
		currentPaneIdentity = prevPane
		statusPaneLister = oldLister
		sendPromptToPane = oldSend
		runAMQCommand = oldRunAMQ
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
	tm := team.Team{Project: dir, Lead: "cto", ExecutionMode: executionModeProjectLead}
	seedAgentRecord(t, base, "issue-96", "cto", launch.Record{
		CWD:      dir,
		Binary:   "codex",
		Handle:   "cto",
		Role:     "cto",
		Session:  "issue-96",
		AgentPID: 4242,
		Tmux:     &launch.TmuxInfo{PaneID: "%7"},
		GoalBinding: &launch.GoalBinding{
			Mode:       "prompt_goal",
			NativeGoal: false,
			Source:     "goal-control",
			Command:    codexGoalControlPrompt("ship safely", tm, team.DefaultProfile, "issue-96", "cto", ""),
			Goal:       "ship safely",
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
	tm := team.Team{Project: dir, Lead: "cto", ExecutionMode: executionModeProjectLead}
	seedAgentRecord(t, base, "issue-96", "cto", launch.Record{
		CWD:      dir,
		Binary:   "codex",
		Handle:   "cto",
		Role:     "cto",
		Session:  "issue-96",
		AgentPID: 4242,
		Tmux:     &launch.TmuxInfo{PaneID: "%7"},
		GoalBinding: &launch.GoalBinding{
			Mode:       "prompt_goal",
			NativeGoal: false,
			Source:     "goal-control",
			Command:    codexGoalControlPrompt("ship safely", tm, team.DefaultProfile, "issue-96", "cto", ""),
			Goal:       "ship safely",
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
	tm := team.Team{Project: dir, Lead: "cto", ExecutionMode: executionModeProjectLead}
	agentDir := seedAgentRecord(t, base, "issue-96", "cto", launch.Record{
		CWD:      dir,
		Binary:   "codex",
		Handle:   "cto",
		Role:     "cto",
		Session:  "issue-96",
		AgentPID: 4242,
		Tmux:     &launch.TmuxInfo{PaneID: "%7"},
		GoalBinding: &launch.GoalBinding{
			Mode:       "prompt_goal",
			NativeGoal: false,
			Source:     "goal-control",
			Command:    codexGoalControlPrompt("ship safely", tm, team.DefaultProfile, "issue-96", "cto", ""),
			Goal:       "ship safely",
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
	if len(sent) != 1 || !strings.Contains(sent[0], "AMQ-SQUAD PROMPT GOAL v1") || !strings.Contains(sent[0], "ship safely") {
		t.Fatalf("goal apply sent = %+v", sent)
	}
	env := decodeJSONEnvelope[goalApplyData](t, stdout)
	if env.Kind != "goal_apply" || env.Data.Status != "prompt_goal_delivered" || env.Data.GoalID != "g1" {
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
	if rec.GoalBinding == nil || rec.GoalBinding.Source != "goal-control" || rec.GoalBinding.NativeGoal || rec.GoalBinding.Mode != "prompt_goal" {
		t.Fatalf("launch goal binding not preserved/updated: %+v", rec.GoalBinding)
	}
}

func TestGoalApplyRejectsCorruptOrMismatchedPromptBindingBeforeMutation(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*launch.GoalBinding)
	}{
		{name: "corrupt command", mutate: func(binding *launch.GoalBinding) { binding.Command += "\ncorrupt" }},
		{name: "typed goal mismatch", mutate: func(binding *launch.GoalBinding) { binding.Goal = "different goal" }},
		{name: "typed attempt mismatch", mutate: func(binding *launch.GoalBinding) { binding.AttemptID = "different-attempt" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			base := setupFakeAMQSessionRoots(t)
			dir := seedTeam(t, team.Team{
				Members:       []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-96"}},
				Orchestrated:  true,
				Lead:          "cto",
				ExecutionMode: executionModeProjectLead,
			})
			tm := team.Team{Project: dir, Lead: "cto", ExecutionMode: executionModeProjectLead}
			const sourceAttempt = "source-attempt"
			binding := &launch.GoalBinding{
				Mode:       "prompt_goal",
				NativeGoal: false,
				Source:     "goal-control",
				Command:    codexGoalControlPrompt("ship safely", tm, team.DefaultProfile, "issue-96", "cto", sourceAttempt),
				Goal:       "ship safely",
				AttemptID:  sourceAttempt,
			}
			tt.mutate(binding)
			agentDir := seedAgentRecord(t, base, "issue-96", "cto", launch.Record{
				CWD: dir, Binary: "codex", Handle: "cto", Role: "cto", Session: "issue-96",
				AgentPID: 4242, Tmux: &launch.TmuxInfo{PaneID: "%7"}, GoalBinding: binding,
			})
			launchPath := launch.ExistingPath(agentDir)
			before, err := os.ReadFile(launchPath)
			if err != nil {
				t.Fatal(err)
			}
			oldSend := sendPromptToPane
			sends := 0
			sendPromptToPane = func(string, string) error { sends++; return nil }
			t.Cleanup(func() { sendPromptToPane = oldSend })

			_, _, err = captureOutput(t, func() error {
				return runGoal([]string{"apply", "--project", dir, "--session", "issue-96", "--gate", "release", "--yes"})
			})
			if err == nil || !strings.Contains(err.Error(), "could not read prompt_goal goal text") {
				t.Fatalf("invalid prompt binding accepted: %v", err)
			}
			after, readErr := os.ReadFile(launchPath)
			if readErr != nil || string(after) != string(before) || sends != 0 {
				t.Fatalf("goal apply mutated invalid binding: sends=%d changed=%t err=%v", sends, string(after) != string(before), readErr)
			}
			if _, statErr := os.Stat(goalAttemptDir(dir, team.DefaultProfile, "issue-96")); !os.IsNotExist(statErr) {
				t.Fatalf("goal apply created attempt state for invalid binding: %v", statErr)
			}
		})
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
	if !strings.Contains(env.Data.OrchestratorPrompt, "role: release-lead") {
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
	if !strings.Contains(env.Data.OrchestratorPrompt, "mode: global_orchestrator") || !strings.Contains(env.Data.OrchestratorPrompt, "target_contract: 2.10.0") {
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
	if !strings.Contains(env.Data.BriefSkeleton, "## Autonomous policy") || !strings.Contains(env.Data.OrchestratorPrompt, "- composition: autonomous") {
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
