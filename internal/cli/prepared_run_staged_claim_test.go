package cli

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/v2/internal/bootstrapack"
	"github.com/omriariav/amq-squad/v2/internal/launch"
	"github.com/omriariav/amq-squad/v2/internal/liveidentity"
	"github.com/omriariav/amq-squad/v2/internal/team"
)

func stagedTransitionStates(t *testing.T, project string, token preparedRunToken, role, claimID string) []string {
	t.Helper()
	dir := preparedRunStagedTransitionsDir(project, team.DefaultProfile, "prepared", token.Generation, role, claimID)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	states := make([]string, 0, len(entries))
	for _, entry := range entries {
		data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		var transition preparedRunStagedTransition
		if err := json.Unmarshal(data, &transition); err != nil {
			t.Fatal(err)
		}
		states = append(states, transition.State)
	}
	return states
}

func seedPreparedStagedAuthorizer(t *testing.T, project string, token preparedRunToken) {
	t.Helper()
	terminal, err := readPreparedRunEvent(preparedRunTerminalEventPath(project, team.DefaultProfile, "prepared", token.Generation))
	if err != nil {
		t.Fatal(err)
	}
	recordToken := token
	recordToken.LaunchAttempt = terminal.LaunchAttempt
	env, err := resolveAMQEnvForTeamLaunchProfile(project, team.DefaultProfile, "prepared", "cto")
	if err != nil {
		t.Fatal(err)
	}
	agentDir := filepath.Join(absoluteAMQRoot(project, env.Root), "agents", "cto")
	rec := launch.Record{
		Schema: launch.SchemaVersion, CWD: project, Binary: "codex", Role: "cto", Handle: "cto",
		Session: "prepared", TeamProfile: team.DefaultProfile, TeamHome: project, Model: "test-model", AgentPID: os.Getpid(), NoWakeReason: "test fixture",
		Tmux:                 &launch.TmuxInfo{Session: "fixture", WindowID: "@1", PaneID: "%1"},
		BootstrapExpectation: &bootstrapack.Expectation{Required: true, LaunchID: "initial-launch-id", PromptVersion: bootstrapack.PromptVersion},
	}
	applyPreparedRunTokenToRecord(&rec, recordToken)
	if err := launch.Write(agentDir, rec); err != nil {
		t.Fatal(err)
	}
	canonicalProject, err := liveidentity.CanonicalProject(project)
	if err != nil {
		t.Fatal(err)
	}
	verified := liveidentity.Verified{
		Key:  liveidentity.Key{Project: canonicalProject, Profile: team.DefaultProfile, Session: "prepared", Handle: "cto", PreparedGeneration: token.Generation, PreparedDigest: token.ManifestDigest, LaunchID: "initial-launch-id"},
		Role: "cto", Binary: "codex", Model: "test-model", PID: os.Getpid(), WakePolicy: liveidentity.WakeDisabled, WakeMode: liveidentity.WakeDisabled,
		Terminal: liveidentity.Terminal{Backend: "tmux", Session: "fixture", WindowID: "@1", PaneID: "%1"},
	}
	oldVerify := preparedRunStagedVerifyAuthorizer
	preparedRunStagedVerifyAuthorizer = func(_, _, _, _ string) (liveidentity.Result, error) {
		return liveidentity.Result{SchemaVersion: liveidentity.SchemaVersion, Verified: &verified}, nil
	}
	t.Cleanup(func() { preparedRunStagedVerifyAuthorizer = oldVerify })
}

