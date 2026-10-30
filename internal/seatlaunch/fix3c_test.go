//go:build unix

package seatlaunch

import (
	"bufio"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/omriariav/amq-squad/v3/internal/fsanchor"
	"github.com/omriariav/amq-squad/v3/internal/launch"
	"github.com/omriariav/amq-squad/v3/internal/procinfo"
	"github.com/omriariav/amq-squad/v3/internal/scheduler"
)

// Fix 3c item 2 (a): a seat that ignores SIGTERM is killed after the close
// grace, the close is confirmed, and the launch ledger records
// close_escalated.
func TestCloseEscalatesToSIGKILLAfterTheGrace(t *testing.T) {
	f := newLauncherFixture(t, "applied")
	f.ignoreTerm = true
	f.l.CloseWait = 300 * time.Millisecond
	req := scheduler.SeatRequest{ID: "seat-req-inc1-31", Pool: poolA}
	f.l.RequestSeat(req)
	f.l.Wait()
	if len(f.provers) != 1 || !f.provers[0].reply.OK {
		t.Fatalf("provers %+v", f.provers)
	}
	pid := f.provers[0].pid
	if err := f.l.CancelSeatRequest(req); err != nil {
		t.Fatalf("a TERM-resistant seat was not closed by escalation: %v", err)
	}
	if procinfo.Alive(pid) {
		t.Fatal("the seat still runs after the escalated close")
	}
	e, err := launch.ReadProof(f.q.SessionRoot(), "cto")
	if err != nil || !e.CloseEscalated || e.ClosedAt == nil {
		t.Fatalf("the ledger does not record the escalation: %+v %v", e, err)
	}
}

// Fix 3c item 4 (a): the probe, the unlink and the bind are one step under
// one lock; a stale socket replaced by a live one after the probe aborts the
// launch as socket_occupied and the racer's socket is left in place.
func TestSocketReplacedAfterTheProbeIsOccupied(t *testing.T) {
	sr := shortSquad(t)
	id := newLaunchID()
	name := "proof-" + id[:12] + ".sock"
	d, _, err := proofDir(sr.SessionRoot(), name)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	stale, _ := net.Listen("unix", socketAddr(d, name))
	stale.(*net.UnixListener).SetUnlinkOnClose(false)
	stale.Close()
	var racer net.Listener
	afterStaleProbe = func(pd *fsanchor.AnchoredDir, n string) {
		unix.Unlinkat(pd.Fd(), n, 0)
		racer, _ = net.Listen("unix", socketAddr(pd, n))
	}
	t.Cleanup(func() { afterStaleProbe = nil })
	_, err = startProofServer(sr, id, Options{Now: time.Now})
	if racer != nil {
		defer racer.Close()
	}
	if err == nil || !strings.Contains(err.Error(), "socket_occupied") {
		t.Fatalf("a socket replaced after the probe was taken over: %v", err)
	}
	c, derr := net.Dial("unix", socketAddr(d, name))
	if derr != nil {
		t.Fatalf("the racer's live socket was unlinked: %v", derr)
	}
	c.Close()
}

// Fix 3c item 4 (c): the reject log writes at most one line per (handle,
// code) per minute, with the count it stands for, and is capped with one
// rotation.
func TestRejectLogIsRateLimitedAndCapped(t *testing.T) {
	q := shortSquad(t)
	var mu sync.Mutex
	now := time.Now()
	clock := func() time.Time { mu.Lock(); defer mu.Unlock(); return now }
	srv, err := startProofServer(q, newLaunchID(), Options{Now: time.Now, ProofClock: clock, PaneShell: func(string) (int, string, bool) { return 0, "", false }, PaneID: func(int) string { return "" }})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.close)
	path := filepath.Join(q.SessionRoot(), "extensions", "amq-squad", "launch", "proof-rejects.log")
	m := launch.ProofMessage{Handle: "cto"}
	for i := 0; i < 500; i++ {
		srv.reject(m, 1, "proof_token_invalid")
	}
	if n := lines(t, path); n != 1 {
		t.Fatalf("500 rejects in one minute wrote %d lines", n)
	}
	mu.Lock()
	now = now.Add(61 * time.Second)
	mu.Unlock()
	srv.reject(m, 1, "proof_token_invalid")
	b, _ := os.ReadFile(path)
	if n := lines(t, path); n != 2 || !strings.Contains(string(b), `"count":500`) {
		t.Fatalf("the next minute's line does not carry the count: %q", b)
	}
	srv.lim.logCap = 600
	for i := 0; i < 40; i++ {
		srv.reject(launch.ProofMessage{Handle: "h" + strconv.Itoa(i)}, 1, "proof_token_invalid")
	}
	st, err := os.Stat(path)
	if err != nil || st.Size() > srv.lim.logCap {
		t.Fatalf("the reject log grew past its cap: %v %v", st, err)
	}
	if _, err := os.Stat(path + ".1"); err != nil {
		t.Fatalf("the capped log was not rotated: %v", err)
	}
}

