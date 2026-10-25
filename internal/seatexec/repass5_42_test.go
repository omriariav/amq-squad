//go:build unix

package seatexec

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/omriariav/amq-squad/v3/internal/claudeseat"
)

// Phase 5 re-pass of 4-2, P1-1: a worktree settings file whose env block
// names AMQ_SQUAD_BIN, an AM_* or AMQ_SQUAD_* variable or a CLAUDE_CODE_*
// switch refuses the seat (Claude's settings env wins over the launch's
// value), and the worktree's settings files are in the config dir's digest,
// so one written after the launch is a changed config.
func TestWorktreeSettingsEnvAndDigest(t *testing.T) {
	for _, c := range []struct{ file, key string }{
		{"settings.json", "AMQ_SQUAD_BIN"},
		{"settings.local.json", "AM_ME"},
		{"settings.json", "AMQ_SQUAD_ROLE_PATH"},
		{"settings.local.json", "CLAUDE_CODE_SUBPROCESS_ENV_SCRUB"},
	} {
		f := newFixture(t, "claude")
		if err := os.MkdirAll(filepath.Join(f.wt, ".claude"), 0o700); err != nil {
			t.Fatal(err)
		}
		write(t, filepath.Join(f.wt, ".claude", c.file), `{"env":{"`+c.key+`":"/tmp/allow"}}`, 0o600)
		f.stage()
		if err := f.run(Options{}); refusalCode(err) != claudeseat.SettingsEnvForbidden || f.execPath != "" {
			t.Errorf("%s env %s: %v (exec %q)", c.file, c.key, err, f.execPath)
		}
	}
	// An env entry that names nothing of the seat's is fine.
	f := newFixture(t, "claude")
	os.MkdirAll(filepath.Join(f.wt, ".claude"), 0o700)
	write(t, filepath.Join(f.wt, ".claude", "settings.json"), `{"env":{"GOFLAGS":"-mod=mod"}}`, 0o600)
	f.stage()
	if err := f.run(Options{}); err != nil {
		t.Fatalf("a harmless env entry refused: %v", err)
	}
	// A worktree settings file that appears after the launch changes the
	// digest: the seat is refused, not run with it.
	g := newFixture(t, "claude")
	g.stage()
	os.MkdirAll(filepath.Join(g.wt, ".claude"), 0o700)
	write(t, filepath.Join(g.wt, ".claude", "settings.local.json"), `{}`, 0o600)
	if err := g.run(Options{}); refusalCode(err) != ClaudePermissionMismatch || g.execPath != "" {
		t.Fatalf("a worktree settings file written after the launch: %v (exec %q)", err, g.execPath)
	}
}
