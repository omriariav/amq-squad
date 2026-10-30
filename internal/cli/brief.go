package cli

import (
	"flag"
	"fmt"
	"os"

	"github.com/omriariav/amq-squad/v3/internal/team"
)

// runBrief is the off-path brief drafting verb (no launch path drafts): --goal
// drafts and validates the brief (draftBrief), --seed-from seeds it (resolveSeed).
func runBrief(args []string) error {
	fs := flag.NewFlagSet("brief", flag.ContinueOnError)
	goalFlag := fs.String("goal", "", "operator goal text to draft the brief from (mutually exclusive with --seed-from)")
	seedFrom := fs.String("seed-from", "", "seed the brief from a deterministic source: file:<path>, issue:<n>, or gh:owner/repo#<n> (mutually exclusive with --goal)")
	force := fs.Bool("force", false, "overwrite an existing brief")
	dryRun := fs.Bool("dry-run", false, "print the candidate brief without writing it")
	projectFlag := fs.String("project", "", "project/team-home directory (default: current directory)")
	sessionFlag := fs.String("session", "", "AMQ workstream session (default: team workstream)")
	profileFlag := fs.String("profile", "", "team profile (default: default profile)")
	registerScopedFlagAliases(fs, projectFlag, sessionFlag, profileFlag)
	fs.Usage = printBriefUsage
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if *goalFlag != "" && *seedFrom != "" {
		return usageErrorf("--goal and --seed-from are mutually exclusive")
	}
	if *goalFlag == "" && *seedFrom == "" {
		return usageErrorf("brief requires --goal TEXT or --seed-from REF")
	}

	resolvedContext, err := resolveCanonicalContext(contextResolveOptions{
		ProjectFlag: *projectFlag, ProfileFlag: *profileFlag, SessionFlag: *sessionFlag,
		ProjectExplicit: flagWasSet(fs, "project"), ProfileExplicit: flagWasSet(fs, "profile"), SessionExplicit: flagWasSet(fs, "session"),
	})
	if err != nil {
		return err
	}
	emitContextDiagnostics(resolvedContext)

	var content string
	if *seedFrom != "" {
		body, err := resolveSeed(*seedFrom)
		if err != nil {
			return err
		}
		content = buildSeedBrief(*seedFrom, body, seedNow())
	} else {
		tm, err := team.ReadFor(resolvedContext.ProjectDir, resolvedContext.Profile)
		if err != nil {
			return fmt.Errorf("read team: %w", err)
		}
		draft, err := draftBrief(resolvedContext.ProjectDir, resolvedContext.Profile, resolvedContext.Session, *goalFlag, tm)
		if err != nil {
			return err
		}
		if draft.Manual {
			fmt.Fprintf(os.Stdout, "drafter config source: %s\n%s", draft.ConfigSource, cliDrafterAttemptsText(draft.Attempts, draft.Evidence))
			fmt.Fprintln(os.Stdout, "\nNo brief was staged.")
			fmt.Fprintf(os.Stdout, "Reason: %s\nRemedy: %s\n\nManual drafting prompt:\n\n%s\n", draft.Reason, draft.Remedy, draft.Prompt)
			return fmt.Errorf("brief requires in-session completion: %s", draft.Reason)
		}
		content = draft.Text
	}

	if *dryRun {
		fmt.Fprint(os.Stdout, content)
		return nil
	}
	path, err := writeSeedBriefForProfile(resolvedContext.ProjectDir, resolvedContext.Profile, resolvedContext.Session, content, *force)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stdout, "wrote brief %s\n", path)
	return nil
}

func printBriefUsage() {
	fmt.Fprint(os.Stderr, `amq-squad brief - draft or seed the workstream brief

Usage:
  amq-squad brief (--goal TEXT | --seed-from REF) [--project DIR] [--profile NAME]
                  [--session NAME] [--force] [--dry-run]

--goal TEXT drafts the brief through the profile's configured drafter and
validates it; if the drafter needs in-session completion, brief prints the
manual drafting prompt and stages nothing. --seed-from REF seeds it from
file:<path>, issue:<n>, or gh:owner/repo#<n>, without a drafter.

Without --force, brief refuses to overwrite an existing brief. --dry-run
prints the candidate brief to stdout without writing anything.

Examples:
  amq-squad brief --goal "fix issue #96" --session issue-96
  amq-squad brief --seed-from issue:96 --session issue-96
  amq-squad brief --seed-from file:./notes.md --session issue-96 --dry-run
`)
}
