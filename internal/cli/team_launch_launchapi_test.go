package cli

import (
	"context"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/avivsinai/agent-message-queue/launchapi"

	"github.com/omriariav/amq-squad/v3/internal/launchintent"
	"github.com/omriariav/amq-squad/v3/internal/team"
)

// launchapiTestTeam seeds a real team.json (gh#747's claudeWorkerPreauthEligible
// reads team.ExistsProfile/team.ReadProfile from disk, so a fake in-memory-only
// Project path would make every seat ineligible regardless of role/binary) and
// returns the matching in-memory team.Team, Project pointed at the seeded dir.
func launchapiTestTeam(t *testing.T) team.Team {
	t.Helper()
	tm := team.Team{
		Orchestrated: true,
		Lead:         "lead",
		Members: []team.Member{
			{Role: "lead", Binary: "claude", Handle: "lead", Session: "s"},
			{Role: "fullstack", Binary: "claude", Handle: "fullstack", Session: "s", CWD: "/proj-fullstack-wt"},
			{Role: "senior-dev", Binary: "codex", Handle: "senior-dev", Session: "s", CWD: "/proj-senior-dev-wt"},
		},
	}
	tm.Project = seedTeam(t, tm)
	return tm
}

// launchapiTestPreflights builds preflights matching launchapiTestTeam's
// roster. projectDir must be the same seeded team's Project: the lead
// member carries no CWD override in that roster, so its EffectiveCWD (and
// this preflight's CWD, which must match it for
// TestLaunchapiBackendDualRunParity) falls back to the project root itself.
func launchapiTestPreflights(projectDir, baseRoot string) []agentLaunchPreflight {
	return []agentLaunchPreflight{
		{Role: "lead", Handle: "lead", CWD: projectDir, Root: "/proj/.agent-mail/s", BaseRoot: baseRoot, Workstream: "s"},
		{Role: "fullstack", Handle: "fullstack", CWD: "/proj-fullstack-wt", Root: "/proj/.agent-mail/s", BaseRoot: baseRoot, Workstream: "s"},
		{Role: "senior-dev", Handle: "senior-dev", CWD: "/proj-senior-dev-wt", Root: "/proj/.agent-mail/s", BaseRoot: baseRoot, Workstream: "s"},
	}
}

// TestLaunchapiBackendRequiresExplicitBaseRoot proves the backend refuses to
// compile a launch intent when TargetV1.BaseRoot resolves empty -- gh#734's
// always-explicit contract, enforced here at the seam that feeds
// internal/launchintent.Compile (which itself refuses an empty BaseRoot; see
// TestCompileRejectsMissingBaseRoot).
func TestLaunchapiBackendRequiresExplicitBaseRoot(t *testing.T) {
	b := launchapiTeamLaunchBackend{}
	tm := launchapiTestTeam(t)
	opts := teamLaunchOptions{Workstream: "s", Trust: trustModeApproveForMe}

	if _, err := b.buildIntentInput(tm, opts, launchapiTestPreflights(tm.Project, ""), nil); err == nil {
		t.Fatal("empty base_root: buildIntentInput accepted it")
	}

	if _, err := b.buildIntentInput(tm, opts, launchapiTestPreflights(tm.Project, "/proj/.agent-mail"), nil); err != nil {
		t.Fatalf("non-empty base_root: buildIntentInput: %v", err)
	}
}

// TestBuildIntentInputSessionRootSatisfiesAuthorityRule is the named test
// cto/t10 required after the following finding: launchapi's own
// openExplicitBaseAuthority (pinned v0.75.0, internal/launch/base_root.go)
// requires filepath.Dir(SessionRoot) == BaseRoot and
// filepath.Base(SessionRoot) == Session, refusing base_root_relation_invalid
// otherwise -- and buildIntentInput previously sent SessionRoot: t.Project,
// which satisfies neither on any real project layout. This mirrors that
// exact rule locally (the same pattern this file already uses for other
// upstream contract checks, e.g. validManagedSessionLabel) so a regression
// here fails a fast unit test instead of only a real Prepare/Apply call.
func TestBuildIntentInputSessionRootSatisfiesAuthorityRule(t *testing.T) {
	b := launchapiTeamLaunchBackend{}
	tm := launchapiTestTeam(t)
	opts := teamLaunchOptions{Workstream: "s", Trust: trustModeApproveForMe}

	input, err := b.buildIntentInput(tm, opts, launchapiTestPreflights(tm.Project, "/proj/.agent-mail"), nil)
	if err != nil {
		t.Fatalf("buildIntentInput: %v", err)
	}
	target := input.Target
	if got := filepath.Dir(target.SessionRoot); got != target.BaseRoot {
		t.Fatalf("filepath.Dir(SessionRoot) = %q, want it to equal BaseRoot %q (launchapi's openExplicitBaseAuthority requires this)", got, target.BaseRoot)
	}
	if got := filepath.Base(target.SessionRoot); got != target.Session {
		t.Fatalf("filepath.Base(SessionRoot) = %q, want it to equal Session %q (launchapi's openExplicitBaseAuthority requires this)", got, target.Session)
	}
	if target.SessionRoot == tm.Project {
		t.Fatalf("SessionRoot equals the team's project root (%q) -- this is the exact bug: SessionRoot must be the session's own AMQ root, not the project directory", tm.Project)
	}
}

