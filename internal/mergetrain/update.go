package mergetrain

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// The branch update GitHub performs asynchronously (brief 3.2.0-3a). On
// 2026-11-05 (run mt-20261105T192313Z-ab184d07, PR #894) the
// updatePullRequestBranch mutation answered with the head as it was
// (68792071) and GitHub's rebase landed five seconds later (2555d34a), so the
// train saw no change, wrote no rebase entry, bound nothing and waited for no
// CI; the check that followed read the moved head and refused it on
// ci_green, force_push and pr_head_moved, with no path back but the seat's
// commit step. The train now journals the request before the mutation, reads
// the head back until it moves (bounded), proves the head it read (its first
// parent is the base tip it rebased onto; the patch-id is unchanged) before
// it binds it, and recovers a branch it moved without recording the result
// from the journaled request.

func (t *Train) updateWait() time.Duration {
	if t.UpdateWait > 0 {
		return t.UpdateWait
	}
	return DefaultUpdateWait
}

// readUpdatedHead reads the PR back after updatePullRequestBranch until its
// head differs from the pinned one, with a backoff (1 s doubling to 16 s)
// bounded by UpdateWait. moved is false when the wait ended with the head
// still pinned: GitHub did not update the branch (an update it rejected
// asynchronously leaves the head unmoved), or the context ended (the
// sleep returns at ctx.Done; a cancelled wait is the same refusal, nothing
// bound). The bound is on the waiting, not a cutoff on the read: a read
// that starts before the deadline may complete after it, and a head that
// moved by then is accepted.
func (t *Train) readUpdatedHead(ctx context.Context, repository string, pr int, pinned string) (cur PRInfo, moved bool, err error) {
	deadline := t.now().Add(t.updateWait())
	backoff := time.Second
	for {
		if cur, err = t.Forge.PR(ctx, repository, pr); err != nil {
			return PRInfo{}, false, err
		}
		if cur.HeadSHA != pinned {
			return cur, true, nil
		}
		remaining := deadline.Sub(t.now())
		if remaining <= 0 || ctx.Err() != nil {
			return cur, false, nil
		}
		if !t.sleepOrDone(ctx, min(backoff, remaining)) {
			return cur, false, nil
		}
		backoff = min(2*backoff, 16*time.Second)
	}
}

// sleepOrDone sleeps d or until ctx ends, and reports whether ctx is still
// live. The Sleep seam, when set, is called for the whole of d (a test
// clock) and ctx is read after it.
func (t *Train) sleepOrDone(ctx context.Context, d time.Duration) bool {
	if t.Sleep != nil {
		t.Sleep(d)
		return ctx.Err() == nil
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-ctx.Done():
		return false
	}
}

// baseCandidate is a base tip the update may have landed on, with the name
// the record gives it.
type baseCandidate struct{ sha, which string }

// updateProof is the verified identity of a branch update the train
// requested: the one force push from the bound head to the current head,
// the current head's first parent (one of the base candidates) and the
// equal patch ids of the two heads against it.
type updateProof struct {
	From, To     string
	Parent       string
	ParentIs     string
	OldID, NewID string
	Push         ForcePush
}

// proveUpdate checks that cur's head (to) is the result of the train's
// update of from (brief 3.2.0-3a items 3 and 6): the force-push timeline
// read whole; exactly one force push, after since when given (the journaled
// request's time, compared at second precision as the timeline reports it),
// and that one from the bound head to the current head; the current head's
// first parent one of the base candidates; the patch ids equal. A failed
// part is returned in words (why); a forge failure is an error.
func proveUpdate(ctx context.Context, forge Forge, repository string, cur PRInfo, from, to string, bases []baseCandidate, since string) (updateProof, string, error) {
	proof := updateProof{From: from, To: to}
	if cur.HistoryIncomplete != "" {
		return proof, "the force-push timeline could not be read whole: " + cur.HistoryIncomplete, nil
	}
	var after []ForcePush
	for _, fp := range cur.ForcePushes {
		if since != "" {
			if fp.At == "" {
				return proof, fmt.Sprintf("the force push %s -> %s carries no time, so it cannot be placed after the journaled request", short(fp.Before), short(fp.After)), nil
			}
			if !pushedAfter(fp.At, since) {
				continue
			}
		}
		after = append(after, fp)
	}
	scope := ""
	if since != "" {
		scope = " after the journaled request"
	}
	switch {
	case len(after) == 0:
		return proof, fmt.Sprintf("no force push%s; the bound head %s was not moved by an update", scope, short(from)), nil
	case len(after) > 1:
		return proof, fmt.Sprintf("%d force pushes%s, one expected (the update's); the last is %s -> %s", len(after), scope, short(after[len(after)-1].Before), short(after[len(after)-1].After)), nil
	case after[0].Before != from || after[0].After != to:
		return proof, fmt.Sprintf("the one force push%s is %s -> %s, not from the bound head %s to the current head %s", scope, short(after[0].Before), short(after[0].After), short(from), short(to)), nil
	}
	proof.Push = after[0]
	parent, err := forge.FirstParent(ctx, repository, to)
	if err != nil {
		return proof, "", fmt.Errorf("read the first parent of %s: %w", short(to), err)
	}
	proof.Parent = parent
	for _, b := range bases { // the first match names it: a journaled base before the current tip
		if b.sha != "" && b.sha == parent {
			proof.ParentIs = b.which
			break
		}
	}
	if proof.ParentIs == "" {
		var names []string
		for _, b := range bases {
			if b.sha != "" {
				names = append(names, b.which+" "+short(b.sha))
			}
		}
		return proof, fmt.Sprintf("the current head's first parent %s is not a base tip the update could have landed on (%s)", short(parent), strings.Join(names, ", ")), nil
	}
	if proof.OldID, err = forge.PatchID(ctx, repository, parent, from); err == nil {
		proof.NewID, err = forge.PatchID(ctx, repository, parent, to)
	}
	if errors.Is(err, ErrIncompleteDiff) {
		return proof, "the update could not be proved: GitHub returned an incomplete diff", nil
	}
	if err != nil {
		return proof, "", fmt.Errorf("prove the patch of %s and %s: %w", short(from), short(to), err)
	}
	if proof.OldID != proof.NewID {
		return proof, fmt.Sprintf("the change moved during the update: patch-id %s at the bound head, %s at the current head", proof.OldID, proof.NewID), nil
	}
	return proof, "", nil
}

