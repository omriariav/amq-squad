package cli

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/bootstrapack"
	"github.com/omriariav/amq-squad/v2/internal/launch"
	squadnamespace "github.com/omriariav/amq-squad/v2/internal/namespace"
	"github.com/omriariav/amq-squad/v2/internal/team"
	"github.com/omriariav/amq-squad/v2/internal/tmuxpane"
	runwizard "github.com/omriariav/amq-squad/v2/internal/wizard"
)

func seededResumeGoalPlan(t *testing.T, conversation string, writeClaim bool) (team.Team, string, []resumePlan) {
	return seededResumeGoalPlanForBinary(t, conversation, writeClaim, "claude")
}

func seededResumeGoalPlanForBinary(t *testing.T, conversation string, writeClaim bool, binary string) (team.Team, string, []resumePlan) {
	t.Helper()
	project := t.TempDir()
	session := "issue-447"
	tm := team.Team{
		Project: project, Orchestrated: true, Lead: "cto", ExecutionMode: executionModeProjectLead,
		Members: []team.Member{{Role: "cto", Handle: "cto", Binary: binary, Session: session, CWD: project}},
	}
	ns := squadnamespace.Resolve(project, team.DefaultProfile, session)
	const attemptID = "attempt-original"
	goal := "ship literal --attempt-id fake\nwith \"quotes\""
	contract, err := goalDeliveryContractForBinary(binary)
	if err != nil {
		t.Fatal(err)
	}
	command := contract.prompt(goal, tm, team.DefaultProfile, session, "cto", attemptID)
	created := time.Unix(100, 0).UTC()
	attempt := goalAttemptRecord{SchemaVersion: 1, AttemptID: attemptID, Goal: goal, Project: project, Profile: team.DefaultProfile, Session: session, Namespace: ns, Role: "cto", Handle: "cto", CreatedAt: created}
	attemptPath, err := goalAttemptPath(project, team.DefaultProfile, session, attemptID)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(attemptPath), 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestJSON(t, attemptPath, attempt)
	if writeClaim {
		writeTestJSON(t, goalAttemptClaimPath(attemptPath), goalAttemptClaim{AttemptID: attemptID, Route: contract.ClaimRoute, ClaimedAt: created.Add(time.Second)})
	}
	rec := launch.Record{
		CWD: project, Binary: binary, Session: session, SharedWorkstream: true, Conversation: conversation,
		Handle: "cto", Role: "cto", Root: ns.AMQRoot, TeamHome: project, TeamProfile: team.DefaultProfile,
		BootstrapExpectation: &bootstrapack.Expectation{Required: true},
		GoalBinding:          contract.binding(goal, attemptID, command, "goal-control", "delivered"),
	}
	return tm, session, []resumePlan{{Role: "cto", Handle: "cto", Action: resumeRestore, HasRestoreRecord: true, RestoreRecord: &rec}}
}

func writeTestJSON(t *testing.T, path string, value any) {
	t.Helper()
	payload, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(payload, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
}

func mustGoalAttemptPath(t *testing.T, project, profile, session, attemptID string) string {
	t.Helper()
	path, err := goalAttemptPath(project, profile, session, attemptID)
	if err != nil {
		t.Fatal(err)
	}
	return path
}
func TestResumeGoalPlanRejectsSavedTeamHomeAndAdoptedTarget(t *testing.T) {
	for _, tt := range []struct {
		name   string
		mutate func(*launch.Record, string)
		want   string
	}{
		{
			name:   "team home mismatch",
			mutate: func(rec *launch.Record, project string) { rec.TeamHome = filepath.Join(project, "other-team") },
			want:   "team home",
		},
		{
			name:   "adopted pane",
			mutate: func(rec *launch.Record, _ string) { rec.Tmux = &launch.TmuxInfo{PaneID: "%old", Target: "adopted"} },
			want:   "adopted",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			tm, session, plans := seededResumeGoalPlan(t, "", true)
			rec := *plans[0].RestoreRecord
			tt.mutate(&rec, tm.Project)
			plans[0].RestoreRecord = &rec
			plan := buildResumeGoalPlan(tm, team.DefaultProfile, session, plans, false, false)
			if plan.Eligible || !strings.Contains(plan.Reason, tt.want) {
				t.Fatalf("plan accepted invalid saved identity: %+v", plan)
			}
		})
	}
}

