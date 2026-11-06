package mergetrain

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/task"
)

// Brief 3.2.0-3a: GitHub updates a branch asynchronously and answers the
// updatePullRequestBranch mutation with the head as it was (run ab184d07 on
// #894: the mutation answered 68792071, the branch moved to 2555d34a five
// seconds later, and the train bound nothing, refused the moved head and had
// no path back). These rows run the train against the fake's asynchronous
// update: R1 binds the head read back, R2 refuses an update that never lands,
// R3 recovers a branch the train moved without recording the result, and the
// lead's re-bind decision is proved before anything is written.

// strictBound is a strict fixture with task 1 bound to PR 1 at its head and
// the base advanced one commit, so the real run must update the branch.
func strictBound(t *testing.T) (*fixture, string, string, Request) {
	t.Helper()
	f := newFixture(t)
	setStrict(f)
	old := f.head("pr1")
	id := f.bindTask(1, old, []string{"pkg/a.go"})
	advanceBase(f, map[string]string{"base.txt": "advance\n"})
	req := Request{Schema: 1, Repository: fixtureRepo, Base: "main", PRs: []Item{{Number: 1}}}
	f.realRun(&req)
	return f, id, old, req
}

// moveBranch rebases a PR branch onto main by hand in the work clone, as
// GitHub's update does, with edit applied to the rebased commit first, and
// force-pushes it; the fake's timeline gets the force push when event is set.
func moveBranch(f *fixture, branch string, edit func(), event bool) (before, after string) {
	f.t.Helper()
	before = f.head(branch)
	mustGit(f.t, f.work, "fetch", "-q", "origin")
	mustGit(f.t, f.work, "checkout", "-q", "-B", branch, "origin/"+branch)
	mustGit(f.t, f.work, "rebase", "-q", "origin/main")
	if edit != nil {
		edit()
	}
	mustGit(f.t, f.work, "push", "-q", "--force", "origin", branch)
	after = f.head(branch)
	if event {
		f.edit(func(s *fakeState) {
			n := strings.TrimPrefix(branch, "pr")
			s.PRs[n].ForcePushes = append(s.PRs[n].ForcePushes, ForcePush{Before: before, After: after, Actor: "owner", At: forcePushAt()})
		})
	}
	return before, after
}

func kinds(entries []Entry, pr int) []string {
	var out []string
	for _, e := range entries {
		if e.PR == pr || e.Kind == "batch" {
			out = append(out, e.Kind)
		}
	}
	return out
}

func boundHead(t *testing.T, f *fixture, id string) string {
	t.Helper()
	tk, err := f.tasks().Get(id)
	if err != nil {
		t.Fatal(err)
	}
	return tk.PRHead
}

// R1: against the asynchronous fake (the mutation answers the old head; the
// branch moves on the second read), a real run binds the head read back:
// update_requested before the mutation, the rebase entry with the head read
// back, the proof (first parent the base tip, equal patch ids), the rebind,
// CI waited at the new head, and the PR merged with the force-push rule
// quiet. Reverted (the mutation's answer bound, no read-back), the run sees
// no change, binds nothing and the check refuses pr_head_moved.
func TestAsyncUpdateBindsTheHeadReadBack(t *testing.T) {
	f, id, old, req := strictBound(t)
	f.edit(func(s *fakeState) {
		s.AsyncUpdate, s.AsyncViews, s.AuditPath = []string{"1"}, map[string]int{"1": 2}, f.train.Audit.Path
	})
	var sleeps []time.Duration
	f.train.Sleep = func(d time.Duration) { sleeps = append(sleeps, d) }
	res, err := f.train.Run(context.Background(), req)
	if err != nil {
		t.Fatalf("run: %v %+v", err, res)
	}
	p := res.PRs[0]
	if p.Status != "merged" || p.Head == old || len(p.Refusals) != 0 || p.PatchProof == nil || !p.PatchProof.Match || p.PatchProof.NewHead != p.Head {
		t.Fatalf("the head read back was not proved and merged: %+v", p)
	}
	if got := boundHead(t, f, id); got != p.Head {
		t.Fatalf("task bound to %s, want the head read back %s", got, p.Head)
	}
	entries, _ := f.train.Audit.Read()
	var requested, rebase, rebind, merge int = -1, -1, -1, -1
	for i, e := range entries {
		switch e.Kind {
		case "update_requested":
			requested = i
			if e.Head != old || e.PR != 1 || e.Task != id || e.BaseSHA == "" {
				t.Fatalf("update_requested names the wrong head or base: %+v", e)
			}
		case "rebase":
			rebase = i
			if e.Head != old || e.NewHead != p.Head {
				t.Fatalf("the rebase entry does not name the head read back: %+v", e)
			}
		case "rebind":
			rebind = i
		case "merge":
			merge = i
		}
	}
	if requested < 0 || rebase < requested || rebind < rebase || merge < rebind {
		t.Fatalf("entry order (requested %d, rebase %d, rebind %d, merge %d): %v", requested, rebase, rebind, merge, kinds(entries, 1))
	}
	calls := f.calls()
	if strings.Count(calls, "commits/"+p.Head+"/check-runs") < 2 || !strings.Contains(calls, "commits/"+p.Head+" --jq") {
		t.Fatalf("CI not waited at the new head, or its parent not read:\n%s", calls)
	}
	if !slices.Contains(sleeps, time.Second) {
		t.Fatalf("no backoff between the read-backs: %v", sleeps)
	}
	st := loadFake(f.state)
	if len(st.Pending) != 0 {
		t.Fatalf("the update never landed: %+v", st.Pending)
	}
	// the request was in the journal when the mutation was called (Codex on
	// #903: the order of entries alone does not prove it)
	if !slices.Contains(st.AuditAtMutation, "update_requested") || slices.Contains(st.AuditAtMutation, "rebase") {
		t.Fatalf("the journal at the mutation: %v", st.AuditAtMutation)
	}
}

// R2: an update GitHub rejects asynchronously leaves the head unmoved; the
// bounded wait (120 s, backoff 1 s doubling to 16 s) ends with a refusal in
// words that names the limit and the PR, under branch_update_unconfirmed,
// nothing bound, nothing merged, no gate. Reverted (the wait's end taken for
// the update's result), the row fails on the refusal.
func TestUnmovedUpdateIsRefusedInWords(t *testing.T) {
	f, id, old, req := strictBound(t)
	f.edit(func(s *fakeState) { s.AsyncRejected = []string{"1"} })
	now := time.Now()
	var sleeps []time.Duration
	f.train.Now = func() time.Time { return now }
	f.train.Sleep = func(d time.Duration) { now = now.Add(d); sleeps = append(sleeps, d) }
	res, err := f.train.Run(context.Background(), req)
	if err != nil {
		t.Fatalf("run: %v %+v", err, res)
	}
	p := res.PRs[0]
	want := "GitHub did not update the branch of PR #1 within 2m0s"
	if p.Status != "refused" || len(p.Refusals) != 1 || p.Refusals[0].Rule != RuleUpdateUnconfirmed || !strings.HasPrefix(p.Refusals[0].Detail, want) || p.GateID != "" {
		t.Fatalf("the unmoved update was not refused in words: %+v", p)
	}
	if got := boundHead(t, f, id); got != old {
		t.Fatalf("the binding moved to %s", got)
	}
	if strings.Contains(f.calls(), "-X PUT") || f.head("pr1") != old {
		t.Fatalf("something merged or moved:\n%s", f.calls())
	}
	var total time.Duration
	for _, d := range sleeps {
		total += d
	}
	if total != 2*time.Minute || len(sleeps) < 5 || sleeps[0] != time.Second || sleeps[1] != 2*time.Second || sleeps[4] != 16*time.Second {
		t.Fatalf("the wait is not 120 s with the backoff: %v", sleeps)
	}
	entries, _ := f.train.Audit.Read()
	if !slices.ContainsFunc(entries, func(e Entry) bool { return e.Kind == "update_requested" && e.PR == 1 && e.Head == old }) ||
		slices.ContainsFunc(entries, func(e Entry) bool { return e.Kind == "rebase" || e.Kind == "rebind" }) ||
		!slices.ContainsFunc(entries, func(e Entry) bool {
			return e.Kind == "refusal" && e.PR == 1 && slices.Equal(e.Rules, []string{RuleUpdateUnconfirmed}) && strings.HasPrefix(e.Detail, want)
		}) {
		t.Fatalf("the journal: %v", kinds(entries, 1))
	}
}