// pushedAfter: the force push's time (RFC3339, whole seconds as the timeline
// reports it) is not before the journaled request's time, compared at
// second precision.
func pushedAfter(at, since string) bool {
	a, err := time.Parse(time.RFC3339Nano, at)
	if err != nil {
		return false
	}
	s, err := time.Parse(time.RFC3339Nano, since)
	if err != nil {
		return false
	}
	return !a.Truncate(time.Second).Before(s.Truncate(time.Second))
}

// reconcileUpdates recovers a branch the train moved without recording the
// result (brief 3.2.0-3a item 3): a bound PR whose head is off its binding,
// with a journaled update_requested for that bound head on record, is
// re-bound after the fact when the proof holds (exactly one force push after
// the request, from the bound head; the current head's first parent the
// journaled base tip, or the base's current tip if the base moved since; the
// patch ids equal): the rebase and rebind entries first, then the binding
// write, then CI waited at the recovered head as after the train's own
// update. Anything else stays as it is and the check refuses it
// (pr_head_moved, force_push): a force push with no train request on record
// is exactly the event pr_head_moved exists to stop, and the owner's identity
// on the push does not tell the train's push from the owner's own hand.
func (t *Train) reconcileUpdates(ctx context.Context, req *Request, baseTip string, ci []RequiredCheck, entry func(Entry) error) error {
	if t.Binder == nil || t.Tasks == nil {
		return nil
	}
	entries, err := t.Audit.Read()
	if err != nil {
		return err
	}
	for i := range req.PRs {
		it := &req.PRs[i]
		if it.BoundHead == "" {
			continue
		}
		requested, ok := lastUpdateRequest(entries, req.Repository, it.Number, it.BoundHead)
		if !ok {
			continue
		}
		cur, err := t.Forge.PR(ctx, req.Repository, it.Number)
		if err != nil {
			return fmt.Errorf("read PR #%d while reconciling its update: %w", it.Number, err)
		}
		if cur.HeadSHA == it.BoundHead {
			continue // the update never landed (or was refused unconfirmed): the check decides
		}
		bases := []baseCandidate{{requested.BaseSHA, "the journaled base tip"}, {baseTip, "the base's current tip"}}
		proof, why, err := proveUpdate(ctx, t.Forge, req.Repository, cur, it.BoundHead, cur.HeadSHA, bases, requested.At)
		if err != nil {
			return err
		}
		if why != "" {
			if err := entry(Entry{Kind: "reconcile", PR: it.Number, Task: it.Task, Head: it.BoundHead, NewHead: cur.HeadSHA,
				Detail: fmt.Sprintf("the update journaled by run %s was not recorded, and the current head is not its result: %s\nbinding not recovered", requested.Run, why)}); err != nil {
				return err
			}
			continue
		}
		detail := fmt.Sprintf("recovered: the update journaled by run %s moved the branch and was not recorded; first parent %s is %s; one force push %s -> %s by %s", requested.Run, short(proof.Parent), proof.ParentIs, short(proof.Push.Before), short(proof.Push.After), orDash(proof.Push.Actor))
		if err := entry(Entry{Kind: "rebase", PR: it.Number, Task: it.Task, Head: it.BoundHead, NewHead: cur.HeadSHA, BaseSHA: proof.Parent, Detail: detail}); err != nil {
			return err
		}
		if err := entry(Entry{Kind: "rebind", PR: it.Number, Task: it.Task, Head: it.BoundHead, NewHead: cur.HeadSHA, Detail: "patch-id: " + proof.OldID + "\nfirst parent: " + proof.Parent + "\n" + detail}); err != nil {
			return err
		}
		if err := t.Binder.BindPR(it.Task, req.Repository, it.Number, cur.HeadSHA); err != nil {
			return fmt.Errorf("%w: task %s to %s: %v", ErrBindingFailed, it.Task, short(cur.HeadSHA), err)
		}
		it.BoundHead = cur.HeadSHA
		t.waitCI(ctx, req.Repository, cur.HeadSHA, ci)
	}
	return nil
}
