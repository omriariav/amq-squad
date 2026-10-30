//go:build unix

package seatlaunch

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/avivsinai/agent-message-queue/launchapi"

	"github.com/omriariav/amq-squad/v3/internal/launch"
	"github.com/omriariav/amq-squad/v3/internal/procinfo"
	"github.com/omriariav/amq-squad/v3/internal/scheduler"
	"github.com/omriariav/amq-squad/v3/internal/task"
)

// launcherFixture runs Launch against a fake amq whose Apply "starts" the
// seat: a prover child process sends the seat's proof over the launch's
// socket, as seat-exec does, and stays up as the seat.
type launcherFixture struct {
	t          *testing.T
	q          Squad
	l          *Launcher
	mu         sync.Mutex
	events     []scheduler.Event
	gate       chan struct{} // when set, Apply waits for it
	publish    bool          // Apply publishes the exec block (false: the seat never shows up)
	ignoreTerm bool          // the seat ignores SIGTERM
	blindAll   bool          // after its proof, the seat's start stamp is unreadable to the close path
	unblind    func()
	provers    []*prover
	procs      []*exec.Cmd
	paneGone   bool
	fake       *fakeAMQ
}

func newLauncherFixture(t *testing.T, applyOutcome string) *launcherFixture {
	f := &launcherFixture{t: t, q: testSquad(t), publish: true}
	fake := &fakeAMQ{t: t, prepare: preparedResult(), apply: launchapi.ApplyResultV1{ResultVersion: 1, Outcome: applyOutcome, Disposition: "committed",
		Observations: []launchapi.ParticipantObservationV1{{Handle: "cto", Runnable: true, Resource: "tmux:v1:pane:%7"}}}}
	if applyOutcome != "applied" {
		fake.apply.Disposition = "not_applied"
	}
	f.fake = fake
	fake.onApply = func() {
		if f.gate != nil {
			<-f.gate
		}
		if applyOutcome != "applied" || !f.publish {
			return
		}
		p := prove(t, f.q, "cto", f.ignoreTerm, nil)
		if !p.reply.OK {
			t.Errorf("the fixture's proof was refused: %+v", p.reply)
		}
		f.provers = append(f.provers, p)
		if f.blindAll {
			f.unblind = blindStamp(p.pid)
		}
	}
	t.Cleanup(func() {
		for _, c := range f.procs {
			c.Process.Kill()
		}
	})
	// Close every request's proof server before TempDir is removed (3.0.1
	// t16, round 5): a server left open runs out its window on its watcher
	// goroutine and marks its unused token in launch/<handle> after the body
	// returned, racing the removal ("directory not empty"). Closing twice is
	// safe; the launcher's wg is not awaited (a failed row with its gate
	// still closed would hang the cleanup).
	t.Cleanup(func() {
		f.l.mu.Lock()
		var srvs []*proofServer
		for _, r := range f.l.reqs {
			if r.proof != nil {
				srvs = append(srvs, r.proof)
			}
		}
		f.l.mu.Unlock()
		for _, s := range srvs {
			s.close()
		}
	})
	opts := launchOpts(t, fake, map[string]string{"sha256:a1": "trust_exact_subject"})
	opts.Wait = 300 * time.Millisecond
	f.l = &Launcher{
		Options: opts, Root: f.q.SessionRoot(), CloseWait: 2 * time.Second,
		Plan: func(req scheduler.SeatRequest) (Squad, error) {
			q := f.q
			q.Seats = q.Seats[:1]
			return q, nil
		},
		Post:     func(ev scheduler.Event) { f.mu.Lock(); f.events = append(f.events, ev); f.mu.Unlock() },
		PaneGone: func(string) (bool, bool) { f.mu.Lock(); defer f.mu.Unlock(); return f.paneGone, true },
	}
	return f
}

func (f *launcherFixture) startProc() int {
	cmd := exec.Command("sleep", "30")
	if err := cmd.Start(); err != nil {
		f.t.Fatal(err)
	}
	f.procs = append(f.procs, cmd)
	go cmd.Wait()
	return cmd.Process.Pid
}

