// Package console — noc_view.go: the beautified, read-only NOC render.
//
// Layout:
//  1. HEADER "pulse": a brand rule + a single rollup line
//     "<N> squads · <n> running · <n> needs you · <n> at-risk · <n> blocked · <clock>".
//     The needs-you segment is bold/hot when >0, all-dim (calm) when 0.
//  2. MAIN two-pane: LEFT a collapsible attention-first tree (root → project →
//     session → agent); RIGHT a detail pane for the selected node.
//  3. FOOTER: keybindings.
//
// Color is the last layer: every state carries a TEXT label; glyph + color are
// secondary and fall away on NO_COLOR / dumb terminals.
package console

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/omriariav/amq-squad/v2/internal/state"
)

// View implements tea.Model.
func (m NOCModel) View() string {
	if !m.ready {
		return "loading…"
	}
	if m.showHelp {
		return m.helpView()
	}
	return m.staticView()
}

// staticView is the full board — shared by the live View and the --once path so
// they render identically.
func (m NOCModel) staticView() string {
	var b strings.Builder
	b.WriteString(m.headerView())
	b.WriteString("\n")
	if m.guidance != "" {
		b.WriteString(m.guidance)
		b.WriteString("\n")
		b.WriteString(m.footerView())
		return b.String()
	}
	b.WriteString(m.mainView())
	b.WriteString("\n")
	b.WriteString(m.footerView())
	return b.String()
}

// headerView renders the brand rule + the rollup pulse line.
func (m NOCModel) headerView() string {
	var b strings.Builder

	brand := m.th.paint(m.th.brand, "amq-squad NOC")
	sub := m.th.paint(m.th.dim, "command center")
	b.WriteString(brand + "  " + sub + "\n")
	b.WriteString(m.th.paint(m.th.rule, m.rule()) + "\n")
	b.WriteString(m.pulseLine())
	return b.String()
}

// pulseLine is the single rollup headline. needs-you is hot when >0, calm dim
// when 0; the rest is calm chrome.
func (m NOCModel) pulseLine() string {
	r := m.ms.Rollup
	squads := len(m.ms.Projects)
	running := m.runningAgentCount()

	dim := func(s string) string { return m.th.paint(m.th.dim, s) }
	sep := dim(" " + m.dot() + " ")

	segs := []string{
		dim(nocCount(squads, "squad", "squads")),
		dim(strconv.Itoa(running) + " running"),
	}

	// needs-you: the single eye-grab.
	nyText := strconv.Itoa(r.NeedsYou) + " needs you"
	if r.NeedsYou > 0 {
		segs = append(segs, m.th.paint(m.th.needsYou, nocStateGlyph(nocNeedsYou, m.colorMode)+" "+nyText))
	} else {
		segs = append(segs, dim(nyText))
	}

	if r.AtRisk > 0 {
		segs = append(segs, m.th.paint(m.th.atRisk, strconv.Itoa(r.AtRisk)+" at-risk"))
	} else {
		segs = append(segs, dim("0 at-risk"))
	}
	if r.Blocked > 0 {
		segs = append(segs, m.th.paint(m.th.blocked, strconv.Itoa(r.Blocked)+" blocked"))
	} else {
		segs = append(segs, dim("0 blocked"))
	}

	segs = append(segs, dim(m.clock()))
	return strings.Join(segs, sep)
}

// clock formats the observation time.
func (m NOCModel) clock() string {
	if m.ms.ObservedAt.IsZero() {
		return ""
	}
	return m.ms.ObservedAt.Format("15:04:05")
}

// runningAgentCount counts live agents across all projects.
func (m NOCModel) runningAgentCount() int {
	n := 0
	for _, ps := range m.ms.Projects {
		for _, sess := range ps.Snap.Sessions {
			for _, ag := range sess.Agents {
				if ag.Liveness == state.LivenessAlive || ag.Liveness == state.LivenessDeadMailboxLive {
					n++
				}
			}
		}
	}
	return n
}

// rule returns the header rule string sized to the width.
func (m NOCModel) rule() string {
	w := m.width
	if w <= 0 {
		w = 78
	}
	ch := "─"
	if m.colorMode == ColorAscii {
		ch = "-"
	}
	return strings.Repeat(ch, w)
}

