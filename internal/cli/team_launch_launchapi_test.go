package cli

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/avivsinai/agent-message-queue/launchapi"

	"github.com/omriariav/amq-squad/v2/internal/launchintent"
	"github.com/omriariav/amq-squad/v2/internal/team"
)

// TestResolveTeamLaunchBackendDefaultsToLaunchapiOnTmux proves
// resolveTeamLaunchBackend -- the single selection seam executeTeamLaunch
// calls -- selects the launchapi backend by default (LaunchVia empty,
// "auto", or the explicit "launchapi" opt-in) whenever the terminal resolves
// to tmux. This is v2.31.0's default flip (gh#755): pre-v2.31.0, only the
// explicit "launchapi" value ever selected this backend.
func TestResolveTeamLaunchBackendDefaultsToLaunchapiOnTmux(t *testing.T) {
	for _, launchVia := range []string{"", "auto", "AUTO", "launchapi"} {
		got, err := resolveTeamLaunchBackend(teamLaunchOptions{Terminal: "tmux", LaunchVia: launchVia})
		if err != nil {
			t.Fatalf("LaunchVia=%q: unexpected error: %v", launchVia, err)
		}
		if got.Name() != "launchapi" {
			t.Fatalf("LaunchVia=%q on tmux selected %q, want launchapi (v2.31.0 default)", launchVia, got.Name())
		}
	}

	if _, err := resolveTeamLaunchBackend(teamLaunchOptions{Terminal: "iterm2", LaunchVia: "launchapi"}); err == nil || !strings.Contains(err.Error(), "requires --terminal tmux") {
		t.Fatalf("--launch-via launchapi with a non-tmux terminal: err=%v, want a tmux-only refusal", err)
	}

	if _, err := resolveTeamLaunchBackend(teamLaunchOptions{Terminal: "tmux", LaunchVia: "bogus"}); err == nil || !strings.Contains(err.Error(), `unsupported --launch-via "bogus"`) {
		t.Fatalf("unknown --launch-via value: err=%v", err)
	}

	// A non-tmux terminal with LaunchVia empty/"auto" still falls back to
	// the legacy per-terminal lookup (launchapi is tmux-only); the
	// unsupported-terminal error text is unchanged.
	if _, err := resolveTeamLaunchBackend(teamLaunchOptions{Terminal: "nope"}); err == nil ||
		err.Error() != `unsupported terminal "nope": supported terminals: `+strings.Join(registeredTeamLaunchTerminals(), ", ") {
		t.Fatalf("legacy unsupported-terminal error text changed: %v", err)
	}
}

// TestResolveTeamLaunchBackendLegacyRequiresExplicitOptIn proves the legacy
// tmux pane driver is reachable ONLY via the explicit "--launch-via legacy"
// opt-out, never via empty/"auto" any more, and that it then selects the
// same backend the pre-gh#755 map lookup (v2.30.1's "auto" behavior) would
// have selected -- byte-identical, same map, same backend.
func TestResolveTeamLaunchBackendLegacyRequiresExplicitOptIn(t *testing.T) {
	fake := &fakeBackend{}
	prevTmux, hadTmux := teamLaunchBackends["tmux"]
	teamLaunchBackends["tmux"] = fake
	t.Cleanup(func() {
		if hadTmux {
			teamLaunchBackends["tmux"] = prevTmux
		} else {
			delete(teamLaunchBackends, "tmux")
		}
	})

	got, err := resolveTeamLaunchBackend(teamLaunchOptions{Terminal: "tmux", LaunchVia: "legacy"})
	if err != nil {
		t.Fatalf("--launch-via legacy: unexpected error: %v", err)
	}
	if got.Name() != fake.Name() {
		t.Fatalf("--launch-via legacy selected %q, want the legacy-registered %q", got.Name(), fake.Name())
	}

	wantLegacy, err := legacyTeamLaunchBackend(teamLaunchOptions{Terminal: "tmux"})
	if err != nil {
		t.Fatalf("legacyTeamLaunchBackend: %v", err)
	}
	if got != wantLegacy {
		t.Fatalf("--launch-via legacy backend diverges from v2.30.1's auto map lookup")
	}

	for _, launchVia := range []string{"", "auto"} {
		got, err := resolveTeamLaunchBackend(teamLaunchOptions{Terminal: "tmux", LaunchVia: launchVia})
		if err != nil {
			t.Fatalf("LaunchVia=%q: unexpected error: %v", launchVia, err)
		}
		if got.Name() == fake.Name() {
			t.Fatalf("LaunchVia=%q selected the legacy backend; v2.31.0 requires the explicit --launch-via legacy opt-out", launchVia)
		}
	}
}

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