var poolA = task.Pool{Runtime: "claude", Model: "sonnet", Effort: "medium"}

func TestLauncherPostsSeatUpWithTheRequestID(t *testing.T) {
	f := newLauncherFixture(t, "applied")
	req := scheduler.SeatRequest{ID: "seat-req-inc1-1", Pool: poolA, TaskID: "t1"}
	if err := f.l.RequestSeat(req); err != nil {
		t.Fatal(err)
	}
	f.l.Wait()
	if len(f.events) != 1 || f.events[0].Kind != scheduler.SeatUp || f.events[0].RequestID != req.ID || f.events[0].Handle != "cto" || f.events[0].Pool != poolA {
		t.Fatalf("events %+v", f.events)
	}
	staged, _ := os.ReadFile(filepath.Join(launch.StagedDir(f.q.SessionRoot(), "cto"), launch.FileName))
	if !strings.Contains(string(staged), `"request_id": "seat-req-inc1-1"`) {
		t.Fatalf("the request id is not in the launch record:\n%s", staged)
	}
	if err := f.l.RequestSeat(req); err == nil {
		t.Fatal("a repeated request id was accepted")
	}
	if err := f.l.RequestSeat(scheduler.SeatRequest{Pool: poolA}); err == nil {
		t.Fatal("a request without an id was accepted")
	}
}

func TestLauncherCancelInFlightClosesTheSeatAndPostsNothing(t *testing.T) {
	f := newLauncherFixture(t, "applied")
	f.gate = make(chan struct{})
	req := scheduler.SeatRequest{ID: "seat-req-inc1-2", Pool: poolA}
	if err := f.l.RequestSeat(req); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	if err := f.l.CancelSeatRequest(req); err == nil {
		t.Fatal("cancel of an in-flight launch was confirmed")
	}
	close(f.gate)
	f.l.Wait()
	if len(f.events) != 0 {
		t.Fatalf("a cancelled request posted %+v", f.events)
	}
	if pid := f.provers[0].pid; procinfo.Alive(pid) {
		t.Fatalf("the cancelled seat (pid %d) still runs", pid)
	}
	if err := f.l.CancelSeatRequest(req); err != nil {
		t.Fatalf("after the close the cancel is confirmed: %v", err)
	}
}

func TestLauncherCancelConfirmsFailedAndUnknownRequests(t *testing.T) {
	f := newLauncherFixture(t, "action_required")
	req := scheduler.SeatRequest{ID: "seat-req-inc1-3", Pool: poolA}
	if err := f.l.RequestSeat(req); err != nil {
		t.Fatal(err)
	}
	f.l.Wait()
	if len(f.events) != 0 {
		t.Fatalf("a failed launch posted %+v", f.events)
	}
	if err := f.l.CancelSeatRequest(req); err != nil {
		t.Fatalf("failed launch: %v", err)
	}
	if err := f.l.CancelSeatRequest(scheduler.SeatRequest{ID: "seat-req-other-9"}); err != nil {
		t.Fatalf("unknown id: %v", err)
	}
}

// Fix 1 item 1: Apply placed the seat but its exec record did not arrive in
// time. The provider may still start, so the cancel is not confirmed until
// the pane is gone; a seat that starts meanwhile is closed first.
func TestLauncherCancelOfAPlacedSeatWaitsForTermination(t *testing.T) {
	f := newLauncherFixture(t, "applied")
	f.publish = false
	req := scheduler.SeatRequest{ID: "seat-req-inc1-5", Pool: poolA}
	if err := f.l.RequestSeat(req); err != nil {
		t.Fatal(err)
	}
	f.l.Wait()
	if err := f.l.CancelSeatRequest(req); err == nil {
		t.Fatal("a placed seat with a live pane was confirmed cancelled")
	}
	// A seat that starts now cannot prove (the launch's socket is closed),
	// so seat-exec refuses it; the pane is the evidence.
	if err := f.l.CancelSeatRequest(req); err == nil {
		t.Fatal("still unconfirmed while the pane is present")
	}
	// Another placed seat whose pane disappears: confirmed without a close.
	g := newLauncherFixture(t, "applied")
	g.publish = false
	req2 := scheduler.SeatRequest{ID: "seat-req-inc1-6", Pool: poolA}
	g.l.RequestSeat(req2)
	g.l.Wait()
	g.mu.Lock()
	g.paneGone = true
	g.mu.Unlock()
	if err := g.l.CancelSeatRequest(req2); err != nil {
		t.Fatalf("pane gone: %v", err)
	}
}