// mainView lays out the LEFT tree and the RIGHT detail pane side by side. When
// the terminal is narrow (or width unknown, e.g. --once) it stacks the tree
// above the detail.
func (m NOCModel) mainView() string {
	left := m.treeView()
	right := m.detailView()

	leftW := m.leftWidth()
	if leftW <= 0 || m.width <= 0 {
		// Stacked fallback (CI / --once / narrow): tree, then detail.
		var b strings.Builder
		b.WriteString(left)
		if strings.TrimSpace(right) != "" {
			b.WriteString("\n")
			b.WriteString(m.th.paint(m.th.dim, m.thinRule()))
			b.WriteString("\n")
			b.WriteString(right)
		}
		return b.String()
	}

	leftLines := strings.Split(left, "\n")
	rightLines := strings.Split(right, "\n")
	n := len(leftLines)
	if len(rightLines) > n {
		n = len(rightLines)
	}
	gutter := m.th.paint(m.th.dim, " │ ")
	if m.colorMode == ColorAscii {
		gutter = " | "
	}
	var b strings.Builder
	for i := 0; i < n; i++ {
		l := ""
		if i < len(leftLines) {
			l = leftLines[i]
		}
		rr := ""
		if i < len(rightLines) {
			rr = rightLines[i]
		}
		b.WriteString(padVisible(l, leftW))
		b.WriteString(gutter)
		b.WriteString(rr)
		if i < n-1 {
			b.WriteString("\n")
		}
	}
	return b.String()
}

// leftWidth is the tree pane width (about 55% of the terminal, bounded).
func (m NOCModel) leftWidth() int {
	if m.width <= 0 {
		return 0
	}
	w := m.width*55/100 - 2
	if w < 24 {
		w = 24
	}
	if w > m.width-20 {
		w = m.width - 20
	}
	if w < 0 {
		return 0
	}
	return w
}

func (m NOCModel) thinRule() string {
	w := m.width
	if w <= 0 {
		w = 60
	}
	ch := "─"
	if m.colorMode == ColorAscii {
		ch = "-"
	}
	return strings.Repeat(ch, w)
}

// treeView renders the flattened, attention-first tree with the amber selection
// bar on the cursor row.
func (m NOCModel) treeView() string {
	ns := m.nodes()
	if len(ns) == 0 {
		return m.th.paint(m.th.dim, "(no matching nodes)")
	}
	var b strings.Builder
	for i, n := range ns {
		line := m.renderNode(n, i == m.cursor)
		b.WriteString(line)
		if i < len(ns)-1 {
			b.WriteString("\n")
		}
	}
	return b.String()
}

// renderNode renders one tree row: selection marker, indent + tree glyph, state
// glyph + TEXT label, label, triage tally (parents) / jump affordance (running
// agents), recent action (dim), and age (dim).
func (m NOCModel) renderNode(n nocNode, selected bool) string {
	var b strings.Builder

	// Selection marker.
	if selected {
		b.WriteString(m.th.paint(m.th.selBar, nocGlyphSelect.glyph(m.colorMode)+" "))
	} else {
		b.WriteString("  ")
	}

	// Indent by depth.
	b.WriteString(strings.Repeat("  ", n.depth))

	// Expand/collapse caret for parents.
	if n.hasKids {
		caret := nocGlyphCollapsed
		if n.expanded {
			caret = nocGlyphExpanded
		}
		b.WriteString(m.th.paint(m.th.dim, caret.glyph(m.colorMode)) + " ")
	} else {
		b.WriteString("  ")
	}

	// State glyph + TEXT label (text always present).
	glyph := nocStateGlyph(n.state, m.colorMode)
	label := nocStateText(n.state)
	style := m.th.nocStateStyle(n.state)
	b.WriteString(m.th.paint(style, glyph+" "+label))
	b.WriteString(" ")

	// Node label (project / session / handle / root).
	nameStyle := m.th.brand
	if n.state == nocNeedsYou {
		nameStyle = m.th.needsYou
	} else if n.kind == nodeAgent {
		nameStyle = m.th.running
		if n.state != nocRunning {
			nameStyle = m.th.dim
		}
	}
	b.WriteString(m.th.paint(nameStyle, n.label))

	// Triage tally for parents.
	if n.kind == nodeProject || n.kind == nodeSession || n.kind == nodeRoot {
		tally := m.tallyText(n.rollup)
		if tally != "" {
			b.WriteString(" " + tally)
		}
	}

	// Jump affordance for running agents.
	if n.canJump {
		b.WriteString(" " + m.th.paint(m.th.running, nocGlyphJump.glyph(m.colorMode)))
	}

	// Recent action / title (dim).
	if n.recent != "" {
		b.WriteString(m.th.paint(m.th.dim, "  "+truncate(n.recent, 40)))
	}

	return b.String()
}

