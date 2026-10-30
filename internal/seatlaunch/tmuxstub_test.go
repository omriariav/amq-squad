//go:build unix

package seatlaunch

import (
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"testing"
)

// TestMain puts a refusing tmux first on PATH for every row of this package
// (3.0.1 t16, Codex finding 6 and the reviewer's note): the stub logs its
// arguments and exits non-zero, so no row touches the real tmux server, and
// a row whose path still reaches tmux fails the package run loudly instead
// of passing by accident on a host that has tmux. A row that brings its own
// tmux puts it before this one; the live rows (AMQ_SQUAD_LIVE=1) run
// against the real tmux and are left alone.
func TestMain(m *testing.M) {
	if os.Getenv("AMQ_SQUAD_LIVE") == "1" {
		os.Exit(m.Run())
	}
	dir, err := os.MkdirTemp("", "amq-squad-tmux-stub-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	log := filepath.Join(dir, "calls.log")
	// `tmux -V` (AMQ's launcher detection, a version probe that never
	// reaches a server) is answered; anything else is refused and logged.
	script := "#!/bin/sh\nif [ \"$*\" = \"-V\" ]; then echo 'tmux 3.5a'; exit 0; fi\nprintf '%s\\n' \"$*\" >> '" + log + "'\necho 'tmux stub: this test reached tmux' >&2\nexit 7\n"
	if err := os.WriteFile(filepath.Join(dir, "tmux"), []byte(script), 0o700); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	os.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	// HOME too (round 7, Codex 5): a successful launch reads the owner's
	// grant through the process HOME (grants.Load, ~/.amq-squad/grants.yaml);
	// no row reads the real one.
	home := filepath.Join(dir, "home")
	if err := os.MkdirAll(home, 0o700); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	os.Setenv("HOME", home)
	tmuxStubLog = log
	code := m.Run()
	if calls, _ := os.ReadFile(log); len(calls) > 0 {
		fmt.Fprintf(os.Stderr, "FAIL: a seatlaunch row reached tmux (the refusing stub was called):\n%s", calls)
		code = 1
	}
	os.RemoveAll(dir)
	os.Exit(code)
}

// tmuxStubLog is the stub's call log ("" for the live rows).
var tmuxStubLog string

// noTmuxCalls fails a row that reached the stub (a row-level check beside
// TestMain's package-level one).
func noTmuxCalls(t *testing.T) {
	t.Helper()
	if tmuxStubLog == "" {
		return
	}
	if calls, _ := os.ReadFile(tmuxStubLog); len(calls) > 0 {
		t.Errorf("this package's rows reached tmux:\n%s", calls)
	}
}

// t16 round 7 (Codex 5): every row of this package runs with HOME in a
// temp dir, never the account's real home (read from the passwd entry, not
// the environment), so a launch's grant read (~/.amq-squad/grants.yaml)
// never reaches the owner's. Reverted (the TestMain leaving HOME), HOME is
// the real one.
func TestRowsRunUnderATempHome(t *testing.T) {
	if os.Getenv("AMQ_SQUAD_LIVE") == "1" {
		t.Skip("the live rows use the real home")
	}
	u, err := user.Current()
	if err != nil || u.HomeDir == "" {
		t.Skip("no passwd home to compare with")
	}
	home := os.Getenv("HOME")
	if home == "" || filepath.Clean(home) == filepath.Clean(u.HomeDir) || strings.HasPrefix(filepath.Clean(home)+"/", filepath.Clean(u.HomeDir)+"/.amq-squad") {
		t.Fatalf("HOME is %q, the real home %q", home, u.HomeDir)
	}
}