func launchapiTestRequiredActions() []launchapi.RequiredActionV1 {
	return []launchapi.RequiredActionV1{
		{ActionID: "a1", Kind: launchapi.RequiredActionTrustConfirmation, AllowedDecisions: []launchapi.DecisionChoiceV1{launchapi.DecisionTrustExactSubject, launchapi.DecisionDeny}, ReasonCode: "new_subject"},
		{ActionID: "a2", Kind: launchapi.RequiredActionStaleConversation, AllowedDecisions: []launchapi.DecisionChoiceV1{launchapi.DecisionFreshOnce, launchapi.DecisionAbort}, ReasonCode: "conversation_stale"},
		{ActionID: "a3", Kind: launchapi.RequiredActionRebindConfirmation, AllowedDecisions: []launchapi.DecisionChoiceV1{launchapi.DecisionCloseOld, launchapi.DecisionLeaveOld}, ReasonCode: "rebind_detected"},
		{ActionID: "a4", Kind: launchapi.RequiredActionUnsupportedCapability, AllowedDecisions: []launchapi.DecisionChoiceV1{launchapi.DecisionAcceptDegraded, launchapi.DecisionAbort}, ReasonCode: "capability_gap"},
	}
}

// TestLaunchapiBackendSurfacesRequiredActionsAsOperatorGates proves every
// RequiredActionV1 kind launchapi v0.70.0 defines becomes one gate/<topic>
// question thread to the configured operator, and that no decision is ever
// auto-selected -- meaning never without an explicit --launchapi-decision:
// surfaceRequiredActionsAsOperatorGates itself never constructs a
// DecisionV1, and launch() only ever calls it with the actions
// resolveLaunchapiDecisions found no supplied decision for.
func TestLaunchapiBackendSurfacesRequiredActionsAsOperatorGates(t *testing.T) {
	project, _, _ := seedNotifyProject(t, team.DefaultOperator())
	calls := withAMQCommandSeams(t, amqEnv{Root: ".agent-mail/{session}", BaseRoot: ".agent-mail"}, "Sent msg-gate to user\n")

	tm, err := team.ReadProfile(project, team.DefaultProfile)
	if err != nil {
		t.Fatalf("read seeded team: %v", err)
	}
	tm.Project = project
	opts := teamLaunchOptions{Profile: team.DefaultProfile, Workstream: "s"}
	actions := launchapiTestRequiredActions()

	if err := surfaceRequiredActionsAsOperatorGates(tm, opts, actions); err != nil {
		t.Fatalf("surfaceRequiredActionsAsOperatorGates: %v", err)
	}
	if len(*calls) != len(actions) {
		t.Fatalf("gate sends=%d, want one per required action (%d)", len(*calls), len(actions))
	}
	seenThreads := map[string]bool{}
	for i, call := range *calls {
		thread := amqFlagValue(call.Arg, "thread")
		if !strings.HasPrefix(thread, "gate/launchapi-") {
			t.Fatalf("call %d thread=%q, want a gate/launchapi-* topic", i, thread)
		}
		if seenThreads[thread] {
			t.Fatalf("duplicate gate thread %q", thread)
		}
		seenThreads[thread] = true
		if amqFlagValue(call.Arg, "kind") != "question" {
			t.Fatalf("call %d kind=%q, want question (never a pre-answered decision)", i, amqFlagValue(call.Arg, "kind"))
		}
		if amqFlagValue(call.Arg, "to") != team.DefaultOperatorHandle {
			t.Fatalf("call %d to=%q, want the configured operator", i, amqFlagValue(call.Arg, "to"))
		}
		body := amqFlagValue(call.Arg, "body")
		if !strings.Contains(body, "No decision was auto-selected") {
			t.Fatalf("call %d body missing the never-auto-answer statement: %q", i, body)
		}
	}
}

// TestLaunchapiBackendSurfacesOnlyUndecidedActionsWithPartialDecisions proves
// that when some (not all) required actions have a supplied
// --launchapi-decision, only the still-undecided actions are surfaced as
// gates -- resolveLaunchapiDecisions's missing list, not the full action set.
func TestLaunchapiBackendSurfacesOnlyUndecidedActionsWithPartialDecisions(t *testing.T) {
	project, _, _ := seedNotifyProject(t, team.DefaultOperator())
	calls := withAMQCommandSeams(t, amqEnv{Root: ".agent-mail/{session}", BaseRoot: ".agent-mail"}, "Sent msg-gate to user\n")

	tm, err := team.ReadProfile(project, team.DefaultProfile)
	if err != nil {
		t.Fatalf("read seeded team: %v", err)
	}
	tm.Project = project
	opts := teamLaunchOptions{Profile: team.DefaultProfile, Workstream: "s"}
	actions := launchapiTestRequiredActions()

	_, missing, err := resolveLaunchapiDecisions(actions, map[string]string{
		"a1": string(launchapi.DecisionTrustExactSubject),
		"a3": string(launchapi.DecisionCloseOld),
	})
	if err != nil {
		t.Fatalf("resolveLaunchapiDecisions: %v", err)
	}
	if len(missing) != 2 {
		t.Fatalf("missing=%v, want exactly the 2 undecided actions (a2, a4)", missing)
	}

	if err := surfaceRequiredActionsAsOperatorGates(tm, opts, missing); err != nil {
		t.Fatalf("surfaceRequiredActionsAsOperatorGates: %v", err)
	}
	if len(*calls) != 2 {
		t.Fatalf("gate sends=%d, want exactly 2 (only the undecided actions)", len(*calls))
	}
	for _, id := range []string{"a2", "a4"} {
		found := false
		for _, call := range *calls {
			if strings.Contains(amqFlagValue(call.Arg, "thread"), "-"+id) {
				found = true
			}
		}
		if !found {
			t.Fatalf("expected a gate thread for undecided action %s, calls=%+v", id, *calls)
		}
	}
	for _, id := range []string{"a1", "a3"} {
		for _, call := range *calls {
			if strings.Contains(amqFlagValue(call.Arg, "thread"), "-"+id) {
				t.Fatalf("decided action %s should not have been re-raised as a gate: %+v", id, call)
			}
		}
	}
}

