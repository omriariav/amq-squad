package cli

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/bootstrapack"
	"github.com/omriariav/amq-squad/v2/internal/launch"
	"github.com/omriariav/amq-squad/v2/internal/liveidentity"
	"github.com/omriariav/amq-squad/v2/internal/team"
)

func liveIdentityResolverFixture(t *testing.T) (liveIdentityScope, liveIdentityResolverDeps) {
	t.Helper()
	project, err := liveidentity.CanonicalProject(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	scope := liveIdentityScope{Project: project, Profile: "review", Session: "s", Handle: "dev"}
	terminal := &launch.TerminalInfo{Backend: "tmux", Target: "new-window", Session: "tmux-s", WindowID: "@1", PaneID: "%2"}
	rec := launch.Record{Role: "dev", Handle: "dev", Binary: "codex", Model: "gpt-5", AgentPID: 101, Session: "s", TeamProfile: "review",
		PreparedRunGeneration: "generation", PreparedRunDigest: "digest", WakeInjectMode: "raw", Terminal: terminal,
		Tmux:                 &launch.TmuxInfo{Target: "new-window", Session: "tmux-s", WindowID: "@1", PaneID: "%2"},
		BootstrapExpectation: &bootstrapack.Expectation{LaunchID: "launch-1"}}
	managed := managedLiveLaunch{Record: rec, AgentDir: "/mail/agents/dev", Root: "/mail"}
	prepared := preparedLiveActor{Project: project, Profile: "review", Session: "s", Handle: "dev", Generation: "generation", Digest: "digest", Role: "dev", Binary: "codex", Model: "gpt-5"}
	key := liveidentity.Key{Project: project, Profile: "review", Session: "s", Handle: "dev", PreparedGeneration: "generation", PreparedDigest: "digest", LaunchID: "launch-1"}
	wake := liveidentity.WakeConsumer{PID: 202, Handle: "dev", Target: "%2", RecordID: "/mail/agents/dev/.wake.lock", RecordDigest: "sha256:wake", LaunchID: "launch-1"}
	observed := observedLiveActor{WakePID: 202, WakeRecordID: wake.RecordID, WakeRecordHash: wake.RecordDigest,
		Identity: liveidentity.Observed{Key: key, PID: 101, Binary: "codex", Model: "gpt-5", Terminal: liveidentity.Terminal{Backend: "tmux", Target: "new-window", Session: "tmux-s", WindowID: "@1", PaneID: "%2"}, WakeConsumers: []liveidentity.WakeConsumer{wake}}}
	deps := liveIdentityResolverDeps{
		ReadLaunch:      func(liveIdentityScope) (managedLiveLaunch, error) { return managed, nil },
		ResolvePrepared: func(liveIdentityScope, managedLiveLaunch) (preparedLiveActor, error) { return prepared, nil },
		Observe: func(liveIdentityScope, managedLiveLaunch, duplicateLaunchProbe, func() (func(int) []int, error)) (observedLiveActor, error) {
			return observed, nil
		},
		Probe: duplicateLaunchProbe{PIDAlive: func(int) bool { return true }, ProcessMatch: func(int, func(string) bool) bool { return true }, Now: time.Now},
		ChildrenIndex: func() (func(int) []int, error) {
			return func(pid int) []int { return map[int][]int{10: {101}}[pid] }, nil
		},
	}
	return scope, deps
}

func TestLiveIdentityResolverAuthorizerPreflightAndTargetPostflight(t *testing.T) {
	scope, deps := liveIdentityResolverFixture(t)
	for name, run := range map[string]func(liveIdentityScope, liveIdentityResolverDeps) (liveidentity.Result, error){
		"authorizer-preflight": verifyLiveIdentityAuthorizerWithDeps,
		"target-postflight":    verifyLiveIdentityTargetWithDeps,
	} {
		t.Run(name, func(t *testing.T) {
			result, err := run(scope, deps)
			if err != nil || result.Verified == nil {
				t.Fatalf("result=%+v err=%v", result, err)
			}
		})
	}
}

func TestValidateLiveIdentityTerminalProjectionPreservesNativeTarget(t *testing.T) {
	rec := launch.Record{AgentTTY: "/dev/ttys001", Terminal: &launch.TerminalInfo{
		Backend: "iterm2", Target: "new-window", Session: "prepared", WindowID: "101", TabID: "tab-1", SessionID: "session-1", TTY: "/dev/ttys001",
	}}
	if err := validateLiveIdentityTerminalProjection(rec); err != nil {
		t.Fatalf("valid native terminal rejected: %v", err)
	}
	want := liveidentity.Terminal{Backend: "iterm2", Target: "new-window", Session: "prepared", WindowID: "101", TabID: "tab-1", SessionID: "session-1", TTY: "/dev/ttys001"}
	if got := liveIdentityTerminal(rec); got != want {
		t.Fatalf("native terminal projection = %+v, want %+v", got, want)
	}
}

func TestValidateLiveIdentityTerminalProjectionRejectsTmuxNativeContradiction(t *testing.T) {
	rec := launch.Record{
		AgentTTY: "/dev/ttys001",
		Terminal: &launch.TerminalInfo{Backend: "iterm2", Target: "new-window", Session: "prepared", WindowID: "101", TabID: "tab-1", SessionID: "session-1", TTY: "/dev/ttys001"},
		Tmux:     &launch.TmuxInfo{Target: "new-window", Session: "prepared", WindowID: "@1", PaneID: "%2"},
	}
	if err := validateLiveIdentityTerminalProjection(rec); err == nil || !strings.Contains(err.Error(), "contradictory tmux projection") {
		t.Fatalf("tmux/native contradiction was not rejected: %v", err)
	}
}

func TestObserveManagedLiveActorAcceptsBoundNativeProcessIdentity(t *testing.T) {
	rec := launch.Record{
		CWD: "/repo", Role: "dev", Binary: "codex", Model: "gpt-5", AgentPID: 101,
		AgentTTY:              "/dev/ttys001",
		PreparedRunGeneration: "generation", PreparedRunDigest: "digest", NoWakeReason: "native injection unsupported",
		BootstrapExpectation: &bootstrapack.Expectation{LaunchID: "launch-1"},
		Terminal:             &launch.TerminalInfo{Backend: "iterm2", Target: "new-window", Session: "prepared", WindowID: "101", TabID: "tab-1", SessionID: "session-1", TTY: "/dev/ttys001"},
	}
	probe := duplicateLaunchProbe{
		PIDAlive: func(pid int) bool { return pid == 101 },
		ProcessMatch: func(pid int, predicate func(string) bool) bool {
			return pid == 101 && predicate("codex --model gpt-5")
		},
		ProcessTTY: func(pid int) (string, bool) { return "/dev/ttys001", pid == 101 },
		Now:        time.Now,
	}
	observed, err := observeManagedLiveActor(liveIdentityScope{Project: "/repo", Profile: "review", Session: "prepared", Handle: "dev"}, managedLiveLaunch{Record: rec}, probe,
		func() (func(int) []int, error) {
			t.Fatal("native identity must not require a tmux process-lineage snapshot")
			return nil, nil
		})
	if err != nil {
		t.Fatalf("observe native identity: %v", err)
	}
	if observed.Identity.PID != 101 || observed.Identity.Terminal != liveIdentityTerminal(rec) {
		t.Fatalf("native observation = %+v", observed)
	}
}

func TestObserveManagedLiveActorRejectsReusedNativePIDOnWrongTTY(t *testing.T) {
	rec := launch.Record{
		CWD: "/repo", Role: "dev", Binary: "codex", Model: "gpt-5", AgentPID: 101, AgentTTY: "/dev/ttys001",
		BootstrapExpectation: &bootstrapack.Expectation{LaunchID: "launch-1"},
		Terminal:             &launch.TerminalInfo{Backend: "iterm2", Target: "new-window", Session: "prepared", WindowID: "101", TabID: "tab-1", SessionID: "session-1", TTY: "/dev/ttys001"},
	}
	probe := duplicateLaunchProbe{
		PIDAlive: func(pid int) bool { return pid == 101 },
		ProcessMatch: func(pid int, predicate func(string) bool) bool {
			return pid == 101 && predicate("codex --model gpt-5")
		},
		ProcessTTY: func(pid int) (string, bool) { return "/dev/ttys009", pid == 101 },
		Now:        time.Now,
	}
	if _, err := observeManagedLiveActor(liveIdentityScope{Project: "/repo", Profile: "review", Session: "prepared", Handle: "dev"}, managedLiveLaunch{Record: rec}, probe, nil); err == nil || !strings.Contains(err.Error(), "TTY differs") {
		t.Fatalf("reused native PID on wrong TTY verified: %v", err)
	}
}

func TestPreparedIdentityMarkersPreserveOrdinaryBootstrapCompatibility(t *testing.T) {
	ordinary := launch.Record{BootstrapExpectation: &bootstrapack.Expectation{Required: true, LaunchID: "ordinary-launch"}}
	if launchRecordClaimsPreparedIdentity(ordinary) {
		t.Fatal("ordinary bootstrap expectation must not claim prepared-generation authority")
	}
	for name, rec := range map[string]launch.Record{
		"generation only": {PreparedRunGeneration: "g"},
		"digest only":     {PreparedRunDigest: "d"},
		"attempt only":    {PreparedRunLaunchAttempt: "a"},
		"complete":        {PreparedRunGeneration: "g", PreparedRunDigest: "d", PreparedRunLaunchAttempt: "a"},
	} {
		t.Run(name, func(t *testing.T) {
			if !launchRecordClaimsPreparedIdentity(rec) {
				t.Fatal("any prepared tuple field must opt into fail-closed verification")
			}
		})
	}
}

func TestPartialPreparedIdentityCannotDowngradeToLegacy(t *testing.T) {
	previous := resolveRuntimeLiveIdentityNow
	t.Cleanup(func() { resolveRuntimeLiveIdentityNow = previous })
	resolveRuntimeLiveIdentityNow = func(liveIdentityScope) (liveidentity.Result, error) {
		t.Fatal("partial tuple must fail before invoking the live resolver")
		return liveidentity.Result{}, nil
	}
	result, required, err := verifyRuntimeActionWithRecord("send", t.TempDir(), team.DefaultProfile, "s", "dev", launch.Record{PreparedRunGeneration: "g"})
	if !required || err == nil || result.Recovery != liveidentity.RecoveryAction || !strings.Contains(err.Error(), "prepared identity tuple is incomplete") {
		t.Fatalf("required=%v result=%+v err=%v", required, result, err)
	}
}

func TestResolvePreparedLiveActorUsesAuthoritativeActiveStagedClaimLifecycle(t *testing.T) {
	project, _, token, claim := preparedStagedProjectionFixture(t, "codex")
	canonical, err := liveidentity.CanonicalProject(project)
	if err != nil {
		t.Fatal(err)
	}
	rec := stagedProjectionRecord(t, project, token, claim)
	managed := managedLiveLaunch{Record: rec}
	targetScope := liveIdentityScope{Project: canonical, Profile: team.DefaultProfile, Session: "prepared", Handle: "qa", AllowAdmittedStaged: true}
	actor, err := resolvePreparedLiveActor(targetScope, managed)
	if err != nil || actor.Binary != claim.Effective.Binary || actor.Model != claim.Effective.Model || actor.Role != claim.Role || actor.Handle != claim.Handle {
		t.Fatalf("admitted target actor=%+v err=%v claim=%+v", actor, err, claim)
	}
	if _, err := resolvePreparedLiveActor(liveIdentityScope{Project: canonical, Profile: team.DefaultProfile, Session: "prepared", Handle: "qa"}, managed); err == nil || !strings.Contains(err.Error(), "admitted but not consumed") {
		t.Fatalf("already-live resolver accepted unconsumed claim: %v", err)
	}
	launchToken := token
	launchToken.LaunchAttempt = claim.ClaimID
	if err := consumePreparedRunStagedClaim(project, team.DefaultProfile, "prepared", launchToken, "qa", "qa"); err != nil {
		t.Fatal(err)
	}
	if actor, err = resolvePreparedLiveActor(liveIdentityScope{Project: canonical, Profile: team.DefaultProfile, Session: "prepared", Handle: "qa"}, managed); err != nil || actor.Binary != claim.Effective.Binary {
		t.Fatalf("consumed live actor=%+v err=%v", actor, err)
	}
}

func TestResolvePreparedLiveActorRejectsStaleFirstReplacedClaim(t *testing.T) {
	project, _, token, first := preparedStagedProjectionFixture(t, "codex")
	second, err := admitPreparedRunStagedClaim(project, team.DefaultProfile, "prepared", token, preparedRunStagedAdmissionRequest{
		Role: "qa", Handle: "qa", AuthorizingRole: "cto", AuthorizingHandle: "cto", ActorMode: team.ActorModeReview,
		SupersedesClaimID: first.ClaimID, LifecycleReason: "replace stale first claim",
	})
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := liveidentity.CanonicalProject(project)
	if err != nil {
		t.Fatal(err)
	}
	scope := liveIdentityScope{Project: canonical, Profile: team.DefaultProfile, Session: "prepared", Handle: "qa", AllowAdmittedStaged: true}
	if _, err := resolvePreparedLiveActor(scope, managedLiveLaunch{Record: stagedProjectionRecord(t, project, token, first)}); err == nil || !strings.Contains(err.Error(), "exact authoritative claim") {
		t.Fatalf("stale first claim verified: %v", err)
	}
	actor, err := resolvePreparedLiveActor(scope, managedLiveLaunch{Record: stagedProjectionRecord(t, project, token, second)})
	if err != nil || actor.Binary != second.Effective.Binary || actor.Model != second.Effective.Model {
		t.Fatalf("replacement actor=%+v err=%v", actor, err)
	}
}

func TestLiveIdentityResolverFailsClosedOnLayerDrift(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*liveIdentityScope, *liveIdentityResolverDeps)
		want   string
	}{
		{name: "stale generation", want: "authority keys", mutate: func(_ *liveIdentityScope, deps *liveIdentityResolverDeps) {
			previous := deps.Observe
			deps.Observe = func(s liveIdentityScope, m managedLiveLaunch, p duplicateLaunchProbe, c func() (func(int) []int, error)) (observedLiveActor, error) {
				o, _ := previous(s, m, p, c)
				o.Identity.Key.PreparedGeneration = "stale"
				return o, nil
			}
		}},
		{name: "wrong pane", want: "terminal identities", mutate: func(_ *liveIdentityScope, deps *liveIdentityResolverDeps) {
			previous := deps.Observe
			deps.Observe = func(s liveIdentityScope, m managedLiveLaunch, p duplicateLaunchProbe, c func() (func(int) []int, error)) (observedLiveActor, error) {
				o, _ := previous(s, m, p, c)
				o.Identity.Terminal.PaneID = "%wrong"
				return o, nil
			}
		}},
		{name: "wrong wake record", want: "record identity", mutate: func(_ *liveIdentityScope, deps *liveIdentityResolverDeps) {
			previous := deps.Observe
			deps.Observe = func(s liveIdentityScope, m managedLiveLaunch, p duplicateLaunchProbe, c func() (func(int) []int, error)) (observedLiveActor, error) {
				o, _ := previous(s, m, p, c)
				o.Identity.WakeConsumers[0].RecordID = "/mail/agents/sibling/.wake.lock"
				return o, nil
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			scope, deps := liveIdentityResolverFixture(t)
			tc.mutate(&scope, &deps)
			result, err := resolveVerifiedLiveIdentityWithDeps(scope, deps)
			if err == nil || result.Verified != nil || result.Recovery != liveidentity.RecoveryAction || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("result=%+v err=%v", result, err)
			}
		})
	}
}

