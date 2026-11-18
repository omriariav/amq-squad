package status

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/scheduler"
	"github.com/omriariav/amq-squad/v3/internal/task"
)

var now = time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func message(t *testing.T, root, owner, id, from, to, thread, subject, kind string, at time.Time) {
	t.Helper()
	write(t, filepath.Join(root, "agents", owner, "inbox", "new", id+".md"), fmt.Sprintf("---json\n{\n  \"schema\": 1,\n  \"id\": %q,\n  \"from\": %q,\n  \"to\": [%q],\n  \"thread\": %q,\n  \"subject\": %q,\n  \"created\": %q,\n  \"kind\": %q\n}\n---\nbody of %s\n",
		id, from, to, thread, subject, at.Format(time.RFC3339Nano), kind, id))
}

func launchRecord(t *testing.T, root, handle, binary, model string, pid int) {
	t.Helper()
	// a launched seat has its AMQ mailbox, and seat-exec writes its exec
	// record under the staged directory since C5a (launch.ExecPath)
	if err := os.MkdirAll(filepath.Join(root, "agents", handle), 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(root, "extensions", "amq-squad", "launch", handle, "exec", "launch.json"),
		fmt.Sprintf(`{"schema":1,"handle":%q,"role":"implementer","binary":%q,"model":%q,"cwd":"/repo","agent_pid":%d,"started_at":"2026-10-24T11:00:00Z","root":%q}`, handle, binary, model, pid, root))
}

