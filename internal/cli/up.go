package cli

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/omriariav/amq-squad/internal/team"
)

func runUp(args []string) error {
	fs := flag.NewFlagSet("up", flag.ContinueOnError)
	dryRun := fs.Bool("dry-run", false, "print the launch plan (one launch command per member) instead of bringing the team up")
	seedFrom := fs.String("seed-from", "", "seed the active brief from a deterministic source: file:<path>, issue:<n>, or gh:owner/repo#<n>")
	force := fs.Bool("force", false, "overwrite an existing active brief when --seed-from is set (no effect otherwise)")
	profileFlag := fs.String("profile", "", "team profile to bring up (default: default profile)")
	jsonOut := fs.Bool("json", false, "emit a schema-versioned JSON envelope (requires --dry-run; the live up path stays human-only in 11A)")
	pf := registerPreviewFlags(fs)
	lf := registerLiveLaunchFlags(fs)
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, `amq-squad up - bring this project's team up

Usage:
  amq-squad up [--profile NAME] [--session workstream] [--fresh]
    [--terminal tmux] [--target current-window|new-session]
    [--layout vertical|horizontal|tiled]
    [--terminal-session name] [--stagger 750ms] [--no-bootstrap]
    [--trust sandboxed|trusted] [--model role=model,...]
    [--codex-args args] [--claude-args args]
    [--force-duplicate]
    [--seed-from REF [--force]] [--dry-run]

Without --dry-run, opens the configured team through the selected terminal
backend (same path as 'team launch'). With --dry-run, prints one launch
command per member (same output as 'team show'); the terminal backend is
not invoked.

--seed-from sources (deterministic only in 8A):
  file:<path>            literal file body
  issue:<n>              gh issue view <n> in the current repo
  gh:<owner>/<repo>#<n>  gh issue view <n> --repo owner/repo

With --seed-from --dry-run the candidate brief is printed to stdout and
nothing is written. With --seed-from alone, the brief is written to
.amq-squad/briefs/<session>.md before the backend launches. --force
overwrites an existing brief; --force without --seed-from is an error.
--force-duplicate remains the separate duplicate-agent flag.

Supported terminal backends: %s
`, strings.Join(registeredTeamLaunchTerminals(), ", "))
	}
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if *force && *seedFrom == "" {
		return usageErrorf("--force without --seed-from has no effect; pass --force-duplicate for live-duplicate handling")
	}
	if *jsonOut && !*dryRun {
		return usageErrorf("--json requires --dry-run on `up`; the live launch path does not have a JSON contract in this release")
	}
	profile, err := resolveProfileFlag(*profileFlag)
	if err != nil {
		return err
	}
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getwd: %w", err)
	}
	if !team.ExistsProfile(cwd, profile) {
		return fmt.Errorf("no team configured for profile %q. Run 'amq-squad team init%s' first.", profile, profileInitHint(profile))
	}

	// Resolve --seed-from up front so source-shape failures (bad ref,
	// missing file, gh error) abort cleanly before any team-launch
	// validation. The brief is NOT written here; executeTeamLaunch performs
	// the write after preflight so a later launch-side rejection cannot
	// mutate disk.
	var seedContent string
	var seedTimestamp time.Time
	if *seedFrom != "" {
		body, err := resolveSeed(*seedFrom)
		if err != nil {
			return err
		}
		// Capture the timestamp once so the brief's frontmatter and the
		// JSON envelope's generated_at field are always identical, even
		// across an advancing clock.
		seedTimestamp = seedNow()
		seedContent = buildSeedBrief(*seedFrom, body, seedTimestamp)
	}

	if *dryRun {
		if seedContent != "" {
			if *jsonOut {
				return printJSONEnvelope("brief_candidate", briefCandidateEnvelope(*seedFrom, seedContent, seedTimestamp))
			}
			fmt.Print(seedContent)
			return nil
		}
		opts, err := pf.toEmitOptions(fs)
		if err != nil {
			return err
		}
		opts.Profile = profile
		opts.JSON = *jsonOut
		return emitTeamCommands(cwd, opts)
	}
	opts, err := buildLiveLaunchOptions(fs, pf, lf)
	if err != nil {
		return err
	}
	opts.SeedBriefContent = seedContent
	opts.SeedBriefForce = *force
	opts.Profile = profile
	return executeTeamLaunch(opts, flagWasSet(fs, "session"), flagWasSet(fs, "trust"))
}

// profileInitHint returns the suffix to suggest on a `team init` command
// when reporting a missing-team error for a named profile.
func profileInitHint(profile string) string {
	if profile == "" || profile == team.DefaultProfile {
		return ""
	}
	return " --profile " + profile
}

// briefCandidate is the kind="brief_candidate" payload emitted by
// `up --dry-run --json --seed-from REF`. It carries the resolved provenance
// fields the would-be brief frontmatter contains plus the rendered brief
// body so callers can inspect or write it themselves.
type briefCandidate struct {
	Source      string `json:"source"`
	GeneratedAt string `json:"generated_at"`
	Generator   string `json:"generator"`
	Content     string `json:"content"`
}

func briefCandidateEnvelope(source, content string, now time.Time) briefCandidate {
	return briefCandidate{
		Source:      source,
		GeneratedAt: now.UTC().Format(time.RFC3339),
		Generator:   "deterministic",
		Content:     content,
	}
}
