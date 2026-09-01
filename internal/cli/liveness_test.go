package cli

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/avivsinai/agent-message-queue/launchapi"

	"github.com/omriariav/amq-squad/v2/internal/launch"
	"github.com/omriariav/amq-squad/v2/internal/team"
	"github.com/omriariav/amq-squad/v2/internal/tmuxpane"
)

// livenessProbe builds a deterministic probe for the unification tests: a PID
// is alive iff it is in alive, and ProcessMatch is true iff the pid is in
// match. Now is fixed so presence freshness is reproducible.
func livenessProbe(alive, match map[int]bool, now time.Time) duplicateLaunchProbe {
	return duplicateLaunchProbe{
		PIDAlive:     func(pid int) bool { return alive[pid] },
		ProcessMatch: func(pid int, _ func(args string) bool) bool { return match[pid] },
		Now:          func() time.Time { return now },
	}
}

func TestStoppedLaunchRecordOverridesReusedLivePID(t *testing.T) {
	base := setupFakeAMQSessionRoots(t)
	dir := seedTeam(t, team.Team{
		Workstream: "issue-96",
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-96"},
		},
	})
	stoppedAt := time.Now().Add(-time.Minute).UTC()
	writeMemberLaunchRecord(t, base, "issue-96", "cto", launch.Record{
		CWD: dir, Binary: "codex", Role: "cto", AgentPID: 7777,
		StartedAt: time.Now().Add(-time.Hour), StoppedAt: &stoppedAt,
	})
	agentDir := filepath.Join(base, "issue-96", "agents", "cto")
	probe := livenessProbe(map[int]bool{7777: true}, map[int]bool{7777: true}, time.Now())

	live := classifyAgentLiveness(agentDir, filepath.Join(base, "issue-96"), "default", "cto", "cto", "codex", "issue-96", dir, probe)
	if live.Verdict != livenessStale || live.Status != statusStateStale || live.Live() {
		t.Fatalf("explicitly stopped record classified live after PID reuse: %+v", live)
	}
	if !strings.Contains(live.Detail, "explicitly stopped") {
		t.Fatalf("stopped classification missing lifecycle detail: %+v", live)
	}
}

func TestCanonicalClassifierRejectsReusedExternalPaneID(t *testing.T) {
	base := setupFakeAMQSessionRoots(t)
	dir := seedTeam(t, team.Team{
		Workstream: "pane-reuse",
		Members:    []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "pane-reuse"}},
	})
	writeMemberLaunchRecord(t, base, "pane-reuse", "cto", launch.Record{
		CWD: dir, Binary: "codex", Role: "cto", Handle: "cto", Session: "pane-reuse",
		External: true, Tmux: &launch.TmuxInfo{PaneID: "%7"},
		StartedAt: time.Now().Add(-time.Hour),
	})
	agentDir := filepath.Join(base, "pane-reuse", "agents", "cto")
	oldInspector := statusPaneInspector
	statusPaneInspector = func(id string) (tmuxpane.TmuxPane, bool) {
		return tmuxpane.TmuxPane{Pane: id, Title: "amq:pane-reuse:someone-else"}, id == "%7"
	}
	t.Cleanup(func() { statusPaneInspector = oldInspector })

	live := classifyAgentLiveness(
		agentDir, filepath.Join(base, "pane-reuse"), team.DefaultProfile,
		"cto", "cto", "codex", "pane-reuse", dir,
		livenessProbe(nil, nil, time.Now()),
	)
	if live.Live() || live.Status == statusStateLive || live.Status == statusStateWakeLive {
		t.Fatalf("reused external pane id classified live: %+v", live)
	}
}

