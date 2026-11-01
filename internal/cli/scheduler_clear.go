package cli

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/omriariav/amq-squad/v3/internal/schedproc"
)

// schedulerClearSend sends the clear over the decision socket (a test seam).
var schedulerClearSend = schedproc.SendClear

// runSchedulerClear is the clear key's verb (brief 31-2, key 4): the owner's
// acknowledgement of a seat's warning line on one launch, sent over the
// scheduler's decision socket and proved under R4 like a relaunch. A clear
// is never a gate and never changes a seat's state.
func runSchedulerClear(args []string, out io.Writer) error {
	f := newStoreFlags("scheduler clear")
	f.fs.Usage = func() { fmt.Fprint(os.Stderr, schedulerUsage) }
	seat := f.fs.String("seat", "", "the seat whose warning line is cleared")
	warning := f.fs.String("warning", "", "the warning's code (the text before its first colon)")
	launchID := f.fs.String("launch", "", "the seat's launch id the line belongs to")
	if err := parseFlags(f.fs, args); err != nil {
		return err
	}
	if f.fs.NArg() > 0 {
		return usageErrorf("scheduler clear: takes no arguments")
	}
	if flagWasSet(f.fs, "me") {
		return usageErrorf("scheduler clear: takes no --me; the scheduler proves the lead from the process, never from a flag")
	}
	if strings.TrimSpace(*seat) == "" || strings.TrimSpace(*warning) == "" || strings.TrimSpace(*launchID) == "" {
		return usageErrorf("scheduler clear: --seat, --warning and --launch are required")
	}
	_, root, release, err := f.store(false)
	if err != nil {
		return err
	}
	release()
	rec, err := schedproc.ReadLaunchRecord(root)
	if err != nil {
		return fmt.Errorf("scheduler clear: no running scheduler for %s (the clear is the scheduler's to record): %v", root, err)
	}
	var reply schedproc.DecideReply
	req := schedproc.ClearRequest{Seat: strings.TrimSpace(*seat), Warning: strings.TrimSpace(*warning), LaunchID: strings.TrimSpace(*launchID)}
	if rec.DecisionSocket == "" {
		reply = schedproc.DecideReply{Code: schedproc.RefusedStarting, Detail: "the scheduler is starting: its decision socket opens once the exits ledger is loaded and every seat of the previous incarnation is closed and confirmed gone"}
	} else if reply, err = schedulerClearSend(root, rec.DecisionSocket, req); err != nil {
		return fmt.Errorf("scheduler clear: the scheduler's decision socket: %w", err)
	}
	if *f.jsonOut {
		if err := writeJSONEnvelope(out, "scheduler_clear", reply); err != nil {
			return err
		}
	} else if reply.OK {
		fmt.Fprintf(out, "scheduler clear: %s\n", reply.Detail)
	}
	if !reply.OK {
		return &codedExitError{code: ExitCommitRefused, msg: fmt.Sprintf("scheduler clear refused (%s): %s", reply.Code, reply.Detail)}
	}
	return nil
}
