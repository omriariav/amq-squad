package cli

import (
	"flag"
	"fmt"
	"os"

	"github.com/omriariav/amq-squad/internal/team"
)

func runUp(args []string) error {
	fs := flag.NewFlagSet("up", flag.ContinueOnError)
	dryRun := fs.Bool("dry-run", false, "print the launch plan instead of bringing the team up")
	pf := registerPreviewFlags(fs)
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, `amq-squad up - bring this project's team up (replacement for 'team show'/'team launch')

Usage:
  amq-squad up --dry-run [--session name] [--fresh] [--no-bootstrap] [--trust sandboxed|trusted] [--model role=model,...] [--codex-args args] [--claude-args args] [--force-duplicate]

--dry-run prints one launch command per member, identical to 'amq-squad team show'.
Bringing the team up live is not implemented in this slice; pass --dry-run.
`)
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	if !*dryRun {
		return usageErrorf("amq-squad up currently requires --dry-run. Live up will land in a later step; for now use --dry-run or 'amq-squad team launch'.")
	}
	opts, err := pf.toEmitOptions(fs)
	if err != nil {
		return err
	}
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getwd: %w", err)
	}
	if !team.Exists(cwd) {
		return fmt.Errorf("no team configured. Run 'amq-squad team init' first.")
	}
	return emitTeamCommands(cwd, opts)
}
