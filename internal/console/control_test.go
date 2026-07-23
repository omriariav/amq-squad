package console

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/omriariav/amq-squad/v2/internal/act"
	"github.com/omriariav/amq-squad/v2/internal/state"
)

func controlBoardModel() Model {
	gate := state.ThreadSummary{
		ID: "gate/release", Subject: "APPROVAL: release",
		Participants:      []string{"cto", "user"},
		Status:            state.ThreadAwaitingReply,
		Triage:            state.TriageNeedsYou,
		OperatorGateState: state.OperatorGateStateOpen,
		OperatorGate: &state.OperatorGateSignal{
			LatestID: "gate-request", From: "cto", Subject: "APPROVAL: release",
			Kind: state.KindQuestion, SchemaOK: true, Terminalizable: true,
		},
	}
	ordinary := state.ThreadSummary{
		ID: "p2p/cto__user", Subject: "Status update",
		Participants: []string{"cto", "user"}, Status: state.ThreadOpen,
	}
	session := state.Session{
		Name: "issue-515", Root: "/base/issue-515",
		Agents:       []state.Agent{{Handle: "cto", Role: "cto", TeamProfile: "review", Liveness: state.LivenessAlive}},
		Coordination: state.Coordination{Threads: []state.ThreadSummary{gate, ordinary}},
		Rollup:       state.TriageRollup{NeedsYou: 1, Gated: 1},
	}
	m := newModel(
		rebuildConfig{ProjectDir: "/Code/app", BaseRoot: "/base"},
		state.Snapshot{BaseRoot: "/base", Sessions: []state.Session{session}, Rollup: session.Rollup},
		"",
	)
	return m.reselect()
}

func confirmedAction(t *testing.T, m Model, called *int, captured *act.Action) Model {
	t.Helper()
	m.actionRunner = func(action act.Action) (act.Receipt, error) {
		(*called)++
		*captured = action
		return act.Receipt{
			MessageID: "msg-515", Thread: action.Thread, AttemptID: "attempt-515",
			DeliveryState: "sent", Path: "/tmp/receipt-515.json",
		}, nil
	}
	next, cmd := m.Update(keyMsg("y"))
	m = next.(Model)
	if cmd == nil {
		t.Fatal("explicit y at confirm stage should return an action command")
	}
	if *called != 0 {
		t.Fatal("runner executed synchronously before Bubble Tea ran the command")
	}
	result := cmd()
	if *called != 1 {
		t.Fatalf("runner calls = %d, want 1", *called)
	}
	next, _ = m.Update(result)
	return next.(Model)
}

func TestActionOverlayRequiresDedicatedConfirmationKey(t *testing.T) {
	m := controlBoardModel()
	called := 0
	m.actionRunner = func(action act.Action) (act.Receipt, error) {
		called++
		return act.Receipt{}, nil
	}

	m = press(t, m, "a")
	if called != 0 || m.actionStage != actionChoose {
		t.Fatalf("opening palette mutated: called=%d stage=%d", called, m.actionStage)
	}
	m = press(t, m, "enter", "maintenance now", "enter")
	if m.actionStage != actionConfirm {
		t.Fatalf("staged broadcast did not reach confirm: %d", m.actionStage)
	}
	next, cmd := m.Update(keyMsg("enter"))
	m = next.(Model)
	if cmd != nil || called != 0 || m.actionStage != actionConfirm {
		t.Fatalf("enter at confirm must be inert: cmd=%v called=%d stage=%d", cmd != nil, called, m.actionStage)
	}
	m = press(t, m, "n")
	if called != 0 || m.actionStage != actionChoose {
		t.Fatalf("n cancellation mutated: called=%d stage=%d", called, m.actionStage)
	}
}

