package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/omriariav/amq-squad/v3/internal/schedproc"
	"github.com/omriariav/amq-squad/v3/internal/seatcommit"
)

// schedulerRecoverSend sends the recovery over the decision socket (a test
// seam).
var schedulerRecoverSend = schedproc.SendRecover

// runSchedulerRecover is the lead's recovery of a seat's quarantined
// commits ledger (3.2.0-0c-B rule 3): the decision is sent over the
// scheduler's decision socket and proved under R4 like a widening; the
// scheduler writes the new chain. This verb never touches the ledger, the
// quarantine or the seed.
func runSchedulerRecover(args []string, out io.Writer) error {
	f := newStoreFlags("scheduler recover")
	f.fs.Usage = func() { fmt.Fprint(os.Stderr, schedulerUsage) }
	seat := f.fs.String("seat", "", "the seat whose commits ledger is recovered")
	seq := f.fs.String("seq", "", "the quarantined head seq in the slot's name (N of commits.H.seqN), or unknown")
	slot := f.fs.Int("slot", 1, "the slot's suffix K of commits.H.seqN.K (1 for the first slot)")
	reason := f.fs.String("reason", "", "why the lead recovers the ledger")
	alarm := f.fs.String("alarm", "", "a decision by alarm: the hold's alarm id from its stop line (on a ledger that verifies now; no --seq, --slot or --carry)")
	var carry multiFlag
	f.fs.Var(&carry, "carry", "a quarantined entry to carry, TASK:COMMIT (repeatable; checked against origin)")
	if err := parseFlags(f.fs, args); err != nil {
		return err
	}
	if f.fs.NArg() > 0 {
		return usageErrorf("scheduler recover: takes no arguments")
	}
	if flagWasSet(f.fs, "me") {
		return usageErrorf("scheduler recover: takes no --me; the scheduler proves the lead from the process, never from a flag")
	}
	byAlarm := flagWasSet(f.fs, "alarm")
	switch {
	case byAlarm && (flagWasSet(f.fs, "seq") || flagWasSet(f.fs, "slot") || len(carry) > 0):
		return usageErrorf("scheduler recover --alarm: names the hold's alarm, not a slot: no --seq, --slot or --carry")
	case byAlarm && !seatcommit.AlarmIDRe.MatchString(strings.TrimSpace(*alarm)):
		return usageErrorf("scheduler recover --alarm: the alarm id is the eight hex characters the seat's stop line shows")
	case byAlarm && (strings.TrimSpace(*seat) == "" || strings.TrimSpace(*reason) == ""):
		return usageErrorf("scheduler recover --alarm: --seat and --reason are required")
	case !byAlarm && (strings.TrimSpace(*seat) == "" || strings.TrimSpace(*seq) == "" || strings.TrimSpace(*reason) == ""):
		return usageErrorf("scheduler recover: --seat, --seq and --reason are required (or --seat, --alarm and --reason)")
	case !byAlarm && *slot < 1:
		return usageErrorf("scheduler recover: --slot is the slot's suffix, 1 or more")
	}
	if _, err := schedproc.ParseCarry(carry); err != nil {
		return usageErrorf("scheduler recover --carry: %v", err)
	}
	_, root, release, err := f.store(false)
	if err != nil {
		return err
	}
	release()
	rec, err := schedproc.ReadLaunchRecord(root)
	if err != nil {
		return fmt.Errorf("scheduler recover: no running scheduler for %s (the recovery is the scheduler's to write): %v", root, err)
	}
	req := schedproc.RecoverRequest{Seat: strings.TrimSpace(*seat), Seq: strings.TrimSpace(*seq), Slot: *slot, Carry: carry, Reason: strings.TrimSpace(*reason)}
	if byAlarm {
		req = schedproc.RecoverRequest{Seat: strings.TrimSpace(*seat), Alarm: strings.TrimSpace(*alarm), Reason: strings.TrimSpace(*reason)}
	}
	var reply schedproc.DecideReply
	if rec.DecisionSocket == "" {
		reply = schedproc.DecideReply{Code: schedproc.RefusedStarting, Detail: "the scheduler is starting: its decision socket opens once the exits ledger is loaded and every seat of the previous incarnation is closed and confirmed gone"}
	} else if reply, err = schedulerRecoverSend(root, rec.DecisionSocket, req); err != nil {
		return fmt.Errorf("scheduler recover: the scheduler's decision socket: %w", err)
	}
	if *f.jsonOut {
		if err := writeJSONEnvelope(out, "scheduler_recover", reply); err != nil {
			return err
		}
	} else if reply.OK {
		fmt.Fprintf(out, "scheduler recover: %s\n", reply.Detail)
		var res seatcommit.RecoverResult
		if json.Unmarshal(reply.Data, &res) == nil && res.Alarm != "" {
			fmt.Fprintf(out, "  alarm %s: the decision is a sealed line of the seat's hold release file\n", res.Alarm)
		} else if json.Unmarshal(reply.Data, &res) == nil {
			seqFrom := res.SeqFrom
			if seqFrom == "" {
				seqFrom = "verified_head"
			}
			fmt.Fprintf(out, "  slot %s\n  chain: seq %d after the quarantined seq %d (sequence from %s); base %s\n", res.Slot, res.NewSeq, res.OldSeq, seqFrom, res.Base)
			for _, c := range res.Carried {
				fmt.Fprintf(out, "  carried %s %s (%s)\n", c.Task, c.Commit, c.Note)
			}
		}
	}
	if !reply.OK {
		return &codedExitError{code: ExitCommitRefused, msg: fmt.Sprintf("scheduler recover refused (%s): %s", reply.Code, reply.Detail)}
	}
	return nil
}
