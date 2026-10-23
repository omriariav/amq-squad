package state

import (
	"sort"
	"strings"
	"time"
)

// DefaultOperatorHandle is the mailbox handle that represents the human
// operator.
const DefaultOperatorHandle = "user"

// ThreadStatus is the derived lifecycle state of a thread, computed from the
// LATEST message kind plus unread/block markers — never read from disk.
type ThreadStatus string

const (
	// ThreadOpen: an ordinary in-progress thread with nothing outstanding.
	ThreadOpen ThreadStatus = "open"
	// ThreadAwaitingReply: the latest message is an unanswered question /
	// review_request / decision — someone owes a response.
	ThreadAwaitingReply ThreadStatus = "awaiting-reply"
	// ThreadBlocked: a block has been explicitly declared in the thread and not
	// yet cleared.
	ThreadBlocked ThreadStatus = "blocked"
	// ThreadResolved: the latest message is a terminal answer/review_response,
	// closing out the outstanding ask.
	ThreadResolved ThreadStatus = "resolved"
)

func (s OperatorGateState) IsTerminal() bool {
	switch s {
	case OperatorGateStateAnswered, OperatorGateStateClosed, OperatorGateStateWithdrawn:
		return true
	default:
		return false
	}
}

// Triage is one of the computed headline tiers (plus Clear). Order of
// severity: NeedsYou > Blocked > Gated > AtRisk > Clear.
type Triage string

const (
	// TriageNeedsYou: an actionable unanswered message addressed TO the operator
	// handle, explicit waiting-for-operator prose, or a declared block awaiting
	// the human. The concrete "the human must act" signal.
	TriageNeedsYou Triage = "needs-you"
	// TriageBlocked: an agent has explicitly declared a block (kind/marker); the
	// owner may be another agent.
	TriageBlocked Triage = "blocked"
	// TriageGated: work is intentionally paused by a policy, release, QA, or
	// authorization gate. It is visible attention, but not a peer blocker.
	TriageGated Triage = "gated"
	// TriageAtRisk: an agent<->agent unanswered review/question aging past
	// ReviewAge, or a heartbeat gone quiet past Heartbeat, or the
	// dead-mailbox-live case. Aging, not yet a hard block.
	TriageAtRisk Triage = "at-risk"
	// TriageClear: nothing outstanding.
	TriageClear Triage = "clear"
)

// AttnReason classifies WHY a needs-you thread needs the human, derived from the
// message addressed to the operator handle.
type AttnReason string

const (
	// AttnNone: not a needs-you thread (or no reason classified).
	AttnNone AttnReason = ""
	// AttnApprove: the agent is paused awaiting a human to approve an action / a
	// command run / a permission grant. The hot, act-now reason — sorted first.
	AttnApprove AttnReason = "approve"
	// AttnGoalReached: the team signalled done / goal reached — the human is
	// asked to review and close.
	AttnGoalReached AttnReason = "goal-reached"
	// AttnGeneric: a plain question to the human with no approve/done markers.
	AttnGeneric AttnReason = "generic"
)

// Rank orders reasons for the NEEDS YOU block: approve (act now) above goal-
// reached (review) above generic above none.
func (a AttnReason) Rank() int {
	switch a {
	case AttnApprove:
		return 0
	case AttnGoalReached:
		return 1
	case AttnGeneric:
		return 2
	default:
		return 3
	}
}

// OperatorGateEscalation is the age band for an unanswered operator gate.
type OperatorGateEscalation string

const (
	OperatorGateEscalationInitial       OperatorGateEscalation = "initial"
	OperatorGateEscalationReminder      OperatorGateEscalation = "reminder"
	OperatorGateEscalationStrongWarning OperatorGateEscalation = "strong-warning"
)

const (
	DefaultOperatorGateInitialNotifyAfter = 0 * time.Second
	DefaultOperatorGateReminderAfter      = 30 * time.Minute
	DefaultOperatorGateStrongWarningAfter = 2 * time.Hour
)

func OperatorGateEscalationForAge(age time.Duration) OperatorGateEscalation {
	if age < 0 {
		age = 0
	}
	switch {
	case age >= DefaultOperatorGateStrongWarningAfter:
		return OperatorGateEscalationStrongWarning
	case age >= DefaultOperatorGateReminderAfter:
		return OperatorGateEscalationReminder
	default:
		return OperatorGateEscalationInitial
	}
}

func OperatorGateEscalationRank(e OperatorGateEscalation) int {
	switch e {
	case OperatorGateEscalationStrongWarning:
		return 2
	case OperatorGateEscalationReminder:
		return 1
	case OperatorGateEscalationInitial:
		return 0
	default:
		return -1
	}
}

// OperatorGateState is the durable lifecycle state derived from the ordered
// messages on a gate/<topic> thread. Open gates may escalate; terminal states
// never do. A later gate request deliberately reopens a terminal thread.
type OperatorGateState string