// R3 (the live shape): an update that lands after the bounded wait is
// refused unconfirmed by the run that asked for it, and recovered by the next
// run from the journaled request: the rebase and rebind entries name the
// recovery, the binding is written, CI is waited at the recovered head, and
// the PR lands.
func TestLateUpdateIsRecoveredByTheNextRun(t *testing.T) {
	f, id, old, req := strictBound(t)
	f.edit(func(s *fakeState) { s.AsyncUpdate, s.AsyncViews = []string{"1"}, map[string]int{"1": 1000} })
	now := time.Now()
	f.train.Now = func() time.Time { return now }
	f.train.Sleep = func(d time.Duration) { now = now.Add(d) }
	first, err := f.train.Run(context.Background(), req)
	if err != nil || first.PRs[0].Status != "refused" || first.PRs[0].Refusals[0].Rule != RuleUpdateUnconfirmed {
		t.Fatalf("the late update was not refused unconfirmed: %v %+v", err, first.PRs)
	}
	f.edit(func(s *fakeState) { s.Pending["1"].Views = 1 }) // GitHub's update lands now
	f.realRun(&req)
	res, err := f.train.Run(context.Background(), req)
	if err != nil || res.PRs[0].Status != "merged" || res.PRs[0].Head == old || len(res.PRs[0].Refusals) != 0 {
		t.Fatalf("the moved branch was not recovered and merged: %v %+v", err, res.PRs)
	}
	if got := boundHead(t, f, id); got != res.PRs[0].Head {
		t.Fatalf("task bound to %s, want %s", got, res.PRs[0].Head)
	}
	entries, _ := f.train.Audit.Read()
	ri := slices.IndexFunc(entries, func(e Entry) bool {
		return e.Kind == "rebase" && e.Run == res.Run && e.Head == old && e.NewHead == res.PRs[0].Head && strings.HasPrefix(e.Detail, "recovered: the update journaled by run "+first.Run)
	})
	bi := slices.IndexFunc(entries, func(e Entry) bool {
		return e.Kind == "rebind" && e.Run == res.Run && e.NewHead == res.PRs[0].Head && strings.HasPrefix(e.Detail, "patch-id: ")
	})
	if ri < 0 || bi < ri {
		t.Fatalf("the recovery's entries (rebase %d, rebind %d): %v", ri, bi, kinds(entries, 1))
	}
}

// R3 (the conditions): a PR whose head moved by the train's unrecorded
// update is re-bound and lands only on the journaled request and the proof;
// without the request on record, or with a differing patch-id, a second
// force push, or a parent that is not a base tip, it stays refused and the
// binding stays. Reverted (each condition), its row merges.
func TestUnrecordedUpdateIsRecoveredOnTheJournalAndTheProof(t *testing.T) {
	journal := func(f *fixture, id, old, base string) {
		f.t.Helper()
		if err := f.train.Audit.Append(Entry{Run: "mt-earlier", Kind: "update_requested", Repository: fixtureRepo, Base: "main", PR: 1, Task: id, Head: old, BaseSHA: base}); err != nil {
			f.t.Fatal(err)
		}
	}
	type row struct {
		name    string
		prepare func(f *fixture, id, old string) // the journal and the move
		merged  bool
		why     string // the reconcile entry's words when refused on the proof
	}
	rows := []row{
		{"journaled, one push onto the journaled base tip", func(f *fixture, id, old string) {
			journal(f, id, old, f.head("main"))
			moveBranch(f, "pr1", nil, true)
		}, true, ""},
		{"the run died after the mutation (the #894 shape): no failed or ambiguous entry, the push inside the window", func(f *fixture, id, old string) {
			journal(f, id, old, f.head("main"))
			moveBranch(f, "pr1", nil, true)
			f.edit(func(s *fakeState) {
				at, _ := time.Parse(time.RFC3339Nano, s.PRs["1"].ForcePushes[0].At)
				s.PRs["1"].ForcePushes[0].At = at.Add(5 * time.Second).UTC().Format(time.RFC3339)
			})
		}, true, ""},
		{"journaled, the base moved since: onto the current tip", func(f *fixture, id, old string) {
			journal(f, id, old, f.head("main"))
			advanceBase(f, map[string]string{"base2.txt": "again\n"})
			moveBranch(f, "pr1", nil, true)
		}, true, ""},
		{"the same push with no journal entry", func(f *fixture, id, old string) {
			moveBranch(f, "pr1", nil, true)
		}, false, ""},
		{"a differing patch-id", func(f *fixture, id, old string) {
			journal(f, id, old, f.head("main"))
			moveBranch(f, "pr1", func() {
				writeFile(f.t, filepath.Join(f.work, "pkg/a.go"), "package pkg\n\nfunc A() {}\n\nfunc Extra() {}\n")
				mustGit(f.t, f.work, "commit", "-q", "-a", "--amend", "--no-edit")
			}, true)
		}, false, "the change moved during the update"},
		{"a second force push", func(f *fixture, id, old string) {
			journal(f, id, old, f.head("main"))
			moveBranch(f, "pr1", nil, true)
			moveBranch(f, "pr1", func() {
				mustGit(f.t, f.work, "commit", "-q", "--amend", "--no-edit", "--date", "2030-01-01T00:00:00Z")
			}, true)
		}, false, "2 force pushes in the journaled request's window"},
		{"a parent that is not the base tip", func(f *fixture, id, old string) {
			journal(f, id, old, f.head("main"))
			moveBranch(f, "pr1", func() {
				writeFile(f.t, filepath.Join(f.work, "pkg/late.go"), "package pkg\n")
				mustGit(f.t, f.work, "add", "-A")
				mustGit(f.t, f.work, "commit", "-q", "-m", "late")
			}, true)
		}, false, "first parent"},
		{"a force push with no time", func(f *fixture, id, old string) {
			journal(f, id, old, f.head("main"))
			moveBranch(f, "pr1", nil, true)
			f.edit(func(s *fakeState) { s.PRs["1"].ForcePushes[0].At = "" })
		}, false, "carries no time"},
	}
	for _, r := range rows {
		t.Run(r.name, func(t *testing.T) {
			f, id, old, req := strictBound(t)
			r.prepare(f, id, old)
			moved := f.head("pr1")
			res, err := f.train.Run(context.Background(), req)
			if err != nil {
				t.Fatalf("run: %v %+v", err, res)
			}
			p := res.PRs[0]
			entries, _ := f.train.Audit.Read()
			bound := boundHead(t, f, id)
			if r.merged {
				if p.Status != "merged" || p.Head != moved || bound != moved {
					t.Fatalf("not recovered and merged: %+v (bound %s, moved %s)", p, bound, moved)
				}
				ri := slices.IndexFunc(entries, func(e Entry) bool {
					return e.Kind == "rebase" && e.Head == old && e.NewHead == moved && strings.HasPrefix(e.Detail, "recovered: the update journaled by run mt-earlier")
				})
				bi := slices.IndexFunc(entries, func(e Entry) bool { return e.Kind == "rebind" && e.NewHead == moved })
				mi := slices.IndexFunc(entries, func(e Entry) bool { return e.Kind == "merge" })
				if ri < 0 || bi < ri || mi < bi {
					t.Fatalf("the recovery's entries: %v", kinds(entries, 1))
				}
				if strings.Contains(r.name, "current tip") && !strings.Contains(entries[ri].Detail, "is the base's current tip") {
					t.Fatalf("the record does not say the base moved: %q", entries[ri].Detail)
				}
				if !strings.Contains(entries[ri].Detail, "inside the request's window") {
					t.Fatalf("the record does not name the window: %q", entries[ri].Detail)
				}
				return
			}
			if p.Status != "refused" || !slices.ContainsFunc(p.Refusals, func(x Refusal) bool { return x.Rule == RulePRHeadMoved }) || bound != old {
				t.Fatalf("recovered without the proof: %+v (bound %s)", p, bound)
			}
			if slices.ContainsFunc(entries, func(e Entry) bool { return e.Kind == "rebind" || (e.Kind == "rebase" && e.Run == res.Run) }) || strings.Contains(f.calls(), "-X PUT") {
				t.Fatalf("something was bound or merged: %v", kinds(entries, 1))
			}
			rec := slices.IndexFunc(entries, func(e Entry) bool { return e.Kind == "reconcile" && e.PR == 1 && e.Run == res.Run })
			if r.why == "" && rec >= 0 {
				t.Fatalf("a recovery was attempted with no journal entry: %+v", entries[rec])
			}
			if r.why != "" && (rec < 0 || !strings.Contains(entries[rec].Detail, r.why) || !strings.HasSuffix(entries[rec].Detail, "binding not recovered")) {
				t.Fatalf("the refusal's record does not name the failing part %q: %d %v", r.why, rec, entries)
			}
		})
	}
}

