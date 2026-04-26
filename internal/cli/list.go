package cli

import (
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/omriariav/amq-squad/internal/launch"
)

type listRecord struct {
	Role         string    `json:"role"`
	Handle       string    `json:"handle"`
	Binary       string    `json:"binary"`
	Session      string    `json:"session"`
	Conversation string    `json:"conversation,omitempty"`
	Source       string    `json:"source"`
	CWD          string    `json:"cwd"`
	StartedAt    time.Time `json:"started_at,omitempty"`
}

func runList(args []string) error {
	fs := flag.NewFlagSet("list", flag.ContinueOnError)
	projectDirs := fs.String("project", "", "comma-separated project directories to scan (default: cwd)")
	jsonOut := fs.Bool("json", false, "emit records as JSON array")

	fs.Usage = func() {
		fmt.Fprint(os.Stderr, `amq-squad list - list restorable agents

Usage:
  amq-squad list [--project dir1,dir2,...] [--json]

Includes full amq-squad launch records and older AMQ-only mailbox history
where the original binary can be inferred.
`)
	}
	if err := fs.Parse(args); err != nil {
		return err
	}

	var dirs []string
	if *projectDirs == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("getwd: %w", err)
		}
		dirs = []string{cwd}
	} else {
		for _, d := range strings.Split(*projectDirs, ",") {
			if d = strings.TrimSpace(d); d != "" {
				dirs = append(dirs, d)
			}
		}
	}

	var all []launch.Entry
	for _, dir := range dirs {
		entries, err := launch.ScanRestorableEntries(dir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: scan %s: %v\n", dir, err)
			continue
		}
		all = append(all, entries...)
	}

	sortRestorableEntries(all)
	rows := listRecordsFromEntries(all)

	if *jsonOut {
		return printJSON(rows)
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ROLE\tHANDLE\tBINARY\tSESSION\tCONVERSATION\tSOURCE\tCWD\tSTARTED")
	for _, r := range rows {
		role := r.Role
		if role == "" {
			role = "(none)"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			role, r.Handle, r.Binary, r.Session, r.Conversation, r.Source, r.CWD, formatListTime(r.StartedAt))
	}
	return w.Flush()
}

func sortRestorableEntries(entries []launch.Entry) {
	sort.Slice(entries, func(i, j int) bool {
		a := entries[i].Record
		b := entries[j].Record
		if a.Role != b.Role {
			return a.Role < b.Role
		}
		if a.Handle != b.Handle {
			return a.Handle < b.Handle
		}
		return entries[i].Source < entries[j].Source
	})
}

func listRecordsFromEntries(entries []launch.Entry) []listRecord {
	rows := make([]listRecord, 0, len(entries))
	for _, e := range entries {
		r := e.Record
		rows = append(rows, listRecord{
			Role:         r.Role,
			Handle:       r.Handle,
			Binary:       r.Binary,
			Session:      r.Session,
			Conversation: r.Conversation,
			Source:       sourceLabel(e.Source),
			CWD:          r.CWD,
			StartedAt:    r.StartedAt,
		})
	}
	return rows
}

func formatListTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format("2006-01-02 15:04")
}

func printJSON(recs any) error {
	enc := jsonEncoder()
	return enc.Encode(recs)
}
