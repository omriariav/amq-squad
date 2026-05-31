package console

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/omriariav/amq-squad/v2/internal/act"
	"github.com/omriariav/amq-squad/v2/internal/noc"
	"github.com/omriariav/amq-squad/v2/internal/state"
)

// --- control-layer test scaffolding --------------------------------------
//
// These tests build a model over a hand-crafted MultiSnapshot so the node
// kinds (project/session/agent) and the needs-you thread are fully under the
// test's control, independent of the shared NOC fixture. Every test drives the
// PUBLIC Update with real tea.KeyMsg values (via nocPress from the sibling test
// file) and asserts on the injected SEAMS — never on a real amq/tmux side
// effect — which is exactly the PR15 safety contract: a decline never calls a
// seam; a confirm calls it once with the exact OpMessage / lifecycle command.

// ctlRoot is the pinned AMQ root the control fixture writes against. It is a
// fake path: no test ever lets a write reach a real bus (the send seam is a
// fake), so the root only has to be a stable, recognizable string in previews.
const ctlRoot = "/fake/root/.agent-mail"

// newControlModel builds a ready *NOCModel over a one-project / one-session
// snapshot whose session carries a needs-you (AttnApprove) thread between the
// operator ("user") and the agent "qa". The agent is alive. The model is sized +
// marked ready so View() renders the live frame and node selection works.
func newControlModel(t *testing.T) *NOCModel {
	t.Helper()
	th := state.ThreadSummary{
		ID:           "decision/ship",
		Participants: []string{"qa", "user"},
		Subject:      "Ship it?",
		Triage:       state.TriageNeedsYou,
		AttnReason:   state.AttnApprove,
	}
	sess := state.Session{
		Name: "beta",
		Root: ctlRoot,
		Agents: []state.Agent{
			{Handle: "qa", Role: "qa", Engine: "claude", Liveness: state.LivenessAlive},
			{Handle: "dev", Role: "dev", Engine: "codex", Liveness: state.LivenessAlive},
		},
		Coordination: state.Coordination{Threads: []state.ThreadSummary{th}},
		Rollup:       state.TriageRollup{NeedsYou: 1},
	}
	ps := noc.ProjectSnapshot{
		Project: "beta",
		Dir:     "/fake/proj/beta",
		Snap:    state.Snapshot{Sessions: []state.Session{sess}},
	}
	ms := noc.MultiSnapshot{Roots: []string{"/fake/proj"}, Projects: []noc.ProjectSnapshot{ps}}

	m := newNOCModel(NOCRebuildConfig{Roots: []string{"/fake/proj"}})
	m.colorMode = ColorNone
	m.th = newNOCTheme(ColorNone)
	// Neutralize the real seams: no test may touch a live bus or tmux.
	m.sendOp = func(act.OpMessage) error { return nil }
	m.lifecycle = func(lifecycleOp) error { return nil }
	m.panes = func() ([]noc.TmuxPane, error) { return nil, nil }
	m.switchTo = func(noc.TmuxTarget) error { return nil }

	mm, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m2 := mm.(*NOCModel)
	mm, _ = m2.Update(nocSnapshotMsg{ms: ms})
	return mm.(*NOCModel)
}

// selectKind moves the cursor onto the first node of the given kind (and, for
// agents, the given handle). It fails the test if no such node is visible.
func selectKind(t *testing.T, m *NOCModel, kind nocNodeKind, handle string) {
	t.Helper()
	for i, n := range m.nodes() {
		if n.kind != kind {
			continue
		}
		if kind == nodeAgent && n.agent.Handle != handle {
			continue
		}
		m.cursor = i
		return
	}
	t.Fatalf("no node of kind %d (handle %q) found in %d nodes", kind, handle, len(m.nodes()))
}

// theNeedsYouThread returns the fixture's needs-you thread off the current
// selection's session, so a test can build the EXACT expected OpMessage.
func theNeedsYouThread(t *testing.T, m *NOCModel) state.ThreadSummary {
	t.Helper()
	n, ok := m.selectedNode()
	if !ok {
		t.Fatal("no selected node")
	}
	ny := n.session.Coordination.NeedsYouThreads()
	if len(ny) == 0 {
		t.Fatal("selected session has no needs-you thread")
	}
	return ny[0]
}

// --- approve / deny ------------------------------------------------------

