package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/seatlaunch"
)

const launchUsage = `amq-squad launch - start a squad's seats through the AMQ launch API

Usage:
  amq-squad launch --seats FILE [--decision ACTION_ID=CHOICE ...] [--dry-run] [--wait DURATION] [--json]

FILE is a typed seat list (JSON; squad.yaml compiles to it in 2b-5):
  {"project_root": "/abs/repo", "session": "s1", "rules_path": "/abs/repo/.amq-squad/team-rules.md",
   "seats": [{"handle": "cto", "runtime": "claude", "model": "sonnet", "effort": "medium",
              "role_file": "/abs/roles/cto.md", "worktree": "/abs/wt/cto", "branch": "seat/cto"}]}

One AMQ launch request covers the squad. Every seat runs behind
'amq-squad seat-exec --seat <handle>'; model and effort (and Codex's
--sandbox workspace-write --ask-for-approval on-request) travel as provider
arguments, so they enter the trust digest. The role reaches the seat only
through role.md; initial_input carries a pointer to it. Prepare and Apply
run through the installed amq on PATH (R51). A required action (such as the
trust confirmation) is never answered for you: the launch stops, prints the
action ids, and exits 10; re-run with --decision. A seat is reported up only
after seat-exec proved one amq drain.
`

type launchSeatFile struct {
	seatlaunch.Squad
	Seats []struct {
		seatlaunch.Seat
		RoleFile string `json:"role_file"`
	} `json:"seats"`
}

func runLaunch(args []string) error {
	fs := flag.NewFlagSet("launch", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() { fmt.Fprint(os.Stderr, launchUsage) }
	seats := fs.String("seats", "", "typed seat list (JSON)")
	dryRun := fs.Bool("dry-run", false, "Prepare only; print the preview and the required actions")
	wait := fs.Duration("wait", 60*time.Second, "how long to wait for every seat's drain proof")
	jsonOut := fs.Bool("json", false, "emit JSON")
	var decisions stringListFlag
	fs.Var(&decisions, "decision", "ACTION_ID=CHOICE answer to a required action (repeatable)")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if *seats == "" || fs.NArg() > 0 {
		return usageErrorf("launch: --seats FILE is required and takes no positional arguments")
	}
	q, err := readLaunchSeats(*seats)
	if err != nil {
		return err
	}
	answers := map[string]string{}
	for _, d := range decisions {
		id, choice, ok := strings.Cut(d, "=")
		if !ok || id == "" || choice == "" {
			return usageErrorf("launch: --decision %q is not ACTION_ID=CHOICE", d)
		}
		answers[id] = choice
	}
	if q.Wrapper, err = os.Executable(); err == nil {
		q.Wrapper, err = filepath.EvalSymlinks(q.Wrapper)
	}
	if err != nil {
		return fmt.Errorf("launch: resolve the amq-squad executable: %w", err)
	}
	if q.AMQPath, err = exec.LookPath("amq"); err != nil {
		return fmt.Errorf("launch: the installed amq is required on PATH (R51): %w", err)
	}
	res, err := seatlaunch.Launch(context.Background(), q, seatlaunch.Options{Decisions: answers, DryRun: *dryRun, Wait: *wait})
	if err != nil {
		return fmt.Errorf("launch: %w", err)
	}
	if *jsonOut {
		if err := printJSONEnvelope("launch", res); err != nil {
			return err
		}
	} else {
		seatlaunch.Report(os.Stdout, res)
	}
	switch {
	case res.Stage == "decisions_pending":
		return &ActionDecisionError{Decision: "pending", Message: fmt.Sprintf("launch: %d required action(s) need an explicit --decision", len(res.Pending))}
	case res.Failed():
		return fmt.Errorf("launch: not every seat came up (stage %s)", res.Stage)
	}
	return nil
}

func readLaunchSeats(path string) (seatlaunch.Squad, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return seatlaunch.Squad{}, fmt.Errorf("launch: %w", err)
	}
	var f launchSeatFile
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&f); err != nil {
		return seatlaunch.Squad{}, usageErrorf("launch: %s: %v", path, err)
	}
	q := f.Squad
	q.Seats = nil
	for _, s := range f.Seats {
		seat := s.Seat
		if s.RoleFile != "" {
			role, err := os.ReadFile(s.RoleFile)
			if err != nil {
				return q, fmt.Errorf("launch: seat %s role_file: %w", seat.Handle, err)
			}
			seat.Role = string(role)
		}
		q.Seats = append(q.Seats, seat)
	}
	return q, nil
}
