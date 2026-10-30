package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/v3/internal/adoptionseam"
	"github.com/omriariav/amq-squad/v3/internal/amqexec"
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
	// The setup probes the claude adapter; the probe answers from a stub on
	// PATH for this one call, so the proof needs no claude on the host (the
	// first v3 CI run's macOS runner had none: "agent claude is not
	// available through a supported adapter probe").
	realAMQCommand(t, binary, parent, withClaudeProbeStub(t, cleanRealAMQEnv()), "setup", "-project-root", parent, "-agents", "claude", "-default-session", "s", "-launcher-preference", "tmux", "-y")

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

// claudeProbeStub answers AMQ v0.116.2's claude adapter probe
// (internal/launch/adapter_claude.go, ClaudeAdapter.Capabilities): the
// executable found on PATH, `claude --version` whose first field is the
// provider version, and `claude --help` naming both identity flags,
// "--session-id <uuid>" and "--resume [value]". Anything else is refused:
// the stub is never a runnable agent.
const claudeProbeStub = `#!/bin/sh
case "$1" in
--version) echo '2.1.0 (Claude Code)' ;;
--help)
	echo 'Usage: claude [options] [command] [prompt]'
	echo '  --session-id <uuid>  Use a specific session ID for the conversation'
	echo '  -r, --resume [value]  Resume a conversation by session ID'
	;;
*) echo 'claude probe stub: answers --version and --help only' >&2; exit 2 ;;
esac
`

// withClaudeProbeStub is env with a private directory holding the claude
// probe stub first on PATH.
func withClaudeProbeStub(t *testing.T, env []string) []string {
	t.Helper()
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "claude"), []byte(claudeProbeStub), 0o700); err != nil {
		t.Fatal(err)
	}
	path := dir
	out := make([]string, 0, len(env)+1)
	for _, kv := range env {
		if v, ok := strings.CutPrefix(kv, "PATH="); ok {
			if v != "" {
				path += string(os.PathListSeparator) + v
			}
			continue
		}
		out = append(out, kv)
	}
	return append(out, "PATH="+path)
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