func TestVerifyResumeGoalPostBaselineReadyUsesExactLeadAndRefusesBeforeResend(t *testing.T) {
	oldReady := verifyResumeLeadReadyNow
	var got resumeExecLaunchCheck
	verifyResumeLeadReadyNow = func(check resumeExecLaunchCheck) error {
		got = check
		return nil
	}
	t.Cleanup(func() { verifyResumeLeadReadyNow = oldReady })

	check := resumeExecLaunchCheck{Role: "cto", Handle: "cto", Root: "/mail/issue-524"}
	results := []resumeExecLaunchResult{
		{Check: resumeExecLaunchCheck{Role: "worker"}, State: resumeExecLaunchStateLaunched},
		{Check: check, State: resumeExecLaunchStateLaunched},
	}
	plan := runwizard.ResumeGoalPlan{LeadRole: "cto"}
	if err := verifyResumeGoalPostBaselineReady(results, plan); err != nil {
		t.Fatalf("verified post-baseline readiness: %v", err)
	}
	if got != check {
		t.Fatalf("verified check = %+v, want %+v", got, check)
	}

	verifyResumeLeadReadyNow = func(resumeExecLaunchCheck) error {
		return errors.New("wake baseline is not armed")
	}
	err := verifyResumeGoalPostBaselineReady(results, plan)
	var partial *PartialError
	if !errors.As(err, &partial) || !strings.Contains(err.Error(), "no post-baseline goal re-send was attempted") || !strings.Contains(err.Error(), "wake baseline is not armed") {
		t.Fatalf("unready lead error = %v", err)
	}
}

func TestResumeJSONSelectedGoalPlanIsReadOnly(t *testing.T) {
	tm, session, plans := seededResumeGoalPlan(t, "", true)
	if err := team.WriteProfile(tm.Project, team.DefaultProfile, tm); err != nil {
		t.Fatal(err)
	}
	ns := squadnamespace.Resolve(tm.Project, team.DefaultProfile, session)
	agentDir := filepath.Join(ns.AMQRoot, "agents", "cto")
	rec := *plans[0].RestoreRecord
	rec.TeamHome = tm.Project
	if err := launch.Write(agentDir, rec); err != nil {
		t.Fatal(err)
	}
	launchBefore, _ := os.ReadFile(launch.ExistingPath(agentDir))
	entriesBefore, _ := os.ReadDir(goalAttemptDir(tm.Project, team.DefaultProfile, session))
	oldLister := statusPaneLister
	statusPaneLister = func() ([]tmuxpane.TmuxPane, error) { return nil, nil }
	t.Cleanup(func() { statusPaneLister = oldLister })
	var out strings.Builder
	err := executeResume(resumeExecution{
		ProjectDir: tm.Project, RequestedSession: session, ExplicitSession: true, Profile: team.DefaultProfile, JSON: true, GoalRedelivery: true,
		Probe: duplicateLaunchProbe{PIDAlive: func(int) bool { return false }, ProcessMatch: func(int, func(string) bool) bool { return false }, Now: time.Now},
		Exec:  resumeExecOptions{RedeliverGoal: true, RedeliveryExplicit: true}, Out: &out,
	})
	if err != nil {
		t.Fatalf("plan-only selected resume JSON: %v", err)
	}
	env := decodeJSONEnvelope[resumeEnvelopeData](t, out.String())
	if env.SchemaVersion != 1 || env.Data.GoalPlan == nil || !env.Data.GoalPlan.Selected || !env.Data.GoalPlan.Eligible {
		t.Fatalf("goal plan JSON=%s", out.String())
	}
	launchAfter, _ := os.ReadFile(launch.ExistingPath(agentDir))
	entriesAfter, _ := os.ReadDir(goalAttemptDir(tm.Project, team.DefaultProfile, session))
	if string(launchAfter) != string(launchBefore) || len(entriesAfter) != len(entriesBefore) {
		t.Fatalf("plan-only JSON mutated launch/attempt evidence")
	}
	transitionPath, _ := resumeGoalTransitionPath(tm.Project, team.DefaultProfile, session, env.Data.GoalPlan.TransitionID)
	if _, err := os.Stat(transitionPath); !os.IsNotExist(err) {
		t.Fatalf("plan-only JSON published transition: %v", err)
	}
}

