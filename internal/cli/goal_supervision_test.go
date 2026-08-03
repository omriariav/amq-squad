package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/activity"
	"github.com/omriariav/amq-squad/v2/internal/launch"
	squadnamespace "github.com/omriariav/amq-squad/v2/internal/namespace"
	"github.com/omriariav/amq-squad/v2/internal/state"
	"github.com/omriariav/amq-squad/v2/internal/team"
	"github.com/omriariav/amq-squad/v2/internal/tmuxpane"
)

func eligibleGoalSupervisionInput() goalSupervisionAssessmentInput {
	observedAt := time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)
	return goalSupervisionAssessmentInput{
		ObservedAt: observedAt,
		Now:        observedAt.Add(time.Second),
		MaxAge:     30 * time.Second,
		Binding: GoalSupervisionBinding{
			Project: "/project", Profile: "default", Session: "release", NamespaceID: "default/release",
			LeadRole: "cto", LeadHandle: "cto", LaunchID: "launch-1",
			LaunchStartedAt: observedAt.Add(-time.Minute), LaunchRecordDigest: "launch-digest",
			LaunchRecordModTime: observedAt.Add(-time.Minute).UnixNano(),
			Runtime: GoalSupervisionRuntimeIdentity{
				Known: true, Live: true, PIDLive: true, PaneLive: true, PIDAlive: true, BinaryMatch: true,
			},
			Pane: GoalSupervisionPaneIdentity{
				Managed: true, Session: "amq", WindowID: "@1", WindowName: "cto",
				PaneID: "%1", Target: "new-window", BusyKnown: true,
			},
			Goal: GoalSupervisionGoalIdentity{
				Mode: "native_goal_blocked", NativeGoal: true, StateKnown: true, Verified: true, ContentExact: true,
				Source: "launch-record", DeliveryState: "blocked", GoalDigest: "goal-digest",
				AttemptID: "attempt-1", BindingDigest: "binding-digest", CommandDigest: "command-digest",
			},
			PauseGeneration:       "pause-generation",
			PreparedRunGeneration: "prepared-generation",
			PreparedRunDigest:     "prepared-digest",
			PreparedLaunchAttempt: "prepared-attempt",
			PreparedGoalNamespace: "default/release",
			PreparedGoalDigest:    "prepared-goal-digest",
		},
		Lifecycle: GoalSupervisionLifecycleEvidence{
			Known: true, Fresh: true, Source: "heartbeat-file", Phase: "goal_blocked",
		},
		Blocker: GoalSupervisionBlockerEvidence{
			ID: "blocker-1", Known: true, Resolved: true,
			Detail: "dependency complete", ResolutionDigest: "resolution-digest",
		},
		Gates:      GoalSupervisionGateEvidence{Known: true},
		LocalInput: GoalSupervisionLocalInputEvidence{Known: true},
		Policy:     team.GoalSupervisionPolicyStatus{Mode: team.GoalSupervisionSafeAuto, Revision: 2, Source: "profile"},
		Claim:      GoalSupervisionClaimProjection{Known: true, State: "none"},
		Budget:     GoalSupervisionBudgetEvidence{Known: true, Allowed: true},
	}
}

