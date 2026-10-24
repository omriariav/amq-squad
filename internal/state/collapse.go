package state

import (
	"encoding/json"
	"sort"
	"strings"
	"time"
)

// Coordination is a session's derived read-only model: threads (with triage)
// and scan warnings, built from scanned messages, an injected clock and thresholds.
type Coordination struct {
	Threads  []ThreadSummary
	Warnings []Warning
}

// collapseInput is every message in the session's inboxes (deduplicated by
// msgid, owner, state) plus agent liveness for the heartbeat/at-risk signals.
type collapseInput struct {
	messages []Message
	agents   []Agent
	warnings []Warning
}

// ProjectCoordination collapses an already-scanned (and filtered) message set
// with the same semantics as Snapshot construction; it performs no I/O.
func ProjectCoordination(messages []Message, agents []Agent, warnings []Warning, now time.Time, th Thresholds) Coordination {
	return buildCoordination(collapseInput{messages: messages, agents: agents, warnings: warnings}, now, th)
}

// buildCoordination collapses messages into triaged threads; it reads no clock or disk.
func buildCoordination(in collapseInput, now time.Time, th Thresholds) Coordination {
	th = withThresholdDefaults(th)

	return Coordination{
		Threads:  collapseThreads(in.messages, now, th, in.agents),
		Warnings: in.warnings,
	}
}

// threadAccumulator gathers everything about one canonical thread as messages
// stream in, so the final summary is a single pass.
type threadAccumulator struct {
	id           string
	participants map[string]bool
	subject      string
	lastKind     Kind
	lastEventAt  time.Time
	count        int
	// unreadBy: recipient handle -> still has an unread copy (inbox/new).
	unreadBy map[string]bool
	// readBy: recipient handle -> has a read copy (inbox/cur). Used so a later
	// read supersedes an earlier unread for the same recipient.
	readBy      map[string]bool
	latest      Message
	hasLatest   bool
	blockActive bool
	blockOwner  string
	messages    []Message
}

// collapseThreads summarizes each canonical thread: participants, latest
// subject/kind, status, unread-by, triage and freshness.
func collapseThreads(msgs []Message, now time.Time, th Thresholds, agents []Agent) []ThreadSummary {
	accs := map[string]*threadAccumulator{}
	var order []string

	for _, m := range msgs {
		acc, ok := accs[m.Thread]
		if !ok {
			acc = &threadAccumulator{
				id:           m.Thread,
				participants: map[string]bool{},
				unreadBy:     map[string]bool{},
				readBy:       map[string]bool{},
			}
			accs[m.Thread] = acc
			order = append(order, m.Thread)
		}
		acc.observe(m)
	}

	sort.Strings(order)
	out := make([]ThreadSummary, 0, len(order))
	for _, id := range order {
		out = append(out, accs[id].summarize(now, th, agents))
	}
	return out
}

func (a *threadAccumulator) observe(m Message) {
	a.count++
	a.messages = append(a.messages, m)
	if m.From != "" {
		a.participants[m.From] = true
	}
	for _, r := range m.To {
		a.participants[r] = true
	}

	// unread-by is per RECIPIENT, decided by where the recipient's own copy
	// sits. A copy in inbox/new => that owner has it unread; a copy in inbox/cur
	// => read. Only the OWNER of an inbox is informative about that owner.
	if m.Owner != "" {
		switch m.State {
		case MailboxNew:
			if !a.readBy[m.Owner] {
				a.unreadBy[m.Owner] = true
			}
		case MailboxCur:
			a.readBy[m.Owner] = true
			delete(a.unreadBy, m.Owner)
		}
	}

	// Track latest by created time (messages arrive sorted, but be defensive).
	if !a.hasLatest || !m.Created.Before(a.lastEventAt) {
		a.latest = m
		a.hasLatest = true
		a.lastEventAt = m.Created
		a.lastKind = m.Kind
		if m.Subject != "" {
			a.subject = m.Subject
		}
	} else if a.subject == "" && m.Subject != "" {
		a.subject = m.Subject
	}

	// Block tracking: a declared block stays active until a later message
	// clears it.
	if declaresBlock(m) {
		a.blockActive = true
		a.blockOwner = primaryRecipient(m)
	} else if a.blockActive && clearsBlock(m) {
		a.blockActive = false
	}
}

