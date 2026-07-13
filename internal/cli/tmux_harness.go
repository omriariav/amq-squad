package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/omriariav/amq-squad/v2/internal/tmuxharness"
)

func runTmuxHarness(args []string) error {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		printTmuxHarnessUsage()
		return nil
	}
	mode := args[0]
	if mode != "exec" && mode != "shell" {
		return usageErrorf("unknown tmux-harness mode %q: use exec or shell", mode)
	}
	fs := flag.NewFlagSet("tmux-harness "+mode, flag.ContinueOnError)
	cwd := fs.String("cwd", "", "working directory for the disposable launcher (default: current directory)")
	fs.Usage = printTmuxHarnessUsage
	if err := parseFlags(fs, args[1:]); err != nil {
		return err
	}
	command := fs.Args()
	if len(command) > 0 && command[0] == "--" {
		command = command[1:]
	}
	if mode == "exec" && len(command) == 0 {
		return usageErrorf("tmux-harness exec requires COMMAND after --")
	}
	if mode == "shell" && len(command) != 0 {
		return usageErrorf("tmux-harness shell does not take a command")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	shell := "/bin/sh"
	if mode == "shell" && strings.TrimSpace(os.Getenv("SHELL")) != "" {
		shell = os.Getenv("SHELL")
	}
	h, err := tmuxharness.Start(ctx, tmuxharness.Options{CWD: *cwd, Shell: shell})
	if err != nil {
		return err
	}
	defer func() {
		// The named return path below performs and joins cleanup. This defer is
		// only a final idempotent guard for panics/future early-return edits.
		_ = h.Close()
	}()
	printTmuxHarnessIdentity(h)
	streams := tmuxharness.Streams{Stdin: os.Stdin, Stdout: os.Stdout, Stderr: os.Stderr}
	if mode == "exec" {
		err = h.Run(ctx, command, streams)
		if err != nil {
			err = fmt.Errorf("tmux harness command failed: %w", err)
		}
	} else {
		err = h.Attach(ctx, streams)
		if err != nil {
			err = fmt.Errorf("tmux harness attach failed: %w", err)
		}
	}
	return errors.Join(err, h.Close())
}

func printTmuxHarnessIdentity(h *tmuxharness.Harness) {
	fmt.Fprintf(os.Stderr, "tmux harness socket: %s\n", h.SocketPath)
	fmt.Fprintf(os.Stderr, "tmux harness ids: session=%s window=%s pane=%s\n", h.SessionID, h.LauncherWindowID, h.LauncherPaneID)
	fmt.Fprintln(os.Stderr, "tmux harness cleanup: automatic on exit")
}

func printTmuxHarnessUsage() {
	fmt.Fprint(os.Stderr, `amq-squad tmux-harness - run smoke tests in an isolated tmux server

Usage:
  amq-squad tmux-harness exec [--cwd DIR] -- COMMAND [ARG...]
  amq-squad tmux-harness shell [--cwd DIR]

The harness creates a private named tmux server, captures exact session,
window, and pane IDs, and routes plain nested tmux clients (including tmux
run-shell helpers) back to that server. Inherited TMUX identity is discarded.
The server is torn down automatically on command exit, detach, or interrupt.

exec preserves COMMAND stdout exactly; harness identity is printed on stderr.
shell attaches an interactive client to the disposable launcher pane. Detach
from it normally (prefix then d) to trigger cleanup.

Options:
  --cwd DIR   Working directory for the launcher and command (default: current)

Examples:
  amq-squad tmux-harness exec --cwd /tmp/project -- sh ./smoke.sh
  amq-squad tmux-harness shell --cwd /tmp/project
`)
}
