package cli

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/console"
	"github.com/omriariav/amq-squad/v2/internal/noc"
	"github.com/omriariav/amq-squad/v2/internal/state"
	"github.com/omriariav/amq-squad/v2/internal/team"
)

// rootList is a repeatable string flag: each --root DIR appends one root. The
// stdlib flag package has no built-in slice flag, so this implements flag.Value.
type rootList []string

func (r *rootList) String() string {
	if r == nil {
		return ""
	}
	return fmt.Sprint([]string(*r))
}

func (r *rootList) Set(v string) error {
	*r = append(*r, v)
	return nil
}

// runNOC is the `amq-squad noc` verb: the read-only, full-screen NOC command
// center over EVERY discovered amq-squad project under one or more roots. With
// --once it renders a single static multi-root board to stdout (CI / no-TTY).
//
// READ-ONLY: the only side effect the surface can cause is a tmux view switch
// (it moves the operator's terminal to an agent's window, never squad state).
//
// It degrades gracefully: no projects found under the roots renders a clear
// guidance state, never a crash.
func runNOC(args []string) error {
	fs := flag.NewFlagSet("noc", flag.ContinueOnError)
	var roots rootList
	fs.Var(&roots, "root", "directory to scan for amq-squad projects (repeatable; default: the project's parent, or cwd)")
	depth := fs.Int("depth", noc.DefaultDepth, "how deep to scan under each root for .agent-mail projects")
	refresh := fs.Duration("refresh", console.NOCDefaultRefresh, "periodic resync cadence (e.g. 2s)")
	atRiskWait := fs.Duration("at-risk-wait", state.DefaultAtRiskWait, "an awaiting-reply thread older than this is at risk")
	reviewAge := fs.Duration("review-age", state.DefaultReviewAge, "an unanswered review/question older than this is at risk")
	staleAfter := fs.Duration("stale-after", state.DefaultStaleAfter, "a thread untouched longer than this is STALE: age-decayed, demoted below live squads, rendered dim")
	once := fs.Bool("once", false, "render one static board to stdout and exit (non-TTY / CI)")
	tree := fs.Bool("tree", false, "with --once: render the full root->project->session->agent tree instead of the rollup digest")
	all := fs.Bool("all", false, "alias for --tree (full expansion under --once)")
	hideStale := fs.Bool("hide-stale", false, "hide stopped/archived (stale) squads — focus on what is alive")
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, `amq-squad noc - live read-only NOC command center across all your squads

Usage:
  amq-squad noc [--root DIR ...] [--depth N] [--refresh 2s]
                [--at-risk-wait 5m] [--review-age 15m] [--stale-after 72h]
                [--once] [--tree|--all] [--hide-stale]

A full-screen, read-only TUI ("network operations center") over EVERY discovered
amq-squad project under the given roots. It shows a header pulse (squads / live /
needs-you / at-risk(live) / blocked(live) / stale counts), a collapsible
attention-first tree (root -> project -> session -> agent), and a detail pane for
the selection. On a running agent, enter (or J) JUMPS your terminal to that
agent's tmux window — the only side effect; nothing here can stop/start/message/
delete an agent.

The NOC rewards LIVENESS: a running squad active just now sorts to the top, while
a stopped squad whose only blocked threads are days old (older than --stale-after)
is age-decayed to the bottom and rendered dim. Press h (or --hide-stale) to hide
stale squads entirely.

--root is repeatable and defaults to the project's parent (so sibling squads
appear) or the current directory. The TUI renders to /dev/tty; stdout stays
clean. With --once it renders a rollup digest (needs-attention + project
rollups) to STDOUT; add --tree (or --all) for the full expansion.

Examples:
  amq-squad noc
  amq-squad noc --root ~/Code --depth 5
  amq-squad noc --once | less -R
  amq-squad noc --once --tree
  amq-squad noc --hide-stale --stale-after 24h
`)
	}
	if err := parseFlags(fs, args); err != nil {
		return err
	}

	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getwd: %w", err)
	}

	return executeNOC(nocExecution{
		Cwd:         cwd,
		Roots:       []string(roots),
		Depth:       *depth,
		Refresh:     *refresh,
		AtRiskWait:  *atRiskWait,
		ReviewAge:   *reviewAge,
		StaleAfter:  *staleAfter,
		Once:        *once,
		Tree:        *tree || *all,
		HideStale:   *hideStale,
		Out:         os.Stdout,
		StdoutIsTTY: outputIsTTY(),
		RunNOC:      console.RunNOC,
	})
}