func TestControl_ApproveConfirmGate(t *testing.T) {
	t.Run("a opens a preview overlay showing act.Preview", func(t *testing.T) {
		m := newControlModel(t)
		selectKind(t, m, nodeSession, "")

		called := false
		m.sendOp = func(act.OpMessage) error { called = true; return nil }

		m, _ = nocPress(m, "a")
		if m.pending == nil {
			t.Fatal("a on a needs-you session should open the confirm overlay")
		}
		want := act.Preview(act.Approve(ctlRoot, "beta", theNeedsYouThread(t, m)))
		if m.pending.preview != want {
			t.Errorf("overlay preview mismatch:\n got %q\nwant %q", m.pending.preview, want)
		}
		if !strings.Contains(m.View(), "amq send") {
			t.Errorf("confirm overlay should render the literal amq send command:\n%s", m.View())
		}
		if !strings.Contains(m.View(), "--kind answer") {
			t.Errorf("approve preview should carry --kind answer:\n%s", m.View())
		}
		if called {
			t.Fatal("merely opening the overlay must NOT call the send seam")
		}
	})

	t.Run("esc declines: send seam NEVER called", func(t *testing.T) {
		m := newControlModel(t)
		selectKind(t, m, nodeSession, "")
		called := false
		m.sendOp = func(act.OpMessage) error { called = true; return nil }

		m, _ = nocPress(m, "a")
		m, _ = nocPress(m, "esc")
		if called {
			t.Error("declining (esc) must NOT call the send seam")
		}
		if m.pending != nil {
			t.Error("esc should close the confirm overlay")
		}
	})

	t.Run("y confirms: send seam called once with the Approve OpMessage", func(t *testing.T) {
		m := newControlModel(t)
		selectKind(t, m, nodeSession, "")
		var got []act.OpMessage
		m.sendOp = func(op act.OpMessage) error { got = append(got, op); return nil }

		want := act.Approve(ctlRoot, "beta", theNeedsYouThread(t, m))
		m, _ = nocPress(m, "a")
		m, _ = nocPress(m, "y")
		if len(got) != 1 {
			t.Fatalf("confirm should call the send seam exactly once, got %d", len(got))
		}
		if got[0] != want {
			t.Errorf("confirmed OpMessage mismatch:\n got %+v\nwant %+v", got[0], want)
		}
		if m.pending != nil {
			t.Error("confirm should close the overlay")
		}
	})
}

func TestControl_DenyConfirmGate(t *testing.T) {
	t.Run("x decline: send seam NEVER called", func(t *testing.T) {
		m := newControlModel(t)
		selectKind(t, m, nodeSession, "")
		called := false
		m.sendOp = func(act.OpMessage) error { called = true; return nil }

		m, _ = nocPress(m, "x")
		if m.input == nil {
			t.Fatal("x should open the deny reason editor")
		}
		for _, ch := range "looks risky" {
			m, _ = nocPress(m, string(ch))
		}
		m, _ = nocPress(m, "enter") // editor -> confirm overlay
		if m.pending == nil {
			t.Fatal("enter on the deny editor should open the confirm overlay")
		}
		if called {
			t.Fatal("building the deny preview must not call the send seam")
		}
		m, _ = nocPress(m, "esc") // decline
		if called {
			t.Error("declining deny must NOT call the send seam")
		}
	})

	t.Run("y confirms: send seam called once with the Deny OpMessage", func(t *testing.T) {
		m := newControlModel(t)
		selectKind(t, m, nodeSession, "")
		var got []act.OpMessage
		m.sendOp = func(op act.OpMessage) error { got = append(got, op); return nil }

		want := act.Deny(ctlRoot, "beta", theNeedsYouThread(t, m), "nope")
		m, _ = nocPress(m, "x")
		for _, ch := range "nope" {
			m, _ = nocPress(m, string(ch))
		}
		m, _ = nocPress(m, "enter")
		m, _ = nocPress(m, "y")
		if len(got) != 1 {
			t.Fatalf("confirmed deny should call the send seam once, got %d", len(got))
		}
		if got[0] != want {
			t.Errorf("confirmed deny OpMessage mismatch:\n got %+v\nwant %+v", got[0], want)
		}
	})
}

// --- message -------------------------------------------------------------

