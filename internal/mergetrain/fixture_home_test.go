package mergetrain

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// The test binary's git runs under its own HOME (standing rule 18; the plan
// reviewer on the 3.1.1 notes): isolateGitHome, which TestMain calls once
// for every mergetrain test, moves HOME off the home the binary started
// under. Here a stand-in for the owner's home carries a git ignore file that
// hides every .txt file, which GIT_CONFIG_GLOBAL=/dev/null does not cover
// (git reads $XDG_CONFIG_HOME/git/ignore, else $HOME/.config/git/ignore, as
// the default core.excludesFile). Under the stand-in a plain git hides a new
// .txt file; after isolateGitHome, a plain git (as production's git
// patch-id runs) and the fixtures' gitOut show it untracked. Reverted (the
// HOME line removed from isolateGitHome), the file stays hidden and this
// fails.
func TestTheTestBinarysGitDoesNotReadTheRunningHome(t *testing.T) {
	standIn := t.TempDir()
	if err := os.MkdirAll(filepath.Join(standIn, ".config", "git"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(standIn, ".config", "git", "ignore"), []byte("*.txt\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// the variables isolateGitHome sets, each restored after the test
	t.Setenv("HOME", standIn)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	_ = os.Unsetenv("XDG_CONFIG_HOME")
	repo := t.TempDir()
	if out, err := exec.Command("git", "init", "-q", repo).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}
	if err := os.WriteFile(filepath.Join(repo, "a.txt"), []byte("x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	status := func() string {
		out, err := exec.Command("git", "-C", repo, "status", "--porcelain").CombinedOutput()
		if err != nil {
			t.Fatalf("git status: %v %s", err, out)
		}
		return string(out)
	}
	// the stand-in works: under it the file is hidden
	if got := status(); got != "" {
		t.Fatalf("the stand-in home does not hide the file (status %q): the row would prove nothing", got)
	}
	isolateGitHome(t.TempDir())
	if got := status(); got != "?? a.txt\n" {
		t.Fatalf("a git under the test binary's environment read the running home: status %q", got)
	}
	if out, err := gitOut(repo, "status", "--porcelain"); err != nil || out != "?? a.txt" {
		t.Fatalf("the fixtures' git read the running home: status %q, err %v", out, err)
	}
}
