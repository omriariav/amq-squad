package cli

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/launch"
	"github.com/omriariav/amq-squad/v3/internal/status"
	"github.com/omriariav/amq-squad/v3/internal/task"
)

// S3-1 (3.3.0): task show --json carries next beside the stored task, never
// in it; with no --me every role's entries, with --me the roles that handle
// holds (the lease holder; the lead by its launch record's role).
func TestTaskShowJSONCarriesNextBesideTheTask(t *testing.T) {
	root := t.TempDir()
	t.Setenv("AM_ROOT", root)
	t.Setenv("AM_ME", "")
	now := withFixedTaskNow(t)
	s, err := task.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	added, err := s.Add(task.AddInput{Title: "t", Class: "plan"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Claim(added.ID, "seat-a", nil, time.Hour, now); err != nil {
		t.Fatal(err)
	}
	leadDir := filepath.Join(root, "agents", "cto")
	if err := launch.WithRecordLock(leadDir, func() error {
		return launch.WriteUnderRecordLock(leadDir, launch.Record{Handle: "cto", Root: root, Role: status.LeadRole, StartedAt: now})
	}); err != nil {
		t.Fatal(err)
	}
	holder := []task.NextEntry{{Verb: "task block", Who: task.NextHolder}, {Verb: "task done", Who: task.NextHolder}, {Verb: "task fail", Who: task.NextHolder}, {Verb: "task release", Who: task.NextHolder}}
	lead := []task.NextEntry{{Verb: "task cancel", Who: task.NextLead}, {Verb: "task release", Who: task.NextLead}}
	for _, c := range []struct {
		name string
		args []string
		want []task.NextEntry
	}{
		{"no --me: every role", []string{"show", added.ID, "--json"}, append(append([]task.NextEntry{}, holder...), lead...)},
		{"--me the holder", []string{"show", added.ID, "--json", "--me", "seat-a"}, holder},
		{"--me the lead", []string{"show", added.ID, "--json", "--me", "cto"}, lead},
		{"--me another seat", []string{"show", added.ID, "--json", "--me", "seat-b"}, []task.NextEntry{}},
	} {
		t.Run(c.name, func(t *testing.T) {
			out, err := taskCLI(t, c.args...)
			if err != nil {
				t.Fatal(err)
			}
			var env struct {
				Data map[string]json.RawMessage `json:"data"`
			}
			if err := json.Unmarshal([]byte(out), &env); err != nil {
				t.Fatalf("%v: %s", err, out)
			}
			var next []task.NextEntry
			if err := json.Unmarshal(env.Data["next"], &next); err != nil || next == nil {
				t.Fatalf("data.next = %s (%v)", env.Data["next"], err)
			}
			if !reflect.DeepEqual(next, c.want) {
				t.Fatalf("next = %+v\nwant   %+v", next, c.want)
			}
			var stored map[string]json.RawMessage
			if err := json.Unmarshal(env.Data["task"], &stored); err != nil {
				t.Fatal(err)
			}
			if _, ok := stored["next"]; ok {
				t.Fatal("data.task carries next: the stored record never does")
			}
		})
	}
}

// s31-fix (Codex on #1017): task show checks --me (or AM_ME) with the CLI's
// handle check before any lookup, so a path such as seat-a/../cto is refused
// as invalid --me and never reads another seat's launch record; and a launch
// record names the lead only for the handle it records, so a record whose
// handle is another seat's gives no lead entries.
func TestTaskShowNextMeIsAHandleAndTheRecordIsItsOwn(t *testing.T) {
	root := t.TempDir()
	t.Setenv("AM_ROOT", root)
	t.Setenv("AM_ME", "")
	now := withFixedTaskNow(t)
	s, err := task.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	added, err := s.Add(task.AddInput{Title: "t", Class: "plan"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Claim(added.ID, "seat-a", nil, time.Hour, now); err != nil {
		t.Fatal(err)
	}
	for dir, handle := range map[string]string{"cto": "cto", "seat-x": "cto"} {
		d := filepath.Join(root, "agents", dir)
		if err := launch.WithRecordLock(d, func() error {
			return launch.WriteUnderRecordLock(d, launch.Record{Handle: handle, Root: root, Role: status.LeadRole, StartedAt: now})
		}); err != nil {
			t.Fatal(err)
		}
	}
	for _, me := range []string{"seat-a/../cto", "../agents/cto", "../../elsewhere/cto", "/abs/agents/cto", "./cto", "seat-a/.."} {
		if out, err := taskCLI(t, "show", added.ID, "--json", "--me", me); err == nil || !strings.Contains(err.Error(), "invalid --me") {
			t.Fatalf("--me %q: %v\n%s", me, err, out)
		}
	}
	// before any lookup (Codex P3 on the prototype): refused without --json,
	// where no launch record is read, and for an unknown task id, before the
	// store is read
	for _, args := range [][]string{{"show", added.ID, "--me", "seat-a/../cto"}, {"show", "t999", "--json", "--me", "seat-a/../cto"}} {
		if out, err := taskCLI(t, args...); err == nil || !strings.Contains(err.Error(), "invalid --me") {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
	}
	t.Setenv("AM_ME", "seat-a/../cto")
	if out, err := taskCLI(t, "show", added.ID, "--json"); err == nil || !strings.Contains(err.Error(), "invalid --me") {
		t.Fatalf("AM_ME seat-a/../cto: %v\n%s", err, out)
	}
	t.Setenv("AM_ME", "")
	out, err := taskCLI(t, "show", added.ID, "--json", "--me", "seat-x")
	if err != nil {
		t.Fatal(err)
	}
	var env struct {
		Data map[string]json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	var next []task.NextEntry
	if err := json.Unmarshal(env.Data["next"], &next); err != nil || next == nil || len(next) != 0 {
		t.Fatalf("--me seat-x, whose launch record names cto: next = %s (%v)", env.Data["next"], err)
	}
}

// s31 round 2 (Codex on #1017): --me never reaches a launch record outside
// the session root. A lead record at <base>/x, outside the root <base>/squad,
// whose handle is the string ../../x, is what <root>/agents/../../x names;
// --me ../../x is refused as invalid --me before any read, and the same
// record gives no other handle the lead's entries.
func TestTaskShowNextMeNeverReadsOutsideTheRoot(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "squad")
	t.Setenv("AM_ROOT", root)
	t.Setenv("AM_ME", "")
	now := withFixedTaskNow(t)
	s, err := task.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	added, err := s.Add(task.AddInput{Title: "t", Class: "plan"}, now)
	if err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(base, "x")
	if err := launch.WithRecordLock(outside, func() error {
		return launch.WriteUnderRecordLock(outside, launch.Record{Handle: "../../x", Root: base, Role: status.LeadRole, StartedAt: now})
	}); err != nil {
		t.Fatal(err)
	}
	if got := filepath.Join(root, "agents", "../../x"); got != outside {
		t.Fatalf("the joined path is %s, not the record outside the root %s", got, outside)
	}
	for _, args := range [][]string{{"show", added.ID, "--json", "--me", "../../x"}, {"show", added.ID, "--me", "../../x"}} {
		out, err := taskCLI(t, args...)
		if err == nil || !strings.Contains(err.Error(), "invalid --me") {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
		if strings.Contains(out, `"lead"`) {
			t.Fatalf("%v: the lead's entries from the record outside the root:\n%s", args, out)
		}
	}
}

// s31 round 2: task claim joins --me into the same path (taskClaimSeatPool,
// the seat's live launch record), so the check in actor refuses --me ../../x
// there too, before the store is touched: the task stays pending, unleased.
func TestTaskClaimMeIsAHandle(t *testing.T) {
	root := t.TempDir()
	t.Setenv("AM_ROOT", root)
	t.Setenv("AM_ME", "")
	now := withFixedTaskNow(t)
	s, err := task.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	added, err := s.Add(task.AddInput{Title: "t", Class: "plan"}, now)
	if err != nil {
		t.Fatal(err)
	}
	for _, me := range []string{"../../x", "seat-a/../cto", "/abs/x"} {
		if out, err := taskCLI(t, "claim", added.ID, "--me", me); err == nil || !strings.Contains(err.Error(), "invalid --me") {
			t.Fatalf("task claim --me %q: %v\n%s", me, err, out)
		}
	}
	got, err := s.Get(added.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != task.StatePending || got.Lease != nil {
		t.Fatalf("a refused claim changed the task: state %s, lease %+v", got.State, got.Lease)
	}
}

// s31 round 2 (the reviewer's condition): the check in actor never refuses a
// handle the squad can have. The squad's real handles, and a handle
// team.ValidateHandle admits that the seat pattern would not (a leading "_"),
// pass task show and task claim; the operator still gets the operator's
// refusal, not invalid --me.
func TestTaskMeAdmitsTheSquadsHandles(t *testing.T) {
	root := t.TempDir()
	t.Setenv("AM_ROOT", root)
	t.Setenv("AM_ME", "")
	now := withFixedTaskNow(t)
	s, err := task.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	added, err := s.Add(task.AddInput{Title: "t", Class: "plan"}, now)
	if err != nil {
		t.Fatal(err)
	}
	for _, me := range []string{"lead", "cto", "dev", "dev2", "reviewer", "qa", "scribe", "senior-dev", "sonnet-dev", "_x"} {
		if out, err := taskCLI(t, "show", added.ID, "--json", "--me", me); err != nil {
			t.Fatalf("task show --me %s: %v\n%s", me, err, out)
		}
	}
	if out, err := taskCLI(t, "claim", added.ID, "--me", "dev2"); err != nil {
		t.Fatalf("task claim --me dev2: %v\n%s", err, out)
	}
	// the operator's name in any case still gets the operator's refusal
	// (Codex on the round-2 prototype: USER)
	for _, op := range []string{"user", "USER", "User"} {
		if out, err := taskCLI(t, "show", added.ID, "--json", "--me", op); err == nil || strings.Contains(err.Error(), "invalid --me") || !strings.Contains(err.Error(), "cannot target operator") {
			t.Fatalf("task show --me %s (the operator): %v\n%s", op, err, out)
		}
	}
}