// TestClassifierReplacementLive: a dead recorded PID but a live SAME-ENGINE
// pane in the member cwd yields the replacement-live verdict (status live),
// proving the verdict-level replacement detector delegates to the shared
// resolver.
func TestClassifierReplacementLive(t *testing.T) {
	base := setupFakeAMQSessionRoots(t)
	dir := seedTeam(t, team.Team{
		Workstream: "issue-96",
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-96"},
		},
	})
	writeMemberLaunchRecord(t, base, "issue-96", "cto", launch.Record{
		CWD: dir, Binary: "codex", Role: "cto", AgentPID: 4242, StartedAt: time.Now(),
	})
	agentDir := filepath.Join(base, "issue-96", "agents", "cto")
	withStubPaneLister(t, []tmuxpane.TmuxPane{
		{Session: "main", Window: "0", Pane: "3", Command: "codex", CWD: canonicalPath(dir)},
	}, nil)

	now := time.Now()
	probe := livenessProbe(map[int]bool{}, map[int]bool{}, now)

	live := classifyAgentLiveness(agentDir, filepath.Join(base, "issue-96"), "default", "cto", "cto", "codex", "issue-96", dir, probe)
	if live.Verdict != livenessReplacementLive {
		t.Fatalf("classifier verdict = %q, want replacement-live", live.Verdict)
	}
	if live.Status != statusStateLive {
		t.Fatalf("classifier status = %q, want live", live.Status)
	}
	if !strings.Contains(live.Detail, "recorded pid dead") {
		t.Fatalf("replacement detail should mention recorded pid dead, got %q", live.Detail)
	}
}

// TestClassifierPresenceLiveWhenWriterUnknown pins the conservative half of the
// zombie guard: a fresh active presence with NO writer records (or a missing
// one) still counts as presence-live, exactly as before. Only a both-present,
// both-dead case demotes it. This guards against the guard over-reaching.
func TestClassifierPresenceLiveWhenWriterUnknown(t *testing.T) {
	base := setupFakeAMQSessionRoots(t)
	dir := seedTeam(t, team.Team{
		Workstream: "issue-96",
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-96"},
		},
	})
	root := filepath.Join(base, "issue-96")
	agentDir := filepath.Join(root, "agents", "cto")

	// Fresh active presence, but NO launch.json and NO wake.lock: writers are
	// unknown, so presence must still count as live.
	now := time.Now()
	writeStatusPresence(t, base, "issue-96", "cto", presenceFile{
		Handle:   "cto",
		Status:   "active",
		LastSeen: now.Add(-10 * time.Second),
	})
	withStubPaneLister(t, nil, nil)
	probe := livenessProbe(map[int]bool{}, map[int]bool{}, now)

	live := classifyAgentLiveness(agentDir, root, "default", "cto", "cto", "codex", "issue-96", dir, probe)
	if live.Verdict != livenessPresenceLive {
		t.Fatalf("presence with unknown writers verdict = %q, want presence-live", live.Verdict)
	}
	if live.Status != statusStateLive {
		t.Fatalf("presence with unknown writers status = %q, want live", live.Status)
	}
}

// stubLaunchapiInspect replaces the launchapiInspect package var for the
// duration of the test, restoring it in cleanup.
func stubLaunchapiInspect(t *testing.T, fn func(context.Context, launchapi.InspectRequestV1) (launchapi.InspectResultV1, error)) {
	t.Helper()
	previous := launchapiInspect
	launchapiInspect = fn
	t.Cleanup(func() { launchapiInspect = previous })
}

// TestInspectLivenessUnknownFailsClosed is gh#737's first named acceptance
// test: an injected unknown session-level Inspect result never reads as
// healthy and never authorizes a mutation -- a live verdict gets capped
// below any live sub-state regardless of its own pane/presence signals.
func TestInspectLivenessUnknownFailsClosed(t *testing.T) {
	live := agentLiveness{Verdict: livenessAgentLive, Status: statusStateLive, Detail: "agent pid 123 alive (codex)"}
	got := applyLaunchapiInspectionFloor(live, launchapiInspectSignal{Outcome: launchapiInspectFloor, Evidence: "unknown"}, "cto")
	if got.Live() {
		t.Fatalf("unknown Inspect signal must never leave a verdict Live(): %+v", got)
	}
	if got.Verdict != livenessStale || got.Status != statusStateStale {
		t.Fatalf("unknown Inspect signal verdict/status = %q/%q, want stale/stale", got.Verdict, got.Status)
	}
	if !strings.Contains(got.Detail, "unknown") {
		t.Fatalf("capped detail does not name the Inspect evidence: %q", got.Detail)
	}

	// ActionRequired must cap even when State itself is not literally
	// "unknown" (e.g. a reason-coded action_required outcome).
	live2 := agentLiveness{Verdict: livenessWakeLive, Status: statusStateWakeLive}
	got2 := applyLaunchapiInspectionFloor(live2, launchapiInspectSignal{Outcome: launchapiInspectFloor, Evidence: "action_required: caller_context_corrupt"}, "cto")
	if got2.Live() {
		t.Fatalf("action_required Inspect signal must never leave a verdict Live(): %+v", got2)
	}
}

