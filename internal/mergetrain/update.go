package mergetrain

import (
	"context"
	"errors"
	"fmt"
	"regexp"
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
		// each read runs under the remaining wait (Codex's re-pass on #903:
		// a stalled gh must not hang past the deadline); a read that times
		// out counts as unmoved for that poll
		remaining := deadline.Sub(t.now())
		if remaining <= 0 {
			remaining = time.Millisecond
		}
		readCtx, cancel := context.WithTimeout(ctx, remaining)
		cur, err = t.Forge.PR(readCtx, repository, pr)
		timedOut := readCtx.Err() == context.DeadlineExceeded && ctx.Err() == nil // the read's own deadline, whatever gh's error says (a gh killed by it returns an exit error)
		cancel()
		if err != nil && !timedOut {
			return PRInfo{}, false, err
		}
		if err == nil && cur.HeadSHA != pinned {
			return cur, true, nil
		}
		if err != nil {
			cur = PRInfo{HeadSHA: pinned} // the poll timed out: unmoved for this poll
		}
		remaining = deadline.Sub(t.now())
		if remaining <= 0 {
			return cur, false, nil
		}
		if !t.sleepOrDone(ctx, min(backoff, remaining)) { // the one guard on the context: a done context ends the wait here
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

// definiteRefusal classifies a failed updatePullRequestBranch (the plan
// reviewer on #903, Codex's passes: definite only on a refusal we can
// name). Definite: a 4xx (gh prints an HTTP error as `gh: <message> (HTTP
// <code>)`), or a GraphQL error whose message, with one of gh's two
// documented wrappers removed whole (refusalMessage), equals an entry of
// graphQLRefusals, case aside. The GraphQL rule reads gh's stderr as gh
// printed it (GHError.Stderr), never this package's wrapper around it; an
// error that does not come from gh has no gh line to read. Any other shape
// (a fragment of a table message inside other text, anything extra before
// or after a wrapper, several errors, a `gh: HTTP <code>` line, a 5xx, a
// transport error, a context deadline, a killed gh) is ambiguous: brittle
// in the safe direction (GitHub may have completed the update, so the
// request stays live and the head is read back), never in the definite
// one. The wrappers are gh's own, version by version: a gh that changes
// them falls to ambiguous, the safe side.
func definiteRefusal(err error) bool {
	if strings.Contains(strings.ToLower(err.Error()), "(http 4") {
		return true
	}
	var ge *GHError
	if !errors.As(err, &ge) {
		return false
	}
	msg, ok := refusalMessage(ge.Stderr)
	if !ok {
		return false
	}
	for _, refusal := range graphQLRefusals {
		if strings.EqualFold(msg, refusal) {
			return true
		}
	}
	return false
}

// refusalMessage strips one of gh's two documented error wrappers, each
// only as a whole, from the last line gh printed on stderr (gh prints the
// error last; the lines before it, such as the fake gh's sentinel line, do
// not decide). Read on gh 2.100.0 (go-gh v2.16.0, its go.mod), each form
// measured with one read-only query of a PR that does not exist:
//
//   - `gh: <message>`, the line `gh api graphql` prints (GH.Rebase's path):
//     cli/cli v2.100.0 pkg/cmd/api/api.go, parseErrorResponse joins the
//     body's errors[].message with "\n" (line 722) and processResponse
//     prints `fmt.Fprintf(opts.IO.ErrOut, "gh: %s\n", serverError)` (line
//     557). Measured: `gh api graphql -f query=...pullRequest(number:999999)...`
//     printed `gh: Could not resolve to a PullRequest with the number of 999999.`
//   - `GraphQL: <message> (<path>)`, the form gh's other commands print:
//     cli/go-gh v2.16.0 pkg/api/errors.go, GraphQLError.Error (lines
//     59-68) writes `GraphQL: ` and each error as `<message> (<path>)`,
//     the path's elements joined by "." (pathString, line 82), several
//     errors joined by ", ". Measured: `gh pr view 999999 -R
//     omriariav/amq-squad --json id` printed `GraphQL: Could not resolve to
//     a PullRequest with the number of 999999. (repository.pullRequest)`.
//
// ok is false when the line is neither form whole (a GraphQL line with no
// path is not the second form).
func refusalMessage(stderr string) (string, bool) {
	lines := strings.Split(strings.TrimSpace(stderr), "\n")
	last := strings.TrimRight(lines[len(lines)-1], "\r")
	if msg, ok := strings.CutPrefix(last, "gh: "); ok {
		return msg, true
	}
	msg, ok := strings.CutPrefix(last, "GraphQL: ")
	if !ok {
		return "", false
	}
	open := strings.LastIndex(msg, " (")
	if open < 0 || !strings.HasSuffix(msg, ")") || !graphQLPathRe.MatchString(msg[open+2:len(msg)-1]) {
		return "", false
	}
	return msg[:open], true
}

// graphQLPathRe is go-gh's error path: its elements (field names and list
// indexes) joined by ".".
var graphQLPathRe = regexp.MustCompile(`^[A-Za-z0-9_]+(\.[A-Za-z0-9_]+)*$`)

// graphQLRefusals is the closed table of updatePullRequestBranch refusals:
// each entry is GitHub's exact message, with its source. A refusal whose
// exact wording is not evidenced is not in the table (and so ambiguous: the
// request stays live, bounded by its window). The fake gh of this package's
// tests prints the first message for its pinned-head mismatch in the
// `gh: <message>` form `gh api graphql` prints, as its last line, after a
// sentinel line of its own; the sentinel stays out of this table, and the
// fake is classed by the same rule as gh.
var graphQLRefusals = []string{
	// GitHub's message when expectedHeadOid is no longer the branch head:
	// the REST "Update a pull request branch" endpoint documents the 422
	// with this body, and the GraphQL mutation answers the same message
	// (read on #894's own update attempts):
	// https://docs.github.com/en/rest/pulls/pulls#update-a-pull-request-branch
	"Head branch was modified. Review and try the update again.",
	// GitHub's GraphQL error for a token without access to the resource (an
	// app token short of the permission), the wording as GitHub's docs and
	// gh print it:
	// https://docs.github.com/en/graphql/overview/resource-limitations
	"Resource not accessible by integration",
}

// RunBound is an upper bound on one real run's wall time for n PRs (the
// CLI runs the train under a context bounded by it, so a stalled gh ends
// the run instead of hanging it): per PR the bounded update wait, the CI
// wait and ten minutes for the reads, the checks and the merge.
func RunBound(n int) time.Duration {
	if n < 1 {
		n = 1
	}
	return time.Duration(n) * (DefaultUpdateWait + DefaultCIWait + 10*time.Minute)
}

// DefaultCIWait is how long a rebased head may wait for CI (Train.CIWait).
const DefaultCIWait = 20 * time.Minute

// UpdateWindowSlack is added to the bounded wait to close the recovery's
// window on a journaled request: the last backoff of the read-back, so a
// read that started before the deadline and completed after it is inside.
const UpdateWindowSlack = 16 * time.Second

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
// read whole; exactly one force push, inside the window [since, until] when
// given (the journaled request's time to the end of its bounded wait plus
// one poll, compared at second precision as the timeline reports it), and
// that one from the bound head to the current head; the current head's
// first parent one of the base candidates; the patch ids equal. A failed
// part is returned in words (why); a forge failure is an error.
func proveUpdate(ctx context.Context, forge Forge, repository string, cur PRInfo, from, to string, bases []baseCandidate, since, until string) (updateProof, string, error) {
	proof := updateProof{From: from, To: to}
	if cur.HistoryIncomplete != "" {
		return proof, "the force-push timeline could not be read whole: " + cur.HistoryIncomplete, nil
	}
	var after []ForcePush
	late := 0
	for _, fp := range cur.ForcePushes {
		if since != "" {
			if fp.At == "" {
				return proof, fmt.Sprintf("the force push %s -> %s carries no time, so it cannot be placed in the journaled request's window", short(fp.Before), short(fp.After)), nil
			}
			if !pushedAfter(fp.At, since) {
				continue
			}
			if until != "" && !pushedAfter(until, fp.At) {
				late++
				continue
			}
		}
		after = append(after, fp)
	}
	scope := ""
	if since != "" {
		scope = " in the journaled request's window"
		if late > 0 {
			scope += fmt.Sprintf(" (%d after the window, which ended at %s; a push later than the bounded wait is never the update's)", late, until)
		}
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

// requestWindowEnd is the end of a journaled request's window: its time
// plus the bounded wait plus the last backoff (RFC3339, UTC); "" when the
// request carries no readable time.
func requestWindowEnd(at string, wait time.Duration) string {
	t, err := time.Parse(time.RFC3339Nano, at)
	if err != nil {
		return ""
	}
	return t.Add(wait + UpdateWindowSlack).UTC().Format(time.RFC3339Nano)
}

// pushedAfter: the force push's time (RFC3339, whole seconds as the timeline
// reports it) is not before the journaled request's time, compared at
// second precision. With the arguments swapped it is "not after": a push
// inside the window's last second is inside the window.
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
		requested, ambiguous, ok := lastUpdateRequest(entries, req.Repository, it.Number, it.BoundHead)
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
		// a head a lead re-bind superseded is never the train's to recover
		// (the plan reviewer on #903): only merge-train rebind re-admits it,
		// and it needs the binding where it is
		if !headAdmitted(entries, req.Repository, it.Number, cur.HeadSHA) {
			if err := entry(Entry{Kind: "reconcile", PR: it.Number, Task: it.Task, Head: it.BoundHead, NewHead: cur.HeadSHA,
				Detail: fmt.Sprintf("the current head %s was superseded by a lead re-bind decision; the train does not recover it, only merge-train rebind re-admits it\nbinding not recovered", short(cur.HeadSHA))}); err != nil {
				return err
			}
			continue
		}
		// the request's window: GitHub lands the update inside the bounded
		// wait, and a read that started before the deadline completes at
		// most one poll after it; a push later than that is never the
		// update's (the plan reviewer on #903)
		until := requestWindowEnd(requested.At, t.updateWait()) // every live request; to narrow it to the ambiguous class: `if ambiguous == "" { until = "" }`
		bases := []baseCandidate{{requested.BaseSHA, "the journaled base tip"}, {baseTip, "the base's current tip"}}
		proof, why, err := proveUpdate(ctx, t.Forge, req.Repository, cur, it.BoundHead, cur.HeadSHA, bases, requested.At, until)
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
		detail := fmt.Sprintf("recovered: the update journaled by run %s moved the branch and was not recorded; first parent %s is %s; one force push %s -> %s by %s, inside the request's window (to %s)", requested.Run, short(proof.Parent), proof.ParentIs, short(proof.Push.Before), short(proof.Push.After), orDash(proof.Push.Actor), until)
		if ambiguous != "" {
			detail += "; the request's mutation had failed without a definite refusal (" + ambiguous + ")"
		}
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