func (a *threadAccumulator) summarize(now time.Time, th Thresholds, agents []Agent) ThreadSummary {
	parts := keysSorted(a.participants)
	unread := keysSorted(a.unreadBy)
	operatorGateState, operatorGate := ResolveOperatorGate(a.messages, th.OperatorHandle, now)

	status := deriveStatus(a)
	fresh := computeFreshness(a.lastEventAt, now, governingThreshold(status, th))
	triage := computeTriage(a, status, fresh, now, th, agents, operatorGate)
	reason := classifyAttnReason(a, triage)
	if operatorGate != nil && triage == TriageNeedsYou {
		reason = operatorGate.Reason
	}
	historical := isHistoricalNeedsYou(triage, fresh, agents, th.OperatorHandle)

	return ThreadSummary{
		ID:                a.id,
		LatestID:          a.latest.ID,
		Participants:      parts,
		Subject:           a.subject,
		Kind:              a.lastKind,
		Status:            status,
		LastEventAt:       a.lastEventAt,
		MessageCount:      a.count,
		UnreadBy:          unread,
		Triage:            triage,
		Freshness:         fresh,
		Historical:        historical,
		AttnReason:        reason,
		OperatorGateState: operatorGateState,
		OperatorGate:      operatorGate,
	}
}

// ResolveOperatorGate replays one gate thread chronologically and returns its
// lifecycle state plus an escalation signal only while it remains open.
func ResolveOperatorGate(input []Message, operatorHandle string, now time.Time) (OperatorGateState, *OperatorGateSignal) {
	messages, conflictedRequests := gateMessagesWithoutConflictingDuplicates(input, operatorHandle)
	sort.SliceStable(messages, func(i, j int) bool {
		if !messages[i].Created.Equal(messages[j].Created) {
			return messages[i].Created.Before(messages[j].Created)
		}
		return messages[i].ID < messages[j].ID
	})
	var pending Message
	pendingConflicted := false
	pendingTerminalizable := false
	requestGeneration := 0
	gateState := OperatorGateStateUnknown
	for _, m := range messages {
		if !pendingConflicted && pendingTerminalizable {
			if terminal, ok := operatorGateTerminalStateForPending(m, pending, operatorHandle, requestGeneration > 1); ok {
				pending = Message{}
				pendingConflicted = false
				pendingTerminalizable = false
				gateState = terminal
				continue
			}
		}
		if strings.HasPrefix(m.Thread, "gate/") && operatorGateRequestMessage(m, operatorHandle) {
			requestGeneration++
			pending = m
			pendingConflicted = conflictedRequests[m.ID]
			pendingTerminalizable = operatorGateRequestTerminalizable(m) && !pendingConflicted
			gateState = OperatorGateStateOpen
		}
	}
	if pending.ID == "" {
		return gateState, nil
	}
	age := now.Sub(pending.Created)
	if age < 0 {
		age = 0
	}
	return OperatorGateStateOpen, &OperatorGateSignal{
		LatestID:       pending.ID,
		From:           pending.From,
		RawTo:          append([]string{}, pending.RawTo...),
		ToPresent:      pending.ToPresent,
		ToArrayValid:   pending.ToArrayValid,
		ToRaw:          pending.ToRaw,
		Subject:        pending.Subject,
		Kind:           pending.Kind,
		Since:          pending.Created,
		Age:            age,
		Reason:         ClassifyAttnSubject(pending.Subject),
		Escalation:     OperatorGateEscalationForAge(age),
		Conflicted:     pendingConflicted,
		SchemaOK:       pending.SchemaOK,
		RawThread:      pending.RawThread,
		Refs:           append([]string{}, pending.Refs...),
		RefsPresent:    pending.RefsPresent,
		RefsValid:      pending.RefsValid,
		RefsRaw:        pending.RefsRaw,
		Terminalizable: pendingTerminalizable,
	}
}

func operatorGateRequestTerminalizable(m Message) bool {
	return m.SchemaOK && m.ID != "" && m.ID == strings.TrimSpace(m.ID) && m.RawThread != "" && m.RawThread == m.Thread
}

