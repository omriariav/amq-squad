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
	"sort"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/omriariav/amq-squad/v2/internal/noc"
	"github.com/omriariav/amq-squad/v2/internal/state"
)

// View implements tea.Model. Pointer receiver to match Update / Init: *NOCModel
// is the type the program is driven as (tea.NewProgram(&m)).
func (m *NOCModel) View() string {
	if !m.ready {
		return "loading…"
	}
	if m.showHelp {
		return m.helpView()
	}
	return m.staticView()
}

// staticView is the static board for the live View and the --once path. Default
// --once leads with a NEEDS-ATTENTION section + PROJECT ROLLUPS (the digest, not
// the firehose); --tree/--all (fullTree) renders the full expandable tree so the
// existing full board is still one flag away.
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
	if m.fullTree {
		b.WriteString(m.mainView())
	} else {
		b.WriteString(m.rollupView())
	}
	b.WriteString("\n")
	b.WriteString(m.footerView())
	return b.String()
}

// nocNeedsYouItem is one needs-you thread plus the project/session it lives in,
// the unit the NEEDS YOU block lists. Carries the typed reason so the block can
// sort + label it (approve above goal-reached above generic).
type nocNeedsYouItem struct {
	project string
	session string
	thread  state.ThreadSummary
}

// collectNeedsYou gathers every needs-you thread across the in-view squads,
// sorted for the NEEDS YOU block: APPROVE first, then GOAL-REACHED, then
// generic; within a reason oldest-first (longest-waiting human ask leads), then
// by project/session/id for determinism. Returns nil when nothing needs the
// human — the caller renders the block ONLY when this is non-empty (never
// fabricate a NEEDS YOU section on a calm board).
func collectNeedsYou(projects []noc.ProjectSnapshot) []nocNeedsYouItem {
	var items []nocNeedsYouItem
	for _, ps := range projects {
		if ps.Warning != "" {
			continue
		}
		for _, sess := range ps.Snap.Sessions {
			for _, th := range sess.Coordination.NeedsYouThreads() {
				items = append(items, nocNeedsYouItem{
					project: ps.Project,
					session: sessionLabel(sess),
					thread:  th,
				})
			}
		}
	}
	sort.SliceStable(items, func(i, j int) bool {
		ri, rj := items[i].thread.AttnReason.Rank(), items[j].thread.AttnReason.Rank()
		if ri != rj {
			return ri < rj
		}
		if !items[i].thread.LastEventAt.Equal(items[j].thread.LastEventAt) {
			return items[i].thread.LastEventAt.Before(items[j].thread.LastEventAt)
		}
		if items[i].project != items[j].project {
			return items[i].project < items[j].project
		}
		if items[i].session != items[j].session {
			return items[i].session < items[j].session
		}
		return items[i].thread.ID < items[j].thread.ID
	})
	return items
}

// needsYouSection renders the "NEEDS YOU" block: needs-you items text-first,
// glyph-second, APPROVE sorted ABOVE GOAL-REACHED above generic. APPROVE uses
// the hot/act-now accent; GOAL-REACHED a distinct cyan REVIEW accent — both with
// TEXT labels that survive NO_COLOR. GOAL-REACHED is never a bare green check: it
// stays inside NEEDS YOU below APPROVE so it never reads as "healthy / no
// action". Returns "" when nothing needs the human (the block is then omitted).
func (m NOCModel) needsYouSection(projects []noc.ProjectSnapshot) string {
	items := collectNeedsYou(projects)
	if len(items) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString(m.th.paint(m.th.needsYou, "NEEDS YOU"))
	b.WriteString(m.th.paint(m.th.dim, fmt.Sprintf(" (%d)", len(items))))
	b.WriteString("\n")
	for _, it := range items {
		b.WriteString("  " + m.needsYouRow(it) + "\n")
	}
	return b.String()
}