// tallyText is a compact per-parent triage tally, e.g. "(2 needs-you, 1 at-risk)".
func (m NOCModel) tallyText(r state.TriageRollup) string {
	var parts []string
	if r.NeedsYou > 0 {
		parts = append(parts, m.th.paint(m.th.needsYou, strconv.Itoa(r.NeedsYou)+" needs-you"))
	}
	if r.Blocked > 0 {
		parts = append(parts, m.th.paint(m.th.blocked, strconv.Itoa(r.Blocked)+" blocked"))
	}
	if r.AtRisk > 0 {
		parts = append(parts, m.th.paint(m.th.atRisk, strconv.Itoa(r.AtRisk)+" at-risk"))
	}
	if len(parts) == 0 {
		return ""
	}
	open := m.th.paint(m.th.dim, "(")
	closep := m.th.paint(m.th.dim, ")")
	sep := m.th.paint(m.th.dim, ", ")
	return open + strings.Join(parts, sep) + closep
}

// detailView renders the right pane for the selected node.
func (m NOCModel) detailView() string {
	n, ok := m.selectedNode()
	if !ok {
		return ""
	}
	switch n.kind {
	case nodeAgent:
		return m.agentDetail(n)
	case nodeSession:
		return m.sessionDetail(n)
	case nodeProject:
		return m.projectDetail(n)
	default:
		return m.rootDetail(n)
	}
}

// projectDetail summarizes a project: its triage tally, sessions, and (if any)
// its warning.
func (m NOCModel) projectDetail(n nocNode) string {
	var b strings.Builder
	b.WriteString(m.th.paint(m.th.brand, "PROJECT  ") + m.th.paint(m.th.brand, n.label) + "\n")
	b.WriteString(m.th.paint(m.th.dim, n.project.Dir) + "\n")
	if n.warning != "" {
		b.WriteString(m.th.paint(m.th.atRisk, "warning: "+firstLine(n.warning)) + "\n")
		return b.String()
	}
	b.WriteString(m.detailRule() + "\n")
	b.WriteString(m.th.paint(m.th.dim, "sessions") + "\n")
	for _, sess := range sortedSessions(n.project.Snap.Sessions) {
		ss := sessionRollupState(sess)
		b.WriteString("  " + m.th.paint(m.th.nocStateStyle(ss), nocStateGlyph(ss, m.colorMode)+" "+nocStateText(ss)))
		b.WriteString(" " + m.th.paint(m.th.brand, sess.Name))
		b.WriteString(m.th.paint(m.th.dim, fmt.Sprintf("  (%d agents)", len(sess.Agents))))
		b.WriteString("\n")
	}
	if m.jumpNote != "" {
		b.WriteString(m.detailRule() + "\n" + m.th.paint(m.th.dim, m.jumpNote) + "\n")
	}
	return b.String()
}

// sessionDetail shows the unresolved threads (the collapsed-thread bus), an
// agents table, and the recent actions timeline.
func (m NOCModel) sessionDetail(n nocNode) string {
	var b strings.Builder
	b.WriteString(m.th.paint(m.th.brand, "SESSION  ") + m.th.paint(m.th.brand, n.label) + "\n")
	b.WriteString(m.th.paint(m.th.dim, n.project.Project) + "\n")
	b.WriteString(m.detailRule() + "\n")

	// Open threads — the collapsed-thread bus, urgency-sorted.
	threads := sortThreads(n.session, Filter{})
	b.WriteString(m.th.paint(m.th.dim, "open threads") + "\n")
	if len(threads) == 0 {
		b.WriteString(m.th.paint(m.th.dim, "  (none open)") + "\n")
	}
	for _, th := range threads {
		st := triageState(th.Triage)
		b.WriteString("  " + m.th.paint(m.th.nocStateStyle(st), nocStateGlyph(st, m.colorMode)))
		b.WriteString(" " + truncate(threadTitle(th), 36))
		if age := nocThreadAge(th); age != "" {
			b.WriteString(m.th.paint(m.th.dim, "  "+age))
		}
		b.WriteString("\n")
	}

	// Agents table.
	b.WriteString(m.detailRule() + "\n")
	b.WriteString(m.th.paint(m.th.dim, "agents") + "\n")
	for _, ag := range sortedAgents(n.session.Agents) {
		st := agentState(ag)
		b.WriteString("  " + m.th.paint(m.th.nocStateStyle(st), nocStateGlyph(st, m.colorMode)+" "+nocStateText(st)))
		b.WriteString(" " + m.th.paint(m.th.brand, agentLabel(ag)))
		if ag.Engine != "" {
			b.WriteString(m.th.paint(m.th.dim, "  "+ag.Engine))
		}
		b.WriteString("\n")
	}

	// Recent actions / timeline.
	if m.showTimeline || len(threads) == 0 {
		b.WriteString(m.detailRule() + "\n")
		b.WriteString(m.th.paint(m.th.dim, "recent") + "\n")
		shown := 0
		for _, ev := range n.session.Coordination.Timeline {
			b.WriteString(m.th.paint(m.th.dim, "  "+truncate(ev.Summary, 44)) + "\n")
			shown++
			if shown >= 5 {
				break
			}
		}
		if shown == 0 {
			b.WriteString(m.th.paint(m.th.dim, "  (no recent events)") + "\n")
		}
	}
	return b.String()
}

