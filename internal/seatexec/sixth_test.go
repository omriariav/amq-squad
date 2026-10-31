//go:build unix

package seatexec

import (
	"testing"

	"github.com/omriariav/amq-squad/v3/internal/registry"
)

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
