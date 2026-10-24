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
func (s Store) BindPR(id, repository string, pr int, head string, now time.Time) (Task, error) {
	repository, head = strings.TrimSpace(repository), strings.TrimSpace(head)
	if !bindRepoRe.MatchString(repository) || pr < 1 || !bindHeadRe.MatchString(head) {
		return Task{}, fmt.Errorf("bind PR: need owner/name, a PR number and a full lowercase head SHA (got %q #%d %q)", repository, pr, head)
	}
	return s.mutate(id, now, func(t *Task, _ *tasks) error {
		t.PRRepository, t.PRNumber, t.PRHead = repository, pr, head
		return nil
	})
}
