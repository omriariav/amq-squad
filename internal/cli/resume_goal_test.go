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

func freshResumeTransitionFixture(t *testing.T) (team.Team, string, []resumePlan, runwizard.ResumeGoalPlan, resumeExecLaunchResult) {
	return freshResumeTransitionFixtureForBinary(t, "claude")
}

func freshResumeTransitionFixtureForBinary(t *testing.T, binary string) (team.Team, string, []resumePlan, runwizard.ResumeGoalPlan, resumeExecLaunchResult) {
	t.Helper()
	tm, session, plans := seededResumeGoalPlanForBinary(t, "", true, binary)
	if err := team.WriteProfile(tm.Project, team.DefaultProfile, tm); err != nil {
		t.Fatal(err)
	}
	plan := buildResumeGoalPlan(tm, team.DefaultProfile, session, plans, false, false)
	if !plan.Eligible {
		t.Fatalf("seed plan ineligible: %+v", plan)
	}
	ns := squadnamespace.Resolve(tm.Project, team.DefaultProfile, session)
	agentDir := filepath.Join(ns.AMQRoot, "agents", "cto")
	rec := *plans[0].RestoreRecord
	rec.TeamHome = tm.Project
	rec.StartedAt = time.Unix(500, 0).UTC()
	rec.BootstrapExpectation = &bootstrapack.Expectation{Required: true, LaunchID: "fresh-launch-447"}
	rec.Tmux = &launch.TmuxInfo{PaneID: "%447", Target: "current-window"}
	if err := launch.Write(agentDir, rec); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(launch.ExistingPath(agentDir))
	if err != nil {
		t.Fatal(err)
	}
	oldInspector := statusPaneInspector
	statusPaneInspector = func(id string) (tmuxpane.TmuxPane, bool) {
		return tmuxpane.TmuxPane{PaneID: id, CWD: tm.Project, Command: binary}, id == "%447"
	}
	t.Cleanup(func() { statusPaneInspector = oldInspector })
	result := resumeExecLaunchResult{
		Check: resumeExecLaunchCheck{Role: "cto", Handle: "cto", CWD: tm.Project, AgentDir: agentDir, Workstream: session, Root: ns.AMQRoot, Binary: binary, Profile: team.DefaultProfile},
		State: resumeExecLaunchStateLaunched, RecordModTime: info.ModTime(), RecordStarted: rec.StartedAt,
	}
	return tm, session, plans, plan, result
}

func TestResumeGoalTransitionNoReplaceAndPlannerFingerprint(t *testing.T) {
	tm, session, plans, plan, verified := freshResumeTransitionFixture(t)
	if err := reserveResumeGoalTransition(tm, team.DefaultProfile, session, verified, plan); err != nil {
		t.Fatalf("reserve transition: %v", err)
	}
	if err := reserveResumeGoalTransition(tm, team.DefaultProfile, session, verified, plan); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("duplicate transition accepted: %v", err)
	}
	blocked := buildResumeGoalPlan(tm, team.DefaultProfile, session, plans, false, false)
	if blocked.Eligible || blocked.Action != "continue" || blocked.TransitionID != plan.TransitionID || blocked.TransitionState != "reserved" || blocked.TransitionDigest == "" || blocked.RecoveryAttemptID == "" || !strings.Contains(blocked.RecoveryCommand, "--resume-transition "+plan.TransitionID) || blocked.EvidenceDigest == plan.EvidenceDigest {
		t.Fatalf("transition not included in read-only plan/fingerprint: %+v", blocked)
	}
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

func TestResumeGoalTransitionConcurrentReservationHasOneWinner(t *testing.T) {
	tm, session, _, plan, verified := freshResumeTransitionFixture(t)
	start := make(chan struct{})
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() {
			<-start
			errs <- reserveResumeGoalTransition(tm, team.DefaultProfile, session, verified, plan)
		}()
	}
	close(start)
	success, refused := 0, 0
	for i := 0; i < 2; i++ {
		if err := <-errs; err == nil {
			success++
		} else if strings.Contains(err.Error(), "already exists") {
			refused++
		} else {
			t.Fatalf("unexpected concurrent reservation result: %v", err)
		}
	}
	if success != 1 || refused != 1 {
		t.Fatalf("concurrent reservation success=%d refused=%d", success, refused)
	}
}

func TestResumeGoalTransitionRejectsLaunchRecordABA(t *testing.T) {
	tm, session, _, plan, verified := freshResumeTransitionFixture(t)
	path := launch.ExistingPath(verified.Check.AgentDir)
	original, err := launch.Read(verified.Check.AgentDir)
	if err != nil {
		t.Fatal(err)
	}
	mutated := original
	mutated.GoalBinding = &launch.GoalBinding{Mode: "native_goal", NativeGoal: true, Source: "goal-control", Command: nativeGoalControlPrompt("different", tm, team.DefaultProfile, session, "cto", "different-attempt")}
	if err := launch.Write(verified.Check.AgentDir, mutated); err != nil {
		t.Fatal(err)
	}
	if err := launch.Write(verified.Check.AgentDir, original); err != nil {
		t.Fatal(err)
	}
	future := verified.RecordModTime.Add(2 * time.Second)
	if err := os.Chtimes(path, future, future); err != nil {
		t.Fatal(err)
	}
	if err := reserveResumeGoalTransition(tm, team.DefaultProfile, session, verified, plan); err == nil || !strings.Contains(err.Error(), "ABA") {
		t.Fatalf("binding replace+restore generation accepted: %v", err)
	}
	transitionPath, _ := resumeGoalTransitionPath(tm.Project, team.DefaultProfile, session, plan.TransitionID)
	if _, err := os.Stat(transitionPath); !os.IsNotExist(err) {
		t.Fatalf("ABA refusal published a transition: %v", err)
	}
}

func TestResumeGoalTransitionRejectsAdoptedFreshness(t *testing.T) {
	tm, session, _, plan, verified := freshResumeTransitionFixture(t)
	rec, err := launch.Read(verified.Check.AgentDir)
	if err != nil {
		t.Fatal(err)
	}
	rec.Tmux.Target = "adopted"
	if err := launch.Write(verified.Check.AgentDir, rec); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(launch.ExistingPath(verified.Check.AgentDir))
	if err != nil {
		t.Fatal(err)
	}
	verified.RecordModTime = info.ModTime()
	if err := reserveResumeGoalTransition(tm, team.DefaultProfile, session, verified, plan); err == nil || !strings.Contains(err.Error(), "adopted") {
		t.Fatalf("adopted launch accepted for redelivery: %v", err)
	}
}

