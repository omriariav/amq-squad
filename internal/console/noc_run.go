// Package console — noc_run.go: wire the NOC Bubble Tea program and its data
// feeds, and the --once static render.
package console

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/omriariav/amq-squad/v2/internal/noc"
	"github.com/omriariav/amq-squad/v2/internal/state"
)

// NOCConfig is the NOC entrypoint's configuration.
type NOCConfig struct {
	Roots         []string
	Depth         int
	Thresholds    state.Thresholds
	Refresh       time.Duration
	Once          bool
	Out           io.Writer
	InitialFilter string
	// Tree forces the full root→project→session→agent expansion in the --once
	// static render (the old full board). Default --once leads with project
	// rollups + a needs-attention section. Ignored by the interactive TUI, which
	// always drills via the tree.
	Tree bool
	// HideStale starts the surface with stopped/archived (stale) squads hidden.
	HideStale bool
	// Lifecycle is the cli-injected stop/resume seam (PR15). cli owns the
	// executeDown/executeResume verbs; passing a closure here lets the live NOC
	// drive them WITHOUT a console→cli import cycle. nil means stop/resume
	// degrade to a "no lifecycle backend" note rather than a panic. It is reached
	// ONLY after the operator confirms the preview overlay, and only on the live
	// (interactive) path — --once is non-interactive so nothing can confirm.
	Lifecycle func(LifecycleRequest) error
}

// LifecycleRequest is the public, cli-facing shape of a confirmed stop/resume.
// It carries the exact scope the NOC previewed (verb + project dir + session +
// affected agents) so the cli closure can call the right verb on the right
// squad. Verb is "stop" or "resume". Keeping this type free of console
// internals is what lets cli inject the seam without importing unexported types.
type LifecycleRequest struct {
	Verb       string
	ProjectDir string
	Session    string
	Agents     []string
}

// adaptLifecycle bridges the public, cli-facing LifecycleRequest seam to the
// model's internal lifecycleOp seam. nil in → nil out (stop/resume then degrade
// to a note).
func adaptLifecycle(fn func(LifecycleRequest) error) func(lifecycleOp) error {
	if fn == nil {
		return nil
	}
	return func(op lifecycleOp) error {
		return fn(LifecycleRequest{
			Verb:       string(op.Verb),
			ProjectDir: op.ProjectDir,
			Session:    op.Session,
			Agents:     op.Agents,
		})
	}
}

// RunNOC is the NOC entrypoint. With cfg.Once it renders a single static board
// to cfg.Out; otherwise it starts the live program on /dev/tty.
func RunNOC(cfg NOCConfig) error {
	if cfg.Once {
		return runNOCOnce(cfg)
	}
	return runNOCLive(cfg)
}

// runNOCOnce renders a single static multi-root board for non-TTY / CI use. The
// color mode is resolved against the Out writer's TTY-ness so output is plain
// (no escape codes) when piped or under NO_COLOR.
func runNOCOnce(cfg NOCConfig) error {
	rebuild := nocRebuildFromConfig(cfg)
	ms := noc.Collect(rebuild.Roots, rebuild.Depth, rebuild.Probe, rebuild.Thresholds)

	m := newNOCModel(rebuild)
	// --once renders to a (possibly non-TTY) writer: resolve color from the
	// writer, not from an assumed interactive terminal.
	mode := resolveColorMode(writerIsTTY(cfg.Out))
	m.colorMode = mode
	m.th = newNOCTheme(mode)
	if cfg.InitialFilter != "" {
		m.filter = cfg.InitialFilter
	}
	m.fullTree = cfg.Tree
	m.hideStale = cfg.HideStale
	m.ms = ms
	m.ready = true
	m.refreshGuidance()
	if cfg.Out != nil {
		fmt.Fprintln(cfg.Out, m.staticView())
	}
	return nil
}

// runNOCLive starts the Bubble Tea program against /dev/tty. Falls back to a
// single static board on stdout if no tty is available.
func runNOCLive(cfg NOCConfig) error {
	rebuild := nocRebuildFromConfig(cfg)
	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return runNOCOnce(cfg)
	}
	defer tty.Close()

	m := newNOCModel(rebuild)
	if cfg.InitialFilter != "" {
		m.filter = cfg.InitialFilter
	}
	m.hideStale = cfg.HideStale
	// Wire the confirmed stop/resume seam (PR15). The AMQ-write seam (sendOp)
	// already defaults to act.Send in newNOCModel; here we bridge the cli-owned
	// lifecycle verbs onto the model's internal seam. Both are reached only after
	// the operator confirms the preview overlay.
	m.lifecycle = adaptLifecycle(cfg.Lifecycle)
	// Seed an initial snapshot synchronously so the first frame is populated.
	m.ms = noc.Collect(rebuild.Roots, rebuild.Depth, rebuild.Probe, rebuild.Thresholds)
	m.ready = true
	m.refreshGuidance()

	opts := []tea.ProgramOption{
		tea.WithInput(tty),
		tea.WithOutput(tty),
		tea.WithAltScreen(),
	}
	// Drive the program as a POINTER so each key handler's cursor / collapse /
	// filter mutation lands on the SAME model the event loop re-binds and renders
	// on the next frame. With a value model + pointer-receiver movement helpers,
	// a keypress would mutate a throwaway copy and the live surface would look
	// frozen (arrows / j / k dead).
	p := tea.NewProgram(&m, opts...)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	startNOCFeeds(ctx, p, rebuild)

	_, err = p.Run()
	return err
}

// nocRebuildFromConfig assembles the immutable rebuild config.
func nocRebuildFromConfig(cfg NOCConfig) NOCRebuildConfig {
	depth := cfg.Depth
	if depth <= 0 {
		depth = noc.DefaultDepth
	}
	return NOCRebuildConfig{
		Roots:      cfg.Roots,
		Depth:      depth,
		Probe:      state.DefaultProbe,
		Thresholds: cfg.Thresholds,
		Refresh:    cfg.Refresh,
	}
}

// nocRebuildCmd collects a fresh MultiSnapshot off the immutable rebuild config
// and delivers it as a nocSnapshotMsg. Pure: it does not mutate the model.
func nocRebuildCmd(cfg NOCRebuildConfig) tea.Cmd {
	return func() tea.Msg {
		ms := noc.Collect(cfg.Roots, cfg.Depth, cfg.Probe, cfg.Thresholds)
		return nocSnapshotMsg{ms: ms}
	}
}

// nocTickCmd schedules the next periodic refresh at the given cadence.
func nocTickCmd(d time.Duration) tea.Cmd {
	if d <= 0 {
		d = NOCDefaultRefresh
	}
	return tea.Tick(d, func(_ time.Time) tea.Msg {
		return nocTickMsg{}
	})
}

// nocRediscoverTickCmd schedules the next periodic re-discovery.
func nocRediscoverTickCmd() tea.Cmd {
	return tea.Tick(NOCDefaultRediscover, func(_ time.Time) tea.Msg {
		return nocRediscoverMsg{}
	})
}

// writerIsTTY reports whether w is an *os.File backed by a terminal. Anything
// else (a bytes.Buffer in tests, a pipe in CI) is treated as non-TTY so the
// --once render stays plain text.
func writerIsTTY(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}
