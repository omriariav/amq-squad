package cli

import (
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/omriariav/amq-squad/internal/launch"
)

func runList(args []string) error {
	fs := flag.NewFlagSet("list", flag.ContinueOnError)
	projectDirs := fs.String("project", "", "comma-separated project directories to scan (default: cwd)")
	jsonOut := fs.Bool("json", false, "emit records as JSON array")

	fs.Usage = func() {
		fmt.Fprint(os.Stderr, `amq-squad list - list registered agents

Usage:
  amq-squad list [--project dir1,dir2,...] [--json]
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

	var all []launch.Record
	for _, dir := range dirs {
		recs, err := launch.Scan(dir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: scan %s: %v\n", dir, err)
			continue
		}
		all = append(all, recs...)
	}

	if *jsonOut {
		return printJSON(all)
	}

	sort.Slice(all, func(i, j int) bool {
		if all[i].Role != all[j].Role {
			return all[i].Role < all[j].Role
		}
		return all[i].Handle < all[j].Handle
	})

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ROLE\tHANDLE\tBINARY\tSESSION\tCWD\tSTARTED")
	for _, r := range all {
		role := r.Role
		if role == "" {
			role = "(none)"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n",
			role, r.Handle, r.Binary, r.Session, r.CWD, r.StartedAt.Format("2006-01-02 15:04"))
	}
	return w.Flush()
}

func printJSON(recs []launch.Record) error {
	enc := jsonEncoder()
	return enc.Encode(recs)
}