func TestResumeGoalPostLaunchEvidenceMutationRefusesBeforeSend(t *testing.T) {
	for _, kind := range []string{"binding", "attempt", "claim"} {
		t.Run(kind, func(t *testing.T) {
			tm, session, _, plan, verified := freshResumeTransitionFixture(t)
			plan.Selected = true
			switch kind {
			case "binding":
				rec, err := launch.Read(verified.Check.AgentDir)
				if err != nil {
					t.Fatal(err)
				}
				rec.GoalBinding.Detail = "changed-after-verification"
				if err := launch.Write(verified.Check.AgentDir, rec); err != nil {
					t.Fatal(err)
				}
			case "attempt", "claim":
				path := mustGoalAttemptPath(t, tm.Project, team.DefaultProfile, session, plan.OriginalAttemptID)
				if kind == "claim" {
					path = goalAttemptClaimPath(path)
				}
				payload, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, append(payload, ' '), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			goalCalls := 0
			oldGoal := runStartGoalWithVersion
			runStartGoalWithVersion = func([]string, string) error { goalCalls++; return nil }
			t.Cleanup(func() { runStartGoalWithVersion = oldGoal })
			err := deliverResumeGoalAfterLaunch(tm, team.DefaultProfile, session, []resumeExecLaunchResult{verified}, plan)
			var partial *PartialError
			if !errors.As(err, &partial) || goalCalls != 0 {
				t.Fatalf("mutation=%s err=%v goalCalls=%d", kind, err, goalCalls)
			}
			transitionPath, _ := resumeGoalTransitionPath(tm.Project, team.DefaultProfile, session, plan.TransitionID)
			if _, err := os.Stat(transitionPath); !os.IsNotExist(err) {
				t.Fatalf("mutation=%s published transition: %v", kind, err)
			}
			entries, _ := os.ReadDir(goalAttemptDir(tm.Project, team.DefaultProfile, session))
			attempts := 0
			for _, entry := range entries {
				if !strings.HasPrefix(entry.Name(), ".") && strings.HasSuffix(entry.Name(), ".json") && !strings.HasSuffix(entry.Name(), ".claim.json") {
					attempts++
				}
			}
			if attempts != 1 {
				t.Fatalf("mutation=%s attempts=%d entries=%v", kind, attempts, entries)
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

func TestResumeGoalTransitionCreatesExactlyOnePreallocatedAttempt(t *testing.T) {
	tm, session, _, plan, verified := freshResumeTransitionFixture(t)
	if err := reserveResumeGoalTransition(tm, team.DefaultProfile, session, verified, plan); err != nil {
		t.Fatal(err)
	}
	oldLister, oldSend := statusPaneLister, sendPromptToPane
	statusPaneLister = func() ([]tmuxpane.TmuxPane, error) {
		return []tmuxpane.TmuxPane{{PaneID: "%447", CWD: tm.Project, Command: "codex", Title: "amq:" + session + ":cto"}}, nil
	}
	var prompts []string
	sendPromptToPane = func(_ string, prompt string) error { prompts = append(prompts, prompt); return nil }
	t.Cleanup(func() { statusPaneLister, sendPromptToPane = oldLister, oldSend })
	opts := goalDeliveryOptions{Project: tm.Project, Profile: team.DefaultProfile, Session: session, Role: "cto", Goal: plan.Goal, Team: tm, Member: tm.Members[0], Namespace: squadnamespace.Resolve(tm.Project, team.DefaultProfile, session), Mode: executionModeProjectLead, ResumeTransitionID: plan.TransitionID}
	result, err := executeGoalDelivery(opts)
	if err != nil {
		t.Fatalf("transition delivery: %v", err)
	}
	if result.DeliveryReceipt == nil || len(prompts) != 1 {
		t.Fatalf("delivery result=%+v prompts=%v", result, prompts)
	}
	transitionPath, _ := resumeGoalTransitionPath(tm.Project, team.DefaultProfile, session, plan.TransitionID)
	var tr resumeGoalTransitionRecord
	payload, _ := os.ReadFile(transitionPath)
	if err := json.Unmarshal(payload, &tr); err != nil || result.DeliveryReceipt.AttemptID != tr.NewAttemptID || !strings.Contains(prompts[0], "--attempt-id "+tr.NewAttemptID) {
		t.Fatalf("preallocated attempt mismatch: transition=%+v receipt=%+v prompts=%v err=%v", tr, result.DeliveryReceipt, prompts, err)
	}
	if _, err := executeGoalDelivery(opts); err == nil || !strings.Contains(err.Error(), "already consumed") {
		t.Fatalf("duplicate transition delivery accepted: %v", err)
	}
	entries, _ := os.ReadDir(goalAttemptDir(tm.Project, team.DefaultProfile, session))
	attempts := 0
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), ".") && strings.HasSuffix(entry.Name(), ".json") && !strings.HasSuffix(entry.Name(), ".claim.json") {
			attempts++
		}
	}
	if attempts != 2 {
		t.Fatalf("attempts=%d want original+one redelivery; entries=%v", attempts, entries)
	}
}

func TestResumeGoalTransitionRedeliversStructuredCodexPrompt(t *testing.T) {
	tm, session, _, plan, verified := freshResumeTransitionFixtureForBinary(t, "codex")
	if plan.BindingMode != "prompt_goal" || plan.BindingNative {
		t.Fatalf("Codex resume plan binding = %+v", plan)
	}
	if err := reserveResumeGoalTransition(tm, team.DefaultProfile, session, verified, plan); err != nil {
		t.Fatal(err)
	}
	oldLister, oldSend := statusPaneLister, sendPromptToPane
	statusPaneLister = func() ([]tmuxpane.TmuxPane, error) {
		return []tmuxpane.TmuxPane{{PaneID: "%447", CWD: tm.Project, Command: "codex", Title: "amq:" + session + ":cto"}}, nil
	}
	var prompt string
	sendPromptToPane = func(_ string, value string) error { prompt = value; return nil }
	t.Cleanup(func() { statusPaneLister, sendPromptToPane = oldLister, oldSend })
	opts := goalDeliveryOptions{Project: tm.Project, Profile: team.DefaultProfile, Session: session, Role: "cto", Goal: plan.Goal, Team: tm, Member: tm.Members[0], Namespace: squadnamespace.Resolve(tm.Project, team.DefaultProfile, session), Mode: executionModeProjectLead, ResumeTransitionID: plan.TransitionID}
	result, err := executeGoalDelivery(opts)
	if err != nil {
		t.Fatalf("Codex transition delivery: %v", err)
	}
	if result.Status != "prompt_goal_delivered" || result.DeliveryReceipt == nil {
		t.Fatalf("Codex transition result = %+v", result)
	}
	goal, attemptID, err := parseCodexGoalControlPrompt(prompt)
	if err != nil || goal != plan.Goal || attemptID != result.DeliveryReceipt.AttemptID {
		t.Fatalf("Codex redelivery prompt identity = goal %q attempt %q err %v; receipt=%+v", goal, attemptID, err, result.DeliveryReceipt)
	}
	if !strings.Contains(prompt, "--route prompt") || !strings.Contains(prompt, "ship literal --attempt-id fake\nwith \"quotes\"") {
		t.Fatalf("Codex resume prompt lost claim route or actual newline: %q", prompt)
	}
	rec, err := launch.Read(verified.Check.AgentDir)
	if err != nil || !exactGoalBinding(rec.GoalBinding, goalDeliveryContract{Binary: "codex", Mode: goalBindingModePrompt, ClaimRoute: goalClaimRoutePrompt}, plan.Goal, attemptID, prompt, "goal-control") {
		t.Fatalf("Codex resume binding = %+v err=%v", rec.GoalBinding, err)
	}
}

func TestResumeGoalTransitionContinuationReusesReservedAttemptAfterCrash(t *testing.T) {
	tm, session, plans, plan, verified := freshResumeTransitionFixture(t)
	if err := reserveResumeGoalTransition(tm, team.DefaultProfile, session, verified, plan); err != nil {
		t.Fatal(err)
	}
	transitionPath, err := resumeGoalTransitionPath(tm.Project, team.DefaultProfile, session, plan.TransitionID)
	if err != nil {
		t.Fatal(err)
	}
	var tr resumeGoalTransitionRecord
	transitionBytes, err := os.ReadFile(transitionPath)
	if err != nil || json.Unmarshal(transitionBytes, &tr) != nil {
		t.Fatalf("read transition: %v", err)
	}
	opts := goalDeliveryOptions{Project: tm.Project, Profile: team.DefaultProfile, Session: session, Role: "cto", Goal: plan.Goal, Team: tm, Member: tm.Members[0], Namespace: squadnamespace.Resolve(tm.Project, team.DefaultProfile, session), Mode: executionModeProjectLead, ResumeTransitionID: plan.TransitionID}
	if _, err := createGoalAttempt(opts, tr.NewAttemptID, time.Unix(501, 0).UTC()); err != nil {
		t.Fatal(err)
	}
	// Simulate a process crash after it has reserved the new binding but before
	// the durable transition completion marker. The recovery must neither reject
	// that legitimate generation nor manufacture a third attempt.
	rec, err := launch.Read(verified.Check.AgentDir)
	if err != nil {
		t.Fatal(err)
	}
	rec.GoalBinding = &launch.GoalBinding{Mode: "native_goal", NativeGoal: true, Source: "goal-control", Command: nativeGoalControlPrompt(plan.Goal, tm, team.DefaultProfile, session, "cto", tr.NewAttemptID), Detail: "reserved"}
	if err := launch.Write(verified.Check.AgentDir, rec); err != nil {
		t.Fatal(err)
	}
	blocked := buildResumeGoalPlan(tm, team.DefaultProfile, session, plans, false, false)
	if blocked.Eligible || blocked.Action != "continue" || blocked.RecoveryAttemptID != tr.NewAttemptID || !strings.Contains(blocked.RecoveryCommand, "--resume-transition "+plan.TransitionID) {
		t.Fatalf("reserved crash recovery plan = %+v", blocked)
	}
	oldLister, oldSend := statusPaneLister, sendPromptToPane
	var prompts []string
	statusPaneLister = func() ([]tmuxpane.TmuxPane, error) {
		return []tmuxpane.TmuxPane{{PaneID: "%447", CWD: tm.Project, Command: "codex"}}, nil
	}
	sendPromptToPane = func(_ string, prompt string) error { prompts = append(prompts, prompt); return nil }
	t.Cleanup(func() { statusPaneLister, sendPromptToPane = oldLister, oldSend })
	if _, err := executeGoalDelivery(opts); err != nil {
		t.Fatalf("continue reserved transition: %v", err)
	}
	if len(prompts) != 1 || !strings.Contains(prompts[0], "--attempt-id "+tr.NewAttemptID) {
		t.Fatalf("continuation prompt = %v", prompts)
	}
	if _, err := os.Stat(resumeGoalTransitionConsumedPath(transitionPath)); err != nil {
		t.Fatalf("continuation did not mark transition consumed: %v", err)
	}
	entries, err := os.ReadDir(goalAttemptDir(tm.Project, team.DefaultProfile, session))
	if err != nil {
		t.Fatal(err)
	}
	attempts := 0
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), ".") && strings.HasSuffix(entry.Name(), ".json") && !strings.HasSuffix(entry.Name(), ".claim.json") {
			attempts++
		}
	}
	if attempts != 2 {
		t.Fatalf("continuation created a third attempt: %v", entries)
	}
}