// TestLaunchapiBackendAppliesExplicitOperatorDecisions proves that when every
// required action has a valid supplied --launchapi-decision,
// resolveLaunchapiDecisions returns a matching DecisionV1 for each one and
// an empty missing list -- so launch() proceeds straight to Apply without
// raising any gate.
func TestLaunchapiBackendAppliesExplicitOperatorDecisions(t *testing.T) {
	actions := launchapiTestRequiredActions()
	supplied := map[string]string{
		"a1": string(launchapi.DecisionTrustExactSubject),
		"a2": string(launchapi.DecisionFreshOnce),
		"a3": string(launchapi.DecisionCloseOld),
		"a4": string(launchapi.DecisionAcceptDegraded),
	}

	decisions, missing, err := resolveLaunchapiDecisions(actions, supplied)
	if err != nil {
		t.Fatalf("resolveLaunchapiDecisions: %v", err)
	}
	if len(missing) != 0 {
		t.Fatalf("missing=%v, want none: every action had a supplied decision", missing)
	}
	if len(decisions) != len(actions) {
		t.Fatalf("decisions=%v, want exactly %d (one per action)", decisions, len(actions))
	}
	gotByID := map[string]launchapi.DecisionChoiceV1{}
	for _, d := range decisions {
		gotByID[d.ActionID] = d.Choice
	}
	for actionID, wantChoice := range supplied {
		if gotByID[actionID] != launchapi.DecisionChoiceV1(wantChoice) {
			t.Fatalf("decision for %s = %q, want %q", actionID, gotByID[actionID], wantChoice)
		}
	}
}

// TestLaunchapiBackendRejectsDecisionOutsideAllowedSet proves a supplied
// --launchapi-decision choice not in that action's AllowedDecisions is
// rejected with an error naming the allowed set, not silently ignored or
// passed through to Apply.
func TestLaunchapiBackendRejectsDecisionOutsideAllowedSet(t *testing.T) {
	actions := launchapiTestRequiredActions()
	_, _, err := resolveLaunchapiDecisions(actions, map[string]string{"a1": "not_a_real_choice"})
	if err == nil {
		t.Fatal("expected an error for a decision outside the allowed set")
	}
	if !strings.Contains(err.Error(), "a1") || !strings.Contains(err.Error(), "not_a_real_choice") {
		t.Fatalf("error = %v, want it to name the action and the rejected choice", err)
	}
	if !strings.Contains(err.Error(), string(launchapi.DecisionTrustExactSubject)) || !strings.Contains(err.Error(), string(launchapi.DecisionDeny)) {
		t.Fatalf("error = %v, want it to name the allowed set (%s, %s)", err, launchapi.DecisionTrustExactSubject, launchapi.DecisionDeny)
	}
}

// TestLaunchapiBackendRejectsStaleDecisionForUnknownAction proves a supplied
// --launchapi-decision for an ActionID Prepare did not return is treated as
// a stale answer and errors, rather than being silently dropped.
func TestLaunchapiBackendRejectsStaleDecisionForUnknownAction(t *testing.T) {
	actions := launchapiTestRequiredActions()
	_, _, err := resolveLaunchapiDecisions(actions, map[string]string{"a-does-not-exist": "deny"})
	if err == nil {
		t.Fatal("expected an error for a decision on an unknown action id")
	}
	if !strings.Contains(err.Error(), "a-does-not-exist") || !strings.Contains(err.Error(), "stale") {
		t.Fatalf("error = %v, want it to name the unknown action id and call it stale", err)
	}
}

