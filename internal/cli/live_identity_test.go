package cli

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/bootstrapack"
	"github.com/omriariav/amq-squad/v2/internal/launch"
	"github.com/omriariav/amq-squad/v2/internal/liveidentity"
)

func liveIdentityResolverFixture(t *testing.T) (liveIdentityScope, liveIdentityResolverDeps) {
	t.Helper()
	project, err := liveidentity.CanonicalProject(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	scope := liveIdentityScope{Project: project, Profile: "review", Session: "s", Handle: "dev"}
	terminal := &launch.TerminalInfo{Backend: "tmux", Session: "tmux-s", WindowID: "@1", PaneID: "%2"}
	rec := launch.Record{Role: "dev", Handle: "dev", Binary: "codex", Model: "gpt-5", AgentPID: 101, Session: "s", TeamProfile: "review",
		PreparedRunGeneration: "generation", PreparedRunDigest: "digest", WakeInjectMode: "raw", Terminal: terminal,
		BootstrapExpectation: &bootstrapack.Expectation{LaunchID: "launch-1"}}
	managed := managedLiveLaunch{Record: rec, AgentDir: "/mail/agents/dev", Root: "/mail"}
	prepared := preparedLiveActor{Project: project, Profile: "review", Session: "s", Handle: "dev", Generation: "generation", Digest: "digest", Role: "dev", Binary: "codex", Model: "gpt-5"}
	key := liveidentity.Key{Project: project, Profile: "review", Session: "s", Handle: "dev", PreparedGeneration: "generation", PreparedDigest: "digest", LaunchID: "launch-1"}
	wake := liveidentity.WakeConsumer{PID: 202, Handle: "dev", Target: "%2", RecordID: "/mail/agents/dev/.wake.lock", RecordDigest: "sha256:wake", LaunchID: "launch-1"}
	observed := observedLiveActor{WakePID: 202, WakeRecordID: wake.RecordID, WakeRecordHash: wake.RecordDigest,
		Identity: liveidentity.Observed{Key: key, PID: 101, Binary: "codex", Model: "gpt-5", Terminal: liveidentity.Terminal{Backend: "tmux", Session: "tmux-s", WindowID: "@1", PaneID: "%2"}, WakeConsumers: []liveidentity.WakeConsumer{wake}}}
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
