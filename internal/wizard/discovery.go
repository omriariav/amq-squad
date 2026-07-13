package wizard

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
)

type RunState string

const (
	RunStateNotStarted RunState = "not_started"
	RunStateRunning    RunState = "running"
	RunStateStopped    RunState = "stopped"
	RunStatePartly     RunState = "partly_running"
	RunStateBlocked    RunState = "blocked"
)

type MemberAction string

const (
	MemberActionLive    MemberAction = "live"
	MemberActionRestore MemberAction = "restore"
	MemberActionFresh   MemberAction = "launch_fresh"
	MemberActionBlocked MemberAction = "blocked"
)

type RunClassification struct {
	State           RunState
	Backend         Backend
	Executable      bool
	RestoreExisting bool
	Detail          string
}

// ClassifyExistingRun applies the approved first-match precedence. The result
// is mutually exclusive: one call returns exactly one state/backend contract.
func ClassifyExistingRun(memberCount, recordCount int, actions []MemberAction, ambiguous bool) RunClassification {
	if memberCount <= 0 || recordCount < 0 || len(actions) != memberCount {
		return blockedClassification("profile has no members or the planner did not return one action per member")
	}
	counts := map[MemberAction]int{}
	for _, action := range actions {
		counts[action]++
	}
	if ambiguous || counts[MemberActionBlocked] > 0 {
		return blockedClassification("profile/session/namespace resolution or a member action is blocked")
	}
	if counts[MemberActionLive] == memberCount {
		return RunClassification{State: RunStateRunning, Detail: "every configured member is live"}
	}
	if counts[MemberActionLive] == 0 && recordCount == 0 && counts[MemberActionFresh] == memberCount {
		return RunClassification{State: RunStateNotStarted, Backend: BackendRunStart, Executable: true, Detail: "no matching launch records; every member is launch-fresh"}
	}
	recoverable := counts[MemberActionRestore] + counts[MemberActionFresh]
	if counts[MemberActionLive] > 0 && recoverable > 0 && counts[MemberActionLive]+recoverable == memberCount {
		return RunClassification{
			State:           RunStatePartly,
			Backend:         BackendResume,
			Executable:      true,
			RestoreExisting: recordCount > 0,
			Detail:          "live members stay running while missing members restore or launch fresh",
		}
	}
	if counts[MemberActionLive] == 0 && recordCount > 0 && recoverable > 0 && recoverable == memberCount {
		return RunClassification{State: RunStateStopped, Backend: BackendResume, Executable: true, RestoreExisting: true, Detail: "no members are live and matching launch history exists"}
	}
	return blockedClassification("discovery facts do not match an executable run state")
}

func blockedClassification(detail string) RunClassification {
	return RunClassification{State: RunStateBlocked, Detail: detail}
}

type DiscoveryMember struct {
	Role       string   `json:"role"`
	Handle     string   `json:"handle"`
	Binary     string   `json:"binary"`
	CWD        string   `json:"cwd"`
	Session    string   `json:"session"`
	NativeArgs []string `json:"native_args"`
	Model      string   `json:"model"`
	Effort     string   `json:"effort"`
}

type DiscoveryOperator struct {
	Enabled         bool                        `json:"enabled"`
	InteractionMode string                      `json:"interaction_mode"`
	Handle          string                      `json:"handle"`
	SelfLead        string                      `json:"self_lead"`
	SelfAllow       []string                    `json:"self_allow"`
	SelfRevision    int64                       `json:"self_revision"`
	SelfPaused      bool                        `json:"self_paused"`
	Notifications   DiscoveryNotificationPolicy `json:"notifications"`
}

type DiscoveryNotificationPolicy struct {
	Enabled           bool                        `json:"enabled"`
	DeliverySemantics string                      `json:"delivery_semantics"`
	Events            []string                    `json:"events"`
	Sinks             []DiscoveryNotificationSink `json:"sinks"`
}

type DiscoveryNotificationSink struct {
	ID      string   `json:"id"`
	Type    string   `json:"type"`
	Argv    []string `json:"argv"`
	Timeout string   `json:"timeout"`
}

type DiscoveryBrief struct {
	Path          string `json:"path"`
	Source        string `json:"source"`
	Goal          string `json:"goal"`
	Provenance    string `json:"provenance"`
	ContentDigest string `json:"content_digest"`
}

type DiscoveryMemberPlan struct {
	Role                string       `json:"role"`
	Action              MemberAction `json:"action"`
	LivenessStatus      string       `json:"liveness_status"`
	LivenessSignals     []string     `json:"liveness_signals"`
	SavedLaunchIdentity string       `json:"saved_launch_identity"`
	SavedTarget         string       `json:"saved_target"`
	Blocker             string       `json:"blocker"`
}