func TestResumeGoalTransitionConsumedRecoveryPlanUsesExactRetry(t *testing.T) {
	tm, session, plans, plan, verified := freshResumeTransitionFixture(t)
	if err := reserveResumeGoalTransition(tm, team.DefaultProfile, session, verified, plan); err != nil {
		t.Fatal(err)
	}
	transitionPath, err := resumeGoalTransitionPath(tm.Project, team.DefaultProfile, session, plan.TransitionID)
	if err != nil {
		t.Fatal(err)
	}
	var tr resumeGoalTransitionRecord
	transitionBytes, err := os.ReadFile(transitionPath)
	if err != nil || json.Unmarshal(transitionBytes, &tr) != nil {
		t.Fatalf("read transition: %v", err)
	}
	writeTestJSON(t, resumeGoalTransitionConsumedPath(transitionPath), resumeGoalTransitionConsumed{SchemaVersion: resumeGoalTransitionSchemaVersion, TransitionID: tr.TransitionID, NewAttemptID: tr.NewAttemptID, ConsumedAt: time.Unix(502, 0).UTC()})
	blocked := buildResumeGoalPlan(tm, team.DefaultProfile, session, plans, false, false)
	if blocked.Eligible || blocked.Action != "retry" || blocked.TransitionState != "consumed" || blocked.RecoveryAttemptID != tr.NewAttemptID || !strings.Contains(blocked.RecoveryCommand, "retry-attempt") || !strings.Contains(blocked.RecoveryCommand, "--attempt-id "+tr.NewAttemptID) {
		t.Fatalf("consumed crash recovery plan = %+v", blocked)
	}
}

func TestResumeGoalTransitionMismatchedRecoveryFailsClosed(t *testing.T) {
	tm, session, plans, plan, verified := freshResumeTransitionFixture(t)
	if err := reserveResumeGoalTransition(tm, team.DefaultProfile, session, verified, plan); err != nil {
		t.Fatal(err)
	}
	transitionPath, err := resumeGoalTransitionPath(tm.Project, team.DefaultProfile, session, plan.TransitionID)
	if err != nil {
		t.Fatal(err)
	}
	var tr resumeGoalTransitionRecord
	transitionBytes, err := os.ReadFile(transitionPath)
	if err != nil || json.Unmarshal(transitionBytes, &tr) != nil {
		t.Fatalf("read transition: %v", err)
	}
	tr.NewAttemptID = tr.OriginalAttemptID
	writeTestJSON(t, transitionPath, tr)
	blocked := buildResumeGoalPlan(tm, team.DefaultProfile, session, plans, false, false)
	if blocked.Eligible || blocked.TransitionState != "mismatched" || blocked.RecoveryCommand != "" {
		t.Fatalf("mismatched transition exposed recovery: %+v", blocked)
	}
}

