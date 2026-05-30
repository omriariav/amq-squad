package console

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/omriariav/amq-squad/v2/internal/noc"
)

// newSeededNOCModel builds a model over the three-project fixture, drives a
// window-size + snapshot message into it, and returns the ready model.
func newSeededNOCModel(t *testing.T) NOCModel {
	t.Helper()
	root, probe := seedNOCFixture(t)
	rebuild := NOCRebuildConfig{Roots: []string{root}, Depth: noc.DefaultDepth, Probe: probe}
	ms := noc.Collect(rebuild.Roots, rebuild.Depth, rebuild.Probe, rebuild.Thresholds)

	m := newNOCModel(rebuild)
	m.colorMode = ColorNone
	m.th = newNOCTheme(ColorNone)

	mm, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m = mm.(NOCModel)
	mm, _ = m.Update(nocSnapshotMsg{ms: ms})
	return mm.(NOCModel)
}

func nocKey(s string) tea.KeyMsg {
	switch s {
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	case "left":
		return tea.KeyMsg{Type: tea.KeyLeft}
	case "right":
		return tea.KeyMsg{Type: tea.KeyRight}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "backspace":
		return tea.KeyMsg{Type: tea.KeyBackspace}
	default:
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
	}
}

func nocPress(m NOCModel, s string) (NOCModel, tea.Cmd) {
	mm, cmd := m.Update(nocKey(s))
	return mm.(NOCModel), cmd
}

func TestNOCUpdate_MoveCursor(t *testing.T) {
	m := newSeededNOCModel(t)
	if m.cursor != 0 {
		t.Fatalf("initial cursor = %d, want 0", m.cursor)
	}
	m, _ = nocPress(m, "j")
	if m.cursor != 1 {
		t.Errorf("after j, cursor = %d, want 1", m.cursor)
	}
	m, _ = nocPress(m, "k")
	if m.cursor != 0 {
		t.Errorf("after k, cursor = %d, want 0", m.cursor)
	}
	// Up at the top clamps.
	m, _ = nocPress(m, "up")
	if m.cursor != 0 {
		t.Errorf("up at top should clamp to 0, got %d", m.cursor)
	}
}

func TestNOCUpdate_CollapseAndExpand(t *testing.T) {
	m := newSeededNOCModel(t)
	// Cursor 0 is the most-urgent project (beta, needs-you), expanded by default,
	// so its session/agent descendants follow it.
	top, ok := m.selectedNode()
	if !ok || top.kind != nodeProject {
		t.Fatalf("expected a project at cursor 0, got %+v ok=%v", top, ok)
	}
	before := len(m.nodes())

	// Collapse it: its descendants disappear.
	m, _ = nocPress(m, "left")
	if !m.tree.isCollapsed(top.id) {
		t.Errorf("left should collapse the project node %q", top.id)
	}
	if got := len(m.nodes()); got >= before {
		t.Errorf("collapse should reduce visible nodes: before=%d after=%d", before, got)
	}

	// Expand it again: descendants return.
	m, _ = nocPress(m, "right")
	if m.tree.isCollapsed(top.id) {
		t.Errorf("right should re-expand the project node %q", top.id)
	}
	if got := len(m.nodes()); got != before {
		t.Errorf("re-expand should restore node count: before=%d after=%d", before, got)
	}
}

func TestNOCUpdate_DrillIntoChild(t *testing.T) {
	m := newSeededNOCModel(t)
	// On an expanded parent, right drills to the first child (next node deeper).
	parent, _ := m.selectedNode()
	m, _ = nocPress(m, "right")
	child, ok := m.selectedNode()
	if !ok {
		t.Fatal("no node after drill")
	}
	if child.depth <= parent.depth {
		t.Errorf("right on expanded parent should drill deeper: parent depth=%d child depth=%d", parent.depth, child.depth)
	}
}

func TestNOCUpdate_JumpCallsSwitcherWithResolvedTarget(t *testing.T) {
	m := newSeededNOCModel(t)

	// Find beta's project dir (the alive claude agent lives there).
	var beta noc.ProjectSnapshot
	for _, n := range m.nodes() {
		if n.kind == nodeProject && n.label == "beta" {
			beta = n.project
			break
		}
	}
	if beta.Dir == "" {
		t.Fatal("beta project not found")
	}

	var gotTarget noc.TmuxTarget
	called := false
	m.switchTo = func(tt noc.TmuxTarget) error { called = true; gotTarget = tt; return nil }
	m.panes = func() ([]noc.TmuxPane, error) {
		// A pane in beta's dir running claude resolves the beta/qa agent.
		return []noc.TmuxPane{{
			Session: "beta", Window: "0", Pane: "1", PID: 5001,
			Command: "claude", CWD: beta.Dir,
		}}, nil
	}
	m.pidTree = func(int) []int { return nil }

	// Navigate to beta's qa agent node and press J.
	agentIdx := -1
	for i, n := range m.nodes() {
		if n.kind == nodeAgent && n.agent.Handle == "qa" {
			agentIdx = i
			break
		}
	}
	if agentIdx < 0 {
		t.Fatalf("qa agent node not found; nodes=%d", len(m.nodes()))
	}
	m.cursor = agentIdx
	m, _ = nocPress(m, "J")

	if !called {
		t.Fatal("J on a running agent should call the injected switcher")
	}
	if gotTarget.Session != "beta" || gotTarget.Window != "0" || gotTarget.Pane != "1" {
		t.Errorf("switcher got wrong target: %+v", gotTarget)
	}
}