func TestPreparedStagedClaimBindsNarrowedIdentityAuthorizerAndParentLaunch(t *testing.T) {
	project, manifest, token := preparedRunStagedStateFixture(t)
	seedPreparedStagedAuthorizer(t, project, token)
	claim, err := admitPreparedRunStagedClaim(project, team.DefaultProfile, "prepared", token, preparedRunStagedAdmissionRequest{
		Role: "qa", Handle: "qa", AuthorizingRole: "cto", AuthorizingHandle: "cto",
		ActorMode: team.ActorModeReview, LifecycleReason: "independent review only",
	})
	if err != nil {
		t.Fatal(err)
	}
	if claim.Accepted.ActorMode != team.ActorModeImplementation || claim.Effective.ActorMode != team.ActorModeReview {
		t.Fatalf("claim permission narrowing accepted=%s effective=%s", claim.Accepted.ActorMode, claim.Effective.ActorMode)
	}
	if claim.Authorizer.Role != "cto" || claim.Authorizer.Handle != "cto" || claim.Authorizer.LaunchID != "initial-launch-id" || claim.Authorizer.ParentLaunchAttempt == "" {
		t.Fatalf("claim authorizer=%+v", claim.Authorizer)
	}
	if claim.Authorizer.Verified.PID != os.Getpid() || claim.Authorizer.Verified.Terminal.PaneID != "%1" || claim.Authorizer.VerificationResult.Verified == nil || claim.Authorizer.VerificationDigest == "" || claim.Authorizer.VerifiedAt.IsZero() {
		t.Fatalf("claim lacks digest-bound verified authorizer identity: %+v", claim.Authorizer)
	}
	if claim.GenerationRef.Generation != manifest.Generation || claim.Namespace != team.DefaultProfile+"/prepared" || claim.LaunchStrategy.Target != manifest.Topology.Target || !claim.Lifecycle.RequiresTargetAbsent {
		t.Fatalf("claim generation/topology/lifecycle=%+v", claim)
	}
	if len(claim.BootstrapDigest) != 64 || claim.BootstrapDigest == manifest.BootstrapDigests[claim.Role] {
		t.Fatalf("claim does not bind the narrowed effective bootstrap independently: claim=%q prepared=%q", claim.BootstrapDigest, manifest.BootstrapDigests[claim.Role])
	}
	if claim.Effective.Binary != claim.Accepted.Binary || claim.Effective.Model != claim.Accepted.Model || claim.Effective.ToolProfile != claim.Accepted.ToolProfile {
		t.Fatalf("narrowing changed runtime/tool identity: accepted=%+v effective=%+v", claim.Accepted, claim.Effective)
	}
	active, err := currentPreparedRunStagedClaim(project, team.DefaultProfile, "prepared", token, "qa")
	if err != nil || active.ClaimID != claim.ClaimID {
		t.Fatalf("active claim=%+v err=%v", active, err)
	}
	pointer, err := readPreparedRunStagedClaimPointer(preparedRunStagedClaimActivePath(project, team.DefaultProfile, "prepared", token.Generation, "qa"))
	if err != nil || pointer.ClaimID != claim.ClaimID || pointer.ClaimDigest == "" || pointer.LifecycleState != stagedClaimStateAdmitted || !stagedClaimIdentityIsExact(claim, pointer.EffectiveIdentity) {
		t.Fatalf("authoritative current surface=%+v err=%v", pointer, err)
	}
}

func TestPreparedStagedClaimRejectsEmptyOrMismatchedVerifiedAuthorizer(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*liveidentity.Result)
	}{
		{name: "empty", mutate: func(result *liveidentity.Result) { result.Verified = &liveidentity.Verified{} }},
		{name: "wrong pane", mutate: func(result *liveidentity.Result) { result.Verified.Terminal.PaneID = "%99" }},
		{name: "wrong pid", mutate: func(result *liveidentity.Result) { result.Verified.PID++ }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			project, _, token := preparedRunStagedStateFixture(t)
			seedPreparedStagedAuthorizer(t, project, token)
			baseVerify := preparedRunStagedVerifyAuthorizer
			preparedRunStagedVerifyAuthorizer = func(project, profile, session, handle string) (liveidentity.Result, error) {
				result, err := baseVerify(project, profile, session, handle)
				tc.mutate(&result)
				return result, err
			}
			_, err := admitPreparedRunStagedClaim(project, team.DefaultProfile, "prepared", token, preparedRunStagedAdmissionRequest{
				Role: "qa", Handle: "qa", AuthorizingRole: "cto", AuthorizingHandle: "cto", ActorMode: team.ActorModeReview,
			})
			if err == nil || !strings.Contains(err.Error(), "verified staged authorizer") {
				t.Fatalf("verified authorizer mismatch error=%v", err)
			}
		})
	}
}

