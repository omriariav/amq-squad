package mergetrain

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"
)

// The one admission check (the plan reviewer's rulings on Codex's third
// pass on #903): headAdmitted decides whether a head may be trusted or
// bound, by the latest terminal entry for it in the journal's append
// order. One row per caller: reconcileRebinds (Codex's sequence, journaled
// by the real refused re-bind), reconcileUpdates, land's binding write and
// Rebind; trainHeads is TestSupersededHeadStaysOutWhateverTheOrder, and the
// source guard is TestOnlyTheRebindVerbWritesReadmitted.

// refusedRebind runs the lead's re-bind old -> moved through a Binder that
// advances the binding right before the conditional write (a concurrent
// train run's stand-in), so the real operation is refused and journals
// rebase(old -> moved), rebind(old -> moved), rebind_superseded(moved).
// The binding is then put back at old, as the seat's commit step would.
func refusedRebind(t *testing.T, f *fixture, id, old, moved string) {
	t.Helper()
	s := f.tasks()
	before, _ := f.train.Audit.Read()
	in := RebindInput{Forge: f.gh, Audit: f.train.Audit, Tasks: s, Binder: advancingBinder{StoreBinder{Store: s}, s, id, strings.Repeat("c", 40)}, PR: 1, From: old, To: moved, Lead: "lead"}
	_, err := Rebind(context.Background(), in)
	var refusal *RebindRefusal
	if !errors.As(err, &refusal) || refusal.Code != RefusedRebindHead {
		t.Fatalf("the re-bind was not refused on the moved binding: %v", err)
	}
	after, _ := f.train.Audit.Read()
	var got []string
	run := ""
	for _, e := range after[len(before):] {
		if e.PR == 1 && e.Head == old && e.NewHead == moved && (run == "" || e.Run == run) {
			run = e.Run
			got = append(got, e.Kind)
		}
	}
	if !slices.Equal(got, []string{"rebase", "rebind", "rebind_superseded"}) {
		t.Fatalf("the refused re-bind journaled %v, want rebase, rebind, rebind_superseded under one run", got)
	}
	if _, err := s.BindPR(id, fixtureRepo, 1, old, time.Now()); err != nil {
		t.Fatal(err)
	}
}

// Codex's sequence (reconcileRebinds): rebase(old -> H), rebind(old -> H),
// rebind_superseded(H), as the real refused re-bind journals them, with the
// task bound at old and the PR's head H. The replay finds the rebind entry
// for the bound head with the server at H, and the admission check leaves
// H unbound: the binding stays at old, a reconcile entry names the verb,
// nothing is rebound under the run, and the check refuses H on force_push.
// Reverted (the replay's check skipped), the replay binds H.
func TestRefusedRebindJournalLeavesTheHeadUnbound(t *testing.T) {
	f, id, old, req := strictBound(t)
	_, moved := moveBranch(f, "pr1", nil, true)
	refusedRebind(t, f, id, old, moved)
	f.realRun(&req)
	res, err := f.train.Run(context.Background(), req)
	if err != nil || boundHead(t, f, id) != old || res.PRs[0].Status != "refused" ||
		!slices.ContainsFunc(res.PRs[0].Refusals, func(r Refusal) bool { return r.Rule == RuleForcePush }) {
		t.Fatalf("the superseded head was bound: %v %+v (bound %s)", err, res.PRs, boundHead(t, f, id))
	}
	entries, _ := f.train.Audit.Read()
	if slices.ContainsFunc(entries, func(e Entry) bool { return e.Run == res.Run && (e.Kind == "rebind" || e.Kind == "rebase") }) {
		t.Fatalf("the run rebound: %v", kinds(entries, 1))
	}
	if !slices.ContainsFunc(entries, func(e Entry) bool {
		return e.Kind == "reconcile" && e.Run == res.Run && e.NewHead == moved && strings.Contains(e.Detail, "does not replay it, only merge-train rebind re-admits it")
	}) {
		t.Fatalf("no reconcile entry names the verb: %v", kinds(entries, 1))
	}
}