func TestLiveIdentityResolverRejectsScopeLaunchAndProcessFailures(t *testing.T) {
	for _, tc := range []struct {
		name string
		fail func(*liveIdentityScope, *liveIdentityResolverDeps)
		want string
	}{
		{name: "wrong profile", want: "wrong profile", fail: func(_ *liveIdentityScope, deps *liveIdentityResolverDeps) {
			deps.ReadLaunch = func(liveIdentityScope) (managedLiveLaunch, error) {
				return managedLiveLaunch{}, fmt.Errorf("wrong profile")
			}
		}},
		{name: "wrong handle", want: "wrong handle", fail: func(_ *liveIdentityScope, deps *liveIdentityResolverDeps) {
			deps.ReadLaunch = func(liveIdentityScope) (managedLiveLaunch, error) {
				return managedLiveLaunch{}, fmt.Errorf("wrong handle")
			}
		}},
		{name: "missing launch record", want: "no such file", fail: func(_ *liveIdentityScope, deps *liveIdentityResolverDeps) {
			deps.ReadLaunch = func(liveIdentityScope) (managedLiveLaunch, error) {
				return managedLiveLaunch{}, fmt.Errorf("no such file")
			}
		}},
		{name: "dead or reused pid", want: "dead or reused", fail: func(_ *liveIdentityScope, deps *liveIdentityResolverDeps) {
			deps.Observe = func(liveIdentityScope, managedLiveLaunch, duplicateLaunchProbe, func() (func(int) []int, error)) (observedLiveActor, error) {
				return observedLiveActor{}, fmt.Errorf("dead or reused pid")
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			scope, deps := liveIdentityResolverFixture(t)
			tc.fail(&scope, &deps)
			result, err := resolveVerifiedLiveIdentityWithDeps(scope, deps)
			if err == nil || result.Verified != nil || result.Recovery != liveidentity.RecoveryAction || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("result=%+v err=%v", result, err)
			}
		})
	}
}

func TestVerifyAgentPaneLineageRejectsReusedAndSiblingPID(t *testing.T) {
	tree := map[int][]int{10: {20}, 20: {30}, 40: {101}}
	children := func() (func(int) []int, error) { return func(pid int) []int { return tree[pid] }, nil }
	if err := verifyAgentPaneLineage(10, 101, children); err == nil || !strings.Contains(err.Error(), "not a descendant") {
		t.Fatalf("unexpected lineage result: %v", err)
	}
	if err := verifyAgentPaneLineage(10, 30, children); err != nil {
		t.Fatalf("valid descendant rejected: %v", err)
	}
	if err := verifyAgentPaneLineage(10, 30, func() (func(int) []int, error) { return nil, fmt.Errorf("denied") }); err == nil || !strings.Contains(err.Error(), "unavailable") {
		t.Fatalf("unavailable lineage did not fail closed: %v", err)
	}
}