func TestControl_MessageConfirmGate(t *testing.T) {
	// typeMessage opens the editor on agent qa, types body, and returns the model
	// at the confirm overlay (preview built, nothing sent yet).
	typeMessage := func(m *NOCModel) *NOCModel {
		selectKind(t, m, nodeAgent, "qa")
		m, _ = nocPress(m, "m")
		if m.input == nil {
			t.Fatal("m on an agent should open the message body editor")
		}
		for _, ch := range "hello qa" {
			m, _ = nocPress(m, string(ch))
		}
		m, _ = nocPress(m, "enter")
		if m.pending == nil {
			t.Fatal("enter on the message editor should open the confirm overlay")
		}
		return m
	}

	t.Run("preview addresses qa via amq send; opening sends nothing", func(t *testing.T) {
		m := newControlModel(t)
		sent := false
		m.sendOp = func(act.OpMessage) error { sent = true; return nil }
		m = typeMessage(m)
		if !strings.Contains(m.pending.preview, "amq send") || !strings.Contains(m.pending.preview, "--to qa") {
			t.Errorf("message preview should address qa via amq send: %q", m.pending.preview)
		}
		if sent {
			t.Error("opening the message overlay must not call the send seam")
		}
	})

	t.Run("decline: send seam NEVER called", func(t *testing.T) {
		m := newControlModel(t)
		called := false
		m.sendOp = func(act.OpMessage) error { called = true; return nil }
		m = typeMessage(m)
		m, _ = nocPress(m, "n") // any non-confirm key cancels
		if called {
			t.Error("declining a message must NOT call the send seam")
		}
		if m.pending != nil {
			t.Error("declining should close the overlay")
		}
	})

	t.Run("confirm: send seam called once addressing qa", func(t *testing.T) {
		m := newControlModel(t)
		var got []act.OpMessage
		m.sendOp = func(op act.OpMessage) error { got = append(got, op); return nil }
		m = typeMessage(m)
		m, _ = nocPress(m, "y")
		if len(got) != 1 {
			t.Fatalf("confirmed message should call the send seam once, got %d", len(got))
		}
		if got[0].To != "qa" || got[0].Body != "hello qa" {
			t.Errorf("confirmed message OpMessage mismatch: %+v", got[0])
		}
	})
}

// --- broadcast -----------------------------------------------------------

func TestControl_BroadcastConfirmGate(t *testing.T) {
	// typeBroadcast opens the editor on the session, types subject then body, and
	// returns the model at the confirm overlay (preview built, nothing sent yet).
	typeBroadcast := func(m *NOCModel) *NOCModel {
		selectKind(t, m, nodeSession, "")
		m, _ = nocPress(m, "b")
		if m.input == nil {
			t.Fatal("b on a session should open the broadcast subject editor")
		}
		for _, ch := range "standup" {
			m, _ = nocPress(m, string(ch))
		}
		m, _ = nocPress(m, "enter") // subject -> body stage
		if m.input == nil || m.input.stage != 1 {
			t.Fatal("first enter on broadcast should advance subject -> body")
		}
		for _, ch := range "status please" {
			m, _ = nocPress(m, string(ch))
		}
		m, _ = nocPress(m, "enter") // body -> confirm overlay
		if m.pending == nil {
			t.Fatal("second enter on broadcast should open the confirm overlay")
		}
		return m
	}

	t.Run("decline: send seam NEVER called", func(t *testing.T) {
		m := newControlModel(t)
		called := false
		m.sendOp = func(act.OpMessage) error { called = true; return nil }
		m = typeBroadcast(m)
		m, _ = nocPress(m, "esc")
		if called {
			t.Error("declining a broadcast must NOT call the send seam")
		}
		if m.pending != nil {
			t.Error("declining should close the overlay")
		}
	})

	t.Run("confirm: send seam called once with the exact Broadcast OpMessage", func(t *testing.T) {
		m := newControlModel(t)
		var got []act.OpMessage
		m.sendOp = func(op act.OpMessage) error { got = append(got, op); return nil }
		m = typeBroadcast(m)
		m, _ = nocPress(m, "y")
		if len(got) != 1 {
			t.Fatalf("confirmed broadcast should call the send seam once, got %d", len(got))
		}
		want := act.Broadcast(ctlRoot, "beta", []string{"qa", "dev"}, "standup", "status please")
		if got[0] != want {
			t.Errorf("confirmed broadcast OpMessage mismatch:\n got %+v\nwant %+v", got[0], want)
		}
	})
}