// reconcileUpdates: a journaled request for the bound head, one force push
// from it to H on the base tip with the patch unchanged (the recovery's
// proof holds), and a rebind_superseded(H) with no rebind entry (so the
// replay has nothing to apply): the recovery leaves H unbound, with a
// reconcile entry naming the verb. Reverted (the recovery's check
// skipped), the recovery binds H.
func TestSupersededHeadIsNotRecoveredFromAJournaledRequest(t *testing.T) {
	f, id, old, req := strictBound(t)
	if err := f.train.Audit.Append(Entry{Run: "mt-earlier", Kind: "update_requested", Repository: fixtureRepo, Base: "main", PR: 1, Task: id, Head: old, BaseSHA: f.head("main")}); err != nil {
		t.Fatal(err)
	}
	_, moved := moveBranch(f, "pr1", nil, true)
	if err := f.train.Audit.Append(Entry{Run: "mt-rebind-earlier", Kind: "rebind_superseded", Repository: fixtureRepo, Base: "main", PR: 1, Task: id, Head: old, NewHead: moved, Detail: "a stale re-bind"}); err != nil {
		t.Fatal(err)
	}
	res, err := f.train.Run(context.Background(), req)
	if err != nil || boundHead(t, f, id) != old || res.PRs[0].Status != "refused" {
		t.Fatalf("the superseded head was recovered: %v %+v (bound %s)", err, res.PRs, boundHead(t, f, id))
	}
	entries, _ := f.train.Audit.Read()
	if slices.ContainsFunc(entries, func(e Entry) bool { return e.Run == res.Run && (e.Kind == "rebind" || e.Kind == "rebase") }) ||
		!slices.ContainsFunc(entries, func(e Entry) bool {
			return e.Kind == "reconcile" && e.Run == res.Run && strings.Contains(e.Detail, "does not recover it, only merge-train rebind re-admits it")
		}) {
		t.Fatalf("the journal: %v", kinds(entries, 1))
	}
}

// supersedeOnUpdate performs the fake's update and then journals a
// rebind_superseded entry for the head it produced, before the read-back:
// a lead re-bind of that head refused while the train waited.
type supersedeOnUpdate struct {
	Forge
	f *fixture
}

func (u *supersedeOnUpdate) Rebase(ctx context.Context, repository string, number int, expect string) (string, error) {
	out, err := u.Forge.Rebase(ctx, repository, number, expect)
	head := u.f.head("pr1")
	if aerr := u.f.train.Audit.Append(Entry{Run: "mt-rebind-meanwhile", Kind: "rebind_superseded", Repository: repository, Base: "main", PR: number, Head: expect, NewHead: head, Detail: "a lead re-bind of this head refused meanwhile"}); aerr != nil {
		return "", aerr
	}
	return out, err
}

// land's binding write: the train's own update produces H, and H is
// superseded before the train binds it. The proof holds (first parent the
// base tip, the patch unchanged), and the admission check refuses it:
// pr_head_moved naming the lead's verb, nothing journaled as rebase or
// rebind for the PR under the run, the binding unchanged, nothing merged.
// Reverted (land's check skipped), the train binds and merges H.
func TestTrainUpdateOfASupersededHeadIsNotBound(t *testing.T) {
	f, id, old, req := strictBound(t)
	f.train.Forge = &supersedeOnUpdate{Forge: f.gh, f: f}
	res, err := f.train.Run(context.Background(), req)
	if err != nil || res.PRs[0].Status != "refused" || res.PRs[0].Refusals[0].Rule != RulePRHeadMoved ||
		!strings.Contains(res.PRs[0].Refusals[0].Detail, "superseded by a lead re-bind decision") || boundHead(t, f, id) != old {
		t.Fatalf("the superseded head was bound: %v %+v (bound %s)", err, res.PRs, boundHead(t, f, id))
	}
	entries, _ := f.train.Audit.Read()
	if slices.ContainsFunc(entries, func(e Entry) bool { return e.Run == res.Run && e.PR == 1 && (e.Kind == "rebind" || e.Kind == "rebase") }) {
		t.Fatalf("the run journaled a rebase or a rebind of the superseded head: %v", kinds(entries, 1))
	}
	if strings.Contains(f.calls(), "-X PUT") {
		t.Fatal("something merged")
	}
}

