//go:build unix

package seatlaunch

import (
	"context"
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
)

// proofLaunch runs one launch whose Apply calls onApply (where the test
// plays the seats), and returns the result.
func proofLaunch(t *testing.T, q Squad, mutate func(*Options), onApply func()) Result {
	t.Helper()
	f := &fakeAMQ{t: t, prepare: preparedResult(), apply: launchapi.ApplyResultV1{ResultVersion: 1, Outcome: "applied", Disposition: "committed"}}
	f.onApply = onApply
	o := launchOpts(t, f, map[string]string{"sha256:a1": "trust_exact_subject"})
	o.Wait = 500 * time.Millisecond
	if mutate != nil {
		mutate(&o)
	}
	res, err := Launch(context.Background(), q, o)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func seatsByHandle(res Result) map[string]SeatState {
	m := map[string]SeatState{}
	for _, s := range res.Seats {
		m[s.Handle] = s
	}
	return m
}

// Item 4: a seat counts up only from its proof on the launch's socket; a
// seat-exec refusal in the record is reported as evidence.
func TestSeatIsUpOnlyFromItsProof(t *testing.T) {
	q := testSquad(t)
	var p *prover
	res := proofLaunch(t, q, nil, func() {
		p = prove(t, q, "cto", false, nil)
		publishExec(t, q, "dev", &launch.Exec{At: time.Now(), Outcome: "refused", ReasonCode: "identity_proof_failed", Detail: "amq env failed: exit status 5"})
	})
	got := seatsByHandle(res)
	if !p.reply.OK || !got["cto"].Up || got["cto"].PID != p.pid || got["cto"].Start != p.start {
		t.Fatalf("cto: reply %+v state %+v", p.reply, got["cto"])
	}
	if got["dev"].Up || got["dev"].ReasonCode != "identity_proof_failed" {
		t.Fatalf("dev %+v", got["dev"])
	}
	if e, err := launch.ReadProof(q.SessionRoot(), "cto"); err != nil || e.PID != p.pid || e.Start != p.start || e.LaunchID != res.LaunchID {
		t.Fatalf("launch ledger %+v %v", e, err)
	}
}

// Item 4: a forged record alone (this launch's id, the seat's request id, an
// exec block) never counts a seat up.
func TestForgedRecordAloneNeverCountsUp(t *testing.T) {
	q := testSquad(t)
	q.Seats = q.Seats[:1]
	res := proofLaunch(t, q, nil, func() {
		publishExec(t, q, "cto", &launch.Exec{At: time.Now(), Outcome: "exec", IdentityProof: "amq env", PID: os.Getpid()})
	})
	if s := seatsByHandle(res)["cto"]; s.Up || s.State != "not_started" {
		t.Fatalf("a forged record counted: %+v", s)
	}
}

// Item 4: a replayed token is refused as identity_forged; the seat that did
// prove is closed and the alarm reaches the owner.
func TestReplayedTokenIsRefusedAndAlarmed(t *testing.T) {
	q := testSquad(t)
	q.Seats = q.Seats[:1]
	var first, second *prover
	res := proofLaunch(t, q, nil, func() {
		token := stagedToken(t, q, "cto") // read before the first proof, as a racer or seat-exec would
		first = prove(t, q, "cto", false, nil)
		second = prove(t, q, "cto", false, func(m *launch.ProofMessage) { m.Token = token })
	})
	if !first.reply.OK || second.reply.OK || second.reply.Code != "identity_forged" || !strings.Contains(second.reply.Detail, "proof_replayed") {
		t.Fatalf("first %+v second %+v", first.reply, second.reply)
	}
	time.Sleep(100 * time.Millisecond)
	if procinfo.Alive(first.pid) {
		t.Fatal("the seat whose token was replayed was not closed")
	}
	if s := seatsByHandle(res)["cto"]; s.Up || s.ReasonCode != "identity_forged" {
		t.Fatalf("seat %+v", s)
	}
	alarms, _ := launch.ReadAlarms(q.SessionRoot())
	if len(alarms) != 1 || alarms[0].Kind != "identity_forged" || alarms[0].Code != "proof_replayed" {
		t.Fatalf("alarms %+v", alarms)
	}
}

// Item 4 tightening 1: a token used more than 60 s after staging is refused
// as proof_expired, and the seat's launch fails.
func TestExpiredTokenIsRefused(t *testing.T) {
	q := testSquad(t)
	q.Seats = q.Seats[:1]
	var mu sync.Mutex
	offset := time.Duration(0)
	now := func() time.Time { mu.Lock(); defer mu.Unlock(); return time.Now().Add(offset) }
	var p *prover
	res := proofLaunch(t, q, func(o *Options) { o.ProofClock = now }, func() {
		mu.Lock()
		offset = launch.ProofTTL + time.Second
		mu.Unlock()
		p = prove(t, q, "cto", false, nil)
	})
	if p.reply.OK || p.reply.Code != "proof_expired" {
		t.Fatalf("late proof: %+v", p.reply)
	}
	if s := seatsByHandle(res)["cto"]; s.Up || s.ReasonCode != "proof_expired" || !res.Failed() {
		t.Fatalf("seat %+v failed %v", s, res.Failed())
	}
}

// Item 4 tightening 2: a proof from a pid whose parent chain does not reach
// the seat's pane shell is refused (proof_origin), not counted, and alarmed.
func TestProofFromOutsideThePaneChainIsRefused(t *testing.T) {
	q := testSquad(t)
	q.Seats = q.Seats[:1]
	other := exec.Command("sleep", "30") // the "pane shell" is an unrelated process
	if err := other.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { other.Process.Kill(); other.Wait() })
	var p *prover
	res := proofLaunch(t, q, func(o *Options) { o.PaneShell = paneOf(other.Process.Pid) }, func() {
		p = prove(t, q, "cto", false, nil)
	})
	if p.reply.OK || p.reply.Code != "identity_forged" || !strings.Contains(p.reply.Detail, "proof_origin") {
		t.Fatalf("outside proof: %+v", p.reply)
	}
	if s := seatsByHandle(res)["cto"]; s.Up {
		t.Fatalf("counted: %+v", s)
	}
	if alarms, _ := launch.ReadAlarms(q.SessionRoot()); len(alarms) != 1 || alarms[0].Code != "proof_origin" {
		t.Fatalf("alarms %+v", alarms)
	}
}