// --- stop / resume -------------------------------------------------------

func TestControl_StopConfirmGate(t *testing.T) {
	t.Run("S decline: lifecycle seam not called", func(t *testing.T) {
		m := newControlModel(t)
		selectKind(t, m, nodeSession, "")
		called := false
		m.lifecycle = func(lifecycleOp) error { called = true; return nil }

		m, _ = nocPress(m, "S")
		if m.pending == nil {
			t.Fatal("S on a session should open the confirm overlay")
		}
		if !strings.Contains(m.pending.preview, "amq-squad stop --all --session beta") {
			t.Errorf("stop overlay should preview the exact lifecycle command: %q", m.pending.preview)
		}
		m, _ = nocPress(m, "esc")
		if called {
			t.Error("declining stop must NOT call the lifecycle seam")
		}
	})

	t.Run("S confirm: lifecycle seam called for the right session", func(t *testing.T) {
		m := newControlModel(t)
		selectKind(t, m, nodeSession, "")
		var ops []lifecycleOp
		m.lifecycle = func(op lifecycleOp) error { ops = append(ops, op); return nil }

		m, _ = nocPress(m, "S")
		m, _ = nocPress(m, "y")
		if len(ops) != 1 {
			t.Fatalf("confirmed stop should call the lifecycle seam once, got %d", len(ops))
		}
		if ops[0].Verb != lifecycleStop || ops[0].Session != "beta" {
			t.Errorf("stop lifecycleOp mismatch: %+v", ops[0])
		}
	})

	t.Run("R confirm: resume lifecycle seam called for the right session", func(t *testing.T) {
		m := newControlModel(t)
		selectKind(t, m, nodeSession, "")
		var ops []lifecycleOp
		m.lifecycle = func(op lifecycleOp) error { ops = append(ops, op); return nil }

		m, _ = nocPress(m, "R")
		if m.pending == nil || !strings.Contains(m.pending.preview, "amq-squad resume --session beta") {
			t.Fatalf("R should preview the exact resume command, got %+v", m.pending)
		}
		m, _ = nocPress(m, "y")
		if len(ops) != 1 || ops[0].Verb != lifecycleResume || ops[0].Session != "beta" {
			t.Fatalf("confirmed resume should call lifecycle once for beta, got %+v", ops)
		}
	})
}

// --- focus / open ('o') --------------------------------------------------

func TestControl_FocusRunningCallsSwitch(t *testing.T) {
	m := newControlModel(t)
	selectKind(t, m, nodeSession, "")
	var gotTarget noc.TmuxTarget
	called := false
	m.switchTo = func(tt noc.TmuxTarget) error { called = true; gotTarget = tt; return nil }
	// A live tmux window for session "beta" exists.
	m.panes = func() ([]noc.TmuxPane, error) {
		return []noc.TmuxPane{{Session: "beta", Window: "0", Pane: "1", Command: "claude", CWD: "/fake/proj/beta"}}, nil
	}

	// 'o' is now confirm-gated (QA-2/QA-4b): it opens a READ-ONLY focus confirm
	// overlay (jumpPending, NOT the mutating pending) and does not focus yet.
	m, _ = nocPress(m, "o")
	if m.jumpPending == nil {
		t.Fatal("o should open the read-only focus confirm overlay")
	}
	if m.pending != nil {
		t.Error("focus is read-only: it must never open the MUTATING confirm overlay")
	}
	if called {
		t.Fatal("o must NOT call the switch seam before confirm")
	}
	// y confirms: the switch seam fires with the right session.
	m, _ = nocPress(m, "y")
	if !called {
		t.Fatal("y on the focus confirm should call the switch seam")
	}
	if gotTarget.Session != "beta" {
		t.Errorf("focus targeted the wrong session: %+v", gotTarget)
	}
}

