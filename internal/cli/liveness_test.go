package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/internal/launch"
	"github.com/omriariav/amq-squad/internal/team"
	"github.com/omriariav/amq-squad/internal/tmuxpane"
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

// TestStatusAndResumeAgreeOnStaleAgent is the core #79 regression: a genuinely
// stale agent on disk (dead launch AgentPID + dead/unrelated wake PID, with no
// fresh active presence and no live replacement pane) must be deemed stale by
// the single shared classifier, render as `stale` in status, and resume to a
// RESTORE action (with a real command) -- never `live`. Before unification,
// status and resume classified liveness independently and could disagree
// (status stale, resume live). Now they consume one verdict and must agree.
func TestStatusAndResumeAgreeOnStaleAgent(t *testing.T) {
	base := setupFakeAMQSessionRoots(t)
	dir := seedTeam(t, team.Team{
		Workstream: "issue-96",
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-96"},
		},
	})
	// Stale launch record: a captured AgentPID that is no longer alive.
	writeMemberLaunchRecord(t, base, "issue-96", "cto", launch.Record{
		CWD: dir, Binary: "codex", Role: "cto", AgentPID: 7777, StartedAt: time.Now(),
	})
	// Stale wake lock: a wake PID that is dead/unrelated.
	agentDir := filepath.Join(base, "issue-96", "agents", "cto")
	writeWakeLock(t, agentDir, wakeLockFile{PID: 8888, Root: filepath.Join(base, "issue-96")})

	// No tmux panes, so the replacement-live fallback never fires (and the test
	// never shells real tmux).
	withStubPaneLister(t, nil, nil)

	now := time.Now()
	// Both pids dead, neither matches.
	probe := livenessProbe(map[int]bool{}, map[int]bool{}, now)

	// 1) The shared classifier verdict is stale.
	live := classifyAgentLiveness(agentDir, filepath.Join(base, "issue-96"), "cto", "cto", "codex", "issue-96", dir, probe)
	if live.Verdict != livenessStale {
		t.Fatalf("classifier verdict = %q, want %q", live.Verdict, livenessStale)
	}
	if live.Status != statusStateStale {
		t.Fatalf("classifier status = %q, want stale", live.Status)
	}
	if live.Live() {
		t.Fatalf("stale verdict must not report Live()")
	}

	// 2) classifyMemberStatus maps it to stale.
	tm, err := team.ReadProfile(dir, team.DefaultProfile)
	if err != nil {
		t.Fatalf("read team: %v", err)
	}
	rec := classifyMemberStatus(tm, tm.Members[0], "issue-96", probe)
	if rec.Status != statusStateStale {
		t.Fatalf("status = %q, want stale (detail %q)", rec.Status, rec.Detail)
	}

	// 3) planMemberResume must restore (record exists), NOT live, and emit a
	//    non-empty command. This is the fix: the stale agent resumes.
	plan, err := planMemberResume(memberPlanInput{
		Member:     tm.Members[0],
		Team:       tm,
		Workstream: "issue-96",
		Mode:       resumeModeDefault,
		SquadBin:   teamSquadBin(),
		Probe:      probe,
	})
	if err != nil {
		t.Fatalf("planMemberResume: %v", err)
	}
	if plan.Action == resumeLive {
		t.Fatalf("resume action = live for a stale agent; want restore. note=%q", plan.Note)
	}
	if plan.Action != resumeRestore {
		t.Fatalf("resume action = %q, want restore", plan.Action)
	}
	if strings.TrimSpace(plan.Command) == "" {
		t.Fatalf("restore action must emit a non-empty command, got empty")
	}

	// 4) Agreement: status stale <-> resume not-live.
	if rec.Status == statusStateStale && plan.Action == resumeLive {
		t.Fatalf("status and resume disagree: status=stale but resume=live")
	}
}

// TestStatusAndResumeAgreeOnMissingAgent: with no records at all, the verdict
// is missing, status renders missing, and resume launches fresh (no record to
// restore) -- never live.
func TestStatusAndResumeAgreeOnMissingAgent(t *testing.T) {
	base := setupFakeAMQSessionRoots(t)
	dir := seedTeam(t, team.Team{
		Workstream: "issue-96",
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-96"},
		},
	})
	agentDir := filepath.Join(base, "issue-96", "agents", "cto")
	withStubPaneLister(t, nil, nil)

	now := time.Now()
	probe := livenessProbe(map[int]bool{}, map[int]bool{}, now)

	live := classifyAgentLiveness(agentDir, filepath.Join(base, "issue-96"), "cto", "cto", "codex", "issue-96", dir, probe)
	if live.Verdict != livenessMissing {
		t.Fatalf("classifier verdict = %q, want missing", live.Verdict)
	}

	tm, err := team.ReadProfile(dir, team.DefaultProfile)
	if err != nil {
		t.Fatalf("read team: %v", err)
	}
	rec := classifyMemberStatus(tm, tm.Members[0], "issue-96", probe)
	if rec.Status != statusStateMissing {
		t.Fatalf("status = %q, want missing", rec.Status)
	}

	plan, err := planMemberResume(memberPlanInput{
		Member:     tm.Members[0],
		Team:       tm,
		Workstream: "issue-96",
		Mode:       resumeModeDefault,
		SquadBin:   teamSquadBin(),
		Probe:      probe,
	})
	if err != nil {
		t.Fatalf("planMemberResume: %v", err)
	}
	if plan.Action == resumeLive {
		t.Fatalf("resume action = live for a missing agent; want launch fresh")
	}
	if plan.Action != resumeFresh {
		t.Fatalf("resume action = %q, want launch fresh", plan.Action)
	}
	if strings.TrimSpace(plan.Command) == "" {
		t.Fatalf("fresh action must emit a non-empty command, got empty")
	}
}