// TestLaunchapiBackendHasWorkerPreauthAtFloor replaces
// TestLaunchapiBackendHasNoWorkerPreauth (gh#747): the v2.30.0 dual-run
// limitation is lifted. At or above the argv-grammar floor, an eligible
// claude worker's compiled participant carries the two-token scoped grant
// (never a bare Bash grant, never the equals-joined spelling), and an
// eligible codex worker's compiled participant keeps approvals_reviewer
// byte-identically. Below the floor both still drop, exercised by
// TestLaunchapiBackendDualRunParity's buildIntentInput-level check and
// internal/launchintent's own BelowFloor tests.
func TestLaunchapiBackendHasWorkerPreauthAtFloor(t *testing.T) {
	tm := launchapiTestTeam(t)
	input, err := launchapiTeamLaunchBackend{}.buildIntentInput(tm, teamLaunchOptions{Workstream: "s", Trust: trustModeApproveForMe}, launchapiTestPreflights(tm.Project, "/proj/.agent-mail"), map[string]launchapi.ProviderCapabilitiesV1{
		"claude": {Provider: "claude", GrammarVersion: 2},
		"codex":  {Provider: "codex", ConfigOverrides: []launchapi.ConfigOverrideCapabilityV1{{Key: "approvals_reviewer", AllowedValues: []string{"user", "auto_review", "guardian_subagent"}}}},
	})
	if err != nil {
		t.Fatalf("buildIntentInput: %v", err)
	}
	intent, _, err := launchintent.Compile(input)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	var sawClaudeGrant, sawCodexReviewer bool
	for _, p := range intent.Participants {
		if p.Handle == "fullstack" {
			for i, arg := range p.Args {
				if arg == "--allowedTools" {
					if i+1 >= len(p.Args) {
						t.Fatalf("fullstack --allowedTools has no value: %v", p.Args)
					}
					if p.Args[i+1] == "Bash" {
						t.Fatalf("fullstack carries a bare Bash grant at the floor: %v", p.Args)
					}
					if p.Args[i+1] != launchintent.ScopedPreauthGrant {
						t.Fatalf("fullstack grant = %q, want %q", p.Args[i+1], launchintent.ScopedPreauthGrant)
					}
					sawClaudeGrant = true
				}
				if strings.HasPrefix(arg, "--allowedTools=") {
					t.Fatalf("fullstack carries the equals-joined spelling at the floor: %v", p.Args)
				}
			}
		}
		if p.Handle == "senior-dev" {
			for i, arg := range p.Args {
				if arg == "-c" && i+1 < len(p.Args) && p.Args[i+1] == `approvals_reviewer="auto_review"` {
					sawCodexReviewer = true
				}
			}
		}
	}
	if !sawClaudeGrant {
		t.Fatalf("fullstack seat has no worker preauth at the floor: %+v", intent.Participants)
	}
	if !sawCodexReviewer {
		t.Fatalf("senior-dev seat has no approvals_reviewer at the floor: %+v", intent.Participants)
	}
}

// TestLaunchapiBackendHasNoWorkerPreauthBelowFloor is
// TestLaunchapiBackendHasWorkerPreauthAtFloor's below-floor complement:
// with no observed capabilities (the phase-1 probe's own shape, and any
// compiled-in contract below the scoped-grammar floor), no participant
// carries any --allowedTools spelling or approvals_reviewer, matching
// gh#732's original behavior byte-for-byte.
func TestLaunchapiBackendHasNoWorkerPreauthBelowFloor(t *testing.T) {
	b := launchapiTeamLaunchBackend{}
	tm := launchapiTestTeam(t)
	opts := teamLaunchOptions{Workstream: "s", Trust: trustModeApproveForMe}
	input, err := b.buildIntentInput(tm, opts, launchapiTestPreflights(tm.Project, "/proj/.agent-mail"), nil)
	if err != nil {
		t.Fatalf("buildIntentInput: %v", err)
	}
	intent, _, err := launchintent.Compile(input)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	for _, p := range intent.Participants {
		for i, arg := range p.Args {
			if strings.HasPrefix(arg, "--allowedTools") {
				t.Fatalf("participant %q carries worker preauth %q below the floor; must have none", p.Handle, arg)
			}
			if arg == "-c" && i+1 < len(p.Args) && strings.HasPrefix(p.Args[i+1], "approvals_reviewer=") {
				t.Fatalf("participant %q carries approvals_reviewer %q below the floor; must have none", p.Handle, p.Args[i+1])
			}
		}
	}
}