// Item 6: the lead's re-bind decision is proved by the train before anything
// is written (one force push from the bound head to the named head, the
// named head's first parent a base tip the train rebased onto, equal patch
// ids), then the rebase and rebind entries and the binding; the next run
// lands the PR with the force-push rule quiet. Each failing part is refused
// with its code and nothing is written.
func TestLeadRebindIsProvedBeforeTheBindingIsWritten(t *testing.T) {
	input := func(f *fixture, pr int, from, to string) RebindInput {
		s := f.tasks()
		return RebindInput{Forge: f.gh, Audit: f.train.Audit, Tasks: s, Binder: StoreBinder{Store: s}, PR: pr, From: from, To: to, Lead: "lead"}
	}
	t.Run("proved and bound", func(t *testing.T) {
		f, id, old, req := strictBound(t)
		// the old code's record: a real-run batch at the base it rebased onto
		pinned := f.head("main")
		if err := f.train.Audit.Append(Entry{Run: "mt-earlier", Kind: "batch", Repository: fixtureRepo, Base: "main", BaseSHA: pinned, Pairs: []Pair{{Number: 1, Head: old}}}); err != nil {
			t.Fatal(err)
		}
		_, moved := moveBranch(f, "pr1", nil, true)
		advanceBase(f, map[string]string{"base2.txt": "after the update\n"}) // the base moved on since
		res, err := Rebind(context.Background(), input(f, 1, old, moved))
		if err != nil {
			t.Fatalf("rebind: %v", err)
		}
		if res.Task != id || res.Parent != pinned || res.ParentIs != "the base tip of run mt-earlier's batch" || res.PatchID == "" || !strings.HasPrefix(res.Run, "mt-rebind-") {
			t.Fatalf("result %+v", res)
		}
		if got := boundHead(t, f, id); got != moved {
			t.Fatalf("task bound to %s, want %s", got, moved)
		}
		entries, _ := f.train.Audit.Read()
		ri := slices.IndexFunc(entries, func(e Entry) bool {
			return e.Kind == "rebase" && e.Run == res.Run && e.Head == old && e.NewHead == moved && e.BaseSHA == pinned && strings.Contains(e.Detail, "recovered by the lead lead's rebind decision")
		})
		bi := slices.IndexFunc(entries, func(e Entry) bool {
			return e.Kind == "rebind" && e.Run == res.Run && e.Task == id && strings.HasPrefix(e.Detail, "patch-id: "+res.PatchID)
		})
		if ri < 0 || bi < ri {
			t.Fatalf("entries: %v", kinds(entries, 1))
		}
		// the next run lands it: the force push is the train's (trainHeads)
		f.realRun(&req)
		run, err := f.train.Run(context.Background(), req)
		if err != nil || run.PRs[0].Status != "merged" {
			t.Fatalf("the re-bound PR did not land: %v %+v", err, run.PRs)
		}
	})
	t.Run("onto the current tip", func(t *testing.T) {
		f, id, old, _ := strictBound(t)
		_, moved := moveBranch(f, "pr1", nil, true)
		res, err := Rebind(context.Background(), input(f, 1, old, moved))
		if err != nil || res.ParentIs != "the base's current tip" || boundHead(t, f, id) != moved {
			t.Fatalf("%v %+v", err, res)
		}
	})
	t.Run("a journaled base that is also the current tip is named as journaled", func(t *testing.T) {
		// Codex on #903 (8): the first match names the parent, so the
		// journaled base wins over the current tip when they are one commit
		f, id, old, _ := strictBound(t)
		if err := f.train.Audit.Append(Entry{Run: "mt-earlier", Kind: "batch", Repository: fixtureRepo, Base: "main", BaseSHA: f.head("main"), Pairs: []Pair{{Number: 1, Head: old}}}); err != nil {
			t.Fatal(err)
		}
		_, moved := moveBranch(f, "pr1", nil, true)
		res, err := Rebind(context.Background(), input(f, 1, old, moved))
		if err != nil || res.ParentIs != "the base tip of run mt-earlier's batch" || boundHead(t, f, id) != moved {
			t.Fatalf("%v %+v", err, res)
		}
	})
	other := strings.Repeat("a", 40)
	for _, r := range []struct {
		name    string
		prepare func(f *fixture, old string) (from, to string)
		code    string
		why     string
	}{
		{"not a full sha", func(f *fixture, old string) (string, string) {
			_, moved := moveBranch(f, "pr1", nil, true)
			return old[:12], moved
		}, RefusedRebindInvalid, ""},
		{"no task bound at the named head", func(f *fixture, old string) (string, string) {
			_, moved := moveBranch(f, "pr1", nil, true)
			return other, moved
		}, RefusedRebindUnbound, "0 tasks are bound to PR #1 at head aaaaaaaaaaaa"},
		{"the PR head is not the named head", func(f *fixture, old string) (string, string) {
			moveBranch(f, "pr1", nil, true)
			return old, other
		}, RefusedRebindHead, "PR #1's head is"},
		{"no force push from the bound head", func(f *fixture, old string) (string, string) {
			_, moved := moveBranch(f, "pr1", nil, false)
			return old, moved
		}, RefusedRebindUnproved, "no force push; the bound head"},
		{"two force pushes", func(f *fixture, old string) (string, string) {
			moveBranch(f, "pr1", nil, true)
			_, moved := moveBranch(f, "pr1", func() {
				mustGit(f.t, f.work, "commit", "-q", "--amend", "--no-edit", "--date", "2030-01-01T00:00:00Z")
			}, true)
			return old, moved
		}, RefusedRebindUnproved, "2 force pushes, one expected"},
		{"a parent that is not a base tip", func(f *fixture, old string) (string, string) {
			_, moved := moveBranch(f, "pr1", func() {
				writeFile(f.t, filepath.Join(f.work, "pkg/late.go"), "package pkg\n")
				mustGit(f.t, f.work, "add", "-A")
				mustGit(f.t, f.work, "commit", "-q", "-m", "late")
			}, true)
			return old, moved
		}, RefusedRebindUnproved, "first parent"},
		{"a differing patch-id", func(f *fixture, old string) (string, string) {
			_, moved := moveBranch(f, "pr1", func() {
				writeFile(f.t, filepath.Join(f.work, "pkg/a.go"), "package pkg\n\nfunc A() {}\n\nfunc Extra() {}\n")
				mustGit(f.t, f.work, "commit", "-q", "-a", "--amend", "--no-edit")
			}, true)
			return old, moved
		}, RefusedRebindUnproved, "the change moved during the update"},
	} {
		t.Run(r.name, func(t *testing.T) {
			f, id, old, _ := strictBound(t)
			from, to := r.prepare(f, old)
			before, _ := f.train.Audit.Read()
			_, err := Rebind(context.Background(), input(f, 1, from, to))
			var refusal *RebindRefusal
			if !errors.As(err, &refusal) || refusal.Code != r.code || !strings.Contains(refusal.Detail, r.why) {
				t.Fatalf("want %s with %q, got %v", r.code, r.why, err)
			}
			after, _ := f.train.Audit.Read()
			if boundHead(t, f, id) != old || len(after) != len(before) {
				t.Fatalf("something was written: bound %s, %d entries before, %d after", boundHead(t, f, id), len(before), len(after))
			}
		})
	}
}

