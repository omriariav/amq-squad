// Package console — noc_palette.go: the COMMAND PALETTE (2.3 awareness+scale).
//
// The palette is a fuzzy "jump to any agent or team in ~2 keystrokes" overlay
// over EVERY agent (and team) across every discovered project/session in the
// MultiSnapshot. It is strictly READ-ONLY: selecting a running agent performs the
// SAME gated tmux jump the tree's enter/J does (the name-first
// ResolveTmuxTargetForSession + the switchTo seam); selecting a stopped agent or
// a team row focuses an existing tmux window if present (the focusTeam path) or
// sets a suggest-up note. It never mutates squad state.
//
// Open with 'p'. Type to fuzzy-filter (subsequence match, case-insensitive) over
// the "project/session/role" label. Up/down (or ctrl+n / ctrl+p) move the
// selection within results; enter SELECTS; esc closes.
package console

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/omriariav/amq-squad/v2/internal/noc"
	"github.com/omriariav/amq-squad/v2/internal/state"
)

// paletteKind distinguishes an agent target from a team (session) target so the
// selection path knows whether to jump (agent) or focus (team).
type paletteKind int

const (
	palAgent paletteKind = iota
	palTeam
)

// paletteItem is one fuzzy-jump candidate: an agent or a team across all
// projects/sessions. It carries everything the read-only jump/focus needs so the
// selection never has to re-walk the snapshot (which may have moved).
type paletteItem struct {
	kind    paletteKind
	label   string // "project/session/role" — the fuzzy-match + display string
	running bool   // an agent whose liveness is alive, or a team with ≥1 alive agent
	// Carried context for the jump/focus.
	project    string // project label (for the suggest-up note)
	projectDir string
	session    string
	agent      state.Agent // valid for palAgent
}

// paletteState is the open command palette: the typed query + the live-filtered
// results + the selection within them. nil on m means the palette is closed.
type paletteState struct {
	query  string
	items  []paletteItem // ALL candidates, snapshot at open time (stable while open)
	cursor int           // selection index INTO the filtered results
}

// filtered returns the items whose label fuzzy-matches the query (subsequence,
// case-insensitive). An empty query matches everything (the full list).
func (p *paletteState) filtered() []paletteItem {
	if strings.TrimSpace(p.query) == "" {
		return p.items
	}
	q := strings.ToLower(p.query)
	out := make([]paletteItem, 0, len(p.items))
	for _, it := range p.items {
		if fuzzySubsequence(strings.ToLower(it.label), q) {
			out = append(out, it)
		}
	}
	return out
}

// fuzzySubsequence reports whether every rune of needle appears in haystack in
// order (a classic fuzzy/subsequence match). Both are expected lowercased by the
// caller. An empty needle matches anything.
func fuzzySubsequence(haystack, needle string) bool {
	if needle == "" {
		return true
	}
	ni := 0
	nr := []rune(needle)
	for _, hc := range haystack {
		if hc == nr[ni] {
			ni++
			if ni == len(nr) {
				return true
			}
		}
	}
	return false
}

// buildPaletteItems flattens the snapshot into the jump candidates: every team
// (session) and every agent across every project. Teams come first within a
// project/session group so a "jump to the whole team" row is reachable, then its
// agents. Ordering is attention-first by project (snapshot order), then sessions
// + agents sorted the SAME way the tree sorts them, so the palette mirrors the
// board. Warning projects contribute nothing (no jumpable target).
func buildPaletteItems(ms noc.MultiSnapshot) []paletteItem {
	var items []paletteItem
	for _, ps := range ms.Projects {
		if ps.Warning != "" {
			continue
		}
		for _, sess := range sortedSessions(ps.Snap.Sessions) {
			sessLabel := sessionLabel(sess)
			// The team row: "project/session" with a "team" role marker so it is
			// distinct from any single agent and still fuzzy-matchable.
			teamRunning := sessionHasAliveAgent(sess)
			items = append(items, paletteItem{
				kind:       palTeam,
				label:      ps.Project + "/" + sessLabel + "/team",
				running:    teamRunning,
				project:    ps.Project,
				projectDir: ps.Dir,
				session:    sess.Name,
			})
			for _, ag := range sortedAgents(sess.Agents) {
				role := strings.TrimSpace(ag.Role)
				if role == "" {
					role = agentLabel(ag)
				}
				items = append(items, paletteItem{
					kind:       palAgent,
					label:      ps.Project + "/" + sessLabel + "/" + role,
					running:    ag.Liveness == state.LivenessAlive,
					project:    ps.Project,
					projectDir: ps.Dir,
					session:    sess.Name,
					agent:      ag,
				})
			}
		}
	}
	return items
}

