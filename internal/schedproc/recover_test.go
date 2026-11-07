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
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/launch"
	"github.com/omriariav/amq-squad/v3/internal/panechain"
	"github.com/omriariav/amq-squad/v3/internal/scheduler"
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
	if len(failed) != 1 || failed[0].ReasonCode != scheduler.ReasonLedgerTampered || !strings.Contains(failed[0].Reason, "quarantined at "+slot+" (seq 1; alarm ") {
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

// Codex's delta pass on c267382d (1), the plan reviewer's ruling, on real
// files through the decision socket's apply step: a seat held by an alarm
// that quarantined nothing (restored from a state file, its task blocked
// with the alarm's detail). A wrong alarm id is refused
// recover_alarm_not_held, nothing written. The document put back verifies, ticks alone release nothing (no slot names
// the hold), and the decision naming the hold's alarm is OK: the record
// names the alarm, the decision line says so, and the next ticks release
// the seat. The same id sent once the hold is gone is refused
// recover_alarm_not_held. Reverted (the held-alarm check), the wrong id is
// accepted. (A decision by alarm on a ledger that does not verify copies it
// and recovers from the copy: TestTheAlarmVerbRecoversALedgerWhoseCopyFailed.)
func TestTheAlarmVerbReleasesAHoldWithNoSlot(t *testing.T) {
	r := newTamperedSeatRig(t)
	const alarm = "3fa9c1d2"
	detail := "seat " + r.handle + ": the refused document could not be quarantined (seq 1; alarm " + alarm + "): the copy failed"
	if _, err := r.store.Block(r.tk.ID, "", scheduler.ReasonLedgerTampered, detail, task.RouteLead, r.now); err != nil {
		t.Fatal(err)
	}
	r.s.RestoreLedgerTampers(scheduler.State{LedgerTampered: []scheduler.LedgerTamper{{Handle: r.handle, Seq: "1", Detail: detail, At: r.now, Tasks: []string{r.tk.ID}, Alarm: alarm}}})
	decide := recoverDecision(r.s, r.root, Recovery{Root: r.root, Target: r.target, Git: "git", Now: func() time.Time { return r.now }}.Apply, func() time.Time { return r.now })
	p := filepath.Join(seatcommit.LedgerDir(r.root), r.handle+".json")
	if reply := decide("lead", RecoverRequest{Seat: r.handle, Alarm: "0badcafe", Reason: "the wrong alarm"}); reply.OK || reply.Code != RefusedRecoverAlarmNotHeld || !strings.Contains(reply.Detail, alarm) {
		t.Fatalf("a wrong alarm id: %+v", reply)
	}
	if err := os.WriteFile(p, r.original, 0o600); err != nil {
		t.Fatal(err)
	}
	if all := r.ticks(5); len(kinds(all, "ledger_hold_released")) != 0 || len(kinds(all, "dispatched")) != 0 {
		t.Fatalf("ticks alone released a hold with no slot: %+v", all)
	}
	reply := decide("lead", RecoverRequest{Seat: r.handle, Alarm: alarm, Reason: "the copy failed; the document is back"})
	var res seatcommit.RecoverResult
	if !reply.OK || json.Unmarshal(reply.Data, &res) != nil || res.Alarm != alarm || !res.Verified {
		t.Fatalf("the decision by alarm: %+v", reply)
	}
	if log, err := seatcommit.ReadAlarms(r.root, r.handle); err != nil || len(log.Decisions) != 1 || log.Decisions[0].Alarm != alarm || log.Decisions[0].Lead != "lead" {
		t.Fatalf("the hold release file: %v %+v", err, log)
	}
	all := r.ticks(3)
	if !has(all, "ledger_hold_released", "", r.handle) || !has(all, "task_reset", r.tk.ID, r.handle) || !has(all, "dispatched", r.tk.ID, r.handle) {
		t.Fatalf("the decision by alarm did not release the hold: %+v", all)
	}
	lines, _ := ReadDecisionsTail(r.root)
	found := false
	for _, l := range lines {
		found = found || (l.Kind == DecisionLedgerRecovered && strings.Contains(l.Reason, "released alarm "+alarm+" of seat "+r.handle))
	}
	if !found {
		t.Fatalf("no decision line naming the alarm: %+v", lines)
	}
	if reply := decide("lead", RecoverRequest{Seat: r.handle, Alarm: alarm, Reason: "again"}); reply.OK || reply.Code != RefusedRecoverAlarmNotHeld {
		t.Fatalf("the old alarm once its hold is gone: %+v", reply)
	}
}

// alarmOf is the alarm id a tamper block's detail names.
func alarmOf(t *testing.T, detail string) string {
	t.Helper()
	m := regexp.MustCompile(`; alarm ([0-9a-f]{8})\)`).FindStringSubmatch(detail)
	if m == nil {
		t.Fatalf("no alarm in %q", detail)
	}
	return m[1]
}

// copyOnlyRe is the decision by alarm's copy-only refusal (the plan
// reviewer's ruling on Codex's pass on 8cffafe7), with the slot and the seq.
var copyOnlyRe = regexp.MustCompile("^seat (\\S+)'s ledger does not verify; it was copied to slot (\\S+); recover it with `amq-squad scheduler recover --seat (\\S+) --seq (\\S+) --reason \\.\\.\\.`, which shows what it carries; then, if the hold remains, send the alarm decision again$")

// Codex on 254f2c2c (P2-1) and on 8cffafe7, the reviewer's two steps, on
// real files through the apply step and the production constructor
// (NewRecovery: Git, env and clock omitted, as scheduler run builds it).
// The body corrupted while the head decodes and the quarantine copy
// refused (its directory's mode 0755): the prepare's alarm holds the seat
// with no slot. The decision by alarm is refused in words while the copy
// still fails. The mode repaired, the decision by alarm makes only the
// lead's copy and is refused with the slot and the slot recovery to run,
// the ledger unchanged (no rebuild). The slot recovery (step one) shows
// what it carries and continues the chain; the hold, which has no slot,
// stays. The decision by alarm sent again (step two) seals and the next
// ticks release the seat. Reverted (no Git default), step one fails.
func TestACopyFailedHoldIsReleasedInTwoSteps(t *testing.T) {
	r := newTamperedSeatRig(t)
	qdir := seatcommit.QuarantineDir(r.root)
	if err := os.MkdirAll(qdir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(qdir, 0o755); err != nil {
		t.Fatal(err)
	}
	r.ticks(3)
	got, _ := r.store.Get(r.tk.ID)
	if got.State != task.StateBlocked || !strings.Contains(got.BlockDetail, "could not be quarantined (seq unknown; alarm ") {
		t.Fatalf("not a hold with no slot: %+v", got)
	}
	alarm := alarmOf(t, got.BlockDetail)
	decide := recoverDecision(r.s, r.root, NewRecovery(r.root, r.target).Apply, func() time.Time { return r.now })
	p := filepath.Join(seatcommit.LedgerDir(r.root), r.handle+".json")
	if reply := decide("lead", RecoverRequest{Seat: r.handle, Alarm: alarm, Reason: "too early"}); reply.OK || reply.Code != seatcommit.RecoverAlarmUnverified || !strings.Contains(reply.Detail, "could not be made") || !strings.Contains(reply.Detail, "0755") {
		t.Fatalf("the decision while the copy fails: %+v", reply)
	}
	if err := os.Chmod(qdir, 0o700); err != nil {
		t.Fatal(err)
	}
	reply := decide("lead", RecoverRequest{Seat: r.handle, Alarm: alarm, Reason: "the quarantine's mode is fixed"})
	m := copyOnlyRe.FindStringSubmatch(reply.Detail)
	if reply.OK || reply.Code != seatcommit.RecoverAlarmUnverified || m == nil || m[1] != r.handle || m[3] != r.handle || m[4] != "1" {
		t.Fatalf("the copy-only refusal: %+v", reply)
	}
	slot := m[2]
	if b, err := os.ReadFile(p); err != nil || !bytes.Equal(b, r.tampered) {
		t.Fatalf("the decision by alarm rebuilt the ledger: %v", err)
	}
	if b, err := os.ReadFile(filepath.Join(slot, "body.json")); err != nil || !bytes.Equal(b, r.tampered) {
		t.Fatalf("the slot is not the live bytes: %v", err)
	}
	if all := r.ticks(3); len(kinds(all, "ledger_hold_released")) != 0 {
		t.Fatalf("the copy released the hold: %+v", all)
	}
	// step one: the slot recovery the refusal names
	one := decide("lead", RecoverRequest{Seat: r.handle, Seq: m[4], Slot: 1, Reason: "the copy, read"})
	var res seatcommit.RecoverResult
	if !one.OK || json.Unmarshal(one.Data, &res) != nil || res.Slot != slot || len(res.Carried) != 0 {
		t.Fatalf("step one, the slot recovery: %+v", one)
	}
	if all := r.ticks(3); len(kinds(all, "ledger_hold_released")) != 0 {
		t.Fatalf("the slot recovery released a hold with no slot: %+v", all)
	}
	// step two: the decision by alarm again, on the ledger that verifies
	if two := decide("lead", RecoverRequest{Seat: r.handle, Alarm: alarm, Reason: "the slot is recovered"}); !two.OK {
		t.Fatalf("step two, the decision by alarm: %+v", two)
	}
	all := r.ticks(3)
	if !has(all, "ledger_hold_released", "", r.handle) || !has(all, "task_reset", r.tk.ID, r.handle) || !has(all, "dispatched", r.tk.ID, r.handle) {
		t.Fatalf("the two steps did not release the hold: %+v", all)
	}
}

// Codex on 8cffafe7 (3): the lead looked at a ledger that verified, and it
// changed before the decision by alarm was applied. The decision makes
// only the copy of what it found and refuses with the slot and the slot
// recovery to run; the ledger is left as it was found (never rebuilt
// unseen), no alarm record is sealed and no tick releases the hold.
// Reverted (the decision recovers from its copy), the changed ledger is
// rebuilt and the hold released.
func TestALedgerChangedBeforeTheAlarmDecisionIsCopiedNotRebuilt(t *testing.T) {
	r := newTamperedSeatRig(t)
	p := filepath.Join(seatcommit.LedgerDir(r.root), r.handle+".json")
	if err := os.WriteFile(p, r.original, 0o600); err != nil {
		t.Fatal(err)
	}
	const alarm = "3fa9c1d2"
	detail := "seat " + r.handle + ": the refused document could not be quarantined (seq 1; alarm " + alarm + "): the copy failed"
	if _, err := r.store.Block(r.tk.ID, "", scheduler.ReasonLedgerTampered, detail, task.RouteLead, r.now); err != nil {
		t.Fatal(err)
	}
	r.s.RestoreLedgerTampers(scheduler.State{LedgerTampered: []scheduler.LedgerTamper{{Handle: r.handle, Seq: "1", Detail: detail, At: r.now, Tasks: []string{r.tk.ID}, Alarm: alarm}}})
	// the lead's look: the ledger verifies
	if _, err := seatcommit.ReadLedger(r.root, r.handle); err != nil {
		t.Fatalf("the look: %v", err)
	}
	// it changes before the decision is applied
	if err := os.WriteFile(p, r.tampered, 0o600); err != nil {
		t.Fatal(err)
	}
	decide := recoverDecision(r.s, r.root, NewRecovery(r.root, r.target).Apply, func() time.Time { return r.now })
	reply := decide("lead", RecoverRequest{Seat: r.handle, Alarm: alarm, Reason: "looked at it a moment ago"})
	m := copyOnlyRe.FindStringSubmatch(reply.Detail)
	if reply.OK || reply.Code != seatcommit.RecoverAlarmUnverified || m == nil {
		t.Fatalf("the decision on a changed ledger: %+v", reply)
	}
	if b, err := os.ReadFile(p); err != nil || !bytes.Equal(b, r.tampered) {
		t.Fatalf("the changed ledger was rebuilt: %v", err)
	}
	if b, err := os.ReadFile(filepath.Join(m[2], "body.json")); err != nil || !bytes.Equal(b, r.tampered) {
		t.Fatalf("the copy is not the changed bytes: %v", err)
	}
	if _, alarms, _, _ := seatcommit.RecoveriesOf(r.root, r.handle); len(alarms) != 0 {
		t.Fatalf("an alarm record was sealed: %v", alarms)
	}
	if all := r.ticks(3); len(kinds(all, "ledger_hold_released")) != 0 {
		t.Fatalf("the hold was released: %+v", all)
	}
}

// Codex on 254f2c2c (P2-2), real writers with barriers for exactly that
// interleaving: A's decision by alarm passes the socket's check and waits
// (the seam); A's hold is released by its slot's recovery; B holds the
// seat and B's decision by alarm is sealed (OK); A resumes. Its check
// inside the ledger lock, right before the seal, finds B holding the seat:
// A is refused recover_alarm_not_held and nothing is written, the record
// still names B, and the next ticks release B. Reverted (the check only
// outside the lock), A's late write replaces B's record and B stays held.
func TestADelayedAlarmDecisionNeverSealsOverANewerRecovery(t *testing.T) {
	r := newTamperedSeatRig(t)
	r.ticks(3)
	a, ok := r.s.HeldAlarm(r.handle)
	if !ok {
		t.Fatal("no hold A")
	}
	decide := recoverDecision(r.s, r.root, Recovery{Root: r.root, Target: r.target, Git: "git", Now: func() time.Time { return r.now }}.Apply, func() time.Time { return r.now })
	reached, resume := make(chan struct{}), make(chan struct{})
	prev := recoverAfterHeldCheck
	t.Cleanup(func() { recoverAfterHeldCheck = prev })
	var once sync.Once
	recoverAfterHeldCheck = func() {
		first := false
		once.Do(func() { first = true })
		if first {
			close(reached)
			<-resume
		}
	}
	done := make(chan DecideReply, 1)
	go func() { done <- decide("lead", RecoverRequest{Seat: r.handle, Alarm: a, Reason: "A, investigated"}) }()
	<-reached
	// A's hold released by its slot
	if reply := decide("lead", RecoverRequest{Seat: r.handle, Seq: "1", Slot: 1, Reason: "A's slot"}); !reply.OK {
		t.Fatalf("A's slot recovery: %+v", reply)
	}
	if all := r.ticks(3); !has(all, "ledger_hold_released", "", r.handle) {
		t.Fatalf("A was not released: %+v", all)
	}
	// B holds the seat; B's decision by alarm is sealed
	const b = "b0000002"
	r.s.RestoreLedgerTampers(scheduler.State{LedgerTampered: []scheduler.LedgerTamper{{Handle: r.handle, Seq: "2", Detail: "seat " + r.handle + ": the refused document could not be quarantined (seq 2; alarm " + b + "): x", At: r.now, Alarm: b}}})
	if reply := decide("lead", RecoverRequest{Seat: r.handle, Alarm: b, Reason: "B, investigated"}); !reply.OK {
		t.Fatalf("B's decision: %+v", reply)
	}
	close(resume)
	replyA := <-done
	if replyA.OK || replyA.Code != RefusedRecoverAlarmNotHeld {
		t.Fatalf("A after B's recovery: %+v", replyA)
	}
	if _, alarms, _, ok := seatcommit.RecoveriesOf(r.root, r.handle); !ok || !slices.Contains(alarms, b) || slices.Contains(alarms, a) {
		t.Fatalf("the records after A resumed: %v %v", ok, alarms)
	}
	if all := r.ticks(2); !has(all, "ledger_hold_released", "", r.handle) {
		t.Fatalf("B was not released: %+v", all)
	}
	if _, held := r.s.HeldAlarm(r.handle); held {
		t.Fatal("the seat is still held")
	}
}

// The plan reviewer's ruling on Codex's pass on 254f2c2c, the root fix,
// now in the hold release file (8cffafe7's successor): B holds the seat
// and B's decision by alarm is written; then an older alarm A's line lands
// after it (a writer with no guard: a decision whose check passed long
// before). The file keeps both lines, and the next ticks release B, which
// stays released. Reverted (the append replacing the file), B is never
// released.
func TestAnOlderAlarmsRecordLandingAfterBsLeavesBReleased(t *testing.T) {
	r := newTamperedSeatRig(t)
	p := filepath.Join(seatcommit.LedgerDir(r.root), r.handle+".json")
	if err := os.WriteFile(p, r.original, 0o600); err != nil {
		t.Fatal(err)
	}
	const a, b = "a0000001", "b0000002"
	r.s.RestoreLedgerTampers(scheduler.State{LedgerTampered: []scheduler.LedgerTamper{{Handle: r.handle, Seq: "1", Detail: "seat " + r.handle + ": the refused document could not be quarantined (seq 1; alarm " + b + "): x", At: r.now, Alarm: b}}})
	decide := recoverDecision(r.s, r.root, Recovery{Root: r.root, Target: r.target, Git: "git", Now: func() time.Time { return r.now }}.Apply, func() time.Time { return r.now })
	if reply := decide("lead", RecoverRequest{Seat: r.handle, Alarm: b, Reason: "B, investigated"}); !reply.OK {
		t.Fatalf("B's decision: %+v", reply)
	}
	// A's record lands after B's
	if _, err := seatcommit.RecoverLedger(seatcommit.RecoverOptions{Root: r.root, Handle: r.handle, Alarm: a, Lead: "lead", Reason: "A, late", Now: r.now}); err != nil {
		t.Fatalf("A's late record: %v", err)
	}
	all := r.ticks(3)
	if !has(all, "ledger_hold_released", "", r.handle) {
		t.Fatalf("B was not released after A's record landed: %+v", all)
	}
	if _, alarms, _, ok := seatcommit.RecoveriesOf(r.root, r.handle); !ok || !slices.Equal(alarms, []string{b, a}) {
		t.Fatalf("the ledger's alarm records: %v %v, want [%s %s]", ok, alarms, b, a)
	}
	if more := r.ticks(5); len(kinds(more, "ledger_hold_released")) != 0 {
		t.Fatalf("a second release: %+v", more)
	}
	if _, held := r.s.HeldAlarm(r.handle); held {
		t.Fatal("B is held again")
	}
}

// noSlotHold restores a hold with no slot on the rig's seat, its task
// blocked with the hold's detail, on a ledger that verifies.
func noSlotHold(t *testing.T, r *tamperedSeatRig, alarm string) {
	t.Helper()
	p := filepath.Join(seatcommit.LedgerDir(r.root), r.handle+".json")
	if err := os.WriteFile(p, r.original, 0o600); err != nil {
		t.Fatal(err)
	}
	detail := "seat " + r.handle + ": the refused document could not be quarantined (seq 1; alarm " + alarm + "): the copy failed"
	if _, err := r.store.Block(r.tk.ID, "", scheduler.ReasonLedgerTampered, detail, task.RouteLead, r.now); err != nil {
		t.Fatal(err)
	}
	r.s.RestoreLedgerTampers(scheduler.State{LedgerTampered: []scheduler.LedgerTamper{{Handle: r.handle, Seq: "1", Detail: detail, At: r.now, Tasks: []string{r.tk.ID}, Alarm: alarm}}})
}

// The plan reviewer's ruling on Codex's pass on 8cffafe7, on real files: a
// valid line of the hold release file followed by a torn tail (a crash
// mid-write) still releases its alarm; the torn tail is skipped and logged
// once (hold_release_line_skipped). Reverted (a torn tail fails the whole
// file), the seat stays held.
func TestAValidReleaseLineBeforeATornTailReleasesItsAlarm(t *testing.T) {
	r := newTamperedSeatRig(t)
	const alarm = "3fa9c1d2"
	noSlotHold(t, r, alarm)
	decide := recoverDecision(r.s, r.root, NewRecovery(r.root, r.target).Apply, func() time.Time { return r.now })
	if reply := decide("lead", RecoverRequest{Seat: r.handle, Alarm: alarm, Reason: "investigated"}); !reply.OK {
		t.Fatalf("the decision: %+v", reply)
	}
	f, err := os.OpenFile(seatcommit.AlarmsPath(r.root, r.handle), os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(`{"seat":"` + r.handle + `","alarm":"0bad`); err != nil {
		t.Fatal(err)
	}
	f.Close()
	all := r.ticks(3)
	if !has(all, "ledger_hold_released", "", r.handle) || !has(all, "task_reset", r.tk.ID, r.handle) {
		t.Fatalf("the valid line did not release its alarm: %+v", all)
	}
	if skipped := kinds(all, "hold_release_line_skipped"); len(skipped) != 1 || !strings.Contains(skipped[0].Reason, "a torn last line") {
		t.Fatalf("the torn tail was not logged once: %+v", skipped)
	}
}

// Codex on 8cffafe7 (2), on real files: B's decision by alarm is sealed in
// the hold release file; before the tick reads it, the ledger is refused
// and recovered from its slot (a chain continuation, a new ledger body).
// The decision is not in the ledger body, so the continuation cannot drop
// it: the next ticks release B. (With the decisions in the ledger body,
// as in 37379e5b, this continuation dropped them.)
func TestAnAlarmDecisionSurvivesALaterContinuation(t *testing.T) {
	r := newTamperedSeatRig(t)
	const b = "b0000002"
	noSlotHold(t, r, b)
	decide := recoverDecision(r.s, r.root, NewRecovery(r.root, r.target).Apply, func() time.Time { return r.now })
	if reply := decide("lead", RecoverRequest{Seat: r.handle, Alarm: b, Reason: "B, investigated"}); !reply.OK {
		t.Fatalf("B's decision: %+v", reply)
	}
	// the continuation, before any tick: the ledger refused, then recovered
	p := filepath.Join(seatcommit.LedgerDir(r.root), r.handle+".json")
	if err := os.WriteFile(p, r.tampered, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := seatcommit.ReadLedgerAs(r.root, r.handle, "a reader"); err == nil {
		t.Fatal("the tampered ledger verified")
	}
	if reply := decide("lead", RecoverRequest{Seat: r.handle, Seq: "1", Slot: 1, Reason: "the slot, read"}); !reply.OK {
		t.Fatalf("the continuation: %+v", reply)
	}
	if _, alarms, _, ok := seatcommit.RecoveriesOf(r.root, r.handle); !ok || !slices.Contains(alarms, b) {
		t.Fatalf("B's decision after the continuation: %v %v", ok, alarms)
	}
	if all := r.ticks(3); !has(all, "ledger_hold_released", "", r.handle) || !has(all, "task_reset", r.tk.ID, r.handle) {
		t.Fatalf("B was not released after the continuation: %+v", all)
	}
}
