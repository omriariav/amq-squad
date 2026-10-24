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
	write(t, filepath.Join(root, "agents", handle, "extensions", "io.github.omriariav.amq-squad", "launch.json"),
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