// sessionHasAliveAgent reports whether a session carries at least one alive agent
// (so a team row can show running vs stopped). It mirrors the tree's
// agent-liveness check (LivenessAlive) used for the jump affordance.
func sessionHasAliveAgent(sess state.Session) bool {
	for _, ag := range sess.Agents {
		if ag.Liveness == state.LivenessAlive {
			return true
		}
	}
	return false
}

// openPalette opens the command palette over the current snapshot. It snapshots
// the candidate list at open time so typing/selection are stable even if a
// refresh lands while the palette is open. It is read-only: opening mutates only
// the palette UI state.
func (m *NOCModel) openPalette() tea.Cmd {
	m.palette = &paletteState{
		items: buildPaletteItems(m.ms),
	}
	return nil
}

// handlePaletteKey routes a key while the palette is open. esc closes; up/down
// (and ctrl+p / ctrl+n) move the selection within the filtered results; enter
// SELECTS; backspace edits the query; any single rune appends to the query. The
// selection is the only place a side effect (the gated tmux switch) can happen,
// and only the read-only jump/focus — never a mutation.
func (m *NOCModel) handlePaletteKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	p := m.palette
	switch msg.String() {
	case "esc":
		m.palette = nil
		return m, nil
	case "up", "ctrl+p":
		p.moveCursor(-1)
		return m, nil
	case "down", "ctrl+n":
		p.moveCursor(1)
		return m, nil
	case "enter":
		return m.paletteSelect()
	case "backspace":
		if len(p.query) > 0 {
			p.query = p.query[:len(p.query)-1]
			p.cursor = 0
		}
		return m, nil
	default:
		if len(msg.String()) == 1 {
			p.query += msg.String()
			p.cursor = 0
		}
		return m, nil
	}
}

// moveCursor moves the selection within the CURRENT filtered result set, clamped
// to its bounds (so a narrowing query never strands the cursor out of range).
func (p *paletteState) moveCursor(delta int) {
	n := len(p.filtered())
	if n == 0 {
		p.cursor = 0
		return
	}
	p.cursor += delta
	if p.cursor < 0 {
		p.cursor = 0
	}
	if p.cursor >= n {
		p.cursor = n - 1
	}
}

// selected returns the palette item at the cursor within the filtered results.
func (p *paletteState) selected() (paletteItem, bool) {
	res := p.filtered()
	if p.cursor < 0 || p.cursor >= len(res) {
		return paletteItem{}, false
	}
	return res[p.cursor], true
}

// paletteSelect acts on the selected candidate. A RUNNING agent JUMPS (the same
// name-first ResolveTmuxTargetForSession + switchTo seam the tree uses). A
// stopped agent / a team row focuses an existing tmux window if present (the
// focusTeam path) else sets a suggest-up note. The palette closes on select. It
// is READ-ONLY: only the gated tmux switch can fire.
func (m *NOCModel) paletteSelect() (tea.Model, tea.Cmd) {
	it, ok := m.palette.selected()
	m.palette = nil
	if !ok {
		return m, nil
	}
	if it.kind == palAgent && it.running {
		m.jumpToPaletteAgent(it)
		return m, nil
	}
	// Stopped agent or a team: focus an existing window, else suggest up/resume.
	m.focusPaletteTarget(it)
	return m, nil
}

// jumpToPaletteAgent performs the read-only tmux jump to a running agent chosen
// in the palette, mirroring NOCModel.jump exactly: resolve name-first via
// ResolveTmuxTargetForSession, then call the switchTo seam, surfacing
// SuggestJump / not-in-tmux text rather than erroring.
func (m *NOCModel) jumpToPaletteAgent(it paletteItem) {
	panes, err := m.panes()
	if err != nil {
		m.jumpNote = "tmux not available: " + err.Error()
		return
	}
	target, resolved := noc.ResolveTmuxTargetForSession(it.agent, it.session, it.projectDir, panes, m.pidTree)
	if !resolved {
		m.jumpNote = "no live tmux pane found for " + it.agent.Handle + " (resume it, or attach manually)"
		return
	}
	if err := m.switchTo(target); err != nil {
		if nit, isNIT := err.(*noc.NotInTmuxError); isNIT {
			m.jumpNote = "not inside tmux — run: " + nit.Command
			return
		}
		m.jumpNote = "jump: " + err.Error() + " (try: " + noc.SuggestJump(target) + ")"
		return
	}
	m.jumpNote = "jumped to " + noc.SuggestJump(target)
}