func TestResumeGoalTransitionReservedBindingGenerationChangeFailsClosed(t *testing.T) {
	tm, session, _, plan, verified := freshResumeTransitionFixture(t)
	if err := reserveResumeGoalTransition(tm, team.DefaultProfile, session, verified, plan); err != nil {
		t.Fatal(err)
	}
	transitionPath, err := resumeGoalTransitionPath(tm.Project, team.DefaultProfile, session, plan.TransitionID)
	if err != nil {
		t.Fatal(err)
	}
	var tr resumeGoalTransitionRecord
	transitionBytes, err := os.ReadFile(transitionPath)
	if err != nil || json.Unmarshal(transitionBytes, &tr) != nil {
		t.Fatalf("read transition: %v", err)
	}
	opts := goalDeliveryOptions{Project: tm.Project, Profile: team.DefaultProfile, Session: session, Role: "cto", Goal: plan.Goal, Team: tm, Member: tm.Members[0], Namespace: squadnamespace.Resolve(tm.Project, team.DefaultProfile, session), Mode: executionModeProjectLead, ResumeTransitionID: plan.TransitionID}
	if _, err := createGoalAttempt(opts, tr.NewAttemptID, time.Unix(503, 0).UTC()); err != nil {
		t.Fatal(err)
	}
	rec, err := launch.Read(verified.Check.AgentDir)
	if err != nil {
		t.Fatal(err)
	}
	rec.GoalBinding = &launch.GoalBinding{Mode: "native_goal", NativeGoal: true, Source: "goal-control", Command: nativeGoalControlPrompt(plan.Goal, tm, team.DefaultProfile, session, "cto", tr.NewAttemptID), Detail: "reserved"}
	if err := launch.Write(verified.Check.AgentDir, rec); err != nil {
		t.Fatal(err)
	}
	if err := ensureResumeGoalTransitionBinding(opts, &tr, verified.Check.AgentDir); err != nil {
		t.Fatal(err)
	}
	rec.Model = "mutated-after-bound-marker"
	if err := launch.Write(verified.Check.AgentDir, rec); err != nil {
		t.Fatal(err)
	}
	oldSend := sendPromptToPane
	sendPromptToPane = func(string, string) error { t.Fatal("generation mismatch reached pane send"); return nil }
	t.Cleanup(func() { sendPromptToPane = oldSend })
	if _, err := executeGoalDelivery(opts); err == nil || !strings.Contains(err.Error(), "reserved launch binding generation changed") {
		t.Fatalf("reserved generation mutation was accepted: %v", err)
	}
}

func TestResumeGoalTransitionReleasesIdentityLocksBeforeBlockedSend(t *testing.T) {
	for _, kind := range []string{"launch", "team"} {
		t.Run(kind, func(t *testing.T) {
			tm, session, _, plan, verified := freshResumeTransitionFixture(t)
			if err := reserveResumeGoalTransition(tm, team.DefaultProfile, session, verified, plan); err != nil {
				t.Fatal(err)
			}
			oldSend, oldLister := sendPromptToPane, statusPaneLister
			writerDone := make(chan error, 1)
			sends := 0
			sendPromptToPane = func(string, string) error {
				sends++
				go func() {
					switch kind {
					case "launch":
						writerDone <- launch.WithRecordLock(verified.Check.AgentDir, func() error {
							rec, err := launch.Read(verified.Check.AgentDir)
							if err != nil {
								return err
							}
							rec.Model = "concurrent-launch-update"
							return launch.WriteUnderRecordLock(verified.Check.AgentDir, rec)
						})
					case "team":
						writerDone <- team.WithProfileLock(tm.Project, team.DefaultProfile, func() error {
							changed, err := team.ReadProfile(tm.Project, team.DefaultProfile)
							if err != nil {
								return err
							}
							changed.Members[0].Model = "concurrent-team-update"
							return team.WriteProfileUnderLock(tm.Project, team.DefaultProfile, changed)
						})
					}
				}()
				select {
				case err := <-writerDone:
					if err != nil {
						t.Fatalf("%s writer while send blocked: %v", kind, err)
					}
				case <-time.After(2 * time.Second):
					t.Fatalf("%s writer was blocked by transition external send", kind)
				}
				return nil
			}
			statusPaneLister = func() ([]tmuxpane.TmuxPane, error) {
				return []tmuxpane.TmuxPane{{PaneID: "%447", CWD: tm.Project, Command: "codex"}}, nil
			}
			t.Cleanup(func() {
				sendPromptToPane, statusPaneLister = oldSend, oldLister
			})
			opts := goalDeliveryOptions{Project: tm.Project, Profile: team.DefaultProfile, Session: session, Role: "cto", Goal: plan.Goal, Team: tm, Member: tm.Members[0], Namespace: squadnamespace.Resolve(tm.Project, team.DefaultProfile, session), Mode: executionModeProjectLead, ResumeTransitionID: plan.TransitionID}
			_, err := executeGoalDelivery(opts)
			if sends != 1 {
				t.Fatalf("kind=%s sends=%d", kind, sends)
			}
			if kind == "launch" {
				if err != nil {
					t.Fatalf("launch update should merge after transition send: %v", err)
				}
				rec, readErr := launch.Read(verified.Check.AgentDir)
				boundGoal := ""
				if rec.GoalBinding != nil {
					boundGoal, _, _ = parseGeneratedGoalBinding(rec.GoalBinding.Command)
				}
				if readErr != nil || rec.GoalBinding == nil || boundGoal != plan.Goal || rec.Model != "concurrent-launch-update" {
					t.Fatalf("serialized launch update lost binding: rec=%+v model=%q err=%v", rec.GoalBinding, rec.Model, readErr)
				}
			} else {
				if err == nil || !strings.Contains(err.Error(), "team generation changed after pane delivery") {
					t.Fatalf("team update must cause explicit stale-generation refusal, got %v", err)
				}
				changed, readErr := team.ReadProfile(tm.Project, team.DefaultProfile)
				if readErr != nil || changed.Members[0].Model != "concurrent-team-update" {
					t.Fatalf("serialized team update missing: model=%q err=%v", changed.Members[0].Model, readErr)
				}
			}
		})
	}
}