func TestConsoleConfirmFlowsProduceTypedActIntents(t *testing.T) {
	tests := []struct {
		name   string
		stage  func(*testing.T, Model) Model
		intent act.Intent
	}{
		{
			name: "broadcast",
			stage: func(t *testing.T, m Model) Model {
				return press(t, m, "a", "enter", "all hands", "enter")
			},
			intent: act.IntentBroadcast,
		},
		{
			name: "message",
			stage: func(t *testing.T, m Model) Model {
				return press(t, m, "enter", "a", "enter", "please review", "enter")
			},
			intent: act.IntentMessage,
		},
		{
			name: "reply",
			stage: func(t *testing.T, m Model) Model {
				return press(t, m, "enter", "j", "j", "a", "enter", "answer body", "enter")
			},
			intent: act.IntentReply,
		},
		{
			name: "approve",
			stage: func(t *testing.T, m Model) Model {
				return press(t, m, "enter", "j", "a", "enter")
			},
			intent: act.IntentApprove,
		},
		{
			name: "deny",
			stage: func(t *testing.T, m Model) Model {
				return press(t, m, "enter", "j", "a", "j", "enter", "unsafe target", "enter")
			},
			intent: act.IntentDeny,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := tc.stage(t, controlBoardModel())
			if m.actionStage != actionConfirm {
				t.Fatalf("stage = %d, want confirm; view:\n%s", m.actionStage, m.View())
			}
			preview := act.Preview(m.pendingAction)
			if !strings.Contains(preview, "--json") {
				t.Fatalf("preview lacks receipted JSON command: %s", preview)
			}
			called := 0
			var captured act.Action
			m = confirmedAction(t, m, &called, &captured)
			if captured.Intent != tc.intent {
				t.Fatalf("intent = %q, want %q", captured.Intent, tc.intent)
			}
			if m.actionStage != actionResult || m.actionReceipt.MessageID != "msg-515" {
				t.Fatalf("result stage/receipt = %d %+v", m.actionStage, m.actionReceipt)
			}
			if !strings.Contains(m.View(), "receipt-515.json") {
				t.Fatalf("result view lacks durable receipt:\n%s", m.View())
			}
		})
	}
}

func TestConsoleConflictedGateCannotReachConfirm(t *testing.T) {
	m := controlBoardModel()
	m.snapshot.Sessions[0].Coordination.Threads[0].OperatorGate.Conflicted = true
	m = press(t, m, "enter", "j", "a", "enter")
	if m.actionStage != actionChoose {
		t.Fatalf("conflicted gate reached stage %d", m.actionStage)
	}
	if m.actionErr == nil || !strings.Contains(m.actionErr.Error(), "conflicting") {
		t.Fatalf("conflicted gate error = %v", m.actionErr)
	}
}

func TestConsoleReplyUsesConfiguredOperatorHandle(t *testing.T) {
	m := controlBoardModel()
	m.rebuild.Thresholds.OperatorHandle = "ops"
	m.snapshot.Sessions[0].Coordination.Threads[1].Participants = []string{"cto", "ops"}
	m = press(t, m, "enter", "j", "j", "a", "enter", "answer body", "enter")
	if m.actionStage != actionConfirm {
		t.Fatalf("stage = %d, want confirm; view:\n%s", m.actionStage, m.View())
	}
	if m.pendingAction.To != "cto" {
		t.Fatalf("reply recipients = %q, want cto", m.pendingAction.To)
	}
}

func TestActionResultMessageNeverMutatesSnapshot(t *testing.T) {
	m := controlBoardModel()
	before := m.Snapshot()
	m.overlay = overlayActions
	m.actionStage = actionRunning
	next, cmd := m.Update(actionResultMsg{receipt: act.Receipt{MessageID: "m", AttemptID: "a", Path: "/r"}})
	if cmd != nil {
		t.Fatal("receipt reduction should not start another command")
	}
	got := next.(Model)
	if len(got.Snapshot().Sessions) != len(before.Sessions) || got.actionStage != actionResult {
		t.Fatalf("receipt reduction mutated snapshot/stage: %+v", got)
	}
}

var _ tea.Model = Model{}
