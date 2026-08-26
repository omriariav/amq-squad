package cli

import (
	"strings"
	"testing"

	"github.com/avivsinai/agent-message-queue/launchapi"

	"github.com/omriariav/amq-squad/v2/internal/team"
)

// TestLaunchapiBackendOptInOnly proves resolveTeamLaunchBackend -- the single
// selection seam executeTeamLaunch calls -- only ever returns the launchapi
// backend when --launch-via launchapi is explicit, and reproduces the
// pre-gh#733 lookup (same map, same error text) whenever it is absent or
// "auto". This is the byte-identical-legacy-behavior guarantee gh#733 names.
func TestLaunchapiBackendOptInOnly(t *testing.T) {
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

	for _, launchVia := range []string{"", "auto", "AUTO"} {
		got, err := resolveTeamLaunchBackend(teamLaunchOptions{Terminal: "tmux", LaunchVia: launchVia})
		if err != nil {
			t.Fatalf("LaunchVia=%q: unexpected error: %v", launchVia, err)
		}
		if got.Name() != fake.Name() {
			t.Fatalf("LaunchVia=%q selected %q, want the legacy-registered %q (auto must never select launchapi)", launchVia, got.Name(), fake.Name())
		}
	}

	got, err := resolveTeamLaunchBackend(teamLaunchOptions{Terminal: "tmux", LaunchVia: "launchapi"})
	if err != nil {
		t.Fatalf("explicit launchapi: %v", err)
	}
	if got.Name() != "launchapi" {
		t.Fatalf("explicit --launch-via launchapi selected %q", got.Name())
	}

	if _, err := resolveTeamLaunchBackend(teamLaunchOptions{Terminal: "iterm2", LaunchVia: "launchapi"}); err == nil || !strings.Contains(err.Error(), "requires --terminal tmux") {
		t.Fatalf("--launch-via launchapi with a non-tmux terminal: err=%v, want a tmux-only refusal", err)
	}

	if _, err := resolveTeamLaunchBackend(teamLaunchOptions{Terminal: "tmux", LaunchVia: "bogus"}); err == nil || !strings.Contains(err.Error(), `unsupported --launch-via "bogus"`) {
		t.Fatalf("unknown --launch-via value: err=%v", err)
	}

	// Legacy error text and the legacy map lookup are unchanged for an
	// unsupported --terminal when --launch-via is absent.
	if _, err := resolveTeamLaunchBackend(teamLaunchOptions{Terminal: "nope"}); err == nil ||
		err.Error() != `unsupported terminal "nope": supported terminals: `+strings.Join(registeredTeamLaunchTerminals(), ", ") {
		t.Fatalf("legacy unsupported-terminal error text changed: %v", err)
	}
}

func launchapiTestTeam() team.Team {
	return team.Team{
		Project:      "/proj",
		Orchestrated: true,
		Lead:         "lead",
		Members: []team.Member{
			{Role: "lead", Binary: "claude", Handle: "lead", Session: "s"},
			{Role: "fullstack", Binary: "claude", Handle: "fullstack", Session: "s", CWD: "/proj-fullstack-wt"},
			{Role: "senior-dev", Binary: "codex", Handle: "senior-dev", Session: "s", CWD: "/proj-senior-dev-wt"},
		},
	}
}

