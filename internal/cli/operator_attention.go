package cli

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/attention"
	squadnamespace "github.com/omriariav/amq-squad/v3/internal/namespace"
	"github.com/omriariav/amq-squad/v3/internal/operatorauth"
	"github.com/omriariav/amq-squad/v3/internal/state"
	"github.com/omriariav/amq-squad/v3/internal/team"
)

type operatorAttention struct {
	EventType      string           `json:"event_type,omitempty"`
	Key            string           `json:"key"`
	Profile        string           `json:"profile"`
	Session        string           `json:"session"`
	NamespaceID    string           `json:"namespace_id"`
	Thread         string           `json:"thread"`
	LatestID       string           `json:"latest_id"`
	From           string           `json:"from,omitempty"`
	Subject        string           `json:"subject"`
	Kind           state.Kind       `json:"kind"`
	Reason         state.AttnReason `json:"reason"`
	Age            string           `json:"age"`
	LastEventAt    time.Time        `json:"last_event_at,omitempty"`
	Escalation     string           `json:"escalation,omitempty"`
	Inspect        string           `json:"inspect"`
	Respond        string           `json:"respond"`
	Role           string           `json:"role,omitempty"`
	GateKind       string           `json:"gate_kind,omitempty"`
	Actor          string           `json:"actor,omitempty"`
	PolicyRevision int64            `json:"policy_revision,omitempty"`
	Summary        string           `json:"summary,omitempty"`
	Actionable     bool             `json:"actionable"`
	Answerable     bool             `json:"answerable"`
	Unread         bool             `json:"-"`
	Cleared        bool             `json:"-"`
}

func collectOperatorAttention(projectDir, profile string, snap state.Snapshot, operatorHandle, onlySession string, now time.Time) []operatorAttention {
	var out []operatorAttention
	profile = squadnamespace.NormalizeProfile(profile)
	for _, sess := range snap.Sessions {
		if !squadnamespace.ProfilesEqual(profile, sess.TeamProfile) {
			continue
		}
		if onlySession != "" && sess.Name != onlySession {
			continue
		}
		for _, th := range sess.Coordination.NeedsYouThreads() {
			if th.Historical {
				continue
			}
			if !notifyStructuralOperatorAttention(th, operatorHandle) {
				continue
			}
			if !notifyThreadMatchesSessionAgents(th, operatorHandle, sess.Agents) {
				continue
			}
			age := now.Sub(th.LastEventAt)
			if age < 0 {
				age = 0
			}
			from := firstNonOperatorParticipant(th, operatorHandle)
			eventType := "surface"
			if strings.HasPrefix(th.ID, "gate/") {
				eventType = "gate"
			}
			answerable := eventType == "gate"
			item := operatorAttention{
				EventType:   eventType,
				Key:         notifyKey(profile, sess.Name, th.ID),
				Profile:     profile,
				Session:     sess.Name,
				NamespaceID: squadnamespace.ID(profile, sess.Name),
				Thread:      th.ID,
				LatestID:    th.LatestID,
				From:        from,
				Subject:     th.Subject,
				Kind:        th.Kind,
				Reason:      th.AttnReason,
				Age:         roundDuration(age).String(),
				LastEventAt: th.LastEventAt,
				Inspect:     notifyInspectCommand(projectDir, profile, sess.Name, th.ID),
				Respond:     notifyRespondCommand(operatorHandle, from, th.ID, th.AttnReason),
				Actionable:  true,
				Answerable:  answerable,
			}
			applyThreadOperatorGateAttention(&item, th)
			item.Respond = notifyRespondCommand(operatorHandle, item.From, th.ID, item.Reason)
			out = append(out, item)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		ri, rj := out[i].Reason.Rank(), out[j].Reason.Rank()
		if ri != rj {
			return ri < rj
		}
		if !out[i].LastEventAt.Equal(out[j].LastEventAt) {
			return out[i].LastEventAt.Before(out[j].LastEventAt)
		}
		if out[i].Session != out[j].Session {
			return out[i].Session < out[j].Session
		}
		return out[i].Thread < out[j].Thread
	})
	return out
}