// seed builds a session root with one row per bucket and every classifier
// state: impl-1 works on t1 (claimed), impl-2 is an idle live seat, impl-3
// raised a gate, impl-4 holds an expired unclaimed lease and is dead, t3
// waits on seat_cap, t4 is blocked for the lead, t5 is completed.
func seed(t *testing.T) (string, []GateInput) {
	t.Helper()
	root := t.TempDir()
	store, _ := task.Open(root)
	sonnet := task.Pool{Runtime: "claude", Model: "sonnet", Effort: "medium"}
	add := func(title string, p *task.Pool, deps ...string) {
		if _, err := store.Add(task.AddInput{Title: title, Class: "implement", Pool: p, DependsOn: deps, ProjectPath: "/repo"}, now.Add(-time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
	add("Fix the parser", &sonnet)
	add("Lapsed", &sonnet)
	add("Needs a seat", &sonnet)
	add("Needs a decision", nil)
	add("Done already", nil)
	must := func(_ task.Task, err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(store.Claim("t1", "impl-1", nil, 10*time.Minute, now.Add(-time.Minute)))
	must(store.Grant("t2", "impl-4", nil, 10*time.Minute, now.Add(-20*time.Minute)))
	_, _ = store.SetWait("t3", task.WaitSeatCap, now.Add(-2*time.Minute))
	must(store.Block("t4", "lead", "needs_decision", "pick one", task.RouteLead, now.Add(-3*time.Minute)))
	must(store.Claim("t5", "impl-2", nil, 10*time.Minute, now.Add(-50*time.Minute)))
	harnessPass(t, store, "t5", now.Add(-45*time.Minute))
	must(store.Done("t5", "impl-2", "ok", "", now.Add(-45*time.Minute)))

	launchRecord(t, root, "impl-1", "/usr/local/bin/claude", "sonnet", 101)
	launchRecord(t, root, "impl-2", "codex", "gpt-5.4", 102)
	launchRecord(t, root, "impl-3", "claude", "opus", 103)
	launchRecord(t, root, "impl-4", "claude", "sonnet", 104) // dead
	write(t, filepath.Join(root, "agents", "user", "inbox", "new", ".keep"), "")
	write(t, filepath.Join(root, "agents", "impl-2", "presence.json"), `{"status":"active","last_seen":"2026-10-24T11:59:30Z"}`)
	ledger := filepath.Join(root, "extensions", "amq-squad", "ledger", "impl-1.jsonl")
	write(t, ledger, strings.Join([]string{
		`{"schema":1,"seq":1,"at":"2026-10-24T11:58:00Z","handle":"impl-1","event":"PostToolUse","tool":"Bash","outcome":"ok","payload":{"usage":{"input_tokens":1000,"output_tokens":200}}}`,
		`{"schema":1,"seq":2,"at":"2026-10-24T11:59:00Z","handle":"impl-1","event":"PostToolUse","tool":"Edit","outcome":"ok","payload":{"usage":{"input_tokens":500,"output_tokens":50}}}`,
	}, "\n")+"\n")
	message(t, root, "lead", "m1", "impl-1", "lead", "p2p/impl-1__lead", "starting t1: parser fix", "status", now.Add(-2*time.Minute))
	message(t, root, "user", "m2", "impl-3", "user", "p2p/impl-3__user", "Approve the release tag?", "question", now.Add(-5*time.Minute))
	write(t, scheduler.StatePathFor(root), `{"max_seats":3,"live_seats":3,"seats":[]}`)
	gates := []GateInput{{ID: "g1", Class: "tag", Digest: "sha256:8502aa99bb11", State: "open", RaisedBy: "impl-3", Since: now.Add(-4 * time.Minute)}}
	return root, gates
}

func alive(pid int) bool { return pid == 101 || pid == 102 || pid == 103 }

func build(t *testing.T) Status {
	t.Helper()
	root, gates := seed(t)
	return Build(Collect(CollectOptions{Root: root, Session: "squad", ProjectPath: "/repo", ProducerVersion: "test", Now: now, Gates: gates, PIDAlive: alive}))
}

func seatRow(s Status, h string) Seat {
	for _, r := range s.Seats {
		if r.Handle == h {
			return r
		}
	}
	return Seat{}
}

func taskRow(s Status, id string) Task {
	for _, r := range s.Tasks {
		if r.ID == id {
			return r
		}
	}
	return Task{}
}

// Acceptance 6: the produced document validates against the golden fixture.
func TestBuiltStatusValidatesAgainstTheGolden(t *testing.T) {
	st := build(t)
	raw, err := json.Marshal(st)
	if err != nil {
		t.Fatal(err)
	}
	if err := CheckAdditive(readGolden(t), raw); err != nil {
		t.Fatalf("status --json does not validate against the golden fixture: %v\n%s", err, raw)
	}
	actions := []Action{st.Actions.NewTask}
	for _, a := range st.Attention {
		if a.Action != nil {
			actions = append(actions, *a.Action)
		}
	}
	for _, a := range actions {
		if err := ValidateAction(a); err != nil {
			t.Error(err)
		}
	}
}

func TestClassifierAndBuckets(t *testing.T) {
	st := build(t)
	seats := []struct {
		handle, task string
		state        State
		reason       string
		bucket       Bucket
	}{
		{"impl-1", "t1", StateOnline, "lease_claimed", BucketWorking},
		{"impl-2", "", StateOnline, "idle", BucketReady},
		{"impl-3", "", StateNeedsYou, "gate_open", BucketNeedsYou},
		{"impl-4", "t2", StateStale, "no_live_signal", BucketInactive}, // holds the expired lease until the scheduler ticks
		{"lead", "", StateUnknown, "no_signal", BucketInactive},        // a mailbox and nothing else
	}
	for _, c := range seats {
		r := seatRow(st, c.handle)
		if r.TaskID != c.task || r.State != c.state || r.ReasonCode != c.reason || r.Bucket != c.bucket {
			t.Errorf("seat %s = task %q %s/%s %s, want %q %s/%s %s", c.handle, r.TaskID, r.State, r.ReasonCode, r.Bucket, c.task, c.state, c.reason, c.bucket)
		}
	}
	if len(st.Seats) != 5 || seatRow(st, "lead").BucketMarker != MarkerUnknown {
		t.Errorf("the operator mailbox must not be a seat; lead is unknown with a marker: %+v", st.Seats)
	}
	tasks := []struct {
		id     string
		state  State
		reason string
		bucket Bucket
	}{
		{"t1", StateOnline, "lease_claimed", BucketWorking},
		{"t2", StateStale, "lease_expired", BucketInactive},
		{"t3", StateWaiting, task.WaitSeatCap, BucketWorking},
		{"t4", StateBlocked, "needs_decision", BucketWorking},
		{"t5", StateStale, "task_completed", BucketInactive},
	}
	for _, c := range tasks {
		r := taskRow(st, c.id)
		if r.State != c.state || r.ReasonCode != c.reason || r.Bucket != c.bucket {
			t.Errorf("task %s = %s/%s %s, want %s/%s %s", c.id, r.State, r.ReasonCode, r.Bucket, c.state, c.reason, c.bucket)
		}
	}
	c := st.Counts
	if c.All != 5+3 || c.NeedsYou+c.Working+c.Ready+c.Inactive != c.All || c.NeedsYou != 1 || c.Ready != 1 {
		t.Errorf("counts = %+v (5 tasks and the 3 seats without a task)", c)
	}
	r1 := seatRow(st, "impl-1")
	if r1.Runtime != "claude" || r1.Model != "sonnet" || r1.PromptState != "running" || r1.TokensIn == nil || *r1.TokensIn != 1500 || *r1.TokensOut != 250 ||
		r1.LastMessage != "starting t1: parser fix: body of m1" || r1.LastActivityAt == nil || r1.ProjectPath != "/repo" {
		t.Errorf("impl-1 details = %+v", r1)
	}
	if r2 := seatRow(st, "impl-2"); r2.TokensIn != nil || r2.TokensOut != nil {
		t.Errorf("a seat with no token numbers must carry null: %+v", r2)
	}
}

func TestAttentionQueue(t *testing.T) {
	st := build(t)
	var ids []string
	for _, a := range st.Attention {
		ids = append(ids, a.ID+"/"+a.Severity)
	}
	want := []string{
		"thread:p2p/impl-3__user/high", // older than the gate
		"gate:g1/high",
		"seat:impl-4:stale/medium", // zero since: the dead seat left no activity
		"task:t2:lease_expired/medium",
		"task:t4:blocked/medium",
		"task:t3:wait:seat_cap/low",
	}
	if !reflect.DeepEqual(ids, want) {
		t.Fatalf("attention = %v\nwant       %v", ids, want)
	}
	if a := st.Attention[3]; a.Action == nil || a.Action.ID != "task.reconcile" {
		t.Fatalf("expired lease action = %+v", a.Action)
	}
	if !strings.Contains(strings.Join(st.Capabilities, ","), "seat.tokens:evidence_ledger") {
		t.Fatalf("capabilities = %v", st.Capabilities)
	}
}

func TestBuildIsDeterministic(t *testing.T) {
	root, gates := seed(t)
	o := CollectOptions{Root: root, Session: "squad", Now: now, Gates: gates, PIDAlive: alive}
	a, _ := json.Marshal(Build(Collect(o)))
	b, _ := json.Marshal(Build(Collect(o)))
	if string(a) != string(b) {
		t.Fatalf("two builds differ:\n%s\n%s", a, b)
	}
}

func TestEmptyRootIsAValidDocument(t *testing.T) {
	st := Build(Collect(CollectOptions{Root: t.TempDir(), Now: now}))
	raw, _ := json.Marshal(st)
	if err := CheckAdditive(readGolden(t), raw); err != nil {
		t.Fatalf("an empty root: %v", err)
	}
	if st.Counts.All != 0 || len(st.Warnings) != 0 {
		t.Fatalf("empty: %+v", st)
	}
}

func TestUsageLimitAttention(t *testing.T) {
	since, until := now.Add(-time.Hour), now.Add(time.Hour)
	st := Build(Input{Now: now, Scheduler: scheduler.State{MaxSeats: 12, UsageLimitSince: &since, UsageLimitUntil: &until}})
	if len(st.Attention) != 1 || st.Attention[0].Kind != "usage_limit" || st.Scheduler.UsageLimitUntil == nil {
		t.Fatalf("usage limit: %+v", st.Attention)
	}
}

// The seat's live field (4-5 ruling): an open gate or a block overrides
// liveness in the state, never in live; a live lease counts as live.
func TestSeatLiveIsIndependentOfTheBucket(t *testing.T) {
	gate := []GateInput{{ID: "g1", Class: "tag", Digest: "sha256:" + strings.Repeat("0", 64), State: "open", RaisedBy: "a", Since: now}}
	lease := []task.Task{{ID: "t1", State: task.StateInProgress, Lease: &task.Lease{Holder: "c", GrantedAt: now.Add(-time.Minute), ExpiresAt: now.Add(9 * time.Minute)}}}
	st := Build(Input{Now: now, Gates: gate, Tasks: lease, Seats: []SeatInput{
		{Handle: "a", Live: true, HasSignal: true}, // needs_you, running
		{Handle: "b", HasSignal: true},             // stopped
		{Handle: "c", HasSignal: true},             // no session signal, live lease
	}})
	want := map[string]struct {
		state State
		live  bool
	}{"a": {StateNeedsYou, true}, "b": {StateStale, false}, "c": {StateStale, true}}
	for _, s := range st.Seats {
		if w := want[s.Handle]; s.State != w.state || s.Live != w.live {
			t.Errorf("seat %s: state %s live %v, want %s %v", s.Handle, s.State, s.Live, w.state, w.live)
		}
	}
}

// followups-b (Codex on #1020): each task row's next is task.Next of the
// stored task with no caller (status names no viewer), at the build's clock,
// in every state the fixture has: the in-progress t1 carries the lead's
// entries, and the completed t5 carries none. The build runs 37 minutes past
// the seed's whole hour, so t1's lease (to 12:09) has lapsed: a next taken
// at another time differs. t6, which the lead created and whose seat asked
// to widen it to two more files, carries the widen entries and the
// creator's reset: a next taken from part of the record, or for another
// caller, differs. t7, reported and waiting for the commit step, has a lease
// live at the clock (a next taken at the wall clock differs) and no task
// done (a next taken without its report differs); t8, blocked for the
// owner, carries the owner's answer (a caller without the owner differs);
// t9, claimed and live with no report, carries the holder's task done.
func TestEachTaskRowCarriesTaskNext(t *testing.T) {
	root, gates := seed(t)
	store, err := task.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Add(task.AddInput{Title: "Created by the lead", Class: "implement", ProjectPath: "/repo", CreatedBy: "lead"}, now.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Claim("t6", "impl-5", nil, 10*time.Minute, now.Add(-2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.BlockWithPaths("t6", "impl-5", task.ReasonWidenRequested, "two more files", task.RouteLead, []string{"internal/a.go", "internal/b.go"}, now.Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	clock := now.Add(37 * time.Minute)
	// t7: reported, waiting for the commit step, its lease live at the clock
	if _, err := store.Add(task.AddInput{Title: "Reported", Class: "implement", ProjectPath: "/repo", DeclaredFiles: []string{"internal/x.go"}}, now.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Claim("t7", "impl-6", nil, 30*time.Minute, clock.Add(-2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Done("t7", "impl-6", "ok", "", clock.Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	// t8: blocked for the owner
	if _, err := store.Add(task.AddInput{Title: "For the owner", Class: "implement", ProjectPath: "/repo"}, now.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Block("t8", "lead", "needs_decision", "the owner decides", task.RouteOwner, clock.Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	// t9: claimed, no report, its lease live at the clock (12:37) and ending
	// at 12:45, before the next whole hour: a next taken at the time rounded
	// to the hour differs
	if _, err := store.Add(task.AddInput{Title: "Working", Class: "implement", ProjectPath: "/repo"}, now.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Claim("t9", "impl-7", nil, 10*time.Minute, clock.Add(-2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	st := Build(Collect(CollectOptions{Root: root, Session: "squad", ProjectPath: "/repo", ProducerVersion: "test", Now: clock, Gates: gates, PIDAlive: alive}))
	withEntries, empty := 0, 0
	for _, id := range []string{"t1", "t2", "t3", "t4", "t5", "t6", "t7", "t8", "t9"} {
		stored, err := store.Get(id)
		if err != nil {
			t.Fatal(err)
		}
		want := task.Next(stored, clock, task.NextCaller{})
		if got := taskRow(st, id).Next; !reflect.DeepEqual(got, want) {
			t.Errorf("task %s (%s): the row's next = %+v, want task.Next = %+v", id, stored.State, got, want)
		}
		if len(want) > 0 {
			withEntries++
		} else {
			empty++
		}
	}
	if withEntries == 0 || empty == 0 {
		t.Fatalf("the fixture has %d rows with entries and %d empty; the check wants both", withEntries, empty)
	}
	if t1, t5 := taskRow(st, "t1"), taskRow(st, "t5"); len(t1.Next) == 0 || t5.Next == nil || len(t5.Next) != 0 {
		t.Fatalf("t1 (%s) next = %+v, want entries; t5 (%s) next = %#v, want an empty list", t1.TaskState, t1.Next, t5.TaskState, t5.Next)
	}
	// the fixture tells the record apart: t1's lapsed lease leaves no holder
	// entry, and t6 has widen entries and a reset only its creator may run
	for _, e := range taskRow(st, "t1").Next {
		if e.Who == task.NextHolder && e.Verb != "task release" {
			t.Fatalf("t1's lapsed lease still lists the holder's %q: %+v", e.Verb, taskRow(st, "t1").Next)
		}
	}
	widen, reset := false, false
	for _, e := range taskRow(st, "t6").Next {
		widen = widen || e.Verb == "task widen"
		reset = reset || (e.Verb == "task reset" && strings.Contains(e.MayRefuse, "only its creator lead"))
	}
	if !widen || !reset {
		t.Fatalf("t6 next = %+v, want task widen and task reset with its creator", taskRow(st, "t6").Next)
	}
	// t7's live lease lists the holder's entries but not task done (its
	// report is filed); t8 lists the owner's answer through Buzz
	holder, done := false, false
	for _, e := range taskRow(st, "t7").Next {
		holder = holder || e.Who == task.NextHolder
		done = done || e.Verb == "task done"
	}
	if !holder || done {
		t.Fatalf("t7 next = %+v, want the holder's entries and no task done", taskRow(st, "t7").Next)
	}
	owner := false
	for _, e := range taskRow(st, "t8").Next {
		owner = owner || (e.Who == task.NextOwner && e.Verb == "answer through Buzz")
	}
	if !owner {
		t.Fatalf("t8 next = %+v, want the owner's answer through Buzz", taskRow(st, "t8").Next)
	}
	// t9's live lease and no report: the holder's task done is listed
	done = false
	for _, e := range taskRow(st, "t9").Next {
		done = done || (e.Who == task.NextHolder && e.Verb == "task done")
	}
	if !done {
		t.Fatalf("t9 next = %+v, want the holder's task done", taskRow(st, "t9").Next)
	}
}