// TestInspectLivenessParityWithPaneProbeForLiveSeats is gh#737's second
// named acceptance test: for a live tmux seat, both sources agree. A
// session-level Inspect Present result corroborates but never overrides
// per-seat classification, so a seat independently confirmed live by its
// own pane/launch-record signals stays live and unchanged.
func TestInspectLivenessParityWithPaneProbeForLiveSeats(t *testing.T) {
	base := setupFakeAMQSessionRoots(t)
	dir := seedTeam(t, team.Team{
		Workstream: "issue-96",
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-96"},
		},
	})
	writeMemberLaunchRecord(t, base, "issue-96", "cto", launch.Record{
		CWD: dir, Binary: "codex", Role: "cto", AgentPID: 5555, StartedAt: time.Now(),
	})
	withStubPaneLister(t, nil, nil)
	stubLaunchapiInspect(t, func(context.Context, launchapi.InspectRequestV1) (launchapi.InspectResultV1, error) {
		return launchapi.InspectResultV1{State: "present"}, nil
	})

	probe := livenessProbe(map[int]bool{5555: true}, map[int]bool{5555: true}, time.Now())
	tm, err := team.ReadProfile(dir, team.DefaultProfile)
	if err != nil {
		t.Fatal(err)
	}
	rec := classifyMemberStatus(tm, team.DefaultProfile, tm.Members[0], "issue-96", probe)
	if rec.Status != statusStateLive {
		t.Fatalf("status = %q, want live (session Inspect present must corroborate, not override, an independently live seat)", rec.Status)
	}
	if rec.liveness.InspectSignal.Outcome != launchapiInspectPresentSignal {
		t.Fatalf("InspectSignal.Outcome = %v, want launchapiInspectPresentSignal", rec.liveness.InspectSignal.Outcome)
	}
}

// TestStatusRollupUnchangedForLegacyLaunches is gh#737's third named
// acceptance test: a session Inspect reports as not launchapi-launched
// (binding_missing) leaves legacy classification completely unchanged, and
// produces no launchapi-related warning.
func TestStatusRollupUnchangedForLegacyLaunches(t *testing.T) {
	base := setupFakeAMQSessionRoots(t)
	dir := seedTeam(t, team.Team{
		Workstream: "issue-96",
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-96"},
		},
	})
	writeMemberLaunchRecord(t, base, "issue-96", "cto", launch.Record{
		CWD: dir, Binary: "codex", Role: "cto", AgentPID: 5555, StartedAt: time.Now(),
	})
	withStubPaneLister(t, nil, nil)
	stubLaunchapiInspect(t, func(context.Context, launchapi.InspectRequestV1) (launchapi.InspectResultV1, error) {
		return launchapi.InspectResultV1{ReasonCode: "binding_missing"}, nil
	})

	probe := livenessProbe(map[int]bool{5555: true}, map[int]bool{5555: true}, time.Now())
	tm, err := team.ReadProfile(dir, team.DefaultProfile)
	if err != nil {
		t.Fatal(err)
	}
	rec := classifyMemberStatus(tm, team.DefaultProfile, tm.Members[0], "issue-96", probe)
	if rec.Status != statusStateLive || rec.Detail != "agent pid 5555 alive (codex)" {
		t.Fatalf("legacy-session status/detail changed: status=%q detail=%q", rec.Status, rec.Detail)
	}
	if rec.liveness.InspectSignal.Outcome != launchapiInspectNotApplicable {
		t.Fatalf("InspectSignal.Outcome = %v, want launchapiInspectNotApplicable for binding_missing", rec.liveness.InspectSignal.Outcome)
	}
	if warnings := statusLaunchapiInspectWarnings("issue-96", []statusRecord{rec}); len(warnings) != 0 {
		t.Fatalf("legacy launch produced a launchapi Inspect warning: %+v", warnings)
	}
}

