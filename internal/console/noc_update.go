// Package console — noc_update.go: the tea.Update reducer + key routing + the
// (only) side effect, the read-only tmux jump.
package console

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/omriariav/amq-squad/v2/internal/noc"
)

// Update implements tea.Model. It folds immutable messages into new model state
// and never blocks; data feeds deliver snapshots / ticks / watch events.
//
// POINTER receiver: the program is driven as *NOCModel (tea.NewProgram(&m)), so
// every key handler's cursor / collapse / filter mutation lands on the SAME
// model the Bubble Tea event loop re-binds and renders on the next frame
// (tea.go: `model, cmd = model.Update(msg)` then `model.View()`). The movement
// helpers (moveCursor / clampCursor / preserveSelection / rememberSelection) are
// pointer-receiver; a VALUE Update would mutate a throwaway copy of the model
// and the live surface would look frozen (arrows / j / k dead), so Update / Init
// / View are all pointer-receiver to keep one consistent model.
func (m *NOCModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.ready = true
		return m, nil

	case nocTickMsg:
		return m, tea.Batch(nocRebuildCmd(m.rebuild), nocTickCmd(m.rebuild.refreshCadence()))

	case nocRediscoverMsg:
		// Re-discovery is the same Collect call (it re-walks the roots), so a
		// fresh rebuild already surfaces new/removed projects. Reschedule.
		return m, tea.Batch(nocRebuildCmd(m.rebuild), nocRediscoverTickCmd())

	case nocWatchMsg:
		return m, nocRebuildCmd(m.rebuild)

	case nocSnapshotMsg:
		// Detect needs-you transitions (0 → >0 per session) against the PRIOR
		// snapshot BEFORE replacing m.ms, then ring the bell + set the banner once
		// (read-only; suppressed when muted). This is the only place alerts fire so
		// they key off real snapshot deltas, not every keypress.
		alerts := m.detectNeedsYouTransitions(msg.ms)
		m.ms = msg.ms
		m.ready = true
		m.fireNeedsYouAlerts(alerts)
		m.refreshGuidance()
		m.clampCursor()
		m.preserveSelection()
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

// refreshGuidance sets the no-projects guidance state when the roots are empty.
func (m *NOCModel) refreshGuidance() {
	if m.hasProjects() {
		m.guidance = ""
		return
	}
	m.guidance = nocNoProjectsGuidance(m.rebuild.Roots)
}

// handleKey routes a key press. The keymap is NON-OVERLOADED and READ-ONLY: the
// only side effect is the tmux jump.
//
// Keymap:
//
//	↑/↓ or j/k     move selection
//	→/l or enter   expand a collapsed parent / drill in; on a RUNNING agent,
//	               enter JUMPS (tmux switch). A dedicated 'J' also jumps.
//	←              collapse the current node (or ascend to its parent)
//	h              toggle hiding stopped/archived (stale) squads
//	/              filter (needs-you/at-risk/blocked/agent:/model:/project:/session:)
//	t              toggle the timeline in the detail pane
//	f              toggle the inter-agent flow graph in the detail pane
//	g              refresh now
//	esc            clear filter / collapse / back
//	q              quit
//	?              help
func (m *NOCModel) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Control overlays take the key first so a mutating action is always two-step
	// and self-contained: while the confirm overlay is open ONLY y/esc/other are
	// meaningful (handleConfirmKey gates the single seam call); while the body
	// editor is open keys feed the buffer. Both are checked before nav so a
	// control flow never leaks a keystroke into the read-only keymap.
	if m.pending != nil {
		return m.handleConfirmKey(msg.String())
	}
	if m.input != nil {
		return m.handleInputKey(msg)
	}
	// The command palette (PR18) is a read-only overlay; like the control overlays
	// it takes the key first so typing/selection never leaks into the nav keymap.
	// Its only side effect is the existing gated tmux jump/focus.
	if m.palette != nil {
		return m.handlePaletteKey(msg)
	}
	if m.filterEditing {
		return m.handleFilterKey(msg)
	}
	if m.showHelp {
		// Any key dismisses help except quit.
		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		default:
			m.showHelp = false
			return m, nil
		}
	}

	m.jumpNote = ""
	m.actNote = ""
	// The needs-you alert banner is acknowledged by any keypress (it persists
	// across silent 2s refreshes but clears once the operator acts), mirroring
	// jumpNote/actNote. It re-appears on the next 0→N transition.
	m.alertBanner = ""

	// Control keys are ADDITIVE and checked before the read-only keymap: a key
	// the control layer owns (a/x/m/b/S/R/o) opens a preview/confirm flow (or,
	// for 'o', a read-only focus) and is consumed here; any other key falls
	// through to the unchanged nav/peek/filter/jump keymap below.
	if m.controlEnabled() {
		if cmd, handled := m.handleControlKey(msg.String()); handled {
			return m, cmd
		}
	}

	switch msg.String() {
	case "q", "ctrl+c":
		return m, tea.Quit
	case "?":
		m.showHelp = true
		return m, nil
	case "p", "ctrl+p":
		// Open the command palette: fuzzy-jump to any agent / team across all
		// discovered projects in ~2 keystrokes. Read-only (gated tmux jump/focus).
		return m, m.openPalette()
	case "A":
		// Toggle needs-you alerts (bell + banner). Mirrors the --no-bell flag.
		m.alertsMuted = !m.alertsMuted
		if m.alertsMuted {
			m.actNote = "alerts muted (A to unmute)"
			m.alertBanner = ""
		} else {
			m.actNote = "alerts on (A to mute)"
		}
		return m, nil
	case "t":
		m.showTimeline = !m.showTimeline
		return m, nil
	case "f":
		// Toggle the inter-agent FLOW GRAPH in the detail pane (2.3). Read-only:
		// it renders the snapshot's already-derived edges (who-messages-whom) with
		// volume + blocked/awaiting markers; no new computation, no side effects.
		// Independent of the timeline toggle ('t') — both sub-panels may be open.
		m.showFlow = !m.showFlow
		return m, nil
	case "h":
		// Toggle hiding stopped/archived (stale) squads so the operator can focus
		// on what is alive. (Collapse is left/←/esc; 'h' is no longer overloaded
		// onto collapse — see footer/help.) Re-anchor the selection afterward.
		m.hideStale = !m.hideStale
		m.clampCursor()
		m.preserveSelection()
		return m, nil
	case "g":
		return m, nocRebuildCmd(m.rebuild)
	case "/":
		m.filterEditing = true
		return m, nil
	case "up", "k":
		m.moveCursor(-1)
		return m, nil
	case "down", "j":
		m.moveCursor(1)
		return m, nil
	case "right", "l":
		return m.expandOrDrill()
	case "enter":
		return m.enter()
	case "J":
		return m.jump()
	case "left":
		return m.collapseOrAscend()
	case "esc":
		if m.filter != "" {
			m.filter = ""
			m.clampCursor()
			m.preserveSelection()
			return m, nil
		}
		return m.collapseOrAscend()
	}
	return m, nil
}

