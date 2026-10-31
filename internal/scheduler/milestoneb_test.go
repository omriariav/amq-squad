package scheduler

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/task"
)

type scripted struct{ results []CommitResult }

func (s *scripted) Commit(_ string, _ task.Task) CommitResult {
	r := s.results[0]
	if len(s.results) > 1 {
		s.results = s.results[1:]
	}
	return r
}

// leadBell records what the lead is told, and what the store said at that
// moment (the record comes first).
type leadBell struct {
	h     *harness
	got   []task.Task
	store []task.Task
}

func (l *leadBell) CommitRefused(t task.Task) error {
	l.got = append(l.got, t)
	l.store = append(l.store, l.h.get(t.ID))
	return nil
}

// 2b-2 milestone B: a deferred commit step writes nothing and runs again on
// the next tick; a refusal is recorded in the store first and then sent to
// the lead.
func TestCommitDeferralAndTheLeadNotice(t *testing.T) {
	h := newHarness(t, Config{IdleTimeout: time.Hour, Lead: "lead", StatePath: filepath.Join(t.TempDir(), "scheduler.json")})
	c := &scripted{results: []CommitResult{{Deferred: true, Detail: "push: network down"}, {ReasonCode: "outside_declared_files", Detail: "README.md"}}}
	bell := &leadBell{h: h}
	h.s.Committer, h.s.Lead = c, bell
	if _, err := h.store.Add(task.AddInput{Title: "edit", Class: "implement", Pool: &sonnet, DeclaredFiles: []string{"src/"}}, t0); err != nil {
		t.Fatal(err)
	}
	h.up("seat-a", sonnet)
	h.tick()
	if _, err := h.store.Claim("t1", "seat-a", nil, 0, t0); err != nil {
		t.Fatal(err)
	}
	harnessPass(t, h.store, "t1", t0)
	if _, err := h.store.Done("t1", "seat-a", "ok", "", t0); err != nil {
		t.Fatal(err)
	}
	if d := h.tick(); !has(d, "commit_deferred", "t1", "seat-a") || len(bell.got) != 0 {
		t.Fatalf("deferral: %+v", d)
	}
	if got := h.get("t1"); got.State != task.StateInProgress || got.Report == nil {
		t.Fatalf("a deferral changed the task: %+v", got)
	}
	if d := h.tick(); !has(d, "commit_refused", "t1", "seat-a") {
		t.Fatalf("refusal: %+v", d)
	}
	if len(bell.got) != 1 || bell.got[0].BlockReason != "outside_declared_files" || bell.store[0].State != task.StateBlocked {
		t.Fatalf("lead notice: %+v (store at notice %+v)", bell.got, bell.store)
	}
	st, err := ReadState(h.s.Config.StatePath)
	if err != nil || st.Lead != "lead" {
		t.Fatalf("state: %+v %v", st, err)
	}
}

// The scheduler binds the task to the PR and head the commit step returned
// (pr_number, pr_head: the binding 3-2's merge train reads).
func TestCommitBindsThePR(t *testing.T) {
	h := newHarness(t, Config{IdleTimeout: time.Hour})
	head := strings.Repeat("c", 40)
	h.s.Committer = &scripted{results: []CommitResult{{OK: true, PRRepository: "o/r", PRNumber: 42, PRHead: head}}}
	if _, err := h.store.Add(task.AddInput{Title: "a", Class: "implement", Pool: &sonnet, DeclaredFiles: []string{"a/"}}, t0); err != nil {
		t.Fatal(err)
	}
	h.up("seat-a", sonnet)
	h.tick()
	h.store.Claim("t1", "seat-a", nil, 0, t0)
	harnessPass(t, h.store, "t1", t0)
	h.store.Done("t1", "seat-a", "ok", "", t0)
	if d := h.tick(); !has(d, "pr_bound", "t1", "seat-a") {
		t.Fatalf("decisions: %+v", d)
	}
	if got := h.get("t1"); got.State != task.StateCompleted || got.PRRepository != "o/r" || got.PRNumber != 42 || got.PRHead != head {
		t.Fatalf("binding: %+v", got)
	}
	if _, err := h.store.BindPR("t1", "o/r", 0, "", t0); err == nil {
		t.Fatal("a binding without a head")
	}
}

type preparer struct {
	calls []string
	err   error
}

func (p *preparer) PrepareSeat(h string) error { p.calls = append(p.calls, h); return p.err }

// The preparer runs on an idle seat before its grant; an error keeps the
// seat out of this tick (seat_prepare_failed) and grants nothing.
func TestPreparerRunsBeforeAGrant(t *testing.T) {
	h := newHarness(t, Config{IdleTimeout: time.Hour})
	p := &preparer{err: errors.New("dirty")}
	h.s.Preparer = p
	h.add("a", &sonnet)
	h.up("seat-a", sonnet)
	if d := h.tick(); !has(d, "seat_prepare_failed", "t1", "seat-a") || has(d, "dispatched", "t1", "seat-a") {
		t.Fatalf("a failed prepare: %+v", d)
	}
	p.err = nil
	if d := h.tick(); !has(d, "dispatched", "t1", "seat-a") {
		t.Fatalf("after the prepare: %+v", d)
	}
	if len(p.calls) != 2 || p.calls[1] != "seat-a" {
		t.Fatalf("calls: %v", p.calls)
	}
}
