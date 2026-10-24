//go:build unix

package seatlaunch

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/omriariav/amq-squad/v3/internal/launch"
	"github.com/omriariav/amq-squad/v3/internal/seatexec"
)

func testServer(t *testing.T, q Squad) (*proofServer, *launch.ProofTicket) {
	t.Helper()
	o := Options{Now: time.Now, PaneShell: paneOf(os.Getpid())}
	srv, err := startProofServer(q, newLaunchID(), o)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.close)
	return srv, srv.ticket("cto", "seat-req-1")
}

// Fix 3b (a): seat-exec connects only to the socket the ticket names (its
// device and inode, through the anchored directory) served by the launcher
// the ticket names (pid and start stamp); the launcher never unlinks a live
// socket and removes a stale one only after ECONNREFUSED.
func TestProofSocketIsBoundToTheTicket(t *testing.T) {
	q := testSquad(t)
	srv, tk := testServer(t, q)
	msg := launch.ProofMessage{LaunchID: srv.launchID, Handle: "cto", Token: "not-the-token"}
	if _, err := seatexec.DialProof(os.Getuid())(*tk, msg); err != nil {
		t.Fatalf("the launcher's socket: %v", err)
	}
	forged := *tk
	forged.LauncherPID = os.Getppid()
	if _, err := seatexec.DialProof(os.Getuid())(forged, msg); err == nil || !strings.Contains(err.Error(), "not the launcher") {
		t.Fatalf("a socket served by another process was trusted: %v", err)
	}
	// A racer unlinks the socket and binds its own at the same name.
	unix.Unlinkat(srv.dir.Fd(), srv.name, 0)
	racer, err := net.Listen("unix", filepath.Join(tk.Dir, tk.Name))
	if err != nil {
		t.Fatal(err)
	}
	defer racer.Close()
	if _, err := seatexec.DialProof(os.Getuid())(*tk, msg); err == nil || !strings.Contains(err.Error(), "not the launcher's socket") {
		t.Fatalf("a rebound socket was trusted: %v", err)
	}
	// A live socket at the name is never unlinked.
	lr := shortSquad(t)
	id := newLaunchID()
	d, _, err := proofDir(lr.SessionRoot(), "proof-"+id[:12]+".sock")
	if err != nil {
		t.Fatal(err)
	}
	live, err := net.Listen("unix", socketAddr(d, "proof-"+id[:12]+".sock"))
	if err != nil {
		t.Fatal(err)
	}
	defer live.Close()
	if _, err := startProofServer(lr, id, Options{Now: time.Now}); err == nil || !strings.Contains(err.Error(), "socket_occupied") {
		t.Fatalf("a live socket was replaced: %v", err)
	}
	// A stale one (nothing listens) is removed and the server starts.
	sr := shortSquad(t)
	id2 := newLaunchID()
	d2, _, _ := proofDir(sr.SessionRoot(), "proof-"+id2[:12]+".sock")
	stale, _ := net.Listen("unix", socketAddr(d2, "proof-"+id2[:12]+".sock"))
	stale.(*net.UnixListener).SetUnlinkOnClose(false)
	stale.Close()
	s2, err := startProofServer(sr, id2, Options{Now: time.Now})
	if err != nil {
		t.Fatalf("a stale socket blocked the launch: %v", err)
	}
	s2.close()
}

// Fix 3b (b): a wrong token is logged and dropped, never a refusal of the
// seat, an alarm or a close; the real proof still succeeds.
func TestWrongTokenIsDroppedNotRefused(t *testing.T) {
	q := testSquad(t)
	q.Seats = q.Seats[:1]
	var wrong, real *prover
	res := proofLaunch(t, q, nil, func() {
		wrong = prove(t, q, "cto", false, func(m *launch.ProofMessage) { m.Token = strings.Repeat("0", 64) })
		real = prove(t, q, "cto", false, nil)
	})
	if wrong.reply.OK || wrong.reply.Code != "proof_token_invalid" || !real.reply.OK {
		t.Fatalf("wrong %+v real %+v", wrong.reply, real.reply)
	}
	if s := seatsByHandle(res)["cto"]; !s.Up {
		t.Fatalf("a wrong-token proof refused the seat: %+v", s)
	}
	if alarms, _ := launch.ReadAlarms(q.SessionRoot()); len(alarms) != 0 {
		t.Fatalf("a wrong token raised an alarm: %+v", alarms)
	}
	if b, _ := os.ReadFile(filepath.Join(q.SessionRoot(), "extensions", "amq-squad", "launch", "proof-rejects.log")); !strings.Contains(string(b), "proof_token_invalid") {
		t.Fatalf("the dropped proof was not logged: %q", b)
	}
}

