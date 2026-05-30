package console

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/omriariav/amq-squad/v2/internal/act"
	"github.com/omriariav/amq-squad/v2/internal/noc"
	"github.com/omriariav/amq-squad/v2/internal/state"
)

// newPaletteModel builds a ready *NOCModel over a TWO-project / multi-session
// snapshot so the palette has several agents to fuzzy-filter across teams. The
// "beta" project's session carries a cto (codex, ALIVE) and a qa (claude,
// STOPPED); the "alpha" project carries an unrelated dev so the fuzzy match has
// to discriminate. Every seam is neutralized: no test touches a real bus/tmux.
func newPaletteModel(t *testing.T) *NOCModel {
	t.Helper()
	betaSess := state.Session{
		Name: "beta",
		Root: "/fake/proj/beta/.agent-mail",
		Agents: []state.Agent{
			{Handle: "cto", Role: "cto", Engine: "codex", Liveness: state.LivenessAlive},
			{Handle: "qa", Role: "qa", Engine: "claude", Liveness: state.LivenessDead},
		},
	}
	betaPS := noc.ProjectSnapshot{
		Project: "beta",
		Dir:     "/fake/proj/beta",
		Snap:    state.Snapshot{Sessions: []state.Session{betaSess}},
	}
	alphaSess := state.Session{
		Name: "alpha",
		Root: "/fake/proj/alpha/.agent-mail",
		Agents: []state.Agent{
			{Handle: "dev", Role: "dev", Engine: "codex", Liveness: state.LivenessAlive},
		},
	}
	alphaPS := noc.ProjectSnapshot{
		Project: "alpha",
		Dir:     "/fake/proj/alpha",
		Snap:    state.Snapshot{Sessions: []state.Session{alphaSess}},
	}
	ms := noc.MultiSnapshot{
		Roots:    []string{"/fake/proj"},
		Projects: []noc.ProjectSnapshot{betaPS, alphaPS},
	}

	m := newNOCModel(NOCRebuildConfig{Roots: []string{"/fake/proj"}})
	m.colorMode = ColorNone
	m.th = newNOCTheme(ColorNone)
	m.panes = func() ([]noc.TmuxPane, error) { return nil, nil }
	m.switchTo = func(noc.TmuxTarget) error { return nil }
	m.pidTree = func(int) []int { return nil }

	mm, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m2 := mm.(*NOCModel)
	mm, _ = m2.Update(nocSnapshotMsg{ms: ms})
	return mm.(*NOCModel)
}

// TestPalette_OpenAndClose proves 'p' opens the palette overlay and esc closes
// it, both through the PUBLIC Update (the level the live surface runs at).
func TestPalette_OpenAndClose(t *testing.T) {
	m := newPaletteModel(t)
	if m.palette != nil {
		t.Fatal("palette should start closed")
	}
	m, _ = nocPress(m, "p")
	if m.palette == nil {
		t.Fatal("p should open the command palette")
	}
	// The overlay renders its header (View dispatches to the palette overlay).
	if !strings.Contains(m.View(), "COMMAND PALETTE") {
		t.Errorf("open palette should render the palette overlay, got:\n%s", m.View())
	}
	m, _ = nocPress(m, "esc")
	if m.palette != nil {
		t.Error("esc should close the palette")
	}
}