// TestInspectAbsentConflictsWithLivePaneProbeFailsClosed is the named test
// cto's ruling added: a session Inspect reporting absent while a seat's own
// pane/launch-record probe reports live is a discrepancy between two live
// signals -- per-seat probes still run (no short-circuit on absent), and
// the conflict fails closed (capped at stale) with both sides named in the
// detail, rather than silently trusting either source.
func TestInspectAbsentConflictsWithLivePaneProbeFailsClosed(t *testing.T) {
	base := setupFakeAMQSessionRoots(t)
	dir := seedTeam(t, team.Team{
		Workstream: "issue-96",
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-96"},
		},
	})
	writeMemberLaunchRecord(t, base, "issue-96", "cto", launch.Record{
		CWD: dir, Binary: "codex", Role: "cto", AgentPID: 5555, StartedAt: time.Now(),
	})
	withStubPaneLister(t, nil, nil)
	stubLaunchapiInspect(t, func(context.Context, launchapi.InspectRequestV1) (launchapi.InspectResultV1, error) {
		return launchapi.InspectResultV1{State: "absent"}, nil
	})

	probe := livenessProbe(map[int]bool{5555: true}, map[int]bool{5555: true}, time.Now())
	tm, err := team.ReadProfile(dir, team.DefaultProfile)
	if err != nil {
		t.Fatal(err)
	}
	rec := classifyMemberStatus(tm, team.DefaultProfile, tm.Members[0], "issue-96", probe)
	if rec.Status != statusStateStale {
		t.Fatalf("status = %q, want stale (session Inspect absent conflicts with a live pane probe -- must fail closed, not silently trust either source)", rec.Status)
	}
	if !strings.Contains(rec.Detail, "absent") || !strings.Contains(rec.Detail, "live") {
		t.Fatalf("conflict detail does not name both disagreeing sides: %q", rec.Detail)
	}
	if rec.liveness.InspectSignal.Outcome != launchapiInspectAbsentSignal {
		t.Fatalf("InspectSignal.Outcome = %v, want launchapiInspectAbsentSignal", rec.liveness.InspectSignal.Outcome)
	}
}

// TestInspectErrorFallsBackToExistingSignalsWithWarning is the named test
// cto's ruling added: an Inspect CALL error (I/O, not a successful
// binding_missing result) must never clamp -- today's classification runs
// unchanged for every member -- but the failure is surfaced as a
// session-level warning naming the error, not silently swallowed.
func TestInspectErrorFallsBackToExistingSignalsWithWarning(t *testing.T) {
	base := setupFakeAMQSessionRoots(t)
	dir := seedTeam(t, team.Team{
		Workstream: "issue-96",
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-96"},
		},
	})
	writeMemberLaunchRecord(t, base, "issue-96", "cto", launch.Record{
		CWD: dir, Binary: "codex", Role: "cto", AgentPID: 5555, StartedAt: time.Now(),
	})
	withStubPaneLister(t, nil, nil)
	stubLaunchapiInspect(t, func(context.Context, launchapi.InspectRequestV1) (launchapi.InspectResultV1, error) {
		return launchapi.InspectResultV1{}, errors.New("stat .agent-mail/issue-96: permission denied")
	})

	probe := livenessProbe(map[int]bool{5555: true}, map[int]bool{5555: true}, time.Now())
	tm, err := team.ReadProfile(dir, team.DefaultProfile)
	if err != nil {
		t.Fatal(err)
	}
	rec := classifyMemberStatus(tm, team.DefaultProfile, tm.Members[0], "issue-96", probe)
	if rec.Status != statusStateLive || rec.Detail != "agent pid 5555 alive (codex)" {
		t.Fatalf("Inspect call error must not clamp: status=%q detail=%q", rec.Status, rec.Detail)
	}
	if rec.liveness.InspectSignal.Outcome != launchapiInspectCallFailed {
		t.Fatalf("InspectSignal.Outcome = %v, want launchapiInspectCallFailed", rec.liveness.InspectSignal.Outcome)
	}
	warnings := statusLaunchapiInspectWarnings("issue-96", []statusRecord{rec})
	if len(warnings) != 1 {
		t.Fatalf("warnings = %+v, want exactly one session-level warning", warnings)
	}
	if !strings.Contains(warnings[0].Detail, "permission denied") {
		t.Fatalf("warning does not name the Inspect error: %q", warnings[0].Detail)
	}
}

