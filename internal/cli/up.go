package cli

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/omriariav/amq-squad/internal/team"
)

func runUp(args []string) error {
	fs := flag.NewFlagSet("up", flag.ContinueOnError)
	dryRun := fs.Bool("dry-run", false, "print the launch plan (one launch command per member) instead of bringing the team up")
	pf := registerPreviewFlags(fs)
	lf := registerLiveLaunchFlags(fs)
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, `amq-squad up - bring this project's team up

Usage:
  amq-squad up [--session workstream] [--fresh] [--terminal tmux]
    [--target current-window|new-session] [--layout vertical|horizontal|tiled]
    [--terminal-session name] [--stagger 750ms] [--no-bootstrap]
    [--trust sandboxed|trusted] [--model role=model,...]
    [--codex-args args] [--claude-args args]
    [--force-duplicate] [--dry-run]

Without --dry-run, opens the configured team through the selected terminal
backend (same path as 'team launch'). With --dry-run, prints one launch
command per member (same output as 'team show'); the terminal backend is
not invoked.

Supported terminal backends: %s
`, strings.Join(registeredTeamLaunchTerminals(), ", "))
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getwd: %w", err)
	}
	if !team.Exists(cwd) {
		return fmt.Errorf("no team configured. Run 'amq-squad team init' first.")
	}
	if *dryRun {
		opts, err := pf.toEmitOptions(fs)
		if err != nil {
			return err
		}
		return emitTeamCommands(cwd, opts)
	}
	opts, err := buildLiveLaunchOptions(fs, pf, lf)
	if err != nil {
		return err
	}
	return executeTeamLaunch(opts, flagWasSet(fs, "session"), flagWasSet(fs, "trust"))
}