// TestPalette_FuzzyFilterNarrows proves typing a fuzzy (subsequence) query
// narrows the candidate list to the matching agent. "betacto" is a subsequence
// of "beta/beta/cto" — it must keep the beta cto agent and drop the alpha dev.
func TestPalette_FuzzyFilterNarrows(t *testing.T) {
	m := newPaletteModel(t)
	m, _ = nocPress(m, "p")

	for _, ch := range "betacto" {
		m, _ = nocPress(m, string(ch))
	}
	res := m.palette.filtered()
	if len(res) == 0 {
		t.Fatalf("fuzzy %q should match at least the beta cto agent, got nothing", m.palette.query)
	}
	// Every survivor must be a fuzzy match; the beta cto agent must be among them
	// and the alpha dev must be gone.
	sawBetaCTO := false
	for _, it := range res {
		if !fuzzySubsequence(strings.ToLower(it.label), strings.ToLower(m.palette.query)) {
			t.Errorf("filtered result %q is not a fuzzy match for %q", it.label, m.palette.query)
		}
		if strings.Contains(it.label, "alpha") {
			t.Errorf("fuzzy %q must not keep the alpha dev row %q", m.palette.query, it.label)
		}
		if it.kind == palAgent && it.label == "beta/beta/cto" {
			sawBetaCTO = true
		}
	}
	if !sawBetaCTO {
		var labels []string
		for _, it := range res {
			labels = append(labels, it.label)
		}
		t.Errorf("fuzzy %q should narrow to the beta/beta/cto agent, got %v", m.palette.query, labels)
	}
}

// TestPalette_EnterRunningAgentJumps proves enter on a RUNNING agent calls the
// switchTo seam with the NAME-FIRST-resolved target (the pane whose title token
// is amq:<session>:<role>), exactly like the tree's gated jump. The palette must
// close after selecting.
func TestPalette_EnterRunningAgentJumps(t *testing.T) {
	m := newPaletteModel(t)

	var gotTarget noc.TmuxTarget
	called := false
	m.switchTo = func(tt noc.TmuxTarget) error { called = true; gotTarget = tt; return nil }
	// The launcher stamps the deterministic title amq:beta:cto on the cto pane;
	// the name-first resolver must pick THAT pane even though another codex pane
	// shares the cwd. The decoy comes first so a cwd+engine-only resolver would
	// mis-pick it.
	m.panes = func() ([]noc.TmuxPane, error) {
		return []noc.TmuxPane{
			{Session: "decoy", Window: "9", Pane: "9", Command: "codex", CWD: "/fake/proj/beta", Title: "amq:beta:other"},
			{Session: "beta", Window: "0", Pane: "1", Command: "codex", CWD: "/fake/proj/beta", Title: "amq:beta:cto"},
		}, nil
	}

	m, _ = nocPress(m, "p")
	for _, ch := range "betacto" {
		m, _ = nocPress(m, string(ch))
	}
	// The cto agent is the top match; select it.
	m, _ = nocPress(m, "enter")

	if !called {
		t.Fatal("enter on a running agent should call the switch seam (the gated jump)")
	}
	if gotTarget.Session != "beta" || gotTarget.Window != "0" || gotTarget.Pane != "1" {
		t.Errorf("jump should resolve name-first to amq:beta:cto's pane (beta:0.1), got %+v", gotTarget)
	}
	if m.palette != nil {
		t.Error("selecting should close the palette")
	}
}

// TestPalette_EnterStoppedAgentSuggestsUpNoSwitch proves selecting a STOPPED
// agent does NOT jump to a live pane: with no tmux window for the squad it sets
// the suggest-up note and calls the switch seam zero times.
func TestPalette_EnterStoppedAgentSuggestsUpNoSwitch(t *testing.T) {
	m := newPaletteModel(t)
	switched := false
	m.switchTo = func(noc.TmuxTarget) error { switched = true; return nil }
	// No tmux windows at all: the squad is not running.
	m.panes = func() ([]noc.TmuxPane, error) { return nil, nil }

	m, _ = nocPress(m, "p")
	// "betaqa" fuzzy-matches the STOPPED qa agent (beta/beta/qa).
	for _, ch := range "betaqa" {
		m, _ = nocPress(m, string(ch))
	}
	// Confirm the top match is the stopped qa agent before selecting.
	sel, ok := m.palette.selected()
	if !ok || sel.label != "beta/beta/qa" {
		t.Fatalf("expected the stopped beta/beta/qa agent selected, got %+v ok=%v", sel, ok)
	}
	if sel.running {
		t.Fatal("fixture qa agent must be stopped for this test")
	}

	m, _ = nocPress(m, "enter")
	if switched {
		t.Error("selecting a stopped agent with no live window must NOT call the switch seam")
	}
	if !strings.Contains(m.jumpNote, "team not running") || !strings.Contains(m.jumpNote, "amq-squad up") {
		t.Errorf("stopped-agent select should set the suggest-up note, got %q", m.jumpNote)
	}
	if m.palette != nil {
		t.Error("selecting should close the palette")
	}
}