func TestPreparedStagedClaimReplacementIsExplicitAndLeavesHistory(t *testing.T) {
	project, _, token := preparedRunStagedStateFixture(t)
	seedPreparedStagedAuthorizer(t, project, token)
	request := preparedRunStagedAdmissionRequest{
		Role: "qa", Handle: "qa", AuthorizingRole: "cto", AuthorizingHandle: "cto", ActorMode: team.ActorModeReview,
	}
	first, err := admitPreparedRunStagedClaim(project, team.DefaultProfile, "prepared", token, request)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := admitPreparedRunStagedClaim(project, team.DefaultProfile, "prepared", token, request); err == nil || !strings.Contains(err.Error(), "replacement must name") {
		t.Fatalf("implicit replacement error=%v", err)
	}
	request.SupersedesClaimID = first.ClaimID
	request.LifecycleReason = "fresh independent review process"
	second, err := admitPreparedRunStagedClaim(project, team.DefaultProfile, "prepared", token, request)
	if err != nil {
		t.Fatal(err)
	}
	if second.ClaimID == first.ClaimID || second.Lifecycle.SupersedesClaimID != first.ClaimID {
		t.Fatalf("replacement=%+v first=%+v", second, first)
	}
	for _, claim := range []preparedRunStagedClaim{first, second} {
		path := preparedRunStagedClaimArtifactPath(project, team.DefaultProfile, "prepared", token.Generation, "qa", claim.ClaimID)
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("immutable claim %s missing: %v", claim.ClaimID, err)
		}
	}
	active, err := currentPreparedRunStagedClaim(project, team.DefaultProfile, "prepared", token, "qa")
	if err != nil || active.ClaimID != second.ClaimID {
		t.Fatalf("replacement active=%+v err=%v", active, err)
	}
	if states := stagedTransitionStates(t, project, token, "qa", first.ClaimID); !containsString(states, stagedClaimStateSuperseded) {
		t.Fatalf("first claim transitions=%v, want superseded", states)
	}
	if _, err := os.Stat(preparedRunStagedClaimPath(project, team.DefaultProfile, "prepared", token.Generation, "qa")); !os.IsNotExist(err) {
		t.Fatalf("stale write-once compatibility claim must not be authoritative: %v", err)
	}
}

func TestPreparedStagedClaimRejectsPermissionWidening(t *testing.T) {
	accepted := preparedRunMemberIdentity{Role: "qa", Handle: "qa", ActorMode: team.ActorModeReview}
	if _, err := narrowedPreparedStagedIdentity(accepted, team.ActorModeImplementation); err == nil || !strings.Contains(err.Error(), "permission widening refused") {
		t.Fatalf("permission widening error=%v", err)
	}
	got, err := narrowedPreparedStagedIdentity(accepted, team.ActorModeReview)
	if err != nil || got.ActorMode != team.ActorModeReview {
		t.Fatalf("exact review identity=%+v err=%v", got, err)
	}
}

func TestPreparedStagedClaimReviewProjectionIsNativeReadOnlyForCodexAndClaude(t *testing.T) {
	for _, tc := range []struct {
		binary string
		args   []string
		want   []string
		deny   []string
	}{
		{binary: "codex", args: []string{"--dangerously-bypass-approvals-and-sandbox=true", "--sandbox", "workspace-write", "--", "literal --dangerously-bypass-approvals-and-sandbox"}, want: []string{"--sandbox", "read-only", "--ask-for-approval", "on-request", "literal --dangerously-bypass-approvals-and-sandbox"}, deny: []string{"--dangerously-bypass-approvals-and-sandbox=true", "workspace-write"}},
		{binary: "claude", args: []string{"--dangerously-skip-permissions=true", "--permission-mode", "auto", "--allowed-tools=Bash(*)", "--", "literal --dangerously-skip-permissions"}, want: []string{"--permission-mode", "plan", "literal --dangerously-skip-permissions"}, deny: []string{"--dangerously-skip-permissions=true", "--allowed-tools"}},
	} {
		t.Run(tc.binary, func(t *testing.T) {
			accepted := preparedRunMemberIdentity{
				Role: "reviewer", Handle: "reviewer", Binary: tc.binary, ActorMode: team.ActorModeImplementation,
				TaskOwnership: "durable_task_assignee", Trust: trustModeTrusted, EffectiveArgs: tc.args,
				PermissionAllowlist: []string{"Bash(git push:*)"}, LauncherAuthority: []string{"Bash(gh pr create:*)"},
			}
			effective, err := narrowedPreparedStagedIdentity(accepted, team.ActorModeReview)
			if err != nil {
				t.Fatal(err)
			}
			joined := strings.Join(effective.EffectiveArgs, " ")
			for _, want := range tc.want {
				if !strings.Contains(joined, want) {
					t.Fatalf("review args %q missing %q", joined, want)
				}
			}
			for _, deny := range tc.deny {
				if strings.Contains(joined, deny) {
					t.Fatalf("review args %q retain %q", joined, deny)
				}
			}
			if effective.ActorMode != team.ActorModeReview || effective.TaskOwnership != "read_only_review" || effective.Trust != trustModeSandboxed || len(effective.PermissionAllowlist) != 0 || len(effective.LauncherAuthority) != 0 || !effective.NoPreauthorize {
				t.Fatalf("review authority not narrowed: %+v", effective)
			}
		})
	}
}

