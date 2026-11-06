package schedproc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/launch"
	"github.com/omriariav/amq-squad/v3/internal/panechain"
	"github.com/omriariav/amq-squad/v3/internal/scheduler"
	"github.com/omriariav/amq-squad/v3/internal/sealchain"
	"github.com/omriariav/amq-squad/v3/internal/seatcommit"
	"github.com/omriariav/amq-squad/v3/internal/task"
)

// The rows of 3.2.0-0c-B's process half (recover.go, prepare.go): the
// recovery through the scheduler's apply step makes the seat grantable, a
// clear does not, and the decision socket proves the sender. The rig's
// root, the seat repository and HOME are temp directories (rule 18).

// tamperedSeatRig is the live rig with a real seat worktree (a bare origin,
// a main checkout, the seat's branch), its staged launch record and
// core-recorded base, the real preparer, and the seat's ledger hand-edited
// so its tag fails.
type tamperedSeatRig struct {
	*liveRig
	handle, wt, origin string
	target             seatcommit.Target
	tk                 task.Task
	original, tampered []byte
}

func newTamperedSeatRig(t *testing.T) *tamperedSeatRig {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not on PATH")
	}
	t.Setenv("HOME", t.TempDir())
	c := &Committer{Step: func(context.Context, string, task.Task) seatcommit.Result { return seatcommit.Result{OK: true} },
		ExitStep: func(string, string, string) seatcommit.Result { return seatcommit.Result{OK: true} }}
	r := &tamperedSeatRig{liveRig: newLiveRig(t, c)}
	base, _ := filepath.EvalSymlinks(t.TempDir())
	proj := filepath.Join(base, "proj")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	gitRun(t, proj, "init", "-q", "-b", "main")
	commitFile(t, proj, "README.md", "readme\n", "initial")
	r.origin = filepath.Join(base, "origin.git")
	gitRun(t, base, "init", "-q", "--bare", "-b", "main", r.origin)
	gitRun(t, proj, "remote", "add", "origin", r.origin)
	gitRun(t, proj, "push", "-q", "origin", "main")
	tk, err := r.store.Add(task.AddInput{Title: "t", Class: "summarize", Pool: &sonnet}, r.now)
	if err != nil {
		t.Fatal(err)
	}
	r.tk = tk
	// the fake launcher names the task's seat seat-<task id>
	r.handle = "seat-" + tk.ID
	branch := "seat/" + r.handle
	r.wt = filepath.Join(base, "wt", r.handle)
	gitRun(t, proj, "worktree", "add", "-q", "-b", branch, r.wt, "main")
	dir := launch.StagedDir(r.root, r.handle)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := launch.WriteFile(filepath.Join(dir, launch.FileName), launch.Record{Handle: r.handle, Root: r.root, Session: "sess", Runtime: "claude", Worktree: r.wt, Branch: branch}); err != nil {
		t.Fatal(err)
	}
	r.target = seatcommit.Target{Repository: "o/r", BaseBranch: "main", PushURL: r.origin, LocalPushAck: true}
	if _, err := seatcommit.RecordWorktreeBase("", r.root, r.handle, r.wt, branch, r.target, r.now); err != nil {
		t.Fatal(err)
	}
	r.s.Preparer = &CleanSeats{Root: r.root, Git: "git"}
	p := filepath.Join(seatcommit.LedgerDir(r.root), r.handle+".json")
	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	head := gitRun(t, r.wt, "rev-parse", "HEAD")
	r.original = raw
	r.tampered = bytes.Replace(raw, []byte(head), []byte(strings.Repeat("0", 40)), 1)
	if bytes.Equal(r.tampered, raw) {
		t.Fatal("the base is not in the ledger")
	}
	if err := os.WriteFile(p, r.tampered, 0o600); err != nil {
		t.Fatal(err)
	}
	return r
}

// ticks runs n ticks and returns every decision.
func (r *tamperedSeatRig) ticks(n int) []scheduler.Decision {
	r.t.Helper()
	var all []scheduler.Decision
	for i := 0; i < n; i++ {
		d, err := r.tick(func() {})
		if err != nil {
			r.t.Fatal(err)
		}
		all = append(all, d...)
		r.now = r.now.Add(2 * time.Second)
	}
	return all
}

func kinds(ds []scheduler.Decision, kind string) []scheduler.Decision {
	var out []scheduler.Decision
	for _, d := range ds {
		if d.Kind == kind {
			out = append(out, d)
		}
	}
	return out
}

