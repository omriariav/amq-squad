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
	Provenance    string `json:"provenance"`
	ContentDigest string `json:"content_digest"`
}

type DiscoveryMemberPlan struct {
	Role                string       `json:"role"`
	Action              MemberAction `json:"action"`
	LivenessStatus      string       `json:"liveness_status"`
	LivenessSignals     []string     `json:"liveness_signals"`
	SavedLaunchIdentity string       `json:"saved_launch_identity"`
	Blocker             string       `json:"blocker"`
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
}

func DiscoveryFingerprint(input DiscoveryFingerprintInput) string {
	canonical := input
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
