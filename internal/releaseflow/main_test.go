package releaseflow

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestMain runs the package's tests without the real HOME and without the
// owner's global or system git config (Codex P2): every git the tests run,
// the fixture's and the fake remote's, inherits this environment.
func TestMain(m *testing.M) {
	home, err := os.MkdirTemp("", "releaseflow-home-")
	if err != nil {
		panic(err)
	}
	for k, v := range map[string]string{"HOME": home, "GIT_CONFIG_GLOBAL": os.DevNull, "GIT_CONFIG_NOSYSTEM": "1"} {
		if err := os.Setenv(k, v); err != nil {
			panic(err)
		}
	}
	code := m.Run()
	_ = os.RemoveAll(home)
	os.Exit(code)
}

func TestTheTestsRunWithoutTheRealHome(t *testing.T) {
	home := os.Getenv("HOME")
	if !strings.HasPrefix(filepath.Base(home), "releaseflow-home-") || os.Getenv("GIT_CONFIG_GLOBAL") != os.DevNull || os.Getenv("GIT_CONFIG_NOSYSTEM") != "1" {
		t.Fatalf("HOME %q GIT_CONFIG_GLOBAL %q GIT_CONFIG_NOSYSTEM %q", home, os.Getenv("GIT_CONFIG_GLOBAL"), os.Getenv("GIT_CONFIG_NOSYSTEM"))
	}
}

// remoteHasIt answers the remote checks yes, so a row exercises the local
// cat-file alone.
type remoteHasIt struct{ *repoExec }

func (remoteHasIt) CommitExists(context.Context, string, string) (bool, error) { return true, nil }
func (remoteHasIt) BranchContains(context.Context, string, string, string) (bool, error) {
	return true, nil
}

// CheckCommit's local cat-file reads neither the global nor the system git
// config: with both pointing at a broken file, the commit still resolves.
func TestCheckCommitReadsNoGlobalOrSystemGitConfig(t *testing.T) {
	f := newFixture(t)
	bad := filepath.Join(t.TempDir(), "gitconfig")
	if err := os.WriteFile(bad, []byte("[core\nthis is not git config\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", bad)
	t.Setenv("GIT_CONFIG_SYSTEM", bad)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "0")
	f.flow.Exec = remoteHasIt{f.exec}
	if err := f.flow.CheckCommit(context.Background(), "owner/fixture", f.commit); err != nil {
		t.Fatalf("the owner's git config reached the check: %v", err)
	}
}
