package seatcommit

import (
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/v3/internal/grants"
	"github.com/omriariav/amq-squad/v3/internal/task"
)

func TestResolvedProtectedWidenGateNamesTheTarget(t *testing.T) {
	root, repo := t.TempDir(), t.TempDir()
	resolved, _ := task.LiteralGlob(".claude/settings.json")
	d := CheckWiden(WidenOptions{Root: root, Task: task.Task{ID: "t1"}, Added: []string{resolved}, ProtectedAdded: []string{resolved}, Grants: grants.Resolved{Grants: grants.Defaults()}, RepoRoot: repo})
	want := ProtectedTarget(root, "t1", []string{resolved})
	if d.Allowed || d.ReasonCode != ReasonGrowthGate || len(d.Crossings) != 1 || d.Crossings[0].Target != want || strings.Contains(d.Crossings[0].Target, "settings-link") {
		t.Fatalf("resolved protected widen = %+v, want target %q", d, want)
	}
}