func TestResumeGoalTransitionFinalValidationReleasesLockBeforeBlockedSend(t *testing.T) {
	tm, session, _, plan, verified := freshResumeTransitionFixture(t)
	if err := reserveResumeGoalTransition(tm, team.DefaultProfile, session, verified, plan); err != nil {
		t.Fatal(err)
	}
	oldSend, oldLister := sendPromptToPane, statusPaneLister
	writerDone := make(chan error, 1)
	sends := 0
	sendPromptToPane = func(string, string) error {
		sends++
		go func() {
			writerDone <- launch.WithRecordLock(verified.Check.AgentDir, func() error {
				rec, err := launch.Read(verified.Check.AgentDir)
				if err != nil {
					return err
				}
				rec.Model = "concurrent-before-send"
				return launch.WriteUnderRecordLock(verified.Check.AgentDir, rec)
			})
		}()
		select {
		case err := <-writerDone:
			if err != nil {
				t.Fatalf("launch writer while send blocked: %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("launch writer was blocked by transition external send")
		}
		return nil
	}
	statusPaneLister = func() ([]tmuxpane.TmuxPane, error) {
		return []tmuxpane.TmuxPane{{PaneID: "%447", CWD: tm.Project, Command: "codex"}}, nil
	}
	t.Cleanup(func() {
		sendPromptToPane, statusPaneLister = oldSend, oldLister
	})
	opts := goalDeliveryOptions{Project: tm.Project, Profile: team.DefaultProfile, Session: session, Role: "cto", Goal: plan.Goal, Team: tm, Member: tm.Members[0], Namespace: squadnamespace.Resolve(tm.Project, team.DefaultProfile, session), Mode: executionModeProjectLead, ResumeTransitionID: plan.TransitionID}
	if _, err := executeGoalDelivery(opts); err != nil || sends != 1 {
		t.Fatalf("err=%v sends=%d", err, sends)
	}
	rec, readErr := launch.Read(verified.Check.AgentDir)
	boundGoal := ""
	if rec.GoalBinding != nil {
		boundGoal, _, _ = parseGeneratedGoalBinding(rec.GoalBinding.Command)
	}
	if readErr != nil || rec.Model != "concurrent-before-send" || rec.GoalBinding == nil || boundGoal != plan.Goal {
		t.Fatalf("serialized pre-send launch update lost binding: model=%q binding=%+v err=%v", rec.Model, rec.GoalBinding, readErr)
	}
}

func TestResumeGoalFailureGuidanceDoesNotRetrySettledDeliveryStates(t *testing.T) {
	tests := []struct {
		name, state string
		post        bool
	}{
		{name: "native queued", state: goalDeliveryStateNativeQueued},
		{name: "fallback sent", state: goalDeliveryStateFallbackSent},
		{name: "pane delivered", state: goalDeliveryStatePaneDelivered, post: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tm, session, _, plan, verified := freshResumeTransitionFixture(t)
			plan.Selected = true
			oldGoal := runStartGoalWithVersion
			runStartGoalWithVersion = func([]string, string) error {
				if tt.post {
					return &goalPostDeliveryBindingError{AttemptID: "exact-attempt", Cause: errors.New("post-delivery failure")}
				}
				return &goalDeliveryAttemptError{AttemptID: "exact-attempt", AttemptPath: "/exact/attempt.json", Sent: true, State: tt.state, Cause: errors.New("delivery failure")}
			}
			t.Cleanup(func() { runStartGoalWithVersion = oldGoal })
			err := deliverResumeGoalAfterLaunch(tm, team.DefaultProfile, session, []resumeExecLaunchResult{verified}, plan)
			if err == nil || !strings.Contains(err.Error(), "exact-attempt") || !strings.Contains(err.Error(), "DO NOT retry") || strings.Contains(err.Error(), "retry-attempt") {
				t.Fatalf("unsafe state guidance: %v", err)
			}
		})
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

func TestGoalRetryAttemptRequiresYesAndNeverCreatesAnotherAttempt(t *testing.T) {
	dir, base, _, prompts := setupGoalDeliveryFailureTest(t, nil)
	tm, err := team.ReadProfile(dir, team.DefaultProfile)
	if err != nil {
		t.Fatal(err)
	}
	const attemptID = "reserved-recovery"
	opts := goalDeliveryOptions{Project: dir, Profile: team.DefaultProfile, Session: "issue-96", Role: "cto", Goal: "recover safely", Team: tm, Member: tm.Members[0], Namespace: squadnamespace.Resolve(dir, team.DefaultProfile, "issue-96"), Mode: executionModeProjectLead}
	if _, err := createGoalAttempt(opts, attemptID, time.Unix(200, 0).UTC()); err != nil {
		t.Fatal(err)
	}
	agentDir := filepath.Join(base, "issue-96", "agents", "cto")
	rec, err := launch.Read(agentDir)
	if err != nil {
		t.Fatal(err)
	}
	contract, err := goalDeliveryContractForBinary(rec.Binary)
	if err != nil {
		t.Fatal(err)
	}
	prompt := contract.prompt(opts.Goal, tm, team.DefaultProfile, "issue-96", "cto", attemptID)
	rec.GoalBinding = contract.binding(opts.Goal, attemptID, prompt, "goal-control", "reserved")
	rec.Root = opts.Namespace.AMQRoot
	rec.TeamHome = dir
	rec.TeamProfile = team.DefaultProfile
	rec.StartedAt = time.Unix(199, 0).UTC()
	rec.BootstrapExpectation = &bootstrapack.Expectation{Required: true, LaunchID: "fresh-retry-launch"}
	if err := launch.Write(agentDir, rec); err != nil {
		t.Fatal(err)
	}
	args := []string{"retry-attempt", "--project", dir, "--session", "issue-96", "--role", "cto", "--attempt-id", attemptID}
	if err := runGoal(args); err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("retry without gate = %v", err)
	}
	if len(*prompts) != 0 {
		t.Fatalf("retry without --yes mutated pane: %v", *prompts)
	}
	if _, _, err := captureOutput(t, func() error { return runGoal(append(args, "--yes")) }); err != nil {
		t.Fatalf("same-attempt retry: %v", err)
	}
	if len(*prompts) != 1 || *prompts == nil || !strings.Contains((*prompts)[0], "--attempt-id "+attemptID) {
		t.Fatalf("retry prompt = %v", *prompts)
	}
	entries, err := os.ReadDir(goalAttemptDir(dir, team.DefaultProfile, "issue-96"))
	if err != nil {
		t.Fatal(err)
	}
	attemptRecords := 0
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".json") && !strings.HasSuffix(entry.Name(), ".claim.json") {
			attemptRecords++
		}
	}
	if attemptRecords != 1 {
		t.Fatalf("retry created another attempt: %v", entries)
	}
	if claimed, _, err := claimGoalAttempt(dir, team.DefaultProfile, "issue-96", attemptID, contract.ClaimRoute, time.Unix(201, 0).UTC()); err != nil || !claimed {
		t.Fatalf("claim recovery attempt: claimed=%t err=%v", claimed, err)
	}
	if _, _, err := captureOutput(t, func() error { return runGoal(append(args, "--yes")) }); err == nil || !strings.Contains(err.Error(), "already claimed") {
		t.Fatalf("claimed retry should refuse: %v", err)
	}
	if len(*prompts) != 1 {
		t.Fatalf("claimed retry sent again: %v", *prompts)
	}
}