// collectGateAttentionProjectionCaptured is the authoritative gate-open
// projection for operator status. (2b-1: the squad attention queue of the
// status contract is internal/status; this stays the operator's gate view.)
func collectGateAttentionProjectionCaptured(t team.Team, projectDir, profile string, snap state.Snapshot, operatorHandle, onlySession string, now time.Time, captures map[string]operatorSessionCapture) []operatorAttention {
	var out []operatorAttention
	profile = squadnamespace.NormalizeProfile(profile)
	for _, sess := range snap.Sessions {
		if !squadnamespace.ProfilesEqual(profile, sess.TeamProfile) {
			continue
		}
		if onlySession != "" && sess.Name != onlySession {
			continue
		}
		capture, captured := captures[sess.Name]
		msgs, warnings := capture.Messages, capture.Warnings
		if !captured {
			msgs, warnings = scanOperatorSessionMessages(sess.Root, func() time.Time { return now })
		}
		byThread := map[string][]state.Message{}
		for _, msg := range msgs {
			if strings.HasPrefix(msg.Thread, "gate/") {
				byThread[msg.Thread] = append(byThread[msg.Thread], msg)
			}
		}
		for thread, threadMsgs := range byThread {
			threadMsgs, conflict := dedupeSecurityMessages(threadMsgs)
			sort.SliceStable(threadMsgs, func(i, j int) bool { return messageAfter(threadMsgs[j], threadMsgs[i]) })
			lifecycleMessages := make([]state.Message, 0, len(threadMsgs))
			for _, msg := range threadMsgs {
				if msg.From == operatorHandle || sessionHasAgentHandle(sess.Agents, msg.From) {
					lifecycleMessages = append(lifecycleMessages, msg)
				}
			}
			gateState, gate := state.ResolveOperatorGate(lifecycleMessages, operatorHandle, now)
			question := latestGateQuestionCandidate(threadMsgs, thread)
			if question == nil || !question.AuthorizationRequestPresent {
				// Projection-only legacy observability: review_request/decision records
				// without typed context remain visible, but can never become an
				// authorization candidate. A typed question, even malformed, remains
				// the barrier and cannot be displaced by a later non-question.
				var legacy *state.Message
				for i := range threadMsgs {
					msg := threadMsgs[i]
					if gateState == state.OperatorGateStateOpen && gate != nil && msg.ID != gate.LatestID {
						continue
					}
					if (msg.Kind == state.KindQuestion || msg.Kind == state.KindReviewRequest || msg.Kind == state.KindDecision) && !msg.AuthorizationRequestPresent && rawOperatorGateRequestMessage(msg, operatorHandle, sess.Agents) && (legacy == nil || messageAfter(msg, *legacy)) {
						copy := msg
						legacy = &copy
					}
				}
				question = legacy
			}
			if question == nil {
				continue
			}
			item := operatorAttention{
				EventType:   "gate",
				Key:         notifyKey(profile, sess.Name, thread),
				Profile:     profile,
				Session:     sess.Name,
				NamespaceID: sess.NamespaceID,
				Thread:      thread,
				LatestID:    question.ID,
				From:        question.From,
				Subject:     question.Subject,
				Kind:        question.Kind,
				Reason:      state.ClassifyAttnSubject(question.Subject),
				LastEventAt: question.Created,
				Inspect:     notifyInspectCommand(projectDir, profile, sess.Name, thread),
				Respond:     notifyRespondCommand(operatorHandle, question.From, thread, state.ClassifyAttnSubject(question.Subject)),
				Actionable:  true,
				Answerable:  true,
			}
			age := now.Sub(question.Created)
			if age < 0 {
				age = 0
			}
			item.Age = roundDuration(age).String()
			item.Escalation = string(state.OperatorGateEscalationForAge(age))

			if conflict {
				item.Summary = "duplicate_message_conflict"
				item.Answerable = false
				item.Respond = ""
				out = append(out, item)
				continue
			}
			if len(warnings) > 0 {
				item.Summary = "message_scan_degraded"
				item.Answerable = false
				item.Respond = ""
				out = append(out, item)
				continue
			}
			if gateState == state.OperatorGateStateClosed || gateState == state.OperatorGateStateWithdrawn {
				item.Cleared = true
				item.Actionable = false
				item.Answerable = false
				item.Respond = ""
				out = append(out, item)
				continue
			}
			if !question.AuthorizationRequestPresent {
				closed := gateState == state.OperatorGateStateAnswered
				item.Cleared = closed
				item.Actionable = !closed
				item.Answerable = !closed
				if closed {
					item.Respond = ""
				}
				out = append(out, item)
				continue
			}

			questionEvidence := resolveTypedHumanEvidence(projectDir, profile, sess.Name, thread, operatorHandle, t, *question, nil)
			if questionEvidence.Reason != "gate_pending" {
				item.Summary = questionEvidence.Reason
				item.Answerable = false
				item.Respond = ""
				out = append(out, item)
				continue
			}
			facts := make([]operatorauth.MessageFact, 0)
			reasons := map[string]string{}
			for i := range threadMsgs {
				msg := threadMsgs[i]
				if msg.From != operatorHandle || !messageAfter(msg, *question) {
					continue
				}
				evidence := resolveTypedHumanEvidence(projectDir, profile, sess.Name, thread, operatorHandle, t, *question, &msg)
				bound := evidence.Disposition == actionDecisionApproved || evidence.Disposition == actionDecisionDenied
				reasons[msg.ID] = evidence.Reason
				facts = append(facts, operatorauth.MessageFact{ID: msg.ID, From: msg.From, Kind: string(msg.Kind), Decision: evidence.Disposition, Bound: bound, After: true, Order: int64(i + 1)})
			}
			precedence := operatorauth.ResolvePrecedence(facts, operatorHandle, "")
			switch {
			case precedence.Decision == actionDecisionApproved || precedence.Decision == actionDecisionDenied:
				item.Cleared = true
				item.Actionable = false
				item.Answerable = false
				item.Respond = ""
				item.LatestID = precedence.MessageID
			case precedence.Barrier:
				item.LatestID = precedence.MessageID
				item.Summary = reasons[precedence.MessageID]
				if item.Summary == "" {
					item.Summary = "human_intervention_pending"
				}
			default:
				item.Summary = "gate_pending"
			}
			out = append(out, item)
		}
	}
	sortOperatorAttention(out)
	return out
}