func TestResumeGoalPlanEligibleUsesExactSettledEvidence(t *testing.T) {
	tm, session, plans := seededResumeGoalPlan(t, "", true)
	got := buildResumeGoalPlan(tm, team.DefaultProfile, session, plans, false, false)
	if !got.Eligible || got.Action != "redeliver" || got.ClaimState != "claimed" || got.AttemptState != "recorded" || got.OriginalAttemptID != "attempt-original" {
		t.Fatalf("goal plan = %+v", got)
	}
	if !strings.Contains(got.Goal, "literal --attempt-id fake") || got.BindingCommandDigest == "" || got.AttemptDigest == "" || got.ClaimDigest == "" || got.EvidenceDigest == "" {
		t.Fatalf("goal plan omitted exact scalar evidence: %+v", got)
	}
	if again := buildResumeGoalPlan(tm, team.DefaultProfile, session, plans, false, false); again != got {
		t.Fatalf("read-only plan is not byte-stable:\n%+v\n%+v", got, again)
	}
}

func TestResumeGoalAttemptIdentityIsExact(t *testing.T) {
	tm, session, plans := seededResumeGoalPlan(t, "", true)
	plan := buildResumeGoalPlan(tm, team.DefaultProfile, session, plans, false, false)
	path := mustGoalAttemptPath(t, tm.Project, team.DefaultProfile, session, plan.OriginalAttemptID)
	attempt, err := readGoalAttempt(path, plan.OriginalAttemptID)
	if err != nil {
		t.Fatal(err)
	}
	ns := squadnamespace.Resolve(tm.Project, team.DefaultProfile, session)
	mutations := map[string]func(*goalAttemptRecord){
		"role case":         func(a *goalAttemptRecord) { a.Role = "CTO" },
		"handle case":       func(a *goalAttemptRecord) { a.Handle = "CTO" },
		"namespace display": func(a *goalAttemptRecord) { a.Namespace.Display = "changed" },
		"namespace path":    func(a *goalAttemptRecord) { a.Namespace.Paths.Brief = "/other" },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			got := attempt
			mutate(&got)
			if err := validateResumeGoalAttempt(got, tm.Project, team.DefaultProfile, session, "cto", "cto", plan.Goal, plan.OriginalAttemptID, ns); err == nil {
				t.Fatalf("exact identity mutation accepted: %+v", got)
			}
		})
	}
}

func TestResumeGoalPlanReattachSkipsButFingerprintsBinding(t *testing.T) {
	tm, session, plans := seededResumeGoalPlan(t, "saved-conversation", false)
	got := buildResumeGoalPlan(tm, team.DefaultProfile, session, plans, false, false)
	if got.Eligible || got.Action != "skip" || !got.SavedConversation || got.BindingDigest == "" || got.BindingCommandDigest == "" || got.Goal == "" {
		t.Fatalf("reattach plan = %+v", got)
	}
}

