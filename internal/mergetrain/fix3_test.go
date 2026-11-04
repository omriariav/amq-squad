package mergetrain

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/v3/internal/task"
)

type failingBinder struct{ Binder }

func (failingBinder) BindPR(string, string, int, string) error { return errors.New("disk full") }
func (b failingBinder) RecordPRMerge(taskID, repository string, pr int, commit string) error {
	return b.Binder.RecordPRMerge(taskID, repository, pr, commit)
}

// P1-4 (lead rulings on fixes 2 and 4): after its own rebase the train binds
// the SHA its rebase reported, audit entry first; the merge-time check reads
// the store. The rebase lands; a push right after the rebase is
// pr_head_moved, not a re-bind; a push the harness did not make is refused at
// merge; a failed re-bind stops the run before the merge (binding_failed).
func TestTrainRebindsAfterItsRebaseAndRefusesAnUnboundPush(t *testing.T) {
	t.Parallel()
	setup := func(t *testing.T) (*fixture, Request) {
		f := newFixture(t)
		f.bindTask(2, f.head("pr2"), []string{"shared.txt", "pkg/b.go"})
		f.bindTask(4, f.head("pr4"), []string{"shared.txt", "pkg/d.go"})
		req := Request{Schema: 1, Repository: fixtureRepo, Base: "main", PRs: []Item{{Number: 2}, {Number: 4}}}
		f.realRun(&req)
		return f, req
	}
	bound := func(f *fixture, pr int) task.Task {
		all, _ := f.tasks().List()
		for _, tk := range all {
			if tk.PRNumber == pr {
				return tk
			}
		}
		return task.Task{}
	}

	// the rebase binds the pushed SHA and lands; audit entry before the merge
	f, req := setup(t)
	res, err := f.train.Run(context.Background(), req)
	if err != nil || res.PRs[0].Status != "merged" || res.PRs[1].Status != "merged" {
		t.Fatalf("rebased PR refused: %v %+v", err, res.PRs)
	}
	if b := bound(f, 4); b.PRHead != res.PRs[1].Head || b.Title == "" {
		t.Fatalf("task not re-bound to the pushed head %s: %+v", res.PRs[1].Head, b)
	}
	entries, _ := f.train.Audit.Read()
	ri := slices.IndexFunc(entries, func(e Entry) bool {
		return e.Kind == "rebind" && e.PR == 4 && e.NewHead == res.PRs[1].Head && e.Run == res.Run
	})
	mi := slices.IndexFunc(entries, func(e Entry) bool { return e.Kind == "merge" && e.PR == 4 })
	if ri < 0 || mi < ri {
		t.Fatalf("rebind audit entry missing or after the merge (%d, %d)", ri, mi)
	}

	// a push right after the server's rebase: the pushed SHA is bound, the
	// read-back differs, pr_head_moved
	c, creq := setup(t)
	c.edit(func(s *fakeState) { s.PushAfterRebase = []string{"4"} })
	cres, err := c.train.Run(context.Background(), creq)
	if err != nil || cres.PRs[1].Status != "refused" || !slices.ContainsFunc(cres.PRs[1].Refusals, func(r Refusal) bool { return r.Rule == RulePRHeadMoved }) {
		t.Fatalf("a push after the rebase was bound or merged: %v %+v", err, cres.PRs)
	}
	if b := bound(c, 4); b.PRHead == c.head("pr4") {
		t.Fatalf("the read-back head was bound: %+v", b)
	}

	// a push the harness did not make is refused at merge
	g, greq := setup(t)
	g.train.Forge = &hookForge{GH: g.gh, n: 5, hook: func() { g.seatPush("pr4") }} // after the plan, before PR 4 lands
	greq.PRs = greq.PRs[1:]
	gres, err := g.train.Run(context.Background(), greq)
	if err != nil || gres.PRs[0].Status != "refused" || !slices.ContainsFunc(gres.PRs[0].Refusals, func(r Refusal) bool { return r.Rule == RulePRHeadMoved }) {
		t.Fatalf("an unbound push merged: %v %+v", err, gres.PRs)
	}

	// a failed re-bind stops the run before the merge
	h, hreq := setup(t)
	h.train.Binder = failingBinder{Binder: h.train.Binder}
	hres, err := h.train.Run(context.Background(), hreq)
	if !errors.Is(err, ErrBindingFailed) || hres.PRs[1].Status == "merged" || strings.Count(mustGit(t, h.bare, "log", "--format=%s", "main"), "squash") != 1 {
		t.Fatalf("a failed re-bind did not stop the run: %v %+v", err, hres.PRs)
	}
	hentries, _ := h.train.Audit.Read()
	if !slices.ContainsFunc(hentries, func(e Entry) bool { return e.Kind == "rebind" && e.PR == 4 }) {
		t.Fatal("the rebind audit entry was not written before the binding write")
	}
}