func TestAssessGoalSupervisionEligibilityTruthTable(t *testing.T) {
	tests := []struct {
		name      string
		mutate    func(*goalSupervisionAssessmentInput)
		state     GoalSupervisionState
		eligible  bool
		automatic bool
	}{
		{
			name: "safe auto eligible", state: GoalSupervisionNativeGoalPausedEligible,
			eligible: true, automatic: true,
		},
		{
			name: "manual eligible but confirmation required",
			mutate: func(in *goalSupervisionAssessmentInput) {
				in.Policy.Mode = team.GoalSupervisionManual
			},
			state: GoalSupervisionNativeGoalPausedEligible, eligible: true,
		},
		{
			name: "notify only eligible but never resumes",
			mutate: func(in *goalSupervisionAssessmentInput) {
				in.Policy.Mode = team.GoalSupervisionNotifyOnly
			},
			state: GoalSupervisionNativeGoalPausedEligible, eligible: true,
		},
		{
			name: "running",
			mutate: func(in *goalSupervisionAssessmentInput) {
				in.Binding.Goal.Mode = "native_goal"
			},
			state: GoalSupervisionRunning,
		},
		{
			name: "goal state unverified",
			mutate: func(in *goalSupervisionAssessmentInput) {
				in.Binding.Goal.StateKnown = false
				in.Binding.Goal.Verified = false
				in.Binding.Goal.ContentExact = false
			},
			state: GoalSupervisionNativeGoalBlockedUnknown,
		},
		{
			name: "parked waiting amq",
			mutate: func(in *goalSupervisionAssessmentInput) {
				in.Lifecycle.Phase = "parked_waiting_amq"
			},
			state: GoalSupervisionParkedWaitingAMQ,
		},
		{
			name: "goal terminal",
			mutate: func(in *goalSupervisionAssessmentInput) {
				in.Lifecycle.Phase = "goal_terminal"
			},
			state: GoalSupervisionGoalTerminal,
		},
		{
			name: "lead down",
			mutate: func(in *goalSupervisionAssessmentInput) {
				in.Binding.Runtime.Live = false
				in.Binding.Runtime.PIDLive = false
				in.Binding.Runtime.PaneLive = false
				in.Binding.Runtime.PIDAlive = false
				in.Binding.Runtime.BinaryMatch = false
			},
			state: GoalSupervisionLeadDown,
		},
		{
			name: "lead down ambiguous scope",
			mutate: func(in *goalSupervisionAssessmentInput) {
				in.Binding.Runtime.Live = false
				in.Binding.Runtime.PIDLive = false
				in.Binding.Runtime.PaneLive = false
				in.Binding.Runtime.PIDAlive = false
				in.Binding.Runtime.BinaryMatch = false
				in.Gates.Ambiguous = true
			},
			state: GoalSupervisionNativeGoalBlockedUnknown,
		},
		{
			name: "pane busy",
			mutate: func(in *goalSupervisionAssessmentInput) {
				in.Binding.Pane.Busy = true
			},
			state: GoalSupervisionPaneBusyOrUnverified,
		},
		{
			name: "pane idle unverified",
			mutate: func(in *goalSupervisionAssessmentInput) {
				in.Binding.Pane.BusyKnown = false
			},
			state: GoalSupervisionPaneBusyOrUnverified,
		},
		{
			name: "open gate requires human",
			mutate: func(in *goalSupervisionAssessmentInput) {
				in.Gates.Open = 1
			},
			state: GoalSupervisionNativeGoalBlockedHuman,
		},
		{
			name: "local input requires human",
			mutate: func(in *goalSupervisionAssessmentInput) {
				in.LocalInput = GoalSupervisionLocalInputEvidence{Known: true, Observed: true, Kind: "permission"}
			},
			state: GoalSupervisionNativeGoalBlockedHuman,
		},
		{
			name: "local input unknown",
			mutate: func(in *goalSupervisionAssessmentInput) {
				in.LocalInput = GoalSupervisionLocalInputEvidence{}
			},
			state: GoalSupervisionNativeGoalBlockedUnknown,
		},
		{
			name: "known unresolved blocker requires human",
			mutate: func(in *goalSupervisionAssessmentInput) {
				in.Blocker.Resolved = false
				in.Blocker.ResolutionDigest = ""
			},
			state: GoalSupervisionNativeGoalBlockedHuman,
		},
		{
			name: "unknown blocker",
			mutate: func(in *goalSupervisionAssessmentInput) {
				in.Blocker = GoalSupervisionBlockerEvidence{}
			},
			state: GoalSupervisionNativeGoalBlockedUnknown,
		},
		{
			name: "incomplete source",
			mutate: func(in *goalSupervisionAssessmentInput) {
				in.SourceErrors = []string{"launch source unavailable"}
			},
			state: GoalSupervisionNativeGoalBlockedUnknown,
		},
		{
			name: "ambiguous gate",
			mutate: func(in *goalSupervisionAssessmentInput) {
				in.Gates.Ambiguous = true
			},
			state: GoalSupervisionNativeGoalBlockedUnknown,
		},
		{
			name: "gate source unknown is not a clear zero",
			mutate: func(in *goalSupervisionAssessmentInput) {
				in.Gates = GoalSupervisionGateEvidence{}
			},
			state: GoalSupervisionNativeGoalBlockedUnknown,
		},
		{
			name: "invariant violation",
			mutate: func(in *goalSupervisionAssessmentInput) {
				in.InvariantErrors = []string{"lead identity mismatch"}
			},
			state: GoalSupervisionNativeGoalBlockedUnknown,
		},
		{
			name: "prior claim indeterminate",
			mutate: func(in *goalSupervisionAssessmentInput) {
				in.Claim.Known = false
				in.Claim.Indeterminate = true
				in.Claim.State = "unknown"
			},
			state: GoalSupervisionNativeGoalBlockedUnknown,
		},
		{
			name: "claim observation absent",
			mutate: func(in *goalSupervisionAssessmentInput) {
				in.Claim = GoalSupervisionClaimProjection{State: "unknown"}
			},
			state: GoalSupervisionNativeGoalBlockedUnknown,
		},
		{
			name: "retry budget exhausted",
			mutate: func(in *goalSupervisionAssessmentInput) {
				in.Budget.Allowed = false
			},
			state: GoalSupervisionNativeGoalBlockedUnknown,
		},
		{
			name: "retry budget unknown",
			mutate: func(in *goalSupervisionAssessmentInput) {
				in.Budget = GoalSupervisionBudgetEvidence{}
			},
			state: GoalSupervisionNativeGoalBlockedUnknown,
		},
		{
			name: "lifecycle stale",
			mutate: func(in *goalSupervisionAssessmentInput) {
				in.Lifecycle.Fresh = false
			},
			state: GoalSupervisionNativeGoalBlockedUnknown,
		},
		{
			name: "binding content mismatch",
			mutate: func(in *goalSupervisionAssessmentInput) {
				in.Binding.Goal.ContentExact = false
			},
			state: GoalSupervisionNativeGoalBlockedUnknown,
		},
		{
			name: "runtime missing pid identity",
			mutate: func(in *goalSupervisionAssessmentInput) {
				in.Binding.Runtime.PIDLive = false
			},
			state: GoalSupervisionPaneBusyOrUnverified,
		},
		{
			name: "stale assessment",
			mutate: func(in *goalSupervisionAssessmentInput) {
				in.Now = in.ObservedAt.Add(in.MaxAge + time.Nanosecond)
			},
			state: GoalSupervisionNativeGoalBlockedUnknown,
		},
		{
			name: "future observation",
			mutate: func(in *goalSupervisionAssessmentInput) {
				in.Now = in.ObservedAt.Add(-time.Nanosecond)
			},
			state: GoalSupervisionNativeGoalBlockedUnknown,
		},
		{
			name: "attempt identity missing",
			mutate: func(in *goalSupervisionAssessmentInput) {
				in.Binding.Goal.AttemptID = ""
			},
			state: GoalSupervisionNativeGoalBlockedUnknown,
		},
		{
			name: "prepared binding missing",
			mutate: func(in *goalSupervisionAssessmentInput) {
				in.Binding.PreparedRunGeneration = ""
			},
			state: GoalSupervisionNativeGoalBlockedUnknown,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			in := eligibleGoalSupervisionInput()
			if tc.mutate != nil {
				tc.mutate(&in)
			}
			got := assessGoalSupervision(in)
			if got.State != tc.state || got.Eligible != tc.eligible || got.AutomaticResumeAllowed != tc.automatic {
				t.Fatalf("assessment = state %q eligible=%t automatic=%t, want %q/%t/%t; reasons=%+v",
					got.State, got.Eligible, got.AutomaticResumeAllowed,
					tc.state, tc.eligible, tc.automatic, got.Reasons)
			}
			if tc.name == "manual eligible but confirmation required" &&
				(got.Actions.Resume.Available || !got.Actions.Resume.NeedsConfirmation ||
					got.Actions.Resume.Fingerprint != got.Fingerprint ||
					got.Actions.Resume.AttemptID != got.Binding.Goal.AttemptID ||
					got.Actions.Resume.Confirmation == "") {
				t.Fatalf("manual resume action = %+v, want unavailable PR5 action with exact confirmation binding", got.Actions.Resume)
			}
			if tc.name == "notify only eligible but never resumes" && got.Actions.Resume.Available {
				t.Fatalf("notify-only resume action = %+v, want unavailable", got.Actions.Resume)
			}
			if tc.name == "lead down" && !got.Actions.Restore.Available {
				t.Fatalf("exact lead-down restore action = %+v, want available", got.Actions.Restore)
			}
			if tc.name == "lead down ambiguous scope" && got.Actions.Restore.Available {
				t.Fatalf("ambiguous lead-down restore action = %+v, want unavailable", got.Actions.Restore)
			}
		})
	}
}