// operatorGateTerminalStateForPending recognizes terminal lifecycle events
// only when they bind to the request that is currently pending. That binding
// prevents a delayed close for an older request from closing a later re-raise.
func operatorGateTerminalStateForPending(m, pending Message, operatorHandle string, requireAnswerBinding bool) (OperatorGateState, bool) {
	if pending.ID == "" || m.ID == "" || !strings.HasPrefix(m.Thread, "gate/") || m.Thread != pending.Thread {
		return OperatorGateStateUnknown, false
	}
	if m.RawThread != "" && m.RawThread != pending.Thread {
		return OperatorGateStateUnknown, false
	}
	if m.Kind == KindAnswer && m.From == operatorHandle && addressedTo(m, pending.From) {
		if m.ReplyTo != "" && m.ReplyTo != pending.ID {
			return OperatorGateStateUnknown, false
		}
		if m.ApprovalPresent && (!m.ApprovalValid || m.Approval == nil || m.Approval.QuestionMessageID != pending.ID) {
			return OperatorGateStateUnknown, false
		}
		if requireAnswerBinding && m.ReplyTo != pending.ID && !m.ApprovalPresent {
			return OperatorGateStateUnknown, false
		}
		return OperatorGateStateAnswered, true
	}
	if m.Kind != KindStatus || !m.SchemaOK || m.RawThread == "" || m.RawThread != pending.Thread || !operatorGateTerminalBindingMatches(m, pending.ID) {
		return OperatorGateStateUnknown, false
	}
	if m.From != pending.From || !addressedTo(m, operatorHandle) {
		return OperatorGateStateUnknown, false
	}
	ctx, ok := operatorGateTerminalContext(m)
	if !ok || ctx.RequestMessageID != pending.ID || ctx.Requester != pending.From || ctx.Thread != pending.Thread || ctx.Actor != m.From {
		return OperatorGateStateUnknown, false
	}
	switch ctx.State {
	case OperatorGateStateClosed:
		return OperatorGateStateClosed, true
	case OperatorGateStateWithdrawn:
		return OperatorGateStateWithdrawn, true
	default:
		return OperatorGateStateUnknown, false
	}
}

func operatorGateTerminalBindingMatches(m Message, requestID string) bool {
	replyPresent := m.ReplyTo != ""
	if replyPresent && (m.ReplyTo != strings.TrimSpace(m.ReplyTo) || m.ReplyTo != requestID) {
		return false
	}
	if m.RefsPresent {
		if !m.RefsValid || len(m.Refs) != 1 || m.Refs[0] == "" || m.Refs[0] != strings.TrimSpace(m.Refs[0]) || m.Refs[0] != requestID {
			return false
		}
	}
	return replyPresent || m.RefsPresent
}

func operatorGateTerminalContext(m Message) (OperatorGateTerminalContext, bool) {
	ctx := OperatorGateTerminalContext{
		State:            OperatorGateState(strings.ToLower(strings.TrimSpace(stringAtPath(m.Context, "gate", "state")))),
		RequestMessageID: stringAtPath(m.Context, "gate", "request_message_id"),
		Requester:        stringAtPath(m.Context, "gate", "requester"),
		Thread:           stringAtPath(m.Context, "gate", "thread"),
		Actor:            stringAtPath(m.Context, "gate", "actor"),
	}
	if ctx.State != OperatorGateStateClosed && ctx.State != OperatorGateStateWithdrawn {
		return OperatorGateTerminalContext{}, false
	}
	if ctx.RequestMessageID == "" || ctx.Requester == "" || ctx.Thread == "" || ctx.Actor == "" {
		return OperatorGateTerminalContext{}, false
	}
	return ctx, true
}

// gateMessagesWithoutConflictingDuplicates collapses exact mailbox copies.
// Conflicting terminal evidence is discarded, while a conflicted request ID
// stays conservatively open through a deterministic request representative.
func gateMessagesWithoutConflictingDuplicates(input []Message, operatorHandle string) ([]Message, map[string]bool) {
	groups := map[string][]Message{}
	var idless []Message
	for _, m := range input {
		if m.ID == "" {
			idless = append(idless, m)
			continue
		}
		groups[m.ID] = append(groups[m.ID], m)
	}
	out := append([]Message(nil), idless...)
	conflictedRequests := map[string]bool{}
	for _, copies := range groups {
		byFingerprint := map[string]Message{}
		for _, m := range copies {
			byFingerprint[gateMessageFingerprint(m)] = m
		}
		if len(byFingerprint) == 1 {
			for _, m := range byFingerprint {
				out = append(out, m)
			}
			continue
		}
		// Conflicting IDs cannot be trusted as terminal evidence. If at least
		// one copy is a valid request, retain the lexicographically smallest
		// request fingerprint so attention fails open deterministically.
		var requestFP string
		var request Message
		for fp, m := range byFingerprint {
			if !strings.HasPrefix(m.Thread, "gate/") || !operatorGateRequestMessage(m, operatorHandle) {
				continue
			}
			if requestFP == "" || fp < requestFP {
				requestFP, request = fp, m
			}
		}
		if requestFP != "" {
			out = append(out, request)
			conflictedRequests[request.ID] = true
		}
	}
	return out, conflictedRequests
}

