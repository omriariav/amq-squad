package seatlaunch

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/avivsinai/agent-message-queue/launchapi"

	"github.com/omriariav/amq-squad/v3/internal/claudeseat"
)

// probeAsLaunched is the tests' mode probe: the installed Claude Code runs
// the seat in the launch's mode.
func probeAsLaunched(_ context.Context, _, _, _, _, mode string) (claudeseat.Probe, error) {
	return claudeseat.Probe{Mode: mode, Version: "test"}, nil
}

// Phase 5 re-pass of 4-2, P1-2: the launch probes the effective permission
// mode of the installed Claude Code for the seat's settings, and refuses the
// launch, before any seat applies, when it is not the launch's mode.
func TestLaunchRefusesAClaudeSeatWhoseModeIsForced(t *testing.T) {
	q := testSquad(t)
	f := &fakeAMQ{t: t, prepare: preparedResult(), apply: launchapi.ApplyResultV1{ResultVersion: 1, Outcome: "applied", Disposition: "committed"}}
	o := launchOpts(t, f, map[string]string{"sha256:a1": "trust_exact_subject"})
	var probed []string
	o.ProbeMode = func(_ context.Context, claude, dir, worktree, home, mode string) (claudeseat.Probe, error) {
		probed = append(probed, dir)
		return claudeseat.Probe{Mode: "default", Version: "2.1.310"}, nil
	}
	_, err := Launch(context.Background(), q, o)
	if err == nil || !strings.Contains(err.Error(), claudeseat.ModeUnverified) || f.applied != nil {
		t.Fatalf("a forced mode: %v (applied %v)", err, f.applied != nil)
	}
	if len(probed) != 1 {
		t.Fatalf("probed %d Claude seats, want 1", len(probed))
	}
	// The seat's own settings carry no env block (the withdrawn scrub).
	raw, rerr := os.ReadFile(filepath.Join(probed[0], "settings.json"))
	if rerr != nil || strings.Contains(string(raw), `"env"`) || strings.Contains(string(raw), "SUBPROCESS_ENV_SCRUB") {
		t.Fatalf("the seat's settings.json carries an env block: %v", rerr)
	}
}