// nocExecution carries the resolved inputs for the noc verb so tests can drive
// dispatch with seams (no real TTY, a captured RunNOC) without starting a
// Bubble Tea program.
type nocExecution struct {
	Cwd        string
	Roots      []string
	Depth      int
	Refresh    time.Duration
	AtRiskWait time.Duration
	ReviewAge  time.Duration
	StaleAfter time.Duration
	Once       bool
	Tree       bool
	HideStale  bool
	Out        io.Writer

	// Seams.
	StdoutIsTTY bool
	// RunNOC runs the NOC surface. Injected so tests assert the assembled config
	// without launching a real program; production passes console.RunNOC.
	RunNOC func(console.NOCConfig) error
}

// executeNOC resolves the roots and the TTY gating, then hands an assembled
// console.NOCConfig to RunNOC.
//
//   - No explicit --root: default to defaultNOCRoots(cwd).
//   - Interactive requested but no TTY: fall back to a single static board on
//     stdout (so a piped invocation still works) rather than failing to open
//     /dev/tty.
func executeNOC(s nocExecution) error {
	roots := s.Roots
	if len(roots) == 0 {
		roots = defaultNOCRoots(s.Cwd)
	}

	cfg := console.NOCConfig{
		Roots: roots,
		Depth: s.Depth,
		Thresholds: state.Thresholds{
			AtRiskWait: s.AtRiskWait,
			ReviewAge:  s.ReviewAge,
			StaleAfter: s.StaleAfter,
		},
		Refresh:   s.Refresh,
		Once:      s.Once,
		Tree:      s.Tree,
		HideStale: s.HideStale,
		Out:       s.Out,
	}

	// Interactive requested but no TTY: render a single static board to stdout.
	if !s.Once && !s.StdoutIsTTY {
		cfg.Once = true
	}

	// Inject the stop/resume lifecycle seam (PR15). cli owns these verbs
	// (executeDown/executeResume); the NOC reaches them ONLY after the operator
	// confirms the preview overlay. The --once path is non-interactive, so the
	// seam is never invoked there even though it is set.
	cfg.Lifecycle = consoleLifecycle
	return s.RunNOC(cfg)
}

// consoleLifecycle is the cli-side stop/resume seam handed to the NOC. It is
// invoked ONLY for a confirmed Stop/Resume from the TUI. It drives the SAME
// verbs the CLI exposes (executeDown / executeResume) against the squad the NOC
// previewed, so a NOC stop is byte-identical to `amq-squad stop --all` and a NOC
// resume to `amq-squad resume`. The verb report is captured to a buffer so it
// never corrupts the AltScreen frame; the NOC surfaces a one-line note instead.
func consoleLifecycle(req console.LifecycleRequest) error {
	dir := req.ProjectDir
	if strings.TrimSpace(dir) == "" {
		if cwd, err := os.Getwd(); err == nil {
			dir = cwd
		}
	}
	var sink bytes.Buffer
	switch req.Verb {
	case "stop":
		return executeDown(downExecution{
			Verb:             "stop",
			ProjectDir:       dir,
			RequestedSession: req.Session,
			ExplicitSession:  req.Session != "",
			All:              true,
			Profile:          team.DefaultProfile,
			Terminator:       newSignalTerminator(false),
			Probe:            defaultDuplicateLaunchProbe,
			Out:              &sink,
		})
	case "resume":
		return executeResume(resumeExecution{
			ProjectDir:       dir,
			RequestedSession: req.Session,
			ExplicitSession:  req.Session != "",
			Mode:             resumeModeDefault,
			Profile:          team.DefaultProfile,
		})
	default:
		return fmt.Errorf("unknown lifecycle verb %q", req.Verb)
	}
}

// defaultNOCRoots picks the default scan roots for a starting directory:
//
//   - If cwd is itself an amq-squad project (it has a .agent-mail child), scan
//     its PARENT so sibling squads under the same workspace appear too.
//   - Otherwise scan cwd itself.
//
// This matches the brief's "default to the project's parent if a single project,
// else cwd" intent while staying a pure function of cwd (no filesystem walk
// beyond the single stat).
func defaultNOCRoots(cwd string) []string {
	if cwd == "" {
		return nil
	}
	if isAMQProject(cwd) {
		parent := filepath.Dir(cwd)
		if parent != "" && parent != cwd {
			return []string{parent}
		}
	}
	return []string{cwd}
}

// isAMQProject reports whether dir contains a .agent-mail child directory.
func isAMQProject(dir string) bool {
	info, err := os.Stat(filepath.Join(dir, noc.AgentMailDirName))
	return err == nil && info.IsDir()
}