// Fix 1 item 2: a close that fails keeps the request closing (not
// cancelled) and the error reaches the scheduler's retry.
func TestLauncherCancelledOnlyAfterAConfirmedClose(t *testing.T) {
	f := newLauncherFixture(t, "applied")
	f.gate = make(chan struct{})
	f.blindAll = true // the seat's stamp is unreadable: the close cannot be confirmed
	f.l.CloseWait = 300 * time.Millisecond
	req := scheduler.SeatRequest{ID: "seat-req-inc1-7", Pool: poolA}
	f.l.RequestSeat(req)
	time.Sleep(50 * time.Millisecond)
	f.l.CancelSeatRequest(req)
	close(f.gate)
	f.l.Wait()
	pid := f.provers[0].pid
	if err := f.l.CancelSeatRequest(req); err == nil || !strings.Contains(err.Error(), "unavailable") {
		t.Fatalf("an unconfirmed close was reported cancelled: %v", err)
	}
	if !procinfo.Alive(pid) {
		t.Fatal("a seat whose stamp could not be read was signalled")
	}
	// The stamp is readable again: the retry closes and confirms.
	f.unblind()
	if err := f.l.CancelSeatRequest(req); err != nil || procinfo.Alive(pid) {
		t.Fatalf("retry: %v, alive %v", err, procinfo.Alive(pid))
	}
}

// Fix 1 item 3: a pid whose birth time differs from the recorded one is a
// reused pid and is never signalled; a missing birth time fails closed.
func TestCloseSeatSignalsOnlyTheRecordedProcess(t *testing.T) {
	f := newLauncherFixture(t, "applied")
	pid := f.startProc()
	born, ok := procinfo.StartToken(pid)
	if !ok {
		t.Skip("process birth time unavailable on this host")
	}
	hold := func(start string) {
		f.l.mu.Lock()
		f.l.ident = map[string]seatIdentity{"cto": {PID: pid, Start: start}}
		f.l.mu.Unlock()
	}
	hold("darwin-tv:1.1") // another process's stamp: a reused pid
	if err := f.l.CloseSeat("cto", "idle"); err != nil || !procinfo.Alive(pid) {
		t.Fatalf("reused pid: %v, alive %v (must not be signalled)", err, procinfo.Alive(pid))
	}
	hold("")
	if err := f.l.CloseSeat("cto", "idle"); err == nil || !procinfo.Alive(pid) {
		t.Fatalf("no start stamp: %v, alive %v (must fail closed)", err, procinfo.Alive(pid))
	}
	hold(born)
	if err := f.l.CloseSeat("cto", "idle"); err != nil || procinfo.Alive(pid) {
		t.Fatalf("exact birth time: %v, alive %v", err, procinfo.Alive(pid))
	}
	if err := f.l.CloseSeat("cto", "idle"); err != nil {
		t.Fatalf("closing a closed seat: %v", err)
	}
	if err := f.l.CloseSeat("nobody", "idle"); err == nil {
		t.Fatal("closing a seat the launcher holds no identity for was confirmed")
	}
}

func publishExecRaw(t *testing.T, q Squad, handle string, ex *launch.Exec) {
	t.Helper()
	if err := launch.WriteUnderRecordLock(filepath.Join(q.SessionRoot(), "agents", handle), launch.Record{Handle: handle, Exec: ex}); err != nil {
		t.Fatal(err)
	}
}