// ResumeGoalPlan is the read-only, session-level evidence used to decide
// whether a freshly re-oriented lead may receive a new claim-once goal
// attempt. Every field is scalar and byte-stable so CLI, JSON, and both
// wizard adapters share the same facts without reconstructing the binding.
type ResumeGoalPlan struct {
	SchemaVersion        int    `json:"schema_version"`
	Action               string `json:"action"`
	Eligible             bool   `json:"eligible"`
	Selected             bool   `json:"selected"`
	Reason               string `json:"reason"`
	LeadRole             string `json:"lead_role"`
	LeadHandle           string `json:"lead_handle"`
	LeadResumeAction     string `json:"lead_resume_action"`
	SavedConversation    bool   `json:"saved_conversation"`
	Goal                 string `json:"goal"`
	BindingMode          string `json:"binding_mode"`
	BindingNative        bool   `json:"binding_native"`
	BindingSource        string `json:"binding_source"`
	BindingDigest        string `json:"binding_digest"`
	BindingCommandDigest string `json:"binding_command_digest"`
	OriginalAttemptID    string `json:"original_attempt_id"`
	AttemptState         string `json:"attempt_state"`
	AttemptDigest        string `json:"attempt_digest"`
	ClaimState           string `json:"claim_state"`
	ClaimRoute           string `json:"claim_route"`
	ClaimDigest          string `json:"claim_digest"`
	TransitionID         string `json:"transition_id"`
	TransitionState      string `json:"transition_state"`
	TransitionDigest     string `json:"transition_digest"`
	RecoveryAttemptID    string `json:"recovery_attempt_id,omitempty"`
	RecoveryCommand      string `json:"recovery_command,omitempty"`
	EvidenceDigest       string `json:"evidence_digest"`
}

// DiscoveryNamespaceFact is one independently mutable input to namespace
// conflict detection. Result strings alone are insufficient: adding durable
// state or changing a profile pin must invalidate a reviewed fingerprint even
// when the rendered conflict label does not change.
type DiscoveryNamespaceFact struct {
	Profile            string `json:"profile"`
	Session            string `json:"session"`
	AMQRoot            string `json:"amq_root"`
	DurableState       bool   `json:"durable_state"`
	ProfilePinsSession bool   `json:"profile_pins_session"`
}

// DiscoveryFingerprintInput contains every existing-profile fact that can
// affect the wizard decision or command. Roster and plan order are preserved;
// set-like facts are sorted in the canonical copy before hashing.
type DiscoveryFingerprintInput struct {
	Profile                 string                   `json:"profile"`
	Roster                  []DiscoveryMember        `json:"roster"`
	Lead                    string                   `json:"lead"`
	LeadMode                string                   `json:"lead_mode"`
	Operator                DiscoveryOperator        `json:"operator"`
	Session                 string                   `json:"session"`
	SessionSource           string                   `json:"session_source"`
	MatchingHistorySessions []string                 `json:"matching_history_sessions"`
	Brief                   DiscoveryBrief           `json:"brief"`
	NamespaceConflicts      []string                 `json:"namespace_conflicts"`
	NamespaceFacts          []DiscoveryNamespaceFact `json:"namespace_facts"`
	RecordIDs               []string                 `json:"record_ids"`
	RecordCount             int                      `json:"record_count"`
	MemberPlans             []DiscoveryMemberPlan    `json:"member_plans"`
	GoalPlan                ResumeGoalPlan           `json:"goal_plan"`
}

func DiscoveryFingerprint(input DiscoveryFingerprintInput) string {
	canonical := input
	// Selected is downstream operator intent, never authoritative discovery.
	canonical.GoalPlan.Selected = false
	canonical.NamespaceConflicts = sortedCopy(input.NamespaceConflicts)
	canonical.NamespaceFacts = canonicalNamespaceFacts(input.NamespaceFacts)
	canonical.RecordIDs = sortedCopy(input.RecordIDs)
	canonical.MatchingHistorySessions = sortedCopy(input.MatchingHistorySessions)
	canonical.Operator.SelfAllow = sortedCopy(input.Operator.SelfAllow)
	canonical.Operator.Notifications.Events = sortedCopy(input.Operator.Notifications.Events)
	canonical.Operator.Notifications.Sinks = canonicalNotificationSinks(input.Operator.Notifications.Sinks)
	canonical.Roster = append([]DiscoveryMember{}, input.Roster...)
	for i := range canonical.Roster {
		canonical.Roster[i].NativeArgs = orderedCopy(input.Roster[i].NativeArgs)
	}
	canonical.MemberPlans = append([]DiscoveryMemberPlan{}, input.MemberPlans...)
	for i := range canonical.MemberPlans {
		canonical.MemberPlans[i].LivenessSignals = sortedCopy(input.MemberPlans[i].LivenessSignals)
	}
	payload, _ := json.Marshal(canonical)
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}

func sortedCopy(values []string) []string {
	out := append([]string{}, values...)
	sort.Strings(out)
	return out
}

func orderedCopy(values []string) []string {
	return append([]string{}, values...)
}

func canonicalNotificationSinks(values []DiscoveryNotificationSink) []DiscoveryNotificationSink {
	out := append([]DiscoveryNotificationSink{}, values...)
	for i := range out {
		out[i].Argv = orderedCopy(out[i].Argv)
	}
	sort.Slice(out, func(i, j int) bool { return canonicalJSON(out[i]) < canonicalJSON(out[j]) })
	return out
}

func canonicalNamespaceFacts(values []DiscoveryNamespaceFact) []DiscoveryNamespaceFact {
	out := append([]DiscoveryNamespaceFact{}, values...)
	sort.Slice(out, func(i, j int) bool { return canonicalJSON(out[i]) < canonicalJSON(out[j]) })
	return out
}

func canonicalJSON(value any) string {
	b, _ := json.Marshal(value)
	return string(b)
}