// needsYouRow renders one NEEDS YOU line:
//
//	⏸ APPROVE       <project>/<session>  <who> paused · <subject> · <age>
//	✓ GOAL-REACHED  <project>/<session>  team done · review and close · <age>
//
// The reason label + glyph lead (text always present); the squad path, a short
// human phrase, the subject, and the age follow, all dim. APPROVE is painted hot;
// GOAL-REACHED cyan (review); generic stays in the needs-you accent.
func (m NOCModel) needsYouRow(it nocNeedsYouItem) string {
	glyph, label, style := m.attnReasonChrome(it.thread.AttnReason)
	var b strings.Builder
	b.WriteString(m.th.paint(style, glyph+" "+padRight(label, 13)))

	loc := it.project + "/" + it.session
	b.WriteString(" " + m.th.paint(m.th.brand, loc))

	dot := " " + m.dot() + " "
	parts := []string{attnReasonPhrase(it.thread)}
	if subj := strings.TrimSpace(threadTitle(it.thread)); subj != "" {
		parts = append(parts, truncate(subj, 40))
	}
	if age := nocThreadAge(it.thread); age != "" {
		parts = append(parts, age)
	}
	b.WriteString(" " + m.th.paint(m.th.dim, strings.Join(parts, dot)))
	return b.String()
}

// padRight pads s with spaces to at least width w (visible runes), so the reason
// labels in the NEEDS YOU block left-align into a column.
func padRight(s string, w int) string {
	if n := len([]rune(s)); n < w {
		return s + strings.Repeat(" ", w-n)
	}
	return s
}

// attnReasonChrome maps a needs-you reason to its (glyph, TEXT label, style).
// APPROVE is the hot act-now accent; GOAL-REACHED the distinct cyan review
// accent; generic falls back to the needs-you accent. The text label is always
// returned so a NO_COLOR terminal still distinguishes the reasons.
func (m NOCModel) attnReasonChrome(r state.AttnReason) (glyph, label string, style lipgloss.Style) {
	switch r {
	case state.AttnApprove:
		return nocGlyphApprove.glyph(m.colorMode), "APPROVE", m.th.needsYou
	case state.AttnGoalReached:
		return nocGlyphGoal.glyph(m.colorMode), "GOAL-REACHED", m.th.review
	default:
		return nocGlyphNeedsYou.glyph(m.colorMode), "NEEDS-YOU", m.th.needsYou
	}
}

// attnReasonInline renders the compact inline reason chip shown on a needs-you
// session tree row: glyph + TEXT label in the reason's accent (hot for approve,
// cyan review for goal-reached, hot for a plain ask). Returns "" for AttnNone.
func (m NOCModel) attnReasonInline(r state.AttnReason) string {
	if r == state.AttnNone {
		return ""
	}
	glyph, label, style := m.attnReasonChrome(r)
	return m.th.paint(style, glyph+" "+label)
}

// attnReasonPhrase is the short human phrase that follows the reason label:
// "<who> paused" for approve, "team done · review and close" for goal-reached,
// "<who> asks" for a plain question.
func attnReasonPhrase(th state.ThreadSummary) string {
	who := threadAsker(th)
	switch th.AttnReason {
	case state.AttnApprove:
		return who + " paused"
	case state.AttnGoalReached:
		return "team done · review and close"
	default:
		return who + " asks"
	}
}

// threadAsker is the non-operator participant a needs-you thread is waiting on
// (the agent that addressed the human), or "an agent" when none is recoverable.
func threadAsker(th state.ThreadSummary) string {
	for _, p := range th.Participants {
		if p != "" && p != state.DefaultOperatorHandle {
			return p
		}
	}
	return "an agent"
}

