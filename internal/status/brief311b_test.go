package status

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/launch"
	"github.com/omriariav/amq-squad/v3/internal/mergetrain"
	"github.com/omriariav/amq-squad/v3/internal/redact"
	"github.com/omriariav/amq-squad/v3/internal/schedproc"
	"github.com/omriariav/amq-squad/v3/internal/scheduler"
	"github.com/omriariav/amq-squad/v3/internal/seatlaunch"
	"github.com/omriariav/amq-squad/v3/internal/task"
)

// Brief 31-1's second PR, row 15 (status's half): the status additions are
// in the golden fixture, and a schema 1 reader of the fixture before them
// still reads it (the new fixture is additive against the old one).
func TestStatusAdditionsAreInTheGoldenAndSchema1ReadersReadIt(t *testing.T) {
	pre, err := os.ReadFile("testdata/status-v1.pre31.golden.json")
	if err != nil {
		t.Fatal(err)
	}
	golden := readGolden(t)
	if err := CheckAdditive(pre, golden); err != nil {
		t.Fatalf("a schema 1 reader of the fixture before brief 31-1 cannot read it: %v", err)
	}
	var old, now map[string]any
	_ = json.Unmarshal(pre, &old)
	_ = json.Unmarshal(golden, &now)
	paths := map[string]func(m map[string]any) any{
		"lead":                 func(m map[string]any) any { return m["lead"] },
		"decisions":            func(m map[string]any) any { return m["decisions"] },
		"train":                func(m map[string]any) any { return m["train"] },
		"scheduler.pid":        func(m map[string]any) any { return m["scheduler"].(map[string]any)["pid"] },
		"scheduler.start":      func(m map[string]any) any { return m["scheduler"].(map[string]any)["launch_start"] },
		"seat.launch":          func(m map[string]any) any { return m["seats"].([]any)[0].(map[string]any)["launch"] },
		"seat.worktree":        func(m map[string]any) any { return m["seats"].([]any)[0].(map[string]any)["worktree"] },
		"task.pr_number":       func(m map[string]any) any { return m["tasks"].([]any)[1].(map[string]any)["pr_number"] },
		"task.evidence":        func(m map[string]any) any { return m["tasks"].([]any)[1].(map[string]any)["evidence"] },
		"seat.timing.segments": func(m map[string]any) any { return seatTiming(m)["segments"] },
	}
	for name, get := range paths {
		if v := safeGet(get, old); v != nil {
			t.Errorf("%s is already in the fixture before brief 31-1: %v", name, v)
		}
		if v := safeGet(get, now); v == nil {
			t.Errorf("%s is missing from the golden fixture", name)
		}
	}
}

func seatTiming(m map[string]any) map[string]any {
	return m["seats"].([]any)[0].(map[string]any)["launch"].(map[string]any)["timing"].(map[string]any)
}

func safeGet(get func(map[string]any) any, m map[string]any) (v any) {
	defer func() {
		if recover() != nil {
			v = nil
		}
	}()
	return get(m)
}