func TestNOCUpdate_JumpNoPaneShowsSuggestionNotError(t *testing.T) {
	m := newSeededNOCModel(t)
	called := false
	m.switchTo = func(noc.TmuxTarget) error { called = true; return nil }
	m.panes = func() ([]noc.TmuxPane, error) { return nil, nil } // no panes resolve
	m.pidTree = func(int) []int { return nil }

	agentIdx := -1
	for i, n := range m.nodes() {
		if n.kind == nodeAgent && n.agent.Handle == "qa" {
			agentIdx = i
			break
		}
	}
	if agentIdx < 0 {
		t.Fatal("qa agent node not found")
	}
	m.cursor = agentIdx
	m, _ = nocPress(m, "J")

	if called {
		t.Error("switcher must NOT be called when no pane resolves")
	}
	if m.jumpNote == "" {
		t.Error("an unresolved jump should set a guidance jumpNote, not error out")
	}
}

func TestNOCUpdate_JumpNotInTmuxShowsSuggestion(t *testing.T) {
	m := newSeededNOCModel(t)
	var beta noc.ProjectSnapshot
	for _, n := range m.nodes() {
		if n.kind == nodeProject && n.label == "beta" {
			beta = n.project
		}
	}
	m.switchTo = func(tt noc.TmuxTarget) error {
		return &noc.NotInTmuxError{Target: tt, Command: noc.SuggestJump(tt)}
	}
	m.panes = func() ([]noc.TmuxPane, error) {
		return []noc.TmuxPane{{Session: "beta", Window: "0", Pane: "1", PID: 5001, Command: "claude", CWD: beta.Dir}}, nil
	}
	m.pidTree = func(int) []int { return nil }

	for i, n := range m.nodes() {
		if n.kind == nodeAgent && n.agent.Handle == "qa" {
			m.cursor = i
		}
	}
	m, _ = nocPress(m, "J")
	if m.jumpNote == "" || !strings.Contains(m.jumpNote, "tmux switch-client") {
		t.Errorf("not-in-tmux jump should surface the suggested command, got %q", m.jumpNote)
	}
}

func TestNOCUpdate_FilterRouting(t *testing.T) {
	m := newSeededNOCModel(t)
	// Open the filter editor and type "needs-you".
	m, _ = nocPress(m, "/")
	if !m.filterEditing {
		t.Fatal("/ should open the filter editor")
	}
	for _, ch := range "needs-you" {
		m, _ = nocPress(m, string(ch))
	}
	if m.filter != "needs-you" {
		t.Fatalf("filter text = %q, want needs-you", m.filter)
	}
	m, _ = nocPress(m, "enter")
	if m.filterEditing {
		t.Error("enter should close the filter editor")
	}
	// Only beta (the needs-you project) survives the filter.
	for _, n := range m.nodes() {
		if n.kind == nodeProject && n.label != "beta" {
			t.Errorf("needs-you filter should drop project %q", n.label)
		}
	}
	// esc clears the filter.
	m, _ = nocPress(m, "esc")
	if m.filter != "" {
		t.Errorf("esc should clear the filter, got %q", m.filter)
	}
}

func TestNOCUpdate_QuitKey(t *testing.T) {
	m := newSeededNOCModel(t)
	_, cmd := nocPress(m, "q")
	if cmd == nil {
		t.Fatal("q should return a command (tea.Quit)")
	}
	if msg := cmd(); msg == nil {
		t.Error("q's command should produce a quit message")
	}
}

func TestNOCUpdate_HelpToggle(t *testing.T) {
	m := newSeededNOCModel(t)
	m, _ = nocPress(m, "?")
	if !m.showHelp {
		t.Fatal("? should open help")
	}
	if !strings.Contains(m.View(), "help") {
		t.Errorf("help view should render help text:\n%s", m.View())
	}
	m, _ = nocPress(m, "x") // any key dismisses help
	if m.showHelp {
		t.Error("a key should dismiss help")
	}
}

func TestNOCUpdate_SelectionStableAcrossSnapshotReplacement(t *testing.T) {
	m := newSeededNOCModel(t)
	// Move to a deeper, identifiable node (beta's qa agent).
	for i, n := range m.nodes() {
		if n.kind == nodeAgent && n.agent.Handle == "qa" {
			m.cursor = i
			m.rememberSelection()
		}
	}
	wantID := m.selectedID
	if wantID == "" {
		t.Fatal("expected a remembered selection id")
	}

	// Replace the snapshot with a freshly-collected (identical) one.
	root := m.rebuild.Roots[0]
	ms2 := noc.Collect([]string{root}, noc.DefaultDepth, m.rebuild.Probe, m.rebuild.Thresholds)
	mm, _ := m.Update(nocSnapshotMsg{ms: ms2})
	m = mm.(NOCModel)

	sel, ok := m.selectedNode()
	if !ok {
		t.Fatal("no selection after snapshot replacement")
	}
	if sel.id != wantID {
		t.Errorf("selection not stable across snapshot: want %q, got %q", wantID, sel.id)
	}
}

func TestNOCUpdate_ReadOnlyNoMutatingKeys(t *testing.T) {
	// READ-ONLY contract: the well-known mutating letters (s/d/m/x/a/D) must be
	// inert — none may change the snapshot, and none may trigger the tmux switch
	// (the only side effect is the explicit jump on J/enter).
	m := newSeededNOCModel(t)
	switched := false
	m.switchTo = func(noc.TmuxTarget) error { switched = true; return nil }
	before := len(m.ms.Projects)
	for _, k := range []string{"s", "d", "m", "x", "a", "D"} {
		m, _ = nocPress(m, k)
	}
	if switched {
		t.Error("no plain letter key may trigger the tmux switch")
	}
	if len(m.ms.Projects) != before {
		t.Error("no key may change the snapshot (read-only contract)")
	}
}
