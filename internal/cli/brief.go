package cli

import (
	"flag"
	"fmt"
	"os"

	"github.com/omriariav/amq-squad/v2/internal/team"
)

// gh#759/t13: brief is the one place LLM drafting happens on a path that can
// ever end in a launch. start/plan/wizard never invoke a drafter themselves
// (they fail closed naming this command when no brief exists) -- launch must
// never depend on an LLM succeeding. This command owns both drafting
// mechanisms that pre-date it: --goal (draftSimpleStartBrief's own
// generate-and-validate contract, moved here unchanged) and --seed-from
// (resolveSeed/buildSeedBrief/writeSeedBriefForProfile, already used by
// up/new and reused here as-is).
func runBrief(args []string) error {
	return runBriefWithDependencies(args, defaultSimpleStartDependencies())
}

// runBriefWithDependencies is runBrief with an injectable
// simpleStartDependencies, mirroring runStartWithDependencies's own
// testability seam so a test can stub ResolveDrafter/RunDrafter (e.g. to a
// panic stub, proving start/plan never reach it -- TestStartNeverInvokesDrafter --
// or to a hermetic fake for brief's own --goal tests) without a real
// configured drafter.
func runBriefWithDependencies(args []string, deps simpleStartDependencies) error {
	deps = normalizeSimpleStartDependencies(deps)
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
		tm, err := team.ReadProfile(resolvedContext.ProjectDir, resolvedContext.Profile)
		if err != nil {
			return fmt.Errorf("read team: %w", err)
		}
		draft, err := draftSimpleStartBrief(resolvedContext.ProjectDir, resolvedContext.Profile, resolvedContext.Session, *goalFlag, tm, deps)
		if err != nil {
			return err
		}
		if draft.Manual {
			fmt.Fprintf(os.Stdout, "drafter config source: %s\n", draft.ConfigSource)
			writeSimpleStartDrafterAttempts(os.Stdout, draft)
			fmt.Fprintln(os.Stdout, "\nNo brief was staged.")
			fmt.Fprintf(os.Stdout, "Reason: %s\nRemedy: %s\n\nManual drafting prompt:\n\n%s\n", draft.Reason, draft.Remedy, draft.Prompt)
			return fmt.Errorf("brief requires in-session completion: %s", draft.Reason)
		}
		content = string(draft.Document)
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
	fmt.Fprintf(os.Stdout, "next: amq-squad plan %s --project %s\n", resolvedContext.Session, resolvedContext.ProjectDir)
	return nil
}

func printBriefUsage() {
	fmt.Fprint(os.Stderr, `amq-squad brief - draft or seed the workstream brief

Usage:
  amq-squad brief (--goal TEXT | --seed-from REF) [--project DIR] [--profile NAME]
                  [--session NAME] [--force] [--dry-run]

brief is the one place drafting an LLM ever happens on a path that can end in
a launch. start, plan, and wizard never invoke a drafter themselves -- if the
workstream has no brief, they fail closed naming this command as the fix.

--goal TEXT drafts a review-ready brief through the profile's configured
drafter (the same generate-and-validate contract start used to run inline).
If the drafter itself needs in-session completion, brief prints the manual
drafting prompt and refuses (no brief is staged) rather than guessing.

--seed-from REF seeds the brief deterministically from file:<path>,
issue:<n>, or gh:owner/repo#<n> -- no drafter involved. --goal and
--seed-from are mutually exclusive.

Without --force, brief refuses to overwrite an existing brief. --dry-run
prints the candidate brief to stdout without writing anything.

Examples:
  amq-squad brief --goal "fix issue #96" --session issue-96
  amq-squad brief --seed-from issue:96 --session issue-96
  amq-squad brief --seed-from file:./notes.md --session issue-96 --dry-run
`)
}