// The plan reviewer on #903: a failed proof leaves no rebase entry, so
// trainHeads never names an unproven head and a following run's force-push
// rule refuses it as a push that is not the train's. Over the asynchronous
// fake (Codex on #903: the update lands between two reads, and the competing
// head lands with it): the differing patch-id
// (the fake alters the seat's hunk during the update) and the wrong parent
// (a push lands on the rebased head before the read-back) both end the first
// run refused with no rebase entry for the PR, and the next run's check
// refuses the head on force_push. Reverted (the entry written before the
// proof), the entry is there and the next run accepts the force push.
func TestFailedProofLeavesNoRebaseEntry(t *testing.T) {
	for name, prep := range map[string]struct {
		edit func(s *fakeState)
		rule string
	}{
		"a differing patch-id":              {func(s *fakeState) { s.AsyncUpdate, s.AlterAfterRebase = []string{"1"}, []string{"1"} }, RuleRebasePatch},
		"a parent that is not the base tip": {func(s *fakeState) { s.AsyncUpdate, s.PushAfterRebase = []string{"1"}, []string{"1"} }, RulePRHeadMoved},
	} {
		t.Run(name, func(t *testing.T) {
			f, id, old, req := strictBound(t)
			f.edit(prep.edit)
			first, err := f.train.Run(context.Background(), req)
			if err != nil || first.PRs[0].Status != "refused" || first.PRs[0].Refusals[0].Rule != prep.rule {
				t.Fatalf("the proof did not refuse: %v %+v", err, first.PRs)
			}
			moved := f.head("pr1")
			entries, _ := f.train.Audit.Read()
			if slices.ContainsFunc(entries, func(e Entry) bool { return e.Kind == "rebase" && e.PR == 1 }) || f.train.Audit.trainHeads(fixtureRepo, 1)[moved] {
				t.Fatalf("a rebase entry names the unproven head: %v", kinds(entries, 1))
			}
			if boundHead(t, f, id) != old {
				t.Fatalf("the unproven head was bound")
			}
			f.realRun(&req)
			second, err := f.train.Run(context.Background(), req)
			if err != nil || second.PRs[0].Status != "refused" || !slices.ContainsFunc(second.PRs[0].Refusals, func(r Refusal) bool { return r.Rule == RuleForcePush }) {
				t.Fatalf("the next run took the unproven head for the train's own push: %v %+v", err, second.PRs)
			}
			if strings.Contains(f.calls(), "-X PUT") {
				t.Fatal("something merged")
			}
		})
	}
}

// Codex on #903 (2): the re-bind's binding write holds only while the task
// is still bound at --from, under the store's lock. A train run that advanced
// the binding between the proof and the write (the Binder here stands in for
// it) keeps its head: the re-bind is refused rebind_head_mismatch and the
// advanced binding stays; the terminal rebind_superseded entry takes the
// head out of trainHeads and the next run refuses a push of it on
// force_push. The store's own check: BindPRFrom with the wrong head is
// ErrPRBindingMismatch and writes nothing.
func TestRebindRefusesWhenTheBindingMovedMeanwhile(t *testing.T) {
	f, id, old, _ := strictBound(t)
	_, moved := moveBranch(f, "pr1", nil, true)
	s := f.tasks()
	other := strings.Repeat("c", 40)
	in := RebindInput{Forge: f.gh, Audit: f.train.Audit, Tasks: s, Binder: advancingBinder{StoreBinder{Store: s}, s, id, other}, PR: 1, From: old, To: moved, Lead: "lead"}
	_, err := Rebind(context.Background(), in)
	var refusal *RebindRefusal
	if !errors.As(err, &refusal) || refusal.Code != RefusedRebindHead || !strings.Contains(refusal.Detail, "the binding moved while the re-bind was proved") {
		t.Fatalf("the stale re-bind was not refused: %v", err)
	}
	if got := boundHead(t, f, id); got != other {
		t.Fatalf("the advanced binding was overwritten: %s", got)
	}
	// the terminal entry (the plan reviewer on #903): the proof's rebase
	// entry is journaled, the superseded entry follows it, and the head is
	// not the train's; a following run's check refuses a push of it on
	// force_push
	entries, _ := f.train.Audit.Read()
	ri := slices.IndexFunc(entries, func(e Entry) bool { return e.Kind == "rebase" && e.PR == 1 && e.NewHead == moved })
	si := slices.IndexFunc(entries, func(e Entry) bool {
		return e.Kind == "rebind_superseded" && e.PR == 1 && e.Task == id && e.Head == old && e.NewHead == moved && strings.Contains(e.Detail, "the binding moved while the re-bind was proved")
	})
	if ri < 0 || si < ri || entries[si].Run != entries[ri].Run || f.train.Audit.trainHeads(fixtureRepo, 1)[moved] {
		t.Fatalf("the superseded head stays the train's: rebase %d, superseded %d, trainHeads %v", ri, si, f.train.Audit.trainHeads(fixtureRepo, 1))
	}
	req := Request{Schema: 1, Repository: fixtureRepo, Base: "main", PRs: []Item{{Number: 1}}}
	f.realRun(&req)
	res, err := f.train.Run(context.Background(), req)
	if err != nil || res.PRs[0].Status != "refused" || !slices.ContainsFunc(res.PRs[0].Refusals, func(r Refusal) bool { return r.Rule == RuleForcePush }) {
		t.Fatalf("a push of the superseded head passed as the train's own: %v %+v", err, res.PRs)
	}
	// the re-admission (the plan reviewer on #903): the lead's re-bind
	// applied brings the head back, as the train's own, through the
	// terminal rebind_readmitted entry; the next run lands the PR
	if _, err := s.BindPR(id, fixtureRepo, 1, old, time.Now()); err != nil { // the binding back where the proof reads it
		t.Fatal(err)
	}
	applied, err := Rebind(context.Background(), RebindInput{Forge: f.gh, Audit: f.train.Audit, Tasks: s, Binder: StoreBinder{Store: s}, PR: 1, From: old, To: moved, Lead: "lead"})
	if err != nil || boundHead(t, f, id) != moved || !f.train.Audit.trainHeads(fixtureRepo, 1)[moved] {
		t.Fatalf("the re-bind did not re-admit the head: %v (trainHeads %v)", err, f.train.Audit.trainHeads(fixtureRepo, 1))
	}
	entries, _ = f.train.Audit.Read()
	if !slices.ContainsFunc(entries, func(e Entry) bool {
		return e.Kind == "rebind_readmitted" && e.Run == applied.Run && e.PR == 1 && e.Task == id && e.Head == old && e.NewHead == moved
	}) {
		t.Fatalf("no readmission entry under the verb's run: %v", kinds(entries, 1))
	}
	f.realRun(&req)
	if res, err := f.train.Run(context.Background(), req); err != nil || res.PRs[0].Status != "merged" {
		t.Fatalf("the re-admitted head did not land: %v %+v", err, res.PRs)
	}
	if _, err := s.BindPRFrom(id, fixtureRepo, 1, old, moved, time.Now()); !errors.Is(err, task.ErrPRBindingMismatch) || boundHead(t, f, id) != moved {
		t.Fatalf("BindPRFrom with the wrong head: %v (bound %s)", err, boundHead(t, f, id))
	}
	if tk, _ := s.BindPRFrom(id, fixtureRepo, 1, moved, other, time.Now()); tk.PRHead != other {
		t.Fatalf("BindPRFrom with the right head: %+v", tk)
	}
}

// advancingBinder moves the task's binding to other right before the
// conditional write, as a concurrent train run would.
type advancingBinder struct {
	Binder
	store     task.Store
	id, other string
}

func (b advancingBinder) BindPRFrom(taskID, repository string, pr int, from, head string) error {
	if _, err := b.store.BindPR(b.id, repository, pr, b.other, time.Now()); err != nil {
		return err
	}
	return b.Binder.BindPRFrom(taskID, repository, pr, from, head)
}