// handleFilterKey edits the filter string while the editor is open.
func (m *NOCModel) handleFilterKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "enter":
		m.filterEditing = false
		m.clampCursor()
		m.preserveSelection()
		return m, nil
	case "esc":
		m.filterEditing = false
		m.filter = ""
		m.clampCursor()
		m.preserveSelection()
		return m, nil
	case "backspace":
		if len(m.filter) > 0 {
			m.filter = m.filter[:len(m.filter)-1]
		}
		m.clampCursor()
		return m, nil
	default:
		if len(msg.String()) == 1 {
			m.filter += msg.String()
			m.clampCursor()
		}
		return m, nil
	}
}

// moveCursor moves selection by delta and remembers the new id.
func (m *NOCModel) moveCursor(delta int) {
	ns := m.nodes()
	if len(ns) == 0 {
		return
	}
	m.cursor += delta
	if m.cursor < 0 {
		m.cursor = 0
	}
	if m.cursor >= len(ns) {
		m.cursor = len(ns) - 1
	}
	m.rememberSelection()
}

// expandOrDrill expands a collapsed parent or, when already expanded, moves into
// its first child. On a running agent it does nothing (use enter/J to jump).
func (m *NOCModel) expandOrDrill() (tea.Model, tea.Cmd) {
	n, ok := m.selectedNode()
	if !ok {
		return m, nil
	}
	if n.hasKids {
		if m.tree.isCollapsed(n.id) {
			m.tree.setCollapsed(n.id, false)
			m.preserveSelection()
			return m, nil
		}
		// Already expanded: drill to the first child (the next node deeper).
		m.moveCursor(1)
		return m, nil
	}
	return m, nil
}