// Rebind: the verb is the re-admission, so its check words the entry it
// writes: a first re-bind's readmission entry says the head is the train's
// own; one after a superseded attempt on the same head says it is the
// train's own again, after that attempt, and the next run lands the PR.
// Reverted (the head taken as admitted), the second wording is lost.
func TestRebindWordsTheReadmissionAfterASupersededAttempt(t *testing.T) {
	readmission := func(t *testing.T, f *fixture, run string) string {
		t.Helper()
		entries, _ := f.train.Audit.Read()
		i := slices.IndexFunc(entries, func(e Entry) bool { return e.Kind == "rebind_readmitted" && e.Run == run })
		if i < 0 {
			t.Fatalf("no readmission entry under the verb's run %s: %v", run, kinds(entries, 1))
		}
		return entries[i].Detail
	}
	apply := func(t *testing.T, f *fixture, old, moved string) RebindResult {
		t.Helper()
		s := f.tasks()
		res, err := Rebind(context.Background(), RebindInput{Forge: f.gh, Audit: f.train.Audit, Tasks: s, Binder: StoreBinder{Store: s}, PR: 1, From: old, To: moved, Lead: "lead"})
		if err != nil {
			t.Fatalf("the re-bind: %v", err)
		}
		return res
	}
	t.Run("a first re-bind", func(t *testing.T) {
		f, _, old, _ := strictBound(t)
		_, moved := moveBranch(f, "pr1", nil, true)
		if got := readmission(t, f, apply(t, f, old, moved).Run); got != "the lead's re-bind bound this head; it is the train's own" {
			t.Fatalf("the readmission: %q", got)
		}
	})
	t.Run("after a superseded attempt", func(t *testing.T) {
		f, id, old, req := strictBound(t)
		_, moved := moveBranch(f, "pr1", nil, true)
		refusedRebind(t, f, id, old, moved)
		if got := readmission(t, f, apply(t, f, old, moved).Run); got != "the lead's re-bind bound this head; it is the train's own again, after a superseded attempt" {
			t.Fatalf("the readmission: %q", got)
		}
		entries, _ := f.train.Audit.Read()
		if !headAdmitted(entries, fixtureRepo, 1, moved) || boundHead(t, f, id) != moved {
			t.Fatalf("the head is not admitted and bound (bound %s)", boundHead(t, f, id))
		}
		f.realRun(&req)
		if res, err := f.train.Run(context.Background(), req); err != nil || res.PRs[0].Status != "merged" {
			t.Fatalf("the re-admitted head did not land: %v %+v", err, res.PRs)
		}
	})
}

// supersedeDuring journals rebind_superseded(head) for PR 1 the first time
// the hook's call is made, after the caller's journal snapshot: inside the
// recovery's proof (FirstParent) or the replay's PR read.
type supersedeDuring struct {
	Forge
	f          *fixture
	old, head  string
	onParent   bool
	onPR, done bool
}

func (s *supersedeDuring) supersede() {
	if s.done {
		return
	}
	s.done = true
	if err := s.f.train.Audit.Append(Entry{Run: "mt-rebind-meanwhile", Kind: "rebind_superseded", Repository: fixtureRepo, Base: "main", PR: 1, Head: s.old, NewHead: s.head, Detail: "a lead re-bind of this head refused meanwhile"}); err != nil {
		s.f.t.Fatal(err)
	}
}

func (s *supersedeDuring) FirstParent(ctx context.Context, repository, sha string) (string, error) {
	if s.onParent && sha == s.head {
		s.supersede()
	}
	return s.Forge.FirstParent(ctx, repository, sha)
}

func (s *supersedeDuring) PR(ctx context.Context, repository string, number int) (PRInfo, error) {
	if s.onPR && number == 1 {
		s.supersede()
	}
	return s.Forge.PR(ctx, repository, number)
}

