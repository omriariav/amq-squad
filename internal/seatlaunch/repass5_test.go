//go:build unix

package seatlaunch

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/avivsinai/agent-message-queue/launchapi"

	"github.com/omriariav/amq-squad/v3/internal/launch"
	"github.com/omriariav/amq-squad/v3/internal/procinfo"
	"github.com/omriariav/amq-squad/v3/internal/scheduler"
)

// Phase 5 re-pass P1-3: two Apply observations for one handle (a live pane
// and a nonexistent one), or one for a handle the request does not know,
// leave the request unresolved (apply_ambiguous): no pane check confirms its
// cancel; a seat that proves is closed by its proved identity and only that
// confirmed close releases it.
func TestAmbiguousApplyObservationsKeepTheRequestUnresolved(t *testing.T) {
	for _, obs := range [][]launchapi.ParticipantObservationV1{
		{{Handle: "cto", Runnable: true, Resource: "tmux:v1:pane:%7"}, {Handle: "cto", Runnable: true, Resource: "tmux:v1:pane:%99"}},
		{{Handle: "cto", Runnable: true, Resource: "tmux:v1:pane:%99"}, {Handle: "other", Runnable: true, Resource: "tmux:v1:pane:%7"}},
	} {
		f := newLauncherFixture(t, "applied")
		f.publish = false
		f.fake.apply.Observations = obs
		// %99 does not exist; %7 is live.
		f.l.PaneGone = func(resource string) (bool, bool) { return resource == "tmux:v1:pane:%99", true }
		req := scheduler.SeatRequest{ID: "seat-req-amb-" + obs[1].Handle, Pool: poolA}
		if err := f.l.RequestSeat(req); err != nil {
			t.Fatal(err)
		}
		f.l.Wait()
		err := f.l.CancelSeatRequest(req)
		if err == nil || !strings.Contains(err.Error(), "apply_ambiguous") {
			t.Fatalf("observations %v: cancel confirmed by a pane check: %v", obs, err)
		}
		// The live pane proves late (the socket is open for the token window):
		// the cancel closes that seat and is confirmed only then.
		p := prove(t, f.q, "cto", false, nil)
		if !p.reply.OK {
			t.Fatalf("late proof refused: %+v", p.reply)
		}
		if err := f.l.CancelSeatRequest(req); err != nil || procinfo.Alive(p.pid) {
			t.Fatalf("after the proof: %v, seat alive %v", err, procinfo.Alive(p.pid))
		}
		// No SeatUp; the unresolved launch is posted once as a failure
		// (apply_ambiguous), 3.0.1 t17.
		if len(f.events) != 1 || f.events[0].Kind != scheduler.SeatLaunchFailed || f.events[0].ReasonCode != "apply_ambiguous" {
			t.Fatalf("an ambiguous request posted %+v", f.events)
		}
	}
}

// Phase 5 re-pass P2-6: the proof's tmux lookup has a deadline (a hung tmux
// is proof_origin_unknown), and it runs outside the server mutex, so a stuck
// lookup stalls neither another seat's proof nor the server's state.
func TestPaneLookupIsBoundedAndOutsideTheMutex(t *testing.T) {
	t.Run("deadline", func(t *testing.T) {
		dir := t.TempDir()
		// A tmux that never answers (its sleep also holds the output pipe).
		if err := os.WriteFile(filepath.Join(dir, "tmux"), []byte("#!/bin/sh\nsleep 30\n"), 0o700); err != nil {
			t.Fatal(err)
		}
		t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
		start := time.Now()
		_, _, ok := tmuxPaneShell("cto", nil)
		if ok || time.Since(start) > 6*time.Second {
			t.Fatalf("hung tmux: ok %v after %s", ok, time.Since(start).Round(time.Second))
		}
	})
	t.Run("outside the mutex", func(t *testing.T) {
		q := shortSquad(t)
		release := make(chan struct{})
		entered := make(chan struct{})
		var once sync.Once
		o := Options{Now: time.Now, PaneShell: func(handle string) (int, string, bool) {
			if handle == "cto" {
				once.Do(func() { close(entered) })
				<-release
			}
			return paneOf(os.Getpid())(handle)
		}, PaneID: func(int) string { return "" }}
		srv, err := startProofServer(q, newLaunchID(), o)
		if err != nil {
			t.Fatal(err)
		}
		defer srv.close()
		a := srv.ticket("cto", "seat-req-a")
		msg := launch.ProofMessage{LaunchID: srv.launchID, RequestID: "seat-req-a", Handle: "cto", Token: a.Token, Root: q.SessionRoot(), Me: "cto", Session: q.Session, PID: os.Getpid()}
		msg.Start, _ = procinfo.StartToken(os.Getpid())
		judged := make(chan struct{})
		go func() { srv.judge(msg, os.Getpid(), true); close(judged) }()
		<-entered
		done := make(chan struct{})
		go func() {
			srv.state("cto")
			srv.judge(launch.ProofMessage{LaunchID: srv.launchID, Handle: "dev", Token: "x"}, 0, false)
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			close(release)
			<-judged
			t.Fatal("the server mutex is held across the pane lookup")
		}
		close(release)
		<-judged
	})
}
