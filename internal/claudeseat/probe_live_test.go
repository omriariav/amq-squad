package claudeseat

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// Phase 5 re-pass of 4-2, P1-2, the probe row against the installed Claude
// Code (AMQ_SQUAD_LIVE=1; it starts claude without a credential and reads
// only its init event, before any model request): the seat's settings run in
// dontAsk, and the withdrawn scrub setting would force default.
func TestModeProbeOnTheInstalledClaude(t *testing.T) {
	if os.Getenv("AMQ_SQUAD_LIVE") != "1" {
		t.Skip("live probe: set AMQ_SQUAD_LIVE=1 (runs the installed claude)")
	}
	claude, err := exec.LookPath("claude")
	if err != nil {
		t.Skip("no claude on PATH")
	}
	base, _ := filepath.EvalSymlinks(t.TempDir())
	wt := filepath.Join(base, "wt")
	os.MkdirAll(wt, 0o700)
	dir := filepath.Join(base, "cfg", "launch-1")
	if _, err := Write(dir, "dontAsk", wt, time.Now()); err != nil {
		t.Fatal(err)
	}
	p, err := ProbeMode(context.Background(), claude, dir, wt, base, "dontAsk")
	if err != nil || p.Mode != "dontAsk" {
		t.Fatalf("the seat's settings on Claude Code %s: mode %q, %v", p.Version, p.Mode, err)
	}
	t.Logf("Claude Code %s runs the seat's settings in %s", p.Version, p.Mode)
	forced := filepath.Join(base, "forced")
	os.MkdirAll(forced, 0o700)
	os.WriteFile(filepath.Join(forced, "settings.json"), []byte(`{"permissions":{"defaultMode":"dontAsk","allow":[]},"env":{"CLAUDE_CODE_SUBPROCESS_ENV_SCRUB":"1"}}`), 0o600)
	if p, err := ProbeMode(context.Background(), claude, forced, wt, base, "dontAsk"); err != nil || p.Mode == "dontAsk" {
		t.Logf("the scrub setting no longer forces another mode on %s (%q, %v)", p.Version, p.Mode, err)
	}
}