func TestResumeSurfacesNativeGoalBlockedRecoveryWithoutReactivation(t *testing.T) {
	tm, session, plans := seededResumeGoalPlan(t, "", true)
	rec := *plans[0].RestoreRecord
	binding := *rec.GoalBinding
	binding.Mode = "native_goal_blocked"
	binding.Detail = "Goal blocked (/goal resume)\n\x1b[31munsafe control text"
	rec.GoalBinding = &binding
	plans[0].RestoreRecord = &rec

	recoveries := resumeNativeGoalBlockedRecoveries(plans)
	if len(recoveries) != 1 || recoveries[0].Role != "cto" || recoveries[0].Action != string(resumeRestore) {
		t.Fatalf("recoveries = %+v", recoveries)
	}
	if !strings.Contains(recoveries[0].Guidance, "/goal resume") || strings.Contains(recoveries[0].Guidance, "automatically redeliver") && !strings.Contains(recoveries[0].Guidance, "Do not automatically") {
		t.Fatalf("unsafe recovery guidance: %q", recoveries[0].Guidance)
	}

	var plain strings.Builder
	writeResumeNativeGoalBlockedRecoveries(&plain, recoveries)
	if !strings.Contains(plain.String(), "Native goal recovery required") || !strings.Contains(plain.String(), "/goal resume") || strings.ContainsRune(plain.String(), '\x1b') {
		t.Fatalf("plain recovery output is not safe/explicit: %q", plain.String())
	}
	if strings.Contains(plain.String(), rec.GoalBinding.Command) {
		t.Fatalf("plain recovery output leaked saved goal command: %q", plain.String())
	}

	var jsonOut strings.Builder
	if err := writeResumeJSONWithGoal(&jsonOut, tm, session, resumeModeDefault, team.DefaultProfile, nil, plans, runwizard.ResumeGoalPlan{}); err != nil {
		t.Fatal(err)
	}
	env := decodeJSONEnvelope[resumeEnvelopeData](t, jsonOut.String())
	if len(env.Data.NativeGoalBlockedRecovery) != 1 || env.Data.NativeGoalBlockedRecovery[0].Guidance != nativeGoalBlockedResumeGuidance {
		t.Fatalf("native blocked recovery JSON = %s", jsonOut.String())
	}
}

func TestResumeNativeGoalBlockedRecoveryCoversMixedRosterWithoutFalsePositives(t *testing.T) {
	blockedLead := launch.Record{GoalBinding: &launch.GoalBinding{Mode: "native_goal_blocked", NativeGoal: true, Detail: "lead blocked"}}
	blockedWorker := launch.Record{GoalBinding: &launch.GoalBinding{Mode: "native_goal_blocked", NativeGoal: true, Detail: "worker blocked"}}
	nativeDelivered := launch.Record{GoalBinding: &launch.GoalBinding{Mode: "native_goal", NativeGoal: true, Detail: "delivered"}}
	plans := []resumePlan{
		{Role: "cto", Handle: "cto", Action: resumeRestore, RestoreRecord: &blockedLead},
		{Role: "fullstack", Handle: "fullstack", Action: resumeRestore, RestoreRecord: &nativeDelivered},
		{Role: "qa", Handle: "qa", Action: resumeFresh},
		{Role: "analyst", Handle: "analyst", Action: resumeRestore, RestoreRecord: &blockedWorker},
	}
	recoveries := resumeNativeGoalBlockedRecoveries(plans)
	if len(recoveries) != 2 || recoveries[0].Role != "cto" || recoveries[1].Role != "analyst" {
		t.Fatalf("mixed roster recoveries = %+v", recoveries)
	}
	for _, recovery := range recoveries {
		if recovery.Action != string(resumeRestore) || recovery.Guidance != nativeGoalBlockedResumeGuidance || strings.Contains(strings.ToLower(recovery.Detail), "delivered") {
			t.Fatalf("invalid recovery = %+v", recovery)
		}
	}
	var out strings.Builder
	writeResumeNativeGoalBlockedRecoveries(&out, recoveries)
	if strings.Count(out.String(), "# Recovery:") != 2 || !strings.Contains(out.String(), "cto") || !strings.Contains(out.String(), "analyst") || strings.Contains(out.String(), "fullstack") {
		t.Fatalf("mixed roster output = %q", out.String())
	}
}

func TestResumeGoalPlanUnclaimedBlocksWithoutCreatingAttempt(t *testing.T) {
	tm, session, plans := seededResumeGoalPlan(t, "", false)
	dir := goalAttemptDir(tm.Project, team.DefaultProfile, session)
	before, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	got := buildResumeGoalPlan(tm, team.DefaultProfile, session, plans, false, false)
	after, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got.Eligible || got.Action != "blocked" || got.ClaimState != "unclaimed" || len(after) != len(before) {
		t.Fatalf("unclaimed plan=%+v files before=%d after=%d", got, len(before), len(after))
	}
}