// Fix 3b (c): the socket stays open for the token window after the launch
// returns, so a second valid proof arriving late is still heard and alarmed.
func TestSocketStaysOpenForTheTokenWindow(t *testing.T) {
	q := testSquad(t)
	q.Seats = q.Seats[:1]
	var token string
	res := proofLaunch(t, q, nil, func() {
		token = stagedToken(t, q, "cto")
		prove(t, q, "cto", false, nil)
	})
	if !seatsByHandle(res)["cto"].Up {
		t.Fatalf("seat %+v", res.Seats)
	}
	late := prove(t, q, "cto", false, func(m *launch.ProofMessage) { m.Token = token })
	if late.reply.Code != "identity_forged" {
		t.Fatalf("a late replay after the launch returned: %+v", late.reply)
	}
}

// Fix 3b (d): every hop's start stamp is read before and after its parent,
// and the sender's stamp is re-checked after the walk.
func TestPaneChainChecksEveryHop(t *testing.T) {
	prevS, prevP := startTokenOf, parentOf
	t.Cleanup(func() { startTokenOf, parentOf = prevS, prevP })
	reads := map[int]int{}
	parentOf = func(pid int) (int, bool) { return map[int]int{300: 200, 200: 100}[pid], true }
	startTokenOf = func(pid int) (string, bool) {
		reads[pid]++
		if pid == 200 && reads[pid] > 1 {
			return "replaced", true // hop 200 is replaced while its parent is read
		}
		return "s" + string(rune('0'+pid/100)), true
	}
	if in, known := inChain(300, "s3", 100, "s1"); in || known {
		t.Fatal("a hop replaced mid-walk kept the chain (or counted as known)")
	}
	reads = map[int]int{}
	startTokenOf = func(pid int) (string, bool) {
		reads[pid]++
		if pid == 300 && reads[pid] > 2 {
			return "replaced", true // the sender is replaced after the walk began
		}
		return "s" + string(rune('0'+pid/100)), true
	}
	if in, _ := inChain(300, "s3", 100, "s1"); in {
		t.Fatal("a sender replaced during the walk kept the chain")
	}
	startTokenOf = func(pid int) (string, bool) { return "s" + string(rune('0'+pid/100)), true }
	if in, known := inChain(300, "s3", 100, "s1"); !in || !known {
		t.Fatal("an intact chain was refused")
	}
}

// Fix 3b (e): a used-up token is overwritten in the staged record: consumed
// at proof time, expired when the window closes.
func TestUsedTokensAreOverwrittenInTheStagedRecord(t *testing.T) {
	q := testSquad(t)
	var res Result
	res = proofLaunch(t, q, nil, func() { prove(t, q, "cto", false, nil) })
	if got := stagedToken(t, q, "cto"); got != launch.TokenConsumed {
		t.Fatalf("cto token after its proof: %q", got)
	}
	res.proof.close()
	if got := stagedToken(t, q, "dev"); got != launch.TokenExpired {
		t.Fatalf("dev token after the window: %q", got)
	}
}

// Fix 3b (f): the window runs on the monotonic clock from minting; a wall
// clock that jumps (the record timestamps' clock) does not expire a token.
func TestTokenWindowIsMonotonic(t *testing.T) {
	q := testSquad(t)
	q.Seats = q.Seats[:1]
	jumped := func() time.Time { return time.Now().Round(0).Add(2 * time.Hour) } // a wall-clock step
	var p *prover
	proofLaunch(t, q, func(o *Options) { o.Now = jumped }, func() { p = prove(t, q, "cto", false, nil) })
	if !p.reply.OK {
		t.Fatalf("a wall-clock step expired the token: %+v", p.reply)
	}
}

// Fix 3b (g): when the kernel does not report the sender, the proof is
// refused (proof_origin_unknown), not accepted on the claimed pid.
func TestUnavailablePeerCredentialsRefuse(t *testing.T) {
	prev := peerOf
	peerOf = func(*net.UnixConn) (int, bool) { return 0, false }
	t.Cleanup(func() { peerOf = prev })
	q := testSquad(t)
	q.Seats = q.Seats[:1]
	var p *prover
	res := proofLaunch(t, q, nil, func() { p = prove(t, q, "cto", false, nil) })
	if p.reply.OK || p.reply.Code != "proof_origin_unknown" || seatsByHandle(res)["cto"].Up {
		t.Fatalf("no peer credentials: %+v", p.reply)
	}
}

// shortSquad is a squad whose launch directory path fits sun_path, so the
// socket is bound in the launch directory itself (no temp fallback).
func shortSquad(t *testing.T) Squad {
	t.Helper()
	base, err := os.MkdirTemp("/tmp", "s")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(base) })
	base, _ = filepath.EvalSymlinks(base)
	root := filepath.Join(base, "p", ".agent-mail", "s1")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	return Squad{BaseRoot: filepath.Dir(root), Session: "s1"}
}