// TestLaunchapiBackendRequestsNamedSeatsOnlyWhenAllowedArgumentFormsIncludeName
// (gh#748): buildIntentInput sets SessionName for every claude seat
// unconditionally (the same "eligibility and gating are separate decisions"
// pattern gh#747 established for the scoped grant), but naming only
// actually reaches compiled argv when the observed capability says the
// negotiated contract accepts it. A codex seat never gets -n even when its
// own capability facts happen to include naming-adjacent forms, because
// buildIntentInput never sets SessionName for a non-claude seat at all.
func TestLaunchapiBackendRequestsNamedSeatsOnlyWhenAllowedArgumentFormsIncludeName(t *testing.T) {
	tm := launchapiTestTeam(t)
	opts := teamLaunchOptions{Workstream: "s", Trust: trustModeApproveForMe}
	preflights := launchapiTestPreflights(tm.Project, "/proj/.agent-mail")
	b := launchapiTeamLaunchBackend{}

	t.Run("claude gets -n when AllowedArgumentForms includes it", func(t *testing.T) {
		input, err := b.buildIntentInput(tm, opts, preflights, map[string]launchapi.ProviderCapabilitiesV1{
			"claude": {Provider: "claude", AllowedArgumentForms: []string{"-n", "--name"}},
		})
		if err != nil {
			t.Fatalf("buildIntentInput: %v", err)
		}
		intent, _, err := launchintent.Compile(input)
		if err != nil {
			t.Fatalf("Compile: %v", err)
		}
		found := false
		for _, p := range intent.Participants {
			if p.Handle != "fullstack" {
				continue
			}
			for i, arg := range p.Args {
				if arg == "-n" && i+1 < len(p.Args) && p.Args[i+1] == "s/fullstack" {
					found = true
				}
				if strings.HasPrefix(arg, "--name=") {
					t.Fatalf("fullstack used the equals-joined --name= spelling: %v", p.Args)
				}
			}
		}
		if !found {
			t.Fatalf("fullstack seat did not get -n s/fullstack with AllowedArgumentForms including it")
		}
	})

	t.Run("claude does not get -n when AllowedArgumentForms lacks it", func(t *testing.T) {
		input, err := b.buildIntentInput(tm, opts, preflights, nil)
		if err != nil {
			t.Fatalf("buildIntentInput: %v", err)
		}
		intent, _, err := launchintent.Compile(input)
		if err != nil {
			t.Fatalf("Compile: %v", err)
		}
		for _, p := range intent.Participants {
			if p.Handle != "fullstack" {
				continue
			}
			for _, arg := range p.Args {
				if arg == "-n" || arg == "--name" {
					t.Fatalf("fullstack seat carries naming with no observed AllowedArgumentForms: %v", p.Args)
				}
			}
		}
	})

	t.Run("codex never gets -n even if its own capability facts include naming forms", func(t *testing.T) {
		input, err := b.buildIntentInput(tm, opts, preflights, map[string]launchapi.ProviderCapabilitiesV1{
			// Not a real upstream shape (t8/t10 confirmed codex has no
			// -n/--name argRules entry on any measured version) -- exists
			// only to prove buildIntentInput's own claude-only SessionName
			// gate holds even if a capability probe ever reported this.
			"codex": {Provider: "codex", AllowedArgumentForms: []string{"-n", "--name"}},
		})
		if err != nil {
			t.Fatalf("buildIntentInput: %v", err)
		}
		intent, _, err := launchintent.Compile(input)
		if err != nil {
			t.Fatalf("Compile: %v", err)
		}
		for _, p := range intent.Participants {
			if p.Handle != "senior-dev" {
				continue
			}
			for _, arg := range p.Args {
				if arg == "-n" || arg == "--name" {
					t.Fatalf("codex seat carries naming, must never: %v", p.Args)
				}
			}
		}
	})
}

// launchapiTestStubAMQEnv stubs resolveTeamLaunchAMQEnv (buildTeamPreflights'
// own injectable seam) so b.prepare can run end to end without a real amq
// binary: every member resolves to the same project-scoped root, well above
// the general-operation floor.
func launchapiTestStubAMQEnv(t *testing.T, projectDir string) {
	t.Helper()
	original := resolveTeamLaunchAMQEnv
	root := filepath.Join(projectDir, ".agent-mail", "s")
	resolveTeamLaunchAMQEnv = func(cwd, profile, session, handle string) (amqEnv, error) {
		return amqEnv{AMQVersion: "0.73.0", Root: root, BaseRoot: root, SessionName: "s", Me: handle}, nil
	}
	t.Cleanup(func() { resolveTeamLaunchAMQEnv = original })
}

// TestLaunchapiBackendProbePrepareIsSideEffectFree proves gh#747's two-phase
// prepare (see the backend's prepare doc comment) does not itself write
// anything: the seeded project tree is byte-identical before and after a
// real b.prepare call that runs both the probe and the recompiled phase-2
// Prepare. This backs the design decision directly, on the actual backend
// code path rather than only the standalone launchapi.Prepare proof already
// recorded in docs/amq-0.73.0-adoption-verdict.md.
func TestLaunchapiBackendProbePrepareIsSideEffectFree(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	tm := launchapiTestTeam(t)
	launchapiTestStubAMQEnv(t, tm.Project)
	opts := teamLaunchOptions{Workstream: "s", Trust: trustModeApproveForMe, Profile: team.DefaultProfile}

	before := snapshotTestTree(t, tm.Project)
	b := launchapiTeamLaunchBackend{}
	prepared, _, err := b.prepare(tm, opts)
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	after := snapshotTestTree(t, tm.Project)
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("project tree changed across the two-phase prepare call:\n before: %v\n after:  %v", before, after)
	}
	if prepared.Result.SubjectDigest == "" {
		t.Fatal("phase-2 SubjectDigest is empty")
	}
}

