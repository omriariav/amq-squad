package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"text/tabwriter"

	"github.com/omriariav/amq-squad/v3/internal/fleet"
	"github.com/omriariav/amq-squad/v3/internal/grants"
	"github.com/omriariav/amq-squad/v3/internal/status"
)

// The fleet registry verb and `status --fleet` (plan 3.5, 16.3; brief 4-3).
// fleet.yaml is the operator session's registry: each squad, its root, its
// lead handle. `status --fleet --json` is the only fleet input for amq-noc
// (R44): one schema 1 document per squad in the fleet header (ruling 16).

const fleetUsage = `amq-squad fleet - the fleet registry ~/.amq-squad/fleet.yaml

Usage:
  amq-squad fleet list [--json]
  amq-squad fleet add --name N --root DIR --project-path DIR --repository OWNER/NAME --lead HANDLE [--session S] [--amq-project P]
  amq-squad fleet remove --name N
  amq-squad fleet operator --root DIR --amq-project P --session S

Every subcommand takes --fleet-file PATH (default ~/.amq-squad/fleet.yaml).
The operator block names the operator session's AMQ root, project and
session; squad leads route gates and status to its user mailbox through
AMQ cross-project routing.
`

func fleetFileFlag(fs *flag.FlagSet) *string {
	return fs.String("fleet-file", fleet.DefaultPath(), "the fleet registry (default ~/.amq-squad/fleet.yaml)")
}

func runFleet(args []string) error {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" {
		fmt.Fprint(os.Stderr, fleetUsage)
		return nil
	}
	fs := flag.NewFlagSet("fleet "+args[0], flag.ContinueOnError)
	fs.Usage = func() { fmt.Fprint(os.Stderr, fleetUsage) }
	path := fleetFileFlag(fs)
	jsonOut := fs.Bool("json", false, "print the registry as JSON")
	var s fleet.Squad
	var op fleet.Operator
	switch args[0] {
	case "add", "remove":
		fs.StringVar(&s.Name, "name", "", "squad name")
		if args[0] == "add" {
			fs.StringVar(&s.Root, "root", "", "the squad's AMQ session root")
			fs.StringVar(&s.ProjectPath, "project-path", "", "the squad's repository root")
			fs.StringVar(&s.Repository, "repository", "", "owner/name")
			fs.StringVar(&s.Lead, "lead", "", "the lead seat's handle")
			fs.StringVar(&s.Session, "session", "", "AMQ session name (default: the root's base name)")
			// not --project: the CLI normalizes every --project as a directory
			fs.StringVar(&s.Project, "amq-project", "", "AMQ peer project name (default: the squad name)")
		}
	case "operator":
		fs.StringVar(&op.Root, "root", "", "the operator's AMQ session root")
		fs.StringVar(&op.Project, "amq-project", "", "the operator's AMQ peer project name")
		fs.StringVar(&op.Session, "session", "", "the operator's AMQ session name")
	case "list":
	default:
		return unknownSubcommandError("fleet", args[0], "list", "add", "remove", "operator")
	}
	if err := parseFlags(fs, args[1:]); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return usageErrorf("fleet %s takes no positional arguments; got %q", args[0], fs.Arg(0))
	}
	if *path == "" {
		return usageErrorf("fleet: no home directory; pass --fleet-file")
	}
	switch args[0] {
	case "add":
		return fleet.Update(*path, func(r *fleet.Registry) error {
			if _, dup := r.Find(s.Name); dup {
				return fmt.Errorf("fleet: squad %q is already registered", s.Name)
			}
			r.Squads = append(r.Squads, s)
			return nil
		})
	case "remove":
		return fleet.Update(*path, func(r *fleet.Registry) error {
			if x, ok := r.Find(s.Name); ok && x.InFlight() {
				return fmt.Errorf("fleet: squad %q is %s by the mayor (reservation %s, mayor pid %s since %s); it is released when the launch ends, its mayor is gone or its phase stalls", s.Name, x.Phase, x.Reservation, x.MayorPID, x.PhaseSince)
			}
			n := len(r.Squads)
			r.Squads = slices.DeleteFunc(r.Squads, func(x fleet.Squad) bool { return x.Name == s.Name })
			if len(r.Squads) == n {
				return fmt.Errorf("fleet: no squad %q", s.Name)
			}
			return nil
		})
	case "operator":
		return fleet.Update(*path, func(r *fleet.Registry) error { r.Operator = op; return nil })
	}
	r, err := readFleet(*path)
	if err != nil {
		return err
	}
	if *jsonOut {
		return writeIndentedJSON(os.Stdout, r)
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "SQUAD\tREPOSITORY\tLEAD\tSESSION\tROOT")
	for _, x := range r.Squads {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", termSafe(x.Name), termSafe(x.Repository), termSafe(x.Lead), termSafe(x.SessionName()), termSafe(x.Root))
	}
	return w.Flush()
}

