//go:build unix

package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func uninstallHost(t *testing.T) (home, root string) {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	home = filepath.Join(base, "home")
	root = filepath.Join(base, "proj", ".agent-mail", "sess")
	for _, p := range []string{filepath.Join(root, "extensions", "amq-squad", "tasks"), filepath.Join(home, ".amq-squad")} {
		if err := os.MkdirAll(p, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(home, ".amq-squad", "registry.yaml"), []byte("r: 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", "")
	t.Setenv("AM_ROOT", "")
	return home, root
}

// U1 through the verb: no flag, a dry run; --purge and --owner-files without
// --write are still a dry run.
func TestUninstallVerbIsADryRunUnlessWriteIsGiven(t *testing.T) {
	home, root := uninstallHost(t)
	for _, args := range [][]string{{"--root", root}, {"--root", root, "--purge", "--owner-files"}} {
		out, _, err := captureOutput(t, func() error { return Run(append([]string{"uninstall"}, args...), "v-test") })
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out, "dry run") || !strings.Contains(out, "claude plugin uninstall") {
			t.Errorf("not a dry run report:\n%s", out)
		}
		for _, p := range []string{filepath.Join(root, "extensions", "amq-squad", "tasks"), filepath.Join(home, ".amq-squad", "registry.yaml")} {
			if _, err := os.Stat(p); err != nil {
				t.Errorf("%s was removed by a dry run", p)
			}
		}
	}
}

// U1, U7 through the verb: --write removes, owner files only with
// --owner-files, a second run has nothing to do; a missing root is a usage error.
func TestUninstallVerbWriteIsIdempotent(t *testing.T) {
	home, root := uninstallHost(t)
	run := func(args ...string) string {
		out, _, err := captureOutput(t, func() error { return Run(append([]string{"uninstall", "--root", root}, args...), "v-test") })
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	run("--write")
	if _, err := os.Stat(filepath.Join(root, "extensions", "amq-squad", "tasks")); err == nil {
		t.Error("--write left the session state")
	}
	if _, err := os.Stat(filepath.Join(home, ".amq-squad", "registry.yaml")); err != nil {
		t.Error("--write removed an owner file without --owner-files")
	}
	run("--write", "--owner-files")
	if _, err := os.Stat(filepath.Join(home, ".amq-squad", "registry.yaml")); err == nil {
		t.Error("--owner-files left registry.yaml")
	}
	if out := run("--write", "--owner-files"); !strings.Contains(out, "nothing to do") {
		t.Errorf("the second run did not report nothing to do:\n%s", out)
	}
	if err := Run([]string{"uninstall"}, "v-test"); ExitCode(err) != ExitUser {
		t.Errorf("no root: want a usage error, got %v", err)
	}
}
