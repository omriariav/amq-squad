package cli

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/launch"
)

const (
	claudeRenameHelperCommand = "__rename-claude-session"
	defaultClaudeRenameDelay  = 3 * time.Second
)

var (
	claudeRenameHelperExecutable = os.Executable
	claudeRenameHelperStart      = func(exe string, args []string) error {
		return runCommand("tmux", "run-shell", "-b", shellCommand(exe, args...))
	}
)

func maybeScheduleClaudeSessionRename(rec launch.Record) error {
	if normalizedAgentBinary(rec.Binary) != "claude" {
		return nil
	}
	if rec.Tmux == nil || strings.TrimSpace(rec.Tmux.PaneID) == "" {
		return nil
	}
	name := claudeSessionRenameName(rec)
	if name == "" {
		return nil
	}
	exe, err := claudeRenameHelperExecutable()
	if err != nil {
		return fmt.Errorf("resolve amq-squad executable: %w", err)
	}
	return claudeRenameHelperStart(exe, []string{
		claudeRenameHelperCommand,
		"--pane", strings.TrimSpace(rec.Tmux.PaneID),
		"--name", name,
		"--delay", defaultClaudeRenameDelay.String(),
	})
}

func runClaudeSessionRename(args []string) error {
	fs := flag.NewFlagSet(claudeRenameHelperCommand, flag.ContinueOnError)
	pane := fs.String("pane", "", "target tmux pane id")
	name := fs.String("name", "", "Claude session name")
	delay := fs.Duration("delay", defaultClaudeRenameDelay, "delay before delivery")
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: amq-squad %s --pane PANE --name NAME [--delay DURATION]\n", claudeRenameHelperCommand)
	}
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return usageErrorf("%s accepts flags only", claudeRenameHelperCommand)
	}
	paneID := strings.TrimSpace(*pane)
	if paneID == "" {
		return usageErrorf("--pane is required")
	}
	if strings.TrimSpace(*name) == "" {
		return usageErrorf("--name is required")
	}
	if *delay < 0 {
		return usageErrorf("--delay cannot be negative")
	}
	rename := sanitizeClaudeSessionRenameName(*name)
	if *delay > 0 {
		time.Sleep(*delay)
	}
	return sendPromptToPane(paneID, "/rename "+rename)
}

func claudeSessionRenameName(rec launch.Record) string {
	base := firstNonEmpty(rec.Role, rec.Handle, defaultHandleFor(rec.Binary))
	base = sanitizeClaudeSessionRenameName(base)
	session := sanitizeClaudeSessionRenameName(rec.Session)
	if strings.TrimSpace(rec.Session) != "" && session != "" && session != base && !strings.Contains(base, session) {
		return base + "-" + session
	}
	return base
}

func sanitizeClaudeSessionRenameName(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var b strings.Builder
	lastSep := false
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastSep = false
		default:
			if !lastSep {
				b.WriteByte('-')
				lastSep = true
			}
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "claude-agent"
	}
	return out
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