func TestResumeGoalPlanRejectsNonGeneratedRawCommand(t *testing.T) {
	tm, session, plans := seededResumeGoalPlan(t, "", true)
	valid := buildResumeGoalPlan(tm, team.DefaultProfile, session, plans, false, false)
	mutations := []string{
		plans[0].RestoreRecord.GoalBinding.Command + " --attempt-id duplicate",
		plans[0].RestoreRecord.GoalBinding.Command + " --unknown value",
		strings.Replace(plans[0].RestoreRecord.GoalBinding.Command, "--profile default", "--profile other", 1),
	}
	for _, command := range mutations {
		rec := *plans[0].RestoreRecord
		binding := *rec.GoalBinding
		binding.Command = command
		rec.GoalBinding = &binding
		mutated := []resumePlan{plans[0]}
		mutated[0].RestoreRecord = &rec
		got := buildResumeGoalPlan(tm, team.DefaultProfile, session, mutated, false, false)
		if got.Eligible || got.Action != "blocked" || got.BindingCommandDigest == valid.BindingCommandDigest {
			t.Fatalf("crafted command accepted: %q\n%+v", command, got)
		}
	}
}

func TestResumeGoalPlanRejectsCorruptOrMismatchedPromptBindingWithoutMutation(t *testing.T) {
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
			tm, session, plans := seededResumeGoalPlanForBinary(t, "", true, "codex")
			attemptPath := mustGoalAttemptPath(t, tm.Project, team.DefaultProfile, session, "attempt-original")
			claimPath := goalAttemptClaimPath(attemptPath)
			beforeAttempt, err := os.ReadFile(attemptPath)
			if err != nil {
				t.Fatal(err)
			}
			beforeClaim, err := os.ReadFile(claimPath)
			if err != nil {
				t.Fatal(err)
			}
			beforeEntries, err := os.ReadDir(filepath.Dir(attemptPath))
			if err != nil {
				t.Fatal(err)
			}
			rec := *plans[0].RestoreRecord
			binding := *rec.GoalBinding
			tt.mutate(&binding)
			rec.GoalBinding = &binding
			plans[0].RestoreRecord = &rec
			oldSend := sendPromptToPane
			sends := 0
			sendPromptToPane = func(string, string) error { sends++; return nil }
			t.Cleanup(func() { sendPromptToPane = oldSend })

			got := buildResumeGoalPlan(tm, team.DefaultProfile, session, plans, false, false)
			if got.Eligible || got.Action != "blocked" || !strings.Contains(got.Reason, "saved goal binding is invalid") {
				t.Fatalf("invalid resume binding accepted: %+v", got)
			}
			afterAttempt, attemptErr := os.ReadFile(attemptPath)
			afterClaim, claimErr := os.ReadFile(claimPath)
			afterEntries, entriesErr := os.ReadDir(filepath.Dir(attemptPath))
			if attemptErr != nil || claimErr != nil || entriesErr != nil || string(afterAttempt) != string(beforeAttempt) || string(afterClaim) != string(beforeClaim) || len(afterEntries) != len(beforeEntries) || sends != 0 {
				t.Fatalf("resume mutated invalid binding: sends=%d entries=%d/%d attempt_changed=%t claim_changed=%t attempt_err=%v claim_err=%v entries_err=%v", sends, len(afterEntries), len(beforeEntries), string(afterAttempt) != string(beforeAttempt), string(afterClaim) != string(beforeClaim), attemptErr, claimErr, entriesErr)
			}
		})
	}
}

func TestParseGeneratedGoalBindingIsQuoteAware(t *testing.T) {
	tm := team.Team{ExecutionMode: executionModeProjectLead}
	wantGoal := "say --attempt-id fake and \"quoted\"\nsecond line"
	command := nativeGoalControlPrompt(wantGoal, tm, "default", "issue-447", "cto", "real-attempt")
	goal, attemptID, err := parseGeneratedGoalBinding(command)
	if err != nil || attemptID != "real-attempt" || goal != wantGoal || strings.Contains(command, `quoted\nsecond`) {
		t.Fatalf("parse = goal %q attempt %q err %v", goal, attemptID, err)
	}
	if _, _, err := parseGeneratedGoalBinding(command + " --attempt-id duplicate"); err == nil {
		t.Fatal("duplicate attempt flag must fail")
	}
}