// focusPaletteTarget focuses an EXISTING tmux window for a stopped-agent / team
// selection, mirroring NOCModel.focusTeam: resolveSquadWindow (read-only) then
// the switchTo seam, or a suggest-up note when nothing is running. It NEVER
// spawns.
func (m *NOCModel) focusPaletteTarget(it paletteItem) {
	panes, err := m.panes()
	if err != nil {
		m.jumpNote = "tmux not available: " + err.Error()
		return
	}
	target, found := resolveSquadWindow(it.session, it.projectDir, panes)
	if !found {
		hint := it.session
		if hint == "" {
			hint = it.project
		}
		m.jumpNote = "team not running — press R to resume or run amq-squad up " + hint
		return
	}
	if err := m.switchTo(target); err != nil {
		if nit, isNIT := err.(*noc.NotInTmuxError); isNIT {
			m.jumpNote = "not inside tmux — run: " + nit.Command
			return
		}
		m.jumpNote = "open: " + err.Error() + " (try: " + noc.SuggestJump(target) + ")"
		return
	}
	m.jumpNote = "focused " + noc.SuggestJump(target)
}

// paletteOverlayView renders the command palette: a query line + the live
// filtered list with the selection bar on the cursor row, each row labeled
// "project/session/role  ●running|○stopped". It is read-only chrome.
func (m NOCModel) paletteOverlayView() string {
	p := m.palette
	var b strings.Builder
	b.WriteString(m.th.paint(m.th.brand, "COMMAND PALETTE — jump to any agent / team"))
	b.WriteString("\n")
	b.WriteString(m.th.paint(m.th.rule, m.thinRule()))
	b.WriteString("\n")

	cursor := "▏"
	if m.colorMode == ColorAscii {
		cursor = "_"
	}
	b.WriteString(m.th.paint(m.th.atRisk, "jump: "+p.query+cursor))
	b.WriteString("\n")
	b.WriteString(m.th.paint(m.th.rule, m.thinRule()))
	b.WriteString("\n")

	res := p.filtered()
	if len(res) == 0 {
		b.WriteString(m.th.paint(m.th.dim, "  (no matching agents or teams)"))
		b.WriteString("\n")
	}
	// Cap the rendered rows so a huge fleet never overruns the frame; the cursor
	// row is always kept in view by windowing around it.
	const maxRows = 12
	start := 0
	if p.cursor >= maxRows {
		start = p.cursor - maxRows + 1
	}
	end := start + maxRows
	if end > len(res) {
		end = len(res)
	}
	for i := start; i < end; i++ {
		b.WriteString(m.paletteRow(res[i], i == p.cursor))
		b.WriteString("\n")
	}
	if len(res) > maxRows {
		b.WriteString(m.th.paint(m.th.dim, "  … "+itoaPalette(len(res))+" matches (type to narrow)"))
		b.WriteString("\n")
	}

	b.WriteString(m.th.paint(m.th.rule, m.thinRule()))
	b.WriteString("\n")
	hint := "type to filter · ↑↓ move · ⏎ jump/focus · esc close"
	if m.colorMode == ColorAscii {
		hint = "type to filter | up/down move | enter jump/focus | esc close"
	}
	b.WriteString(m.th.paint(m.th.dim, hint))
	return b.String()
}

// paletteRow renders one palette result row: a selection marker, the
// "project/session/role" label, and a running/stopped glyph+label.
func (m NOCModel) paletteRow(it paletteItem, selected bool) string {
	var b strings.Builder
	if selected {
		b.WriteString(m.th.paint(m.th.selBar, nocGlyphSelect.glyph(m.colorMode)+" "))
	} else {
		b.WriteString("  ")
	}
	nameStyle := m.th.brand
	if it.running {
		nameStyle = m.th.running
	} else {
		nameStyle = m.th.dim
	}
	b.WriteString(m.th.paint(nameStyle, padRight(it.label, 40)))
	b.WriteString("  ")
	if it.running {
		dot := "●"
		if m.colorMode == ColorAscii {
			dot = "[run]"
		}
		b.WriteString(m.th.paint(m.th.running, dot+" running"))
	} else {
		dot := "○"
		if m.colorMode == ColorAscii {
			dot = "[stop]"
		}
		b.WriteString(m.th.paint(m.th.dim, dot+" stopped"))
	}
	return b.String()
}

// itoaPalette is a tiny int→string for the palette match count (avoids importing
// strconv just for one call; noc_view.go already owns strconv-based helpers but
// this file stays lean).
func itoaPalette(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