func gateMessageFingerprint(m Message) string {
	to := append([]string{}, m.To...)
	b, _ := json.Marshal(struct {
		From                        string
		To                          []string
		RawTo                       []string
		ToPresent                   bool
		ToArrayValid                bool
		ToRaw                       string
		Thread                      string
		RawThread                   string
		Subject                     string
		RawSubject                  string
		Created                     time.Time
		RawCreated                  string
		Priority                    Priority
		Kind                        Kind
		ReplyTo                     string
		Refs                        []string
		RefsPresent                 bool
		RefsValid                   bool
		RefsRaw                     string
		Labels                      []string
		Orchestrator                string
		FromProject                 string
		ReplyProject                string
		OrchestratorEvent           string
		ExternalTaskID              string
		Context                     map[string]any
		AuthorizationRequestPresent bool
		AuthorizationRequestValid   bool
		AuthorizationRequestError   string
		ApprovalPresent             bool
		ApprovalValid               bool
		ApprovalError               string
		Body                        string
		RawBody                     string
		AuthorityRaw                bool
		SchemaOK                    bool
	}{
		From: m.From, To: to, RawTo: append([]string{}, m.RawTo...), ToPresent: m.ToPresent, ToArrayValid: m.ToArrayValid, ToRaw: m.ToRaw,
		Thread: m.Thread, RawThread: m.RawThread, Subject: m.Subject, RawSubject: m.RawSubject, Created: m.Created, RawCreated: m.RawCreated,
		Priority: m.Priority, Kind: m.Kind, ReplyTo: m.ReplyTo, Refs: append([]string{}, m.Refs...), RefsPresent: m.RefsPresent, RefsValid: m.RefsValid, RefsRaw: m.RefsRaw,
		Labels: append([]string{}, m.Labels...), Orchestrator: m.Orchestrator, FromProject: m.FromProject, ReplyProject: m.ReplyProject,
		OrchestratorEvent: m.OrchestratorEvent, ExternalTaskID: m.ExternalTaskID, Context: m.Context,
		AuthorizationRequestPresent: m.AuthorizationRequestPresent, AuthorizationRequestValid: m.AuthorizationRequestValid, AuthorizationRequestError: m.AuthorizationRequestError,
		ApprovalPresent: m.ApprovalPresent, ApprovalValid: m.ApprovalValid, ApprovalError: m.ApprovalError,
		Body: m.Body, RawBody: m.RawBody, AuthorityRaw: m.AuthorityRaw, SchemaOK: m.SchemaOK,
	})
	return string(b)
}

func operatorGateRequestMessage(m Message, operatorHandle string) bool {
	if !addressedTo(m, operatorHandle) || !operatorMessageNeedsAction(m) {
		return false
	}
	switch m.Kind {
	case KindQuestion, KindReviewRequest:
		return true
	case KindDecision:
		// A DONE/completion decision is terminal prose, not a fresh approval
		// request. Decisions reopen only with a direct action marker.
		return hasDirectOperatorActionMarker(messageSignalText(m))
	default:
		return false
	}
}

func isHistoricalNeedsYou(triage Triage, fresh Freshness, agents []Agent, operatorHandle string) bool {
	if triage != TriageNeedsYou {
		return false
	}
	seenAgent := false
	hasActiveAgent := false
	for _, ag := range agents {
		if ag.Handle == operatorHandle {
			continue
		}
		seenAgent = true
		if agentCanCurrentlyNeedOperator(ag.Liveness) {
			hasActiveAgent = true
		}
	}
	if seenAgent && !hasActiveAgent {
		return true
	}
	if fresh.Age <= DefaultNeedsYouHistoricalAfter {
		return false
	}
	for _, ag := range agents {
		if ag.Handle == operatorHandle {
			continue
		}
		if !agentCanCurrentlyNeedOperator(ag.Liveness) {
			return false
		}
	}
	return seenAgent
}

func agentCanCurrentlyNeedOperator(l Liveness) bool {
	switch l {
	case LivenessAlive, LivenessDeadMailboxLive:
		return true
	default:
		return false
	}
}

// approveMarkers are case-insensitive substrings in a needs-you thread's subject
// (or kind) that mean the agent is PAUSED awaiting a human approval / permission
// / confirmation before it can proceed. The act-now reason.
var approveMarkers = []string{
	"approve", "approval", "permission", "allow", "proceed",
	"confirm", "[y/n]", "(y/n)", "y/n", "run this", "ok to",
}

