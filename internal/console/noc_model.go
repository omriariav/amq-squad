// Package console — noc_model.go: the Bubble Tea model for the NOC command
// center, a READ-ONLY, beautified multi-project TUI over noc.MultiSnapshot.
//
// READ-ONLY contract: no key may stop / start / message / delete an agent. The
// ONLY side effect permitted anywhere in the NOC surface is the tmux switch
// (noc.SwitchTo), which moves the OPERATOR'S VIEW, never squad state. The model
// itself is a pure view: data feeds deliver immutable nocSnapshotMsg values and
// no goroutine mutates the model.
package console

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/omriariav/amq-squad/v2/internal/noc"
	"github.com/omriariav/amq-squad/v2/internal/state"
)

// NOCDefaultRefresh is the periodic resync cadence when unset.
const NOCDefaultRefresh = 2 * time.Second

// NOCDefaultRediscover is how often the roots are re-walked so new/removed
// projects appear without a restart.
const NOCDefaultRediscover = 20 * time.Second

// NOCRebuildConfig is everything a NOC snapshot rebuild needs. Immutable across
// the program's lifetime.
type NOCRebuildConfig struct {
	Roots      []string
	Depth      int
	Probe      state.Probe
	Thresholds state.Thresholds
	// Refresh is the periodic resync cadence the refresh ticker uses. Zero falls
	// back to NOCDefaultRefresh.
	Refresh time.Duration
}

// refreshCadence returns the configured refresh, defaulted.
func (c NOCRebuildConfig) refreshCadence() time.Duration {
	if c.Refresh <= 0 {
		return NOCDefaultRefresh
	}
	return c.Refresh
}

// nocSwitcher is the injectable jump seam. Production points it at noc.SwitchTo;
// tests record the resolved target. It is a struct field (not a package global)
// so concurrent tests never collide.
type nocSwitcher func(noc.TmuxTarget) error

// nocPaneLister is the injectable tmux pane-list seam (read-only).
type nocPaneLister func() ([]noc.TmuxPane, error)

// NOCModel is the Bubble Tea model. Pure view + UI state.
type NOCModel struct {
	rebuild NOCRebuildConfig

	ms      noc.MultiSnapshot
	lastErr error

	tree   nocTreeState
	cursor int
	scroll int
	width  int
	height int
	ready  bool

	filter        string
	filterEditing bool
	showHelp      bool
	showTimeline  bool

	// hideStale hides STOPPED / stale (archived) squads so the operator can focus
	// on what is alive. Toggled by 'h'. Off by default.
	hideStale bool

	// fullTree forces the full root→project→session→agent expansion in the static
	// (--once) render. Off by default: --once leads with project rollups + a
	// needs-attention section, not the firehose. The interactive TUI always
	// drills via the tree regardless of this flag.
	fullTree bool

	// jumpNote is a transient status line (e.g. SuggestJump text when a jump
	// could not resolve a live pane). Cleared on the next navigation.
	jumpNote string

	// selectedID is the stable node id preserved across snapshot replacement.
	selectedID string

	colorMode ColorMode
	th        nocTheme

	// guidance is shown when no projects were found under the roots.
	guidance string

	// Seams.
	switchTo nocSwitcher
	panes    nocPaneLister
	pidTree  func(pid int) []int
}

// newNOCModel builds a model from an immutable rebuild config.
func newNOCModel(rebuild NOCRebuildConfig) NOCModel {
	mode := resolveColorMode(true)
	return NOCModel{
		rebuild:   rebuild,
		tree:      newNOCTreeState(),
		colorMode: mode,
		th:        newNOCTheme(mode),
		switchTo:  noc.SwitchTo,
		panes:     noc.DefaultPaneLister,
		pidTree:   defaultPidTree,
	}
}

// Init implements tea.Model: start the refresh + rediscover tickers.
func (m NOCModel) Init() tea.Cmd {
	return tea.Batch(nocTickCmd(m.rebuild.refreshCadence()), nocRediscoverTickCmd())
}

// nocSnapshotMsg delivers a freshly collected MultiSnapshot.
type nocSnapshotMsg struct {
	ms noc.MultiSnapshot
}

// nocTickMsg is the periodic refresh signal.
type nocTickMsg struct{}

// nocRediscoverMsg is the periodic re-discovery signal.
type nocRediscoverMsg struct{}

// nocWatchMsg signals a filesystem change under a watched .agent-mail tree.
type nocWatchMsg struct{}

// nodes returns the flattened visible tree for the current snapshot + state,
// honoring the hide-stale toggle.
func (m NOCModel) nodes() []nocNode {
	return buildNOCTree(m.ms, m.tree, m.filter, m.hideStale)
}

// selectedNode returns the node at the cursor.
func (m NOCModel) selectedNode() (nocNode, bool) {
	ns := m.nodes()
	if m.cursor < 0 || m.cursor >= len(ns) {
		return nocNode{}, false
	}
	return ns[m.cursor], true
}

// clampCursor keeps the cursor inside the visible node bounds.
func (m *NOCModel) clampCursor() {
	ns := m.nodes()
	if m.cursor >= len(ns) {
		m.cursor = len(ns) - 1
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
}

// rememberSelection records the id at the cursor for cross-snapshot stability.
func (m *NOCModel) rememberSelection() {
	if n, ok := m.selectedNode(); ok {
		m.selectedID = n.id
	}
}

// preserveSelection re-locates the previously selected id after a snapshot or
// collapse change so a refresh never yanks the selection.
func (m *NOCModel) preserveSelection() {
	if m.selectedID == "" {
		return
	}
	for i, n := range m.nodes() {
		if n.id == m.selectedID {
			m.cursor = i
			return
		}
	}
	// The selected node vanished (collapsed parent / removed project): clamp.
	m.clampCursor()
}

// hasProjects reports whether the snapshot found any project.
func (m NOCModel) hasProjects() bool {
	return len(m.ms.Projects) > 0
}

// Compile-time assurance NOCModel satisfies the Bubble Tea interface.
var _ tea.Model = NOCModel{}
