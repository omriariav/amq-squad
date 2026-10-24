//go:build unix

package seatlaunch

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/procinfo"
	"github.com/omriariav/amq-squad/v3/internal/scheduler"
)

// Fix 3 item 1: amq launch --apply that fails after it began (here: no valid
// ApplyResult) may have created the pane, so the request is unresolved, not
// failed: the cancel is not confirmed while the pane is present.
func TestApplyErrorAfterStartIsUnresolved(t *testing.T) {
	f := newLauncherFixture(t, "applied")
	f.publish = false
	broken := &fakeAMQ{t: t, prepare: preparedResult()}
	f.l.Options.RunAMQ = func(ctx context.Context, amq string, args []string, stdin []byte, env []string, dir string) ([]byte, []byte, int, error) {
		if strings.Contains(strings.Join(args, " "), "--apply") {
			return []byte("not json"), []byte("amq: crashed after creating the pane"), 1, nil
		}
		return broken.run(ctx, amq, args, stdin, env, dir)
	}
	req := scheduler.SeatRequest{ID: "seat-req-inc1-11", Pool: poolA}
	f.l.RequestSeat(req)
	f.l.Wait()
	if err := f.l.CancelSeatRequest(req); err == nil {
		t.Fatal("an Apply that failed after it began was confirmed as no seat")
	}
	f.mu.Lock()
	f.paneGone = true
	f.mu.Unlock()
	if err := f.l.CancelSeatRequest(req); err != nil {
		t.Fatalf("pane gone: %v", err)
	}
}

// Fix 3 item 2a: a seat that is up (its SeatUp posted) is confirmed
// cancelled at once, even when its close would fail: the scheduler then
// refuses that SeatUp for the cancelled id and closes the seat, so a closing
// seat is never counted.
func TestCancelOfAnUpSeatIsConfirmedForTheScheduler(t *testing.T) {
	f := newLauncherFixture(t, "applied")
	f.ignoreTerm = true // a close of this seat would fail
	f.l.CloseWait = 200 * time.Millisecond
	req := scheduler.SeatRequest{ID: "seat-req-inc1-12", Pool: poolA}
	f.l.RequestSeat(req)
	f.l.Wait()
	if len(f.events) != 1 {
		t.Fatalf("events %+v", f.events)
	}
	if err := f.l.CancelSeatRequest(req); err != nil {
		t.Fatalf("cancel of an up seat: %v (the scheduler would count it while it closes)", err)
	}
}

// Fix 3 item 2b: termination is confirmed by process state only; a seat the
// launcher holds no identity for is not confirmed closed, whatever its
// record says (here the record is deleted).
func TestCloseSeatNeverTrustsAMissingRecord(t *testing.T) {
	f := newLauncherFixture(t, "applied")
	pid := f.startProc()
	os.RemoveAll(filepath.Join(f.q.SessionRoot(), "agents", "cto"))
	if err := f.l.CloseSeat("cto", "idle"); err == nil || !procinfo.Alive(pid) {
		t.Fatalf("no held identity, no record: %v (alive %v)", err, procinfo.Alive(pid))
	}
	born, _ := procinfo.StartToken(pid)
	f.l.ident = map[string]seatIdentity{"cto": {PID: pid, Start: born}}
	if err := f.l.CloseSeat("cto", "idle"); err != nil || procinfo.Alive(pid) {
		t.Fatalf("held identity, record deleted: %v (alive %v)", err, procinfo.Alive(pid))
	}
}

// Fix 3 (Linux btime): the comparison uses the raw start stamp, not a
// wall-clock reconstruction, so a live process always matches itself.
func TestCloseSeatUsesTheRawStartStamp(t *testing.T) {
	f := newLauncherFixture(t, "applied")
	pid := f.startProc()
	born, ok := procinfo.StartToken(pid)
	if !ok || !(strings.HasPrefix(born, "darwin-tv:") || strings.HasPrefix(born, "linux-ticks:")) {
		t.Fatalf("start stamp %q %v", born, ok)
	}
	if again, _ := procinfo.StartToken(pid); again != born {
		t.Fatalf("the stamp of one process changed: %q then %q", born, again)
	}
	f.l.ident = map[string]seatIdentity{"cto": {PID: pid, Start: born}}
	f.l.CloseWait = time.Second
	if err := f.l.CloseSeat("cto", "idle"); err != nil || procinfo.Alive(pid) {
		t.Fatalf("raw stamp close: %v (alive %v)", err, procinfo.Alive(pid))
	}
}
