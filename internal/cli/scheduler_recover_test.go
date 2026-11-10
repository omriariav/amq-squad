package cli

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/schedproc"
	"github.com/omriariav/amq-squad/v3/internal/seatcommit"
)

// 3.2.0-0c-B rule 3, the verb's half: `scheduler recover` sends the lead's
// recovery over the scheduler's decision socket (the send seam) and never
// writes anything itself; its flags are checked before anything is sent;
// a refusal is exit 15 with the code; a scheduler that has not opened its
// socket is scheduler_starting. Reverted (the verb writing the chain), a
// seat's process could recover its own ledger.
func TestSchedulerRecoverSendsTheDecisionOverTheSocket(t *testing.T) {
	root, _ := filepath.EvalSymlinks(t.TempDir())
	t.Setenv("AM_ROOT", root)
	var sent []schedproc.RecoverRequest
	reply := schedproc.DecideReply{OK: true, Detail: "seat dev recovered from quarantine seq 150: the chain continues at seq 151; 1 entries carried"}
	reply.Data, _ = json.Marshal(seatcommit.RecoverResult{Handle: "dev", Slot: "/q/commits.dev.seq150", OldSeq: 150, NewSeq: 151, Base: strings.Repeat("b", 40), Note: "recovered.txt could not be restored",
		Carried: []seatcommit.LedgerCommit{{Task: "t1", Commit: strings.Repeat("a", 40), Note: "carried from quarantine seq 150"}}})
	prev := schedulerRecoverSend
	t.Cleanup(func() { schedulerRecoverSend = prev })
	schedulerRecoverSend = func(_, _ string, req schedproc.RecoverRequest) (schedproc.DecideReply, error) {
		sent = append(sent, req)
		return reply, nil
	}
	commit := strings.Repeat("a", 40)
	// the flags, before any launch record is read
	for name, args := range map[string][]string{
		"no seat":   {"recover", "--seq", "150", "--reason", "r"},
		"no seq":    {"recover", "--seat", "dev", "--reason", "r"},
		"no reason": {"recover", "--seat", "dev", "--seq", "150"},
		"slot 0":    {"recover", "--seat", "dev", "--seq", "150", "--reason", "r", "--slot", "0"},
		"bad carry": {"recover", "--seat", "dev", "--seq", "150", "--reason", "r", "--carry", "t1:abc"},
		"--me":      {"recover", "--seat", "dev", "--seq", "150", "--reason", "r", "--me", "dev"},
		"argument":  {"recover", "--seat", "dev", "--seq", "150", "--reason", "r", "extra"},
		// the decision by alarm: an alarm id, a seat and a reason, no slot
		"alarm and seq":   {"recover", "--seat", "dev", "--alarm", "3fa9c1d2", "--seq", "150", "--reason", "r"},
		"alarm and slot":  {"recover", "--seat", "dev", "--alarm", "3fa9c1d2", "--slot", "2", "--reason", "r"},
		"alarm and carry": {"recover", "--seat", "dev", "--alarm", "3fa9c1d2", "--carry", "t1:" + strings.Repeat("a", 40), "--reason", "r"},
		"alarm bad id":    {"recover", "--seat", "dev", "--alarm", "a3f9", "--reason", "r"},
		"alarm no reason": {"recover", "--seat", "dev", "--alarm", "3fa9c1d2"},
	} {
		_, _, err := captureOutput(t, func() error { return runScheduler(args) })
		var ue UsageError
		if !errors.As(err, &ue) || len(sent) != 0 {
			t.Fatalf("%s: %v (sent %d)", name, err, len(sent))
		}
	}
	if _, _, err := captureOutput(t, func() error {
		return runScheduler([]string{"recover", "--seat", "dev", "--seq", "150", "--reason", "r"})
	}); err == nil || !strings.Contains(err.Error(), "no running scheduler") || len(sent) != 0 {
		t.Fatalf("no launch record: %v (sent %d)", err, len(sent))
	}
	now := time.Now().UTC()
	if err := schedproc.WriteLaunchRecord(root, schedproc.LaunchRecord{Lead: "lead", Root: root, PID: 1, StartedAt: now}); err != nil {
		t.Fatal(err)
	}
	_, _, err := captureOutput(t, func() error {
		return runScheduler([]string{"recover", "--seat", "dev", "--seq", "150", "--reason", "r"})
	})
	if ExitCode(err) != ExitCommitRefused || !strings.Contains(err.Error(), schedproc.RefusedStarting) || len(sent) != 0 {
		t.Fatalf("a scheduler still starting: %v (sent %d)", err, len(sent))
	}
	if err := schedproc.WriteLaunchRecord(root, schedproc.LaunchRecord{Lead: "lead", Root: root, PID: 1, StartedAt: now, DecisionSocket: "/sock"}); err != nil {
		t.Fatal(err)
	}
	stdout, _, err := captureOutput(t, func() error {
		return runScheduler([]string{"recover", "--seat", "dev", "--seq", "150", "--slot", "2", "--carry", "t1:" + commit, "--reason", "the edit is understood"})
	})
	if err != nil || len(sent) != 1 {
		t.Fatalf("the send: %v (sent %d)", err, len(sent))
	}
	want := schedproc.RecoverRequest{Seat: "dev", Seq: "150", Slot: 2, Carry: []string{"t1:" + commit}, Reason: "the edit is understood"}
	if got := sent[0]; got.Seat != want.Seat || got.Seq != want.Seq || got.Slot != want.Slot || got.Reason != want.Reason || len(got.Carry) != 1 || got.Carry[0] != want.Carry[0] {
		t.Fatalf("sent %+v, want %+v", got, want)
	}
	for _, line := range []string{"scheduler recover: seat dev recovered from quarantine seq 150", "chain: seq 151 after the quarantined seq 150", "carried t1 " + commit + " (carried from quarantine seq 150)", "note: recovered.txt could not be restored"} {
		if !strings.Contains(stdout, line) {
			t.Fatalf("the output does not say %q:\n%s", line, stdout)
		}
	}
	// nothing under the root but the launch record: the verb wrote no
	// ledger, no quarantine, no seed
	for _, rel := range []string{"extensions/amq-squad/commits", "extensions/amq-squad/quarantine", "extensions/amq-squad/routing"} {
		if _, err := os.Lstat(filepath.Join(root, rel)); err == nil {
			t.Fatalf("the verb wrote %s", rel)
		}
	}
	// the decision by alarm (Codex's delta pass on c267382d): the seat, the
	// alarm and the reason are sent, no seq, slot or carry; the output names
	// the alarm and its link
	reply = schedproc.DecideReply{OK: true, Detail: "seat dev: alarm 3fa9c1d2 released by decision, a sealed line of its hold release file; the scheduler releases the seat at its next tick"}
	reply.Data, _ = json.Marshal(seatcommit.RecoverResult{Handle: "dev", Verified: true, Alarm: "3fa9c1d2"})
	stdout, _, err = captureOutput(t, func() error {
		return runScheduler([]string{"recover", "--seat", "dev", "--alarm", "3fa9c1d2", "--reason", "investigated"})
	})
	if err != nil || len(sent) != 2 {
		t.Fatalf("the decision by alarm: %v (sent %d)", err, len(sent))
	}
	if got := sent[1]; got.Seat != "dev" || got.Alarm != "3fa9c1d2" || got.Reason != "investigated" || got.Seq != "" || got.Slot != 0 || len(got.Carry) != 0 {
		t.Fatalf("sent %+v", got)
	}
	if !strings.Contains(stdout, "alarm 3fa9c1d2 released by decision") || !strings.Contains(stdout, "alarm 3fa9c1d2: the decision is a sealed line of the seat's hold release file") {
		t.Fatalf("the output:\n%s", stdout)
	}
	// a refusal
	reply = schedproc.DecideReply{Code: schedproc.RefusedNotLead, Detail: "pid 7 does not descend from the lead"}
	_, _, err = captureOutput(t, func() error {
		return runScheduler([]string{"recover", "--seat", "dev", "--seq", "150", "--reason", "r"})
	})
	if ExitCode(err) != ExitCommitRefused || !strings.Contains(err.Error(), "scheduler recover refused (not_lead)") {
		t.Fatalf("a refusal: %v", err)
	}
}