func TestPreparedStagedClaimActivationFaultLeavesInspectableInactiveClaim(t *testing.T) {
	project, _, token := preparedRunStagedStateFixture(t)
	seedPreparedStagedAuthorizer(t, project, token)
	oldHook := preparedRunStagedClaimBeforeActivate
	preparedRunStagedClaimBeforeActivate = func() error { return errors.New("injected activation interruption") }
	t.Cleanup(func() { preparedRunStagedClaimBeforeActivate = oldHook })
	_, err := admitPreparedRunStagedClaim(project, team.DefaultProfile, "prepared", token, preparedRunStagedAdmissionRequest{
		Role: "qa", Handle: "qa", AuthorizingRole: "cto", AuthorizingHandle: "cto", ActorMode: team.ActorModeReview,
	})
	if err == nil || !strings.Contains(err.Error(), "injected activation interruption") {
		t.Fatalf("activation interruption error=%v", err)
	}
	if _, err := os.Stat(preparedRunStagedClaimActivePath(project, team.DefaultProfile, "prepared", token.Generation, "qa")); !os.IsNotExist(err) {
		t.Fatalf("interrupted claim became active: %v", err)
	}
	entries, err := os.ReadDir(preparedRunStagedClaimsDir(project, team.DefaultProfile, "prepared", token.Generation, "qa"))
	if err != nil || len(entries) != 1 {
		t.Fatalf("inactive claim not inspectable: entries=%v err=%v", entries, err)
	}
	claimID := strings.TrimSuffix(entries[0].Name(), ".json")
	if states := stagedTransitionStates(t, project, token, "qa", claimID); !containsString(states, stagedClaimStateFailed) {
		t.Fatalf("interrupted claim transitions=%v, want failed", states)
	}
}

func TestPreparedStagedClaimLaunchConsumptionIsSingleUse(t *testing.T) {
	project, _, token := preparedRunStagedStateFixture(t)
	seedPreparedStagedAuthorizer(t, project, token)
	claim, err := admitPreparedRunStagedClaim(project, team.DefaultProfile, "prepared", token, preparedRunStagedAdmissionRequest{
		Role: "qa", Handle: "qa", AuthorizingRole: "cto", AuthorizingHandle: "cto", ActorMode: team.ActorModeReview,
	})
	if err != nil {
		t.Fatal(err)
	}
	launchToken := token
	launchToken.LaunchAttempt = claim.ClaimID
	if err := consumePreparedRunStagedClaimLocked(project, team.DefaultProfile, "prepared", launchToken, "qa", "qa"); err != nil {
		t.Fatal(err)
	}
	if err := consumePreparedRunStagedClaimLocked(project, team.DefaultProfile, "prepared", launchToken, "qa", "qa"); err == nil || !strings.Contains(err.Error(), "replay refused") {
		t.Fatalf("staged claim replay error=%v", err)
	}
	pointer, err := readPreparedRunStagedClaimPointer(preparedRunStagedClaimActivePath(project, team.DefaultProfile, "prepared", token.Generation, "qa"))
	if err != nil || pointer.LifecycleState != stagedClaimStateConsumed || pointer.Consumption == nil || pointer.Consumption.EventDigest == "" {
		t.Fatalf("consumed current surface=%+v err=%v", pointer, err)
	}
}

