package seatlaunch

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/avivsinai/agent-message-queue/launchapi"

	"github.com/omriariav/amq-squad/v3/internal/launch"
	"github.com/omriariav/amq-squad/v3/internal/registry"
	"github.com/omriariav/amq-squad/v3/internal/seatcommit"
)

// Brief 31-1, rows 2 and 3 (the launch's half): a squad with a Claude seat
// launches with no registry at all under the launch's HOME (no claude pin
// to read) and no mode probe to run; the Claude seat's executable is claude
// resolved like codex (LookPath), and its staged record carries no pin.
func TestClaudeSeatLaunchesWithNoPinAndNoProbe(t *testing.T) {
	q := testSquad(t)
	f := &fakeAMQ{t: t, prepare: preparedResult(), apply: launchapi.ApplyResultV1{ResultVersion: 1, Outcome: "applied", Disposition: "committed"}}
	o := launchOpts(t, f, nil)
	o.Wait = 50 * time.Millisecond
	if _, err := os.Stat(registry.Path(o.Home)); !os.IsNotExist(err) {
		t.Fatalf("the launch's HOME has a registry: %v", err)
	}
	res, err := Launch(context.Background(), q, o)
	if err != nil || res.Stage != "applied" {
		t.Fatalf("launch: %s %v", res.Stage, err)
	}
	res.proof.close()
	want, _ := lookPath("claude")
	var exe string
	for _, p := range f.applied.Prepare.Intent.Participants {
		if p.Handle == "cto" {
			exe = p.Executable
		}
	}
	if exe != want {
		t.Fatalf("the Claude seat's executable is %q, want %q (claude on PATH)", exe, want)
	}
	raw, _ := os.ReadFile(filepath.Join(launch.StagedDir(q.SessionRoot(), "cto"), launch.FileName))
	if strings.Contains(string(raw), "claude_pin") || strings.Contains(string(raw), "claude_config_sha256") {
		t.Fatalf("the staged record carries a pin or a config digest:\n%s", raw)
	}
}

// Brief 31-1, row 10 (the launch's half): a target whose https push needs
// the pinned gh, with no gh pin, no longer stops the launch (LaunchTarget
// and CheckTarget moved into the commit step, which refuses that push as
// before: seatcommit's push-target rows).
func TestLaunchWithNoGhPinApplies(t *testing.T) {
	q := testSquad(t)
	f := &fakeAMQ{t: t, prepare: preparedResult(), apply: launchapi.ApplyResultV1{ResultVersion: 1, Outcome: "applied", Disposition: "committed"}}
	o := launchOpts(t, f, nil)
	o.Wait = 50 * time.Millisecond
	o.Target = &seatcommit.Target{Repository: "o/r", BaseBranch: "main", PushURL: "https://github.com/o/r.git"}
	if err := seatcommit.CheckTarget(*o.Target); err == nil {
		t.Fatal("the fixture target passes CheckTarget; the row proves nothing")
	}
	res, err := Launch(context.Background(), q, o)
	if err != nil || res.Stage != "applied" || f.applied == nil {
		t.Fatalf("a launch with no gh pin: %s %v", res.Stage, err)
	}
	res.proof.close()
}

// Brief 31-1, row 3 on its own: a launch with a Claude seat runs no mode
// probe. The claude the launch resolves is a script that leaves a mark when
// it runs, and the launch's HOME carries a valid registry pin of it, so the
// old pin step passes and the old probe would exec it (`claude -p … stream
// json`). The launch applies and the mark is absent: nothing executed claude.
func TestClaudeSeatLaunchRunsNoModeProbe(t *testing.T) {
	q := testSquad(t)
	f := &fakeAMQ{t: t, prepare: preparedResult(), apply: launchapi.ApplyResultV1{ResultVersion: 1, Outcome: "applied", Disposition: "committed"}}
	o := launchOpts(t, f, nil)
	o.Wait = 50 * time.Millisecond
	bin := filepath.Join(t.TempDir(), "bin")
	mark := filepath.Join(t.TempDir(), "claude-ran")
	claude := filepath.Join(bin, "claude")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(claude, []byte("#!/bin/sh\ntouch '"+mark+"'\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	sum, err := registry.FileSHA256(claude)
	if err != nil {
		t.Fatal(err)
	}
	if err := registry.Write(registry.Path(o.Home), registry.Registry{Schema: 1, Pins: []registry.Pin{{Name: "claude", Path: claude, SHA256: sum}}}); err != nil {
		t.Fatal(err)
	}
	o.LookPath = func(name string) (string, error) {
		if name == "claude" {
			return claude, nil
		}
		return lookPath(name)
	}
	res, err := Launch(context.Background(), q, o)
	if err != nil || res.Stage != "applied" {
		t.Fatalf("launch: %s %v", res.Stage, err)
	}
	res.proof.close()
	if _, err := os.Stat(mark); !os.IsNotExist(err) {
		t.Fatalf("the launch executed claude (a mode probe): %v", err)
	}
}