func lines(t *testing.T, path string) int {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Count(string(b), "\n")
}

// Fix 3c item 4 (d): at most maxProofConns connections at once; a further
// connection is closed and counted; an idle connection is dropped at the
// read deadline.
func TestProofConnectionsAreBounded(t *testing.T) {
	prevN, prevT := maxProofConns, proofReadTimeout
	maxProofConns, proofReadTimeout = 2, 2*time.Second
	t.Cleanup(func() { maxProofConns, proofReadTimeout = prevN, prevT })
	q := shortSquad(t)
	srv, tk := testServer(t, q)
	var held []net.Conn
	for i := 0; i < 2; i++ {
		c, err := net.Dial("unix", tk.Socket)
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		held = append(held, c)
	}
	third, err := net.Dial("unix", tk.Socket)
	if err != nil {
		t.Fatal(err)
	}
	defer third.Close()
	// Refused at once (well inside the 2 s read deadline a served
	// connection would wait for).
	third.SetReadDeadline(time.Now().Add(time.Second))
	if _, err := third.Read(make([]byte, 1)); err == nil || errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("a connection past the bound was served: %v", err)
	}
	srv.mu.Lock()
	over := srv.overflow
	srv.mu.Unlock()
	if over != 1 {
		t.Fatalf("overflow count %d", over)
	}
	// The idle connections are closed by the server at the read deadline.
	held[0].SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, err := bufio.NewReader(held[0]).ReadBytes('\n'); err == nil || errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("an idle connection was not dropped at the read deadline: %v", err)
	}
}

// Fix 3c item 4 (e): a process entry that vanishes mid-walk is
// proof_origin_unknown, with no alarm and no refusal of the seat.
func TestVanishingProcessEntryIsOriginUnknown(t *testing.T) {
	prevS, prevP := startTokenOf, parentOf
	t.Cleanup(func() { startTokenOf, parentOf = prevS, prevP })
	startTokenOf = func(pid int) (string, bool) { return "s" + strconv.Itoa(pid), true }
	parentOf = func(pid int) (int, bool) {
		if pid == 300 {
			return 0, false // the entry is gone mid-walk
		}
		return 100, true
	}
	if in, known := inChain(300, "s300", 100, "s100"); in || known {
		t.Fatalf("a vanished entry was judged: in %v known %v", in, known)
	}
	q := shortSquad(t)
	o := Options{Now: time.Now, PaneShell: func(string) (int, string, bool) { return 100, "s100", true }, PaneID: func(int) string { return "" }}
	srv, err := startProofServer(q, newLaunchID(), o)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.close)
	tk := srv.ticket("cto", "seat-req-1")
	m := launch.ProofMessage{LaunchID: srv.launchID, RequestID: "seat-req-1", Handle: "cto", Token: tk.Token,
		PID: 300, Start: "s300", Root: srv.root, Me: "cto", Session: srv.session}
	if r := srv.judge(m, 300, true); r.Code != "proof_origin_unknown" {
		t.Fatalf("a vanished entry: %+v", r)
	}
	if alarms, _ := launch.ReadAlarms(q.SessionRoot()); len(alarms) != 0 {
		t.Fatalf("a vanished entry raised an alarm: %+v", alarms)
	}
	if _, _, _, refused, _ := srv.state("cto"); refused != "" {
		t.Fatalf("a vanished entry refused the seat: %s", refused)
	}
}

// Fix 3c item 4 (f): a consumed marker that cannot be written fails the
// seat's launch (no up, no ledger entry).
func TestMarkerWriteFailureFailsTheLaunch(t *testing.T) {
	markTokenHook = func(handle, marker string) error {
		if marker == launch.TokenConsumed {
			return errors.New("disk full")
		}
		return nil
	}
	t.Cleanup(func() { markTokenHook = nil })
	q := testSquad(t)
	q.Seats = q.Seats[:1]
	var p *prover
	res := proofLaunch(t, q, nil, func() { p = prove(t, q, "cto", false, nil) })
	if p.reply.OK || p.reply.Code != "proof_marker_failed" || seatsByHandle(res)["cto"].Up {
		t.Fatalf("a failed marker write still counted the seat: %+v", p.reply)
	}
	if _, err := launch.ReadProof(q.SessionRoot(), "cto"); err == nil {
		t.Fatal("a ledger entry was written behind a failed marker")
	}
}

