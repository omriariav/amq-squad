//go:build unix

package seatexec

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/omriariav/amq-squad/v3/internal/claudeseat"
	"github.com/omriariav/amq-squad/v3/internal/registry"
)

// 3.0.1 t6, reviewer addition (a): the claude seat-exec is about to exec is
// compared with the registry pin's sha256 (registry.OpenPinned, doctor's pin
// check) before any config check. A binary upgraded in place at the pin's
// path is refused claude_pin_mismatch, even when the seat's config dir is
// also wrong, and nothing is exec'd.
func TestAnUpgradedClaudeIsRefusedBeforeTheConfigChecks(t *testing.T) {
	f := newFixture(t, "claude")
	f.stage()
	write(t, filepath.Join(f.bin, "claude"), "#!/bin/sh\n# Claude Code, upgraded in place\nexit 0\n", 0o700)
	os.Remove(filepath.Join(claudeseat.Dir(f.root, "cto", f.rec.LaunchID), ".claude.json"))
	if err := f.run(Options{}); refusalCode(err) != ClaudePinMismatch || f.execPath != "" {
		t.Fatalf("want %s and no exec, got %v (exec %q)", ClaudePinMismatch, err, f.execPath)
	}
}

// The reviewer's ruling on the flag cache: every Claude seat's exec'd
// environment carries CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1 (the
// registry's Claude seat environment), whatever the caller had, so its flag
// client is off and never writes the flag cache into the seat's config dir.
func TestClaudeSeatRunsWithNonessentialTrafficOff(t *testing.T) {
	if len(registry.ClaudeSeatEnv) == 0 {
		t.Fatal("the registry names no Claude seat environment")
	}
	f := newFixture(t, "claude")
	f.stage()
	if err := f.run(Options{Env: f.env("CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=")}); err != nil {
		t.Fatal(err)
	}
	if got := Getenv(f.execEnv, "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC"); got != "1" {
		t.Fatalf("CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC = %q in the exec'd environment, want 1", got)
	}
	g := newFixture(t, "codex")
	g.stage()
	if err := g.run(Options{}); err != nil {
		t.Fatal(err)
	}
	if got := Getenv(g.execEnv, "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC"); got != "" {
		t.Fatalf("a Codex seat carries it: %q", got)
	}
}
