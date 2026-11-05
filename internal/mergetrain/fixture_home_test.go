package mergetrain

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// The fixture's git runs under its own HOME (standing rule 18; the plan
// reviewer on the 3.1.1 notes): the home the test process runs under, here
// a stand-in for the owner's, carries a git ignore file that hides every
// .txt file, which GIT_CONFIG_GLOBAL=/dev/null does not cover (git reads
// $XDG_CONFIG_HOME/git/ignore, else $HOME/.config/git/ignore, as the
// default core.excludesFile). The fixture's git does not see it: a new
// .txt file is untracked. Reverted (the fixture inheriting HOME), the file
// is hidden and this fails.
func TestTheFixtureGitDoesNotReadTheRunningHome(t *testing.T) {
	standIn := t.TempDir()
	if err := os.MkdirAll(filepath.Join(standIn, ".config", "git"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(standIn, ".config", "git", "ignore"), []byte("*.txt\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", standIn)
	t.Setenv("XDG_CONFIG_HOME", "")
	// the stand-in works: a git run under it hides the file
	repo := t.TempDir()
	if out, err := exec.Command("git", "init", "-q", repo).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}
	if err := os.WriteFile(filepath.Join(repo, "a.txt"), []byte("x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	plain := exec.Command("git", "-C", repo, "status", "--porcelain")
	plain.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
	if out, err := plain.CombinedOutput(); err != nil || len(out) != 0 {
		t.Fatalf("the stand-in home does not hide the file (out %q, err %v): the row would prove nothing", out, err)
	}
	// the fixture's git: its own home, the file untracked
	out, err := gitOut(repo, "status", "--porcelain")
	if err != nil || out != "?? a.txt" {
		t.Fatalf("the fixture's git read the running home: status %q, err %v", out, err)
	}
	if fixtureHome == "" || fixtureHome == standIn {
		t.Fatalf("fixture home %q", fixtureHome)
	}
}
