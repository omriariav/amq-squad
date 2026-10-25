package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/grants"
	"github.com/omriariav/amq-squad/v3/internal/schedproc"
	"github.com/omriariav/amq-squad/v3/internal/seatcommit"
	"github.com/omriariav/amq-squad/v3/internal/task"
)

const taskWidenUsage = `amq-squad task widen - the lead's decision to add paths to a task's declared files

Usage:
  amq-squad task widen ID --files GLOB,... --reason TEXT [--root DIR | --project P --session S] [--json]

A recorded lead decision (plan 4.3, 7.2): the paths join the task's
declared_files inside the grant. The verb takes no --me: it sends the
decision over the scheduler's own Unix socket (named in the scheduler's
launch record), and the scheduler applies it only when the kernel-named
sending process descends from the lead's pane (the pane AMQ labelled with
the lead handle of its launch record; the parent chain with start stamps at
every hop) and the task is its squad's (milestone B second pass, ruling R4).
A task blocked by a commit refusal is re-armed and the commit step runs
again. The verb also sends the lead's AMQ message to the scheduler mailbox as
the record of the decision; the message carries no authority. Refusals:
  not_lead              the sender does not descend from the lead's pane
  origin_unknown        the sender's origin cannot be read (no kernel peer, a
                        vanished hop, no or two lead panes)
  not_squad             the task is not this squad's
  self_widen            the seat that holds or reported the task asked
  file_lock_conflict    the widened set overlaps another active task's files
  growth_gate_required  the widening covers a protected path (a managed
                        block included), or leaves the seat's diff over the
                        size cap: the owner-signed growth gate for that exact
                        crossing is raised and the task goes to the owner;
                        once the owner accepts it, the same widen goes through.
Exit 15: refused.
`

// taskWidenData is the scheduler's decision on one widening (the reply's
// JSON, and the verb's envelope once sent).
type taskWidenData struct {
	Root     string                   `json:"root"`
	Decision seatcommit.WidenDecision `json:"decision"`
	GateIDs  []string                 `json:"gate_ids,omitempty"`
	GateErrs []string                 `json:"gate_errors,omitempty"`
	Task     *task.Task               `json:"task,omitempty"`
}

// taskWidenSend sends the widening message with the caller's AMQ identity
// (a test seam).
var taskWidenSend = func(root string, args []string) ([]byte, error) {
	amq, err := exec.LookPath("amq")
	if err != nil {
		return nil, err
	}
	if root != "" {
		args = append([]string{args[0], "--root", root}, args[1:]...)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, amq, args...)
	cmd.Env = append(os.Environ(), "AMQ_NO_UPDATE_CHECK=1")
	return cmd.CombinedOutput()
}

func runTaskWiden(args []string, out io.Writer) error {
	id, rest, _ := peelPositional(args)
	f := newTaskFlags("widen")
	f.fs.Usage = func() { fmt.Fprint(os.Stderr, taskWidenUsage) }
	files := f.fs.String("files", "", "comma-separated globs to add (repo-relative)")
	reason := f.fs.String("reason", "", "why the lead widens the task")
	if err := parseFlags(f.fs, rest); err != nil {
		return err
	}
	if id == "" || f.fs.NArg() > 0 || strings.TrimSpace(*files) == "" || strings.TrimSpace(*reason) == "" {
		return usageErrorf("task widen: ID, --files and --reason are required")
	}
	if flagWasSet(f.fs, "me") {
		return usageErrorf("task widen: takes no --me; the scheduler proves the lead from the process, never from a flag")
	}
	if err := task.ValidID(id); err != nil {
		return usageErrorf("task widen: %v", err)
	}
	_, root, release, err := f.store(false)
	if err != nil {
		return err
	}
	release()
	rec, err := schedproc.ReadLaunchRecord(root)
	if err != nil {
		return fmt.Errorf("task widen: no running scheduler for %s (the widening is the scheduler's to apply): %v", root, err)
	}
	req := schedproc.WidenRequest{Kind: schedproc.WidenRequestKind, Task: id, Files: splitList(*files), Reason: strings.TrimSpace(*reason)}
	var reply schedproc.DecideReply
	if rec.DecisionSocket == "" {
		// The fourth pass: the socket opens only after the start-up barrier.
		reply = schedproc.DecideReply{Code: schedproc.RefusedStarting, Detail: "the scheduler is starting: its decision socket opens once the exits ledger is loaded and every seat of the previous incarnation is closed and confirmed gone"}
	} else {
		if reply, err = schedproc.SendDecision(rec.DecisionSocket, req); err != nil {
			return fmt.Errorf("task widen: the scheduler's decision socket: %w", err)
		}
		// The lead's AMQ message is the record of the decision, never its
		// authority (R4).
		body, _ := json.Marshal(map[string]any{"request": req, "decision": reply})
		if sent, err := taskWidenSend(root, []string{"send", "--to", schedproc.Handle, "--kind", "status", "--subject", "widen " + id + " (record)", "--body", string(body)}); err != nil {
			fmt.Fprintf(os.Stderr, "task widen: the AMQ record of the decision was not sent: %v: %s\n", err, strings.TrimSpace(string(sent)))
		}
	}
	if *f.jsonOut {
		if err := writeJSONEnvelope(out, "task_widen", reply); err != nil {
			return err
		}
	} else if reply.OK {
		fmt.Fprintf(out, "task widen %s: applied by the scheduler\n", id)
	}
	if !reply.OK {
		return &codedExitError{code: ExitCommitRefused, msg: fmt.Sprintf("task widen %s refused (%s): %s", id, reply.Code, reply.Detail)}
	}
	return nil
}