// agentDetail shows the selected agent's recent threads (those it waits on) and
// a jump hint.
func (m NOCModel) agentDetail(n nocNode) string {
	var b strings.Builder
	st := agentState(n.agent)
	b.WriteString(m.th.paint(m.th.brand, "AGENT  ") + m.th.paint(m.th.brand, agentLabel(n.agent)))
	b.WriteString("  " + m.th.paint(m.th.nocStateStyle(st), nocStateGlyph(st, m.colorMode)+" "+nocStateText(st)) + "\n")

	meta := []string{}
	if n.agent.Role != "" {
		meta = append(meta, "role "+n.agent.Role)
	}
	if n.agent.Engine != "" {
		meta = append(meta, "engine "+n.agent.Engine)
	}
	meta = append(meta, "session "+n.session.Name)
	b.WriteString(m.th.paint(m.th.dim, strings.Join(meta, " "+m.dot()+" ")) + "\n")
	b.WriteString(m.detailRule() + "\n")

	// Recent threads relevant to this agent: those it participates in.
	b.WriteString(m.th.paint(m.th.dim, "recent threads") + "\n")
	shown := 0
	for _, th := range sortThreads(n.session, Filter{}) {
		if !threadHasParticipant(th, n.agent.Handle) {
			continue
		}
		stt := triageState(th.Triage)
		b.WriteString("  " + m.th.paint(m.th.nocStateStyle(stt), nocStateGlyph(stt, m.colorMode)))
		b.WriteString(" " + truncate(threadTitle(th), 38))
		b.WriteString("\n")
		shown++
		if shown >= 6 {
			break
		}
	}
	if shown == 0 {
		b.WriteString(m.th.paint(m.th.dim, "  (no open threads)") + "\n")
	}

	// Jump hint.
	b.WriteString(m.detailRule() + "\n")
	if n.canJump {
		hint := nocGlyphJump.glyph(m.colorMode) + "  enter / J to jump to this agent's tmux window"
		b.WriteString(m.th.paint(m.th.running, hint) + "\n")
	} else {
		b.WriteString(m.th.paint(m.th.dim, "agent not running — nothing to jump to") + "\n")
	}
	if m.jumpNote != "" {
		b.WriteString(m.th.paint(m.th.dim, m.jumpNote) + "\n")
	}
	return b.String()
}

// rootDetail summarizes a root header node.
func (m NOCModel) rootDetail(n nocNode) string {
	var b strings.Builder
	b.WriteString(m.th.paint(m.th.brand, "ROOT  ") + m.th.paint(m.th.brand, n.label) + "\n")
	b.WriteString(m.th.paint(m.th.dim, "expand to see this root's projects") + "\n")
	return b.String()
}

// dot returns the inline separator dot, degraded to ascii on a dumb terminal.
func (m NOCModel) dot() string {
	if m.colorMode == ColorAscii {
		return "-"
	}
	return "·"
}

// detailRule is a short divider inside the detail pane.
func (m NOCModel) detailRule() string {
	return m.th.paint(m.th.dim, strings.Repeat(m.dot(), 28))
}

