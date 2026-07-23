package cli

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/omriariav/amq-squad/v2/internal/team"
)

// runTeamSharedCwdException manages team.Team.SharedCwdException (#497) on an
// already-existing profile, so recording the exception never requires
// recreating the roster (the exact friction #523 fixed for profile reuse).
func runTeamSharedCwdException(args []string) error {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" {
		fmt.Fprint(os.Stderr, `amq-squad team shared-cwd-exception - record or clear the shared-worktree exception

Usage:
  amq-squad team shared-cwd-exception set "<reason>" [--project DIR] [--profile NAME]
  amq-squad team shared-cwd-exception clear [--project DIR] [--profile NAME]
  amq-squad team shared-cwd-exception show [--json] [--project DIR] [--profile NAME]

Readiness fails closed when 2+ mutation-capable members share one working
directory (#497). 'set' records an explicit reason accepting that (e.g. only
one member mutates at a time, work is intentionally serial, or the additional
members are read-only reviewers). 'clear' returns to the fail-closed default.
`)
		if len(args) == 0 {
			return usageErrorf("team shared-cwd-exception requires a subcommand ('set', 'clear', or 'show')")
		}
		return nil
	}
	switch args[0] {
	case "set":
		return runTeamSharedCwdExceptionSet(args[1:])
	case "clear":
		return runTeamSharedCwdExceptionClear(args[1:])
	case "show":
		return runTeamSharedCwdExceptionShow(args[1:])
	default:
		return usageErrorf("unknown 'team shared-cwd-exception' subcommand: %q. Try 'set', 'clear', or 'show'.", args[0])
	}
}

func runTeamSharedCwdExceptionSet(args []string) error {
	reason, rest, ok := peelPositional(args)
	if !ok || strings.TrimSpace(reason) == "" {
		return usageErrorf(`a reason is required, e.g. 'team shared-cwd-exception set "only one member mutates"'`)
	}
	fs := flag.NewFlagSet("team shared-cwd-exception set", flag.ContinueOnError)
	projectFlag := fs.String("project", "", "project/team-home directory (default: cwd)")
	profileFlag := fs.String("profile", "", "team profile to mutate (default: default profile)")
	if err := parseFlags(fs, rest); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return usageErrorf("unexpected argument %q", fs.Arg(0))
	}
	projectDir, profile, err := resolveExistingTeamProfile(*projectFlag, *profileFlag, flagWasSet(fs, "project"))
	if err != nil {
		return err
	}
	if err := withProfileLock(projectDir, profile, func() error {
		t, err := team.ReadProfile(projectDir, profile)
		if err != nil {
			return fmt.Errorf("read team: %w", err)
		}
		t.SharedCwdException = strings.TrimSpace(reason)
		return team.WriteProfileUnderLock(projectDir, profile, t)
	}); err != nil {
		return err
	}
	fmt.Printf("shared-cwd exception recorded: %s\n", strings.TrimSpace(reason))
	return nil
}

func runTeamSharedCwdExceptionClear(args []string) error {
	fs := flag.NewFlagSet("team shared-cwd-exception clear", flag.ContinueOnError)
	projectFlag := fs.String("project", "", "project/team-home directory (default: cwd)")
	profileFlag := fs.String("profile", "", "team profile to mutate (default: default profile)")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return usageErrorf("unexpected argument %q", fs.Arg(0))
	}
	projectDir, profile, err := resolveExistingTeamProfile(*projectFlag, *profileFlag, flagWasSet(fs, "project"))
	if err != nil {
		return err
	}
	if err := withProfileLock(projectDir, profile, func() error {
		t, err := team.ReadProfile(projectDir, profile)
		if err != nil {
			return fmt.Errorf("read team: %w", err)
		}
		t.SharedCwdException = ""
		return team.WriteProfileUnderLock(projectDir, profile, t)
	}); err != nil {
		return err
	}
	fmt.Println("shared-cwd exception cleared; readiness fails closed on a detected collision again.")
	return nil
}

func runTeamSharedCwdExceptionShow(args []string) error {
	fs := flag.NewFlagSet("team shared-cwd-exception show", flag.ContinueOnError)
	projectFlag := fs.String("project", "", "project/team-home directory (default: cwd)")
	profileFlag := fs.String("profile", "", "team profile to read (default: default profile)")
	jsonOut := fs.Bool("json", false, "emit a schema-versioned envelope")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return usageErrorf("unexpected argument %q", fs.Arg(0))
	}
	projectDir, profile, err := resolveExistingTeamProfile(*projectFlag, *profileFlag, flagWasSet(fs, "project"))
	if err != nil {
		return err
	}
	t, err := team.ReadProfile(projectDir, profile)
	if err != nil {
		return fmt.Errorf("read team: %w", err)
	}
	if *jsonOut {
		return printJSONEnvelope("team_shared_cwd_exception", struct {
			Reason string `json:"reason,omitempty"`
		}{Reason: t.SharedCwdException})
	}
	if t.SharedCwdException == "" {
		fmt.Println("shared-cwd exception: (none)")
		return nil
	}
	fmt.Printf("shared-cwd exception: %s\n", t.SharedCwdException)
	return nil
}