// TestLaunchapiBackendApplyBoundToPhaseTwoDigest proves prepare's returned
// Prepared (what launch/DryRun/Apply all use) reflects phase 2 -- the
// recompiled request with observed capability facts applied -- not the
// phase-1 probe. On this repo's pinned launchapi module the two genuinely
// differ (the probe's conservative intent carries no grant for the eligible
// claude seat; phase 2's does), so this compares real digests rather than
// asserting a tautology.
func TestLaunchapiBackendApplyBoundToPhaseTwoDigest(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	tm := launchapiTestTeam(t)
	launchapiTestStubAMQEnv(t, tm.Project)
	opts := teamLaunchOptions{Workstream: "s", Trust: trustModeApproveForMe, Profile: team.DefaultProfile}

	b := launchapiTeamLaunchBackend{}
	preflights, err := buildTeamPreflights(tm, opts)
	if err != nil {
		t.Fatalf("buildTeamPreflights: %v", err)
	}
	probeInput, err := b.buildIntentInput(tm, opts, preflights, nil)
	if err != nil {
		t.Fatalf("buildIntentInput (probe): %v", err)
	}
	probePrepared, err := b.callPrepare(opts, probeInput)
	if err != nil {
		t.Fatalf("callPrepare (probe): %v", err)
	}

	prepared, _, err := b.prepare(tm, opts)
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}

	if prepared.Result.SubjectDigest == probePrepared.Result.SubjectDigest {
		t.Fatalf("prepare's returned digest matches the phase-1 probe's (%s); phase 2 should have recompiled a different request for the eligible claude seat", prepared.Result.SubjectDigest)
	}
	foundGrant := false
	for _, p := range prepared.Request.Intent.Participants {
		if p.Handle != "fullstack" {
			continue
		}
		for i, arg := range p.Args {
			if arg == "--allowedTools" && i+1 < len(p.Args) && p.Args[i+1] == launchintent.ScopedPreauthGrant {
				foundGrant = true
			}
		}
	}
	if !foundGrant {
		t.Fatalf("prepare's returned Request.Intent (what Apply is bound to) does not carry the phase-2 grant: %+v", prepared.Request.Intent.Participants)
	}
}

// TestBuildIntentInputCarriesRecoveredGoalOnFreshMintOnly is gh#758/t11
// slice C's named acceptance test for the GoalPrompt-on-relaunch contract
// (cto's ruling, task/t11): "a goal survives any relaunch that mints a NEW
// conversation" -- opts.GoalPrompts threads into SeatFacts.GoalPrompt and
// then into InitialInputV1.Text ("GOAL: ..."), but native restore
// (opts.RestoreConversations, ResumePolicy=resume) wins when both are set
// for the same role: the goal already lives in the restored transcript
// there, and re-sending it is noise.
func TestBuildIntentInputCarriesRecoveredGoalOnFreshMintOnly(t *testing.T) {
	tm := launchapiTestTeam(t)
	opts := teamLaunchOptions{
		Workstream: "s", Trust: trustModeApproveForMe,
		GoalPrompts: map[string]string{"fullstack": "ship the widget", "senior-dev": "ship the widget"},
		RestoreConversations: map[string]string{
			// senior-dev is being natively restored; its GoalPrompt above
			// must be suppressed. fullstack has no restored conversation,
			// so its GoalPrompt must fire.
			"senior-dev": "conv-123",
		},
	}
	input, err := launchapiTeamLaunchBackend{}.buildIntentInput(tm, opts, launchapiTestPreflights(tm.Project, "/proj/.agent-mail"), nil)
	if err != nil {
		t.Fatalf("buildIntentInput: %v", err)
	}
	intent, _, err := launchintent.Compile(input)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	var checkedFullstack, checkedSeniorDev, checkedLead bool
	for _, p := range intent.Participants {
		switch p.Handle {
		case "fullstack":
			checkedFullstack = true
			if p.InitialInput == nil || !strings.Contains(p.InitialInput.Text, "GOAL: ship the widget") {
				t.Errorf("fullstack (fresh mint) InitialInput = %+v, want it to carry the recovered goal", p.InitialInput)
			}
			if p.ResumePolicy == launchapi.ResumePolicyResume {
				t.Errorf("fullstack has no restored conversation but got ResumePolicy=resume")
			}
		case "senior-dev":
			checkedSeniorDev = true
			if p.ResumePolicy != launchapi.ResumePolicyResume {
				t.Errorf("senior-dev ResumePolicy = %q, want resume", p.ResumePolicy)
			}
			if p.InitialInput != nil && strings.Contains(p.InitialInput.Text, "GOAL:") {
				t.Errorf("senior-dev (native restore) InitialInput = %+v, want no GoalPrompt injected", p.InitialInput)
			}
		case "lead":
			checkedLead = true
			if p.InitialInput != nil {
				t.Errorf("lead has no GoalPrompts entry but got InitialInput = %+v", p.InitialInput)
			}
		}
	}
	if !checkedFullstack || !checkedSeniorDev || !checkedLead {
		t.Fatalf("did not observe all three participants: fullstack=%t senior-dev=%t lead=%t", checkedFullstack, checkedSeniorDev, checkedLead)
	}
}

