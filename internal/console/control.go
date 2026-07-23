package console

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/omriariav/amq-squad/v2/internal/act"
	"github.com/omriariav/amq-squad/v2/internal/state"
)

type actionStage int

const (
	actionChoose actionStage = iota
	actionInput
	actionConfirm
	actionRunning
	actionResult
)

type controlChoice struct {
	Intent      act.Intent
	Label       string
	Available   bool
	Reason      string
	InputPrompt string
	Build       func(string) act.Action
}

type actionResultMsg struct {
	receipt act.Receipt
	err     error
}

func (m Model) controlPalette() []controlChoice {
	ctx, session, ok := m.selectedActionContext()
	if !ok {
		return nil
	}
	sel, selected := m.selectedRow()
	if !selected {
		return nil
	}
	switch sel.kind {
	case rowSession:
		return []controlChoice{{
			Intent: act.IntentBroadcast, Label: "broadcast to squad", Available: true,
			InputPrompt: "Broadcast body",
			Build: func(input string) act.Action {
				return act.Broadcast(ctx, "BROADCAST: operator", input)
			},
		}}
	case rowAgent:
		handle := strings.TrimPrefix(sel.ID, "agent:")
		return []controlChoice{{
			Intent: act.IntentMessage, Label: "message " + handle, Available: handle != "",
			Reason: "selected agent has no handle", InputPrompt: "Message body",
			Build: func(input string) act.Action {
				return act.Message(ctx, handle, "MESSAGE: operator", input)
			},
		}, {
			Intent: act.IntentBroadcast, Label: "broadcast to squad", Available: true,
			InputPrompt: "Broadcast body",
			Build: func(input string) act.Action {
				return act.Broadcast(ctx, "BROADCAST: operator", input)
			},
		}}
	case rowThread:
		threadID := strings.TrimPrefix(sel.ID, "thread:")
		thread, found := threadByID(session, threadID)
		if !found {
			return nil
		}
		var choices []controlChoice
		isGate := strings.HasPrefix(thread.ID, "gate/")
		if !isGate {
			choices = append(choices, controlChoice{
				Intent: act.IntentReply, Label: "reply on " + thread.ID,
				Available: len(nonOperatorThreadParticipants(thread, m.operatorHandle())) > 0,
				Reason:    "thread has no non-operator participant", InputPrompt: "Reply body",
				Build: func(input string) act.Action { return act.Reply(ctx, thread, m.operatorHandle(), input) },
			})
		}
		if isGate && thread.OperatorGateState == state.OperatorGateStateOpen && thread.OperatorGate != nil {
			gateAvailable := !thread.OperatorGate.Conflicted && thread.OperatorGate.SchemaOK && thread.OperatorGate.Terminalizable
			reason := ""
			switch {
			case thread.OperatorGate.Conflicted:
				reason = "gate has conflicting durable copies; inspect before answering"
			case !thread.OperatorGate.SchemaOK:
				reason = "gate request schema is degraded; inspect before answering"
			case !thread.OperatorGate.Terminalizable:
				reason = "gate request lacks exact terminal identity; inspect before answering"
			}
			choices = append(choices,
				controlChoice{
					Intent: act.IntentApprove, Label: "approve pending gate", Available: gateAvailable, Reason: reason,
					Build: func(string) act.Action { return act.Approve(ctx, thread) },
				},
				controlChoice{
					Intent: act.IntentDeny, Label: "deny pending gate", Available: gateAvailable, Reason: reason,
					InputPrompt: "Denial reason", Build: func(input string) act.Action { return act.Deny(ctx, thread, input) },
				},
			)
		}
		choices = append(choices, controlChoice{
			Intent: act.IntentBroadcast, Label: "broadcast to squad", Available: true,
			InputPrompt: "Broadcast body",
			Build: func(input string) act.Action {
				return act.Broadcast(ctx, "BROADCAST: operator", input)
			},
		})
		return choices
	default:
		return nil
	}
}

func (m Model) selectedActionContext() (act.Context, state.Session, bool) {
	sel, ok := m.selectedRow()
	if !ok {
		return act.Context{}, state.Session{}, false
	}
	var session state.Session
	var found bool
	if sel.kind == rowSession {
		session, found = sessionByKey(m.snapshot, sel.ID)
	} else {
		session, found = m.currentSession()
	}
	if !found || strings.TrimSpace(session.Name) == "" || strings.TrimSpace(session.Root) == "" {
		return act.Context{}, state.Session{}, false
	}
	return act.Context{
		Project: m.rebuild.ProjectDir,
		Profile: sessionProfile(session, true),
		Session: session.Name,
	}, session, true
}

func threadByID(session state.Session, id string) (state.ThreadSummary, bool) {
	for _, thread := range session.Coordination.Threads {
		if thread.ID == id {
			return thread, true
		}
	}
	return state.ThreadSummary{}, false
}

func (m Model) operatorHandle() string {
	handle := strings.TrimSpace(m.rebuild.Thresholds.OperatorHandle)
	if handle == "" {
		return state.DefaultOperatorHandle
	}
	return handle
}

func nonOperatorThreadParticipants(thread state.ThreadSummary, operatorHandle string) []string {
	var out []string
	for _, participant := range thread.Participants {
		if participant != operatorHandle {
			out = append(out, participant)
		}
	}
	return out
}