// rollupView is the --once digest: a NEEDS YOU block (operator action required)
// and a NEEDS ATTENTION section (running squads that carry live at-risk/blocked,
// or needs-you) on top, then a compact PROJECT ROLLUPS list (one line per squad,
// attention-first). Stale-only squads render dim with their stale counts
// parenthesized, never as live attention.
func (m NOCModel) rollupView() string {
	var b strings.Builder

	projects := m.visibleProjects()

	// --- NEEDS YOU: operator action required, reason-first. Rendered only when
	// something actually needs the human (never fabricated). ---
	if ny := m.needsYouSection(projects); ny != "" {
		b.WriteString(ny)
		b.WriteString("\n")
	}

	// --- NEEDS ATTENTION: live squads with something outstanding now. ---
	var attn []noc.ProjectSnapshot
	for _, ps := range projects {
		if ps.Warning != "" {
			continue
		}
		r := ps.Snap.Rollup
		if r.NeedsYou > 0 || (hasRunningAgentSnap(ps.Snap) && (r.AtRisk > 0 || r.Blocked > 0)) {
			attn = append(attn, ps)
		}
	}
	b.WriteString(m.th.paint(m.th.brand, "NEEDS ATTENTION"))
	b.WriteString("\n")
	if len(attn) == 0 {
		b.WriteString(m.th.paint(m.th.dim, "  (nothing live needs you right now)") + "\n")
	}
	for _, ps := range attn {
		b.WriteString("  " + m.projectRollupLine(ps, true) + "\n")
	}

	// --- PROJECT ROLLUPS: every (visible) squad, one calm line each. ---
	b.WriteString("\n")
	b.WriteString(m.th.paint(m.th.brand, nocCount(len(projects), "PROJECT", "PROJECTS")))
	b.WriteString(m.th.paint(m.th.dim, fmt.Sprintf(" (%d)", len(projects))))
	b.WriteString("\n")
	if len(projects) == 0 {
		b.WriteString(m.th.paint(m.th.dim, "  (no matching squads)") + "\n")
	}
	for _, ps := range projects {
		b.WriteString("  " + m.projectRollupLine(ps, false) + "\n")
	}
	return b.String()
}

// projectRollupLine renders one squad as a single rollup row: state glyph,
// project label, a liveness phrase ("running N/M agents alive" / "stopped"), and
// the triage tally (live leading, stale dim/parenthesized). When attn is true the
// live counts are emphasized (it heads the NEEDS ATTENTION section).
func (m NOCModel) projectRollupLine(ps noc.ProjectSnapshot, attn bool) string {
	var b strings.Builder
	st := projectRollupState(ps)
	b.WriteString(m.th.paint(m.th.nocStateStyle(st), nocStateGlyph(st, m.colorMode)+" "))

	nameStyle := m.th.brand
	if st == nocNeedsYou {
		nameStyle = m.th.needsYou
	} else if projectIsStaleOnly(ps) {
		nameStyle = m.th.dim
	}
	b.WriteString(m.th.paint(nameStyle, ps.Project))

	if ps.Warning != "" {
		b.WriteString(" " + m.th.paint(m.th.atRisk, "warning: "+firstLine(ps.Warning)))
		return b.String()
	}

	b.WriteString(" " + m.th.paint(m.th.dim, projectLivenessPhrase(ps)))

	if tally := m.tallyText(ps.Snap.Rollup); tally != "" {
		b.WriteString(" " + tally)
	}
	return b.String()
}

// visibleProjects returns the projects to render in the digest, honoring the
// SAME scope the headline counts: the hideStale toggle (drop stopped/stale
// squads) AND the active filter. Keeping the digest and the headline on one
// scope is what makes the headline live-blocked total reconcile with the sum of
// the per-project rows (the count-leak the reviewer caught: a hidden/filtered
// squad's live blocks were counted in the headline but shown in no row).
func (m NOCModel) visibleProjects() []noc.ProjectSnapshot {
	return m.scopedProjects()
}

// scopedProjects is the single source of truth for "which squads are in view":
// it applies the hide-stale toggle and the typed filter, in the order the tree
// uses them, so the headline rollup, the --once digest, and the interactive
// tree all agree on the visible set. The headline is summed over THIS slice
// (scopedRollup), guaranteeing headline counts == sum(visible per-project).
func (m NOCModel) scopedProjects() []noc.ProjectSnapshot {
	out := make([]noc.ProjectSnapshot, 0, len(m.ms.Projects))
	for _, ps := range m.ms.Projects {
		if m.hideStale && projectIsStaleOnly(ps) {
			continue
		}
		if !projectMatchesNOCFilter(ps, m.filter) {
			continue
		}
		out = append(out, ps)
	}
	return out
}