// Codex's fourth pass on #903 (1): the admission is checked on a fresh read
// of the journal under the audit lock right before the binding write, not
// on the snapshot a reconcile read before its network reads and its proof
// (an earlier PR's CI wait can take twenty minutes). A rebind_superseded
// entry appended during the recovery's proof, or during the replay's PR
// read, leaves the head unbound: the binding stays, a reconcile entry
// names the lead's verb, and the check refuses the head. Reverted (only the
// earlier snapshot checked), the head is bound.
func TestSupersededDuringTheRecoveryIsNotBound(t *testing.T) {
	t.Run("during the recovery's proof", func(t *testing.T) {
		f, id, old, req := strictBound(t)
		if err := f.train.Audit.Append(Entry{Run: "mt-earlier", Kind: "update_requested", Repository: fixtureRepo, Base: "main", PR: 1, Task: id, Head: old, BaseSHA: f.head("main")}); err != nil {
			t.Fatal(err)
		}
		_, moved := moveBranch(f, "pr1", nil, true)
		hook := &supersedeDuring{Forge: f.gh, f: f, old: old, head: moved, onParent: true}
		f.train.Forge = hook
		res, err := f.train.Run(context.Background(), req)
		if err != nil || !hook.done || boundHead(t, f, id) != old || res.PRs[0].Status != "refused" {
			t.Fatalf("the head superseded during the proof was bound: %v %+v (bound %s, hooked %v)", err, res.PRs, boundHead(t, f, id), hook.done)
		}
		entries, _ := f.train.Audit.Read()
		if !slices.ContainsFunc(entries, func(e Entry) bool {
			return e.Kind == "reconcile" && e.Run == res.Run && e.NewHead == moved && strings.Contains(e.Detail, "before the binding write; the train does not bind it, only merge-train rebind re-admits it")
		}) {
			t.Fatalf("no reconcile entry names the verb: %v", kinds(entries, 1))
		}
	})
	t.Run("during the replay's PR read", func(t *testing.T) {
		f, id, old, req := strictBound(t)
		_, moved := moveBranch(f, "pr1", nil, true)
		// a run that journaled its proof-backed rebind and stopped before
		// the binding write
		if err := f.train.Audit.Append(Entry{Run: "mt-earlier", Kind: "rebind", Repository: fixtureRepo, Base: "main", PR: 1, Task: id, Head: old, NewHead: moved, Detail: "patch-id: abc\nfirst parent: " + f.head("main")}); err != nil {
			t.Fatal(err)
		}
		hook := &supersedeDuring{Forge: f.gh, f: f, old: old, head: moved, onPR: true}
		f.train.Forge = hook
		res, err := f.train.Run(context.Background(), req)
		if err != nil || !hook.done || boundHead(t, f, id) != old || res.PRs[0].Status != "refused" {
			t.Fatalf("the head superseded during the replay's read was bound: %v %+v (bound %s)", err, res.PRs, boundHead(t, f, id))
		}
		entries, _ := f.train.Audit.Read()
		if !slices.ContainsFunc(entries, func(e Entry) bool {
			return e.Kind == "reconcile" && e.Run == res.Run && e.NewHead == moved && strings.Contains(e.Detail, "before the binding write")
		}) {
			t.Fatalf("no reconcile entry names the verb: %v", kinds(entries, 1))
		}
	})
}