// TestBuildIntentInputNativeRestoreFromInspectWinsOverGoalPrompt is
// gh#758/t11 slice C's named acceptance test for work item 2 (cto's
// ruling, task/t11): the native-restore signal for a launchapi seat is
// amq's own minted conversation, exposed per participant via a session
// Inspect call (ParticipantObservationV1.Conversation) -- never
// launch.Record, which the launchapi path never writes at all. When
// Inspect reports a conversation for a handle, that seat's ResumePolicy
// becomes Resume and any recovered GoalPrompt for the same handle is
// suppressed (native restore wins, same precedence rule as legacy
// RestoreConversations). A handle Inspect has no evidence for falls
// through to fresh-mint + recovered GoalPrompt untouched.
func TestBuildIntentInputNativeRestoreFromInspectWinsOverGoalPrompt(t *testing.T) {
	tm := launchapiTestTeam(t)
	stubLaunchapiInspect(t, func(context.Context, launchapi.InspectRequestV1) (launchapi.InspectResultV1, error) {
		return launchapi.InspectResultV1{
			State: "present",
			Observations: []launchapi.ParticipantObservationV1{
				{Handle: "senior-dev", Runnable: true, Conversation: "conv-abc123"},
				{Handle: "fullstack", Runnable: true},
			},
		}, nil
	})
	opts := teamLaunchOptions{
		Workstream: "s", Trust: trustModeApproveForMe,
		GoalPrompts: map[string]string{"fullstack": "ship the widget", "senior-dev": "ship the widget"},
	}
	input, err := launchapiTeamLaunchBackend{}.buildIntentInput(tm, opts, launchapiTestPreflights(tm.Project, "/proj/.agent-mail"), nil)
	if err != nil {
		t.Fatalf("buildIntentInput: %v", err)
	}
	intent, _, err := launchintent.Compile(input)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	var checkedFullstack, checkedSeniorDev bool
	for _, p := range intent.Participants {
		switch p.Handle {
		case "senior-dev":
			checkedSeniorDev = true
			if p.ResumePolicy != launchapi.ResumePolicyResume {
				t.Errorf("senior-dev ResumePolicy = %q, want resume (Inspect reported a minted conversation)", p.ResumePolicy)
			}
			if p.InitialInput != nil && strings.Contains(p.InitialInput.Text, "GOAL:") {
				t.Errorf("senior-dev (native restore via Inspect) InitialInput = %+v, want no GoalPrompt injected", p.InitialInput)
			}
		case "fullstack":
			checkedFullstack = true
			if p.ResumePolicy == launchapi.ResumePolicyResume {
				t.Errorf("fullstack has no Inspect-reported conversation but got ResumePolicy=resume")
			}
			if p.InitialInput == nil || !strings.Contains(p.InitialInput.Text, "GOAL: ship the widget") {
				t.Errorf("fullstack (fresh mint, no native restore) InitialInput = %+v, want it to carry the recovered goal", p.InitialInput)
			}
		}
	}
	if !checkedFullstack || !checkedSeniorDev {
		t.Fatalf("did not observe both participants: fullstack=%t senior-dev=%t", checkedFullstack, checkedSeniorDev)
	}
}

// TestBuildIntentInputFallsThroughToFreshMintWhenInspectHasNoEvidence
// covers the "no minted conversation" half of work item 2: when Inspect's
// session target does not even resolve (binding_missing/base_root_*), the
// nativeConversationByHandle map stays empty and every seat proceeds as an
// ordinary fresh mint with its recovered GoalPrompt intact -- exactly as if
// Inspect had never been consulted.
func TestBuildIntentInputFallsThroughToFreshMintWhenInspectHasNoEvidence(t *testing.T) {
	tm := launchapiTestTeam(t)
	stubLaunchapiInspect(t, func(context.Context, launchapi.InspectRequestV1) (launchapi.InspectResultV1, error) {
		return launchapi.InspectResultV1{}, fmt.Errorf("base_root_unauthorized: no authority here")
	})
	opts := teamLaunchOptions{
		Workstream: "s", Trust: trustModeApproveForMe,
		GoalPrompts: map[string]string{"fullstack": "ship the widget"},
	}
	input, err := launchapiTeamLaunchBackend{}.buildIntentInput(tm, opts, launchapiTestPreflights(tm.Project, "/proj/.agent-mail"), nil)
	if err != nil {
		t.Fatalf("buildIntentInput: %v", err)
	}
	intent, _, err := launchintent.Compile(input)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	for _, p := range intent.Participants {
		if p.Handle != "fullstack" {
			continue
		}
		if p.ResumePolicy == launchapi.ResumePolicyResume {
			t.Errorf("fullstack got ResumePolicy=resume with no Inspect evidence at all")
		}
		if p.InitialInput == nil || !strings.Contains(p.InitialInput.Text, "GOAL: ship the widget") {
			t.Errorf("fullstack InitialInput = %+v, want the recovered goal to survive an unresolvable Inspect target", p.InitialInput)
		}
	}
}