func TestAssessGoalSupervisionEligibilityReasonsAreExplicitAndOrdered(t *testing.T) {
	got := assessGoalSupervision(eligibleGoalSupervisionInput())
	var codes []string
	for _, reason := range got.Reasons {
		if !reason.Passed {
			t.Fatalf("eligible reason %q failed: %+v", reason.Code, reason)
		}
		codes = append(codes, reason.Code)
	}
	want := []string{
		"fresh_assessment", "sources_complete", "exact_namespace", "exact_lead_identity",
		"lifecycle_known", "resumable_lifecycle", "native_goal_paused", "goal_binding_content",
		"goal_attempt", "launch_generation", "prepared_run_binding", "pause_generation",
		"runtime_identity", "pane_identity", "pane_idle", "blocker_known", "blocker_resolved",
		"gates_known", "no_open_gate", "no_gate_ambiguity", "local_input_known",
		"no_local_input", "invariants_ok", "claim_known", "claim_clear",
		"retry_budget_known", "retry_budget",
	}
	if !reflect.DeepEqual(gotCodes(got.Reasons), want) {
		t.Fatalf("reason codes = %v, want %v", codes, want)
	}
}

func TestGoalSupervisionManagedTmuxTargetRequiresCanonicalManagedTarget(t *testing.T) {
	tests := []struct {
		target string
		want   bool
	}{
		{target: "current-window", want: true},
		{target: "new-window", want: true},
		{target: "new-session", want: true},
		{target: ""},
		{target: "adopted"},
		{target: "external"},
		{target: "unknown"},
		{target: " current-window"},
		{target: "current-window "},
	}
	for _, tc := range tests {
		t.Run(tc.target, func(t *testing.T) {
			if got := goalSupervisionManagedTmuxTarget(tc.target); got != tc.want {
				t.Fatalf("goalSupervisionManagedTmuxTarget(%q) = %t, want %t", tc.target, got, tc.want)
			}

			input := eligibleGoalSupervisionInput()
			input.Binding.Pane.Target = tc.target
			// Deliberately leave the projected bit true: restore must enforce
			// the canonical target itself rather than trust a stale projection.
			input.Binding.Pane.Managed = true
			input.Binding.Runtime.Live = false
			input.Binding.Runtime.PIDLive = false
			input.Binding.Runtime.PaneLive = false
			input.Binding.Runtime.PIDAlive = false
			input.Binding.Runtime.BinaryMatch = false
			assessment := assessGoalSupervision(input)
			if assessment.Actions.Restore.Available != tc.want {
				t.Fatalf("lead-down restore for target %q available = %t, want %t: %+v",
					tc.target, assessment.Actions.Restore.Available, tc.want, assessment.Actions.Restore)
			}
		})
	}

	t.Run("managed projection is an independent restore veto", func(t *testing.T) {
		input := eligibleGoalSupervisionInput()
		input.Binding.Pane.Target = "new-window"
		input.Binding.Pane.Managed = false
		input.Binding.Runtime.Live = false
		input.Binding.Runtime.PIDLive = false
		input.Binding.Runtime.PaneLive = false
		input.Binding.Runtime.PIDAlive = false
		input.Binding.Runtime.BinaryMatch = false
		assessment := assessGoalSupervision(input)
		if assessment.State != GoalSupervisionLeadDown ||
			assessment.Actions.Restore.Available {
			t.Fatalf("unmanaged lead-down restore was not refused: state=%q action=%+v",
				assessment.State, assessment.Actions.Restore)
		}
	})
}

