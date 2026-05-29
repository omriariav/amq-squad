package cli

import (
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/omriariav/amq-squad/internal/console"
	"github.com/omriariav/amq-squad/internal/state"
	"github.com/omriariav/amq-squad/internal/team"
)

// runConsole is the `amq-squad console` verb: a read-only, full-screen Mission
// Control TUI over every discovered session, or — with --once — a single static
// text board to stdout for non-TTY / CI use.
//
// The interactive TUI renders to /dev/tty (never os.Stdout), so the
// stdout-purity contract the other verbs' tests assert stays intact. --once is
// the sole path that writes the board to stdout.
//
// It degrades gracefully: no team configured (or no TTY) emits guidance; an
// unresolvable AMQ base root degrades to an explanatory state rather than
// crashing.
func runConsole(args []string) error {
	fs := flag.NewFlagSet("console", flag.ContinueOnError)
	profileFlag := fs.String("profile", "", "team profile to inspect (default: default profile)")
	sessionFlag := fs.String("session", "", "scope the console to a single session (default: all sessions)")
	refresh := fs.Duration("refresh", console.DefaultRefresh, "periodic resync cadence (e.g. 2s)")
	atRiskWait := fs.Duration("at-risk-wait", state.DefaultAtRiskWait, "an awaiting-reply thread older than this is at risk")
	reviewAge := fs.Duration("review-age", state.DefaultReviewAge, "an unanswered review/question older than this is at risk")
	once := fs.Bool("once", false, "render one static board to stdout and exit (non-TTY / CI)")
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, `amq-squad console - live read-only Mission Control over your sessions

Usage:
  amq-squad console [--profile NAME] [--session NAME] [--refresh 2s]
                    [--at-risk-wait 5m] [--review-age 15m] [--once]

A full-screen, read-only TUI showing every discovered session, its triage
rollup (needs-you / at-risk / blocked), and per-agent liveness. The TUI
renders to your terminal (/dev/tty); stdout stays clean.

With --once it renders a single static board to STDOUT and exits — use this
in CI or when there is no terminal attached.

Examples:
  amq-squad console
  amq-squad console --once
  amq-squad console --session issue-96 --at-risk-wait 5m
`)
	}
	if err := parseFlags(fs, args); err != nil {
		return err
	}

	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getwd: %w", err)
	}

	return executeConsole(consoleExecution{
		ProjectDir:  cwd,
		Profile:     *profileFlag,
		Session:     *sessionFlag,
		Refresh:     *refresh,
		AtRiskWait:  *atRiskWait,
		ReviewAge:   *reviewAge,
		Once:        *once,
		Out:         os.Stdout,
		Stderr:      os.Stderr,
		TeamExists:  team.ExistsProfile,
		ResolveBase: scanBaseRootForProject,
		StdoutIsTTY: outputIsTTY(),
		RunConsole:  console.Run,
	})
}

// consoleExecution carries the inputs for the console verb so tests can drive
// flag parsing + dispatch with seams (no real TTY, an injected resolver and a
// captured RunConsole) without ever starting a Bubble Tea program.
type consoleExecution struct {
	ProjectDir string
	Profile    string
	Session    string
	Refresh    time.Duration
	AtRiskWait time.Duration
	ReviewAge  time.Duration
	Once       bool
	Out        io.Writer
	Stderr     io.Writer

	// Seams.
	TeamExists  func(projectDir, profile string) bool
	ResolveBase func(projectDir string) (string, error)
	StdoutIsTTY bool
	// RunConsole runs Mission Control. Injected so tests assert the assembled
	// Config without launching a real program; production passes console.Run.
	RunConsole func(console.Config) error
}

// executeConsole resolves the profile, the AMQ base root, and the team/TTY
// gating, then hands an assembled console.Config to RunConsole. The gating
// rules:
//
//   - No team configured + (--once OR no TTY): write clear guidance to stdout
//     and exit 0 (it is not a crash to have no team). Returns nonzero only when
//     truly unusable.
//   - No team configured + TTY: hand a NoTeam notice to the console so it shows
//     the explanatory failure-state screen instead of aborting.
//   - Unresolvable base root: degrade to a NoTeam-style guidance state (notice
//     names the `amq env` probe), never panic.
func executeConsole(s consoleExecution) error {
	profile, err := resolveProfileFlag(s.Profile)
	if err != nil {
		return err
	}

	teamExists := s.TeamExists != nil && s.TeamExists(s.ProjectDir, profile)

	// No team configured. On a non-interactive surface (--once or no TTY) this is
	// guidance to stdout, not a crash. On an interactive surface, hand the
	// console a NoTeam notice so it shows the explanatory screen.
	if !teamExists {
		notice := fmt.Sprintf("amq-squad: no team configured for profile %q. "+
			"Run 'amq-squad team init%s' first, then 'amq-squad console'.",
			profile, profileInitHint(profile))
		if s.Once || !s.StdoutIsTTY {
			fmt.Fprintln(s.Out, notice)
			return nil
		}
		return s.RunConsole(console.Config{
			ProjectDir:   s.ProjectDir,
			Once:         false,
			Out:          s.Out,
			NoTeamNotice: notice,
		})
	}

	// Team exists: resolve the AMQ base root once. An unresolvable root degrades
	// to a guidance/NoTeam state naming the probe, never a panic.
	var baseRoot string
	if s.ResolveBase != nil {
		baseRoot, err = s.ResolveBase(s.ProjectDir)
	}
	notice := ""
	if err != nil || baseRoot == "" {
		notice = "amq-squad: could not resolve the AMQ base root via `amq env` " +
			"(is `amq` installed and on PATH?)."
		if err != nil {
			notice += " " + err.Error()
		}
	}

	// A --session scope is handed to the console as a typed-filter preset, so the
	// same predicate machinery applies in every view (and the --once board).
	initialFilter := ""
	if s.Session != "" {
		initialFilter = "session:" + s.Session
	}

	cfg := console.Config{
		ProjectDir: s.ProjectDir,
		BaseRoot:   baseRoot,
		Thresholds: state.Thresholds{
			AtRiskWait: s.AtRiskWait,
			ReviewAge:  s.ReviewAge,
		},
		Refresh:       s.Refresh,
		Once:          s.Once,
		Out:           s.Out,
		NoTeamNotice:  notice,
		InitialFilter: initialFilter,
	}

	// Non-interactive (--once) on a degraded root still emits guidance to stdout
	// and exits 0; the console.runOnce path handles the notice.
	if !s.Once && !s.StdoutIsTTY && notice == "" {
		// Interactive requested but no TTY and a healthy root: fall back to a
		// single static board on stdout rather than failing to open /dev/tty.
		cfg.Once = true
	}

	return s.RunConsole(cfg)
}