func TestGoalRetryAttemptRejectsCorruptOrMismatchedPromptBindingBeforeMutation(t *testing.T) {
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
			dir, base, calls, prompts := setupGoalDeliveryFailureTest(t, nil)
			tm, err := team.ReadProfile(dir, team.DefaultProfile)
			if err != nil {
				t.Fatal(err)
			}
			const attemptID = "invalid-binding-retry"
			ns := squadnamespace.Resolve(dir, team.DefaultProfile, "issue-96")
			opts := goalDeliveryOptions{Project: dir, Profile: team.DefaultProfile, Session: "issue-96", Role: "cto", Goal: "retry exact", Team: tm, Member: tm.Members[0], Namespace: ns, Mode: executionModeProjectLead}
			attemptPath, err := createGoalAttempt(opts, attemptID, time.Unix(800, 0).UTC())
			if err != nil {
				t.Fatal(err)
			}
			agentDir := filepath.Join(base, "issue-96", "agents", "cto")
			rec, err := launch.Read(agentDir)
			if err != nil {
				t.Fatal(err)
			}
			contract, err := goalDeliveryContractForBinary(rec.Binary)
			if err != nil {
				t.Fatal(err)
			}
			rec.Root, rec.TeamHome, rec.TeamProfile = ns.AMQRoot, dir, team.DefaultProfile
			rec.StartedAt = time.Unix(799, 0).UTC()
			rec.BootstrapExpectation = &bootstrapack.Expectation{Required: true, LaunchID: "invalid-binding-retry-launch"}
			prompt := contract.prompt(opts.Goal, tm, team.DefaultProfile, "issue-96", "cto", attemptID)
			rec.GoalBinding = contract.binding(opts.Goal, attemptID, prompt, "goal-control", "reserved")
			tt.mutate(rec.GoalBinding)
			if err := launch.Write(agentDir, rec); err != nil {
				t.Fatal(err)
			}
			launchPath := launch.ExistingPath(agentDir)
			beforeLaunch, err := os.ReadFile(launchPath)
			if err != nil {
				t.Fatal(err)
			}
			beforeAttempt, err := os.ReadFile(attemptPath)
			if err != nil {
				t.Fatal(err)
			}

			_, _, err = captureOutput(t, func() error {
				return runGoal([]string{"retry-attempt", "--project", dir, "--session", "issue-96", "--role", "cto", "--attempt-id", attemptID, "--yes"})
			})
			if err == nil || !strings.Contains(err.Error(), "current lead binding does not match attempt") {
				t.Fatalf("invalid retry binding accepted: %v", err)
			}
			afterLaunch, launchErr := os.ReadFile(launchPath)
			afterAttempt, attemptErr := os.ReadFile(attemptPath)
			if launchErr != nil || attemptErr != nil || string(afterLaunch) != string(beforeLaunch) || string(afterAttempt) != string(beforeAttempt) || len(*prompts) != 0 || len(*calls) != 0 {
				t.Fatalf("retry mutated invalid binding: prompts=%v calls=%d launch_changed=%t attempt_changed=%t launch_err=%v attempt_err=%v", *prompts, len(*calls), string(afterLaunch) != string(beforeLaunch), string(afterAttempt) != string(beforeAttempt), launchErr, attemptErr)
			}
			if _, claimErr := os.Stat(goalAttemptClaimPath(attemptPath)); !os.IsNotExist(claimErr) {
				t.Fatalf("retry created claim for invalid binding: %v", claimErr)
			}
		})
	}
}

func TestGoalBindingReservationFailureReportsExactUnsentAttempt(t *testing.T) {
	dir, _, _, prompts := setupGoalDeliveryFailureTest(t, nil)
	oldWrite := goalLaunchWriteUnderRecordLock
	goalLaunchWriteUnderRecordLock = func(string, launch.Record) error { return errors.New("injected binding write failure") }
	t.Cleanup(func() { goalLaunchWriteUnderRecordLock = oldWrite })
	_, _, err := captureOutput(t, func() error {
		return runGoal([]string{"deliver", "--project", dir, "--session", "issue-96", "--role", "cto", "--goal", "durable ordering"})
	})
	var attemptErr *goalDeliveryAttemptError
	if !errors.As(err, &attemptErr) || attemptErr.AttemptID == "" || attemptErr.AttemptPath == "" || attemptErr.Sent {
		t.Fatalf("reservation failure lacks typed exact not-sent attempt: err=%v typed=%+v", err, attemptErr)
	}
	if _, statErr := os.Stat(attemptErr.AttemptPath); statErr != nil {
		t.Fatalf("typed attempt was not durable: %v", statErr)
	}
	if len(*prompts) != 0 {
		t.Fatalf("pane mutated after binding reservation failure: %v", *prompts)
	}
}

func TestGoalRetryAttemptQueuedAndFallbackReuseExactAttempt(t *testing.T) {
	tests := []struct {
		name          string
		sendErr       error
		wantFallbacks int
	}{
		{name: "queued", sendErr: &tmuxpane.QueuedInputError{PaneID: "%7"}},
		{name: "unconfirmed fallback", sendErr: &tmuxpane.SubmitUnconfirmedError{PaneID: "%7", Attempts: 3}, wantFallbacks: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir, base, calls, prompts := setupGoalDeliveryFailureTest(t, tt.sendErr)
			tm, err := team.ReadProfile(dir, team.DefaultProfile)
			if err != nil {
				t.Fatal(err)
			}
			const attemptID = "retry-same-attempt"
			ns := squadnamespace.Resolve(dir, team.DefaultProfile, "issue-96")
			opts := goalDeliveryOptions{Project: dir, Profile: team.DefaultProfile, Session: "issue-96", Role: "cto", Goal: "retry exact", Team: tm, Member: tm.Members[0], Namespace: ns, Mode: executionModeProjectLead}
			if _, err := createGoalAttempt(opts, attemptID, time.Unix(600, 0).UTC()); err != nil {
				t.Fatal(err)
			}
			agentDir := filepath.Join(base, "issue-96", "agents", "cto")
			rec, err := launch.Read(agentDir)
			if err != nil {
				t.Fatal(err)
			}
			rec.Root, rec.TeamHome, rec.TeamProfile = ns.AMQRoot, dir, team.DefaultProfile
			rec.StartedAt = time.Unix(599, 0).UTC()
			rec.BootstrapExpectation = &bootstrapack.Expectation{Required: true, LaunchID: "retry-queued-fallback"}
			contract, err := goalDeliveryContractForBinary(rec.Binary)
			if err != nil {
				t.Fatal(err)
			}
			prompt := contract.prompt(opts.Goal, tm, team.DefaultProfile, "issue-96", "cto", attemptID)
			rec.GoalBinding = contract.binding(opts.Goal, attemptID, prompt, "goal-control", "reserved")
			if err := launch.Write(agentDir, rec); err != nil {
				t.Fatal(err)
			}
			if _, _, err := captureOutput(t, func() error {
				return runGoal([]string{"retry-attempt", "--project", dir, "--session", "issue-96", "--role", "cto", "--attempt-id", attemptID, "--yes"})
			}); err != nil {
				t.Fatalf("retry path: %v", err)
			}
			if len(*prompts) != 1 || !strings.Contains((*prompts)[0], "--attempt-id "+attemptID) || len(*calls) != tt.wantFallbacks {
				t.Fatalf("retry prompts=%v fallbacks=%d want=%d", *prompts, len(*calls), tt.wantFallbacks)
			}
			if tt.wantFallbacks == 1 && !strings.Contains(strings.Join((*calls)[0].Arg, " "), attemptID) {
				t.Fatalf("fallback did not reuse exact attempt %s: %+v", attemptID, (*calls)[0].Arg)
			}
			entries, _ := os.ReadDir(goalAttemptDir(dir, team.DefaultProfile, "issue-96"))
			attempts := 0
			for _, entry := range entries {
				if !strings.HasPrefix(entry.Name(), ".") && strings.HasSuffix(entry.Name(), ".json") && !strings.HasSuffix(entry.Name(), ".claim.json") {
					attempts++
				}
			}
			if attempts != 1 {
				t.Fatalf("retry created another attempt: %v", entries)
			}
		})
	}
}