// readFleet reads the registry; a missing file is an empty fleet.
func readFleet(path string) (fleet.Registry, error) {
	r, err := fleet.Read(path)
	if errors.Is(err, os.ErrNotExist) {
		return fleet.Registry{Schema: fleet.Schema}, nil
	}
	return r, err
}

// fleetStatus is `status --fleet`: every squad of the registry through the
// single-squad producer.
func fleetStatus(path, version string) (fleet.Registry, status.Fleet, error) {
	r, err := readFleet(path)
	if err != nil {
		return r, status.Fleet{}, err
	}
	now := statusNow()
	collect := func(s fleet.Squad) status.Status {
		st := buildStatusContract(s.Root, s.ProjectPath, version, now)
		// a registered root that is gone keeps its squad row, with a warning
		// that says so, instead of an empty document that reads as idle
		if info, err := os.Stat(s.Root); err != nil || !info.IsDir() {
			why := "not a directory"
			if err != nil {
				why = err.Error()
			}
			st.Warnings = append(st.Warnings, fleet.WarningRootUnavailable+": squad "+s.Name+" root "+s.Root+": "+why)
		}
		return st
	}
	f := fleet.Status(r, collect, version, now)
	if r.Operator.Root != "" {
		// the operator session's own seat row (the user handle, which a
		// squad document skips), when the session runs (4-5's presence)
		f.Operator = status.OperatorSeat(status.CollectOptions{Root: r.Operator.Root, Session: r.Operator.Session, Now: now, PIDAlive: statusPIDAlive})
	}
	return r, f, nil
}

func runFleetStatus(out io.Writer, path, version string, jsonOut bool) error {
	_, f, err := fleetStatus(path, version)
	if err != nil {
		return err
	}
	if jsonOut {
		return writeIndentedJSON(out, f)
	}
	fmt.Fprintf(out, "Fleet: %d squads\n", len(f.Squads))
	for _, st := range f.Squads {
		lead := "-"
		if st.LeadHandle != nil {
			lead = *st.LeadHandle
		}
		fmt.Fprintf(out, "  %s (lead %s): %d needs you, %d working, %d ready, %d inactive\n",
			termSafe(st.Session), termSafe(lead), st.Counts.NeedsYou, st.Counts.Working, st.Counts.Ready, st.Counts.Inactive)
	}
	return nil
}

const operatorInboxUsage = `amq-squad operator inbox - the operator session's view of the fleet

Usage:
  amq-squad operator inbox [--fleet-file PATH] [--json]

Manual mode (plan 3.5): every open gate of every squad in fleet.yaml in one
list with its squad, oldest first, and the lead questions that wait on the
owner, each with the relay argv that answers the asking seat over AMQ
cross-project routing. A typed gate carries no answer: only the owner
answers it, with a signature. It reads the status contract of each squad
and never writes.
`

func runOperatorInbox(args []string) error {
	fs := flag.NewFlagSet("operator inbox", flag.ContinueOnError)
	fs.Usage = func() { fmt.Fprint(os.Stderr, operatorInboxUsage) }
	path := fleetFileFlag(fs)
	jsonOut := fs.Bool("json", false, "print the inbox as JSON")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return usageErrorf("operator inbox takes no positional arguments; got %q", fs.Arg(0))
	}
	r, f, err := fleetStatus(*path, "")
	if err != nil {
		return err
	}
	in := fleet.GroupInbox(r, f)
	if *jsonOut {
		return printJSONEnvelope("operator_inbox", in)
	}
	return renderInbox(os.Stdout, in)
}