// 3.0.1 t16 round 5 (reviewer ruling, a defect): the expiry mark of a
// launch's proof window is a compare-and-set on the staged record's launch
// id. Request A's apply is not applied, so A fails and its cancel succeeds
// at once while its proof server stays open for the 60 s window; 30 s later
// request B for the same handle restages launch/cto with its own launch id
// and token; when A's window ends, A's watcher must not mark B's token
// expired. The record keeps B's launch, request and token, and B's seat
// proves after A's window (seat-exec reads that token). Reverted (the mark
// rewrites whatever is staged), B's token reads expired and its seat is
// refused proof_expired.
func TestExpiredWindowNeverMarksALaterLaunchsToken(t *testing.T) {
	f := newLauncherFixture(t, "action_required")
	var mu sync.Mutex
	now := time.Now()
	clock := func() time.Time { mu.Lock(); defer mu.Unlock(); return now }
	advance := func(d time.Duration) { mu.Lock(); now = now.Add(d); mu.Unlock() }
	f.l.Options.ProofClock, f.l.Clock = clock, clock
	a := scheduler.SeatRequest{ID: "seat-req-a", Pool: poolA}
	if err := f.l.RequestSeat(a); err != nil {
		t.Fatal(err)
	}
	f.l.Wait()
	if err := f.l.CancelSeatRequest(a); err != nil {
		t.Fatalf("A not applied: its cancel is confirmed at once: %v", err)
	}
	advance(30 * time.Second)
	f.fake.apply.Outcome, f.fake.apply.Disposition = "applied", "committed"
	b := scheduler.SeatRequest{ID: "seat-req-b", Pool: poolA}
	if err := f.l.RequestSeat(b); err != nil {
		t.Fatal(err)
	}
	f.l.Wait()
	staged := func() launch.Record {
		raw, err := os.ReadFile(filepath.Join(launch.StagedDir(f.q.SessionRoot(), "cto"), launch.FileName))
		if err != nil {
			t.Fatal(err)
		}
		rec, err := launch.Decode(raw)
		if err != nil || rec.Proof == nil {
			t.Fatalf("staged record: %v", err)
		}
		return rec
	}
	before := staged()
	f.l.mu.Lock()
	launchA, launchB := f.l.reqs[a.ID].launchID, f.l.reqs[b.ID].launchID
	f.l.mu.Unlock()
	if before.LaunchID != launchB || before.RequestID != b.ID || launchA == launchB {
		t.Fatalf("B's restaged record: launch %s request %s (A %s, B %s)", before.LaunchID, before.RequestID, launchA, launchB)
	}
	advance(31 * time.Second) // A's window ends; B's has 29 s left
	// Wait for A's watcher to have closed A's server (its expiry marks are
	// written before done closes), bounded (Codex on round 5, item 3).
	f.l.mu.Lock()
	srvA := f.l.reqs[a.ID].proof
	f.l.mu.Unlock()
	select {
	case <-srvA.done:
	case <-time.After(5 * time.Second):
		t.Fatal("A's window watcher never closed A's proof server")
	}
	after := staged()
	if after.LaunchID != launchB || after.RequestID != b.ID || after.Proof.Token != before.Proof.Token {
		t.Fatalf("A's window rewrote B's record: token %q (was %q), launch %s, request %s", after.Proof.Token, before.Proof.Token, after.LaunchID, after.RequestID)
	}
	if p := prove(t, f.q, "cto", false, nil); !p.reply.OK {
		t.Fatalf("B's seat was refused after A's window: %+v", p.reply)
	}
	log, _ := os.ReadFile(filepath.Join(f.q.SessionRoot(), "extensions", "amq-squad", "launch", "proof-rejects.log"))
	if !strings.Contains(string(log), `"code":"token_mark_stale"`) || !strings.Contains(string(log), launchA) || !strings.Contains(string(log), launchB) {
		t.Fatalf("the skipped mark is not logged with both launches:\n%s", log)
	}
}