// budgetSeat writes one seat's staged record (compile's started_at, the
// proof ticket's issued_at, AMQ's apply), its accepted proof and its
// admitted wake, as a launch leaves them.
func budgetSeat(t *testing.T, root, handle, runtime, model string, start time.Time, prepare, mint, apply, seatExec time.Duration, wake *seatlaunch.SeatWakeFacts) {
	t.Helper()
	for _, d := range []string{filepath.Join(root, "agents", handle), launch.StagedDir(root, handle)} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	compiled := start.Add(prepare)
	issued := compiled.Add(mint)
	applied := issued.Add(apply)
	live := applied.Add(seatExec)
	rec := launch.Record{Handle: handle, Root: root, Session: "s", Runtime: runtime, Model: model, LaunchID: "launch-1",
		Worktree: "/wt/" + handle, Branch: "seat/" + handle, StartedAt: compiled,
		Proof: &launch.ProofTicket{IssuedAt: issued, Token: launch.TokenConsumed},
		Apply: &launch.Apply{Outcome: "applied", At: applied}}
	if err := launch.WriteFile(filepath.Join(launch.StagedDir(root, handle), launch.FileName), rec); err != nil {
		t.Fatal(err)
	}
	if err := launch.WriteProof(root, launch.ProofEntry{Handle: handle, LaunchID: "launch-1", PID: 4242, Start: "s", At: live}); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(seatlaunch.SeatWakeRecord{LaunchID: "launch-1", Wake: wake})
	if err := os.WriteFile(filepath.Join(launch.StagedDir(root, handle), seatlaunch.WakeFileName), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	// the seat's own launch start, as the launch writes it
	marks, _ := json.Marshal(seatlaunch.SeatMarks{LaunchID: "launch-1", Start: &start})
	if err := os.WriteFile(filepath.Join(launch.StagedDir(root, handle), seatlaunch.MarksFileName), marks, 0o600); err != nil {
		t.Fatal(err)
	}
}

func seatByHandle(t *testing.T, st Status, h string) Seat {
	t.Helper()
	for _, s := range st.Seats {
		if s.Handle == h {
			return s
		}
	}
	t.Fatalf("no seat %s in %+v", h, st.Seats)
	return Seat{}
}

func hasWarning(s Seat, code string) string {
	for _, w := range s.Warnings {
		if strings.HasPrefix(w, code+": ") {
			return w
		}
	}
	return ""
}

// Row 14: the launch budget from fixture records. Per seat: the start
// (launch.json's start, AMQ_SQUAD_START) to seat live (the accepted
// proof's stamp) and to the first AMQ message (the scheduler's
// seat_first_message), with the segments up to seat live from the records.
// A seat over the 10 s ceiling to seat live carries the warning naming its
// slowest segment; the first message has no ceiling.
func TestLaunchBudgetFromFixtureRecords(t *testing.T) {
	base, _ := filepath.EvalSymlinks(t.TempDir())
	root := filepath.Join(base, "root")
	start := time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)
	if err := os.MkdirAll(schedproc.Dir(root), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := schedproc.WriteLaunchRecord(root, schedproc.LaunchRecord{Lead: "cto", Root: root, PID: 77, StartedAt: start.Add(time.Second), Start: &start}); err != nil {
		t.Fatal(err)
	}
	ms := time.Millisecond
	// the baseline's dev seat: 4.485 s to seat live, no first message yet
	budgetSeat(t, root, "dev", "codex", "gpt-5.6-terra", start, 3201*ms, 602*ms, 538*ms, 144*ms, nil)
	// the reviewer: 4.419 s to seat live, its first message 57.449 s after the start (no ceiling)
	budgetSeat(t, root, "reviewer", "codex", "gpt-5.6-sol", start, 3201*ms, 602*ms, 538*ms, 78*ms, nil)
	// a slow seat: 12.4 s to seat live, the seat-exec segment the slowest
	budgetSeat(t, root, "slow", "claude", "sonnet", start, 3201*ms, 602*ms, 538*ms, 8059*ms, nil)
	// the reviewer's first message, recorded in its marks by the scheduler
	// (the decisions log carries no seat_first_message: it scrolled out)
	if err := seatlaunch.RecordFirstMessage(root, "reviewer", start.Add(57449*ms)); err != nil {
		t.Fatal(err)
	}
	st := Build(Collect(CollectOptions{Root: root, Now: start.Add(time.Minute), PIDAlive: func(int) bool { return false }}))
	want := map[string]struct {
		live, first int64
		over        bool
		slowest     string
	}{
		"dev":      {4485, -1, false, "prepare"},
		"reviewer": {4419, 57449, false, "prepare"},
		"slow":     {12400, -1, true, "seat_exec"},
	}
	for h, w := range want {
		s := seatByHandle(t, st, h)
		if s.Launch == nil || s.Launch.Outcome != "live" {
			t.Fatalf("%s launch %+v", h, s.Launch)
		}
		tm := s.Launch.Timing
		if tm.ToSeatLiveMS == nil || *tm.ToSeatLiveMS != w.live || tm.CeilingMS != 10000 || tm.OverCeiling != w.over || tm.SlowestSegment != w.slowest {
			t.Errorf("%s timing %+v (to seat live %v)", h, tm, deref64(tm.ToSeatLiveMS))
		}
		if got := deref64(tm.ToFirstMessageMS); got != w.first {
			t.Errorf("%s to first message %d, want %d", h, got, w.first)
		}
		if names := segNames(tm.Segments); names != "prepare,mint,apply,seat_exec" {
			t.Errorf("%s segments %s", h, names)
		}
		warning := hasWarning(s, WarnLaunchBudget)
		switch {
		case w.over && !strings.Contains(warning, "slowest segment seat_exec 8.059 s"):
			t.Errorf("%s over the ceiling carries %q", h, s.Warnings)
		case !w.over && warning != "":
			t.Errorf("%s within the ceiling (first message %d ms, which has no ceiling) carries %q", h, w.first, warning)
		}
		if s.Worktree != "/wt/"+h || s.Branch != "seat/"+h {
			t.Errorf("%s worktree %q branch %q", h, s.Worktree, s.Branch)
		}
	}
	if st.Scheduler.LaunchStart == nil || !st.Scheduler.LaunchStart.Equal(start) || st.Scheduler.PID != 77 {
		t.Fatalf("scheduler %+v", st.Scheduler)
	}
	// Without a start of its own launch (a scheduler started by hand) the
	// seat's marks are null and nothing is over the ceiling.
	noStart, _ := json.Marshal(seatlaunch.SeatMarks{LaunchID: "launch-1"})
	if err := os.WriteFile(filepath.Join(launch.StagedDir(root, "slow"), seatlaunch.MarksFileName), noStart, 0o600); err != nil {
		t.Fatal(err)
	}
	st = Build(Collect(CollectOptions{Root: root, Now: start.Add(time.Minute), PIDAlive: func(int) bool { return false }}))
	if s := seatByHandle(t, st, "slow"); s.Launch.Timing.ToSeatLiveMS != nil || s.Launch.Timing.OverCeiling || hasWarning(s, WarnLaunchBudget) != "" {
		t.Fatalf("no start, yet %+v %q", s.Launch.Timing, s.Warnings)
	}
}

func deref64(p *int64) int64 {
	if p == nil {
		return -1
	}
	return *p
}

func segNames(ss []LaunchSegment) string {
	var out []string
	for _, s := range ss {
		out = append(out, s.Name)
	}
	return strings.Join(out, ",")
}

// Brief 31-1's second PR, section 6: status carries the lead and the
// scheduler (pid, start stamp, pane), the decisions tail with exit_refused,
// the train's runs and alerts, each task's evidence and PR, and a seat's
// admitted wake (a wake-off seat names AMQ's reason as a warning). The read
// takes no lock.
func TestStatusCarriesTheScreensInputs(t *testing.T) {
	base, _ := filepath.EvalSymlinks(t.TempDir())
	root := filepath.Join(base, "root")
	now := time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)
	if err := os.MkdirAll(schedproc.Dir(root), 0o700); err != nil {
		t.Fatal(err)
	}
	lead, _ := json.Marshal(schedproc.LeadRecord{Schema: 1, PID: 4300, Start: "stamp-1", Pane: "%3", At: now.Add(-time.Minute)})
	if err := os.WriteFile(filepath.Join(schedproc.Dir(root), schedproc.LeadRecordName), lead, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := schedproc.WriteLaunchRecord(root, schedproc.LaunchRecord{Lead: "cto", Root: root, PID: 4321, StartedAt: now.Add(-50 * time.Second)}); err != nil {
		t.Fatal(err)
	}
	for i := range 40 {
		_ = schedproc.AppendDecision(root, now.Add(time.Duration(i)*time.Second), scheduler.Decision{Kind: "lease_renewed", Seat: "qa", TaskID: fmt.Sprintf("t%d", i)})
	}
	_ = schedproc.AppendDecision(root, now.Add(time.Minute), scheduler.Decision{Kind: "exit_refused", Seat: "qa", TaskID: "t9", Reason: "ledger_tampered at gone"})
	audit := mergetrain.AuditFor(root)
	for _, e := range []mergetrain.Entry{
		{Run: "r1", Kind: "batch", Repository: "o/app", Base: "main", Pairs: []mergetrain.Pair{{Number: 7, Head: "a"}}},
		{Run: "r1", Kind: "merge", PR: 7, Repository: "o/app", Base: "main"},
		{Run: "r2", Kind: "batch", Repository: "o/app", Base: "main", Pairs: []mergetrain.Pair{{Number: 8, Head: "b"}, {Number: 9, Head: "c"}}},
		{Run: "r2", Kind: "refusal", PR: 9, Rules: []string{"checks"}, Detail: "a required check failed", Repository: "o/app", Base: "main"},
	} {
		if err := audit.Append(e); err != nil {
			t.Fatal(err)
		}
	}
	budgetSeat(t, root, "qa", "codex", "gpt-5.6-sol", now.Add(-50*time.Second), time.Second, time.Second, time.Second, time.Second,
		&seatlaunch.SeatWakeFacts{Mode: "disabled", AuditReason: seatlaunch.CodexNoWake + "AMQ's Codex dialog hook is not on the host", Source: "amq_prepare_preview"})
	store, err := task.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Add(task.AddInput{Title: "fix", Class: "implement"}, now); err != nil {
		t.Fatal(err)
	}
	if _, err := store.BindPR("t1", "o/app", 9, "4f1c2a9e0b5d6c7f8a9b0c1d2e3f4a5b6c7d8e9f", now); err != nil {
		t.Fatalf("bind: %v", err)
	}
	alive := func(pid int) bool { return pid == 4300 || pid == 4321 }
	st := Build(Collect(CollectOptions{Root: root, Now: now.Add(2 * time.Minute), PIDAlive: alive,
		StartToken: func(pid int) (string, bool) { return "stamp-1", pid == 4300 }}))
	if st.Lead == nil || st.Lead.PID != 4300 || st.Lead.Pane != "%3" || !st.Lead.Live || st.Lead.Handle != "cto" || st.Lead.Attach != "tmux attach -t %3" {
		t.Errorf("lead %+v", st.Lead)
	}
	if st.Scheduler.PID != 4321 || !st.Scheduler.Live || st.Scheduler.StartedAt == nil {
		t.Errorf("scheduler %+v", st.Scheduler)
	}
	if len(st.Decisions) != DecisionsTail || st.Decisions[len(st.Decisions)-1].Kind != "exit_refused" || st.Decisions[len(st.Decisions)-1].Reason != "ledger_tampered at gone" {
		t.Errorf("decisions tail (%d): %+v", len(st.Decisions), st.Decisions[len(st.Decisions)-1])
	}
	if len(st.Train.Runs) != 2 || st.Train.Runs[0].Run != "r2" || st.Train.Runs[0].Done || !strings.Contains(st.Train.Runs[0].Refusal, "a required check failed") ||
		st.Train.Runs[1].Run != "r1" || !st.Train.Runs[1].Done || len(st.Train.Runs[1].Merged) != 1 {
		t.Errorf("train runs %+v", st.Train.Runs)
	}
	if len(st.Tasks) != 1 || st.Tasks[0].PRNumber != 9 || st.Tasks[0].PRRepository != "o/app" || st.Tasks[0].PRHead != "4f1c2a9e0b5d6c7f8a9b0c1d2e3f4a5b6c7d8e9f" {
		t.Errorf("task %+v", st.Tasks)
	}
	qa := seatByHandle(t, st, "qa")
	if qa.Launch == nil || qa.Launch.Wake == nil || qa.Launch.Wake.Mode != "disabled" || !strings.Contains(hasWarning(qa, WarnWakeOff), "Codex dialog hook") {
		t.Errorf("qa wake %+v warnings %q", qa.Launch, qa.Warnings)
	}
	// Lock-free: no lock file was created by the read.
	for _, name := range []string{schedproc.LeadLockName, "run.lock"} {
		if _, err := os.Stat(filepath.Join(schedproc.Dir(root), name)); err == nil {
			t.Errorf("status created %s", name)
		}
	}
}

// Row 19 (status's half): a Claude seat stalled at a harness dialog (AMQ's
// .wake.dialog marker in its mailbox) and a pi seat whose PROVIDER/ID model
// has no key each carry a warning beside the seat's state; neither is a
// refusal (no needs_you state, no attention item).
func TestStalledSeatAndKeylessPiSeatAreWarningsNotRefusals(t *testing.T) {
	base, _ := filepath.EvalSymlinks(t.TempDir())
	root := filepath.Join(base, "root")
	now := time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)
	start := now.Add(-time.Minute)
	sec := time.Second
	budgetSeat(t, root, "scribe", "claude", "sonnet", start, sec, sec, sec, sec, nil)
	if err := os.WriteFile(filepath.Join(root, "agents", "scribe", ".wake.dialog"), []byte(`{"harness":"claude"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	pi := func(handle, model string, needs []string, auth string) {
		budgetSeat(t, root, handle, "pi", model, start, sec, sec, sec, sec, nil)
		agentDir := filepath.Join(base, "pi-agent-"+handle)
		_ = os.MkdirAll(agentDir, 0o700)
		if auth != "" {
			_ = os.WriteFile(filepath.Join(agentDir, "auth.json"), []byte(auth), 0o600)
		}
		_ = launch.UpdateStaged(root, handle, func(r *launch.Record) error {
			r.EnvNeeds, r.PiAgentDir = needs, agentDir
			return nil
		})
	}
	pi("pi-nokey", "anthropic/claude-sonnet-5", nil, "")
	pi("pi-env", "anthropic/claude-sonnet-5", []string{"ANTHROPIC_API_KEY"}, "")
	pi("pi-login", "openai/gpt-6", nil, `{"openai":{"type":"oauth"}}`)
	pi("pi-local", "ollama/qwen3.6:27b", nil, "")
	st := Build(Collect(CollectOptions{Root: root, Now: now, PIDAlive: func(int) bool { return true }}))
	scribe := seatByHandle(t, st, "scribe")
	if w := hasWarning(scribe, WarnStalledAtDialog); w == "" {
		t.Errorf("scribe at a dialog: warnings %q", scribe.Warnings)
	}
	if w := hasWarning(seatByHandle(t, st, "pi-nokey"), WarnPiProviderNoKey); !strings.Contains(w, "ANTHROPIC_API_KEY") {
		t.Errorf("keyless pi seat: %q", seatByHandle(t, st, "pi-nokey").Warnings)
	}
	if w := hasWarning(seatByHandle(t, st, "pi-local"), WarnWakeOff); !strings.Contains(w, "pi") {
		t.Errorf("a pi seat is wake-off with the pi reason: %q", seatByHandle(t, st, "pi-local").Warnings)
	}
	for _, h := range []string{"pi-env", "pi-login", "pi-local"} {
		if w := hasWarning(seatByHandle(t, st, h), WarnPiProviderNoKey); w != "" {
			t.Errorf("%s has a key or needs none, yet %q", h, w)
		}
	}
	for _, h := range []string{"scribe", "pi-nokey"} {
		s := seatByHandle(t, st, h)
		if s.State == StateNeedsYou || s.State == StateBlocked || s.Launch.Outcome != "live" {
			t.Errorf("%s is a refusal: state %s, launch %s", h, s.State, s.Launch.Outcome)
		}
	}
	for _, a := range st.Attention {
		if a.Seat == "scribe" || a.Seat == "pi-nokey" {
			t.Errorf("attention item %+v for a warning", a)
		}
	}
}

// The screen's fixture (pi/test/status-fixture.json, rows 16 and 19) is a
// status document of this build: it decodes strictly into Status and is
// additive against the golden fixture, so the screen is tested against
// the contract it reads.
func TestThePiScreensFixtureIsAStatusDocument(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "pi", "test", "status-fixture.json"))
	if err != nil {
		t.Fatal(err)
	}
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	var s Status
	if err := dec.Decode(&s); err != nil {
		t.Fatalf("the screen's fixture is not a status document: %v", err)
	}
	if err := CheckAdditive(readGolden(t), raw); err != nil {
		t.Fatalf("the screen's fixture is not additive against the golden fixture: %v", err)
	}
}

// Day 3: a pending task waiting on a file lock carries the tasks holding it
// (wait_holders), by the task store's lock rule: in progress or blocked,
// declared files overlapping. A cancelled task holds nothing; any other
// task row carries [].
func TestAFileLockWaitNamesItsHolders(t *testing.T) {
	now := time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)
	mk := func(id, state string, files ...string) task.Task {
		return task.Task{ID: id, State: state, DeclaredFiles: files, CreatedAt: now, UpdatedAt: now}
	}
	waiting := mk("t26", task.StatePending, "internal/cli/", "pi/")
	waiting.WaitReason = task.WaitFileLock
	in := Input{Now: now, Tasks: []task.Task{
		waiting,
		mk("t23", task.StateBlocked, "pi/extensions/"),
		mk("t20", task.StateInProgress, "internal/cli/up.go"),
		mk("t19", task.StateCancelled, "internal/cli/"),
		mk("t18", task.StateInProgress, "docs/"),
	}}
	st := Build(in)
	for _, r := range st.Tasks {
		want := "[]"
		if r.ID == "t26" {
			want = `["t20","t23"]`
		}
		if got, _ := json.Marshal(r.WaitHolders); string(got) != want {
			t.Errorf("%s wait_holders %s, want %s", r.ID, got, want)
		}
	}
}

// Codex P2 (6) and (7) on #829: each seat's marks are its own launch's.
// A seat adopted by a later scheduler incarnation keeps the start of the
// launch that started it (not the later scheduler's), a seat whose proof
// predates its recorded start, or that has no marks of its launch, has
// null timing and no ceiling check, and the first message comes from the
// seat's marks, so it survives the decisions log scrolling.
func TestEachSeatsMarksAreItsOwnLaunchs(t *testing.T) {
	base, _ := filepath.EvalSymlinks(t.TempDir())
	root := filepath.Join(base, "root")
	first := time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)
	later := first.Add(2 * time.Hour) // a restarted scheduler's start
	if err := os.MkdirAll(schedproc.Dir(root), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := schedproc.WriteLaunchRecord(root, schedproc.LaunchRecord{Lead: "cto", Root: root, PID: 77, StartedAt: later, Start: &later}); err != nil {
		t.Fatal(err)
	}
	s := time.Second
	budgetSeat(t, root, "adopted", "codex", "gpt", first, s, s, s, s, nil) // live at first+4s, adopted at the restart
	if err := seatlaunch.RecordFirstMessage(root, "adopted", first.Add(50*s)); err != nil {
		t.Fatal(err)
	}
	budgetSeat(t, root, "old", "codex", "gpt", first, s, s, s, s, nil)
	_ = os.Remove(filepath.Join(launch.StagedDir(root, "old"), seatlaunch.MarksFileName)) // launched before the marks
	budgetSeat(t, root, "skewed", "codex", "gpt", first, s, s, s, s, nil)
	skew, _ := json.Marshal(seatlaunch.SeatMarks{LaunchID: "launch-1", Start: &later}) // a start after its proof
	_ = os.WriteFile(filepath.Join(launch.StagedDir(root, "skewed"), seatlaunch.MarksFileName), skew, 0o600)
	for i := range 20000 { // the decisions log scrolls far past its tail read
		_ = schedproc.AppendDecision(root, later, scheduler.Decision{Kind: "lease_renewed", Seat: "adopted", TaskID: fmt.Sprintf("t%d-%s", i, strings.Repeat("x", 40))})
	}
	st := Build(Collect(CollectOptions{Root: root, Now: later.Add(time.Minute), PIDAlive: func(int) bool { return false }}))
	a := seatByHandle(t, st, "adopted").Launch.Timing
	if deref64(a.ToSeatLiveMS) != 4000 || deref64(a.ToFirstMessageMS) != 50000 || a.Start == nil || !a.Start.Equal(first) {
		t.Errorf("adopted seat timing %+v (live %d, first %d)", a, deref64(a.ToSeatLiveMS), deref64(a.ToFirstMessageMS))
	}
	for _, h := range []string{"old", "skewed"} {
		s := seatByHandle(t, st, h)
		if tm := s.Launch.Timing; tm.Start != nil || tm.ToSeatLiveMS != nil || tm.OverCeiling || hasWarning(s, WarnLaunchBudget) != "" {
			t.Errorf("%s: timing %+v warnings %q", h, tm, s.Warnings)
		}
	}
}

// Codex P2 (10) on #829: the document feeds the screen and the lead's
// model, so every free-text field is redacted before it is serialized. A
// run-time canary (a credential-named value, matching no real credential
// shape) in each field never survives into the JSON.
func TestStatusRedactsEveryFreeTextField(t *testing.T) {
	now := time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)
	var canaries []string
	canary := func(tag string) string {
		v := "t31c" + tag + "v" + "0042"
		text := "note api" + "_key=" + v + " end"
		for _, re := range redact.RealCredentialShapes {
			if re.MatchString(text) {
				t.Fatalf("the canary matches a real credential shape: %s", re)
			}
		}
		canaries = append(canaries, v)
		return text
	}
	in := Input{Now: now, Root: "/r",
		Seats: []SeatInput{{Handle: "qa", HasSignal: true, LastMessage: canary("msg"), Warnings: []string{canary("warn")},
			Launch: &SeatLaunch{Outcome: "live", Wake: &SeatWake{Mode: "disabled", AuditReason: canary("wake")}}}},
		Tasks:     []task.Task{{ID: "t1", Title: canary("title"), Class: "implement", State: task.StateCompleted, Evidence: canary("evidence"), CreatedAt: now, UpdatedAt: now}},
		Decisions: []Decision{{At: now, Kind: "exit_refused", Reason: canary("decision")}},
		TrainRuns: []TrainRun{{Run: "r1", Refusal: canary("train"), PRs: []int{}, Merged: []int{}}},
		Alerts:    []AlertInput{{ID: "a1", Kind: "ungated_tag", Subject: canary("alert"), At: now}},
		NeedsYou:  []ThreadInput{{ID: "th1", Seat: "qa", Subject: canary("thread"), Since: now}},
		Warnings:  []string{canary("docwarn")},
	}
	raw, err := json.Marshal(Build(in))
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range canaries {
		if strings.Contains(string(raw), c) {
			t.Errorf("canary %s survived into status --json", c)
		}
	}
	if !strings.Contains(string(raw), "end") {
		t.Fatal("the fields are gone, not redacted")
	}
}