func applyThreadOperatorGateAttention(item *operatorAttention, th state.ThreadSummary) {
	if item == nil || th.OperatorGate == nil {
		return
	}
	gate := th.OperatorGate
	item.LatestID = gate.LatestID
	item.From = gate.From
	item.Subject = gate.Subject
	item.Kind = gate.Kind
	item.Reason = gate.Reason
	item.Age = roundDuration(gate.Age).String()
	item.LastEventAt = gate.Since
	item.Escalation = string(gate.Escalation)
}

func rawOperatorGateRequestMessage(msg state.Message, operatorHandle string, agents []state.Agent) bool {
	return operatorMessageToContains(msg, operatorHandle) && sessionHasAgentHandle(agents, msg.From)
}

func mergeOperatorAttention(base, extra []operatorAttention) []operatorAttention {
	if len(extra) == 0 {
		return base
	}
	merged := make([]operatorAttention, 0, len(base)+len(extra))
	index := map[string]int{}
	for _, item := range base {
		index[item.Key] = len(merged)
		merged = append(merged, item)
	}
	for _, item := range extra {
		if i, ok := index[item.Key]; ok {
			// Conflicting projections fail open: an active observation always
			// beats a cleared tombstone, independent of merge order.
			if merged[i].Cleared && !item.Cleared {
				merged[i] = item
			} else if !merged[i].Cleared && item.Cleared {
				continue
			} else {
				merged[i] = item
			}
			continue
		}
		index[item.Key] = len(merged)
		merged = append(merged, item)
	}
	sortOperatorAttention(merged)
	return merged
}

