package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/bootstrapack"
	"github.com/omriariav/amq-squad/v2/internal/launch"
	"github.com/omriariav/amq-squad/v2/internal/liveidentity"
	"github.com/omriariav/amq-squad/v2/internal/team"
	"github.com/omriariav/amq-squad/v2/internal/tmuxpane"
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
		PreparedRunGeneration: "generation", PreparedRunDigest: "digest", WakeInjectMode: "raw", WakePID: 202,
		WakeRecordID: "/mail/agents/dev/.wake.lock", WakeRecordDigest: "sha256:wake", Terminal: terminal,
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

func TestCompletePreparedIdentityRequiresVerifiedResolverProjection(t *testing.T) {
	previous := resolveRuntimeLiveIdentityNow
	t.Cleanup(func() { resolveRuntimeLiveIdentityNow = previous })
	resolveRuntimeLiveIdentityNow = func(liveIdentityScope) (liveidentity.Result, error) {
		return liveidentity.Result{}, nil
	}
	result, required, err := verifyRuntimeActionWithRecord("dispatch", t.TempDir(), team.DefaultProfile, "s", "dev", launch.Record{
		PreparedRunGeneration: "g", PreparedRunDigest: "d", PreparedRunLaunchAttempt: "a",
	})
	if !required || err == nil || result.Verified != nil || result.Recovery != liveidentity.RecoveryAction || !strings.Contains(err.Error(), "no verified identity") {
		t.Fatalf("required=%v result=%+v err=%v", required, result, err)
	}
}

