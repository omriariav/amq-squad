package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/omriariav/amq-squad/v3/internal/grants"
	"github.com/omriariav/amq-squad/v3/internal/scheduler"
	"github.com/omriariav/amq-squad/v3/internal/seatcommit"
	"github.com/omriariav/amq-squad/v3/internal/task"
)

const taskWidenUsage = `amq-squad task widen - the lead adds paths to a task's declared files

Usage:
  amq-squad task widen ID --me LEAD --files GLOB,... --reason TEXT [--root DIR | --project P --session S] [--json]

A recorded lead decision (plan 4.3): the paths join the task's declared_files
inside the grant. A task blocked by a commit refusal is re-armed and the
scheduler runs the commit step again. The core refuses:
  self_widen            the seat that holds or reported the task asked
  not_lead              the scheduler names another lead
  file_lock_conflict    the widened set overlaps another active task's files
  growth_gate_required  the widening covers a protected path, or leaves the
                        seat's diff over the size cap: the owner-signed growth
                        gate for that exact crossing is raised and the task
                        goes to the owner; once the owner accepts it, the same
                        widen goes through.
Exit 15: refused.
`

// taskWidenData is the JSON envelope of task widen.
type taskWidenData struct {
	Root     string                   `json:"root"`
	Decision seatcommit.WidenDecision `json:"decision"`
	GateIDs  []string                 `json:"gate_ids,omitempty"`
	GateErrs []string                 `json:"gate_errors,omitempty"`
	Task     *task.Task               `json:"task,omitempty"`
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
	me, err := f.actor("widen", true)
	if err != nil {
		return err
	}
	// A seat's environment carries its own AM_ME from the launch record; a
	// --me that differs from it is not the lead acting.
	if env := strings.TrimSpace(os.Getenv("AM_ME")); env != "" && env != me {
		return usageErrorf("task widen: --me %s differs from this environment's AM_ME %s", me, env)
	}
	sel, err := f.selection(true)
	if err != nil {
		return err
	}
	defer sel.release()
	refuse := func(code, msg string, data taskWidenData) error {
		if *f.jsonOut {
			data.Decision.ReasonCode, data.Decision.Detail = firstOf(data.Decision.ReasonCode, code), firstOf(data.Decision.Detail, msg)
			if err := writeJSONEnvelope(out, "task_widen", data); err != nil {
				return err
			}
		}
		return &codedExitError{code: ExitCommitRefused, msg: fmt.Sprintf("task widen %s refused (%s): %s", id, code, msg)}
	}
	data := taskWidenData{Root: sel.root}
	if st, err := scheduler.ReadState(scheduler.StatePathFor(sel.root)); err == nil && st.Lead != "" && st.Lead != me {
		return refuse("not_lead", "the scheduler names "+st.Lead+" as the lead", data)
	}
	t, err := sel.store.Get(id)
	if err != nil {
		return fmt.Errorf("task widen: %w", err)
	}
	g, gerr := grants.Load(t.ProjectPath)
	wo := seatcommit.WidenOptions{Root: sel.root, Task: t, Added: splitList(*files), Grants: g, GrantsErr: gerr, RepoRoot: t.ProjectPath, Gates: sel.gateChecker()}
	if t.Report != nil {
		wo.Commit = &seatcommit.Options{Root: sel.root, Handle: t.Report.By, Now: taskNow}
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
				gid, err := raiseGrowthGate(sel, me, t.ID, c.Target)
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
			if !*f.jsonOut {
				fmt.Fprintf(out, "task widen %s: refused (%s)\n  %s\n", t.ID, d.ReasonCode, d.Detail)
				for i, gid := range data.GateIDs {
					fmt.Fprintf(out, "  growth gate %s raised for the owner\n", gid)
					_ = i
				}
				for _, e := range data.GateErrs {
					fmt.Fprintf(out, "  gate: %s\n", e)
				}
			}
		}
		return refuse(d.ReasonCode, d.Detail, data)
	}
	after, err := sel.store.WidenGated(t.ID, me, *reason, wo.Added, d.Gates, taskLeaseDuration, taskNow())
	var wr *task.WidenRefusal
	if errors.As(err, &wr) {
		return refuse(wr.ReasonCode, wr.Error(), data)
	}
	if err != nil {
		return fmt.Errorf("task widen: %w", err)
	}
	data.Task = &after
	if *f.jsonOut {
		return writeJSONEnvelope(out, "task_widen", data)
	}
	fmt.Fprintf(out, "Widened %s %q: declared files %s; %s\n", after.ID, after.Title, strings.Join(after.DeclaredFiles, ", "), after.State)
	for _, n := range d.Notes {
		fmt.Fprintf(out, "  note: %s\n", n)
	}
	return nil
}
