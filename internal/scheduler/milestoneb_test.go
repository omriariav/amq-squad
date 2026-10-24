package scheduler

import (
	"path/filepath"
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
