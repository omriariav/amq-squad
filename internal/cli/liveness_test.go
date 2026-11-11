package cli

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/launch"
	squadnamespace "github.com/omriariav/amq-squad/v3/internal/namespace"
	"github.com/omriariav/amq-squad/v3/internal/team"
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

// TestClassifierPresenceLiveWhenWriterUnknown pins the conservative half of the
// zombie guard: a fresh active presence with NO writer records (or a missing
// one) still counts as presence-live, exactly as before. Only a both-present,
// both-dead case demotes it. This guards against the guard over-reaching.
func TestClassifierPresenceLiveWhenWriterUnknown(t *testing.T) {
	base := setupFakeAMQSessionRoots(t)
	_ = seedTeam(t, team.Team{
		Session: "issue-96",
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
	probe := livenessProbe(map[int]bool{}, map[int]bool{}, now)

	live := classifyAgentLivenessFromDisk(agentDir, root, "default", "cto", "codex", probe)
	if live.Verdict != livenessPresenceLive {
		t.Fatalf("presence with unknown writers verdict = %q, want presence-live", live.Verdict)
	}
	if live.Status != statusStateLive {
		t.Fatalf("presence with unknown writers status = %q, want live", live.Status)
	}
}

func TestStoppedLaunchRecordOverridesReusedLivePID(t *testing.T) {
	base := setupFakeAMQSessionRoots(t)
	dir := seedTeam(t, team.Team{
		Session: "issue-96",
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

	live := classifyAgentLivenessFromDisk(agentDir, filepath.Join(base, "issue-96"), "default", "cto", "codex", probe)
	if live.Verdict != livenessStale || live.Status != statusStateStale || live.Live() {
		t.Fatalf("explicitly stopped record classified live after PID reuse: %+v", live)
	}
	if !strings.Contains(live.Detail, "explicitly stopped") {
		t.Fatalf("stopped classification missing lifecycle detail: %+v", live)
	}
}

// TestStatusRollupLiveSeatFromDisk keeps the status rollup's disk-classifier
// row: a launch record whose process identity matches is live.
func TestStatusRollupLiveSeatFromDisk(t *testing.T) {
	base := setupFakeAMQSessionRoots(t)
	dir := seedTeam(t, team.Team{
		Session: "issue-96",
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-96"},
		},
	})
	writeMemberLaunchRecord(t, base, "issue-96", "cto", launch.Record{
		CWD: dir, Binary: "codex", Role: "cto", AgentPID: 5555, StartedAt: time.Now(),
	})

	probe := livenessProbe(map[int]bool{5555: true}, map[int]bool{5555: true}, time.Now())
	tm, err := team.Read(dir)
	if err != nil {
		t.Fatal(err)
	}
	rec := classifyMemberStatusForTest(tm, squadnamespace.DefaultProfile, tm.Members[0], "issue-96", probe)
	if rec.Status != statusStateLive {
		t.Fatalf("status = %q, want live", rec.Status)
	}
}

// TestStatusRollupUnchangedForLegacyLaunches keeps the legacy launch record's
// exact status and detail under the disk classifier.
func TestStatusRollupUnchangedForLegacyLaunches(t *testing.T) {
	base := setupFakeAMQSessionRoots(t)
	dir := seedTeam(t, team.Team{
		Session: "issue-96",
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-96"},
		},
	})
	writeMemberLaunchRecord(t, base, "issue-96", "cto", launch.Record{
		CWD: dir, Binary: "codex", Role: "cto", AgentPID: 5555, StartedAt: time.Now(),
	})

	probe := livenessProbe(map[int]bool{5555: true}, map[int]bool{5555: true}, time.Now())
	tm, err := team.Read(dir)
	if err != nil {
		t.Fatal(err)
	}
	rec := classifyMemberStatusForTest(tm, squadnamespace.DefaultProfile, tm.Members[0], "issue-96", probe)
	if rec.Status != statusStateLive || rec.Detail != "agent pid 5555 alive (codex)" {
		t.Fatalf("legacy-session status/detail changed: status=%q detail=%q", rec.Status, rec.Detail)
	}
}

// classifyMemberStatusForTest classifies one roster member the way status does
// (buildStatusRows), without the activity and worktree attachments.
func classifyMemberStatusForTest(t team.Team, profile string, m team.Member, workstream string, probe duplicateLaunchProbe) statusRecord {
	entries, scanErr := launch.ScanEntries(t.Project)
	return classifyMemberStatusFromEntries(t, profile, m, workstream, probe, entries, scanErr)
}
