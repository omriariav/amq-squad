package cli

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/omriariav/amq-squad/internal/launch"
	"github.com/omriariav/amq-squad/internal/role"
)

type restoreCandidate struct {
	entry launch.Entry
}

func runRestore(args []string) error {
	fs := flag.NewFlagSet("restore", flag.ContinueOnError)
	projectDirs := fs.String("project", "", "comma-separated project directories to scan (default: cwd)")
	execRestore := fs.Bool("exec", false, "exec the selected launch in this terminal")
	roleFilter := fs.String("role", "", "only consider records with this role")
	handleFilter := fs.String("handle", "", "only consider records with this handle")
	sessionFilter := fs.String("session", "", "only consider records with this session")
	conversationFilter := fs.String("conversation", "", "only consider records with this conversation name/id")

	fs.Usage = func() {
		fmt.Fprint(os.Stderr, `amq-squad restore - restore registered agents from local launch history

Usage:
  amq-squad restore [--project dir1,dir2,...] [--role r] [--handle h] [--session s] [--conversation ref]
  amq-squad restore --exec --role cto

Scans each project for .agent-mail/<session>/agents/<handle>/launch.json
records, nearby role.md persona files, and older AMQ mailbox history when
launch.json is missing and the original binary can be inferred. Default
scope is the current working directory if --project is omitted. Without
--exec, prints a bash command per matching agent.

With --exec, exactly one record must match; amq-squad changes to that
record's cwd and execs the saved launch through 'amq coop exec'.
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

	var records []restoreCandidate

	for _, dir := range dirs {
		entries, err := launch.ScanRestorableEntries(dir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: scan %s: %v\n", dir, err)
			continue
		}
		for _, e := range entries {
			if !matchesRestoreFilters(e.Record, *roleFilter, *handleFilter, *sessionFilter, *conversationFilter) {
				continue
			}
			records = append(records, restoreCandidate{entry: e})
		}
	}

	if len(records) == 0 {
		return fmt.Errorf("no matching launch.json records found in %s", strings.Join(dirs, ", "))
	}

	sort.Slice(records, func(i, j int) bool {
		if records[i].entry.Record.Role != records[j].entry.Record.Role {
			return records[i].entry.Record.Role < records[j].entry.Record.Role
		}
		return records[i].entry.Record.Handle < records[j].entry.Record.Handle
	})

	if *execRestore {
		if len(records) != 1 {
			printRestoreCandidates(os.Stderr, records)
			return fmt.Errorf("--exec requires exactly one matching record; narrow with --role, --handle, --session, or --conversation")
		}
		rec := records[0].entry.Record
		fmt.Fprintf(os.Stderr, "Restoring %s via amq coop exec.\n", restoreLabel(rec))
		return execRestoreRecord(rec)
	}

	fmt.Println("# amq-squad restore - run each command in its own terminal tab")
	fmt.Println()
	for i, f := range records {
		rec := f.entry.Record
		fmt.Printf("# %d. %s - %s (%s)\n", i+1, restoreLabel(rec), rec.Binary, restoreMetadata(f.entry))
		fmt.Println(emitCommand(rec))
		fmt.Println()
	}
	return nil
}

func matchesRestoreFilters(rec launch.Record, roleFilter, handleFilter, sessionFilter, conversationFilter string) bool {
	if roleFilter != "" && rec.Role != roleFilter {
		return false
	}
	if handleFilter != "" && rec.Handle != handleFilter {
		return false
	}
	if sessionFilter != "" && rec.Session != sessionFilter {
		return false
	}
	if conversationFilter != "" && rec.Conversation != conversationFilter {
		return false
	}
	return true
}

func printRestoreCandidates(out *os.File, records []restoreCandidate) {
	fmt.Fprintln(out, "Matching restore candidates:")
	for i, f := range records {
		rec := f.entry.Record
		fmt.Fprintf(out, "  %d. %s - %s (%s)\n", i+1, restoreLabel(rec), rec.Binary, restoreMetadata(f.entry))
	}
}

func restoreLabel(rec launch.Record) string {
	if rec.Role != "" {
		return rec.Role
	}
	if rec.Handle != "" {
		return rec.Handle
	}
	return "(unknown)"
}

func restoreMetadata(entry launch.Entry) string {
	rec := entry.Record
	parts := []string{}
	if rec.Session != "" {
		parts = append(parts, "session: "+rec.Session)
	}
	if rec.Conversation != "" {
		parts = append(parts, "conversation: "+rec.Conversation)
	}
	if rec.Handle != "" {
		parts = append(parts, "handle: "+rec.Handle)
	}
	if _, err := os.Stat(filepath.Join(entry.AgentDir, role.FileName)); err == nil {
		parts = append(parts, "persona: role.md")
	} else {
		parts = append(parts, "persona: missing")
	}
	if entry.Source != "" {
		parts = append(parts, "source: "+sourceLabel(entry.Source))
	}
	if !rec.StartedAt.IsZero() {
		label := "started"
		if entry.Source != "" && entry.Source != launch.FileName {
			label = "last seen"
		}
		parts = append(parts, label+": "+rec.StartedAt.Format("2006-01-02 15:04"))
	}
	if rec.CWD != "" {
		parts = append(parts, "cwd: "+rec.CWD)
	}
	return strings.Join(parts, ", ")
}

func sourceLabel(source string) string {
	switch source {
	case launch.FileName:
		return "amq-squad"
	case "amq history":
		return "amq"
	case "":
		return "(unknown)"
	default:
		return source
	}
}

func execRestoreRecord(rec launch.Record) error {
	if rec.CWD == "" {
		return fmt.Errorf("launch record has empty cwd")
	}
	if err := os.Chdir(rec.CWD); err != nil {
		return fmt.Errorf("chdir %s: %w", rec.CWD, err)
	}
	return runLaunch(launchArgsFromRecord(rec))
}

func launchArgsFromRecord(rec launch.Record) []string {
	args := []string{"--no-bootstrap"}
	if rec.Role != "" {
		args = append(args, "--role", rec.Role)
	}
	if rec.Session != "" {
		args = append(args, "--session", rec.Session)
	} else if rec.Root != "" {
		args = append(args, "--root", rec.Root)
	}
	if rec.Conversation != "" {
		args = append(args, "--conversation", rec.Conversation)
	}
	if rec.Handle != "" {
		args = append(args, "--me", rec.Handle)
	}
	args = append(args, rec.Binary)
	argv := restoreArgvFromRecord(rec)
	if len(argv) > 0 {
		args = append(args, "--")
		args = append(args, argv...)
	}
	return args
}

func restoreArgvFromRecord(rec launch.Record) []string {
	if rec.Conversation == "" {
		return append([]string(nil), rec.Argv...)
	}
	return stripConversationRestoreArgs(rec.Binary, rec.Argv, rec.Conversation)
}

// emitCommand reconstructs the bash command for a launch record.
// It prefers 'amq-squad launch' so role + metadata round-trip cleanly;
// callers who want the raw amq invocation can run with --dry-run to see it.
func emitCommand(rec launch.Record) string {
	var b strings.Builder
	b.WriteString("cd ")
	b.WriteString(shellQuote(rec.CWD))
	b.WriteString(" && amq-squad launch")
	b.WriteString(" --no-bootstrap")
	if rec.Role != "" {
		b.WriteString(" --role ")
		b.WriteString(shellQuote(rec.Role))
	}
	if rec.Session != "" {
		b.WriteString(" --session ")
		b.WriteString(shellQuote(rec.Session))
	} else if rec.Root != "" {
		b.WriteString(" --root ")
		b.WriteString(shellQuote(rec.Root))
	}
	if rec.Conversation != "" {
		b.WriteString(" --conversation ")
		b.WriteString(shellQuote(rec.Conversation))
	}
	if rec.Handle != "" && rec.Handle != defaultHandleFor(rec.Binary) {
		b.WriteString(" --me ")
		b.WriteString(shellQuote(rec.Handle))
	}
	b.WriteString(" ")
	b.WriteString(shellQuote(rec.Binary))
	argv := restoreArgvFromRecord(rec)
	if len(argv) > 0 {
		b.WriteString(" --")
		for _, a := range argv {
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

func shellCommand(bin string, args ...string) string {
	parts := make([]string, 0, len(args)+1)
	parts = append(parts, shellQuote(bin))
	for _, arg := range args {
		parts = append(parts, shellQuote(arg))
	}
	return strings.Join(parts, " ")
}