// Codex on #903 (3): the wait's sleep ends at the context's end, and a
// cancelled wait is the unconfirmed refusal, nothing bound.
func TestCancelledUpdateWaitIsRefusedUnconfirmed(t *testing.T) {
	f, id, old, req := strictBound(t)
	f.edit(func(s *fakeState) { s.AsyncRejected = []string{"1"} })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	now := time.Now()
	sleeps := 0
	f.train.Now = func() time.Time { return now }
	f.train.Sleep = func(d time.Duration) { now = now.Add(d); sleeps++; cancel() } // the clock runs, so a wait that ignores the cancel ends at 120 s after many sleeps
	res, err := f.train.Run(ctx, req)
	if err != nil || res.PRs[0].Status != "refused" || res.PRs[0].Refusals[0].Rule != RuleUpdateUnconfirmed || sleeps != 1 {
		t.Fatalf("the cancelled wait: %v %+v (sleeps %d)", err, res.PRs, sleeps)
	}
	if boundHead(t, f, id) != old || strings.Contains(f.calls(), "-X PUT") {
		t.Fatal("something was bound or merged")
	}
}

// Codex on #903 (3): the bound is on the waiting, not a cutoff on the read.
// A read that starts before the deadline and completes after it (each PR
// read here takes 61 s of the test clock) sees the moved head and the train
// accepts it.
func TestPollCrossingTheDeadlineAcceptsAMovedHead(t *testing.T) {
	f, id, old, req := strictBound(t)
	f.edit(func(s *fakeState) { s.AsyncUpdate, s.AsyncViews = []string{"1"}, map[string]int{"1": 2} })
	now := time.Now()
	f.train.Now = func() time.Time { return now }
	f.train.Sleep = func(d time.Duration) { now = now.Add(d) }
	f.train.Forge = &slowForge{Forge: f.gh, tick: func() { now = now.Add(61 * time.Second) }}
	res, err := f.train.Run(context.Background(), req)
	if err != nil || res.PRs[0].Status != "merged" || res.PRs[0].Head == old || boundHead(t, f, id) != res.PRs[0].Head {
		t.Fatalf("the head read past the deadline was not accepted: %v %+v", err, res.PRs)
	}
}

// slowForge advances a test clock on every PR read.
type slowForge struct {
	Forge
	tick func()
}

func (s *slowForge) PR(ctx context.Context, repository string, number int) (PRInfo, error) {
	s.tick()
	return s.Forge.PR(ctx, repository, number)
}

// Codex on #903 (4), the plan reviewer's split: a request GitHub definitely
// refused (here a GraphQL error naming the refusal) is followed by an
// update_failed entry and counts for nothing: a seat's own push after it,
// even with the same patch on the base tip, is not recovered, and the next
// run refuses it as a force push that is not the train's. (A seat's push
// with a changed patch after a live request is R3's "a differing patch-id"
// subtest: refused on the patch-id, nothing bound.)
func TestFailedMutationLeavesNoLiveRequest(t *testing.T) {
	f, id, old, req := strictBound(t)
	f.train.Forge = &failRebaseForge{Forge: f.gh}
	first, err := f.train.Run(context.Background(), req)
	if err == nil || !strings.Contains(first.Stopped, "rebase: gh api graphql: exit status 1: GraphQL: Head branch was modified") {
		t.Fatalf("the failed mutation did not stop the run: %v %q", err, first.Stopped)
	}
	entries, _ := f.train.Audit.Read()
	ri := slices.IndexFunc(entries, func(e Entry) bool { return e.Kind == "update_requested" && e.PR == 1 && e.Head == old })
	fi := slices.IndexFunc(entries, func(e Entry) bool {
		return e.Kind == "update_failed" && e.PR == 1 && e.Head == old && strings.Contains(e.Detail, "Head branch was modified")
	})
	if ri < 0 || fi < ri {
		t.Fatalf("no update_failed after the request: %v", kinds(entries, 1))
	}
	if _, _, ok := lastUpdateRequest(entries, fixtureRepo, 1, old); ok {
		t.Fatal("the failed request is still live")
	}
	f.train.Forge = f.gh
	moveBranch(f, "pr1", nil, true) // the seat's own push: the same patch, on the base tip
	f.realRun(&req)
	second, err := f.train.Run(context.Background(), req)
	if err != nil || second.PRs[0].Status != "refused" || !slices.ContainsFunc(second.PRs[0].Refusals, func(r Refusal) bool { return r.Rule == RuleForcePush }) {
		t.Fatalf("the seat's push after the failed request was recovered: %v %+v", err, second.PRs)
	}
	entries, _ = f.train.Audit.Read()
	if slices.ContainsFunc(entries, func(e Entry) bool {
		return e.Run == second.Run && (e.Kind == "rebase" || e.Kind == "rebind" || e.Kind == "reconcile")
	}) || boundHead(t, f, id) != old {
		t.Fatalf("a recovery was attempted: %v (bound %s)", kinds(entries, 1), boundHead(t, f, id))
	}
}

type failRebaseForge struct{ Forge }

func (failRebaseForge) Rebase(context.Context, string, int, string) (string, error) {
	return "", errors.New("gh api graphql: exit status 1: GraphQL: Head branch was modified. Review and try the update again. (updatePullRequestBranch)")
}

// Codex on #903 (6): the force push's time is placed after the journaled
// request at second precision, the timeline's: the same second is after,
// the second before is not, and a sub-second request inside the push's
// second is before it.
func TestPushedAfterTakesTheSameSecondAsAfter(t *testing.T) {
	for _, c := range []struct {
		at, since string
		want      bool
	}{
		{"2026-11-05T19:23:26Z", "2026-11-05T19:23:20.853766Z", true},
		{"2026-11-05T19:23:20Z", "2026-11-05T19:23:20.853766Z", true},
		{"2026-11-05T19:23:19Z", "2026-11-05T19:23:20.853766Z", false},
		{"2026-11-05T19:23:20Z", "2026-11-05T19:23:21.000001Z", false},
		{"", "2026-11-05T19:23:20Z", false},
		{"2026-11-05T19:23:20Z", "not a time", false},
	} {
		if got := pushedAfter(c.at, c.since); got != c.want {
			t.Errorf("pushedAfter(%q, %q) = %v, want %v", c.at, c.since, got, c.want)
		}
	}
}

// ambiguousForge fails the mutation on this side (a transport error) after
// optionally letting the fake perform it (GitHub completed the update, the
// answer was lost).
type ambiguousForge struct {
	Forge
	perform bool
	text    string // the error; empty: a transport error
}

func (a *ambiguousForge) Rebase(ctx context.Context, repository string, number int, expect string) (string, error) {
	if a.perform {
		if _, err := a.Forge.Rebase(ctx, repository, number, expect); err != nil {
			return "", err
		}
	}
	if a.text != "" {
		return "", errors.New(a.text)
	}
	return "", errors.New(`Post "https://api.github.com/graphql": context deadline exceeded`)
}