func launchapiTestPreflights(baseRoot string) []agentLaunchPreflight {
	return []agentLaunchPreflight{
		{Role: "lead", Handle: "lead", CWD: "/proj", Root: "/proj/.agent-mail/s", BaseRoot: baseRoot, Workstream: "s"},
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
	tm := launchapiTestTeam()
	opts := teamLaunchOptions{Workstream: "s", Trust: trustModeApproveForMe}

	if _, err := b.buildIntentInput(tm, opts, launchapiTestPreflights("")); err == nil {
		t.Fatal("empty base_root: buildIntentInput accepted it")
	}

	if _, err := b.buildIntentInput(tm, opts, launchapiTestPreflights("/proj/.agent-mail")); err != nil {
		t.Fatalf("non-empty base_root: buildIntentInput: %v", err)
	}
}

// TestLaunchapiBackendSurfacesRequiredActionsAsOperatorGates proves every
// RequiredActionV1 kind launchapi v0.70.0 defines becomes one gate/<topic>
// question thread to the configured operator, and that no decision is ever
// auto-selected: surfaceRequiredActionsAsOperatorGates never constructs a
// DecisionV1, so nothing downstream can silently answer for the operator.
func TestLaunchapiBackendSurfacesRequiredActionsAsOperatorGates(t *testing.T) {
	project, _, _ := seedNotifyProject(t, team.DefaultOperator())
	calls := withAMQCommandSeams(t, amqEnv{Root: ".agent-mail/{session}", BaseRoot: ".agent-mail"}, "Sent msg-gate to user\n")

	tm, err := team.ReadProfile(project, team.DefaultProfile)
	if err != nil {
		t.Fatalf("read seeded team: %v", err)
	}
	tm.Project = project
	opts := teamLaunchOptions{Profile: team.DefaultProfile, Workstream: "s"}

	actions := []launchapi.RequiredActionV1{
		{ActionID: "a1", Kind: launchapi.RequiredActionTrustConfirmation, AllowedDecisions: []launchapi.DecisionChoiceV1{launchapi.DecisionTrustExactSubject, launchapi.DecisionDeny}, ReasonCode: "new_subject"},
		{ActionID: "a2", Kind: launchapi.RequiredActionStaleConversation, AllowedDecisions: []launchapi.DecisionChoiceV1{launchapi.DecisionFreshOnce, launchapi.DecisionAbort}, ReasonCode: "conversation_stale"},
		{ActionID: "a3", Kind: launchapi.RequiredActionRebindConfirmation, AllowedDecisions: []launchapi.DecisionChoiceV1{launchapi.DecisionCloseOld, launchapi.DecisionLeaveOld}, ReasonCode: "rebind_detected"},
		{ActionID: "a4", Kind: launchapi.RequiredActionUnsupportedCapability, AllowedDecisions: []launchapi.DecisionChoiceV1{launchapi.DecisionAcceptDegraded, launchapi.DecisionAbort}, ReasonCode: "capability_gap"},
	}

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

// TestLaunchapiBackendDualRunParity proves the new backend resolves the same
// per-seat cwd and handle as the legacy tmux backend for an identical team +
// options, and that the two backends' argv diverge ONLY on the two documented
// gh#732 guarantees (no --allowedTools, no approvals_reviewer) -- any other
// divergence would be an undocumented drift this test catches.
func TestLaunchapiBackendDualRunParity(t *testing.T) {
	b := launchapiTeamLaunchBackend{}
	tm := launchapiTestTeam()
	opts := teamLaunchOptions{Workstream: "s", Trust: trustModeApproveForMe}
	preflights := launchapiTestPreflights("/proj/.agent-mail")

	input, err := b.buildIntentInput(tm, opts, preflights)
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
	// launchintent.Compile still contain approvals_reviewer (sanitization is
	// launchintent's job, exercised by TestCompileIntentDropsApprovalsReviewerOnNewPathOnly);
	// what must hold here is that buildIntentInput's own resolution matches
	// the legacy composer's resolution byte-for-byte before sanitization, so
	// the only later divergence is the two documented guarantees.
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
}

// TestLaunchapiBackendHasNoWorkerPreauth pins gh#733's documented,
// accepted dual-run limitation: a launchapi-composed seat never carries the
// extra Bash(gh pr create:*) preauth grant legacy's applyClaudeWorkerPreauth
// injects, because buildIntentInput never calls that legacy launcher-policy
// path at all. This is intentional (see #296/#733's KNOWN LIMITATION note),
// not a regression to "fix".
func TestLaunchapiBackendHasNoWorkerPreauth(t *testing.T) {
	b := launchapiTeamLaunchBackend{}
	tm := launchapiTestTeam()
	opts := teamLaunchOptions{Workstream: "s", Trust: trustModeApproveForMe}
	input, err := b.buildIntentInput(tm, opts, launchapiTestPreflights("/proj/.agent-mail"))
	if err != nil {
		t.Fatalf("buildIntentInput: %v", err)
	}
	for _, seat := range input.Seats {
		for _, arg := range seat.Args {
			if strings.HasPrefix(arg, "--allowedTools") {
				t.Fatalf("seat %q carries worker preauth %q; launchapi seats must have none during dual-run", seat.Handle, arg)
			}
		}
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
