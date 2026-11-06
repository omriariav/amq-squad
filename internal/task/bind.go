package task

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

var (
	bindRepoRe = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)
	bindHeadRe = regexp.MustCompile(`^([0-9a-f]{40}|[0-9a-f]{64})$`)
)

// BindPR binds a task to its PR: the repository (owner/name), the PR number
// and the head the harness pushed (3-2 fix 4). It runs inside the store's
// lock and journal like every mutation, so a concurrent lease renewal is not
// lost, and the binding survives later mutations. The harness commit step
// (2b-2 milestone B) binds when it opens the PR; the merge train re-binds
// after its own rebase push.
//
// A PR is never bound to two tasks (2b-2 milestone B, pr_bound_elsewhere): a
// repository and number another task holds refuses, inside the same
// transaction. CompleteBound applies the same rule with the completion.
func (s Store) BindPR(id, repository string, pr int, head string, now time.Time) (Task, error) {
	return s.mutate(id, now, func(t *Task, all *tasks) error {
		return bindPR(t, all, repository, pr, head)
	})
}

// BindPRFrom is BindPR only while the task's binding is still (repository,
// pr, from), checked and written under the store's lock (brief 3.2.0-3a,
// Codex on #903): the merge train's re-bind moves a binding it read a
// moment ago, and a train run that advanced the binding (and recorded a
// merge) in between must not be overwritten by the stale head. A binding
// already at exactly the target (the same repository and PR number, the
// head already head) is a read-only success, nothing written and any merge
// fact untouched (Codex's fifth pass on #903: a train's recovery that bound
// the same head first, or the verb run again after it landed). A binding
// anywhere else is ErrPRBindingMismatch.
func (s Store) BindPRFrom(id, repository string, pr int, from, head string, now time.Time) (Task, error) {
	var out Task
	wrote := false
	err := s.update(now, func(all *tasks) error {
		t := all.get(strings.TrimSpace(id))
		if t == nil {
			return fmt.Errorf("%w: %q", ErrNotFound, id)
		}
		repository := strings.TrimSpace(repository)
		if strings.EqualFold(t.PRRepository, repository) && t.PRNumber == pr && t.PRHead == strings.TrimSpace(head) {
			out = *t
			return nil
		}
		if !strings.EqualFold(t.PRRepository, repository) || t.PRNumber != pr || t.PRHead != strings.TrimSpace(from) {
			return fmt.Errorf("%w: bind PR from %s: task %s is bound to %s#%d at %s", ErrPRBindingMismatch, short(from), t.ID, t.PRRepository, t.PRNumber, t.PRHead)
		}
		if err := bindPR(t, all, repository, pr, head); err != nil {
			return err
		}
		all.touch(t.ID)
		out, wrote = *t, true
		return nil
	})
	if wrote {
		out.UpdatedAt = now
	}
	return out, err
}

func short(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}

// bindPR validates and writes the binding inside a mutation.
func bindPR(t *Task, all *tasks, repository string, pr int, head string) error {
	repository, head = strings.TrimSpace(repository), strings.TrimSpace(head)
	if !bindRepoRe.MatchString(repository) || pr < 1 || !bindHeadRe.MatchString(head) {
		return fmt.Errorf("bind PR: need owner/name, a PR number and a full lowercase head SHA (got %q #%d %q)", repository, pr, head)
	}
	if err := prFree(t, all, repository, pr); err != nil {
		return err
	}
	if t.PRRepository != repository || t.PRNumber != pr || t.PRHead != head {
		t.PRMergeHead, t.PRMergeCommit = "", ""
	}
	t.PRRepository, t.PRNumber, t.PRHead = repository, pr, head
	return nil
}

// RecordPRMerge records the merge train's proof that the task's currently
// bound PR merged at head. An identical fact is a read-only success.
func (s Store) RecordPRMerge(id, repository string, pr int, head, commit string, now time.Time) (Task, error) {
	var out Task
	wrote := false
	err := s.update(now, func(all *tasks) error {
		t := all.get(strings.TrimSpace(id))
		if t == nil {
			return fmt.Errorf("%w: %q", ErrNotFound, id)
		}
		repository, head, commit = strings.TrimSpace(repository), strings.TrimSpace(head), strings.TrimSpace(commit)
		if !bindRepoRe.MatchString(repository) || pr < 1 || !bindHeadRe.MatchString(head) || !bindHeadRe.MatchString(commit) {
			return fmt.Errorf("record PR merge: need owner/name, a PR number and full lowercase head and merge SHAs (got %q #%d %q %q)", repository, pr, head, commit)
		}
		if !strings.EqualFold(t.PRRepository, repository) || t.PRNumber != pr || t.PRHead != head {
			return fmt.Errorf("%w: record PR merge: task %s is bound to %s#%d at %s, not %s#%d at %s", ErrPRBindingMismatch, t.ID, t.PRRepository, t.PRNumber, t.PRHead, repository, pr, head)
		}
		if t.PRMergeCommit != "" && t.PRMergeCommit != commit {
			return fmt.Errorf("record PR merge: task %s already records commit %s", t.ID, t.PRMergeCommit)
		}
		if t.PRMergeHead != "" && t.PRMergeHead != head {
			return fmt.Errorf("record PR merge: task %s already records head %s", t.ID, t.PRMergeHead)
		}
		if t.PRMergeHead == head && t.PRMergeCommit == commit {
			out = *t
			return nil
		}
		t.PRMergeHead, t.PRMergeCommit = head, commit
		all.touch(t.ID)
		out, wrote = *t, true
		return nil
	})
	if wrote {
		out.UpdatedAt = now
	}
	return out, err
}