// TestLaunchapiBackendDualRunParity proves the new backend resolves the same
// per-seat cwd and handle as the legacy tmux backend for an identical team +
// options, and that the two backends' argv diverge ONLY on the two documented
// gh#732 guarantees (no --allowedTools, no approvals_reviewer) -- any other
// divergence would be an undocumented drift this test catches.
func TestLaunchapiBackendDualRunParity(t *testing.T) {
	b := launchapiTeamLaunchBackend{}
	tm := launchapiTestTeam(t)
	opts := teamLaunchOptions{Workstream: "s", Trust: trustModeApproveForMe}
	preflights := launchapiTestPreflights(tm.Project, "/proj/.agent-mail")

	input, err := b.buildIntentInput(tm, opts, preflights, nil)
	if err != nil {
		t.Fatalf("buildIntentInput: %v", err)
	}
	legacyPanes := buildTeamLaunchPanes(tm, opts)
	if len(legacyPanes) != len(input.Seats) {
		t.Fatalf("legacy panes=%d, launchapi seats=%d", len(legacyPanes), len(input.Seats))
	}

	preflightByRole := map[string]agentLaunchPreflight{}
	for _, p := range preflights {
		preflightByRole[p.Role] = p
	}
	members := orderedTeamMembers(tm.Members)
	for i, seat := range input.Seats {
		role := members[i].Role
		pre := preflightByRole[role]
		legacyCWD := members[i].EffectiveCWD(tm.Project)
		if seat.Cwd.Path != legacyCWD {
			t.Fatalf("role %s: launchapi cwd=%q, legacy cwd=%q", role, seat.Cwd.Path, legacyCWD)
		}
		if seat.Handle != pre.Handle {
			t.Fatalf("role %s: launchapi handle=%q, preflight handle=%q", role, seat.Handle, pre.Handle)
		}
	}

	// codex senior-dev seat: the resolved args this backend hands to
	// launchintent.Compile still contain approvals_reviewer (gh#747:
	// sanitization is launchintent's job, exercised by
	// TestCompileIntentKeepsApprovalsReviewerAtOrAboveFloor /
	// TestCompileIntentDropsApprovalsReviewerBelowFloor); what must hold
	// here is that buildIntentInput's own resolution matches the legacy
	// composer's resolution byte-for-byte before sanitization.
	seniorArgs := ([]string)(nil)
	found := false
	for _, seat := range input.Seats {
		if seat.Handle == "senior-dev" {
			seniorArgs, found = seat.Args, true
			break
		}
	}
	if !found {
		t.Fatalf("no senior-dev seat in compiled input: %+v", input.Seats)
	}
	seniorLegacy := launchDefaultChildArgsWithTrust("codex", true, nil, nil, trustModeApproveForMe)
	if strings.Join(seniorArgs, "\x00") != strings.Join(seniorLegacy, "\x00") {
		t.Fatalf("senior-dev resolved args diverge from legacy before sanitization:\n launchapi=%v\n legacy=  %v", seniorArgs, seniorLegacy)
	}

	// claude fullstack seat: gh#747 restores parity at the floor. legacy
	// injects the grant as one equals-joined token
	// (claudePreauthChildArgs); the new path carries it as two tokens
	// (internal/launchintent's sanitizer requirement, see
	// docs/amq-0.73.0-adoption-verdict.md section 3: the equals-joined form
	// is never accepted upstream at any measured version). Grant and
	// reviewer key are no longer expected diffs at the floor: what must
	// hold is the same VALUE, not the same token form.
	fullstackArgs := ([]string)(nil)
	found = false
	for _, seat := range input.Seats {
		if seat.Handle == "fullstack" {
			fullstackArgs, found = seat.Args, true
			break
		}
	}
	if !found {
		t.Fatalf("no fullstack seat in compiled input: %+v", input.Seats)
	}
	legacyGrant := claudeInScopePreauthAllowlist(opts.Workstream)
	if len(legacyGrant) != 1 {
		t.Fatalf("legacy grant = %v, want exactly one pattern", legacyGrant)
	}
	if legacyGrant[0] != launchintent.ScopedPreauthGrant {
		t.Fatalf("legacy grant %q diverges from internal/launchintent.ScopedPreauthGrant %q: not one source of truth", legacyGrant[0], launchintent.ScopedPreauthGrant)
	}
	found = false
	for i, arg := range fullstackArgs {
		if arg == "--allowedTools" && i+1 < len(fullstackArgs) && fullstackArgs[i+1] == launchintent.ScopedPreauthGrant {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("fullstack seat args %v do not carry the two-token grant %q; legacy would preauth this eligible worker with the same value", fullstackArgs, launchintent.ScopedPreauthGrant)
	}

	// gh#748: SessionName is an expected new-path-only diff, not a parity
	// gap. Legacy has no concept of managed-plan naming at all (no field,
	// no argv it ever emits for it) -- unlike CWD/Handle/Args above, which
	// all have a direct legacy equivalent this test holds to the same
	// value, SessionName's "legacy equivalent" is simply absent by design.
	// buildIntentInput still resolves it deterministically for every claude
	// seat regardless of whether naming ends up in compiled argv (that
	// gate is AllowedArgumentForms, exercised by
	// TestLaunchapiBackendRequestsNamedSeatsOnlyWhenAllowedArgumentFormsIncludeName).
	for _, seat := range input.Seats {
		switch seat.Handle {
		case "fullstack", "lead":
			want := opts.Workstream + "/" + seat.Handle
			if seat.SessionName != want {
				t.Fatalf("claude seat %q SessionName = %q, want %q (new-path-only, no legacy equivalent)", seat.Handle, seat.SessionName, want)
			}
		case "senior-dev":
			if seat.SessionName != "" {
				t.Fatalf("codex seat %q SessionName = %q, want empty: codex never gets managed-plan naming", seat.Handle, seat.SessionName)
			}
		}
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
	if _, err := b.launch(tm, opts); err == nil || !strings.Contains(err.Error(), "stale subject_digest") {
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

	if _, err := b.launch(tm, opts); err != nil && strings.Contains(err.Error(), "stale subject_digest") {
		t.Fatalf("launch with a matching ExpectedSubjectDigest wrongly refused as stale: %v", err)
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

// TestLaunchapiBackendComposedCommandStripsIdentityEnvByDiff proves the
// composed tmux pane command line carries `env -u <key>` for exactly the env
// var names adoptionseam.SanitizeEnv stripped (gh#735) -- computed as the
// diff between the parent env and the seam's sanitized output, never a
// hardcoded list, so the backend and the seam cannot silently drift apart.
func TestLaunchapiBackendComposedCommandStripsIdentityEnvByDiff(t *testing.T) {
	before := []string{"AM_ROOT=x", "AM_BASE_ROOT=y", "AM_ROOT_ID=z", "AM_BASE_ROOT_ID=w", "AM_SESSION=v", "AM_ME=keep", "PATH=/usr/bin"}
	after := []string{"AM_ME=keep", "PATH=/usr/bin"}

	stripped := sanitizedIdentityVarNames(before, after)
	want := []string{"AM_ROOT", "AM_BASE_ROOT", "AM_ROOT_ID", "AM_BASE_ROOT_ID", "AM_SESSION"}
	gotSet, wantSet := map[string]bool{}, map[string]bool{}
	for _, k := range stripped {
		gotSet[k] = true
	}
	for _, k := range want {
		wantSet[k] = true
	}
	if len(gotSet) != len(wantSet) {
		t.Fatalf("sanitizedIdentityVarNames = %v, want exactly %v", stripped, want)
	}
	for k := range wantSet {
		if !gotSet[k] {
			t.Fatalf("sanitizedIdentityVarNames missing %q: got %v", k, stripped)
		}
	}
	if gotSet["AM_ME"] {
		t.Fatalf("sanitizedIdentityVarNames wrongly stripped AM_ME (present in both before and after): %v", stripped)
	}

	cmd := shellCommandFromArgv([]string{"/usr/bin/claude", "--permission-mode", "auto"}, nil, stripped)
	for _, key := range want {
		if !strings.Contains(cmd, "-u "+key) {
			t.Fatalf("composed command missing env -u %s: %q", key, cmd)
		}
	}
	if strings.Contains(cmd, "-u AM_ME") {
		t.Fatalf("composed command wrongly strips AM_ME: %q", cmd)
	}
	if !strings.HasPrefix(cmd, "env ") {
		t.Fatalf("composed command does not start with the env -u prefix: %q", cmd)
	}
	if !strings.Contains(cmd, "/usr/bin/claude") {
		t.Fatalf("composed command lost the argv: %q", cmd)
	}
}

// TestLaunchapiBackendComposedCommandOmitsEnvPrefixWhenNothingStripped proves
// the env -u prefix is only emitted when something was actually stripped, so
// a no-op sanitize (already-clean parent env) doesn't add pointless noise to
// every pane command.
func TestLaunchapiBackendComposedCommandOmitsEnvPrefixWhenNothingStripped(t *testing.T) {
	cmd := shellCommandFromArgv([]string{"/usr/bin/claude"}, nil, nil)
	if strings.HasPrefix(cmd, "env ") {
		t.Fatalf("composed command has an env prefix with nothing stripped: %q", cmd)
	}
}

// TestLegacyBackendRetainsWorkerPreauthDuringDualRun proves the legacy
// preauth composer is completely unaffected by gh#733: an eligible Claude
// worker in an orchestrated team still gets the equals-joined
// Bash(gh pr create:*) grant, byte-identical to pre-v2.30.0.
func TestLegacyBackendRetainsWorkerPreauthDuringDualRun(t *testing.T) {
	project := t.TempDir()
	cfg := team.Team{
		Orchestrated: true,
		Lead:         "lead",
		Members: []team.Member{
			{Role: "lead", Binary: "claude", Handle: "lead", Session: "s"},
			{Role: "fullstack", Binary: "claude", Handle: "fullstack", Session: "s"},
		},
	}
	if err := team.Write(project, cfg); err != nil {
		t.Fatal(err)
	}
	childArgs := defaultChildArgsForBinaryWithTrust("claude", trustModeApproveForMe)
	out, actions, injected := applyClaudeWorkerPreauth(project, team.DefaultProfile, "fullstack", "claude", "s", childArgs, true)
	if !injected {
		t.Fatalf("eligible worker did not get launcher-owned preauth: out=%v actions=%v", out, actions)
	}
	want := "--allowedTools=Bash(gh pr create:*)"
	found := false
	for _, arg := range out {
		if arg == want {
			found = true
		}
	}
	if !found {
		t.Fatalf("legacy composed args=%v, want %q present (dual-run must not touch legacy preauth)", out, want)
	}
}