// TestStatusAndResumeAgreeOnLiveAgentPID: a live agent PID (alive + binary
// match) must be agent-live in the classifier, `live` in status, and `live`
// (command suppressed) in resume.
func TestStatusAndResumeAgreeOnLiveAgentPID(t *testing.T) {
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
	agentDir := filepath.Join(base, "issue-96", "agents", "cto")
	withStubPaneLister(t, nil, nil)

	now := time.Now()
	probe := livenessProbe(map[int]bool{5555: true}, map[int]bool{5555: true}, now)

	live := classifyAgentLiveness(agentDir, filepath.Join(base, "issue-96"), "cto", "cto", "codex", "issue-96", dir, probe)
	if live.Verdict != livenessAgentLive {
		t.Fatalf("classifier verdict = %q, want agent-live", live.Verdict)
	}
	if !live.Live() {
		t.Fatalf("agent-live verdict must report Live()")
	}

	tm, err := team.ReadProfile(dir, team.DefaultProfile)
	if err != nil {
		t.Fatalf("read team: %v", err)
	}
	rec := classifyMemberStatus(tm, tm.Members[0], "issue-96", probe)
	if rec.Status != statusStateLive {
		t.Fatalf("status = %q, want live", rec.Status)
	}

	plan, err := planMemberResume(memberPlanInput{
		Member:     tm.Members[0],
		Team:       tm,
		Workstream: "issue-96",
		Mode:       resumeModeDefault,
		SquadBin:   teamSquadBin(),
		Probe:      probe,
	})
	if err != nil {
		t.Fatalf("planMemberResume: %v", err)
	}
	if plan.Action != resumeLive {
		t.Fatalf("resume action = %q, want live", plan.Action)
	}
	if plan.Command != "" {
		t.Fatalf("live member must suppress its command by default, got %q", plan.Command)
	}
}

// TestStatusAndResumeAgreeOnLiveWakeOnly: a live wake lock with no launch
// record is wake-live in the classifier, `wake-live` in status, and `live`
// (command suppressed) in resume. The wake matcher must verify the live PID is
// an amq wake for this handle/root, so this probe matches on a real-looking arg
// string.
func TestStatusAndResumeAgreeOnLiveWakeOnly(t *testing.T) {
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

	now := time.Now()
	probe := duplicateLaunchProbe{
		PIDAlive: func(pid int) bool { return pid == 4321 },
		ProcessMatch: func(pid int, predicate func(args string) bool) bool {
			return pid == 4321 && predicate("amq wake --me cto --root "+root)
		},
		Now: func() time.Time { return now },
	}

	live := classifyAgentLiveness(agentDir, root, "cto", "cto", "codex", "issue-96", dir, probe)
	if live.Verdict != livenessWakeLive {
		t.Fatalf("classifier verdict = %q, want wake-live", live.Verdict)
	}
	if live.Status != statusStateWakeLive {
		t.Fatalf("classifier status = %q, want wake-live", live.Status)
	}

	tm, err := team.ReadProfile(dir, team.DefaultProfile)
	if err != nil {
		t.Fatalf("read team: %v", err)
	}
	rec := classifyMemberStatus(tm, tm.Members[0], "issue-96", probe)
	if rec.Status != statusStateWakeLive {
		t.Fatalf("status = %q, want wake-live", rec.Status)
	}

	plan, err := planMemberResume(memberPlanInput{
		Member:     tm.Members[0],
		Team:       tm,
		Workstream: "issue-96",
		Mode:       resumeModeDefault,
		SquadBin:   teamSquadBin(),
		Probe:      probe,
	})
	if err != nil {
		t.Fatalf("planMemberResume: %v", err)
	}
	if plan.Action != resumeLive {
		t.Fatalf("resume action = %q, want live (wake-live should suppress relaunch)", plan.Action)
	}
	if plan.Command != "" {
		t.Fatalf("wake-live member must suppress its command by default, got %q", plan.Command)
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

	live := classifyAgentLiveness(agentDir, filepath.Join(base, "issue-96"), "cto", "cto", "codex", "issue-96", dir, probe)
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
