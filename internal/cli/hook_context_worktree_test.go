package cli

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/launch"
)

// The seat context names the seat's own working tree (2b-2 milestone B, the
// R12 live finding: a worker that saw only the task's project_path ran its
// tests in the repository root): from the published launch record, else the
// core's staged one.
func TestContextNamesTheSeatWorktree(t *testing.T) {
	root := t.TempDir()
	id := hookIdentity{Handle: "pi-1", Root: root, Session: "s", AgentDir: filepath.Join(root, "agents", "pi-1")}
	if err := os.MkdirAll(id.AgentDir, 0o700); err != nil {
		t.Fatal(err)
	}
	staged := launch.StagedDir(root, "pi-1")
	if err := os.MkdirAll(staged, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := launch.WriteFile(filepath.Join(staged, launch.FileName), launch.Record{Handle: "pi-1", Worktree: "/w/wt-pi-1"}); err != nil {
		t.Fatal(err)
	}
	if d := buildHookContext(id, "", time.Now()); d.Worktree != "/w/wt-pi-1" {
		t.Fatalf("from the staged record: %q", d.Worktree)
	}
	if err := launch.WriteUnderRecordLock(id.AgentDir, launch.Record{Handle: "pi-1", Worktree: "/w/published"}); err != nil {
		t.Fatal(err)
	}
	if d := buildHookContext(id, "", time.Now()); d.Worktree != "/w/published" {
		t.Fatalf("from the published record: %q", d.Worktree)
	}
}