func TestGoalSupervisionExactPaneIdentityRequiresAllStableIDsAndTitle(t *testing.T) {
	recorded := &launch.TmuxInfo{
		Session: "squad", WindowID: "@1", PaneID: "%1", Target: "new-window",
	}
	valid := tmuxpane.TmuxPane{
		Session: "squad", WindowID: "@1", PaneID: "%1",
		Title: paneTitleToken("release", "cto"),
	}
	if !goalSupervisionExactPaneIdentity(valid, recorded, "release", "cto") {
		t.Fatal("exact pane identity was rejected")
	}
	tests := []struct {
		name   string
		mutate func(*tmuxpane.TmuxPane)
	}{
		{name: "pane", mutate: func(p *tmuxpane.TmuxPane) { p.PaneID = "%2" }},
		{name: "window", mutate: func(p *tmuxpane.TmuxPane) { p.WindowID = "@2" }},
		{name: "session", mutate: func(p *tmuxpane.TmuxPane) { p.Session = "other" }},
		{name: "title", mutate: func(p *tmuxpane.TmuxPane) { p.Title = "amq:release:other" }},
		{name: "empty title", mutate: func(p *tmuxpane.TmuxPane) { p.Title = "" }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := valid
			tc.mutate(&got)
			if goalSupervisionExactPaneIdentity(got, recorded, "release", "cto") {
				t.Fatalf("mismatched %s identity was accepted: %+v", tc.name, got)
			}
		})
	}
}

func TestVerifyGoalSupervisionBlockedBindingContentsIsExact(t *testing.T) {
	member := team.Member{Role: "cto", Binary: "claude", Handle: "cto"}
	tm := team.Team{
		Project: "/project", Orchestrated: true, Lead: "cto",
		ExecutionMode: executionModeProjectLead, Members: []team.Member{member},
	}
	goal := "ship the accepted run"
	attemptID := strings.Repeat("a", 32)
	command := nativeGoalControlPrompt(
		goal, tm, team.DefaultProfile, "release", member.Role, attemptID,
	)
	valid := launch.GoalBinding{
		Mode: "native_goal_blocked", NativeGoal: true,
		Source: "goal-runtime", DeliveryState: "blocked",
		Goal: goal, AttemptID: attemptID, Command: command,
	}
	if gotGoal, gotAttempt, err := verifyGoalSupervisionBlockedBindingContents(
		tm, team.DefaultProfile, "release", member, &valid,
	); err != nil || gotGoal != goal || gotAttempt != attemptID {
		t.Fatalf("valid blocked binding = goal %q attempt %q err %v", gotGoal, gotAttempt, err)
	}
	tests := []struct {
		name   string
		mutate func(*launch.GoalBinding)
	}{
		{name: "typed goal", mutate: func(b *launch.GoalBinding) { b.Goal = "different" }},
		{name: "typed attempt", mutate: func(b *launch.GoalBinding) { b.AttemptID = strings.Repeat("b", 32) }},
		{name: "command", mutate: func(b *launch.GoalBinding) { b.Command = strings.Replace(b.Command, "release", "other", 1) }},
		{name: "source", mutate: func(b *launch.GoalBinding) { b.Source = "launch-record" }},
		{name: "delivery", mutate: func(b *launch.GoalBinding) { b.DeliveryState = "delivered" }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := valid
			tc.mutate(&got)
			if _, _, err := verifyGoalSupervisionBlockedBindingContents(
				tm, team.DefaultProfile, "release", member, &got,
			); err == nil {
				t.Fatalf("mismatched %s binding was accepted: %+v", tc.name, got)
			}
		})
	}
}

