//go:build unix

package cli

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/v3/internal/flock"
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

// U7 through the verb: a partial failure lists what remains and exits 3.
func TestUninstallVerbPartialFailureExitsNonZero(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("a read-only directory does not stop root")
	}
	_, root := uninstallHost(t)
	x := filepath.Join(root, "extensions", "amq-squad")
	if err := os.MkdirAll(filepath.Join(x, "scheduler"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(x, 0o500); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(x, 0o700)
	var err error
	out, _, _ := captureOutput(t, func() error {
		err = Run([]string{"uninstall", "--root", root, "--write"}, "v-test")
		return nil
	})
	if ExitCode(err) != ExitPartial || !strings.Contains(out, "what remains") {
		t.Errorf("want exit %d and a report of what remains, got %v:\n%s", ExitPartial, err, out)
	}
}

// A busy refusal and a symlink refusal exit with the explicit system code.
func TestUninstallVerbRefusalsExitWithTheSystemCode(t *testing.T) {
	_, root := uninstallHost(t)
	sd := filepath.Join(root, "extensions", "amq-squad", "scheduler")
	if err := os.MkdirAll(sd, 0o700); err != nil {
		t.Fatal(err)
	}
	lock, ok, err := flock.TryExclusive(filepath.Join(sd, "run.lock"), true)
	if err != nil || !ok {
		t.Fatalf("hold the lock: %v", err)
	}
	var busyErr error
	captureOutput(t, func() error { busyErr = Run([]string{"uninstall", "--root", root, "--write"}, "v-test"); return nil })
	lock.Close()
	var ce *codedExitError
	if !errors.As(busyErr, &ce) || ce.code != ExitSystem || !strings.Contains(busyErr.Error(), "uninstall_busy") {
		t.Errorf("busy: want an explicit system exit, got %v", busyErr)
	}
	tasks := filepath.Join(root, "extensions", "amq-squad", "tasks")
	os.RemoveAll(tasks)
	if err := os.Symlink(t.TempDir(), tasks); err != nil {
		t.Fatal(err)
	}
	var linkErr error
	captureOutput(t, func() error { linkErr = Run([]string{"uninstall", "--root", root, "--write"}, "v-test"); return nil })
	if !errors.As(linkErr, &ce) || ce.code != ExitSystem || !strings.Contains(linkErr.Error(), "uninstall_refused_symlink") {
		t.Errorf("symlink: want an explicit system exit, got %v", linkErr)
	}
}
