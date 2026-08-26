package adoptionseam

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/avivsinai/agent-message-queue/launchapi"
	"github.com/omriariav/amq-squad/v2/internal/launchintent"
)

// hashTree returns a stable digest of every regular file under root (path
// relative to root, plus content), so the test can prove a directory tree
// is byte-identical before and after a call without depending on mtimes.
func hashTree(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	if _, err := os.Stat(root); os.IsNotExist(err) {
		return out
	}
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		out[rel] = string(data)
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	return out
}

// TestAdoptionSeamTargetsNestedWorktreeWithoutParentEscape reproduces the
// exact hazard gh#734 exists to close: a task worktree nested INSIDE a
// parent repo, where AMQ's own upward .amqrc discovery would otherwise
// retarget writes into the parent's live base root. This seam takes no
// discovery path at all -- every root is the explicit one the caller
// resolved -- so the parent's .agent-mail tree must be byte-identical
// before and after, and the resolved roots must live under the nested
// project's own explicit base, never the parent's.
func TestAdoptionSeamTargetsNestedWorktreeWithoutParentEscape(t *testing.T) {
	// Keep the trust store this seam opens fully inside the test's temp
	// dir; DefaultLaunchStateDir honors XDG_STATE_HOME. Otherwise Prepare
	// would touch the real machine's global AMQ state directory.
	stateHome := t.TempDir()
	t.Setenv("XDG_STATE_HOME", stateHome)

	parentRepo := t.TempDir()
	parentAgentMail := filepath.Join(parentRepo, ".agent-mail")
	if err := os.MkdirAll(filepath.Join(parentAgentMail, "live-profile", "live-session"), 0o755); err != nil {
		t.Fatalf("seed parent .agent-mail: %v", err)
	}
	liveSentinel := filepath.Join(parentAgentMail, "live-profile", "live-session", "sentinel.txt")
	if err := os.WriteFile(liveSentinel, []byte("parent live session, must not move\n"), 0o644); err != nil {
		t.Fatalf("seed parent sentinel: %v", err)
	}

	// A task worktree nested INSIDE the parent repo -- our real layout
	// (amq-squad-wt-*) and the default hazard, not an edge case.
	nestedProject := filepath.Join(parentRepo, "task-worktree")
	if err := os.MkdirAll(nestedProject, 0o755); err != nil {
		t.Fatalf("create nested project: %v", err)
	}
	nestedBaseRoot := filepath.Join(nestedProject, ".agent-mail", "squad-v2-30-0", "v2-30-0")

	before := hashTree(t, parentAgentMail)

	intent := launchintent.Input{
		Operator: launchintent.OperatorFacts{Handle: "user"},
		Seats: []launchintent.SeatFacts{
			{
				Handle:      "senior-dev",
				Executable:  "/usr/bin/claude",
				Args:        []string{"--permission-mode", "auto"},
				Cwd:         launchintent.SeatCWD{Kind: launchapi.WorkingDirectoryAbsolute, Path: nestedProject},
				RequireWake: true,
			},
		},
		Target: launchintent.TargetFacts{
			ProjectRoot: nestedProject,
			BaseRoot:    nestedBaseRoot,
			SessionRoot: nestedProject,
			Session:     "v2-30-0",
		},
	}

	prepared, err := Prepare(context.Background(), PrepareInput{
		Intent:   intent,
		Launcher: "tmux",
		Env:      []string{"AM_ROOT=" + filepath.Join(parentRepo, ".agent-mail", "live-profile", "live-session"), "PATH=/usr/bin"},
	})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}

	after := hashTree(t, parentAgentMail)
	if len(before) != len(after) {
		t.Fatalf("parent .agent-mail tree changed: before had %d files, after has %d", len(before), len(after))
	}
	for path, contentBefore := range before {
		contentAfter, ok := after[path]
		if !ok {
			t.Fatalf("parent .agent-mail file %s disappeared", path)
		}
		if contentAfter != contentBefore {
			t.Fatalf("parent .agent-mail file %s changed content", path)
		}
	}
	for path := range after {
		if _, ok := before[path]; !ok {
			t.Fatalf("parent .agent-mail gained a new file %s -- discovery escaped into the parent repo", path)
		}
	}

	resolvedBase := prepared.Request.Target.BaseRoot
	resolvedProject := prepared.Request.Target.ProjectRoot
	if resolvedBase != nestedBaseRoot {
		t.Fatalf("resolved base_root = %q, want the explicit nested base %q (no upward discovery)", resolvedBase, nestedBaseRoot)
	}
	if resolvedProject != nestedProject {
		t.Fatalf("resolved project_root = %q, want the explicit nested project %q", resolvedProject, nestedProject)
	}
	if rel, err := filepath.Rel(nestedProject, resolvedBase); err != nil || len(rel) >= 2 && rel[:2] == ".." {
		t.Fatalf("resolved base_root %q is not under the nested project %q", resolvedBase, nestedProject)
	}
	for _, entry := range prepared.Env {
		if len(entry) >= 8 && entry[:8] == "AM_ROOT=" {
			t.Fatalf("Prepared.Env still carries inherited AM_ROOT: %v", prepared.Env)
		}
	}
}