// scopedRollup sums the per-project triage rollups over the in-view squads and
// counts how many of them are squads / running. This is the EXACT arithmetic the
// per-project rows perform, so the headline it feeds reconciles with their sum:
// headline live-blocked == sum(project live-blocked), and likewise for at-risk,
// needs-you, and the stale variants.
func scopedRollup(projects []noc.ProjectSnapshot) (r state.TriageRollup, squads, live int) {
	for _, ps := range projects {
		if ps.Warning != "" {
			squads++
			continue
		}
		r.Add(ps.Snap.Rollup)
		squads++
		if hasRunningAgentSnap(ps.Snap) {
			live++
		}
	}
	return r, squads, live
}

// headerView renders the brand rule + the rollup pulse line + a last-activity
// summary line.
func (m NOCModel) headerView() string {
	var b strings.Builder

	brand := m.th.paint(m.th.brand, "amq-squad NOC")
	sub := m.th.paint(m.th.dim, "command center")
	b.WriteString(brand + "  " + sub + "\n")
	b.WriteString(m.th.paint(m.th.rule, m.rule()) + "\n")
	b.WriteString(m.pulseLine())
	if la := m.lastActivityLine(); la != "" {
		b.WriteString("\n" + la)
	}
	return b.String()
}

// pulseLine is the rollup headline. It LEADS with what is alive — squads / live
// squads / needs-you / LIVE at-risk / LIVE blocked — all primary; the
// age-decayed STALE at-risk/blocked counts trail, dim and parenthesized, so a
// 38-blocked pile of 30-day-old threads never masquerades as live attention.
//
//	"14 squads · 1 live · 0 needs-you · 3 at-risk(live) · 0 blocked(live) · 38 blocked(stale) · <clock>"
func (m NOCModel) pulseLine() string {
	// Sum the headline over the SAME in-view squads the body renders, so the
	// live/stale blocked+at-risk totals reconcile with the per-project rows.
	r, squads, live := scopedRollup(m.scopedProjects())

	dim := func(s string) string { return m.th.paint(m.th.dim, s) }
	sep := dim(" " + m.dot() + " ")

	segs := []string{
		dim(nocCount(squads, "squad", "squads")),
		dim(strconv.Itoa(live) + " live"),
	}

	// needs-you: the single eye-grab (always live — human action never decays).
	nyText := strconv.Itoa(r.NeedsYou) + " needs-you"
	if r.NeedsYou > 0 {
		segs = append(segs, m.th.paint(m.th.needsYou, nocStateGlyph(nocNeedsYou, m.colorMode)+" "+nyText))
	} else {
		segs = append(segs, dim(nyText))
	}

	// LIVE at-risk / blocked lead (primary; colored when >0).
	if r.AtRisk > 0 {
		segs = append(segs, m.th.paint(m.th.atRisk, strconv.Itoa(r.AtRisk)+" at-risk(live)"))
	} else {
		segs = append(segs, dim("0 at-risk(live)"))
	}
	if r.Blocked > 0 {
		segs = append(segs, m.th.paint(m.th.blocked, strconv.Itoa(r.Blocked)+" blocked(live)"))
	} else {
		segs = append(segs, dim("0 blocked(live)"))
	}

	// STALE at-risk / blocked trail, dim + parenthesized — secondary signal only,
	// shown only when present so the calm case stays calm.
	if r.AtRiskStale > 0 {
		segs = append(segs, dim(strconv.Itoa(r.AtRiskStale)+" at-risk(stale)"))
	}
	if r.BlockedStale > 0 {
		segs = append(segs, dim(strconv.Itoa(r.BlockedStale)+" blocked(stale)"))
	}

	segs = append(segs, dim(m.clock()))
	return strings.Join(segs, sep)
}

// lastActivityLine is the top-level "last activity across all squads" summary,
// always dim. Empty when no project recorded any activity.
func (m NOCModel) lastActivityLine() string {
	if m.ms.LastActivity.IsZero() {
		return ""
	}
	age := ""
	if !m.ms.ObservedAt.IsZero() {
		if d := m.ms.ObservedAt.Sub(m.ms.LastActivity); d > 0 {
			age = " (" + ageLabel(d) + " ago)"
		}
	}
	return m.th.paint(m.th.dim, "last activity across all squads: "+m.ms.LastActivity.Format("15:04:05")+age)
}