func sortOperatorAttention(items []operatorAttention) {
	sort.SliceStable(items, func(i, j int) bool {
		ri, rj := items[i].Reason.Rank(), items[j].Reason.Rank()
		if ri != rj {
			return ri < rj
		}
		if !items[i].LastEventAt.Equal(items[j].LastEventAt) {
			return items[i].LastEventAt.Before(items[j].LastEventAt)
		}
		if items[i].Session != items[j].Session {
			return items[i].Session < items[j].Session
		}
		return items[i].Thread < items[j].Thread
	})
}

func operatorMessageToContains(msg state.Message, handle string) bool {
	for _, to := range msg.To {
		if to == handle {
			return true
		}
	}
	return false
}

func sessionHasAgentHandle(agents []state.Agent, handle string) bool {
	handle = strings.TrimSpace(handle)
	if handle == "" {
		return false
	}
	for _, agent := range agents {
		if agent.Handle == handle {
			return true
		}
	}
	return false
}

func notifyStructuralOperatorAttention(th state.ThreadSummary, operatorHandle string) bool {
	if !notifyUnreadBy(th, operatorHandle) {
		return false
	}
	if strings.HasPrefix(th.ID, "gate/") {
		return true
	}
	switch th.Kind {
	case state.KindQuestion, state.KindDecision:
		return true
	default:
		return false
	}
}

func notifyThreadMatchesSessionAgents(th state.ThreadSummary, operatorHandle string, agents []state.Agent) bool {
	handles := map[string]bool{}
	for _, a := range agents {
		if h := strings.TrimSpace(a.Handle); h != "" {
			handles[h] = true
		}
	}
	for _, p := range th.Participants {
		p = strings.TrimSpace(p)
		if p == "" || p == operatorHandle {
			continue
		}
		if handles[p] {
			return true
		}
	}
	return false
}

func notifyUnreadBy(th state.ThreadSummary, handle string) bool {
	for _, unread := range th.UnreadBy {
		if unread == handle {
			return true
		}
	}
	return false
}

func notifyKey(profile, session, thread string) string {
	return attention.GateKey(squadnamespace.NormalizeProfile(profile), session, thread)
}

func notifyInspectCommand(projectDir, profile, session, thread string) string {
	profile = squadnamespace.NormalizeProfile(profile)
	profileArg := ""
	if profile != team.DefaultProfile {
		profileArg = " --profile " + notifyShellQuote(profile)
	}
	return fmt.Sprintf("amq-squad operator status --project %s%s --session %s --json", notifyShellQuote(projectDir), profileArg, notifyShellQuote(session))
}

func notifyRespondCommand(operatorHandle, to, thread string, reason state.AttnReason) string {
	if strings.TrimSpace(to) == "" {
		to = "<agent-handle>"
	}
	subject := "ANSWER: <response>"
	if reason == state.AttnApprove {
		subject = "APPROVED: <decision>"
	}
	return fmt.Sprintf("amq send --me %s --to %s --thread %s --kind answer --subject %s",
		notifyShellQuote(operatorHandle), notifyShellQuote(to), notifyShellQuote(thread), notifyShellQuote(subject))
}

func firstNonOperatorParticipant(th state.ThreadSummary, operatorHandle string) string {
	for _, p := range th.Participants {
		if p != operatorHandle {
			return p
		}
	}
	return ""
}

func roundDuration(d time.Duration) time.Duration {
	if d < time.Minute {
		return d.Round(time.Second)
	}
	return d.Round(time.Minute)
}

func notifyShellQuote(s string) string {
	if s == "" {
		return "''"
	}
	if strings.ContainsAny(s, " \t\n'\"\\$`!*?[]{}()<>|&;") {
		return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
	}
	return s
}