// goalMarkers are substrings meaning the goal is reached and the human should
// review and close. "done" is matched as a whole word by hasGoalWord instead,
// since as a substring it hits "abandoned" or "redone".
var goalMarkers = []string{
	"complete", "completed", "shipped", "goal reached",
	"finished", "ready to close", "✅",
}

// hasGoalWord reports a whole-word "done" (the taught `DONE:` prefix, "all
// done"), bounded by non-letters, so "abandoned the retry" does not match.
func hasGoalWord(subjLower string) bool {
	const w = "done"
	for i := 0; i+len(w) <= len(subjLower); i++ {
		if subjLower[i:i+len(w)] != w {
			continue
		}
		if i > 0 && isASCIILetter(subjLower[i-1]) {
			continue
		}
		if j := i + len(w); j < len(subjLower) && isASCIILetter(subjLower[j]) {
			continue
		}
		return true
	}
	return false
}

// isASCIILetter reports whether b is an ASCII letter, used for word-boundary
// checks in hasGoalWord.
func isASCIILetter(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}

// classifyAttnReason says why a needs-you thread needs the human (AttnNone for
// other threads). Approve wins over goal-reached; neither marker is generic.
func classifyAttnReason(a *threadAccumulator, triage Triage) AttnReason {
	if triage != TriageNeedsYou {
		return AttnNone
	}
	return ClassifyAttnSubject(a.subject)
}

// ClassifyAttnSubject maps an operator-facing subject line into the attention
// reason used by status, notify, and console surfaces.
func ClassifyAttnSubject(subject string) AttnReason {
	subj := strings.ToLower(subject)
	for _, mk := range approveMarkers {
		if strings.Contains(subj, mk) {
			return AttnApprove
		}
	}
	if hasGoalWord(subj) {
		return AttnGoalReached
	}
	for _, mk := range goalMarkers {
		if strings.Contains(subj, mk) {
			return AttnGoalReached
		}
	}
	return AttnGeneric
}

// deriveStatus computes the thread lifecycle status from the LATEST message
// kind plus the active-block flag.
func deriveStatus(a *threadAccumulator) ThreadStatus {
	if a.blockActive {
		return ThreadBlocked
	}
	switch a.lastKind {
	case KindQuestion, KindReviewRequest:
		return ThreadAwaitingReply
	case KindDecision:
		// A decision addressed to someone (awaiting their ack/action) reads as
		// awaiting-reply; otherwise it is an open record.
		if len(a.latest.To) > 0 {
			return ThreadAwaitingReply
		}
		return ThreadOpen
	case KindAnswer, KindReviewResponse:
		return ThreadResolved
	default:
		return ThreadOpen
	}
}

// governingThreshold picks which staleness threshold applies to a thread's
// freshness, by status.
func governingThreshold(status ThreadStatus, th Thresholds) time.Duration {
	switch status {
	case ThreadAwaitingReply:
		return th.ReviewAge
	case ThreadBlocked:
		return th.AtRiskWait
	default:
		return th.AtRiskWait
	}
}

// computeFreshness measures the thread's age from its last event and flags
// staleness against the governing threshold.
func computeFreshness(at time.Time, now time.Time, threshold time.Duration) Freshness {
	age := time.Duration(0)
	if !at.IsZero() {
		age = now.Sub(at)
		if age < 0 {
			age = 0
		}
	}
	return Freshness{
		Observed: at,
		Age:      age,
		Stale:    threshold > 0 && age > threshold,
	}
}