func TestPreparedStagedClaimAbandonmentIsInspectableAndAllowsExplicitReplacement(t *testing.T) {
	project, _, token := preparedRunStagedStateFixture(t)
	seedPreparedStagedAuthorizer(t, project, token)
	first, err := admitPreparedRunStagedClaim(project, team.DefaultProfile, "prepared", token, preparedRunStagedAdmissionRequest{
		Role: "qa", Handle: "qa", AuthorizingRole: "cto", AuthorizingHandle: "cto", ActorMode: team.ActorModeReview,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := abandonPreparedRunStagedClaim(project, team.DefaultProfile, "prepared", token, "qa", first.ClaimID, "operator cancelled before launch"); err != nil {
		t.Fatal(err)
	}
	if _, err := currentPreparedRunStagedClaim(project, team.DefaultProfile, "prepared", token, "qa"); err == nil || !strings.Contains(err.Error(), "abandoned") {
		t.Fatalf("abandoned claim remained active: %v", err)
	}
	if states := stagedTransitionStates(t, project, token, "qa", first.ClaimID); !containsString(states, stagedClaimStateAbandoned) {
		t.Fatalf("abandoned claim transitions=%v", states)
	}
	second, err := admitPreparedRunStagedClaim(project, team.DefaultProfile, "prepared", token, preparedRunStagedAdmissionRequest{
		Role: "qa", Handle: "qa", AuthorizingRole: "cto", AuthorizingHandle: "cto", ActorMode: team.ActorModeReview,
		SupersedesClaimID: first.ClaimID, LifecycleReason: "new reviewer process",
	})
	if err != nil || second.ClaimID == first.ClaimID {
		t.Fatalf("replacement after abandonment=%+v err=%v", second, err)
	}
}

func TestPreparedStagedClaimRejectsExistingLiveTarget(t *testing.T) {
	project, _, token := preparedRunStagedStateFixture(t)
	seedPreparedStagedAuthorizer(t, project, token)
	old := preparedRunStagedTargetAbsent
	preparedRunStagedTargetAbsent = func(_, _, _, _ string) error { return errors.New("live target consumer") }
	t.Cleanup(func() { preparedRunStagedTargetAbsent = old })
	_, err := admitPreparedRunStagedClaim(project, team.DefaultProfile, "prepared", token, preparedRunStagedAdmissionRequest{
		Role: "qa", Handle: "qa", AuthorizingRole: "cto", AuthorizingHandle: "cto", ActorMode: team.ActorModeReview,
	})
	if err == nil || !strings.Contains(err.Error(), "target qa is not absent") {
		t.Fatalf("live target admission error=%v", err)
	}
}

func TestPreparedStagedClaimPointerFaultLeavesPriorCommittedAndNewFailed(t *testing.T) {
	project, _, token := preparedRunStagedStateFixture(t)
	seedPreparedStagedAuthorizer(t, project, token)
	request := preparedRunStagedAdmissionRequest{
		Role: "qa", Handle: "qa", AuthorizingRole: "cto", AuthorizingHandle: "cto", ActorMode: team.ActorModeReview,
	}
	first, err := admitPreparedRunStagedClaim(project, team.DefaultProfile, "prepared", token, request)
	if err != nil {
		t.Fatal(err)
	}
	oldReplace := preparedRunStagedReplaceCurrent
	preparedRunStagedReplaceCurrent = func(_ string, _ []byte) error { return errors.New("injected current pointer failure") }
	t.Cleanup(func() { preparedRunStagedReplaceCurrent = oldReplace })
	request.SupersedesClaimID = first.ClaimID
	_, err = admitPreparedRunStagedClaim(project, team.DefaultProfile, "prepared", token, request)
	if err == nil || !strings.Contains(err.Error(), "current pointer failure") {
		t.Fatalf("pointer fault error=%v", err)
	}
	active, err := currentPreparedRunStagedClaim(project, team.DefaultProfile, "prepared", token, "qa")
	if err != nil || active.ClaimID != first.ClaimID {
		t.Fatalf("prior claim lost committed-active state: active=%+v err=%v", active, err)
	}
	entries, err := os.ReadDir(preparedRunStagedClaimsDir(project, team.DefaultProfile, "prepared", token.Generation, "qa"))
	if err != nil || len(entries) != 2 {
		t.Fatalf("claim history entries=%v err=%v", entries, err)
	}
	for _, entry := range entries {
		claimID := strings.TrimSuffix(entry.Name(), ".json")
		if claimID == first.ClaimID {
			continue
		}
		if states := stagedTransitionStates(t, project, token, "qa", claimID); !containsString(states, stagedClaimStateFailed) {
			t.Fatalf("uncommitted replacement transitions=%v, want failed", states)
		}
	}
}

func TestPreparedStagedClaimConsumptionRecoversPointerFault(t *testing.T) {
	project, _, token := preparedRunStagedStateFixture(t)
	seedPreparedStagedAuthorizer(t, project, token)
	claim, err := admitPreparedRunStagedClaim(project, team.DefaultProfile, "prepared", token, preparedRunStagedAdmissionRequest{
		Role: "qa", Handle: "qa", AuthorizingRole: "cto", AuthorizingHandle: "cto", ActorMode: team.ActorModeReview,
	})
	if err != nil {
		t.Fatal(err)
	}
	launchToken := token
	launchToken.LaunchAttempt = claim.ClaimID
	oldReplace := preparedRunStagedReplaceCurrent
	fail := true
	preparedRunStagedReplaceCurrent = func(path string, data []byte) error {
		if fail {
			fail = false
			return errors.New("injected consumption pointer failure")
		}
		return durableReplace(path, data)
	}
	t.Cleanup(func() { preparedRunStagedReplaceCurrent = oldReplace })
	if err := consumePreparedRunStagedClaimLocked(project, team.DefaultProfile, "prepared", launchToken, "qa", "qa"); err == nil || !strings.Contains(err.Error(), "consumption pointer failure") {
		t.Fatalf("first consumption error=%v", err)
	}
	if err := consumePreparedRunStagedClaimLocked(project, team.DefaultProfile, "prepared", launchToken, "qa", "qa"); err != nil {
		t.Fatalf("recover consumption pointer: %v", err)
	}
	pointer, err := readPreparedRunStagedClaimPointer(preparedRunStagedClaimActivePath(project, team.DefaultProfile, "prepared", token.Generation, "qa"))
	if err != nil || pointer.LifecycleState != stagedClaimStateConsumed || pointer.Consumption == nil {
		t.Fatalf("recovered consumption surface=%+v err=%v", pointer, err)
	}
}

func TestPreparedStagedClaimConsumptionFaultsBeforeEventAndTransitionAreRetryable(t *testing.T) {
	for _, point := range []string{"event", "transition"} {
		t.Run(point, func(t *testing.T) {
			project, _, token := preparedRunStagedStateFixture(t)
			seedPreparedStagedAuthorizer(t, project, token)
			claim, err := admitPreparedRunStagedClaim(project, team.DefaultProfile, "prepared", token, preparedRunStagedAdmissionRequest{
				Role: "qa", Handle: "qa", AuthorizingRole: "cto", AuthorizingHandle: "cto", ActorMode: team.ActorModeReview,
			})
			if err != nil {
				t.Fatal(err)
			}
			launchToken := token
			launchToken.LaunchAttempt = claim.ClaimID
			oldEvent, oldTransition := preparedRunStagedConsumptionBeforeEvent, preparedRunStagedConsumptionBeforeTransition
			failed := false
			inject := func() error {
				if !failed {
					failed = true
					return errors.New("injected " + point + " fault")
				}
				return nil
			}
			if point == "event" {
				preparedRunStagedConsumptionBeforeEvent = inject
			} else {
				preparedRunStagedConsumptionBeforeTransition = inject
			}
			t.Cleanup(func() {
				preparedRunStagedConsumptionBeforeEvent = oldEvent
				preparedRunStagedConsumptionBeforeTransition = oldTransition
			})
			if err := consumePreparedRunStagedClaimLocked(project, team.DefaultProfile, "prepared", launchToken, "qa", "qa"); err == nil {
				t.Fatal("injected consumption fault succeeded")
			}
			if err := consumePreparedRunStagedClaimLocked(project, team.DefaultProfile, "prepared", launchToken, "qa", "qa"); err != nil {
				t.Fatalf("retry after %s fault: %v", point, err)
			}
		})
	}
}

func TestPreparedStagedClaimTargetAbsenceRejectsOrphanWakeWithoutLaunchRecord(t *testing.T) {
	project, _, token := preparedRunStagedStateFixture(t)
	seedPreparedStagedAuthorizer(t, project, token)
	env, err := resolveAMQEnvForTeamLaunchProfile(project, team.DefaultProfile, "prepared", "qa")
	if err != nil {
		t.Fatal(err)
	}
	agentDir := filepath.Join(absoluteAMQRoot(project, env.Root), "agents", "qa")
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	wake := wakeLockFile{PID: os.Getpid()}
	data, err := json.Marshal(wake)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(wakeLockPath(agentDir), data, 0o644); err != nil {
		t.Fatal(err)
	}
	_, err = admitPreparedRunStagedClaim(project, team.DefaultProfile, "prepared", token, preparedRunStagedAdmissionRequest{
		Role: "qa", Handle: "qa", AuthorizingRole: "cto", AuthorizingHandle: "cto", ActorMode: team.ActorModeReview,
	})
	if err == nil || !strings.Contains(err.Error(), "live wake consumer") {
		t.Fatalf("orphan wake admission error=%v", err)
	}
}