// B2 (the process half), B3 and B1's exact-once over the real preparer: a
// tampered ledger at prepare is one seat_prepare_failed line (with the
// reason code and the quarantine path) and one typed block over many
// ticks; a clear of a warning on the seat leaves it held; the lead's
// recovery through the scheduler's apply step writes the chain, appends a
// ledger_recovered line naming the seat, the old seq, the slot and the
// carried entries, and at the next tick the task returns to pending and
// is dispatched to the seat (its prepare now passes); the quarantine copy
// is unchanged. Reverted (the recovery not applied to the scheduler), the
// chain is written but the seat stays held.
func TestRecoveryThroughTheSchedulerMakesTheSeatGrantable(t *testing.T) {
	r := newTamperedSeatRig(t)
	all := r.ticks(40)
	slot := filepath.Join(seatcommit.QuarantineDir(r.root), "commits."+r.handle+".seq1")
	failed := kinds(all, "seat_prepare_failed")
	if len(failed) != 1 || failed[0].ReasonCode != scheduler.ReasonLedgerTampered || !strings.Contains(failed[0].Reason, "quarantined at "+slot+" (seq 1)") {
		t.Fatalf("seat_prepare_failed over 40 ticks: %+v", failed)
	}
	if len(kinds(all, "blocked")) != 1 || len(kinds(all, "dispatched")) != 0 {
		t.Fatalf("the block over 40 ticks: %+v", all)
	}
	got, _ := r.store.Get(r.tk.ID)
	if got.State != task.StateBlocked || got.BlockReason != scheduler.ReasonLedgerTampered || !strings.Contains(got.BlockDetail, slot) {
		t.Fatalf("the task: %+v", got)
	}
	slotBody := func() []byte {
		b, err := os.ReadFile(filepath.Join(slot, "body.json"))
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	if !bytes.Equal(slotBody(), r.tampered) {
		t.Fatal("the slot is not the refused body")
	}
	// B3: a clear is not the recovery. ledger_tampered is no warning
	// (clear_not_a_warning); a warning the seat does show, cleared, leaves
	// the seat held and the task blocked.
	showing := func(_ string, seat string) (string, []string, error) {
		return "L1", []string{"wake_off: no AMQ wake for this seat"}, nil
	}
	if reply := RecordClear(r.root, "lead", ClearRequest{Seat: r.handle, Warning: "ledger_tampered", LaunchID: "L1"}, showing); reply.OK || reply.Code != RefusedClearNotAWarning {
		t.Fatalf("a clear of ledger_tampered: %+v", reply)
	}
	if reply := RecordClear(r.root, "lead", ClearRequest{Seat: r.handle, Warning: "wake_off", LaunchID: "L1"}, showing); !reply.OK {
		t.Fatalf("a clear of a showing warning: %+v", reply)
	}
	if _, err := r.store.Reset(r.tk.ID, nil, r.now); err != nil {
		t.Fatal(err)
	}
	if all = r.ticks(10); len(kinds(all, "dispatched")) != 0 || len(kinds(all, "seat_prepare_failed")) != 0 {
		t.Fatalf("the cleared seat was tried: %+v", all)
	}
	if _, err := r.store.Block(r.tk.ID, "", scheduler.ReasonLedgerTampered, got.BlockDetail, task.RouteLead, r.now); err != nil {
		t.Fatal(err)
	}
	// the recovery, as the decision socket's apply step runs it after the
	// sender is proved
	decide := recoverDecision(r.s, r.root, Recovery{Root: r.root, Target: r.target, Git: "git", Now: func() time.Time { return r.now }}.Apply, func() time.Time { return r.now })
	if reply := decide("lead", RecoverRequest{Seat: r.handle, Seq: "1", Slot: 1, Reason: "the edit is understood"}); !reply.OK {
		t.Fatalf("the recovery: %+v", reply)
	}
	l, err := seatcommit.ReadLedger(r.root, r.handle)
	if err != nil || l.Seal.Seq != 2 || l.Recovered == nil || l.Recovered.Slot != slot {
		t.Fatalf("the recovered ledger: %v %+v", err, l.Seal)
	}
	if !bytes.Equal(slotBody(), r.tampered) {
		t.Fatal("the recovery changed the quarantine copy")
	}
	lines, err := ReadDecisionsTail(r.root)
	if err != nil {
		t.Fatal(err)
	}
	var rec *DecisionLine
	for i := range lines {
		if lines[i].Kind == DecisionLedgerRecovered {
			rec = &lines[i]
		}
	}
	if rec == nil || rec.Seat != r.handle {
		t.Fatalf("no ledger_recovered line: %+v", lines)
	}
	for _, want := range []string{"the lead lead recovered", "seat " + r.handle, "quarantine seq 1", slot, "continues at seq 2", "carried entries: none", "the edit is understood"} {
		if !strings.Contains(rec.Reason, want) {
			t.Fatalf("the decision line does not say %q: %s", want, rec.Reason)
		}
	}
	all = r.ticks(3)
	if len(kinds(all, "task_reset")) != 1 || !has(all, "dispatched", r.tk.ID, r.handle) {
		t.Fatalf("after the recovery: %+v", all)
	}
	if got, _ = r.store.Get(r.tk.ID); got.State != task.StateInProgress || got.Lease == nil || got.Lease.Holder != r.handle {
		t.Fatalf("not dispatched to the recovered seat: %+v", got)
	}
}

func has(ds []scheduler.Decision, kind, taskID, seat string) bool {
	for _, d := range ds {
		if d.Kind == kind && (taskID == "" || d.TaskID == taskID) && (seat == "" || d.Seat == seat) {
			return true
		}
	}
	return false
}

// B3b: the recovery over the decision socket is proved like a widening: a
// sender whose chain does not reach the lead's recorded incarnation is
// not_lead and nothing is applied; the lead's pane is applied. A scheduler
// with no recovery hook refuses recover_unsupported; a malformed request
// (a bad seat, seq or carry) is refused before the apply step. Reverted
// (the kind not proved), a seat's process recovers its own ledger.
func TestRecoveryIsProvedFromTheLeadsPane(t *testing.T) {
	lead := &panechain.Pane{Shell: 80, Start: "s80", Agent: "lead"}
	hit := func(p panechain.Pane) (*panechain.Pane, bool) { return &p, true }
	miss := func(panechain.Pane) (*panechain.Pane, bool) { return nil, true }
	serve := func(reach func(panechain.Pane) (*panechain.Pane, bool), recover func(string, RecoverRequest) DecideReply) *DecisionServer {
		root, _ := filepath.EvalSymlinks(t.TempDir())
		s, err := ServeDecisions(DecideOptions{Root: root, Lead: "lead",
			Apply:      func(string, WidenRequest) DecideReply { return DecideReply{OK: true} },
			Recover:    recover,
			LeadPane:   func(string, string) (panechain.Pane, error) { return *lead, nil },
			Reaches:    func(_ int, _ string, panes []panechain.Pane) (*panechain.Pane, bool) { return reach(panes[0]) },
			StartToken: func(int) (string, bool) { return "s", true },
			Peer:       func(*net.UnixConn) (int, bool) { return 4242, true },
		})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(s.Close)
		return s
	}
	req := RecoverRequest{Seat: "dev", Seq: "150", Slot: 1, Carry: []string{"t1:" + strings.Repeat("a", 40)}, Reason: "r"}
	applied := 0
	hook := func(lead string, got RecoverRequest) DecideReply {
		applied++
		if got.Seat != "dev" || got.Seq != "150" || got.Slot != 1 || len(got.Carry) != 1 || lead != "lead" {
			t.Errorf("the request as applied: %+v by %s", got, lead)
		}
		return DecideReply{OK: true}
	}
	s := serve(miss, hook)
	if reply, err := SendRecover(s.Root(), s.Path(), req); err != nil || reply.Code != RefusedNotLead || applied != 0 {
		t.Fatalf("a seat's process: %+v %v (applied %d)", reply, err, applied)
	}
	s = serve(hit, hook)
	if reply, err := SendRecover(s.Root(), s.Path(), req); err != nil || !reply.OK || applied != 1 {
		t.Fatalf("the lead's pane: %+v %v (applied %d)", reply, err, applied)
	}
	s = serve(hit, nil)
	if reply, err := SendRecover(s.Root(), s.Path(), req); err != nil || reply.Code != RefusedRecoverUnsupported {
		t.Fatalf("no hook: %+v %v", reply, err)
	}
	// the shape, checked by the apply step before the chain writer runs
	calls := 0
	decide := recoverDecision(&scheduler.Scheduler{}, t.TempDir(), func(string, RecoverRequest) (seatcommit.RecoverResult, error) {
		calls++
		return seatcommit.RecoverResult{}, errors.New("not reached")
	}, time.Now)
	for _, bad := range []RecoverRequest{
		{Seat: "../dev", Seq: "1"},
		{Seat: "dev", Seq: "0"},
		{Seat: "dev", Seq: "x"},
		{Seat: "dev", Seq: "1", Slot: -1},
		{Seat: "dev", Seq: "1", Carry: []string{"t1:abc"}},
		{Seat: "dev", Seq: "1", Carry: []string{strings.Repeat("a", 40)}},
	} {
		if reply := decide("lead", bad); reply.OK || reply.Code != RefusedRecoverMalformed || calls != 0 {
			t.Fatalf("%+v: %+v (writer called %d)", bad, reply, calls)
		}
	}
	// a refusal of the chain writer carries its code
	decide = recoverDecision(&scheduler.Scheduler{}, t.TempDir(), func(string, RecoverRequest) (seatcommit.RecoverResult, error) {
		return seatcommit.RecoverResult{}, &seatcommit.RecoverRefusal{Code: seatcommit.RecoverSlotMissing, Detail: "no slot"}
	}, time.Now)
	if reply := decide("lead", RecoverRequest{Seat: "dev", Seq: "unknown"}); reply.OK || reply.Code != seatcommit.RecoverSlotMissing || reply.Detail != "no slot" {
		t.Fatalf("the writer's refusal: %+v", reply)
	}
}

// Codex's P2 on #904, the interrupted completion: the chain is written and
// the decision line cannot be appended (the log's path taken by a
// directory): the reply is recover_unrecorded; the next tick releases the
// seat from the ledger all the same (ledger_hold_released, task_reset,
// dispatched); the decision sent again on the recovered ledger is answered
// OK as already recovered and records the line, with the ledger unchanged.
// Reverted (the release a message of the apply step), the hold outlives
// the failed append.
func TestInterruptedRecoveryCompletesFromTheLedger(t *testing.T) {
	r := newTamperedSeatRig(t)
	r.ticks(3)
	if got, _ := r.store.Get(r.tk.ID); got.State != task.StateBlocked {
		t.Fatalf("not blocked: %+v", got)
	}
	if err := os.MkdirAll(DecisionsPath(r.root), 0o700); err != nil {
		t.Fatal(err)
	}
	decide := recoverDecision(r.s, r.root, Recovery{Root: r.root, Target: r.target, Git: "git", Now: func() time.Time { return r.now }}.Apply, func() time.Time { return r.now })
	req := RecoverRequest{Seat: r.handle, Seq: "1", Slot: 1, Reason: "understood"}
	if reply := decide("lead", req); reply.OK || reply.Code != RefusedRecoverUnrecorded {
		t.Fatalf("the failed append: %+v", reply)
	}
	written, err := os.ReadFile(filepath.Join(seatcommit.LedgerDir(r.root), r.handle+".json"))
	if err != nil {
		t.Fatal(err)
	}
	if l, err := seatcommit.ReadLedger(r.root, r.handle); err != nil || l.Recovered == nil {
		t.Fatalf("the chain was not written: %v", err)
	}
	all := r.ticks(3)
	if !has(all, "ledger_hold_released", "", r.handle) || !has(all, "task_reset", r.tk.ID, r.handle) || !has(all, "dispatched", r.tk.ID, r.handle) {
		t.Fatalf("the hold was not released from the ledger: %+v", all)
	}
	if err := os.Remove(DecisionsPath(r.root)); err != nil {
		t.Fatal(err)
	}
	reply := decide("lead", req)
	var res seatcommit.RecoverResult
	if !reply.OK || json.Unmarshal(reply.Data, &res) != nil || !res.AlreadyRecovered || res.OldSeq != 1 || res.NewSeq != 2 || !strings.Contains(reply.Detail, "already recovered") {
		t.Fatalf("the retry: %+v", reply)
	}
	if after, _ := os.ReadFile(filepath.Join(seatcommit.LedgerDir(r.root), r.handle+".json")); !bytes.Equal(after, written) {
		t.Fatal("the retry wrote the ledger again")
	}
	lines, _ := ReadDecisionsTail(r.root)
	n := 0
	for _, l := range lines {
		if l.Kind == DecisionLedgerRecovered && strings.Contains(l.Reason, "had recovered") {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("the retry's decision line: %d, want 1", n)
	}
}

// The plan reviewer on c9826c31, end to end: the held seat's document is
// put back (the refused bytes replaced by the document it was edited
// from, which verifies at the same seq): the reconciliation holds (the
// ledger records no recovery), the lead's decision on the slot is recorded
// as the next link (Verified), and the next tick releases the seat and
// dispatches. Reverted (the verifying document refused not_tampered), the
// seat is held for good.
func TestARestoredDocumentIsReleasedByTheLeadsDecision(t *testing.T) {
	r := newTamperedSeatRig(t)
	r.ticks(3)
	if got, _ := r.store.Get(r.tk.ID); got.State != task.StateBlocked {
		t.Fatalf("not blocked: %+v", got)
	}
	p := filepath.Join(seatcommit.LedgerDir(r.root), r.handle+".json")
	if err := os.WriteFile(p, r.original, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := seatcommit.ReadLedger(r.root, r.handle); err != nil {
		t.Fatalf("the restored document does not verify: %v", err)
	}
	if all := r.ticks(5); len(kinds(all, "ledger_hold_released")) != 0 || len(kinds(all, "dispatched")) != 0 {
		t.Fatalf("the restored document released the hold without a decision: %+v", all)
	}
	decide := recoverDecision(r.s, r.root, Recovery{Root: r.root, Target: r.target, Git: "git", Now: func() time.Time { return r.now }}.Apply, func() time.Time { return r.now })
	reply := decide("lead", RecoverRequest{Seat: r.handle, Seq: "1", Slot: 1, Reason: "a transient alarm"})
	var res seatcommit.RecoverResult
	if !reply.OK || json.Unmarshal(reply.Data, &res) != nil || !res.Verified || res.NewSeq != 2 {
		t.Fatalf("the decision: %+v", reply)
	}
	all := r.ticks(3)
	if !has(all, "ledger_hold_released", "", r.handle) || !has(all, "task_reset", r.tk.ID, r.handle) || !has(all, "dispatched", r.tk.ID, r.handle) {
		t.Fatalf("the decision did not release the seat: %+v", all)
	}
	lines, _ := ReadDecisionsTail(r.root)
	found := false
	for _, l := range lines {
		found = found || (l.Kind == DecisionLedgerRecovered && strings.Contains(l.Reason, "recorded the release of"))
	}
	if !found {
		t.Fatalf("no verified-recovery line: %+v", lines)
	}
}

// Codex on b250b951, the process half of the baseline: the seat's ledger
// records the lead's verified decision (the restored document, released);
// then its head's seq is changed to 1, and the prepare's alarm carries the
// record the refused body had as the hold's baseline, equal to the mark
// LedgerRecovery answers once the head is put back, so the scheduler's
// covers check refuses it (the scheduler row). Reverted (no baseline in the
// prepare's error), the alarm has none.
func TestThePrepareAlarmCarriesTheLedgersRecordAsTheBaseline(t *testing.T) {
	r := newTamperedSeatRig(t)
	r.ticks(3)
	p := filepath.Join(seatcommit.LedgerDir(r.root), r.handle+".json")
	if err := os.WriteFile(p, r.original, 0o600); err != nil {
		t.Fatal(err)
	}
	decide := recoverDecision(r.s, r.root, Recovery{Root: r.root, Target: r.target, Git: "git", Now: func() time.Time { return r.now }}.Apply, func() time.Time { return r.now })
	if reply := decide("lead", RecoverRequest{Seat: r.handle, Seq: "1", Slot: 1, Reason: "a transient alarm"}); !reply.OK {
		t.Fatalf("the decision: %+v", reply)
	}
	c := r.s.Preparer.(*CleanSeats)
	before, ok := c.LedgerRecovery(r.handle)
	if !ok || before.Link != 2 {
		t.Fatalf("the ledger's record: %v %+v", ok, before)
	}
	hp := sealchain.HeadPath(r.root, "commits."+r.handle)
	orig, err := os.ReadFile(hp)
	if err != nil {
		t.Fatal(err)
	}
	var hd map[string]any
	if err := json.Unmarshal(orig, &hd); err != nil {
		t.Fatal(err)
	}
	hd["seq"] = 1
	edited, _ := json.Marshal(hd)
	if err := os.WriteFile(hp, edited, 0o600); err != nil {
		t.Fatal(err)
	}
	var e *scheduler.LedgerTamperedError
	if err := c.PrepareSeat(r.handle); !errors.As(err, &e) || e.Seq != "1" || e.Baseline == nil {
		t.Fatalf("the alarm: %v %+v", err, e)
	}
	if err := os.WriteFile(hp, orig, 0o600); err != nil {
		t.Fatal(err)
	}
	back, ok := c.LedgerRecovery(r.handle)
	if !ok || back.Seq != e.Baseline.Seq || back.Slot != e.Baseline.Slot || back.Link != e.Baseline.Link || !back.At.Equal(e.Baseline.At) {
		t.Fatalf("the baseline %+v is not the record read back %+v", e.Baseline, back)
	}
}