// TestLaunchapiInspectMemoCollapsesRosterToOneCall is the named test cto's
// review of PR #779 required: classifyAgentLivenessForRollup was calling
// launchapiSessionInspect inside the per-member/per-handle path, so a
// roster rollup called launchapi.Inspect once per member instead of once
// per session -- contradicting the approved design ("once per session").
// Every member here shares the same project/root/session Target, so the
// memo cache introduced to fix that must collapse them to exactly one real
// Inspect call, count invocations through the stubbed launchapiInspect var.
func TestLaunchapiInspectMemoCollapsesRosterToOneCall(t *testing.T) {
	base := setupFakeAMQSessionRoots(t)
	dir := seedTeam(t, team.Team{
		Workstream: "issue-96",
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-96"},
			{Role: "senior-dev", Binary: "codex", Handle: "senior-dev", Session: "issue-96"},
			{Role: "fullstack", Binary: "codex", Handle: "fullstack", Session: "issue-96"},
		},
	})
	writeMemberLaunchRecord(t, base, "issue-96", "cto", launch.Record{
		CWD: dir, Binary: "codex", Role: "cto", AgentPID: 5555, StartedAt: time.Now(),
	})
	writeMemberLaunchRecord(t, base, "issue-96", "senior-dev", launch.Record{
		CWD: dir, Binary: "codex", Role: "senior-dev", AgentPID: 5556, StartedAt: time.Now(),
	})
	writeMemberLaunchRecord(t, base, "issue-96", "fullstack", launch.Record{
		CWD: dir, Binary: "codex", Role: "fullstack", AgentPID: 5557, StartedAt: time.Now(),
	})
	withStubPaneLister(t, nil, nil)

	calls := 0
	stubLaunchapiInspect(t, func(context.Context, launchapi.InspectRequestV1) (launchapi.InspectResultV1, error) {
		calls++
		return launchapi.InspectResultV1{State: "present"}, nil
	})

	probe := livenessProbe(
		map[int]bool{5555: true, 5556: true, 5557: true},
		map[int]bool{5555: true, 5556: true, 5557: true},
		time.Now(),
	)
	tm, err := team.ReadProfile(dir, team.DefaultProfile)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range tm.Members {
		rec := classifyMemberStatus(tm, team.DefaultProfile, m, "issue-96", probe)
		if rec.Status != statusStateLive {
			t.Fatalf("member %q status = %q, want live", m.Handle, rec.Status)
		}
		if rec.liveness.InspectSignal.Outcome != launchapiInspectPresentSignal {
			t.Fatalf("member %q InspectSignal.Outcome = %v, want launchapiInspectPresentSignal", m.Handle, rec.liveness.InspectSignal.Outcome)
		}
	}
	if calls != 1 {
		t.Fatalf("launchapiInspect called %d times for %d roster members sharing one session, want exactly 1 (memo cache must collapse per-rollup)", calls, len(tm.Members))
	}
}

