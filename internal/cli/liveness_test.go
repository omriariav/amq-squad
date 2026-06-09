package cli

import (
	"bytes"
	"encoding/json"
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

	// 4) Agreement: status stale <-> resume not-live, AND the resume plan carries
	// the shared liveness verdict matching status (what resume --json exposes).
	if rec.Status == statusStateStale && plan.Action == resumeLive {
		t.Fatalf("status and resume disagree: status=stale but resume=live")
	}
	if plan.Liveness == nil {
		t.Fatal("resume plan must carry a liveness verdict")
	}
	if plan.Liveness.Status != rec.Status {
		t.Errorf("resume liveness.status %q != status %q (must agree)", plan.Liveness.Status, rec.Status)
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

// TestStatusAndResumeAgreeOnZombiePresence is the #44 zombie-heartbeat
// regression carried into the shared classifier: a FRESH active same-handle
// presence.json whose launch+wake writer PIDs are BOTH confirmed dead is a
// leftover heartbeat, not a live agent. The classifier must apply the same
// zombie guard the launch preflight does, so the verdict is stale (not
// presence-live). Status then correctly reports stale (previously it wrongly
// said live -- the latent #44 bug), and resume offers restore with a command
// (previously it would have reported action=live with no command). Both
// surfaces must agree on stale.
func TestStatusAndResumeAgreeOnZombiePresence(t *testing.T) {
	base := setupFakeAMQSessionRoots(t)
	dir := seedTeam(t, team.Team{
		Workstream: "issue-96",
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-96"},
		},
	})
	root := filepath.Join(base, "issue-96")
	agentDir := filepath.Join(root, "agents", "cto")

	// Both writer records present with dead PIDs.
	writeMemberLaunchRecord(t, base, "issue-96", "cto", launch.Record{
		CWD: dir, Binary: "codex", Role: "cto", AgentPID: 7777, StartedAt: time.Now(),
	})
	writeWakeLock(t, agentDir, wakeLockFile{PID: 8888, Root: root, Started: time.Now()})

	// A fresh, active, same-handle presence -- the zombie heartbeat.
	now := time.Now()
	writeStatusPresence(t, base, "issue-96", "cto", presenceFile{
		Handle:   "cto",
		Status:   "active",
		LastSeen: now.Add(-10 * time.Second),
	})

	// No live tmux pane, so the replacement-live fallback never fires.
	withStubPaneLister(t, nil, nil)

	// Both writer PIDs dead; neither matches.
	probe := livenessProbe(map[int]bool{}, map[int]bool{}, now)

	// 1) Classifier: zombie presence demotes to stale (NOT presence-live).
	live := classifyAgentLiveness(agentDir, root, "cto", "cto", "codex", "issue-96", dir, probe)
	if live.Verdict != livenessStale {
		t.Fatalf("zombie presence verdict = %q, want stale", live.Verdict)
	}
	if live.Status != statusStateStale {
		t.Fatalf("zombie presence status = %q, want stale (detail %q)", live.Status, live.Detail)
	}
	if live.Live() {
		t.Fatalf("zombie presence must not report Live()")
	}

	// 2) classifyMemberStatus reports stale (the #44 fix at the status layer).
	tm, err := team.ReadProfile(dir, team.DefaultProfile)
	if err != nil {
		t.Fatalf("read team: %v", err)
	}
	rec := classifyMemberStatus(tm, tm.Members[0], "issue-96", probe)
	if rec.Status != statusStateStale {
		t.Fatalf("status = %q, want stale (detail %q)", rec.Status, rec.Detail)
	}

	// 3) resume offers restore with a command -- NOT live-with-no-command.
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
		t.Fatalf("zombie-presence resume action = live; want restore. note=%q", plan.Note)
	}
	if plan.Action != resumeRestore {
		t.Fatalf("zombie-presence resume action = %q, want restore", plan.Action)
	}
	if strings.TrimSpace(plan.Command) == "" {
		t.Fatalf("zombie-presence restore must emit a non-empty command, got empty")
	}

	// 4) Agreement: status stale <-> resume not-live.
	if rec.Status == statusStateStale && plan.Action == resumeLive {
		t.Fatalf("status and resume disagree on zombie presence: status=stale but resume=live")
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

	live := classifyAgentLiveness(agentDir, root, "cto", "cto", "codex", "issue-96", dir, probe)
	if live.Verdict != livenessPresenceLive {
		t.Fatalf("presence with unknown writers verdict = %q, want presence-live", live.Verdict)
	}
	if live.Status != statusStateLive {
		t.Fatalf("presence with unknown writers status = %q, want live", live.Status)
	}
}

// resumeLiveNote must list EVERY live source (not just the highest-precedence
// verdict) in preflight blocker order wake+launch+presence, matching the
// pre-unification summarizeBlocker output the resume contract promised.
func TestResumeLiveNoteListsAllLiveSources(t *testing.T) {
	all := agentLiveness{
		Verdict:      livenessAgentLive, // highest-precedence verdict...
		Signals:      statusSignals{AgentAlive: true, BinaryMatch: true, WakeAlive: true},
		PresenceLive: true, // ...but every source is live
	}
	if got := resumeLiveNote(all, "codex"); got != "wake+launch+presence" {
		t.Errorf("multi-signal note = %q, want %q", got, "wake+launch+presence")
	}
	// A subset (wake + presence, no live agent pid).
	sub := agentLiveness{Verdict: livenessPresenceLive, Signals: statusSignals{WakeAlive: true}, PresenceLive: true}
	if got := resumeLiveNote(sub, "codex"); got != "wake+presence" {
		t.Errorf("subset note = %q, want %q", got, "wake+presence")
	}
	// Single source.
	if got := resumeLiveNote(agentLiveness{Verdict: livenessWakeLive, Signals: statusSignals{WakeAlive: true}}, "codex"); got != "wake" {
		t.Errorf("wake-only note = %q, want wake", got)
	}
	// Replacement keeps its distinct phrasing + target.
	repl := agentLiveness{Verdict: livenessReplacementLive, ReplacementTarget: "%5"}
	if got := resumeLiveNote(repl, "codex"); !strings.Contains(got, "%5") || !strings.Contains(got, "recorded pid dead") {
		t.Errorf("replacement note = %q, want it to mention the dead pid + target", got)
	}
}

// resume --json must expose a `liveness` block carrying the SAME verdict status
// status reports, so a client compares liveness.status to status's status
// instead of inferring liveness from the planning `action` (#79 PR B).
func TestResumePlanJSONCarriesLiveness(t *testing.T) {
	var buf bytes.Buffer
	plans := []resumePlan{{
		Role: "cto", Handle: "cto", Action: resumeRestore,
		Command:  "amq-squad agent up codex --role cto",
		Liveness: &agentLiveness{Status: statusStateStale, Detail: "agent pid dead", Signals: statusSignals{AgentPID: 7777}},
	}}
	if err := writeResumeJSON(&buf, team.Team{Project: "/p"}, "issue-96", resumeModeDefault, "", plans); err != nil {
		t.Fatal(err)
	}
	var env struct {
		Data struct {
			Plan []struct {
				Action   string `json:"action"`
				Liveness *struct {
					Status string `json:"status"`
					Detail string `json:"detail"`
				} `json:"liveness"`
			} `json:"plan"`
		} `json:"data"`
	}
	if err := json.Unmarshal(buf.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	p := env.Data.Plan[0]
	if p.Liveness == nil {
		t.Fatal("resume_plan member must carry a liveness block")
	}
	if p.Liveness.Status != "stale" {
		t.Errorf("liveness.status = %q, want %q (the shared verdict status)", p.Liveness.Status, "stale")
	}
	if p.Action != "restore" {
		t.Errorf("a stale verdict should restore, got action %q", p.Action)
	}
}

// #87: plain `resume` and `resume --json` MUST render the same action. They
// both branch from the SAME []resumePlan in executeResume, so they cannot
// diverge by code (the report was a cross-invocation race). This pins it: the
// fixture sets Action DELIBERATELY mismatched against Liveness.Status, so a
// renderer that (wrongly) re-derived the action from liveness would emit a
// different string and fail. Both renderers must echo resumePlan.Action.
func TestPlainAndJSONResumeRenderSameAction(t *testing.T) {
	plans := []resumePlan{
		// Action=restore but liveness says live: output must show "restore".
		{Role: "cto", Handle: "cto", Action: resumeRestore, Command: "amq-squad agent up codex --role cto",
			Liveness: &agentLiveness{Status: statusStateLive, Detail: "live"}},
		// Action=live but liveness says stale: output must show "live".
		{Role: "qa", Handle: "qa", Action: resumeLive, Note: "wake+launch",
			Liveness: &agentLiveness{Status: statusStateStale}},
	}
	tm := team.Team{Project: "/p"}
	var plain, jsonBuf bytes.Buffer
	writeResumePlan(&plain, tm, "issue-96", resumeModeDefault, plans, false, false, resumePrinterStyle{Label: "resume", FooterVerb: "up"})
	if err := writeResumeJSON(&jsonBuf, tm, "issue-96", resumeModeDefault, "", plans); err != nil {
		t.Fatal(err)
	}

	// Plain: the row for each role shows that member's action.
	plainRow := map[string]string{}
	for _, line := range strings.Split(plain.String(), "\n") {
		if f := strings.Fields(line); len(f) >= 2 {
			plainRow[f[0]] = line
		}
	}
	// JSON: action per role.
	var env struct {
		Data struct {
			Plan []struct{ Role, Action string } `json:"plan"`
		} `json:"data"`
	}
	if err := json.Unmarshal(jsonBuf.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	jsonAction := map[string]string{}
	for _, p := range env.Data.Plan {
		jsonAction[p.Role] = p.Action
	}

	for _, p := range plans {
		if row, ok := plainRow[p.Role]; !ok || !strings.Contains(row, string(p.Action)) {
			t.Errorf("plain resume row for %s = %q, must show action %q", p.Role, row, p.Action)
		}
		if jsonAction[p.Role] != string(p.Action) {
			t.Errorf("json action for %s = %q, want %q", p.Role, jsonAction[p.Role], p.Action)
		}
		// And the two renderers must agree.
		if got := jsonAction[p.Role]; !strings.Contains(plainRow[p.Role], got) {
			t.Errorf("plain (%q) and json (%q) disagree on %s's action", plainRow[p.Role], got, p.Role)
		}
	}
}

// #87 (reopened): when BOTH the agent PID and the wake PID are live, status,
// doctor, plain resume, and resume --json must ALL classify the member live.
// The reopened repro showed status/doctor saying stale while resume said live
// for the exact same live records; they share classifyAgentLiveness, so this
// pins cross-surface agreement on the full agent+wake-live evidence.
func TestStatusDoctorResumeAgreeWhenAgentAndWakeLive(t *testing.T) {
	base := setupFakeAMQSessionRoots(t)
	dir := seedTeam(t, team.Team{
		Workstream: "issue-96",
		Members:    []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-96"}},
	})
	writeMemberLaunchRecord(t, base, "issue-96", "cto", launch.Record{
		CWD: dir, Binary: "codex", Role: "cto", AgentPID: 5555, StartedAt: time.Now(),
	})
	root := filepath.Join(base, "issue-96")
	agentDir := filepath.Join(root, "agents", "cto")
	writeWakeLock(t, agentDir, wakeLockFile{PID: 6666, Root: root, Started: time.Now()})
	withStubPaneLister(t, nil, nil)

	now := time.Now()
	// Both PIDs alive AND matching — the reopened repro's "ps shows both alive".
	probe := livenessProbe(map[int]bool{5555: true, 6666: true}, map[int]bool{5555: true, 6666: true}, now)

	tm, err := team.ReadProfile(dir, team.DefaultProfile)
	if err != nil {
		t.Fatalf("read team: %v", err)
	}
	m := tm.Members[0]

	// 1) status
	rec := classifyMemberStatus(tm, m, "issue-96", probe)
	if rec.Status != statusStateLive {
		t.Fatalf("status = %q, want live (agent+wake both alive); detail=%q", rec.Status, rec.Detail)
	}
	// 2) doctor maps the SAME statusRecord
	if dc := doctorCheckFromStatus(rec); dc.Status != doctorOK {
		t.Fatalf("doctor = %q, want ok; detail=%q", dc.Status, dc.Detail)
	}
	// 3) plain resume
	plan, err := planMemberResume(memberPlanInput{
		Member: m, Team: tm, Workstream: "issue-96", Mode: resumeModeDefault,
		SquadBin: teamSquadBin(), Probe: probe,
	})
	if err != nil {
		t.Fatalf("planMemberResume: %v", err)
	}
	if plan.Action != resumeLive {
		t.Fatalf("plain resume action = %q, want live", plan.Action)
	}
	// 4) resume --json liveness
	var buf bytes.Buffer
	if err := writeResumeJSON(&buf, tm, "issue-96", resumeModeDefault, "", []resumePlan{plan}); err != nil {
		t.Fatalf("writeResumeJSON: %v", err)
	}
	var env struct {
		Data struct {
			Plan []struct {
				Liveness *struct {
					Status string `json:"status"`
				} `json:"liveness"`
			} `json:"plan"`
		} `json:"data"`
	}
	if err := json.Unmarshal(buf.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	if env.Data.Plan[0].Liveness == nil || env.Data.Plan[0].Liveness.Status != string(statusStateLive) {
		t.Fatalf("resume --json liveness = %+v, want live", env.Data.Plan[0].Liveness)
	}
}
