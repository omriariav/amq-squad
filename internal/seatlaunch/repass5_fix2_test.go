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
	"github.com/omriariav/amq-squad/v3/internal/registry"
)

// The narrow pass's HIGH: a Claude seat's executable is the registry's
// claude pin. Compile names the pin as the seat's executable, and a
// Seat.Provider that is not the pinned file (device and inode) is refused
// provider_not_pinned, so the probe never certifies one claude while the
// seat runs another.
func TestClaudeProviderMustBeThePin(t *testing.T) {
	base, _ := filepath.EvalSymlinks(t.TempDir())
	a, b := filepath.Join(base, "a", "claude"), filepath.Join(base, "b", "claude")
	os.MkdirAll(filepath.Dir(a), 0o700)
	os.MkdirAll(filepath.Dir(b), 0o700)
	os.WriteFile(a, []byte("#!/bin/sh\n# A\n"), 0o700)
	os.WriteFile(b, []byte("#!/bin/sh\n# B ignores the permission mode\n"), 0o700)
	q := testSquad(t)
	q.ClaudePins = map[string]registry.Pin{"cto": {Name: "claude", Path: a, SHA256: "x"}}
	q.Seats[0].Provider = b
	if _, err := Compile(q, "", lookPath, time.Unix(1, 0)); err == nil || !strings.Contains(err.Error(), ReasonProviderNotPinned) {
		t.Fatalf("provider B with pin A: %v", err)
	}
	os.MkdirAll(filepath.Join(base, "link"), 0o700)
	link := filepath.Join(base, "link", "claude")
	if err := os.Link(a, link); err != nil {
		t.Fatal(err)
	}
	q.Seats[0].Provider = link // the same file as the pin
	c, err := Compile(q, "", lookPath, time.Unix(1, 0))
	if err != nil {
		t.Fatalf("the pinned file under another name: %v", err)
	}
	if got := c.Request.Intent.Participants[1].Executable; got != a {
		t.Fatalf("the seat's executable is %q, not the pin %q", got, a)
	}
	q.Seats[0].Provider = ""
	q.ClaudePins = map[string]registry.Pin{"cto": {Name: "claude", Path: filepath.Join(base, "2.1.310"), SHA256: "x"}}
	if _, err := Compile(q, "", lookPath, time.Unix(1, 0)); err == nil || !strings.Contains(err.Error(), "file name") {
		t.Fatalf("a pin whose file is not named claude: %v", err)
	}
	q.ClaudePins = nil
	if _, err := Compile(q, "", lookPath, time.Unix(1, 0)); err == nil || !strings.Contains(err.Error(), registry.PinMissing) {
		t.Fatalf("no pin: %v", err)
	}
}

// The narrow pass's item 5, at the launch: two Claude seats with the same
// settings and different pin digests are probed twice.
func TestTwoPinsAreProbedTwice(t *testing.T) {
	q := testSquad(t)
	extra := q.Seats[0]
	extra.Handle, extra.Worktree, extra.Branch = "qa", filepath.Join(filepath.Dir(extra.Worktree), "wt-qa"), "seat/qa"
	q.Seats = append(q.Seats, extra)
	f := &fakeAMQ{t: t, prepare: preparedResult(), apply: launchapi.ApplyResultV1{ResultVersion: 1, Outcome: "applied", Disposition: "committed"}}
	o := launchOpts(t, f, map[string]string{"sha256:a1": "trust_exact_subject"})
	o.Wait = 50 * time.Millisecond
	o.PinFor = func(s Seat) (registry.Pin, error) {
		return registry.Pin{Name: "claude", Path: testPin.Path, SHA256: "sha-" + s.Handle}, nil
	}
	var pins []string
	o.ProbeMode = func(_ context.Context, pin registry.Pin, dir, mode string) (claudeseat.Probe, error) {
		pins = append(pins, pin.SHA256)
		return claudeseat.Probe{Mode: mode, Version: "test"}, nil
	}
	res, err := Launch(context.Background(), q, o)
	if err != nil {
		t.Fatal(err)
	}
	defer res.proof.close()
	if len(pins) != 2 {
		t.Fatalf("two pin digests were probed %d times: %v", len(pins), pins)
	}
}