// TestStatusExplainsMissingLaunchRecordForLaunchapiSeats is gh#766's
// launchapi-seat Detail-wording acceptance item (from cto's ruling on
// task/t10): a launchapi-launched worker seat never writes amq-squad's own
// launch.Record, so a seat classified purely from wake/presence plus a
// meaningfully-engaged session Inspect must say so explicitly rather than
// reading as unexplained weaker evidence. No launch.json is written for
// this member -- only a wake lock -- to reproduce exactly that gap.
func TestStatusExplainsMissingLaunchRecordForLaunchapiSeats(t *testing.T) {
	base := setupFakeAMQSessionRoots(t)
	dir := seedTeam(t, team.Team{
		Workstream: "issue-96",
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-96"},
		},
	})
	root := filepath.Join(base, "issue-96")
	agentDir := filepath.Join(root, "agents", "cto")
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeWakeLock(t, agentDir, wakeLockFile{PID: 4321, Root: root, Started: time.Now()})
	withStubPaneLister(t, nil, nil)
	stubLaunchapiInspect(t, func(context.Context, launchapi.InspectRequestV1) (launchapi.InspectResultV1, error) {
		return launchapi.InspectResultV1{State: "present"}, nil
	})

	now := time.Now()
	probe := duplicateLaunchProbe{
		PIDAlive: func(pid int) bool { return pid == 4321 },
		ProcessMatch: func(pid int, predicate func(args string) bool) bool {
			return pid == 4321 && predicate("amq wake --me cto --root "+root)
		},
		Now: func() time.Time { return now },
	}

	tm, err := team.ReadProfile(dir, team.DefaultProfile)
	if err != nil {
		t.Fatalf("read team: %v", err)
	}
	rec := classifyMemberStatus(tm, team.DefaultProfile, tm.Members[0], "issue-96", probe)
	if rec.Status != statusStateWakeLive {
		t.Fatalf("status = %q, want wake-live", rec.Status)
	}
	if !strings.Contains(rec.Detail, "no launch record (launchapi-launched)") {
		t.Fatalf("detail does not explain the missing launch record: %q", rec.Detail)
	}
	if !strings.Contains(rec.Detail, "session Inspect") {
		t.Fatalf("detail does not name session Inspect as the classification source: %q", rec.Detail)
	}
	if rec.liveness.InspectSignal.Outcome != launchapiInspectPresentSignal {
		t.Fatalf("InspectSignal.Outcome = %v, want launchapiInspectPresentSignal", rec.liveness.InspectSignal.Outcome)
	}
}

