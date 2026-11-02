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
		t.PRMergeCommit = ""
	}
	t.PRRepository, t.PRNumber, t.PRHead = repository, pr, head
	return nil
}

// RecordPRMerge records the merge train's proof that the task's currently
// bound PR merged. The binding must still name the same repository and PR.
func (s Store) RecordPRMerge(id, repository string, pr int, commit string, now time.Time) (Task, error) {
	return s.mutate(id, now, func(t *Task, _ *tasks) error {
		repository, commit = strings.TrimSpace(repository), strings.TrimSpace(commit)
		if !bindRepoRe.MatchString(repository) || pr < 1 || !bindHeadRe.MatchString(commit) {
			return fmt.Errorf("record PR merge: need owner/name, a PR number and a full lowercase merge SHA (got %q #%d %q)", repository, pr, commit)
		}
		if !strings.EqualFold(t.PRRepository, repository) || t.PRNumber != pr {
			return fmt.Errorf("record PR merge: task %s is bound to %s#%d, not %s#%d", t.ID, t.PRRepository, t.PRNumber, repository, pr)
		}
		if t.PRMergeCommit != "" && t.PRMergeCommit != commit {
			return fmt.Errorf("record PR merge: task %s already records %s", t.ID, t.PRMergeCommit)
		}
		t.PRMergeCommit = commit
		return nil
	})
}
