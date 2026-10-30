package seatexec

import (
	"path/filepath"
	"testing"

	"github.com/omriariav/amq-squad/v3/internal/claudeseat"
)

// 3.0.1 t9 (reviewer condition): the launch admits a Claude seat's wake from
// a scratch copy of its config dir, so seat-exec re-checks the guard deny of
// AskUserQuestion and ExitPlanMode on the real dir before exec when the
// record says WakeRequired. A real dir whose PreToolUse hook runs the guard
// for Bash only (hooksWired still passes, the digest re-recorded) refuses
// with claude_dialog_guard_missing; without WakeRequired it execs.
func TestClaudeWakeRecheckOnTheRealConfigDir(t *testing.T) {
	narrow := func(f *fixture) {
		dir := claudeseat.Dir(f.root, "cto", f.rec.LaunchID)
		matches, _ := filepath.Glob(filepath.Join(dir, "plugins", "cache", "amq-squad", "amq-squad", "*", "hooks", "hooks.json"))
		if len(matches) == 0 {
			t.Fatal("no installed hooks.json")
		}
		for _, m := range matches {
			write(t, m, `{"hooks":{"PreToolUse":[{"matcher":"Bash","hooks":[{"command":"sh amq-squad-hook.sh pre-tool"}]}]}}`, 0o600)
		}
		f.rec.ClaudeConfig, _ = claudeseat.Digest(dir)
	}
	for _, tc := range []struct {
		name         string
		wake, narrow bool
		code         string
	}{
		{"wake admitted, real dir wired", true, false, ""},
		{"wake admitted, real dir not wired", true, true, ClaudeDialogGuardMissing},
		{"no wake, real dir not wired", false, true, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t, "claude")
			f.stage()
			if tc.narrow {
				narrow(f)
			}
			f.rec.WakeRequired = tc.wake
			f.stage()
			err := f.run(Options{})
			if tc.code == "" {
				if err != nil || f.execPath == "" {
					t.Fatalf("want exec, got %v", err)
				}
				return
			}
			if refusalCode(err) != tc.code || f.execPath != "" {
				t.Fatalf("want %s and no exec, got %v (exec %q)", tc.code, err, f.execPath)
			}
		})
	}
}