// TestStatusSurfacesNotableLaunchapiObservation is gh#766's Observations
// acceptance item (cto's ruling on task/t10): a session Inspect's matched
// ParticipantObservationV1 for this handle corroborates/explains Detail and
// is surfaced structurally on statusRecord.LaunchapiObservation for --json
// consumers, but never overrides per-seat Status -- exactly the scenario
// cto named: Runnable=false alongside a seat this repo's own pane/launch
// evidence independently reports live.
func TestStatusSurfacesNotableLaunchapiObservation(t *testing.T) {
	base := setupFakeAMQSessionRoots(t)
	dir := seedTeam(t, team.Team{
		Workstream: "issue-96",
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-96"},
		},
	})
	writeMemberLaunchRecord(t, base, "issue-96", "cto", launch.Record{
		CWD: dir, Binary: "codex", Role: "cto", AgentPID: 5555, StartedAt: time.Now(),
	})
	withStubPaneLister(t, nil, nil)
	stubLaunchapiInspect(t, func(context.Context, launchapi.InspectRequestV1) (launchapi.InspectResultV1, error) {
		return launchapi.InspectResultV1{
			State: "present",
			Observations: []launchapi.ParticipantObservationV1{
				{Handle: "cto", Runnable: false, Execution: "blocked", ReasonCode: "caller_context_stale"},
			},
		}, nil
	})

	probe := livenessProbe(map[int]bool{5555: true}, map[int]bool{5555: true}, time.Now())
	tm, err := team.ReadProfile(dir, team.DefaultProfile)
	if err != nil {
		t.Fatal(err)
	}
	rec := classifyMemberStatus(tm, team.DefaultProfile, tm.Members[0], "issue-96", probe)
	if rec.Status != statusStateLive {
		t.Fatalf("status = %q, want live (a notable Observation corroborates/explains, never overrides per-seat evidence)", rec.Status)
	}
	if !strings.Contains(rec.Detail, "launchapi observes") || !strings.Contains(rec.Detail, "runnable=false") || !strings.Contains(rec.Detail, "caller_context_stale") {
		t.Fatalf("detail does not surface the notable observation: %q", rec.Detail)
	}
	if rec.LaunchapiObservation == nil {
		t.Fatal("LaunchapiObservation is nil, want the matched observation")
	}
	if rec.LaunchapiObservation.Handle != "cto" || rec.LaunchapiObservation.Runnable || rec.LaunchapiObservation.Execution != "blocked" || rec.LaunchapiObservation.ReasonCode != "caller_context_stale" {
		t.Fatalf("LaunchapiObservation = %+v, want the matched fields", rec.LaunchapiObservation)
	}

	encoded, err := json.Marshal(rec)
	if err != nil {
		t.Fatalf("marshal statusRecord: %v", err)
	}
	var decoded struct {
		LaunchapiObservation *struct {
			Handle      string `json:"handle"`
			Runnable    bool   `json:"runnable"`
			Execution   string `json:"execution"`
			ReasonCode  string `json:"reason_code"`
			Disposition string `json:"disposition"`
		} `json:"launchapi_observation"`
	}
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("unmarshal statusRecord JSON: %v", err)
	}
	if decoded.LaunchapiObservation == nil {
		t.Fatalf("JSON output has no launchapi_observation field: %s", encoded)
	}
	if decoded.LaunchapiObservation.Handle != "cto" || decoded.LaunchapiObservation.Runnable || decoded.LaunchapiObservation.ReasonCode != "caller_context_stale" {
		t.Fatalf("launchapi_observation JSON shape = %+v, want the matched fields", decoded.LaunchapiObservation)
	}
}

// TestStatusOmitsUnremarkableLaunchapiObservation proves the corroborate-or-
// silence rule: a Runnable=true observation with no reason code matches the
// ordinary live case and must not add Detail noise, even though
// LaunchapiObservation itself is still populated for --json consumers.
func TestStatusOmitsUnremarkableLaunchapiObservation(t *testing.T) {
	base := setupFakeAMQSessionRoots(t)
	dir := seedTeam(t, team.Team{
		Workstream: "issue-96",
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-96"},
		},
	})
	writeMemberLaunchRecord(t, base, "issue-96", "cto", launch.Record{
		CWD: dir, Binary: "codex", Role: "cto", AgentPID: 5555, StartedAt: time.Now(),
	})
	withStubPaneLister(t, nil, nil)
	stubLaunchapiInspect(t, func(context.Context, launchapi.InspectRequestV1) (launchapi.InspectResultV1, error) {
		return launchapi.InspectResultV1{
			State: "present",
			Observations: []launchapi.ParticipantObservationV1{
				{Handle: "cto", Runnable: true, Execution: "running"},
			},
		}, nil
	})

	probe := livenessProbe(map[int]bool{5555: true}, map[int]bool{5555: true}, time.Now())
	tm, err := team.ReadProfile(dir, team.DefaultProfile)
	if err != nil {
		t.Fatal(err)
	}
	rec := classifyMemberStatus(tm, team.DefaultProfile, tm.Members[0], "issue-96", probe)
	if strings.Contains(rec.Detail, "launchapi observes") {
		t.Fatalf("detail should stay silent for an unremarkable observation: %q", rec.Detail)
	}
	if rec.LaunchapiObservation == nil || !rec.LaunchapiObservation.Runnable {
		t.Fatalf("LaunchapiObservation = %+v, want it still populated (runnable=true) for --json even though Detail stayed silent", rec.LaunchapiObservation)
	}
}