const (
	OperatorGateStateUnknown   OperatorGateState = ""
	OperatorGateStateOpen      OperatorGateState = "open"
	OperatorGateStateAnswered  OperatorGateState = "answered"
	OperatorGateStateClosed    OperatorGateState = "closed"
	OperatorGateStateWithdrawn OperatorGateState = "withdrawn"
)

// OperatorGateTerminalContext binds a close or withdraw event to one exact
// open gate generation. Every field is required for terminalization.
type OperatorGateTerminalContext struct {
	State            OperatorGateState
	RequestMessageID string
	Requester        string
	Thread           string
	Actor            string
}

// OperatorGateSignal describes an unanswered operator-facing gate message. Its
// age is measured from the LAST unanswered operator-facing message on the
// gate/<topic> thread, so a re-raised or updated gate starts a fresh clock.
type OperatorGateSignal struct {
	LatestID     string
	From         string
	RawTo        []string
	ToPresent    bool
	ToArrayValid bool
	ToRaw        string
	Subject      string
	Kind         Kind
	Since        time.Time
	Age          time.Duration
	Reason       AttnReason
	Escalation   OperatorGateEscalation
	Conflicted   bool
	SchemaOK     bool
	RawThread    string
	Refs         []string
	RefsPresent  bool
	RefsValid    bool
	RefsRaw      string
	// Terminalizable is true only when the selected request generation has
	// exact, trustworthy protocol identity suitable for a mutation command.
	Terminalizable bool
}

// Freshness annotates a derived field with how old its underlying signal is and
// whether that age has crossed the relevant staleness threshold.
type Freshness struct {
	Observed time.Time     // the timestamp the age is measured from
	Age      time.Duration // now - Observed (>=0)
	Stale    bool          // Age exceeded the governing threshold
}

// Thresholds tune the time-based triage/freshness math. Zero values fall back
// to the documented defaults via withThresholdDefaults, so callers may set only
// the fields they care about.
type Thresholds struct {
	// AtRiskWait: an awaiting-reply thread older than this is at risk.
	AtRiskWait time.Duration
	// Heartbeat: presence/last-activity older than this is a quiet agent.
	Heartbeat time.Duration
	// ReviewAge: an unanswered review/question older than this is at risk.
	ReviewAge time.Duration
	// StaleAfter: a thread whose last event is older than this is STALE.
	StaleAfter time.Duration
	// OperatorHandle is the human's mailbox handle (default "user").
	OperatorHandle string
}

// Default threshold values.
const (
	DefaultAtRiskWait              = 30 * time.Minute
	DefaultHeartbeat               = 90 * time.Second
	DefaultReviewAge               = 45 * time.Minute
	DefaultNeedsYouHistoricalAfter = 24 * time.Hour
	// DefaultStaleAfter: 72h. A thread untouched for three days is treated as
	// stale — its triage no longer counts as LIVE attention.
	DefaultStaleAfter = 72 * time.Hour
)

func withThresholdDefaults(t Thresholds) Thresholds {
	if t.AtRiskWait <= 0 {
		t.AtRiskWait = DefaultAtRiskWait
	}
	if t.Heartbeat <= 0 {
		t.Heartbeat = DefaultHeartbeat
	}
	if t.ReviewAge <= 0 {
		t.ReviewAge = DefaultReviewAge
	}
	if t.StaleAfter <= 0 {
		t.StaleAfter = DefaultStaleAfter
	}
	if strings.TrimSpace(t.OperatorHandle) == "" {
		t.OperatorHandle = DefaultOperatorHandle
	}
	return t
}

// ThreadSummary collapses every message sharing a canonical thread id into one
// derived row. Participants are the union of from+to across the thread.
type ThreadSummary struct {
	ID             string
	LatestID       string
	Participants   []string // union of from + to, sorted
	Subject        string   // latest non-empty subject
	Kind           Kind     // latest recognized kind
	Labels         []string // union of AMQ labels observed on messages in the thread
	Orchestrator   string   // latest orchestrator metadata, when present
	FromProject    string   // latest cross-project sender metadata, when present
	ReplyProject   string   // latest cross-project reply metadata, when present
	ExternalTaskID string   // latest verified external task id from orchestrator context
	Status         ThreadStatus
	LastEventAt    time.Time
	MessageCount   int
	UnreadBy       []string // recipients still holding a copy in inbox/new
	Triage         Triage
	Freshness      Freshness
	// Stale is true when now - LastEventAt exceeds Thresholds.StaleAfter.
	Stale bool
	// Historical is true for a needs-you ask retained for context when it is not
	// current operator action, such as an old ask or a stopped session with no
	// active non-operator agent.
	Historical bool
	// AttnReason classifies WHY a needs-you thread needs the human (approve vs
	// goal-reached vs a plain question). It is AttnNone on every non-needs-you
	// thread. See AttnReason.
	AttnReason AttnReason
	// OperatorGateState records the latest gate lifecycle state even after the
	// gate becomes terminal and drops out of operator attention.
	OperatorGateState OperatorGateState
	// OperatorGate is present only when this is an open operator-facing
	// gate/<topic> ask. It is independent of LastEventAt because later chatter in
	// the thread must not hide the age of the still-unanswered gate.
	OperatorGate *OperatorGateSignal
}

