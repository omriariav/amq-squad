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
	// Seed an initial snapshot synchronously so the first frame is populated.
	m.ms = noc.Collect(rebuild.Roots, rebuild.Depth, rebuild.Probe, rebuild.Thresholds)
	m.ready = true
	m.refreshGuidance()

	opts := []tea.ProgramOption{
		tea.WithInput(tty),
		tea.WithOutput(tty),
		tea.WithAltScreen(),
	}
	p := tea.NewProgram(m, opts...)

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
