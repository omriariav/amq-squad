package cli

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/omriariav/amq-squad/internal/launch"
)

func runRestore(args []string) error {
	fs := flag.NewFlagSet("restore", flag.ContinueOnError)
	projectDirs := fs.String("project", "", "comma-separated project directories to scan (default: cwd)")

	fs.Usage = func() {
		fmt.Fprint(os.Stderr, `amq-squad restore - emit bash commands to restore every registered agent

Usage:
  amq-squad restore [--project dir1,dir2,...]

Scans each project for .agent-mail/<session>/agents/<handle>/launch.json
records and prints a bash command per agent. Default scope is the current
working directory if --project is omitted.

Each emitted command is ready to paste into its own terminal tab.
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

	type found struct {
		project string
		rec     launch.Record
	}
	var records []found

	for _, dir := range dirs {
		recs, err := launch.Scan(dir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: scan %s: %v\n", dir, err)
			continue
		}
		for _, r := range recs {
			records = append(records, found{project: dir, rec: r})
		}
	}

	if len(records) == 0 {
		return fmt.Errorf("no launch.json records found in %s", strings.Join(dirs, ", "))
	}

	sort.Slice(records, func(i, j int) bool {
		if records[i].rec.Role != records[j].rec.Role {
			return records[i].rec.Role < records[j].rec.Role
		}
		return records[i].rec.Handle < records[j].rec.Handle
	})

	fmt.Println("# amq-squad restore - run each command in its own terminal tab")
	fmt.Println()
	for i, f := range records {
		label := f.rec.Role
		if label == "" {
			label = f.rec.Handle
		}
		fmt.Printf("# %d. %s - %s (%s/%s)\n", i+1, label, f.rec.Binary, f.rec.CWD, f.rec.Session)
		fmt.Println(emitCommand(f.rec))
		fmt.Println()
	}
	return nil
}

// emitCommand reconstructs the bash command for a launch record.
// It prefers 'amq-squad launch' so role + metadata round-trip cleanly;
// callers who want the raw amq invocation can run with --dry-run to see it.
func emitCommand(rec launch.Record) string {
	var b strings.Builder
	b.WriteString("cd ")
	b.WriteString(shellQuote(rec.CWD))
	b.WriteString(" && amq-squad launch")
	if rec.Role != "" {
		b.WriteString(" --role ")
		b.WriteString(shellQuote(rec.Role))
	}
	if rec.Session != "" {
		b.WriteString(" --session ")
		b.WriteString(shellQuote(rec.Session))
	}
	if rec.Handle != "" && rec.Handle != defaultHandleFor(rec.Binary) {
		b.WriteString(" --me ")
		b.WriteString(shellQuote(rec.Handle))
	}
	b.WriteString(" ")
	b.WriteString(shellQuote(rec.Binary))
	if len(rec.Argv) > 0 {
		b.WriteString(" --")
		for _, a := range rec.Argv {
			b.WriteString(" ")
			b.WriteString(shellQuote(a))
		}
	}
	return b.String()
}

func defaultHandleFor(binary string) string {
	return strings.ToLower(filepath.Base(binary))
}

// shellQuote wraps a string in single quotes for safe shell pasting.
// If the string has no special chars, returns it as-is.
func shellQuote(s string) string {
	if s == "" {
		return "''"
	}
	safe := true
	for _, r := range s {
		if !(r == '/' || r == '.' || r == '-' || r == '_' || r == '=' ||
			(r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')) {
			safe = false
			break
		}
	}
	if safe {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
