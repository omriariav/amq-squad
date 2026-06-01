package cli

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/omriariav/amq-squad/v2/internal/team"
)

type teamRemoveExecution struct {
	ProjectDir string
	Profile    string
	Yes        bool
	DryRun     bool
	Confirm    io.Reader
	Out        io.Writer
}

func runTeamRemove(args []string) error {
	fs := flag.NewFlagSet("team rm", flag.ContinueOnError)
	projectFlag := fs.String("project", "", "project/team-home directory to target (default: cwd)")
	profileFlag := fs.String("profile", "", "team profile to delete (default: default profile)")
	yes := fs.Bool("yes", false, "skip the confirmation prompt (for automation)")
	fs.BoolVar(yes, "y", false, "shorthand for --yes")
	dryRun := fs.Bool("dry-run", false, "preview the deletion without removing files")
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, `amq-squad team rm - delete one team profile config

Usage:
  amq-squad team rm [PROFILE] [--project DIR] [--profile NAME] [--dry-run] [--yes|-y]
  amq-squad team delete [PROFILE] [--project DIR] [--profile NAME] [--dry-run] [--yes|-y]

Deletes the selected team profile config only:
  default profile -> .amq-squad/team.json
  named profile   -> .amq-squad/teams/<profile>.json

It does not delete AMQ session mailboxes, workstream briefs, team-rules.md, or
CLAUDE.md / AGENTS.md pointer stubs. Use top-level archive/rm for sessions.

By default the command previews the exact profile file and prompts for
confirmation (default: No). Pass --dry-run to preview without a prompt, or
--yes/-y for automation.

Examples:
  amq-squad team rm --dry-run
  amq-squad team rm --profile review
  amq-squad team delete review --project ~/Code/app --yes
`)
	}
	args = allowInterspersedFlags(fs, args)
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if fs.NArg() > 1 {
		return usageErrorf("team rm takes at most one profile argument; got %d", fs.NArg())
	}
	if fs.NArg() == 1 && flagWasSet(fs, "profile") {
		return usageErrorf("pass the profile either positionally or with --profile, not both")
	}
	profileArg := *profileFlag
	if fs.NArg() == 1 {
		profileArg = fs.Arg(0)
	}
	profile, err := resolveProfileFlag(profileArg)
	if err != nil {
		return err
	}
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getwd: %w", err)
	}
	projectDir, err := resolveProjectDirFlag(cwd, *projectFlag, flagWasSet(fs, "project"))
	if err != nil {
		return err
	}
	return executeTeamRemove(teamRemoveExecution{
		ProjectDir: projectDir,
		Profile:    profile,
		Yes:        *yes,
		DryRun:     *dryRun,
		Confirm:    os.Stdin,
		Out:        os.Stdout,
	})
}

func executeTeamRemove(e teamRemoveExecution) error {
	projectDir := strings.TrimSpace(e.ProjectDir)
	if projectDir == "" {
		return fmt.Errorf("project dir cannot be empty")
	}
	profile, err := resolveProfileFlag(e.Profile)
	if err != nil {
		return err
	}
	if !team.ExistsProfile(projectDir, profile) {
		return fmt.Errorf("no team configured for profile %q at %s. Nothing removed.", profile, team.ProfilePath(projectDir, profile))
	}
	t, err := team.ReadProfile(projectDir, profile)
	if err != nil {
		return fmt.Errorf("read team profile %q: %w", profile, err)
	}
	out := e.Out
	if out == nil {
		out = os.Stdout
	}
	path := team.ProfilePath(projectDir, profile)
	fmt.Fprintln(out, "Team profile removal preview")
	fmt.Fprintf(out, "project:  %s\n", projectDir)
	fmt.Fprintf(out, "profile:  %s\n", profile)
	fmt.Fprintf(out, "path:     %s\n", path)
	fmt.Fprintf(out, "members:  %d\n", len(t.Members))
	fmt.Fprintln(out, "keeps:    AMQ sessions, briefs, team-rules.md, pointer stubs")
	if e.DryRun {
		fmt.Fprintln(out, "Dry run: no files removed.")
		return nil
	}
	if !e.Yes {
		if !confirmTeamRemove(out, e.Confirm, profile) {
			fmt.Fprintln(out, "team rm: aborted; no files removed.")
			return nil
		}
	}
	if err := team.DeleteProfile(projectDir, profile); err != nil {
		return fmt.Errorf("delete team profile %q: %w", profile, err)
	}
	fmt.Fprintf(out, "Removed team profile %s at %s.\n", profile, path)
	return nil
}

func confirmTeamRemove(out io.Writer, r io.Reader, profile string) bool {
	if r == nil {
		r = os.Stdin
	}
	fmt.Fprintf(out, "Delete team profile %s? [y/N] ", profile)
	line, err := bufio.NewReader(r).ReadString('\n')
	if err != nil && strings.TrimSpace(line) == "" {
		return false
	}
	answer := strings.ToLower(strings.TrimSpace(line))
	return answer == "y" || answer == "yes"
}