func TestGoalRetryAttemptReleasesIdentityLocksBeforeBlockedSend(t *testing.T) {
	dir, base, _, prompts := setupGoalDeliveryFailureTest(t, nil)
	tm, err := team.ReadProfile(dir, team.DefaultProfile)
	if err != nil {
		t.Fatal(err)
	}
	const attemptID = "retry-cas-attempt"
	ns := squadnamespace.Resolve(dir, team.DefaultProfile, "issue-96")
	opts := goalDeliveryOptions{Project: dir, Profile: team.DefaultProfile, Session: "issue-96", Role: "cto", Goal: "retry cas", Team: tm, Member: tm.Members[0], Namespace: ns, Mode: executionModeProjectLead}
	if _, err := createGoalAttempt(opts, attemptID, time.Unix(700, 0).UTC()); err != nil {
		t.Fatal(err)
	}
	agentDir := filepath.Join(base, "issue-96", "agents", "cto")
	rec, err := launch.Read(agentDir)
	if err != nil {
		t.Fatal(err)
	}
	rec.Root, rec.TeamHome, rec.TeamProfile = ns.AMQRoot, dir, team.DefaultProfile
	rec.StartedAt = time.Unix(699, 0).UTC()
	rec.BootstrapExpectation = &bootstrapack.Expectation{Required: true, LaunchID: "retry-cas-launch"}
	contract, err := goalDeliveryContractForBinary(rec.Binary)
	if err != nil {
		t.Fatal(err)
	}
	prompt := contract.prompt(opts.Goal, tm, team.DefaultProfile, "issue-96", "cto", attemptID)
	rec.GoalBinding = contract.binding(opts.Goal, attemptID, prompt, "goal-control", "reserved")
	if err := launch.Write(agentDir, rec); err != nil {
		t.Fatal(err)
	}
	oldSend := sendPromptToPane
	writerDone := make(chan error, 1)
	sendPromptToPane = func(paneID, prompt string) error {
		go func() {
			writerDone <- launch.WithRecordLock(agentDir, func() error {
				changed, err := launch.Read(agentDir)
				if err != nil {
					return err
				}
				changed.Model = "concurrent-retry-update"
				return launch.WriteUnderRecordLock(agentDir, changed)
			})
		}()
		select {
		case err := <-writerDone:
			if err != nil {
				t.Fatalf("retry writer while send blocked: %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("retry writer was blocked by external send")
		}
		return oldSend(paneID, prompt)
	}
	t.Cleanup(func() { sendPromptToPane = oldSend })
	_, _, err = captureOutput(t, func() error {
		return runGoal([]string{"retry-attempt", "--project", dir, "--session", "issue-96", "--role", "cto", "--attempt-id", attemptID, "--yes"})
	})
	if err == nil || !strings.Contains(err.Error(), "was sent but current identity/generation changed afterward") || len(*prompts) != 1 {
		t.Fatalf("retry must explicitly refuse stale post-send identity: err=%v prompts=%v", err, *prompts)
	}
	current, readErr := launch.Read(agentDir)
	if readErr != nil || current.Model != "concurrent-retry-update" || current.GoalBinding == nil || current.GoalBinding.Command != rec.GoalBinding.Command {
		t.Fatalf("serialized retry update lost binding: model=%q binding=%+v err=%v", current.Model, current.GoalBinding, readErr)
	}
}

func TestQueuedRedeliveryReservesSecondAttemptAndBlocksThird(t *testing.T) {
	dir, base, _, _ := setupGoalDeliveryFailureTest(t, &tmuxpane.QueuedInputError{PaneID: "%7"})
	tm, err := team.ReadProfile(dir, team.DefaultProfile)
	if err != nil {
		t.Fatal(err)
	}
	ns := squadnamespace.Resolve(dir, team.DefaultProfile, "issue-96")
	original := goalDeliveryOptions{Project: dir, Profile: team.DefaultProfile, Session: "issue-96", Role: "cto", Goal: "ship safely", Team: tm, Member: tm.Members[0], Namespace: ns, Mode: executionModeProjectLead}
	contract, err := goalDeliveryContractForBinary(tm.Members[0].Binary)
	if err != nil {
		t.Fatal(err)
	}
	const originalID = "original-claimed"
	if _, err := createGoalAttempt(original, originalID, time.Unix(300, 0).UTC()); err != nil {
		t.Fatal(err)
	}
	if claimed, _, err := claimGoalAttempt(dir, team.DefaultProfile, "issue-96", originalID, contract.ClaimRoute, time.Unix(301, 0).UTC()); err != nil || !claimed {
		t.Fatalf("claim original: %t %v", claimed, err)
	}
	if _, _, err := captureOutput(t, func() error {
		return runGoal([]string{"deliver", "--project", dir, "--session", "issue-96", "--role", "cto", "--goal", original.Goal})
	}); err != nil {
		t.Fatalf("queued redelivery: %v", err)
	}
	agentDir := filepath.Join(base, "issue-96", "agents", "cto")
	rec, err := launch.Read(agentDir)
	if err != nil {
		t.Fatal(err)
	}
	_, secondID, err := goalBindingPayload(rec.GoalBinding, contract)
	if err != nil || secondID == "" || secondID == originalID {
		t.Fatalf("reserved binding did not advance attempt: %q %v", secondID, err)
	}
	rec.Root = ns.AMQRoot
	rec.TeamHome = dir
	rec.TeamProfile = team.DefaultProfile
	rec.BootstrapExpectation = &bootstrapack.Expectation{Required: true}
	plan := buildResumeGoalPlan(tm, team.DefaultProfile, "issue-96", []resumePlan{{Role: "cto", Handle: "cto", Action: resumeRestore, RestoreRecord: &rec}}, false, false)
	if plan.Eligible || plan.Action != "blocked" || plan.OriginalAttemptID != secondID || plan.ClaimState != "unclaimed" {
		t.Fatalf("second queued attempt did not block third: %+v", plan)
	}
	entries, err := os.ReadDir(goalAttemptDir(dir, team.DefaultProfile, "issue-96"))
	if err != nil {
		t.Fatal(err)
	}
	attempts := 0
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".json") && !strings.HasSuffix(entry.Name(), ".claim.json") {
			attempts++
		}
	}
	if attempts != 2 {
		t.Fatalf("attempt count=%d want exactly 2; entries=%v", attempts, entries)
	}
}