func TestBuildGoalSupervisionAssessmentContentVerificationDoesNotOverrideStatusVeto(t *testing.T) {
	project := t.TempDir()
	now := time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)
	session := "release"
	ns := squadnamespace.Resolve(project, team.DefaultProfile, session)
	member := team.Member{Role: "cto", Handle: "cto", Binary: "codex"}
	tm := team.Team{
		Project: project, Orchestrated: true, Lead: member.Role,
		ExecutionMode: executionModeProjectLead,
		Members:       []team.Member{member},
	}
	agentDir := filepath.Join(ns.AMQRoot, "agents", member.Handle)
	rec := launch.Record{
		CWD: project, Binary: member.Binary, Handle: member.Handle, Role: member.Role,
		Session: session, TeamProfile: team.DefaultProfile, AgentPID: 4242, StartedAt: now.Add(-time.Minute),
		GoalBinding: &launch.GoalBinding{
			Mode: "native_goal_blocked", NativeGoal: true, Source: "goal-runtime",
			DeliveryState: "blocked", Goal: "ship", AttemptID: "attempt-1",
			Command: "synthetic exact command", Detail: "waiting",
		},
	}
	if err := launch.Write(agentDir, rec); err != nil {
		t.Fatal(err)
	}
	stored, err := launch.Read(agentDir)
	if err != nil {
		t.Fatal(err)
	}
	rows := []statusRecord{{
		Role: member.Role, Handle: member.Handle, Binary: member.Binary,
		Session: session, Namespace: ns, AgentDir: agentDir,
		// A refused managed identity is stale, so status must veto the
		// otherwise content-exact blocked binding.
		Status:           statusStateStale,
		LiveIdentityMode: "managed_refused",
		goalBinding:      stored.GoalBinding,
		liveness: agentLiveness{
			LaunchFound: true, LaunchRecord: stored,
		},
		Activity: &activity.Snapshot{
			Source: activity.SourceHeartbeat, WrittenAt: now.Add(-time.Second),
			Phase: "goal_blocked",
		},
	}}
	if binding := goalBindingForStatus(
		ns, newSessionStatusContext(tm, team.DefaultProfile, session, ""), rows,
	); binding.Verified {
		t.Fatalf("stale/refused status unexpectedly verified binding: %+v", binding)
	}

	previousVerifier := goalSupervisionBlockedBindingVerifier
	goalSupervisionBlockedBindingVerifier = func(
		team.Team, string, string, team.Member, launch.Record,
	) (string, string, error) {
		return "ship", "attempt-1", nil
	}
	t.Cleanup(func() { goalSupervisionBlockedBindingVerifier = previousVerifier })

	assessment := buildGoalSupervisionAssessment(
		tm, team.DefaultProfile, session, ns, rows,
		goalSupervisionGateObservation{Evidence: GoalSupervisionGateEvidence{Known: true}},
		nil, nil,
		duplicateLaunchProbe{
			PIDAlive:         func(int) bool { return false },
			ProcessMatch:     func(int, func(string) bool) bool { return false },
			ProcessTTY:       func(int) (string, bool) { return "", false },
			ProcessStartTime: func(int) (time.Time, bool) { return time.Time{}, false },
			Now:              func() time.Time { return now },
		},
		now,
	)
	if assessment.Binding.Goal.StateKnown ||
		assessment.Binding.Goal.Verified ||
		assessment.Binding.Goal.ContentExact ||
		assessment.Eligible {
		t.Fatalf("content verification overrode status veto: %+v", assessment.Binding.Goal)
	}
}

func TestVerifyGoalSupervisionPreparedGoalRejectsGoalOrGenerationDrift(t *testing.T) {
	generation := strings.Repeat("a", 32)
	launchAttempt := strings.Repeat("b", 32)
	manifestDigest := "sha256:manifest"
	goalDigest := "sha256:goal"
	manifest := preparedRunManifest{
		Generation: generation, Profile: team.DefaultProfile,
		Session: "release", Namespace: "default/release", GoalText: "ship",
		GoalNamespace: "default/release", GoalDigest: goalDigest,
	}
	rec := launch.Record{
		PreparedRunGeneration: generation, PreparedRunDigest: manifestDigest,
		PreparedRunGoalNamespace: "default/release", PreparedRunGoalDigest: goalDigest,
		PreparedRunLaunchAttempt: launchAttempt,
	}
	if err := verifyGoalSupervisionPreparedGoal(
		"ship", team.DefaultProfile, "release", rec, manifest, manifestDigest,
	); err != nil {
		t.Fatalf("exact prepared goal rejected: %v", err)
	}
	changedGoal := manifest
	changedGoal.GoalText = "different"
	if err := verifyGoalSupervisionPreparedGoal(
		"ship", team.DefaultProfile, "release", rec, changedGoal, manifestDigest,
	); err == nil {
		t.Fatal("prepared goal text drift was accepted")
	}
	changedGeneration := rec
	changedGeneration.PreparedRunGeneration = strings.Repeat("c", 32)
	if err := verifyGoalSupervisionPreparedGoal(
		"ship", team.DefaultProfile, "release", changedGeneration, manifest, manifestDigest,
	); err == nil {
		t.Fatal("prepared generation drift was accepted")
	}
}