// computeTriage classifies a thread into a triage tier. Severity order is
// enforced by checking NeedsYou first, then Blocked, then Gated, then AtRisk.
func computeTriage(a *threadAccumulator, status ThreadStatus, fresh Freshness, now time.Time, th Thresholds, agents []Agent, operatorGate *OperatorGateSignal) Triage {
	op := th.OperatorHandle

	// NeedsYou: an unanswered ask addressed TO the operator, a block awaiting
	// the human, or explicit prose that says the thread is waiting for the
	// operator/user.
	if operatorGate != nil {
		return TriageNeedsYou
	}
	if status == ThreadAwaitingReply || status == ThreadBlocked {
		if addressedTo(a.latest, op) {
			switch a.latest.Kind {
			case KindQuestion, KindReviewRequest, KindDecision:
				if operatorMessageNeedsAction(a.latest) && (operatorStillUnread(a, op) || a.latest.From != op) {
					return TriageNeedsYou
				}
			}
		}
		if a.blockActive && a.blockOwner == op {
			return TriageNeedsYou
		}
	}
	if declaresUserWait(a.latest) {
		return TriageNeedsYou
	}

	// Blocked: an explicitly declared, still-active block (owner may be another
	// agent).
	if a.blockActive {
		return TriageBlocked
	}

	// Gated: intentionally paused on release, QA, policy, or approval gates. A
	// user-waiting gate has already been classified as NeedsYou above.
	if declaresGate(a.latest) {
		return TriageGated
	}

	// AtRisk: agent<->agent unanswered review/question aging past thresholds, or
	// a quiet heartbeat on a participant.
	if status == ThreadAwaitingReply && fresh.Stale {
		return TriageAtRisk
	}
	if heartbeatQuiet(a.participants, agents, now, th) && status == ThreadAwaitingReply {
		return TriageAtRisk
	}
	if deadMailboxLiveParticipant(a.participants, agents) {
		return TriageAtRisk
	}

	return TriageClear
}

// addressedTo reports whether handle is among the message recipients.
func addressedTo(m Message, handle string) bool {
	for _, r := range m.To {
		if r == handle {
			return true
		}
	}
	return false
}

var operatorActionMarkers = []string{
	"?", "please", "need ", "needs ", "scope-check", "scope check",
	"please review", "review this", "review needed", "needs review",
	"approve", "approval", "confirm", "decide", "need decision", "decision needed",
	"choose", "answer", "reply", "input", "permission", "ok to",
	"can you", "should we", "action needed",
}

var operatorNoticePrefixes = []string{
	"ack", "ack:", "re:", "fyi", "status", "qa confirms", "correction",
	"closeout", "main promotion complete",
}

func operatorMessageNeedsAction(m Message) bool {
	if declaresUserWait(m) {
		return true
	}
	text := messageSignalText(m)
	if looksLikeOperatorNotice(text) && !hasDirectOperatorActionMarker(text) {
		return false
	}
	if hasOperatorActionMarker(text) {
		return true
	}
	return m.Kind == KindReviewRequest
}

func hasOperatorActionMarker(text string) bool {
	if hasDirectOperatorActionMarker(text) {
		return true
	}
	for _, mk := range goalMarkers {
		if strings.Contains(text, mk) {
			return true
		}
	}
	return hasGoalWord(text)
}

func hasDirectOperatorActionMarker(text string) bool {
	for _, mk := range operatorActionMarkers {
		if strings.Contains(text, mk) {
			return true
		}
	}
	return false
}

func looksLikeOperatorNotice(text string) bool {
	text = strings.TrimSpace(text)
	for _, prefix := range operatorNoticePrefixes {
		if strings.HasPrefix(text, prefix) {
			return true
		}
	}
	return false
}

// operatorStillUnread reports whether the operator still holds the latest copy
// unread (true also when we have no inbox observation for the operator, since
// absence of a read receipt cannot be treated as "answered").
func operatorStillUnread(a *threadAccumulator, op string) bool {
	if a.readBy[op] {
		return false
	}
	return true
}

// primaryRecipient returns the first recipient of a message (the block owner),
// or "" when none.
func primaryRecipient(m Message) string {
	if len(m.To) > 0 {
		return m.To[0]
	}
	return ""
}

// heartbeatQuiet reports whether any thread participant that is a discovered
// agent has gone quiet past the heartbeat threshold (last_seen too old), which
// makes an outstanding ask at-risk.
func heartbeatQuiet(participants map[string]bool, agents []Agent, now time.Time, th Thresholds) bool {
	for _, ag := range agents {
		if !participants[ag.Handle] {
			continue
		}
		// A verified-alive agent is not quiet just because presence.json is old
		// (agents need not rewrite it every turn); only a not-alive one can be.
		if ag.Liveness == LivenessAlive {
			continue
		}
		if ag.LastSeen.IsZero() {
			continue
		}
		if now.Sub(ag.LastSeen) > th.Heartbeat {
			return true
		}
	}
	return false
}

// deadMailboxLiveParticipant reports whether any thread participant is in the
// dead-mailbox-live state from PR1's liveness — a concrete at-risk signal.
func deadMailboxLiveParticipant(participants map[string]bool, agents []Agent) bool {
	for _, ag := range agents {
		if participants[ag.Handle] && ag.Liveness == LivenessDeadMailboxLive {
			return true
		}
	}
	return false
}

// keysSorted returns the sorted keys of a string-set map.
func keysSorted(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