func (m Model) handleActionKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	choices := m.controlPalette()
	if m.actionIndex < 0 {
		m.actionIndex = 0
	}
	if len(choices) > 0 && m.actionIndex >= len(choices) {
		m.actionIndex = len(choices) - 1
	}

	switch m.actionStage {
	case actionChoose:
		switch key {
		case "esc", "q":
			return m.closeActionOverlay(), nil
		case "j", "down":
			if len(choices) > 0 {
				m.actionIndex = (m.actionIndex + 1) % len(choices)
			}
			return m, nil
		case "k", "up":
			if len(choices) > 0 {
				m.actionIndex = (m.actionIndex - 1 + len(choices)) % len(choices)
			}
			return m, nil
		case "enter":
			if len(choices) == 0 {
				return m, nil
			}
			choice := choices[m.actionIndex]
			if !choice.Available {
				m.actionErr = fmt.Errorf("%s", choice.Reason)
				return m, nil
			}
			m.actionErr = nil
			m.actionReceipt = act.Receipt{}
			if choice.InputPrompt != "" {
				m.actionStage = actionInput
				m.actionInput = ""
				return m, nil
			}
			m.pendingAction = choice.Build("")
			m.actionStage = actionConfirm
			return m, nil
		}
	case actionInput:
		switch msg.Type {
		case tea.KeyEsc:
			m.actionStage = actionChoose
			m.actionInput = ""
			return m, nil
		case tea.KeyEnter:
			if strings.TrimSpace(m.actionInput) == "" || len(choices) == 0 {
				return m, nil
			}
			m.pendingAction = choices[m.actionIndex].Build(m.actionInput)
			m.actionStage = actionConfirm
			return m, nil
		case tea.KeyBackspace, tea.KeyDelete:
			runes := []rune(m.actionInput)
			if len(runes) > 0 {
				m.actionInput = string(runes[:len(runes)-1])
			}
			return m, nil
		case tea.KeyRunes, tea.KeySpace:
			m.actionInput += string(msg.Runes)
			if msg.Type == tea.KeySpace && len(msg.Runes) == 0 {
				m.actionInput += " "
			}
			return m, nil
		}
	case actionConfirm:
		switch key {
		case "esc", "n":
			m.pendingAction = act.Action{}
			m.actionStage = actionChoose
			return m, nil
		case "y":
			runner := m.actionRunner
			if runner == nil {
				runner = act.Send
			}
			action := m.pendingAction
			m.actionStage = actionRunning
			return m, func() tea.Msg {
				receipt, err := runner(action)
				return actionResultMsg{receipt: receipt, err: err}
			}
		default:
			return m, nil
		}
	case actionRunning:
		// The subprocess owns a crash-visible durable attempt receipt. Do not let
		// navigation imply cancellation while it is resolving the final state.
		return m, nil
	case actionResult:
		switch key {
		case "enter", "esc", "q":
			return m.closeActionOverlay(), rebuildCmd(m.rebuild, true)
		default:
			return m, nil
		}
	}
	return m, nil
}

func (m Model) closeActionOverlay() Model {
	m.overlay = overlayNone
	m.actionStage = actionChoose
	m.actionIndex = 0
	m.actionInput = ""
	m.pendingAction = act.Action{}
	m.actionReceipt = act.Receipt{}
	m.actionErr = nil
	return m.afterNav()
}

func (m Model) renderControlActions() string {
	var b strings.Builder
	switch m.actionStage {
	case actionChoose:
		b.WriteString(styleHeader.Render("actions — select") + "\n")
		b.WriteString(styleFaint.Render("read-only until you preview and explicitly confirm an AMQ write") + "\n\n")
		choices := m.controlPalette()
		if len(choices) == 0 {
			b.WriteString("  (no operator actions for this selection)\n")
		}
		for i, choice := range choices {
			cursor := "  "
			if i == m.actionIndex {
				cursor = "› "
			}
			status := ""
			if !choice.Available {
				status = " — unavailable: " + choice.Reason
			}
			fmt.Fprintf(&b, "%s%-10s %s%s\n", cursor, choice.Intent, choice.Label, status)
		}
		if m.actionErr != nil {
			b.WriteString("\n" + styleErr.Render(m.actionErr.Error()) + "\n")
		}
		b.WriteString("\n" + styleFaint.Render("j/k choose · enter stage · esc cancel") + "\n")
	case actionInput:
		choices := m.controlPalette()
		prompt := "Input"
		if len(choices) > 0 && m.actionIndex < len(choices) {
			prompt = choices[m.actionIndex].InputPrompt
		}
		b.WriteString(styleHeader.Render(prompt) + "\n\n")
		b.WriteString("> " + m.actionInput + "█\n\n")
		b.WriteString(styleFaint.Render("enter previews · esc cancels; nothing has been sent") + "\n")
	case actionConfirm:
		b.WriteString(styleHeader.Render("CONFIRM AMQ WRITE") + "\n")
		b.WriteString(styleFaint.Render("review the exact command; only y executes it") + "\n\n")
		width := m.width
		if width <= 0 {
			width = 80
		}
		b.WriteString(wrapLine(act.Preview(m.pendingAction), width) + "\n\n")
		b.WriteString("Press y to send · n/esc to cancel\n")
	case actionRunning:
		b.WriteString(styleHeader.Render("sending…") + "\n")
		b.WriteString(styleFaint.Render("waiting for the stable AMQ id and durable receipt") + "\n")
	case actionResult:
		if m.actionErr != nil {
			b.WriteString(styleHeader.Render("action failed") + "\n\n")
			b.WriteString(styleErr.Render(m.actionErr.Error()) + "\n")
		} else {
			b.WriteString(styleHeader.Render("action sent") + "\n\n")
			fmt.Fprintf(&b, "message: %s\nthread: %s\nattempt: %s\nstate: %s\nreceipt: %s\n",
				m.actionReceipt.MessageID, m.actionReceipt.Thread, m.actionReceipt.AttemptID,
				m.actionReceipt.DeliveryState, m.actionReceipt.Path)
		}
		b.WriteString("\n" + styleFaint.Render("enter/esc closes and refreshes") + "\n")
	}
	return b.String()
}
