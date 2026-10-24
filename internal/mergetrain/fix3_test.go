package mergetrain

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

type failingBinder struct{}

func (failingBinder) BindPR(string, int, string) error { return errors.New("disk full") }

// P1-4 (lead ruling on fix 2): the train re-binds a task right after its own
// rebase push, so the rebase never refuses its PR; a push the harness did not
// make and re-bind is pr_head_moved at merge; a failed re-bind stops the run
// before the merge (binding_failed).
func TestTrainRebindsAfterItsRebaseAndRefusesAnUnboundPush(t *testing.T) {
	setup := func(t *testing.T) (*fixture, string, Request) {
		f := newFixture(t)
		dir := filepath.Join(f.dir, "root", "extensions", "amq-squad", "tasks")
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		for id, pr := range map[string]int{"t2": 2, "t4": 4} {
			files := []string{"shared.txt", "pkg/b.go"}
			if pr == 4 {
				files = []string{"shared.txt", "pkg/d.go"}
			}
			raw, _ := json.Marshal(map[string]any{"id": id, "title": "kept", "declared_files": files, "pr_number": pr, "pr_head": f.head("pr" + string(rune('0'+pr)))})
			if err := os.WriteFile(filepath.Join(dir, id+".json"), raw, 0o600); err != nil {
				t.Fatal(err)
			}
		}
		req := Request{Schema: 1, Repository: fixtureRepo, Base: "main", PRs: []Item{{Number: 2}, {Number: 4}}}
		if err := BindTasks(dir, &req); err != nil {
			t.Fatal(err)
		}
		f.train.Binder = FileBinder{Dir: dir}
		return f, dir, req
	}

	// the rebase re-binds and lands
	f, dir, req := setup(t)
	res, err := f.train.Run(context.Background(), req)
	if err != nil || res.PRs[0].Status != "merged" || res.PRs[1].Status != "merged" {
		t.Fatalf("rebased PR refused: %v %+v", err, res.PRs)
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "t4.json"))
	var t4 map[string]any
	if err := json.Unmarshal(raw, &t4); err != nil || t4["pr_head"] != res.PRs[1].Head || t4["title"] != "kept" {
		t.Fatalf("task not re-bound to the rebased head %s: %s", res.PRs[1].Head, raw)
	}
	entries, _ := f.train.Audit.Read()
	if !slices.ContainsFunc(entries, func(e Entry) bool {
		return e.Kind == "rebind" && e.PR == 4 && e.NewHead == res.PRs[1].Head && e.Run == res.Run
	}) {
		t.Fatal("no rebind audit entry")
	}

	// a push the harness did not make is refused at merge
	g, _, greq := setup(t)
	g.train.Forge = &hookForge{GH: g.gh, n: 5, hook: func() { g.seatPush("pr4") }} // after the plan, before PR 4 lands
	greq.PRs = greq.PRs[1:]
	gres, err := g.train.Run(context.Background(), greq)
	if err != nil || gres.PRs[0].Status != "refused" || !slices.ContainsFunc(gres.PRs[0].Refusals, func(r Refusal) bool { return r.Rule == RulePRHeadMoved }) {
		t.Fatalf("an unbound push merged: %v %+v", err, gres.PRs)
	}

	// a failed re-bind stops the run before the merge
	h, _, hreq := setup(t)
	h.train.Binder = failingBinder{}
	hres, err := h.train.Run(context.Background(), hreq)
	if !errors.Is(err, ErrBindingFailed) || hres.PRs[1].Status == "merged" || strings.Count(mustGit(t, h.bare, "log", "--format=%s", "main"), "squash") != 1 {
		t.Fatalf("a failed re-bind did not stop the run: %v %+v", err, hres.PRs)
	}
}
