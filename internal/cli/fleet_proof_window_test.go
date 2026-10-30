package cli

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/fleet"
	"github.com/omriariav/amq-squad/v3/internal/seatlaunch"
)

// 2b-2 follow-up on 4-3's roster launcher: a one-shot start (the CLI verb)
// keeps the proof socket open until the token window ends before it
// returns; a running operator session's start returns at once.
func TestOneShotStartWaitsOutTheProofWindow(t *testing.T) {
	t.Setenv("HOME", fleetTempDir(t))
	bin := fleetTempDir(t)
	if err := os.WriteFile(filepath.Join(bin, "amq"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	repo := fleetTempDir(t)
	req := fleet.LaunchRequest{ProjectPath: repo, Root: filepath.Join(repo, ".agent-mail", "app"), Session: "app", SquadYAML: adapterSquadYAML}
	up := seatlaunch.Result{Stage: "applied", Seats: []seatlaunch.SeatState{{Handle: "lead", Up: true}, {Handle: "dev", Up: true}}}
	start := func(longRunning bool) (returned chan error, windowEnd chan struct{}, waited chan struct{}) {
		returned, windowEnd, waited = make(chan error, 1), make(chan struct{}), make(chan struct{}, 1)
		l := seatRoster{
			launch: func(context.Context, seatlaunch.Squad, seatlaunch.Options) (seatlaunch.Result, error) { return up, nil },
			waitWindow: func(seatlaunch.Result) {
				waited <- struct{}{}
				<-windowEnd // the socket's token window
			},
		}
		r := req
		r.LongRunning = longRunning
		go func() { returned <- l.Launch(context.Background(), r) }()
		return returned, windowEnd, waited
	}

	returned, windowEnd, waited := start(false)
	select {
	case <-waited:
	case err := <-returned:
		t.Fatalf("a one-shot start returned without waiting for the proof window: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("a one-shot start never waited for the proof window")
	}
	select {
	case err := <-returned:
		t.Fatalf("a one-shot start returned while the proof window was open: %v", err)
	case <-time.After(200 * time.Millisecond):
	}
	close(windowEnd)
	if err := <-returned; err != nil {
		t.Fatalf("one-shot start after the window: %v", err)
	}

	returned, windowEnd, waited = start(true)
	defer close(windowEnd)
	select {
	case err := <-returned:
		if err != nil {
			t.Fatalf("session start: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a running session's start waited for the proof window")
	}
	select {
	case <-waited:
		t.Fatal("a running session's start waited for the proof window")
	default:
	}
}
