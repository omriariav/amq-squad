//go:build unix

package seatexec

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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

// Plan 4.3 / R36 ruling: a Claude seat never runs with the hook absent. A
// missing, disabled or project-scope plugin, or one whose hooks.json does
// not run the amq-squad hook on PreToolUse, refuses claude_hooks_missing
// before exec.
func TestClaudeSeatRefusesWithoutTheUserScopeHook(t *testing.T) {
	cases := map[string]func(f *fixture){
		"not installed": func(f *fixture) {
			os.Remove(filepath.Join(f.home, ".claude", "plugins", "installed_plugins.json"))
		},
		"not enabled": func(f *fixture) {
			write(t, filepath.Join(f.home, ".claude", "settings.json"), `{"enabledPlugins":{"amq-squad@amq-squad":false}}`, 0o600)
		},
		"project scope only": func(f *fixture) {
			installPlugin(t, f.home, "project")
		},
		"hooks.json without the guard": func(f *fixture) {
			dir := installPlugin(t, f.home, "user")
			write(t, filepath.Join(dir, "hooks", "hooks.json"), `{"hooks":{"PostToolUse":[{"hooks":[{"command":"sh amq-squad-hook.sh post-tool"}]}]}}`, 0o600)
		},
		"hook script missing": func(f *fixture) {
			dir := installPlugin(t, f.home, "user")
			os.Remove(filepath.Join(dir, "hooks", "amq-squad-hook.sh"))
		},
	}
	for name, breakIt := range cases {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t, "claude")
			f.stage()
			breakIt(f)
			err := f.run(Options{})
			if refusalCode(err) != ClaudeHooksMissing || f.execPath != "" {
				t.Fatalf("want %s and no exec, got %v (exec %q)", ClaudeHooksMissing, err, f.execPath)
			}
		})
	}
	// CLAUDE_CONFIG_DIR moves the config: the plugin is looked up there.
	f := newFixture(t, "claude")
	f.stage()
	altHome := filepath.Join(f.base, "alt")
	installPlugin(t, altHome, "user")
	write(t, filepath.Join(altHome, ".claude", "settings.json"), ownerSettings, 0o600)
	if err := os.RemoveAll(filepath.Join(f.home, ".claude", "plugins")); err != nil {
		t.Fatal(err)
	}
	if err := f.run(Options{}); refusalCode(err) != ClaudeHooksMissing {
		t.Fatalf("the plugin removed from HOME: %v", err)
	}
	if err := f.run(Options{Env: f.env("CLAUDE_CONFIG_DIR=" + filepath.Join(altHome, ".claude"))}); err != nil {
		t.Fatalf("CLAUDE_CONFIG_DIR not honored: %v", err)
	}
}

// The permission mode the launch set is recorded in the launch record and
// checked with the hook as one unit: a record without it, an argv with
// another mode, a second mode or a permission bypass is refused.
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
