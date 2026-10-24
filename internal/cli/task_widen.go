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
declared_files inside the grant. The verb only sends the decision: it is the
lead's AMQ message to the squad's scheduler process (the "scheduler"
mailbox), from the caller's own AMQ identity; it takes no --me and decides
nothing (milestone B first pass, ruling R1). The scheduler accepts it only
when the message comes from the lead handle in its launch record and the
task is its squad's, then applies it: a task blocked by a commit refusal is
re-armed and the commit step runs again. The core refuses:
  not_lead              the message is not from the scheduler's lead
  not_squad             the task is not this squad's
  self_widen            the seat that holds or reported the task asked
  file_lock_conflict    the widened set overlaps another active task's files
  growth_gate_required  the widening covers a protected path (a managed
                        block included), or leaves the seat's diff over the
                        size cap: the owner-signed growth gate for that exact
                        crossing is raised and the task goes to the owner;
                        once the owner accepts it, the same widen goes through.
The decision comes back to the lead as an AMQ status message from the
scheduler, and in the task record.
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
		return usageErrorf("task widen: takes no --me; the scheduler takes the lead's identity from the AMQ message, never from a flag")
	}
	if err := task.ValidID(id); err != nil {
		return usageErrorf("task widen: %v", err)
	}
	_, root, release, err := f.store(false)
	if err != nil {
		return err
	}
	release()
	req := schedproc.WidenRequest{Kind: schedproc.WidenRequestKind, Task: id, Files: splitList(*files), Reason: strings.TrimSpace(*reason)}
	body, _ := json.Marshal(req)
	sent, err := taskWidenSend(root, []string{"send", "--to", schedproc.Handle, "--kind", "todo", "--subject", "widen " + id, "--body", string(body)})
	if err != nil {
		return fmt.Errorf("task widen: send the decision to the scheduler: %v: %s", err, strings.TrimSpace(string(sent)))
	}
	if *f.jsonOut {
		return writeJSONEnvelope(out, "task_widen", map[string]any{"root": root, "sent_to": schedproc.Handle, "request": req})
	}
	fmt.Fprintf(out, "task widen %s: sent to the scheduler as your AMQ message; its decision comes back as an AMQ status message and in the task record\n", id)
	return nil
}

// handleSchedulerRequests drains the scheduler's mailbox once and applies
// every widening whose message is from the lead of the scheduler's launch
// record and whose task is the squad's (the scheduler process's Requests
// hook). Every decision goes back by AMQ status message: a refusal to the
// sender, an applied or refused widening to the lead.
func handleSchedulerRequests(sel storeSelection, sender schedproc.Sender, projectRoot string, logf func(string, ...any)) {
	msgs, err := sender.Drain()
	if err != nil {
		logf("scheduler: drain the mailbox: %v", err)
		return
	}
	for _, m := range msgs {
		req, ok := schedproc.ParseWidenRequest(m.Body)
		if !ok {
			logf("scheduler: message %s from %s is not a request the scheduler takes; ignored", m.ID, m.From)
			continue
		}
		code, why := verifyWidenRequest(sel, projectRoot, m, req)
		if code != "" {
			logf("scheduler: widen %s from %s refused (%s): %s", req.Task, m.From, code, why)
			_ = sender.Reply(m.From, "widen "+req.Task+" refused: "+code, why)
			continue
		}
		data, err := applyLeadWiden(sel, m.From, req)
		reply, _ := json.MarshalIndent(data, "", "  ")
		subject := "widen " + req.Task + ": applied"
		if err != nil {
			subject = "widen " + req.Task + " refused: " + firstOf(data.Decision.ReasonCode, "widen_failed")
			reply = append([]byte(err.Error()+"\n"), reply...)
		}
		logf("scheduler: %s", subject)
		if rerr := sender.Reply(m.From, subject, string(reply)); rerr != nil {
			logf("scheduler: reply to %s: %v", m.From, rerr)
		}
	}
}

// verifyWidenRequest is R1's check: the message is from the lead handle in
// the scheduler's launch record, and the task is this squad's.
func verifyWidenRequest(sel storeSelection, projectRoot string, m schedproc.Message, req schedproc.WidenRequest) (string, string) {
	rec, err := schedproc.ReadLaunchRecord(sel.root)
	if err != nil {
		return "not_lead", "the scheduler has no launch record naming its lead: " + err.Error()
	}
	if strings.TrimSpace(m.From) == "" || m.From != rec.Lead {
		return "not_lead", fmt.Sprintf("the message is from %q; the scheduler's launch record names %s as the lead", m.From, rec.Lead)
	}
	t, err := sel.store.Get(req.Task)
	if err != nil {
		return "not_squad", "the task is not in this squad's store: " + err.Error()
	}
	if want := firstOf(rec.ProjectRoot, projectRoot); want != "" && t.ProjectPath != "" && evalPath(t.ProjectPath) != evalPath(want) {
		return "not_squad", fmt.Sprintf("task %s is for %s, not this squad's repository %s", t.ID, t.ProjectPath, want)
	}
	return "", ""
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
		wo.Commit = &seatcommit.Options{Root: sel.root, Handle: t.Report.By, Now: taskNow, Clear: clearVerifier()}
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
