package cli

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"slices"
	"text/tabwriter"

	"github.com/omriariav/amq-squad/v3/internal/fleet"
	"github.com/omriariav/amq-squad/v3/internal/status"
)

// The fleet registry verb and `status --fleet` (plan 3.5, 16.3; brief 4-3).
// fleet.yaml is the operator session's registry: each squad, its root, its
// lead handle. `status --fleet --json` is the only fleet input for amq-noc
// (R44): one schema 1 document per squad in the fleet header (ruling 16).

const fleetUsage = `amq-squad fleet - the fleet registry ~/.amq-squad/fleet.yaml

Usage:
  amq-squad fleet list [--json]
  amq-squad fleet add --name N --root DIR --project-path DIR --repository OWNER/NAME --lead HANDLE [--session S] [--project P]
  amq-squad fleet remove --name N
  amq-squad fleet operator --root DIR --project P --session S

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
			fs.StringVar(&s.Project, "project", "", "AMQ peer project name (default: the squad name)")
		}
	case "operator":
		fs.StringVar(&op.Root, "root", "", "the operator's AMQ session root")
		fs.StringVar(&op.Project, "project", "", "the operator's AMQ peer project name")
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
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", x.Name, x.Repository, x.Lead, x.SessionName(), x.Root)
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
		return buildStatusContract(s.Root, s.ProjectPath, version, now)
	}
	return r, fleet.Status(r, collect, version, now), nil
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
			st.Session, lead, st.Counts.NeedsYou, st.Counts.Working, st.Counts.Ready, st.Counts.Inactive)
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
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "SQUAD\tGATE\tCLASS\tRAISED BY\tSINCE")
	for _, g := range in.Gates {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", g.Squad, g.Gate.ID, g.Gate.ActionClass, g.Gate.RaisedBy, g.Since.Format("2006-01-02T15:04Z"))
	}
	if err := w.Flush(); err != nil {
		return err
	}
	for _, q := range in.Questions {
		fmt.Fprintf(os.Stdout, "question [%s] %s: %s\n", q.Squad, q.Attention.Seat, q.Attention.Subject)
	}
	return nil
}

func writeIndentedJSON(out io.Writer, v any) error {
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}
