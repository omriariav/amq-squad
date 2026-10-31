//go:build unix

package seatexec

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/v3/internal/claudeseat"
)

// 4-2: seat-exec pins AMQ_SQUAD_BIN to the binary that launched the seat, so
// the seat's hooks call that binary, not whatever amq-squad is on PATH.
func TestSeatExecPinsAMQSquadBin(t *testing.T) {
	f := newFixture(t, "claude")
	f.stage()
	if err := f.run(Options{Self: "/opt/amq-squad/bin/amq-squad", Env: f.env("AMQ_SQUAD_BIN=/tmp/other/amq-squad")}); err != nil {
		t.Fatal(err)
	}
	if got := Getenv(f.execEnv, "AMQ_SQUAD_BIN"); got != "/opt/amq-squad/bin/amq-squad" {
		t.Fatalf("AMQ_SQUAD_BIN = %q, want the launching binary", got)
	}
	g := newFixture(t, "claude")
	g.stage()
	if err := g.run(Options{}); err != nil {
		t.Fatal(err)
	}
	self, _ := os.Executable()
	if real, err := filepath.EvalSymlinks(self); err == nil {
		self = real
	}
	if got := Getenv(g.execEnv, "AMQ_SQUAD_BIN"); got != self || !filepath.IsAbs(got) {
		t.Fatalf("default AMQ_SQUAD_BIN = %q, want %q", got, self)
	}
}

// The seat runs with its own CLAUDE_CONFIG_DIR, whatever the caller had; the
// owner's user-scope settings (with an allow rule) are not its config.
func TestClaudeSeatRunsWithItsOwnConfigDir(t *testing.T) {
	f := newFixture(t, "claude")
	f.stage()
	if err := f.run(Options{Env: f.env("CLAUDE_CONFIG_DIR=" + filepath.Join(f.home, ".claude"))}); err != nil {
		t.Fatal(err)
	}
	want := claudeseat.Dir(f.root, "cto", "launch-1")
	if got := Getenv(f.execEnv, "CLAUDE_CONFIG_DIR"); got != want {
		t.Fatalf("CLAUDE_CONFIG_DIR = %q, want the seat's %q", got, want)
	}
	raw, _ := os.ReadFile(filepath.Join(want, "settings.json"))
	if strings.Contains(string(raw), "Bash(echo") || !strings.Contains(string(raw), `"defaultMode": "dontAsk"`) {
		t.Fatalf("the seat's settings carry the owner's rule or not the mode:\n%s", raw)
	}
}

// The permission mode the launch set is recorded in the launch record and
// checked with the hook as one unit: a record without it, an argv with
// another mode, a second mode, a permission bypass or a --settings file is
// refused.
func TestClaudePermissionModeIsRecordedAndChecked(t *testing.T) {
	ok := newFixture(t, "claude")
	ok.stage()
	if err := ok.run(Options{}); err != nil {
		t.Fatal(err)
	}
	pub, err := os.ReadFile(filepath.Join(ok.root, "agents", "cto", "extensions", "io.github.omriariav.amq-squad", "launch.json"))
	if err != nil || !strings.Contains(string(pub), `"permission_mode": "dontAsk"`) {
		t.Fatalf("published record lacks the permission mode: %s %v", pub, err)
	}
	claude := func(f *fixture) string { return filepath.Join(f.bin, "claude") }
	for name, tc := range map[string]struct {
		recorded string
		argv     func(f *fixture) []string
	}{
		"no recorded mode": {"", func(f *fixture) []string { return []string{claude(f), "--permission-mode", "dontAsk"} }},
		"another mode":     {"dontAsk", func(f *fixture) []string { return []string{claude(f), "--permission-mode", "bypassPermissions"} }},
		"no mode in argv":  {"dontAsk", func(f *fixture) []string { return []string{claude(f), "--model", "m"} }},
		"two modes": {"dontAsk", func(f *fixture) []string {
			return []string{claude(f), "--permission-mode", "dontAsk", "--permission-mode=acceptEdits"}
		}},
		"a permission skip": {"dontAsk", func(f *fixture) []string {
			return []string{claude(f), "--permission-mode", "dontAsk", "--dangerously-skip-permissions"}
		}},
		"a settings file": {"dontAsk", func(f *fixture) []string {
			return []string{claude(f), "--permission-mode", "dontAsk", "--settings", "/tmp/x.json"}
		}},
	} {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t, "claude")
			f.rec.PermissionMode = tc.recorded
			f.stage()
			err := f.run(Options{Argv: tc.argv(f)})
			if refusalCode(err) != ClaudePermissionMismatch || f.execPath != "" {
				t.Fatalf("want %s, got %v", ClaudePermissionMismatch, err)
			}
		})
	}
}
