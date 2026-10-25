package seatlaunch

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/avivsinai/agent-message-queue/launchapi"

	"github.com/omriariav/amq-squad/v3/internal/claudeseat"
	"github.com/omriariav/amq-squad/v3/internal/launch"
	"github.com/omriariav/amq-squad/v3/internal/registry"
)

// testPin is the tests' registry pin of claude (the path the fake lookPath
// names; nothing execs it).
var testPin = registry.Pin{Name: "claude", Path: "/usr/local/bin/claude", SHA256: "test"}

func testClaudePin(Seat) (registry.Pin, error) { return testPin, nil }

// probeAsLaunched is the tests' mode probe: the pinned Claude Code runs the
// seat in the launch's mode.
func probeAsLaunched(_ context.Context, _ registry.Pin, _, mode string) (claudeseat.Probe, error) {
	return claudeseat.Probe{Mode: mode, Version: "test"}, nil
}

// Phase 5 re-pass of 4-2, P1-2: the launch probes the effective permission
// mode of the pinned Claude Code for the seat's settings, and refuses the
// launch, before any seat applies, when it is not the launch's mode.
func TestLaunchRefusesAClaudeSeatWhoseModeIsForced(t *testing.T) {
	q := testSquad(t)
	f := &fakeAMQ{t: t, prepare: preparedResult(), apply: launchapi.ApplyResultV1{ResultVersion: 1, Outcome: "applied", Disposition: "committed"}}
	o := launchOpts(t, f, map[string]string{"sha256:a1": "trust_exact_subject"})
	var probed []string
	o.ProbeMode = func(_ context.Context, _ registry.Pin, dir, mode string) (claudeseat.Probe, error) {
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
	// The seat's own settings carry no env block (the withdrawn scrub) and
	// keep their hooks (disableAllHooks is the probe copy's only).
	raw, rerr := os.ReadFile(filepath.Join(probed[0], "settings.json"))
	if rerr != nil || strings.Contains(string(raw), `"env"`) || strings.Contains(string(raw), "SUBPROCESS_ENV_SCRUB") || strings.Contains(string(raw), "disableAllHooks") {
		t.Fatalf("the seat's settings.json: %v\n%s", rerr, raw)
	}
}

// The fix round's probe ruling: the probes run before any proof ticket is
// minted (a slow probe never eats a ticket's window), and seats whose
// settings are identical are probed once per launch.
func TestProbeRunsBeforeTicketsAndOncePerSeatKind(t *testing.T) {
	q := testSquad(t)
	extra := q.Seats[0]
	extra.Handle, extra.Worktree, extra.Branch = "qa", filepath.Join(filepath.Dir(extra.Worktree), "wt-qa"), "seat/qa"
	q.Seats = append(q.Seats, extra)
	f := &fakeAMQ{t: t, prepare: preparedResult(), apply: launchapi.ApplyResultV1{ResultVersion: 1, Outcome: "applied", Disposition: "committed"}}
	o := launchOpts(t, f, map[string]string{"sha256:a1": "trust_exact_subject"})
	o.Wait = 50 * time.Millisecond
	calls := 0
	var probeEnd time.Time
	o.ProbeMode = func(_ context.Context, _ registry.Pin, dir, mode string) (claudeseat.Probe, error) {
		calls++
		time.Sleep(300 * time.Millisecond)
		probeEnd = time.Now()
		return claudeseat.Probe{Mode: mode, Version: "test"}, nil
	}
	res, err := Launch(context.Background(), q, o)
	if err != nil {
		t.Fatal(err)
	}
	defer res.proof.close()
	if calls != 1 {
		t.Fatalf("two Claude seats with identical settings were probed %d times", calls)
	}
	for _, h := range []string{"cto", "qa"} {
		raw, err := os.ReadFile(filepath.Join(launch.StagedDir(q.SessionRoot(), h), launch.FileName))
		if err != nil {
			t.Fatal(err)
		}
		rec, err := launch.Decode(raw)
		if err != nil || rec.Proof == nil || rec.Proof.IssuedAt.Before(probeEnd) {
			t.Fatalf("seat %s: the ticket was minted at %v, before the probe ended at %v (%v)", h, rec.Proof, probeEnd, err)
		}
	}
}

// The probe runs the registry's pinned claude, never the PATH claude, and a
// claude with no pin refuses the probe.
func TestProbeRunsThePinnedClaudeNeverPath(t *testing.T) {
	base, _ := filepath.EvalSymlinks(t.TempDir())
	home := filepath.Join(base, "home")
	os.MkdirAll(home, 0o700)
	pathDir := filepath.Join(base, "path")
	os.MkdirAll(pathDir, 0o700)
	marker := filepath.Join(base, "path-claude-ran")
	os.WriteFile(filepath.Join(pathDir, "claude"), []byte("#!/bin/sh\ntouch "+marker+"\n"), 0o700)
	t.Setenv("PATH", pathDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	dir := filepath.Join(base, "cfg", "launch-1")
	if _, err := claudeseat.Write(dir, "dontAsk", filepath.Join(base, "wt"), time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := registryClaudePin(home); err == nil || !strings.Contains(err.Error(), registry.PinMissing) {
		t.Fatalf("no claude pin: %v", err)
	}
	pinned := filepath.Join(base, "pinned-claude")
	os.WriteFile(pinned, []byte("#!/bin/sh\necho '{\"type\":\"system\",\"subtype\":\"init\",\"permissionMode\":\"dontAsk\",\"claude_code_version\":\"pinned\"}'\n"), 0o700)
	sum, _ := registry.FileSHA256(pinned)
	if err := registry.Write(registry.Path(home), registry.Registry{Schema: 1, Pins: []registry.Pin{{Name: "claude", Path: pinned, SHA256: sum}}}); err != nil {
		t.Fatal(err)
	}
	pin, err := registryClaudePin(home)
	if err != nil {
		t.Fatal(err)
	}
	p, err := probePinned(context.Background(), pin, dir, "dontAsk")
	if err != nil || p.Version != "pinned" || p.Mode != "dontAsk" {
		t.Fatalf("the pinned probe: %+v %v", p, err)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("the PATH claude ran")
	}
}