// B7: the sidecar's binary version equals `amq-squad version`'s output for
// the built binary: Run sets seatcommit.BinaryVersion from the version
// main resolved (the ldflag), and a quarantine made after that records it.
// Reverted (the line not set), the sidecar names the module's vcs
// revision. The quarantine is made under a temp root (rule 18).
func TestSidecarCarriesTheVersionTheCLIPrints(t *testing.T) {
	prev := seatcommit.BinaryVersion
	t.Cleanup(func() { seatcommit.BinaryVersion = prev })
	seatcommit.BinaryVersion = ""
	stdout, _, err := captureOutput(t, func() error { return Run([]string{"version"}, "3.2.0-test.7") })
	if err != nil || strings.TrimSpace(stdout) != "amq-squad 3.2.0-test.7" {
		t.Fatalf("version: %v %q", err, stdout)
	}
	if seatcommit.BinaryVersion != "3.2.0-test.7" {
		t.Fatalf("BinaryVersion is %q after Run", seatcommit.BinaryVersion)
	}
	root := t.TempDir()
	base := strings.Repeat("a", 40)
	if _, err := seatcommit.RecordBase(root, "dev", filepath.Join(root, "wt"), "seat/dev", base, time.Now()); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(seatcommit.LedgerDir(root), "dev.json")
	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(strings.Replace(string(raw), base, strings.Repeat("0", 40), 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = seatcommit.ReadLedger(root, "dev")
	var q *seatcommit.QuarantinedError
	if !errors.As(err, &q) {
		t.Fatalf("no quarantine: %v", err)
	}
	side, err := os.ReadFile(filepath.Join(q.Slot, "quarantine.txt"))
	if err != nil || !strings.Contains(string(side), "\nbinary: 3.2.0-test.7\n") {
		t.Fatalf("the sidecar's binary line: %v\n%s", err, side)
	}
}