// The plan reviewer's split on #903 (4), the ambiguous class: a mutation
// that fails on this side without a definite refusal (a timeout, a 5xx, a
// dropped connection) may have been completed by GitHub. The request stays
// live, stamped update_ambiguous, and the train reads the head back at once:
// an update that did land is proved, bound and merged in the same run; one
// that did not ends the bounded wait unconfirmed. At the next run the
// recovery accepts only a force push inside the request's window (its time
// to 120 s plus the 16 s last backoff, the same second inclusive): a push
// inside it is recovered on the proof, a push after it is refused and the
// reconcile entry says so.
func TestAmbiguousMutationKeepsTheRequestLiveInsideItsWindow(t *testing.T) {
	t.Run("the update landed and the answer was lost", func(t *testing.T) {
		f, id, old, req := strictBound(t)
		f.edit(func(s *fakeState) { s.AsyncUpdate = []string{"1"} })
		f.train.Forge = &ambiguousForge{Forge: f.gh, perform: true}
		res, err := f.train.Run(context.Background(), req)
		if err != nil || res.PRs[0].Status != "merged" || res.PRs[0].Head == old || boundHead(t, f, id) != res.PRs[0].Head {
			t.Fatalf("the lost answer's update was not read back and merged: %v %+v", err, res.PRs)
		}
		entries, _ := f.train.Audit.Read()
		ai := slices.IndexFunc(entries, func(e Entry) bool {
			return e.Kind == "update_ambiguous" && e.PR == 1 && e.Head == old && strings.Contains(e.Detail, "context deadline exceeded")
		})
		ri := slices.IndexFunc(entries, func(e Entry) bool { return e.Kind == "rebase" && e.PR == 1 })
		if ai < 0 || ri < ai {
			t.Fatalf("no update_ambiguous before the rebase entry: %v", kinds(entries, 1))
		}
	})
	// the update never ran: the first run ends unconfirmed with the request
	// live and ambiguous; then a push by hand at an offset from the request
	ambiguousRun := func(t *testing.T) (*fixture, string, string, Request, string) {
		t.Helper()
		f, id, old, req := strictBound(t)
		f.train.Forge = &ambiguousForge{Forge: f.gh}
		now := time.Now()
		f.train.Now = func() time.Time { return now }
		f.train.Sleep = func(d time.Duration) { now = now.Add(d) }
		first, err := f.train.Run(context.Background(), req)
		if err != nil || first.PRs[0].Status != "refused" || first.PRs[0].Refusals[0].Rule != RuleUpdateUnconfirmed {
			t.Fatalf("the ambiguous failure did not end in the bounded wait: %v %+v", err, first.PRs)
		}
		entries, _ := f.train.Audit.Read()
		requested, ambiguous, ok := lastUpdateRequest(entries, fixtureRepo, 1, old)
		if !ok || !strings.Contains(ambiguous, "context deadline exceeded") {
			t.Fatalf("the request is not live and ambiguous: %v %q", ok, ambiguous)
		}
		f.train.Forge, f.train.Now, f.train.Sleep = f.gh, nil, nil
		return f, id, old, req, requested.At
	}
	pushAt := func(f *fixture, requestAt string, offset time.Duration) {
		at, err := time.Parse(time.RFC3339Nano, requestAt)
		if err != nil {
			f.t.Fatal(err)
		}
		moveBranch(f, "pr1", nil, true)
		f.edit(func(s *fakeState) {
			s.PRs["1"].ForcePushes[0].At = at.Add(offset).UTC().Format(time.RFC3339)
		})
	}
	t.Run("a push inside the window is recovered", func(t *testing.T) {
		f, id, old, req, requestAt := ambiguousRun(t)
		pushAt(f, requestAt, 100*time.Second)
		f.realRun(&req)
		res, err := f.train.Run(context.Background(), req)
		if err != nil || res.PRs[0].Status != "merged" || res.PRs[0].Head == old || boundHead(t, f, id) != res.PRs[0].Head {
			t.Fatalf("the push inside the window was not recovered: %v %+v", err, res.PRs)
		}
		entries, _ := f.train.Audit.Read()
		if !slices.ContainsFunc(entries, func(e Entry) bool {
			return e.Kind == "rebase" && e.Run == res.Run && strings.Contains(e.Detail, "inside the request's window") && strings.Contains(e.Detail, "had failed without a definite refusal")
		}) {
			t.Fatalf("the recovery's record: %v", kinds(entries, 1))
		}
	})
	t.Run("an unknown GraphQL wording is ambiguous and the read-back runs", func(t *testing.T) {
		// Codex's re-pass on #903 (1): definite only on an explicit refusal
		f, id, old, req := strictBound(t)
		f.edit(func(s *fakeState) { s.AsyncUpdate = []string{"1"} })
		f.train.Forge = &ambiguousForge{Forge: f.gh, perform: true, text: "gh api graphql: exit status 1: GraphQL: Internal server error (updatePullRequestBranch)"}
		res, err := f.train.Run(context.Background(), req)
		if err != nil || res.PRs[0].Status != "merged" || boundHead(t, f, id) != res.PRs[0].Head {
			t.Fatalf("the unknown wording retired the request: %v %+v", err, res.PRs)
		}
		entries, _ := f.train.Audit.Read()
		if !slices.ContainsFunc(entries, func(e Entry) bool { return e.Kind == "update_ambiguous" && e.PR == 1 && e.Head == old }) ||
			slices.ContainsFunc(entries, func(e Entry) bool { return e.Kind == "update_failed" }) {
			t.Fatalf("the journal: %v", kinds(entries, 1))
		}
	})
	// the window's endpoints (Codex's re-pass on #903 (4)): the request's
	// own second and the last second of the window are inside; one second
	// outside each end is not
	for name, c := range map[string]struct {
		offset    time.Duration
		recovered bool
		why       string
	}{
		"the request's own second":      {0, true, ""},
		"one second before the request": {-time.Second, false, "no force push in the journaled request's window"},
		"the window's last second":      {DefaultUpdateWait + UpdateWindowSlack, true, ""},
		"one second past the window":    {DefaultUpdateWait + UpdateWindowSlack + time.Second, false, "1 after the window, which ended at"},
	} {
		t.Run(name, func(t *testing.T) {
			f, id, old, req, requestAt := ambiguousRun(t)
			pushAt(f, requestAt, c.offset)
			f.realRun(&req)
			res, err := f.train.Run(context.Background(), req)
			if err != nil {
				t.Fatal(err)
			}
			entries, _ := f.train.Audit.Read()
			if c.recovered {
				if res.PRs[0].Status != "merged" || boundHead(t, f, id) == old {
					t.Fatalf("not recovered: %+v (bound %s)", res.PRs, boundHead(t, f, id))
				}
				return
			}
			rec := slices.IndexFunc(entries, func(e Entry) bool { return e.Kind == "reconcile" && e.Run == res.Run && e.PR == 1 })
			if res.PRs[0].Status != "refused" || boundHead(t, f, id) != old || rec < 0 || !strings.Contains(entries[rec].Detail, c.why) {
				t.Fatalf("recovered outside the window: %+v (bound %s, reconcile %d)", res.PRs, boundHead(t, f, id), rec)
			}
		})
	}
	t.Run("a push after the window is refused", func(t *testing.T) {
		f, id, old, req, requestAt := ambiguousRun(t)
		pushAt(f, requestAt, 200*time.Second) // past 120 s + 16 s
		f.realRun(&req)
		res, err := f.train.Run(context.Background(), req)
		if err != nil || res.PRs[0].Status != "refused" || boundHead(t, f, id) != old ||
			!slices.ContainsFunc(res.PRs[0].Refusals, func(r Refusal) bool { return r.Rule == RuleForcePush }) {
			t.Fatalf("the push after the window was recovered: %v %+v (bound %s)", err, res.PRs, boundHead(t, f, id))
		}
		entries, _ := f.train.Audit.Read()
		rec := slices.IndexFunc(entries, func(e Entry) bool { return e.Kind == "reconcile" && e.Run == res.Run && e.PR == 1 })
		if rec < 0 || !strings.Contains(entries[rec].Detail, "1 after the window, which ended at") || !strings.HasSuffix(entries[rec].Detail, "binding not recovered") {
			t.Fatalf("the refusal's record does not name the window: %d %v", rec, entries)
		}
	})
}