// Fix 3c item 4 (f): the staged record has one writer path: concurrent
// read-modify-writes under the lock lose no update, and concurrent writers
// never share a temp file.
func TestStagedRecordWritersDoNotCollide(t *testing.T) {
	q := shortSquad(t)
	dir := launch.StagedDir(q.SessionRoot(), "cto")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, launch.FileName)
	base := launch.Record{Schema: 1, Handle: "cto", Root: q.SessionRoot(), Session: q.Session, LaunchNonce: "0",
		Proof: &launch.ProofTicket{Token: launch.TokenConsumed}}
	if err := launch.WriteFile(path, base); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 64)
	for i := 0; i < 32; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			errs <- launch.UpdateStaged(q.SessionRoot(), "cto", func(r *launch.Record) error {
				n, _ := strconv.Atoi(r.LaunchNonce)
				r.LaunchNonce = strconv.Itoa(n + 1)
				return nil
			})
		}()
		go func() {
			defer wg.Done()
			errs <- launch.WriteFile(filepath.Join(dir, "other.json"), base)
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("a concurrent staged write failed: %v", err)
		}
	}
	b, _ := os.ReadFile(path)
	rec, err := launch.Decode(b)
	if err != nil || rec.LaunchNonce != "32" || rec.Proof == nil || rec.Proof.Token != launch.TokenConsumed {
		t.Fatalf("lost updates or a lost marker: nonce %q proof %+v (%v)", rec.LaunchNonce, rec.Proof, err)
	}
}

// Fix 3c item 4 (g): the window closes only after every handler has
// finished, so a judgement in flight (its log line or alarm) is written
// before done.
func TestHandlersFinishBeforeDone(t *testing.T) {
	entered := make(chan struct{}, 1)
	judgeHook = func() {
		entered <- struct{}{}
		time.Sleep(300 * time.Millisecond)
	}
	t.Cleanup(func() { judgeHook = nil })
	q := shortSquad(t)
	srv, tk := testServer(t, q)
	c, err := net.Dial("unix", tk.Socket)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	b, _ := json.Marshal(launch.ProofMessage{LaunchID: srv.launchID, Handle: "cto", Token: strings.Repeat("0", 64)})
	c.Write(append(b, '\n'))
	<-entered
	srv.close()
	srv.wait()
	log, _ := os.ReadFile(filepath.Join(q.SessionRoot(), "extensions", "amq-squad", "launch", "proof-rejects.log"))
	if !strings.Contains(string(log), "proof_token_invalid") {
		t.Fatalf("done before the in-flight judgement was written: %q", log)
	}
}

// Fix 3c item 4 (h): the window is per ticket from its minting: a ticket
// minted late keeps its own 60 s and holds the socket open after an earlier
// ticket expired.
func TestWindowIsPerTicketFromMinting(t *testing.T) {
	var mu sync.Mutex
	now := time.Now()
	clock := func() time.Time { mu.Lock(); defer mu.Unlock(); return now }
	advance := func(d time.Duration) { mu.Lock(); now = now.Add(d); mu.Unlock() }
	q := shortSquad(t)
	srv, err := startProofServer(q, newLaunchID(), Options{Now: time.Now, ProofClock: clock, PaneShell: func(string) (int, string, bool) { return 0, "", false }, PaneID: func(int) string { return "" }})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.close)
	a := srv.ticket("cto", "seat-req-a")
	advance(50 * time.Second)
	b := srv.ticket("dev", "seat-req-b")
	advance(11 * time.Second) // a: 61 s, b: 11 s
	if srv.windowOver() {
		t.Fatal("the window closed while a later ticket was still valid")
	}
	msg := func(h, req, tok string) launch.ProofMessage {
		return launch.ProofMessage{LaunchID: srv.launchID, RequestID: req, Handle: h, Token: tok, Root: srv.root, Me: h, Session: srv.session}
	}
	if r := srv.judge(msg("cto", "seat-req-a", a.Token), 0, false); r.Code != "proof_expired" {
		t.Fatalf("the early ticket after its 60 s: %+v", r)
	}
	if r := srv.judge(msg("dev", "seat-req-b", b.Token), 0, false); r.Code == "proof_expired" {
		t.Fatalf("the late ticket expired on the first ticket's clock: %+v", r)
	}
	advance(50 * time.Second)
	if !srv.windowOver() {
		t.Fatal("the window stayed open after every ticket expired")
	}
}