// TestStartApplyRequiresExactDecisionsForEveryRequiredAction is gh#757's
// named acceptance test for resolveLaunchapiDecisions -- the exact
// mechanism start --apply/--decision depends on and launchapiTeamLaunchBackend.launch
// already calls before ever reaching adoptionseam.Apply. Manufacturing a
// live RequiredActionV1 through a real Prepare call requires elaborate
// trust-store/conversation state this repo's other launchapi tests do not
// attempt either (TestLaunchapiBackendSurfacesRequiredActionsAsOperatorGates
// uses the same launchapiTestRequiredActions() fixture directly for exactly
// this reason) -- so this proves the missing/extra-decision contract
// directly against that fixture: every required action needs an exact
// decision, no partial application, before any roster mutation.
func TestStartApplyRequiresExactDecisionsForEveryRequiredAction(t *testing.T) {
	actions := launchapiTestRequiredActions()

	t.Run("missing decision for one action reports it, not an error", func(t *testing.T) {
		supplied := map[string]string{
			"a1": string(launchapi.DecisionTrustExactSubject),
			"a2": string(launchapi.DecisionFreshOnce),
			"a3": string(launchapi.DecisionCloseOld),
			// a4 deliberately omitted.
		}
		decisions, missing, err := resolveLaunchapiDecisions(actions, supplied)
		if err != nil {
			t.Fatalf("resolveLaunchapiDecisions: %v", err)
		}
		if len(decisions) != 3 {
			t.Fatalf("decisions = %+v, want exactly the 3 supplied", decisions)
		}
		if len(missing) != 1 || missing[0].ActionID != "a4" {
			t.Fatalf("missing = %+v, want exactly a4", missing)
		}
	})

	t.Run("an extra decision for an unknown action_id refuses before any roster mutation", func(t *testing.T) {
		supplied := map[string]string{"a1": string(launchapi.DecisionTrustExactSubject), "stale-action": "fresh_once"}
		if _, _, err := resolveLaunchapiDecisions(actions, supplied); err == nil || !strings.Contains(err.Error(), "stale answer") {
			t.Fatalf("resolveLaunchapiDecisions with an unknown action_id = %v, want a stale-answer refusal", err)
		}
	})

	t.Run("a choice outside the action's allowed set refuses", func(t *testing.T) {
		supplied := map[string]string{"a1": "close_old"} // a1 is RequiredActionTrustConfirmation; close_old is not in its allowed set.
		if _, _, err := resolveLaunchapiDecisions(actions, supplied); err == nil || !strings.Contains(err.Error(), "not in the allowed set") {
			t.Fatalf("resolveLaunchapiDecisions with a disallowed choice = %v, want an allowed-set refusal", err)
		}
	})

	t.Run("every action decided exactly returns all decisions and no missing", func(t *testing.T) {
		supplied := map[string]string{
			"a1": string(launchapi.DecisionTrustExactSubject), "a2": string(launchapi.DecisionFreshOnce),
			"a3": string(launchapi.DecisionCloseOld), "a4": string(launchapi.DecisionAcceptDegraded),
		}
		decisions, missing, err := resolveLaunchapiDecisions(actions, supplied)
		if err != nil {
			t.Fatalf("resolveLaunchapiDecisions: %v", err)
		}
		if len(missing) != 0 {
			t.Fatalf("missing = %+v, want none", missing)
		}
		if len(decisions) != len(actions) {
			t.Fatalf("decisions = %+v, want one per action (%d)", decisions, len(actions))
		}
	})
}

// TestLaunchapiBackendLaunchRejectsStaleSubjectDigest is gh#757's supporting
// unit-level proof (see TestStartApplyRejectsStaleSubjectDigest for the
// start-level acceptance test): opts.ExpectedSubjectDigest binds launch to
// one exact previously computed subject_digest. A caller-supplied digest
// that does not match the freshly recomputed one refuses before any
// decision is resolved or adoptionseam.Apply is ever called.
func TestLaunchapiBackendLaunchRejectsStaleSubjectDigest(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	tm := launchapiTestTeam(t)
	launchapiTestStubAMQEnv(t, tm.Project)
	opts := teamLaunchOptions{
		Workstream: "s", Trust: trustModeApproveForMe, Profile: team.DefaultProfile,
		ExpectedSubjectDigest: "sha256:0000000000000000000000000000000000000000000000000000000000000",
	}

	b := launchapiTeamLaunchBackend{}
	if _, err := b.apply(tm, opts); err == nil || !strings.Contains(err.Error(), "stale subject_digest") {
		t.Fatalf("launch with a mismatched ExpectedSubjectDigest = %v, want a stale subject_digest refusal", err)
	}
}

// TestLaunchapiBackendLaunchAcceptsMatchingSubjectDigest proves the digest
// gate itself passes when ExpectedSubjectDigest matches a fresh Prepare's
// SubjectDigest exactly -- launch proceeds past the check (whatever it does
// or does not accomplish afterward in this no-real-tmux test environment is
// not this test's concern; it only proves the gate is not a false refusal).
func TestLaunchapiBackendLaunchAcceptsMatchingSubjectDigest(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	tm := launchapiTestTeam(t)
	launchapiTestStubAMQEnv(t, tm.Project)
	opts := teamLaunchOptions{Workstream: "s", Trust: trustModeApproveForMe, Profile: team.DefaultProfile}

	b := launchapiTeamLaunchBackend{}
	prepared, _, err := b.prepare(tm, opts)
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	opts.ExpectedSubjectDigest = prepared.Result.SubjectDigest

	if _, err := b.apply(tm, opts); err != nil && strings.Contains(err.Error(), "stale subject_digest") {
		t.Fatalf("launch with a matching ExpectedSubjectDigest wrongly refused as stale: %v", err)
	}
}
