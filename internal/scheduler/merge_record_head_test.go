package scheduler

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/v3/internal/task"
)

func taskInput(title string, dependsOn []string) task.AddInput {
	return task.AddInput{Title: title, Class: "implement", Pool: &sonnet, DependsOn: dependsOn}
}

func TestLegacyCommitOnlyMergeRecordIsAuditedAtGrant(t *testing.T) {
	h := newHarness(t, Config{})
	dep, err := h.store.Add(taskInput("dependency", nil), t0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.store.BindPR(dep.ID, "o/r", 31, strings.Repeat("a", 40), t0); err != nil {
		t.Fatal(err)
	}
	dep = h.get(dep.ID)
	dep.State = task.StateCompleted
	dep.PRMergeCommit = strings.Repeat("b", 40)
	raw, _ := json.Marshal(dep)
	if err := os.WriteFile(filepath.Join(h.store.Dir, dep.ID+".json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	child, err := h.store.Add(taskInput("child", []string{dep.ID}), t0)
	if err != nil {
		t.Fatal(err)
	}
	h.up("seat-a", sonnet)
	ds := h.tick()
	if !has(ds, "dispatched", child.ID, "seat-a") {
		t.Fatalf("legacy merge did not release dependent: %+v", ds)
	}
	var audit []Decision
	for _, d := range ds {
		if d.Kind == "legacy_merge_head_missing" {
			audit = append(audit, d)
		}
	}
	want := "dependency " + dep.ID + ": its merge record has no head (written before 3.1.1)"
	if len(audit) != 1 || audit[0].TaskID != child.ID || audit[0].Reason != want {
		t.Fatalf("legacy merge release audit missing: %+v", ds)
	}
}
