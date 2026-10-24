package mergetrain

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// The task-to-PR binding (3-2 fix 2, P1-4, lead ruling). The harness commit
// step (2b-2 milestone B) writes the PR number and the head it pushed into
// the task when it opens the PR; the train reads the task through that
// binding, never through a task id the request names. A real run's request
// names PR numbers only. The fields are milestone B's (task.Store.BindPR):
// pr_number (int) and pr_head (string), read by name here until milestone B
// merges.
type boundTask struct {
	ID            string   `json:"id"`
	DeclaredFiles []string `json:"declared_files"`
	PRNumber      int      `json:"pr_number"`
	PRHead        string   `json:"pr_head"`
}

// BindTasks fills each PR of a real-run request from the task bound to it in
// the session's task store (<root>/extensions/amq-squad/tasks/<id>.json): its
// id, declared files and pushed head. A PR with no bound task, or with more
// than one, is marked unbound and the check refuses it (task_unbound).
func BindTasks(taskDir string, req *Request) error {
	entries, err := os.ReadDir(taskDir)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("read task store: %w", err)
	}
	byPR := map[int][]boundTask{}
	for _, e := range entries {
		if e.IsDir() || strings.HasPrefix(e.Name(), ".") || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(taskDir, e.Name()))
		if err != nil {
			return fmt.Errorf("read %s: %w", e.Name(), err)
		}
		var t boundTask
		if err := json.Unmarshal(raw, &t); err != nil {
			return fmt.Errorf("parse %s: %w", e.Name(), err)
		}
		if t.PRNumber > 0 {
			byPR[t.PRNumber] = append(byPR[t.PRNumber], t)
		}
	}
	for i := range req.PRs {
		it := &req.PRs[i]
		bound := byPR[it.Number]
		if len(bound) != 1 {
			it.Unbound = fmt.Sprintf("%d tasks are bound to PR #%d", len(bound), it.Number)
			continue
		}
		it.Task, it.DeclaredFiles, it.BoundHead = bound[0].ID, bound[0].DeclaredFiles, bound[0].PRHead
	}
	return nil
}