func TestGoalSupervisionLifecycleObservationFailsClosed(t *testing.T) {
	now := time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)
	fresh := &activity.Snapshot{
		Source: activity.SourceHeartbeat, WrittenAt: now.Add(-time.Second),
		Phase: "parked_waiting_amq",
	}
	if got := goalSupervisionLifecycleObservation(fresh, now); !got.Known || !got.Fresh || got.Phase != fresh.Phase {
		t.Fatalf("fresh lifecycle = %+v", got)
	}
	for name, snapshot := range map[string]*activity.Snapshot{
		"absent": nil,
		"stale": {
			Source:    activity.SourceHeartbeat,
			WrittenAt: now.Add(-activity.DefaultStaleAfter - time.Nanosecond),
		},
		"future": {
			Source:    activity.SourceHeartbeat,
			WrittenAt: now.Add(time.Nanosecond),
		},
		"missing phase": {
			Source:    activity.SourceHeartbeat,
			WrittenAt: now.Add(-time.Second),
			Phase:     "  ",
		},
		"unrecognized phase": {
			Source:    activity.SourceHeartbeat,
			WrittenAt: now.Add(-time.Second),
			Phase:     "testing",
		},
		"task store": {
			Source:    activity.SourceTaskStore,
			WrittenAt: now.Add(-time.Second),
		},
	} {
		t.Run(name, func(t *testing.T) {
			if got := goalSupervisionLifecycleObservation(snapshot, now); got.Known || got.Fresh {
				t.Fatalf("%s lifecycle = %+v, want unknown", name, got)
			}
		})
	}
}

func TestGoalSupervisionResumableLifecycleRequiresGoalBlockedPhase(t *testing.T) {
	eligible := assessGoalSupervision(eligibleGoalSupervisionInput())
	if !goalSupervisionReasonPassed(eligible.Reasons, "resumable_lifecycle") {
		t.Fatalf("goal_blocked lifecycle was not resumable: %+v", eligible.Reasons)
	}

	for _, phase := range []string{"parked_waiting_amq", "goal_terminal", "testing"} {
		t.Run(phase, func(t *testing.T) {
			input := eligibleGoalSupervisionInput()
			// Exercise the assessment boundary directly. The nonterminal
			// "testing" row distinguishes the goal_blocked whitelist from the
			// former parked/terminal blacklist.
			input.Lifecycle = GoalSupervisionLifecycleEvidence{
				Known: true, Fresh: true, Source: activity.SourceHeartbeat, Phase: phase,
			}
			assessment := assessGoalSupervision(input)
			if goalSupervisionReasonPassed(assessment.Reasons, "resumable_lifecycle") {
				t.Fatalf("phase %q passed resumable_lifecycle: %+v", phase, assessment.Reasons)
			}
		})
	}
}