// The classes of a failed mutation as gh reports them (the plan reviewer on
// #903): a 4xx or a GraphQL refusal is definite; a 5xx, a transport error,
// a context deadline or a GraphQL "something went wrong" is ambiguous.
func TestDefiniteRefusalClassesTheGhError(t *testing.T) {
	for text, want := range map[string]bool{
		"gh api graphql: exit status 1: GraphQL: Head branch was modified. Review and try the update again. (updatePullRequestBranch)":                                            true,
		"gh api graphql: exit status 1: GraphQL: Pull request is not mergeable (updatePullRequestBranch)":                                                                         false, // no evidenced exact wording: ambiguous
		"gh api graphql: exit status 1: GraphQL: Resource not accessible by integration (updatePullRequestBranch)":                                                                true,
		"gh api graphql: exit status 1: gh: Validation Failed (HTTP 422)":                                                                                                         true,
		"gh api graphql: exit status 1: gh: Forbidden (HTTP 403)":                                                                                                                 true,
		"gh api graphql: exit status 1: GraphQL: Head branch was modified. Review and try the update again. (updatePullRequestBranch) [fake: expected head abc, the head is def]": true,
		"gh api graphql: exit status 1: GraphQL: Forbidden (updatePullRequestBranch)":                                                                                             false, // no evidenced exact wording; the HTTP 403 form is the 4xx rule
		"gh api graphql: exit status 1: gh: Not Found (HTTP 404)":                                                                                                                 true,
		"gh api graphql: exit status 1: gh: Bad Gateway (HTTP 502)":                                                                                                               false,
		"gh api graphql: exit status 1: gh: Internal Server Error (HTTP 500)":                                                                                                     false,
		"gh api graphql: exit status 1: GraphQL: Something went wrong while executing your query. Please include `ABCD` when reporting this issue.":                               false,
		"gh api graphql: exit status 1: GraphQL: Internal server error (updatePullRequestBranch)":                                                                                 false,
		"gh api graphql: exit status 1: GraphQL: a wording this code has never seen (updatePullRequestBranch)":                                                                    false,
		"gh api graphql: exit status 1: GraphQL: Internal server error while checking expected head (updatePullRequestBranch)":                                                    false, // a fragment inside another sentence
		"gh api graphql: exit status 1: GraphQL: Internal server error: Head branch was modified. Review and try the update again. (updatePullRequestBranch)":                     false, // the message inside another sentence
		"gh api graphql: exit status 1: GraphQL: Something went wrong, resource not accessible by integration (updatePullRequestBranch)":                                          false,
		"gh api graphql: exit status 1: GraphQL: Something is not mergeable by this rule (updatePullRequestBranch)":                                                               false,
		"gh api graphql: exit status 1: GraphQL: head branch was modified. review and try the update again.":                                                                      true,  // the message itself, case aside
		"gh api graphql: exit status 1: GraphQL: Head branch was modified":                                                                                                        false, // a prefix of the message is not the message
		"gh api graphql: exit status 1: GraphQL: The request timed out (updatePullRequestBranch)":                                                                                 false,
		`gh api graphql: exit status 1: Post "https://api.github.com/graphql": context deadline exceeded`:                                                                         false,
		"gh api graphql: exit status 1: error connecting to api.github.com: read: connection reset by peer":                                                                       false,
		"gh api graphql: signal: killed: ": false,
	} {
		if got := definiteRefusal(errors.New(text)); got != want {
			t.Errorf("definiteRefusal(%q) = %v, want %v", text, got, want)
		}
	}
	if end := requestWindowEnd("2026-11-05T19:23:20.853766Z", 120*time.Second); end != "2026-11-05T19:25:36.853766Z" {
		t.Errorf("the window's end: %s", end)
	}
	if requestWindowEnd("not a time", time.Minute) != "" {
		t.Error("an unreadable request time has a window")
	}
}

// Codex's re-pass on #903 (2): the superseded exclusion is terminal whatever
// the order: rebase(H), rebind_superseded(H), rebase(H) leaves H out of
// trainHeads (overlapping proof attempts can journal it so).
func TestSupersededHeadStaysOutWhateverTheOrder(t *testing.T) {
	a := AuditFor(t.TempDir())
	h, other := strings.Repeat("d", 40), strings.Repeat("e", 40)
	for _, e := range []Entry{
		{Kind: "rebase", Repository: fixtureRepo, PR: 1, Head: "x", NewHead: h},
		{Kind: "rebind_superseded", Repository: fixtureRepo, PR: 1, Head: "x", NewHead: h},
		{Kind: "rebase", Repository: fixtureRepo, PR: 1, Head: "x", NewHead: h},
		{Kind: "rebase", Repository: fixtureRepo, PR: 1, Head: "x", NewHead: other},
		{Kind: "rebind_superseded", Repository: fixtureRepo, PR: 2, Head: "x", NewHead: other}, // another PR's
	} {
		if err := a.Append(e); err != nil {
			t.Fatal(err)
		}
	}
	got := a.trainHeads(fixtureRepo, 1)
	if got[h] || !got[other] {
		t.Fatalf("trainHeads %v", got)
	}
	// the latest terminal entry decides: the verb's re-admission brings H
	// back, a later superseded entry takes it out again
	if err := a.Append(Entry{Kind: "rebind_readmitted", Repository: fixtureRepo, PR: 1, Head: "x", NewHead: h}); err != nil {
		t.Fatal(err)
	}
	if got := a.trainHeads(fixtureRepo, 1); !got[h] {
		t.Fatalf("the re-admitted head is out: %v", got)
	}
	if err := a.Append(Entry{Kind: "rebind_superseded", Repository: fixtureRepo, PR: 1, Head: "x", NewHead: h}); err != nil {
		t.Fatal(err)
	}
	if got := a.trainHeads(fixtureRepo, 1); got[h] {
		t.Fatalf("the superseded-again head is in: %v", got)
	}
	// "latest" is the journal's order, never the timestamps (the plan
	// reviewer on #903): a readmitted entry stamped a day earlier but
	// appended later wins, and a superseded entry stamped later but
	// appended earlier does not
	earlier := time.Now().Add(-24 * time.Hour).UTC().Format(time.RFC3339Nano)
	if err := a.Append(Entry{At: earlier, Kind: "rebind_readmitted", Repository: fixtureRepo, PR: 1, Head: "x", NewHead: h}); err != nil {
		t.Fatal(err)
	}
	if got := a.trainHeads(fixtureRepo, 1); !got[h] {
		t.Fatalf("the later-appended, earlier-stamped readmission lost: %v", got)
	}
	later := time.Now().Add(24 * time.Hour).UTC().Format(time.RFC3339Nano)
	if err := a.Append(Entry{At: later, Kind: "rebind_superseded", Repository: fixtureRepo, PR: 1, Head: "x", NewHead: h}); err != nil {
		t.Fatal(err)
	}
	if err := a.Append(Entry{At: earlier, Kind: "rebind_readmitted", Repository: fixtureRepo, PR: 1, Head: "x", NewHead: h}); err != nil {
		t.Fatal(err)
	}
	if got := a.trainHeads(fixtureRepo, 1); !got[h] {
		t.Fatalf("the timestamps reordered the terminal entries: %v", got)
	}
}

// The plan reviewer on #903: only the lead's merge-train rebind verb
// writes rebind_readmitted; no train path does. A train run that meets a
// superseded head (a lead re-bind attempt on it refused on the conditional
// write) never recovers it even on a journaled request and the proof: the
// binding stays, no rebind or readmitted entry is written, the head stays
// out of trainHeads, the reconcile entry names the lead's verb, and the
// check refuses the head on force_push. The verb then re-admits it and the
// next run lands the PR. Reverted (the recovery proceeding on a superseded
// head), the binding moves and the row fails.
func TestTrainNeverReadmitsASupersededHead(t *testing.T) {
	f, id, old, req := strictBound(t)
	if err := f.train.Audit.Append(Entry{Run: "mt-earlier", Kind: "update_requested", Repository: fixtureRepo, Base: "main", PR: 1, Task: id, Head: old, BaseSHA: f.head("main")}); err != nil {
		t.Fatal(err)
	}
	_, moved := moveBranch(f, "pr1", nil, true)
	// the refused re-bind's journal, as Rebind writes it: the rebase and
	// rebind entries (audit first), then the terminal superseded entry; the
	// rebind entry is exactly what reconcileRebinds would replay (Codex's
	// third pass on #903)
	for _, e := range []Entry{
		{Kind: "rebase", Head: old, NewHead: moved, BaseSHA: f.head("main"), Detail: "recovered by the lead lead's rebind decision"},
		{Kind: "rebind", Head: old, NewHead: moved, Detail: "patch-id: abc\nfirst parent: " + f.head("main")},
		{Kind: "rebind_superseded", Head: old, NewHead: moved, Detail: "a stale re-bind"},
	} {
		e.Run, e.Repository, e.Base, e.PR, e.Task = "mt-rebind-earlier", fixtureRepo, "main", 1, id
		if err := f.train.Audit.Append(e); err != nil {
			t.Fatal(err)
		}
	}
	res, err := f.train.Run(context.Background(), req)
	if err != nil || res.PRs[0].Status != "refused" || !slices.ContainsFunc(res.PRs[0].Refusals, func(r Refusal) bool { return r.Rule == RuleForcePush }) || boundHead(t, f, id) != old {
		t.Fatalf("the superseded head was recovered: %v %+v (bound %s)", err, res.PRs, boundHead(t, f, id))
	}
	entries, _ := f.train.Audit.Read()
	if slices.ContainsFunc(entries, func(e Entry) bool {
		return e.Kind == "rebind_readmitted" || (e.Run == res.Run && (e.Kind == "rebind" || e.Kind == "rebase"))
	}) || f.train.Audit.trainHeads(fixtureRepo, 1)[moved] {
		t.Fatalf("the train wrote a re-admission or a rebind: %v (trainHeads %v)", kinds(entries, 1), f.train.Audit.trainHeads(fixtureRepo, 1))
	}
	replay, recovery := false, false
	for _, e := range entries {
		if e.Kind == "reconcile" && e.Run == res.Run && strings.Contains(e.Detail, "only merge-train rebind re-admits it") {
			replay = replay || strings.Contains(e.Detail, "does not replay it")
			recovery = recovery || strings.Contains(e.Detail, "does not recover it")
		}
	}
	if !replay || !recovery {
		t.Fatalf("the reconcile entries do not name the verb for the replay (%v) and the recovery (%v): %v", replay, recovery, kinds(entries, 1))
	}
	s := f.tasks()
	if _, err := Rebind(context.Background(), RebindInput{Forge: f.gh, Audit: f.train.Audit, Tasks: s, Binder: StoreBinder{Store: s}, PR: 1, From: old, To: moved, Lead: "lead"}); err != nil {
		t.Fatalf("the verb did not re-admit: %v", err)
	}
	f.realRun(&req)
	if res, err := f.train.Run(context.Background(), req); err != nil || res.PRs[0].Status != "merged" {
		t.Fatalf("the re-admitted head did not land: %v %+v", err, res.PRs)
	}
}