// footerView renders the keybindings (or the filter editor when active).
func (m NOCModel) footerView() string {
	if m.filterEditing {
		cursor := "▏"
		if m.colorMode == ColorAscii {
			cursor = "_"
		}
		prompt := "/filter: " + m.filter + cursor
		return m.th.paint(m.th.rule, m.thinRule()) + "\n" + m.th.paint(m.th.atRisk, prompt)
	}
	keys := "↑↓/jk move · →/l/⏎ expand/drill · ←/h collapse · ⏎/J jump · / filter · t timeline · g refresh · esc back · ? help · q quit"
	if m.colorMode == ColorAscii {
		keys = "up/down move | right/l/enter expand | left/h collapse | enter/J jump | / filter | t timeline | g refresh | esc back | ? help | q quit"
	}
	var b strings.Builder
	b.WriteString(m.th.paint(m.th.rule, m.thinRule()) + "\n")
	if m.filter != "" {
		b.WriteString(m.th.paint(m.th.atRisk, "filter: "+m.filter) + m.th.paint(m.th.dim, "  (esc clears)") + "\n")
	}
	b.WriteString(m.th.paint(m.th.dim, keys))
	return b.String()
}

// helpView is the full help overlay.
func (m NOCModel) helpView() string {
	var b strings.Builder
	b.WriteString(m.th.paint(m.th.brand, "amq-squad NOC — help") + "\n")
	b.WriteString(m.th.paint(m.th.rule, m.rule()) + "\n\n")
	lines := []string{
		"NAVIGATION",
		"  ↑ / k, ↓ / j      move selection",
		"  → / l             expand a collapsed node, or drill into it",
		"  ← / h             collapse the node, or ascend to its parent",
		"  enter             expand/drill; on a RUNNING agent: JUMP",
		"  J                 jump to the selected running agent's tmux window",
		"",
		"VIEW",
		"  /                 filter (needs-you / at-risk / blocked / agent: / model: / project: / session:)",
		"  t                 toggle the timeline in the detail pane",
		"  g                 refresh now",
		"  esc               clear filter / collapse / back",
		"  ?                 toggle this help",
		"  q                 quit",
		"",
		"READ-ONLY: the only side effect is the tmux jump (it moves your view,",
		"not squad state). No key can stop / start / message / delete an agent.",
	}
	for _, l := range lines {
		b.WriteString(m.th.paint(m.th.dim, l) + "\n")
	}
	return b.String()
}

// --- small string helpers (visible-width aware enough for our ASCII labels) ---

func truncate(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	if max <= 1 {
		return string(r[:max])
	}
	return string(r[:max-1]) + "…"
}

// threadTitle is a thread's display title: its subject, or a short id fallback.
func threadTitle(th state.ThreadSummary) string {
	if s := strings.TrimSpace(th.Subject); s != "" {
		return s
	}
	return shortID(th.ID)
}

// nocThreadAge renders a thread's pre-computed freshness age (the snapshot ages
// it against the build clock, so this is deterministic and needs no live clock).
func nocThreadAge(th state.ThreadSummary) string {
	if th.Freshness.Age > 0 {
		return ageLabel(th.Freshness.Age)
	}
	return ""
}

// threadHasParticipant reports whether handle is among a thread's participants.
func threadHasParticipant(th state.ThreadSummary, handle string) bool {
	for _, p := range th.Participants {
		if strings.EqualFold(p, handle) {
			return true
		}
	}
	return false
}

// nocCount renders a counted noun, e.g. "1 squad" / "3 squads".
func nocCount(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return strconv.Itoa(n) + " " + many
}

// padVisible pads s to width w accounting for ANSI escape sequences so the
// two-pane gutter aligns even when the left cell contains color codes.
func padVisible(s string, w int) string {
	vis := visibleWidth(s)
	if vis >= w {
		return s
	}
	return s + strings.Repeat(" ", w-vis)
}

// visibleWidth returns the rune count of s ignoring ANSI escape sequences.
func visibleWidth(s string) int {
	n := 0
	inEsc := false
	for _, r := range s {
		if inEsc {
			if r == 'm' {
				inEsc = false
			}
			continue
		}
		if r == '\x1b' {
			inEsc = true
			continue
		}
		n++
	}
	return n
}

// nocNoProjectsGuidance is the clear, never-a-crash empty state.
func nocNoProjectsGuidance(roots []string) string {
	var b strings.Builder
	b.WriteString("No amq-squad projects found under:\n")
	if len(roots) == 0 {
		b.WriteString("  (current directory)\n")
	}
	for _, r := range roots {
		b.WriteString("  " + displayRoot(r) + "\n")
	}
	b.WriteString("\nA project is any directory containing a .agent-mail/ folder.\n")
	b.WriteString("Try a different --root, increase --depth, or run 'amq-squad up' to start a team.\n")
	return b.String()
}