// NeedsYouThreads returns the needs-you threads carried by a coordination view,
// sorted for a NEEDS YOU listing: by reason rank (approve, then goal-reached,
// then generic), then oldest-first within a reason (the longest-waiting human
// ask leads), then by id for determinism.
func (c Coordination) NeedsYouThreads() []ThreadSummary {
	var out []ThreadSummary
	for _, t := range c.Threads {
		if t.Triage == TriageNeedsYou {
			out = append(out, t)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		ri, rj := out[i].AttnReason.Rank(), out[j].AttnReason.Rank()
		if ri != rj {
			return ri < rj
		}
		ti, tj := needsYouWaitSince(out[i]), needsYouWaitSince(out[j])
		if !ti.Equal(tj) {
			return ti.Before(tj)
		}
		return out[i].ID < out[j].ID
	})
	return out
}

func needsYouWaitSince(t ThreadSummary) time.Time {
	if t.OperatorGate != nil && !t.OperatorGate.Since.IsZero() {
		return t.OperatorGate.Since
	}
	return t.LastEventAt
}

// blockMarkers are case-insensitive substrings in a message body that declare a
// block when the kind itself is not block-bearing. Defensive: real data uses
// "NO-GO" / "blocked" / "blocker" prose to declare blocks.
var blockMarkers = []string{"no-go", "blocked on", "blocker:", "i am blocked", "we are blocked", "blocking:"}

// declaresBlock reports whether a message declares a block, via an explicit
// marker in the body. (There is no dedicated block kind on disk; blocks are
// declared in review_response/status prose, which is why this is defensive.)
func declaresBlock(m Message) bool {
	body := strings.ToLower(m.Body)
	subj := strings.ToLower(m.Subject)
	for _, mk := range blockMarkers {
		if strings.Contains(body, mk) || strings.Contains(subj, mk) {
			return true
		}
	}
	return false
}

// userWaitMarkers are conservative prose signals that the latest thread event
// is waiting for the human/operator, even when it was emitted as a status note
// instead of a message addressed to the operator mailbox.
var userWaitMarkers = []string{
	"waiting for instructions",
	"awaiting instructions",
	"need instructions",
	"needs instructions",
	"waiting for direction",
	"awaiting direction",
	"need direction",
	"needs direction",
	"waiting on user",
	"waiting for user",
	"awaiting user",
	"need user",
	"needs user",
	"user action",
	"user approval",
	"user decision",
	"waiting on operator",
	"waiting for operator",
	"awaiting operator",
	"operator action",
	"operator approval",
	"operator decision",
	"human action",
	"human approval",
	"human decision",
	"waiting for scope",
	"awaiting scope",
	"need scope",
	"needs scope",
	"release scope",
	"choose next action",
	"action needed",
}

var userWaitNegationMarkers = []string{
	"no operator action",
	"no user action",
	"no human action",
	"no action requested",
	"no action needed",
	"not waiting for operator",
	"not waiting for user",
	"not waiting for human",
}

func declaresUserWait(m Message) bool {
	text := messageSignalText(m)
	for _, mk := range userWaitNegationMarkers {
		if strings.Contains(text, mk) {
			return false
		}
	}
	for _, mk := range userWaitMarkers {
		if strings.Contains(text, mk) {
			return true
		}
	}
	return false
}

// gateMarkers are prose signals for intentional governance/approval pauses.
// They render as gated unless a more explicit user-wait marker above says the
// operator must act now.
var gateMarkers = []string{
	"approval gate",
	"policy gate",
	"qa gate",
	"qa gates",
	"release gate",
	"governance gate",
	"authorization gate",
	"waiting for approval",
	"awaiting approval",
	"pending approval",
	"paused on",
	"paused until",
	"until cto release",
	"cto release",
	"qa approval",
	"release authorized",
	"gates are authorized",
	"authorization required",
	"governance-gated",
	"approval-gated",
	"policy-gated",
}

func declaresGate(m Message) bool {
	text := messageSignalText(m)
	for _, mk := range gateMarkers {
		if strings.Contains(text, mk) {
			return true
		}
	}
	return false
}

func messageSignalText(m Message) string {
	return strings.ToLower(m.Subject + "\n" + m.Body)
}

// clearsBlock reports whether a message clears a previously-declared block.
// "GO" / "unblocked" / "resolved" signal forward progress.
func clearsBlock(m Message) bool {
	if m.Kind == KindReviewResponse || m.Kind == KindAnswer {
		body := strings.ToLower(m.Body)
		// A bare "GO" decision (not "NO-GO") clears.
		if (strings.Contains(body, "\ngo ") || strings.HasPrefix(body, "go ") ||
			strings.Contains(body, "go for") || strings.Contains(body, "unblocked") ||
			strings.Contains(body, "resolved")) && !declaresBlock(m) {
			return true
		}
	}
	return false
}