// renderInbox is the human inbox. Every field a squad, a lead or a seat
// wrote passes termSafe at this output boundary (P2-2).
func renderInbox(out io.Writer, in fleet.Inbox) error {
	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "SQUAD\tGATE\tCLASS\tRAISED BY\tSINCE")
	for _, g := range in.Gates {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", termSafe(g.Squad), termSafe(g.Gate.ID), termSafe(g.Gate.ActionClass), termSafe(g.Gate.RaisedBy), g.Since.Format("2006-01-02T15:04Z"))
	}
	if err := w.Flush(); err != nil {
		return err
	}
	for _, q := range in.Questions {
		fmt.Fprintf(out, "question [%s] %s: %s\n", termSafe(q.Squad), termSafe(q.Attention.Seat), termSafe(q.Attention.Subject))
	}
	return nil
}

const operatorStartSquadUsage = `amq-squad operator start-squad - the mayor starts a squad inside the fleet grant

Usage:
  amq-squad operator start-squad --name N --root DIR --project-path DIR --repository OWNER/NAME
      --lead HANDLE --squad-yaml FILE [--fleet-file PATH] [--json]

Auto mode (plan 3.5, R40): checks the owner's fleet grant (max_squads,
cost_per_day_usd, permitted_repositories), then writes squad.yaml through
the digest flow (team propose and apply), provisions the roster through the
seat launch (the AMQ launch API, the installed amq on PATH) and registers the
squad in fleet.yaml. A root the launch API cannot provision (no .amqrc,
upstream ask 2) and manual mode give a proposal for the owner, never a
launch. Outcomes: started, refused, proposed. This verb is one start, not a
running operator session: a start held by max_squads or cost_per_day_usd is
refused by that name, never queued, and recorded under fleet.yaml waiting,
which status --fleet shows as wait_reason fleet_cap. A cost_per_day_usd cap
with no meter that can read the outcome log holds every start
(cost_unmetered, shown as that wait_reason); remove the cap to proceed. It
never answers a gate. After a launch it stays until the seats' proof window
has ended (up to 60 s), so a late replayed proof is still heard.
`

// mayorSquadWriter and mayorLauncher are the CLI's adapters over 2b-5's
// squad.yaml digest flow and 2b-2's seat launch (fleet_adapters.go). Tests
// replace them; with either nil, a start that passes every grant check stops
// before any write.
var (
	mayorSquadWriter fleet.SquadWriter    = &teamSquadWriter{}
	mayorLauncher    fleet.RosterLauncher = seatRoster{}
)

func runOperatorStartSquad(args []string) error {
	fs := flag.NewFlagSet("operator start-squad", flag.ContinueOnError)
	fs.Usage = func() { fmt.Fprint(os.Stderr, operatorStartSquadUsage) }
	path := fleetFileFlag(fs)
	jsonOut := fs.Bool("json", false, "print the outcome as JSON")
	yamlFile := fs.String("squad-yaml", "", "the squad.yaml to propose")
	var s fleet.Squad
	fs.StringVar(&s.Name, "name", "", "squad name")
	fs.StringVar(&s.Root, "root", "", "the squad's AMQ session root")
	fs.StringVar(&s.ProjectPath, "project-path", "", "the squad's repository root")
	fs.StringVar(&s.Repository, "repository", "", "owner/name")
	fs.StringVar(&s.Lead, "lead", "", "the lead seat's handle")
	// no --session: the launch runs under the registered root, whose base name is the session
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if fs.NArg() > 0 || *yamlFile == "" {
		return usageErrorf("operator start-squad needs --squad-yaml and takes no positional arguments")
	}
	text, err := os.ReadFile(*yamlFile)
	if err != nil {
		return err
	}
	// the owner grant is loaded inside the mayor on every start (a removed
	// permission stops the next start)
	m := &fleet.Mayor{Grant: func() (grants.Resolved, error) { return grants.Load("") }, RegistryPath: *path, Squads: mayorSquadWriter, Launcher: mayorLauncher}
	res, err := m.Start(context.Background(), fleet.StartRequest{Squad: s, SquadYAML: string(text)})
	if err != nil {
		return err
	}
	if *jsonOut {
		return printJSONEnvelope("operator_start_squad", res)
	}
	fmt.Fprintf(os.Stdout, "%s %s: %s %s\n", termSafe(s.Name), termSafe(res.Outcome), termSafe(res.Reason), termSafe(res.Detail))
	if res.Proposal != nil {
		for _, step := range res.Proposal.Steps {
			fmt.Fprintln(os.Stdout, "  owner runs: "+termSafe(strings.Join(step, " ")))
		}
	}
	return nil
}

func writeIndentedJSON(out io.Writer, v any) error {
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}