// widenDecision is the scheduler's apply step for a decision its socket
// proved to come from the lead's pane (R4): the task must be this squad's,
// then applyLeadWiden.
func widenDecision(sel storeSelection, projectRoot, lead string, req schedproc.WidenRequest) schedproc.DecideReply {
	t, err := sel.store.Get(req.Task)
	if err != nil {
		return schedproc.DecideReply{Code: "not_squad", Detail: "the task is not in this squad's store: " + err.Error()}
	}
	if projectRoot != "" && t.ProjectPath != "" && evalPath(t.ProjectPath) != evalPath(projectRoot) {
		return schedproc.DecideReply{Code: "not_squad", Detail: fmt.Sprintf("task %s is for %s, not this squad's repository %s", t.ID, t.ProjectPath, projectRoot)}
	}
	data, err := applyLeadWiden(sel, lead, req)
	raw, _ := json.Marshal(data)
	if err != nil {
		code := "widen_failed"
		var wr *widenRefused
		if errors.As(err, &wr) {
			code = wr.code
		}
		return schedproc.DecideReply{Code: code, Detail: err.Error(), Data: raw}
	}
	return schedproc.DecideReply{OK: true, Data: raw}
}

func evalPath(p string) string {
	if real, err := filepath.EvalSymlinks(p); err == nil {
		return real
	}
	return filepath.Clean(p)
}

// widenRefused is a refused widening with its reason code.
type widenRefused struct{ code, msg string }

func (e *widenRefused) Error() string { return fmt.Sprintf("widen refused (%s): %s", e.code, e.msg) }

// applyLeadWiden applies a verified lead decision: the grant checks
// (seatcommit.CheckWiden), the growth gate when a limit is crossed, then
// the store's widening (file lock, self widen).
func applyLeadWiden(sel storeSelection, lead string, req schedproc.WidenRequest) (taskWidenData, error) {
	data := taskWidenData{Root: sel.root}
	t, err := sel.store.Get(req.Task)
	if err != nil {
		return data, err
	}
	g, gerr := grants.Load(t.ProjectPath)
	wo := seatcommit.WidenOptions{Root: sel.root, Task: t, Added: req.Files, Grants: g, GrantsErr: gerr, RepoRoot: t.ProjectPath, Gates: sel.gateChecker()}
	if t.Report != nil {
		wo.Commit = &seatcommit.Options{Root: sel.root, Handle: t.Report.By, Now: taskNow, Clear: ownerTrust{Root: sel.root}}
	}
	d := seatcommit.CheckWiden(wo)
	data.Decision = d
	if !d.Allowed {
		if d.ReasonCode == seatcommit.ReasonGrowthGate {
			var raised []string
			for _, c := range d.Crossings {
				if c.Accepted {
					continue
				}
				gid, err := raiseGrowthGate(sel, lead, t.ID, c.Target)
				data.GateIDs = append(data.GateIDs, gid)
				raised = append(raised, gid+" "+c.Target)
				if err != nil {
					data.GateErrs = append(data.GateErrs, gid+": "+err.Error())
				}
			}
			if t.State == task.StateBlocked {
				if after, err := sel.store.Escalate(t.ID, seatcommit.ReasonGrowthGate, d.Detail+"; growth gate: "+strings.Join(raised, "; "), taskNow()); err == nil {
					data.Task = &after
				}
			}
		}
		return data, &widenRefused{code: d.ReasonCode, msg: d.Detail}
	}
	after, err := sel.store.WidenGated(t.ID, lead, req.Reason, wo.Added, d.Gates, taskLeaseDuration, taskNow())
	var wr *task.WidenRefusal
	if errors.As(err, &wr) {
		data.Decision.ReasonCode, data.Decision.Detail = wr.ReasonCode, wr.Error()
		return data, &widenRefused{code: wr.ReasonCode, msg: wr.Error()}
	}
	if err != nil {
		return data, err
	}
	data.Task = &after
	return data, nil
}