// clock formats the observation time.
func (m NOCModel) clock() string {
	if m.ms.ObservedAt.IsZero() {
		return ""
	}
	return m.ms.ObservedAt.Format("15:04:05")
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

	// Needs-you reason inline on a session node: a needs-you session shows WHY
	// the human is needed (approve / goal-reached / a plain ask) right on the row.
	if n.kind == nodeSession && n.rollup.NeedsYou > 0 {
		if rl := m.attnReasonInline(n.session.Coordination.TopAttnReason()); rl != "" {
			b.WriteString(" " + rl)
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

// tallyText is a compact per-parent triage tally. LIVE classes lead, colored;
// STALE classes trail, dim and labeled "(stale)" so a stopped squad's ancient
// blocks read as decayed noise, not live attention, e.g.
// "(2 needs-you, 1 at-risk · 38 blocked stale)".
func (m NOCModel) tallyText(r state.TriageRollup) string {
	var live []string
	if r.NeedsYou > 0 {
		live = append(live, m.th.paint(m.th.needsYou, strconv.Itoa(r.NeedsYou)+" needs-you"))
	}
	if r.Blocked > 0 {
		live = append(live, m.th.paint(m.th.blocked, strconv.Itoa(r.Blocked)+" blocked"))
	}
	if r.AtRisk > 0 {
		live = append(live, m.th.paint(m.th.atRisk, strconv.Itoa(r.AtRisk)+" at-risk"))
	}

	var stale []string
	if r.BlockedStale > 0 {
		stale = append(stale, strconv.Itoa(r.BlockedStale)+" blocked stale")
	}
	if r.AtRiskStale > 0 {
		stale = append(stale, strconv.Itoa(r.AtRiskStale)+" at-risk stale")
	}

	if len(live) == 0 && len(stale) == 0 {
		return ""
	}
	sep := m.th.paint(m.th.dim, ", ")
	inner := strings.Join(live, sep)
	if len(stale) > 0 {
		staleText := m.th.paint(m.th.dim, strings.Join(stale, ", "))
		if inner != "" {
			inner += m.th.paint(m.th.dim, " "+m.dot()+" ") + staleText
		} else {
			inner = staleText
		}
	}
	open := m.th.paint(m.th.dim, "(")
	closep := m.th.paint(m.th.dim, ")")
	return open + inner + closep
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
		b.WriteString(" " + m.th.paint(m.th.brand, sessionLabel(sess)))
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
	meta = append(meta, "session "+sessionLabel(n.session))
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
	keys := "↑↓/jk move · →/l/⏎ expand/drill · ← collapse · ⏎/J jump · / filter · h hide-stale · t timeline · g refresh · esc back · ? help · q quit"
	if m.colorMode == ColorAscii {
		keys = "up/down move | right/l/enter expand | left collapse | enter/J jump | / filter | h hide-stale | t timeline | g refresh | esc back | ? help | q quit"
	}
	var b strings.Builder
	b.WriteString(m.th.paint(m.th.rule, m.thinRule()) + "\n")
	notes := []string{}
	if m.filter != "" {
		notes = append(notes, m.th.paint(m.th.atRisk, "filter: "+m.filter))
	}
	if m.hideStale {
		notes = append(notes, m.th.paint(m.th.dim, "hiding stale squads (h shows all)"))
	}
	if len(notes) > 0 {
		b.WriteString(strings.Join(notes, m.th.paint(m.th.dim, "  "+m.dot()+"  ")) + "\n")
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
		"  ←                 collapse the node, or ascend to its parent",
		"  enter             expand/drill; on a RUNNING agent: JUMP",
		"  J                 jump to the selected running agent's tmux window",
		"",
		"VIEW",
		"  /                 filter (needs-you / at-risk / blocked / agent: / model: / project: / session:)",
		"  h                 toggle hiding stopped/archived (stale) squads — focus on what is alive",
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
