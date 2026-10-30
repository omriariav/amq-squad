package cli

import (
	"path/filepath"
	"testing"
	"time"
)

// 3.0.1 t9 (reviewer ruling 1): a seat never answers a dialog, and its
// question to anyone is AMQ mail to the lead, so the guard denies
// AskUserQuestion and ExitPlanMode in a seat.
func TestGuardDeniesSeatDialogs(t *testing.T) {
	root := t.TempDir()
	id := hookIdentity{Handle: "s1", Root: root, Session: "s", AgentDir: filepath.Join(root, "agents", "s1")}
	for _, tool := range []string{"AskUserQuestion", "ExitPlanMode"} {
		v := evaluateGuard(id, guardCall{Tool: tool, Args: map[string]string{"questions": "which branch?"}, CWD: t.TempDir()}, time.Now())
		if v.Verdict != "deny" || v.ReasonCode != "seat_dialog" {
			t.Errorf("%s: %+v", tool, v)
		}
	}
	if v := evaluateGuard(id, guardCall{Tool: "Read", Args: map[string]string{"file_path": filepath.Join(root, "notes.txt")}, CWD: t.TempDir()}, time.Now()); v.Verdict != "allow" {
		t.Fatalf("a read: %+v", v)
	}
}