// The guard from the source: among the package's non-test files only
// rebind.go (the lead's verb) writes a rebind_readmitted entry; a train path
// that starts writing one fails here by file.
func TestOnlyTheRebindVerbWritesReadmitted(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") || name == "rebind.go" {
			continue
		}
		raw, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(raw), `Kind: "rebind_readmitted"`) {
			t.Errorf("%s writes a rebind_readmitted entry; only the lead's verb (rebind.go) may", name)
		}
	}
	raw, _ := os.ReadFile("rebind.go")
	if !strings.Contains(string(raw), `Kind: "rebind_readmitted"`) {
		t.Fatal("rebind.go no longer writes the re-admission")
	}
}

// blockingForge's PR read blocks until its context ends.
type blockingForge struct{ Forge }

func (b blockingForge) PR(ctx context.Context, repository string, number int) (PRInfo, error) {
	<-ctx.Done()
	return PRInfo{}, ctx.Err()
}

// Codex's re-pass on #903 (3): each read of the bounded wait runs under the
// remaining deadline, so a stalled gh returns within it: a Forge whose PR
// read blocks until its context ends, under a 300 ms wait on the real
// clock, ends the run refused unconfirmed well inside a second, nothing
// bound. The CLI runs the train under RunBound.
func TestStalledReadReturnsWithinTheBoundedWait(t *testing.T) {
	f, id, old, req := strictBound(t)
	forge := &updateOnlyBlocking{Forge: f.gh}
	f.train.Forge = forge
	f.train.UpdateWait = 300 * time.Millisecond
	res, err := f.train.Run(context.Background(), req)
	elapsed := time.Since(forge.mutatedAt) // from the mutation: the plan phase runs git under the suite's load
	if err != nil || res.PRs[0].Status != "refused" || res.PRs[0].Refusals[0].Rule != RuleUpdateUnconfirmed || boundHead(t, f, id) != old {
		t.Fatalf("the stalled read: %v %+v", err, res.PRs)
	}
	if forge.mutatedAt.IsZero() || elapsed > 3*time.Second {
		t.Fatalf("the wait took %s after the mutation, past a 300 ms wait", elapsed)
	}
	if b := RunBound(3); b != 3*(DefaultUpdateWait+DefaultCIWait+10*time.Minute) || RunBound(0) != RunBound(1) {
		t.Fatalf("RunBound %s", b)
	}
}

// updateOnlyBlocking blocks the PR reads that follow the mutation (the
// read-back), and answers every other read from the fake.
type updateOnlyBlocking struct {
	Forge
	mutated   bool
	mutatedAt time.Time
}

func (u *updateOnlyBlocking) Rebase(ctx context.Context, repository string, number int, expect string) (string, error) {
	out, err := u.Forge.Rebase(ctx, repository, number, expect) // the fake's git work, under the suite's load, is before the clock starts
	u.mutated, u.mutatedAt = true, time.Now()
	return out, err
}

func (u *updateOnlyBlocking) PR(ctx context.Context, repository string, number int) (PRInfo, error) {
	if u.mutated {
		<-ctx.Done()
		return PRInfo{}, ctx.Err()
	}
	return u.Forge.PR(ctx, repository, number)
}

// Codex's re-pass on #903 (4): the cancellation through the production
// timer path (no Sleep seam): the context is cancelled 100 ms into the
// first real 1 s backoff, the wait ends there, refused unconfirmed with
// nothing bound, the run well inside the 3 s wait. Reverted (the select
// waits the timer out), the run lasts the whole wait.
func TestCancellationEndsTheProductionSleep(t *testing.T) {
	f, id, old, req := strictBound(t)
	f.edit(func(s *fakeState) { s.AsyncRejected = []string{"1"} })
	f.train.UpdateWait = 3 * time.Second
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	forge := &cancelAfterMutation{Forge: f.gh, cancel: cancel}
	f.train.Forge = forge
	res, err := f.train.Run(ctx, req)
	elapsed := time.Since(forge.mutatedAt)
	if err != nil || res.PRs[0].Status != "refused" || res.PRs[0].Refusals[0].Rule != RuleUpdateUnconfirmed || boundHead(t, f, id) != old {
		t.Fatalf("the cancelled wait: %v %+v", err, res.PRs)
	}
	if forge.mutatedAt.IsZero() || elapsed > 900*time.Millisecond {
		t.Fatalf("the cancel did not end the sleep: %s after the mutation", elapsed)
	}
}

// cancelAfterMutation cancels the run's context 150 ms after the mutation:
// inside the first real 1 s backoff of the read-back.
type cancelAfterMutation struct {
	Forge
	cancel    context.CancelFunc
	mutatedAt time.Time
}

func (c *cancelAfterMutation) Rebase(ctx context.Context, repository string, number int, expect string) (string, error) {
	out, err := c.Forge.Rebase(ctx, repository, number, expect)
	c.mutatedAt = time.Now()
	go func() { time.Sleep(150 * time.Millisecond); c.cancel() }()
	return out, err
}

// Codex's third pass on #903 (3): a real gh killed at the read's deadline
// returns an exit error, not a context error; the read's own deadline
// decides that the poll is unmoved. The read-back goes through the real
// exec path (GH.exec, CommandContext) to a gh stand-in that sleeps past
// the 300 ms wait: the run ends refused unconfirmed within seconds,
// nothing bound. Reverted (the error type alone), the run stops with the
// kill's error.
func TestStalledGhReadIsUnmovedForThePoll(t *testing.T) {
	f, id, old, req := strictBound(t)
	script := filepath.Join(f.dir, "gh-stalled")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nexec sleep 30\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	forge := &readBackVia{Forge: f.gh, slow: &GH{Bin: script}}
	f.train.Forge = forge
	f.train.UpdateWait = 300 * time.Millisecond
	res, err := f.train.Run(context.Background(), req)
	elapsed := time.Since(forge.mutatedAt) // from the mutation: the plan phase runs git under the suite's load
	if err != nil || res.PRs[0].Status != "refused" || res.PRs[0].Refusals[0].Rule != RuleUpdateUnconfirmed || boundHead(t, f, id) != old {
		t.Fatalf("the killed gh read: %v %+v", err, res.PRs)
	}
	if forge.mutatedAt.IsZero() || elapsed > 5*time.Second {
		t.Fatalf("the wait took %s after the mutation, past a 300 ms wait", elapsed)
	}
}

// readBackVia answers the PR reads after the mutation through slow (a real
// GH over its exec path), everything else from the fake.
type readBackVia struct {
	Forge
	slow      Forge
	mutated   bool
	mutatedAt time.Time
}

func (r *readBackVia) Rebase(ctx context.Context, repository string, number int, expect string) (string, error) {
	out, err := r.Forge.Rebase(ctx, repository, number, expect) // the fake's git work, under the suite's load, is before the clock starts
	r.mutated, r.mutatedAt = true, time.Now()
	return out, err
}

func (r *readBackVia) PR(ctx context.Context, repository string, number int) (PRInfo, error) {
	if r.mutated {
		return r.slow.PR(ctx, repository, number)
	}
	return r.Forge.PR(ctx, repository, number)
}