func TestRestoreGoalBindingIsMetadataNotChildArg(t *testing.T) {
	tm := team.Team{Project: "/tmp/project", Lead: "cto", ExecutionMode: executionModeProjectLead}
	command := codexGoalControlPrompt("ship\nsecond line", tm, team.DefaultProfile, "s", "cto", "a1")
	rec := launch.Record{CWD: "/tmp/project", Binary: "codex", Role: "cto", Handle: "cto", Session: "s", Argv: []string{"--enable", "goals", command}, GoalBinding: &launch.GoalBinding{Mode: "prompt_goal", NativeGoal: false, Source: "goal-control", Command: command, Goal: "ship\nsecond line", AttemptID: "a1", Detail: "reserved"}}
	args := launchArgsFromRecord(rec)
	var metadata string
	dash := len(args)
	for i, arg := range args {
		if arg == "--restore-goal-binding" && i+1 < len(args) {
			metadata = args[i+1]
		}
		if arg == "--" {
			dash = i
			break
		}
	}
	if metadata == "" {
		t.Fatalf("restore args omitted binding metadata: %#v", args)
	}
	for _, arg := range args[dash:] {
		if arg == command {
			t.Fatalf("saved goal leaked into child argv: %#v", args)
		}
	}
	var decoded launch.GoalBinding
	if err := json.Unmarshal([]byte(metadata), &decoded); err != nil || decoded != *rec.GoalBinding {
		t.Fatalf("metadata round trip = %+v, %v", decoded, err)
	}
	if emitted := emitCommand(rec); !strings.Contains(emitted, "--restore-goal-binding") {
		t.Fatalf("copy-paste restore omitted binding metadata: %s", emitted)
	}
}

func TestRunLaunchRestoresGoalBindingMetadataWithoutActivation(t *testing.T) {
	project := seedTeam(t, team.Team{Members: []team.Member{{Role: "cto", Handle: "cto", Binary: "codex", Session: "issue-447"}}, Orchestrated: true, Lead: "cto"})
	setupFakeAMQ(t)
	tm := team.Team{Project: project, Members: []team.Member{{Role: "cto", Handle: "cto", Binary: "codex", Session: "issue-447"}}, Orchestrated: true, Lead: "cto", ExecutionMode: executionModeProjectLead}
	goal := "ship\nsecond line"
	command := codexGoalControlPrompt(goal, tm, team.DefaultProfile, "issue-447", "cto", "a1")
	binding := &launch.GoalBinding{Mode: "prompt_goal", NativeGoal: false, Source: "goal-control", Command: command, Goal: goal, AttemptID: "a1", Detail: "reserved bytes\nunchanged"}
	rec := launch.Record{CWD: project, Binary: "codex", Role: "cto", Handle: "cto", Session: "issue-447", SharedWorkstream: true, TeamHome: project, Argv: []string{"--enable", "goals", binding.Command}, GoalBinding: binding}
	var observed launch.Record
	var child []string
	oldObserver := launchPlanObserver
	launchPlanObserver = func(got launch.Record, args []string) { observed, child = got, append([]string(nil), args...) }
	t.Cleanup(func() { launchPlanObserver = oldObserver })
	args := append([]string{"--dry-run", "--project", project}, launchArgsFromRecord(rec)...)
	if _, _, err := captureOutput(t, func() error { return runLaunch(args) }); err != nil {
		t.Fatalf("restore launch: %v", err)
	}
	if observed.GoalBinding == nil || *observed.GoalBinding != *binding {
		t.Fatalf("restored binding changed: got=%+v want=%+v", observed.GoalBinding, binding)
	}
	for _, arg := range child {
		if arg == binding.Command {
			t.Fatalf("restored binding activated through child argv: %v", child)
		}
	}
	payload, err := json.Marshal(binding)
	if err != nil {
		t.Fatal(err)
	}
	conflict := []string{"--dry-run", "--project", project, "--session", "issue-447", "--role", "cto", "--restore-goal-binding", string(payload), "codex", "--", binding.Command}
	if _, _, err := captureOutput(t, func() error { return runLaunch(conflict) }); err == nil || !strings.Contains(err.Error(), "cannot be combined") {
		t.Fatalf("metadata/prompt-goal conflict accepted: %v", err)
	}
}