func TestGoalSupervisionMutatingActionsBindFingerprintAndAttempt(t *testing.T) {
	// UPDATED FOR #498 U1/F4, and the answer came from the source rather than from preference. This test
	// asserted NeedsConfirmation=true and Available=false for the resume action. goal_supervision.go:469-479
	// states that those two values WERE PLACEHOLDERS "for a supervisor surface that did not exist" and that
	// the surface exists now, so the metadata states the truth instead:
	//   Available          mirrors the assessment's own conclusion (AutomaticResumeAllowed);
	//   NeedsConfirmation  false ONLY under safe_auto, where operator consent is already recorded in policy.
	// eligibleGoalSupervisionInput uses safe_auto (goal_supervision_test.go:40), so the ratified values for
	// this fixture are Available=true and NeedsConfirmation=false. The old assertions pinned the placeholders
	// that U1/F4 explicitly replaced, which is why they failed the first honest compile.
	got := assessGoalSupervision(eligibleGoalSupervisionInput())

	// RESTORE is unconditionally NeedsConfirmation=true (goal_supervision.go:459) -- the no-confirmation
	// ruling is scoped to the resume action under safe_auto, NOT global. Checked separately from resume for
	// exactly that reason: folding them into one loop is what made this test assert a global invariant that
	// the contract never had.
	if r := got.Actions.Restore; !r.Mutates || !r.NeedsConfirmation ||
		r.Fingerprint != got.Fingerprint ||
		r.AttemptID != got.Binding.Goal.AttemptID ||
		r.Confirmation == "" {
		t.Fatalf("restore action lacks exact mutation binding: %+v", got.Actions.Restore)
	}

	// RESUME: the bindings that are unconditional stay unconditional; the two policy-derived fields are
	// asserted against the POLICY rather than against a frozen literal, so this row cannot go stale again the
	// next time a policy mode is added.
	resume := got.Actions.Resume
	wantConfirmation := got.Policy.Mode != team.GoalSupervisionSafeAuto
	if !resume.Mutates ||
		resume.Fingerprint != got.Fingerprint ||
		resume.AttemptID != got.Binding.Goal.AttemptID ||
		resume.Confirmation == "" {
		t.Fatalf("resume action lacks exact mutation binding: %+v", resume)
	}
	if resume.NeedsConfirmation != wantConfirmation {
		t.Fatalf("resume NeedsConfirmation=%t, want %t for policy mode %q: the no-confirmation ruling is "+
			"scoped to safe_auto, where the operator's consent is already recorded in policy",
			resume.NeedsConfirmation, wantConfirmation, got.Policy.Mode)
	}
	if resume.Available != got.AutomaticResumeAllowed {
		t.Fatalf("resume Available=%t but the assessment's AutomaticResumeAllowed=%t: the published action "+
			"must mirror the assessment's own conclusion, or a caller is invited to invoke something the "+
			"assessment refuses", resume.Available, got.AutomaticResumeAllowed)
	}
	// STILL UNCONDITIONAL, and still the point of the test: the published command is a BOUND invocation.
	if !strings.Contains(resume.Command, got.Binding.Goal.AttemptID) ||
		!strings.Contains(resume.Command, got.Fingerprint) {
		t.Fatalf("resume action is not safely scoped to its attempt and fingerprint: %+v", resume)
	}

	// THE UNBOUND CASE: no attempt id, so the assessment is ineligible and NEITHER action may be available.
	// Split for the same reason as above -- this loop would have failed identically once the first Fatalf
	// stopped short-circuiting it, so fixing only the first half would have moved the failure rather than
	// removed it. I checked that rather than assuming the one reported failure was the only one.
	missingAttempt := eligibleGoalSupervisionInput()
	missingAttempt.Binding.Goal.AttemptID = ""
	unbound := assessGoalSupervision(missingAttempt)

	if r := unbound.Actions.Restore; !r.Mutates || !r.NeedsConfirmation || r.Available ||
		r.Fingerprint != unbound.Fingerprint || r.Confirmation == "" {
		t.Fatalf("restore unbound action misstates mutation semantics: %+v", r)
	}

	ur := unbound.Actions.Resume
	// UNAVAILABILITY is the invariant that matters here and it is NOT policy-derived: an ineligible
	// assessment must never publish an available mutating action, whatever the policy says.
	if !ur.Mutates || ur.Available ||
		ur.Fingerprint != unbound.Fingerprint || ur.Confirmation == "" {
		t.Fatalf("resume unbound action misstates mutation semantics: %+v", ur)
	}
	// NeedsConfirmation stays keyed on Policy.Mode ALONE, deliberately: goal_supervision.go:505-508 notes that
	// folding eligibility into it would flip an ineligible actor under safe_auto back to needing confirmation,
	// misreporting the operator's standing consent as absent because of an unrelated eligibility fact.
	if want := unbound.Policy.Mode != team.GoalSupervisionSafeAuto; ur.NeedsConfirmation != want {
		t.Fatalf("resume unbound NeedsConfirmation=%t, want %t for policy mode %q: consent is a question "+
			"about policy, not about eligibility", ur.NeedsConfirmation, want, unbound.Policy.Mode)
	}
}

func TestGoalSupervisionFingerprintIgnoresObservationTimeButBindsAttempt(t *testing.T) {
	firstInput := eligibleGoalSupervisionInput()
	first := assessGoalSupervision(firstInput)

	secondInput := eligibleGoalSupervisionInput()
	secondInput.ObservedAt = secondInput.ObservedAt.Add(10 * time.Second)
	secondInput.Now = secondInput.Now.Add(10 * time.Second)
	second := assessGoalSupervision(secondInput)
	if first.Fingerprint != second.Fingerprint {
		t.Fatalf("fresh observations changed fingerprint: %q != %q", first.Fingerprint, second.Fingerprint)
	}

	secondInput.Binding.Goal.AttemptID = "attempt-2"
	changed := assessGoalSupervision(secondInput)
	if first.Fingerprint == changed.Fingerprint {
		t.Fatalf("attempt change did not change fingerprint %q", first.Fingerprint)
	}
}

func TestGoalSupervisionPauseGenerationIgnoresUnrelatedLaunchRecordDrift(t *testing.T) {
	binding := eligibleGoalSupervisionInput().Binding
	first := goalSupervisionPauseGeneration(binding)

	unrelatedDrift := binding
	unrelatedDrift.LaunchRecordDigest = "different-launch-record-digest"
	unrelatedDrift.LaunchRecordModTime++
	if got := goalSupervisionPauseGeneration(unrelatedDrift); got != first {
		t.Fatalf("unrelated launch-record drift rotated pause generation: %q != %q", got, first)
	}

	for name, mutate := range map[string]func(*GoalSupervisionBinding){
		"launch id": func(b *GoalSupervisionBinding) {
			b.LaunchID = "different-launch"
		},
		"binding digest": func(b *GoalSupervisionBinding) {
			b.Goal.BindingDigest = "different-binding"
		},
		"attempt id": func(b *GoalSupervisionBinding) {
			b.Goal.AttemptID = "different-attempt"
		},
		"mode": func(b *GoalSupervisionBinding) {
			b.Goal.Mode = "different-mode"
		},
	} {
		t.Run(name, func(t *testing.T) {
			changed := binding
			mutate(&changed)
			if got := goalSupervisionPauseGeneration(changed); got == first {
				t.Fatalf("%s did not rotate pause generation %q", name, got)
			}
		})
	}
}