// Item 4 tightening 2, the check itself with a stubbed pane lookup: inside
// the chain is accepted (TestSeatIsUpOnlyFromItsProof); a claimed pid that
// is not the sender, or a pane that is not known yet, is refused.
func TestPaneChainCheckWithStubbedInspect(t *testing.T) {
	if in, _ := inChain(os.Getpid(), mustStamp(t, os.Getpid()), os.Getppid(), mustStamp(t, os.Getppid())); !in {
		t.Fatal("this process is not in its parent's chain")
	}
	if in, _ := inChain(os.Getppid(), mustStamp(t, os.Getppid()), os.Getpid(), mustStamp(t, os.Getpid())); in {
		t.Fatal("a parent counted as its child's descendant")
	}
	q := testSquad(t)
	q.Seats = q.Seats[:1]
	var claimed, unknown *prover
	proofLaunch(t, q, nil, func() {
		claimed = prove(t, q, "cto", false, func(m *launch.ProofMessage) { m.PID, m.Start = os.Getpid(), mustStamp(t, os.Getpid()) })
	})
	if claimed.reply.OK || !strings.Contains(claimed.reply.Detail, "proof_origin") {
		t.Fatalf("a proof claiming another pid: %+v", claimed.reply)
	}
	r := testSquad(t)
	r.Seats = r.Seats[:1]
	proofLaunch(t, r, func(o *Options) { o.PaneShell = func(string) (int, string, bool) { return 0, "", false } }, func() {
		unknown = prove(t, r, "cto", false, nil)
	})
	if unknown.reply.OK || unknown.reply.Code != "proof_origin_unknown" {
		t.Fatalf("pane not known: %+v", unknown.reply)
	}
}

func mustStamp(t *testing.T, pid int) string {
	t.Helper()
	s, ok := procinfo.StartToken(pid)
	if !ok {
		t.Skip("start stamps unavailable on this host")
	}
	return s
}

// Item 4 (e): the launcher keeps the proved identity in memory and in the
// core-owned launch ledger; a launcher without it in memory (a restart)
// closes the seat from the ledger, never from the seat's record.
func TestCloseSeatUsesTheLaunchLedger(t *testing.T) {
	q := testSquad(t)
	seat := exec.Command("sleep", "30")
	if err := seat.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { seat.Process.Kill() })
	go seat.Wait()
	start := mustStamp(t, seat.Process.Pid)
	if err := launch.WriteProof(q.SessionRoot(), launch.ProofEntry{Handle: "cto", LaunchID: "l1", PID: seat.Process.Pid, Start: start, At: time.Now()}); err != nil {
		t.Fatal(err)
	}
	l := &Launcher{Root: q.SessionRoot(), CloseWait: time.Second}
	if err := l.CloseSeat("cto", "idle"); err != nil || procinfo.Alive(seat.Process.Pid) {
		t.Fatalf("close from the ledger: %v (alive %v)", err, procinfo.Alive(seat.Process.Pid))
	}
}

// stagedToken reads a seat's token from its staged record.
func stagedToken(t *testing.T, q Squad, handle string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(launch.StagedDir(q.SessionRoot(), handle), launch.FileName))
	if err != nil {
		t.Fatal(err)
	}
	rec, err := launch.Decode(b)
	if err != nil || rec.Proof == nil {
		t.Fatalf("staged ticket: %v", err)
	}
	return rec.Proof.Token
}

// Third pass R8: the accepted proof records the pane shell it reached (pid
// and start stamp) in the launch ledger; the scheduler's decision socket
// proves the lead against that record and never against a pane label.
func TestProofRecordsTheLeadPaneShell(t *testing.T) {
	q := testSquad(t)
	q.Seats = q.Seats[:1]
	var p *prover
	proofLaunch(t, q, func(o *Options) { o.PaneShell = paneOf(os.Getpid()) }, func() {
		p = prove(t, q, "cto", false, nil)
	})
	if !p.reply.OK {
		t.Fatalf("proof: %+v", p.reply)
	}
	e, err := launch.ReadProof(q.SessionRoot(), "cto")
	if err != nil || e.PaneShell != os.Getpid() || e.PaneShellStart != mustStamp(t, os.Getpid()) {
		t.Fatalf("the ledger does not carry the pane shell: %+v %v", e, err)
	}
}