func TestControl_FocusStoppedSuggestsUpCallsNothing(t *testing.T) {
	m := newControlModel(t)
	selectKind(t, m, nodeSession, "")
	switched := false
	lifecycleCalled := false
	sent := false
	m.switchTo = func(noc.TmuxTarget) error { switched = true; return nil }
	m.lifecycle = func(lifecycleOp) error { lifecycleCalled = true; return nil }
	m.sendOp = func(act.OpMessage) error { sent = true; return nil }
	// No tmux windows: the squad is not running.
	m.panes = func() ([]noc.TmuxPane, error) { return nil, nil }

	// 'o' opens the read-only focus confirm overlay; confirming with y then finds
	// no running window and sets the suggest-up note (still NOTHING is called).
	m, _ = nocPress(m, "o")
	if m.pending != nil {
		t.Error("focus is read-only: it must never open the MUTATING confirm overlay")
	}
	if m.jumpPending == nil {
		t.Fatal("o should open the read-only focus confirm overlay")
	}
	m, _ = nocPress(m, "y")
	if switched || lifecycleCalled || sent {
		t.Error("o on a stopped squad must call NOTHING (no switch, no lifecycle, no send)")
	}
	if !strings.Contains(m.actNote, "team not running") || !strings.Contains(m.actNote, "amq-squad up") {
		t.Errorf("o on a stopped squad should set the suggest-up note, got %q", m.actNote)
	}
}

// --- wrong-node no-ops ---------------------------------------------------

func TestControl_WrongNodeNoOps(t *testing.T) {
	// 'S' (stop) on an AGENT row is a no-op note, no lifecycle call.
	t.Run("S on an agent row is a no-op", func(t *testing.T) {
		m := newControlModel(t)
		selectKind(t, m, nodeAgent, "qa")
		called := false
		m.lifecycle = func(lifecycleOp) error { called = true; return nil }
		m, _ = nocPress(m, "S")
		if called {
			t.Error("S on an agent row must NOT call the lifecycle seam")
		}
		if m.pending != nil {
			t.Error("S on an agent row must NOT open a confirm overlay")
		}
		if m.actNote == "" {
			t.Error("S on the wrong node should leave a guidance note")
		}
	})

	// 'a' (approve) on a PROJECT with no needs-you is a no-op note.
	t.Run("a on a project with no needs-you thread is a no-op", func(t *testing.T) {
		m := newControlModel(t)
		// Drop the needs-you thread so nothing needs the operator.
		m.ms.Projects[0].Snap.Sessions[0].Coordination = state.Coordination{}
		m.ms.Projects[0].Snap.Sessions[0].Rollup = state.TriageRollup{}
		selectKind(t, m, nodeProject, "")
		sent := false
		m.sendOp = func(act.OpMessage) error { sent = true; return nil }
		m, _ = nocPress(m, "a")
		if sent {
			t.Error("a on a project must NOT call the send seam")
		}
		if m.pending != nil {
			t.Error("a on a project with no needs-you must NOT open a confirm overlay")
		}
		if m.actNote == "" {
			t.Error("a on the wrong node should leave a guidance note")
		}
	})

	// 'm' (message) on a SESSION row (not an agent) is a no-op note.
	t.Run("m on a session row is a no-op", func(t *testing.T) {
		m := newControlModel(t)
		selectKind(t, m, nodeSession, "")
		sent := false
		m.sendOp = func(act.OpMessage) error { sent = true; return nil }
		m, _ = nocPress(m, "m")
		if sent || m.input != nil || m.pending != nil {
			t.Error("m on a session row must be a no-op (no editor, no overlay, no send)")
		}
		if m.actNote == "" {
			t.Error("m on the wrong node should leave a guidance note")
		}
	})
}

// --- read-only default ---------------------------------------------------

func TestControl_ReadOnlyDefaultNoMutatingSeam(t *testing.T) {
	// Plain nav / peek / filter keys must never reach a mutating seam.
	m := newControlModel(t)
	sent := false
	lifecycleCalled := false
	m.sendOp = func(act.OpMessage) error { sent = true; return nil }
	m.lifecycle = func(lifecycleOp) error { lifecycleCalled = true; return nil }

	for _, k := range []string{"j", "k", "down", "up", "right", "left", "enter", "h", "t", "g", "/"} {
		m, _ = nocPress(m, k)
	}
	if sent {
		t.Error("nav/peek/filter keys must NOT call the send seam")
	}
	if lifecycleCalled {
		t.Error("nav/peek/filter keys must NOT call the lifecycle seam")
	}
	if m.pending != nil {
		t.Error("nav/peek/filter keys must NOT open a confirm overlay")
	}
}