func TestResumeExplicitRedeliveryRejectsPendingSecondAttemptBeforeMutation(t *testing.T) {
	tests := []struct {
		name       string
		sendErr    error
		claimRoute string
	}{
		{name: "queued prompt", sendErr: &tmuxpane.QueuedInputError{PaneID: "%7"}, claimRoute: "prompt"},
		{name: "durable fallback", sendErr: &tmuxpane.SubmitUnconfirmedError{PaneID: "%7", Attempts: 3}, claimRoute: "amq"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir, base, calls, prompts := setupGoalDeliveryFailureTest(t, tt.sendErr)
			if err := os.Symlink(base, filepath.Join(dir, ".agent-mail")); err != nil {
				t.Fatal(err)
			}
			tm, err := team.ReadProfile(dir, team.DefaultProfile)
			if err != nil {
				t.Fatal(err)
			}
			contract, err := goalDeliveryContractForBinary(tm.Members[0].Binary)
			if err != nil {
				t.Fatal(err)
			}
			ns := squadnamespace.Resolve(dir, team.DefaultProfile, "issue-96")
			original := goalDeliveryOptions{Project: dir, Profile: team.DefaultProfile, Session: "issue-96", Role: "cto", Goal: "ship safely", Team: tm, Member: tm.Members[0], Namespace: ns, Mode: executionModeProjectLead}
			const originalID = "settled-original"
			if _, err := createGoalAttempt(original, originalID, time.Unix(400, 0).UTC()); err != nil {
				t.Fatal(err)
			}
			if claimed, _, err := claimGoalAttempt(dir, team.DefaultProfile, "issue-96", originalID, tt.claimRoute, time.Unix(401, 0).UTC()); err != nil || !claimed {
				t.Fatalf("claim original: %t %v", claimed, err)
			}
			agentDir := filepath.Join(base, "issue-96", "agents", "cto")
			rec, err := launch.Read(agentDir)
			if err != nil {
				t.Fatal(err)
			}
			rec.Root = filepath.Join(base, "issue-96")
			rec.BaseRoot = base
			rec.TeamHome = dir
			rec.TeamProfile = team.DefaultProfile
			rec.SharedWorkstream = true
			rec.BootstrapExpectation = &bootstrapack.Expectation{Required: true, LaunchID: "fresh-resume-redelivery"}
			prompt := contract.prompt(original.Goal, tm, team.DefaultProfile, "issue-96", "cto", originalID)
			rec.GoalBinding = contract.binding(original.Goal, originalID, prompt, "goal-control", "delivered")
			goalPlan := buildResumeGoalPlan(tm, team.DefaultProfile, "issue-96", []resumePlan{{Role: "cto", Handle: "cto", Action: resumeRestore, HasRestoreRecord: true, RestoreRecord: &rec}}, false, false)
			if !goalPlan.Eligible {
				t.Fatalf("original settled plan ineligible: %+v", goalPlan)
			}
			goalPlan.Selected = true
			rec.StartedAt = time.Unix(402, 0).UTC()
			rec.Tmux = &launch.TmuxInfo{PaneID: "%7", Target: "current-window"}
			if err := launch.Write(agentDir, rec); err != nil {
				t.Fatal(err)
			}
			info, err := os.Stat(launch.ExistingPath(agentDir))
			if err != nil {
				t.Fatal(err)
			}
			oldInspector := statusPaneInspector
			statusPaneInspector = func(id string) (tmuxpane.TmuxPane, bool) {
				return tmuxpane.TmuxPane{PaneID: id, CWD: dir, Command: "codex"}, id == "%7"
			}
			t.Cleanup(func() { statusPaneInspector = oldInspector })
			// Assert the durable attempt and launch binding exist before either the
			// queued-prompt or unconfirmed/fallback send seam runs.
			underlyingSend := sendPromptToPane
			sendPromptToPane = func(paneID, prompt string) error {
				_, attemptID, parseErr := parseCodexGoalControlPrompt(prompt)
				if parseErr != nil {
					t.Fatalf("parse outgoing redelivery: %v", parseErr)
				}
				if _, statErr := os.Stat(mustGoalAttemptPath(t, dir, team.DefaultProfile, "issue-96", attemptID)); statErr != nil {
					t.Fatalf("attempt missing at send time: %v", statErr)
				}
				bound, readErr := launch.Read(agentDir)
				if readErr != nil || bound.GoalBinding == nil || bound.GoalBinding.Command != prompt {
					t.Fatalf("binding not durable at send time: rec=%+v err=%v", bound.GoalBinding, readErr)
				}
				return underlyingSend(paneID, prompt)
			}
			verified := resumeExecLaunchResult{Check: resumeExecLaunchCheck{Role: "cto", Handle: "cto", CWD: dir, AgentDir: agentDir, Workstream: "issue-96", Root: filepath.Join(base, "issue-96"), Binary: "codex", Profile: team.DefaultProfile}, State: resumeExecLaunchStateLaunched, RecordModTime: info.ModTime(), RecordStarted: rec.StartedAt}
			if _, _, err := captureOutput(t, func() error {
				return deliverResumeGoalAfterLaunch(tm, team.DefaultProfile, "issue-96", []resumeExecLaunchResult{verified}, goalPlan)
			}); err != nil {
				t.Fatalf("resume redelivery did not create pending second attempt: %v", err)
			}
			current, err := launch.Read(agentDir)
			if err != nil {
				t.Fatal(err)
			}
			_, secondID, err := goalBindingPayload(current.GoalBinding, contract)
			if err != nil || secondID == originalID {
				t.Fatalf("second binding = %q, %v", secondID, err)
			}
			// The crash/restart scenario has no surviving pane; keep the initial
			// delivery seam above, then make resume classify the lead as restore.
			statusPaneLister = func() ([]tmuxpane.TmuxPane, error) { return nil, nil }
			paneCalls, goalCalls := 0, 0
			oldTmux := runTmuxLaunchPlanForResume
			oldGoal := runStartGoalWithVersion
			runTmuxLaunchPlanForResume = func(tmuxLaunchPlan) error { paneCalls++; return nil }
			runStartGoalWithVersion = func([]string, string) error { goalCalls++; return nil }
			t.Cleanup(func() { runTmuxLaunchPlanForResume, runStartGoalWithVersion = oldTmux, oldGoal })
			beforePrompts, beforeFallbacks := len(*prompts), len(*calls)
			err = executeResume(resumeExecution{
				ProjectDir: dir, RequestedSession: "issue-96", ExplicitSession: true, Mode: resumeModeDefault,
				Profile: team.DefaultProfile, GoalRedelivery: true,
				Probe: duplicateLaunchProbe{PIDAlive: func(int) bool { return false }, ProcessMatch: func(int, func(string) bool) bool { return false }, Now: time.Now},
				Exec:  resumeExecOptions{Enabled: true, Terminal: "tmux", Target: "current-window", Layout: "vertical", RedeliverGoal: true, RedeliveryExplicit: true},
			})
			if err == nil || !strings.Contains(err.Error(), "--redeliver-goal is unavailable") || !strings.Contains(err.Error(), "claim is missing") {
				t.Fatalf("second resume request = %v", err)
			}
			if paneCalls != 0 || goalCalls != 0 || len(*prompts) != beforePrompts || len(*calls) != beforeFallbacks {
				t.Fatalf("ineligible resume mutated: pane=%d goal=%d prompts=%d/%d fallback=%d/%d", paneCalls, goalCalls, len(*prompts), beforePrompts, len(*calls), beforeFallbacks)
			}
			entries, err := os.ReadDir(goalAttemptDir(dir, team.DefaultProfile, "issue-96"))
			if err != nil {
				t.Fatal(err)
			}
			attempts := 0
			for _, entry := range entries {
				if !strings.HasPrefix(entry.Name(), ".") && strings.HasSuffix(entry.Name(), ".json") && !strings.HasSuffix(entry.Name(), ".claim.json") {
					attempts++
				}
			}
			if attempts != 2 {
				t.Fatalf("attempt count=%d want 2", attempts)
			}
			if claimed, _, err := claimGoalAttempt(dir, team.DefaultProfile, "issue-96", secondID, contract.ClaimRoute, time.Now().UTC()); err != nil || !claimed {
				t.Fatalf("pending second attempt is not actionable: claimed=%t err=%v", claimed, err)
			}
		})
	}
}