func TestGoalSupervisionProjectionJSONConsistentAcrossStatusBoardDoctor(t *testing.T) {
	assessment := assessGoalSupervision(eligibleGoalSupervisionInput())
	surfaces := []struct {
		name  string
		value any
	}{
		{name: "status", value: statusEnvelopeData{GoalSupervision: &assessment}},
		{name: "board", value: sessionBoardRow{GoalSupervision: &assessment}},
		{name: "doctor", value: doctorCheck{Kind: "goal_supervision", GoalSupervision: &assessment}},
	}
	var canonical json.RawMessage
	for _, surface := range surfaces {
		payload, err := json.Marshal(surface.value)
		if err != nil {
			t.Fatalf("%s marshal: %v", surface.name, err)
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(payload, &fields); err != nil {
			t.Fatalf("%s decode: %v", surface.name, err)
		}
		got := fields["goal_supervision"]
		if len(got) == 0 {
			t.Fatalf("%s omitted goal_supervision: %s", surface.name, payload)
		}
		if canonical == nil {
			canonical = append(json.RawMessage(nil), got...)
			continue
		}
		if !bytes.Equal(canonical, got) {
			t.Fatalf("%s projection differs:\n%s\n%s", surface.name, canonical, got)
		}
	}
}

func TestDoctorGoalSupervisionWarnsWhenAssessmentCannotResolve(t *testing.T) {
	check := doctorCheckGoalSupervision(defaultDoctorExecution(t.TempDir()), "")
	if check.Status != doctorWarn || check.GoalSupervision != nil ||
		!strings.Contains(check.Detail, "assessment is unavailable") {
		t.Fatalf("missing-workstream goal supervision check = %+v, want explicit warning", check)
	}
}

func TestInspectGoalSupervisionGatesReadsDurableOpenGate(t *testing.T) {
	project, base, _ := seedNotifyProject(t, team.DefaultOperator())
	seedNotifyLaunch(t, project, base, "s", "cto")
	seedNotifyMessage(t, base, "s", team.DefaultOperatorHandle, "new", notifyMsg{
		ID:      "gate-1",
		From:    "cto",
		To:      team.DefaultOperatorHandle,
		Thread:  "gate/release",
		Subject: "APPROVAL: release",
		Kind:    string(state.KindQuestion),
		Created: notifyNow,
	})
	cfg, err := team.ReadProfile(project, team.DefaultProfile)
	if err != nil {
		t.Fatal(err)
	}
	observation := inspectGoalSupervisionGates(cfg, team.DefaultProfile, "s", filepath.Join(base, "s"), duplicateLaunchProbe{
		PIDAlive:     func(int) bool { return true },
		ProcessMatch: func(int, func(string) bool) bool { return true },
		Now:          func() time.Time { return notifyNow },
	}, notifyNow)
	if len(observation.SourceErrors) != 0 ||
		!observation.Evidence.Known ||
		observation.Evidence.Open != 1 {
		t.Fatalf("gate observation = %+v, want known one durable open gate", observation)
	}
}

func TestInspectGoalSupervisionGatesFailsClosedWhenNamespaceCannotBeScanned(t *testing.T) {
	cfg := team.Team{
		Project: t.TempDir(),
		Operator: &team.OperatorConfig{
			Enabled: true, Handle: team.DefaultOperatorHandle, Participant: true,
		},
	}
	observation := inspectGoalSupervisionGates(
		cfg, team.DefaultProfile, "missing", "", duplicateLaunchProbe{}, notifyNow,
	)
	if observation.Evidence.Known || len(observation.SourceErrors) == 0 {
		t.Fatalf("gate observation = %+v, want unknown with source error", observation)
	}
}

func TestInspectGoalSupervisionGatesMarksMailboxWarningsAmbiguous(t *testing.T) {
	project, base, _ := seedNotifyProject(t, team.DefaultOperator())
	seedNotifyLaunch(t, project, base, "s", "cto")
	tornDir := filepath.Join(base, "s", "agents", team.DefaultOperatorHandle, "inbox", "new")
	if err := os.MkdirAll(tornDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tornDir, "torn.md"), []byte("---json\n{\"id\":"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := team.ReadProfile(project, team.DefaultProfile)
	if err != nil {
		t.Fatal(err)
	}
	observation := inspectGoalSupervisionGates(
		cfg, team.DefaultProfile, "s", filepath.Join(base, "s"),
		duplicateLaunchProbe{
			PIDAlive:     func(int) bool { return true },
			ProcessMatch: func(int, func(string) bool) bool { return true },
			Now:          func() time.Time { return notifyNow },
		},
		notifyNow,
	)
	if !observation.Evidence.Known ||
		!observation.Evidence.Ambiguous ||
		len(observation.SourceErrors) == 0 {
		t.Fatalf("gate observation = %+v, want known but ambiguous with source error", observation)
	}
}

func gotCodes(reasons []GoalSupervisionEligibilityReason) []string {
	codes := make([]string, 0, len(reasons))
	for _, reason := range reasons {
		codes = append(codes, reason.Code)
	}
	return codes
}