func TestReadManagedLiveLaunchTypesMissingRosterHandleAsUnmanaged(t *testing.T) {
	project := t.TempDir()
	if err := team.WriteProfile(project, team.DefaultProfile, team.Team{Project: project, Members: []team.Member{{Role: "cto", Handle: "cto", Binary: "codex"}}}); err != nil {
		t.Fatal(err)
	}
	_, err := readManagedLiveLaunch(liveIdentityScope{Project: project, Profile: team.DefaultProfile, Session: "s", Handle: "outsider"})
	var unmanaged unmanagedLiveActorError
	if !errors.As(err, &unmanaged) || unmanaged.Handle != "outsider" {
		t.Fatalf("missing roster handle error = %T %v", err, err)
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

func TestLiveIdentityProductionObservationRejectsWakeLockRewrittenAfterLaunch(t *testing.T) {
	project, err := liveidentity.CanonicalProject(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(project, ".agent-mail", "prepared")
	agentDir := filepath.Join(root, "agents", "dev")
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeLock := func(pid int) {
		raw, err := json.Marshal(wakeLockFile{PID: pid, Root: root, Started: time.Now().UTC()})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(wakeLockPath(agentDir), raw, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	writeLock(202)
	initial, _, err := readWakeRecordBinding(agentDir)
	if err != nil {
		t.Fatal(err)
	}
	terminal := &launch.TerminalInfo{Backend: "tmux", Target: "new-window", Session: "tmux-s", WindowID: "@1", PaneID: "%2"}
	rec := launch.Record{CWD: project, Root: root, Role: "dev", Handle: "dev", Binary: "codex", Model: "gpt-5", AgentPID: 101,
		Session: "s", TeamProfile: "review", PreparedRunGeneration: "generation", PreparedRunDigest: "digest", PreparedRunLaunchAttempt: "attempt",
		WakeInjectMode: "raw", WakePID: initial.PID, WakeRecordID: initial.RecordID, WakeRecordDigest: initial.RecordDigest,
		Terminal: terminal, Tmux: &launch.TmuxInfo{Target: "new-window", Session: "tmux-s", WindowID: "@1", PaneID: "%2"},
		BootstrapExpectation: &bootstrapack.Expectation{LaunchID: "launch-1"}}
	writeLock(303)
	previousInspector := statusPaneInspector
	statusPaneInspector = func(id string) (tmuxpane.TmuxPane, bool) {
		return tmuxpane.TmuxPane{PaneID: id, PID: 10, CWD: project}, id == "%2"
	}
	t.Cleanup(func() { statusPaneInspector = previousInspector })
	scope := liveIdentityScope{Project: project, Profile: "review", Session: "s", Handle: "dev"}
	probe := duplicateLaunchProbe{
		PIDAlive: func(pid int) bool { return pid == 101 || pid == 202 || pid == 303 },
		ProcessMatch: func(pid int, predicate func(string) bool) bool {
			if pid == 101 {
				return predicate("codex --model gpt-5")
			}
			return predicate("amq wake --me dev --root " + root)
		},
		Now: time.Now,
	}
	deps := liveIdentityResolverDeps{
		ReadLaunch: func(liveIdentityScope) (managedLiveLaunch, error) {
			return managedLiveLaunch{Record: rec, AgentDir: agentDir, Root: root}, nil
		},
		ResolvePrepared: func(liveIdentityScope, managedLiveLaunch) (preparedLiveActor, error) {
			return preparedLiveActor{Project: project, Profile: "review", Session: "s", Handle: "dev", Generation: "generation", Digest: "digest", Role: "dev", Binary: "codex", Model: "gpt-5"}, nil
		},
		Observe: observeManagedLiveActor,
		Probe:   probe,
		ChildrenIndex: func() (func(int) []int, error) {
			return func(pid int) []int { return map[int][]int{10: {101}}[pid] }, nil
		},
	}
	result, err := resolveVerifiedLiveIdentityWithDeps(scope, deps)
	if err == nil || result.Verified != nil || result.Recovery != liveidentity.RecoveryAction || !strings.Contains(err.Error(), "launch PID") {
		t.Fatalf("rewritten wake lock verified: result=%+v err=%v", result, err)
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

func verifiedPaneProcessPIDFromDeliveryPath(t *testing.T) int {
	t.Helper()
	const paneID = "%7"

	previousRun, previousInspect, previousOutput := tmuxRunCommand, inspectPaneExact, tmuxOutputCommand
	t.Cleanup(func() {
		tmuxRunCommand, inspectPaneExact, tmuxOutputCommand = previousRun, previousInspect, previousOutput
	})

	var delivered []string
	tmuxRunCommand = func(name string, args ...string) error {
		delivered = append([]string{name}, args...)
		return nil
	}
	if err := deliverPaneCommand(paneID, "codex --model gpt-5"); err != nil {
		t.Fatalf("deliver pane-process command: %v", err)
	}
	if got, want := strings.Join(delivered, " "), "tmux respawn-pane -k -t %7 codex --model gpt-5"; got != want {
		t.Fatalf("delivery path = %q, want #577 pane-process path %q", got, want)
	}

	inspectPaneExact = func(id string) tmuxpane.PaneInspection {
		return tmuxpane.PaneInspection{
			State: tmuxpane.PaneInspectionFound,
			Pane:  tmuxpane.TmuxPane{Pane: id, PaneID: id, PID: 4242},
		}
	}
	tmuxOutputCommand = func(string, ...string) (string, error) {
		return paneID + "\t0\n", nil
	}
	panePIDText, err := verifyPaneProcessLaunched(paneID)
	if err != nil {
		t.Fatalf("verify pane-process delivery: %v", err)
	}
	panePID, err := strconv.Atoi(panePIDText)
	if err != nil {
		t.Fatalf("parse verified pane pid %q: %v", panePIDText, err)
	}
	return panePID
}

func TestObserveManagedLiveActorAcceptsPaneProcessRecordFromDeliveryPath(t *testing.T) {
	const paneID = "%7"
	panePID := verifiedPaneProcessPIDFromDeliveryPath(t)

	previousStatusInspect := statusPaneInspector
	t.Cleanup(func() { statusPaneInspector = previousStatusInspect })

	project := t.TempDir()
	root := filepath.Join(project, ".agent-mail", "review", "s")
	agentDir := filepath.Join(root, "agents", "dev")
	terminal := &launch.TerminalInfo{
		Backend: "tmux", Target: "new-window", Session: "tmux-s",
		WindowID: "@1", PaneID: paneID,
	}
	rec := launch.Record{
		CWD: project, Root: root, Role: "dev", Handle: "dev", Binary: "codex", Model: "gpt-5",
		AgentPID: panePID, Session: "s", TeamProfile: "review",
		PreparedRunGeneration: "generation", PreparedRunDigest: "digest",
		NoWakeReason: "test fixture", Terminal: terminal,
		Tmux:                 &launch.TmuxInfo{Target: "new-window", Session: "tmux-s", WindowID: "@1", PaneID: paneID},
		BootstrapExpectation: &bootstrapack.Expectation{LaunchID: "launch-1"},
	}
	// Persist and read the same launch.json shape that agent up writes. The PID
	// comes from #577's delivery/verification seam above rather than an invented
	// equal pair in a hand-built live-identity fixture.
	if err := launch.Write(agentDir, rec); err != nil {
		t.Fatalf("write pane-process launch record: %v", err)
	}
	readRecord, err := launch.Read(agentDir)
	if err != nil {
		t.Fatalf("read pane-process launch record: %v", err)
	}
	rec = readRecord

	statusPaneInspector = func(id string) (tmuxpane.TmuxPane, bool) {
		return tmuxpane.TmuxPane{Pane: id, PaneID: id, PID: panePID, CWD: project}, id == paneID
	}
	childrenSnapshotCalled := false
	observed, err := observeManagedLiveActor(
		liveIdentityScope{Project: project, Profile: "review", Session: "s", Handle: "dev"},
		managedLiveLaunch{Record: rec, AgentDir: agentDir, Root: root},
		duplicateLaunchProbe{
			PIDAlive: func(pid int) bool { return pid == panePID },
			ProcessMatch: func(pid int, predicate func(string) bool) bool {
				return pid == panePID && predicate("codex --model gpt-5")
			},
			Now: time.Now,
		},
		func() (func(int) []int, error) {
			childrenSnapshotCalled = true
			return nil, fmt.Errorf("pane-process equality must not need a process snapshot")
		},
	)
	if err != nil {
		t.Fatalf("observe #577 pane-process launch record: %v", err)
	}
	if observed.Identity.PID != panePID {
		t.Fatalf("observed pid = %d, want verified pane-process pid %d", observed.Identity.PID, panePID)
	}
	if childrenSnapshotCalled {
		t.Fatal("pane-process equality called the descendant snapshot")
	}
}

func TestStopClosePanesAcceptsPaneProcessRecordFromDeliveryPath(t *testing.T) {
	panePID := verifiedPaneProcessPIDFromDeliveryPath(t)
	configured, member, record, pane, project := completeDownPaneFixture(t)
	record.AgentPID = panePID
	if err := launch.Write(filepath.Join(record.Root, "agents", record.Handle), record); err != nil {
		t.Fatalf("write #577 pane-process launch record: %v", err)
	}
	pane.PID = panePID

	var events []string
	deps := PaneCleanupDependencies{
		Inspect: func(string) tmuxpane.PaneInspection {
			events = append(events, "inspect")
			return tmuxpane.PaneInspection{State: tmuxpane.PaneInspectionFound, Pane: pane}
		},
		ChildrenIndex: func() (func(int) []int, error) {
			t.Fatal("#577 pane-process equality must not require a process snapshot")
			return nil, fmt.Errorf("unreachable")
		},
		Close: func(string) error {
			events = append(events, "close")
			return nil
		},
	}
	report := terminateMember(
		configured, project, team.DefaultProfile, member, "issue-465",
		eventTerminator{events: &events},
		downFakeProbe(map[int]bool{panePID: true}, map[int]bool{panePID: true}),
		nil, true, deps,
	)
	if report.Status != downStatusStopped || report.Pane.Outcome != PaneCleanupClosed {
		t.Fatalf("stop --close-panes report=%+v, want stopped and pane closed", report)
	}
	if got, want := strings.Join(events, ","), "inspect,signal,inspect,close"; got != want {
		t.Fatalf("stop --close-panes events=%q, want %q", got, want)
	}
}

func TestPaneCleanupRejectsUnrelatedPaneProcessPID(t *testing.T) {
	panePID := verifiedPaneProcessPIDFromDeliveryPath(t)
	configured, member, record, pane, project := completeDownPaneFixture(t)
	record.AgentPID = panePID + 1
	if err := launch.Write(filepath.Join(record.Root, "agents", record.Handle), record); err != nil {
		t.Fatalf("write unrelated agent launch record: %v", err)
	}
	pane.PID = panePID

	closeCalls := 0
	report := terminateMember(
		configured, project, team.DefaultProfile, member, "issue-465",
		eventTerminator{events: &[]string{}},
		downFakeProbe(map[int]bool{record.AgentPID: true}, map[int]bool{record.AgentPID: true}),
		nil, true, PaneCleanupDependencies{
			Inspect: func(string) tmuxpane.PaneInspection {
				return tmuxpane.PaneInspection{State: tmuxpane.PaneInspectionFound, Pane: pane}
			},
			ChildrenIndex: func() (func(int) []int, error) {
				return func(int) []int { return nil }, nil
			},
			Close: func(string) error {
				closeCalls++
				return nil
			},
		},
	)
	if report.Status != downStatusStopped || report.Pane.Outcome != PaneCleanupPreservedIdentityUnconfirmed {
		t.Fatalf("unrelated cleanup report=%+v, want signaled with pane preserved", report)
	}
	if closeCalls != 0 {
		t.Fatalf("unrelated pane-process cleanup closed pane %d times", closeCalls)
	}
	if len(report.Pane.Mismatches) != 1 || report.Pane.Mismatches[0].Field != "agent_pid_ancestry" {
		t.Fatalf("unrelated pane-process mismatch=%+v", report.Pane.Mismatches)
	}
}

func TestStrictDescendantRemainsStrictForPaneCleanup(t *testing.T) {
	if strictDescendant(func(int) []int { return nil }, 10, 10) {
		t.Fatal("strictDescendant accepted equality; pane-cleanup semantics must remain strict")
	}
}

func TestPaneProcessOrDescendantRejectsNonPositivePIDs(t *testing.T) {
	for _, tc := range []struct {
		name     string
		panePID  int
		agentPID int
	}{
		{name: "zero equality", panePID: 0, agentPID: 0},
		{name: "negative equality", panePID: -1, agentPID: -1},
		{name: "missing pane pid", panePID: 0, agentPID: 1},
		{name: "missing agent pid", panePID: 1, agentPID: 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if paneProcessOrDescendant(nil, tc.panePID, tc.agentPID) {
				t.Fatalf("paneProcessOrDescendant(nil, %d, %d) accepted a non-positive PID", tc.panePID, tc.agentPID)
			}
		})
	}
}

func TestVerifyAgentPaneLineageRejectsUnrelatedAndAcceptsDescendantPID(t *testing.T) {
	tree := map[int][]int{10: {20}, 20: {30}, 40: {101}}
	children := func() (func(int) []int, error) { return func(pid int) []int { return tree[pid] }, nil }
	if err := verifyAgentPaneLineage(10, 101, children); err == nil || !strings.Contains(err.Error(), "neither recorded pane process") {
		t.Fatalf("unexpected lineage result: %v", err)
	}
	if err := verifyAgentPaneLineage(10, 30, children); err != nil {
		t.Fatalf("valid descendant rejected: %v", err)
	}
	if err := verifyAgentPaneLineage(10, 30, func() (func(int) []int, error) { return nil, fmt.Errorf("denied") }); err == nil || !strings.Contains(err.Error(), "unavailable") {
		t.Fatalf("unavailable lineage did not fail closed: %v", err)
	}
}
