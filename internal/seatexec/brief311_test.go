package seatexec

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/omriariav/amq-squad/v3/internal/claudeseat"
	"github.com/omriariav/amq-squad/v3/internal/registry"
)

// Brief 31-1, row 2 (seat-exec's half): a Claude seat runs the claude its
// argv names, resolved like codex (providerPath), with no registry pin at
// all; a claude replaced in place after the launch execs too (no
// tool_unpinned, pin_changed_since_probe or claude_pin_mismatch).
func TestClaudeSeatExecsWithoutARegistryPin(t *testing.T) {
	f := newFixture(t, "claude")
	if _, err := os.Stat(registry.Path(f.home)); !os.IsNotExist(err) {
		t.Fatalf("the fixture wrote a registry: %v", err)
	}
	f.stage()
	if err := f.run(Options{}); err != nil || f.execPath != filepath.Join(f.bin, "claude") {
		t.Fatalf("a claude with no pin: %v (exec %q)", err, f.execPath)
	}
	g := newFixture(t, "claude")
	g.stage()
	write(t, filepath.Join(g.bin, "claude"), "#!/bin/sh\n# upgraded in place\nexit 0\n", 0o700)
	if err := g.run(Options{}); err != nil || g.execPath == "" {
		t.Fatalf("a claude changed after the launch: %v (exec %q)", err, g.execPath)
	}
}

// Brief 31-1, row 4 (seat-exec's half): the deleted config-dir refusals.
// A Claude seat whose worktree's settings carry allow rules, whose worktree
// CLAUDE.md imports a file outside it, or whose config dir changed after the
// launch wrote it, execs; the first two are status warnings (status's row).
func TestClaudeSeatExecsPastTheDeletedConfigDirRefusals(t *testing.T) {
	for name, setup := range map[string]func(f *fixture){
		"worktree allow rules": func(f *fixture) {
			os.MkdirAll(filepath.Join(f.wt, ".claude"), 0o700)
			write(f.t, filepath.Join(f.wt, ".claude", "settings.json"), `{"permissions":{"allow":["Bash(*)"]}}`, 0o600)
		},
		"an external CLAUDE.md include": func(f *fixture) {
			write(f.t, filepath.Join(f.base, "shared.md"), "shared\n", 0o600)
			write(f.t, filepath.Join(f.wt, "CLAUDE.md"), "@"+filepath.Join(f.base, "shared.md")+"\n", 0o600)
		},
		"a config dir changed after the launch": func(f *fixture) {
			dir := claudeseat.Dir(f.root, "cto", f.rec.LaunchID)
			os.Remove(filepath.Join(dir, "settings.json"))
			write(f.t, filepath.Join(dir, "settings.json"), `{"permissions":{"defaultMode":"dontAsk","allow":["Bash(*)"]}}`, 0o600)
		},
	} {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t, "claude")
			f.stage()
			setup(f)
			if err := f.run(Options{}); err != nil || f.execPath == "" {
				t.Fatalf("refused: %v (exec %q)", err, f.execPath)
			}
		})
	}
}