// TestDeliverResumeGoalAfterLaunchPrintsRetirementNoticeAndNeverFails is
// gh#761's resume-side named test (task/t9's ruling): automatic goal
// redelivery via pane injection is retired for ALL sessions in v2.31.0.
// Given a valid eligible/selected plan and a matching launched lead,
// deliverResumeGoalAfterLaunch must print a non-silent retirement notice
// naming the complete runnable `amq-squad goal --goal ...` replacement, and
// must return nil -- resume --exec as a whole must not fail just because
// the retired --redeliver-goal flag was passed.
func TestDeliverResumeGoalAfterLaunchPrintsRetirementNoticeAndNeverFails(t *testing.T) {
	tm := team.Team{
		Project: "/Code/app", Orchestrated: true, Lead: "cto", ExecutionMode: executionModeProjectLead,
		Members: []team.Member{{Role: "cto", Handle: "cto", Binary: "codex", Session: "issue-447"}},
	}
	results := []resumeExecLaunchResult{
		{Check: resumeExecLaunchCheck{Role: "cto", Handle: "cto"}, State: resumeExecLaunchStateLaunched},
	}
	plan := runwizard.ResumeGoalPlan{Eligible: true, Selected: true, LeadRole: "cto", Goal: "ship v2.31.0"}

	_, stderr, err := captureOutput(t, func() error {
		return deliverResumeGoalAfterLaunch(tm, team.DefaultProfile, "issue-447", results, plan)
	})
	if err != nil {
		t.Fatalf("deliverResumeGoalAfterLaunch returned an error, want nil (retirement must not fail resume --exec): %v", err)
	}
	if !strings.Contains(stderr, "retired") {
		t.Fatalf("stderr does not name the retirement (non-silent requirement): %q", stderr)
	}
	want := "amq-squad goal --goal 'ship v2.31.0' --project /Code/app --profile default --session issue-447"
	if !strings.Contains(stderr, want) {
		t.Fatalf("stderr does not contain the complete runnable replacement command %q: %q", want, stderr)
	}
}

// TestResumeGoalRecoveryGuidanceNeverPrintsRemovedSubcommands proves the
// second half of task/t9's ruling: everywhere this package prints
// operator-facing recovery/redelivery guidance (goalManualDeliveryCommand,
// the shared builder for buildResumeGoalPlan's RecoveryCommand across the
// "consumed" and "reserved" transition states, and writeResumeGoalPlan's own
// prose), the printed text never references a goal subcommand gh#761
// removed (start/retry-attempt/apply/claim/deliver), and always offers the
// `amq-squad goal --goal` replacement instead.
func TestResumeGoalRecoveryGuidanceNeverPrintsRemovedSubcommands(t *testing.T) {
	removed := []string{"goal start", "goal retry-attempt", "goal apply", "goal claim", "goal deliver"}
	assertNoRemovedSubcommands := func(t *testing.T, label, text string) {
		t.Helper()
		for _, r := range removed {
			if strings.Contains(text, r) {
				t.Fatalf("%s printed a removed subcommand %q: %q", label, r, text)
			}
		}
	}

	cmd := goalManualDeliveryCommand("/Code/app", team.DefaultProfile, "issue-447", "ship v2.31.0")
	assertNoRemovedSubcommands(t, "goalManualDeliveryCommand", cmd)
	if !strings.Contains(cmd, "amq-squad goal --goal") {
		t.Fatalf("goalManualDeliveryCommand does not offer the replacement verb: %q", cmd)
	}

	var buf strings.Builder
	writeResumeGoalPlan(&buf, runwizard.ResumeGoalPlan{
		SchemaVersion: resumeGoalPlanSchemaVersion, Eligible: true, Goal: "ship v2.31.0", RecoveryCommand: cmd, RecoveryAttemptID: "attempt-1",
	})
	assertNoRemovedSubcommands(t, "writeResumeGoalPlan", buf.String())
	if !strings.Contains(buf.String(), "retired") {
		t.Fatalf("writeResumeGoalPlan prose does not name the retirement: %q", buf.String())
	}
}