// TestPalette_TeamRowFocusesExistingWindow proves selecting a TEAM row focuses an
// EXISTING tmux window for the squad (the focus path) rather than jumping to a
// single agent pane.
func TestPalette_TeamRowFocusesExistingWindow(t *testing.T) {
	m := newPaletteModel(t)
	var gotTarget noc.TmuxTarget
	called := false
	m.switchTo = func(tt noc.TmuxTarget) error { called = true; gotTarget = tt; return nil }
	m.panes = func() ([]noc.TmuxPane, error) {
		return []noc.TmuxPane{{Session: "beta", Window: "0", Pane: "1", Command: "codex", CWD: "/fake/proj/beta"}}, nil
	}

	m, _ = nocPress(m, "p")
	for _, ch := range "betabetateam" {
		m, _ = nocPress(m, string(ch))
	}
	sel, ok := m.palette.selected()
	if !ok || sel.kind != palTeam {
		t.Fatalf("expected the beta team row selected, got %+v ok=%v", sel, ok)
	}
	m, _ = nocPress(m, "enter")
	if !called {
		t.Fatal("selecting a team row with a live window should focus it via the switch seam")
	}
	if gotTarget.Session != "beta" {
		t.Errorf("team focus targeted the wrong session: %+v", gotTarget)
	}
}

// TestPalette_CursorMovesWithinResults proves down/up move the selection within
// the filtered results and clamp at the bounds.
func TestPalette_CursorMovesWithinResults(t *testing.T) {
	m := newPaletteModel(t)
	m, _ = nocPress(m, "p")
	if m.palette.cursor != 0 {
		t.Fatalf("palette cursor should start at 0, got %d", m.palette.cursor)
	}
	n := len(m.palette.filtered())
	if n < 2 {
		t.Fatalf("fixture must produce >= 2 palette rows, got %d", n)
	}
	m, _ = nocPress(m, "down")
	if m.palette.cursor != 1 {
		t.Errorf("down should move palette cursor to 1, got %d", m.palette.cursor)
	}
	m, _ = nocPress(m, "up")
	if m.palette.cursor != 0 {
		t.Errorf("up should move palette cursor back to 0, got %d", m.palette.cursor)
	}
	// Up at the top clamps.
	m, _ = nocPress(m, "up")
	if m.palette.cursor != 0 {
		t.Errorf("up at the top should clamp to 0, got %d", m.palette.cursor)
	}
}

// TestPalette_ReadOnlyNoMutatingSeam proves the palette never reaches a mutating
// seam (send / lifecycle) no matter what is selected — it is read-only, only the
// gated tmux switch may fire.
func TestPalette_ReadOnlyNoMutatingSeam(t *testing.T) {
	m := newPaletteModel(t)
	sent := false
	mutated := false
	m.sendOp = func(act.OpMessage) error { sent = true; return nil }
	m.lifecycle = func(lifecycleOp) error { mutated = true; return nil }
	m.panes = func() ([]noc.TmuxPane, error) {
		return []noc.TmuxPane{{Session: "beta", Window: "0", Pane: "1", Command: "codex", CWD: "/fake/proj/beta", Title: "amq:beta:cto"}}, nil
	}

	m, _ = nocPress(m, "p")
	for _, ch := range "betacto" {
		m, _ = nocPress(m, string(ch))
	}
	m, _ = nocPress(m, "enter")
	if sent {
		t.Error("palette selection must NEVER call the send seam")
	}
	if mutated {
		t.Error("palette selection must NEVER call the lifecycle seam")
	}
}
