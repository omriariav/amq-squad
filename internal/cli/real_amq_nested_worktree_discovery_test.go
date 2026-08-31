package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/v2/internal/adoptionseam"
	"github.com/omriariav/amq-squad/v2/internal/amqexec"
)

// TestNestedWorktreeRootDiscoveryOnAMQ074 is gh#768's named acceptance test.
// It is the real-AMQ-gated companion to the source-level proof recorded in
// docs/amq-0.75.0-adoption-verdict.md (porting v0.74.0's own
// nested_worktree_discovery_test.go onto a v0.73.0 checkout of amq fails 2 of
// its 7 subtests there): here the CLI binary itself, at or above the new
// adoption floor, resolves a nested git worktree's own root instead of
// silently adopting the parent's live queue.
//
// This does NOT exercise adoptionseam.Prepare (which never calls amq CLI
// discovery at all -- gh#734's whole point) and it does not, and must not,
// change ErrEmptyBaseRoot's unconditional refusal. It only proves the fact
// that justifies BaseRootSeamStatus's "belt_and_braces" value: upstream has
// independently closed the discovery gap our own seam never relied on
// staying open.
func TestNestedWorktreeRootDiscoveryOnAMQ074(t *testing.T) {
	binary := strings.TrimSpace(os.Getenv("AMQ_SQUAD_REAL_AMQ"))
	if binary == "" {
		t.Skip("set AMQ_SQUAD_REAL_AMQ to a real amq >= v0.74.0 binary to run this proof")
	}
	info, err := os.Stat(binary)
	if err != nil || info.IsDir() || info.Mode()&0o111 == 0 {
		t.Fatalf("AMQ_SQUAD_REAL_AMQ %q is unavailable or not executable: %v", binary, err)
	}
	version := strings.TrimSpace(realAMQCommand(t, binary, t.TempDir(), nil, "version"))
	if !semverMeetsStableFloor(strings.TrimPrefix(version, "v"), strings.TrimPrefix(adoptionseam.AdoptionFloorAMQVersion, "v")) {
		t.Skipf("real amq %s is below the adoption floor %s; this proof is only meaningful at or above it", version, adoptionseam.AdoptionFloorAMQVersion)
	}
	t.Logf("real AMQ binary=%s version=%s", binary, version)

	if adoptionseam.BaseRootSeamStatus != "belt_and_braces" {
		t.Fatalf("adoptionseam.BaseRootSeamStatus = %q, want %q now that the discovery gap it documents is closed at this floor", adoptionseam.BaseRootSeamStatus, "belt_and_braces")
	}

	scratch := t.TempDir()
	parent := filepath.Join(scratch, "livebase")
	nested := filepath.Join(scratch, "task-checkout")
	if err := os.Mkdir(parent, 0o755); err != nil {
		t.Fatal(err)
	}
	runGitForNestedWorktreeTest(t, parent, "init", "-q")
	runGitForNestedWorktreeTest(t, parent, "commit", "-q", "--allow-empty", "-m", "init")
	realAMQInitAgents(t, binary, parent, filepath.Join(parent, ".agent-mail"), "lead", "worker")
	realAMQCommand(t, binary, parent, cleanRealAMQEnv(), "setup", "-project-root", parent, "-agents", "claude", "-default-session", "s", "-launcher-preference", "tmux", "-y")

	runGitForNestedWorktreeTest(t, parent, "worktree", "add", "-q", "--detach", nested, "HEAD")
	t.Cleanup(func() { runGitForNestedWorktreeTest(t, parent, "worktree", "remove", "-f", nested) })

	if _, err := os.Stat(filepath.Join(nested, ".amqrc")); !os.IsNotExist(err) {
		t.Fatalf("fixture nested worktree unexpectedly already has its own .amqrc: %v", err)
	}

	out, envErr := realAMQTryCommand(binary, nested, cleanRealAMQEnv(), "env", "--json")
	if envErr == nil {
		t.Fatalf("amq env from inside the nested worktree unexpectedly succeeded (should refuse rather than adopt the parent's live root):\n%s", out)
	}
	refusal := envErr.Error()
	lower := strings.ToLower(refusal)
	if !strings.Contains(lower, "worktree") {
		t.Fatalf("nested-worktree refusal did not name the worktree ceiling as the reason:\n%s", refusal)
	}
	if strings.Contains(refusal, parent) {
		t.Fatalf("nested-worktree refusal output leaked the parent's live root path %q:\n%s", parent, refusal)
	}
}

func cleanRealAMQEnv() []string {
	return amqexec.NoUpdateCheckEnv(envWithoutAMQIdentity(os.Environ()))
}

func runGitForNestedWorktreeTest(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}