// The reviewer's condition on bindAdmitted (one lock, two serialized
// orders), order (a): the supersede takes the journal lock first. The
// binding write, started while the lock is held, waits for it; its fresh
// read then sees the supersede and refuses, and the head stays unbound.
// Reverted (the journal read before the lock is taken), the write reads
// the journal without the supersede and binds the head.
func TestSupersedeFirstIsSeenByTheBindingWrite(t *testing.T) {
	f, id, old, _ := strictBound(t)
	_, moved := moveBranch(f, "pr1", nil, true)
	s, a := f.tasks(), f.train.Audit
	done := make(chan error, 1)
	err := a.locked(func() error {
		started := make(chan struct{})
		go func() {
			close(started)
			_, err := bindAdmitted(a, StoreBinder{Store: s}, nil, bindWrite{Task: id, Repository: fixtureRepo, PR: 1, From: old, To: moved})
			done <- err
		}()
		<-started
		time.Sleep(200 * time.Millisecond) // the write is at the lock now
		select {
		case err := <-done:
			return fmt.Errorf("the binding write did not wait for the journal lock: %v", err)
		default:
		}
		return a.appendLocked(Entry{Run: "mt-rebind-first", Kind: "rebind_superseded", Repository: fixtureRepo, Base: "main", PR: 1, Head: old, NewHead: moved, Detail: "the supersede takes the lock first"})
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := <-done; !errors.Is(err, ErrHeadSuperseded) || boundHead(t, f, id) != old {
		t.Fatalf("the write after the supersede bound the head: %v (bound %s)", err, boundHead(t, f, id))
	}
}

// lockedBinder holds the binding write open for a while and starts a
// supersede append meanwhile, which must wait for the journal lock the
// write runs under.
type lockedBinder struct {
	StoreBinder
	a                Audit
	old, head        string
	appended, waited chan error
}

func (b *lockedBinder) BindPR(taskID, repository string, pr int, head string) error {
	go func() {
		b.appended <- b.a.Append(Entry{Run: "mt-rebind-after", Kind: "rebind_superseded", Repository: repository, Base: "main", PR: pr, Head: b.old, NewHead: b.head, Detail: "the supersede takes the lock after the binding write"})
	}()
	time.Sleep(200 * time.Millisecond)
	select {
	case err := <-b.appended:
		b.waited <- fmt.Errorf("the supersede was appended during the binding write: %v", err)
		b.appended <- err
	default:
		b.waited <- nil
	}
	return b.StoreBinder.BindPR(taskID, repository, pr, head)
}

// Order (b): the binding write takes the journal lock first and binds; the
// supersede, started during the write, is appended after it. The binding
// is the head, the journal has the supersede after the train's rebase
// entry, trainHeads excludes the head, and the next run refuses it on
// force_push. Reverted (the binding write outside the lock), the supersede
// lands during the write.
func TestBindingFirstThenSupersedeIsExcludedNextRun(t *testing.T) {
	f, id, old, req := strictBound(t)
	_, moved := moveBranch(f, "pr1", nil, true)
	if err := f.train.Audit.Append(Entry{Run: "mt-earlier", Kind: "rebase", Repository: fixtureRepo, Base: "main", PR: 1, Head: old, NewHead: moved, BaseSHA: f.head("main")}); err != nil {
		t.Fatal(err)
	}
	if !f.train.Audit.trainHeads(fixtureRepo, 1)[moved] {
		t.Fatal("the train's rebase entry does not name the head")
	}
	b := &lockedBinder{StoreBinder: StoreBinder{Store: f.tasks()}, a: f.train.Audit, old: old, head: moved, appended: make(chan error, 1), waited: make(chan error, 1)}
	if _, err := bindAdmitted(f.train.Audit, b, nil, bindWrite{Task: id, Repository: fixtureRepo, PR: 1, From: old, To: moved}); err != nil {
		t.Fatalf("the binding write: %v", err)
	}
	if err := <-b.waited; err != nil {
		t.Fatal(err)
	}
	if err := <-b.appended; err != nil {
		t.Fatal(err)
	}
	if boundHead(t, f, id) != moved || f.train.Audit.trainHeads(fixtureRepo, 1)[moved] {
		t.Fatalf("bound %s, trainHeads %v", boundHead(t, f, id), f.train.Audit.trainHeads(fixtureRepo, 1))
	}
	f.realRun(&req)
	res, err := f.train.Run(context.Background(), req)
	if err != nil || res.PRs[0].Status != "refused" || !slices.ContainsFunc(res.PRs[0].Refusals, func(r Refusal) bool { return r.Rule == RuleForcePush }) {
		t.Fatalf("the superseded head was taken for the train's own: %v %+v", err, res.PRs)
	}
}