// enter JUMPS only on a RUNNING-AGENT row; on every other row it DRILLS/EXPANDS
// and never teleports into tmux (the jump guard). A STOPPED agent row leaves a
// note explaining there is nothing live to jump to, rather than silently doing
// nothing.
//
//   - running agent (nodeAgent && canJump): jump (the only tmux side effect).
//   - stopped agent (nodeAgent && !canJump): a note, no jump.
//   - project / session / root: expand or drill — never a jump.
func (m *NOCModel) enter() (tea.Model, tea.Cmd) {
	n, ok := m.selectedNode()
	if !ok {
		return m, nil
	}
	if n.kind == nodeAgent {
		if n.canJump {
			return m.jump()
		}
		m.jumpNote = "agent not running — nothing to jump to (enter jumps only on a running agent)"
		return m, nil
	}
	return m.expandOrDrill()
}

// collapseOrAscend collapses an expanded parent, or moves selection to the
// parent node when the current node is a leaf / already collapsed.
func (m *NOCModel) collapseOrAscend() (tea.Model, tea.Cmd) {
	n, ok := m.selectedNode()
	if !ok {
		return m, nil
	}
	if n.hasKids && !m.tree.isCollapsed(n.id) {
		m.tree.setCollapsed(n.id, true)
		m.rememberSelection()
		m.clampCursor()
		return m, nil
	}
	// Ascend: walk up to the nearest shallower node before the cursor.
	ns := m.nodes()
	for i := m.cursor - 1; i >= 0; i-- {
		if ns[i].depth < n.depth {
			m.cursor = i
			m.rememberSelection()
			return m, nil
		}
	}
	return m, nil
}

// jump performs the READ-ONLY tmux switch to the selected running agent. It
// resolves the live pane (rotation-proof via cwd+engine, PID-tree bonus), then
// calls the injected switcher. If no pane resolves, or the switch reports a
// not-in-tmux condition, it surfaces SuggestJump text rather than erroring out.
func (m *NOCModel) jump() (tea.Model, tea.Cmd) {
	n, ok := m.selectedNode()
	if !ok || n.kind != nodeAgent {
		return m, nil
	}
	if !n.canJump {
		m.jumpNote = "agent is not running — cannot jump"
		return m, nil
	}

	panes, err := m.panes()
	if err != nil {
		m.jumpNote = "tmux not available: " + err.Error()
		return m, nil
	}
	target, resolved := noc.ResolveTmuxTargetForSession(n.agent, n.session.Name, n.project.Dir, panes, m.pidTree)
	if !resolved {
		m.jumpNote = "no live tmux pane found for " + n.agent.Handle + " (resume it, or attach manually)"
		return m, nil
	}
	if err := m.switchTo(target); err != nil {
		if nit, isNIT := err.(*noc.NotInTmuxError); isNIT {
			m.jumpNote = "not inside tmux — run: " + nit.Command
			return m, nil
		}
		m.jumpNote = "jump: " + err.Error() + " (try: " + noc.SuggestJump(target) + ")"
		return m, nil
	}
	// A successful switch-client detaches our view to the agent's pane; leave a
	// note so a returning operator sees what happened.
	m.jumpNote = "jumped to " + noc.SuggestJump(target)
	return m, nil
}
