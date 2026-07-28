package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/activity"
	"github.com/omriariav/amq-squad/v2/internal/launch"
	squadnamespace "github.com/omriariav/amq-squad/v2/internal/namespace"
	"github.com/omriariav/amq-squad/v2/internal/runtimeaction"
	"github.com/omriariav/amq-squad/v2/internal/state"
	"github.com/omriariav/amq-squad/v2/internal/team"
	"github.com/omriariav/amq-squad/v2/internal/tmuxpane"
)

const (
	goalSupervisionAssessmentSchema = 1
	goalSupervisionFreshness        = 30 * time.Second
)

type GoalSupervisionState string

const (
	GoalSupervisionRunning                  GoalSupervisionState = "running"
	GoalSupervisionParkedWaitingAMQ         GoalSupervisionState = "parked_waiting_amq"
	GoalSupervisionNativeGoalPausedEligible GoalSupervisionState = "native_goal_paused_eligible"
	GoalSupervisionNativeGoalBlockedHuman   GoalSupervisionState = "native_goal_blocked_human"
	GoalSupervisionNativeGoalBlockedUnknown GoalSupervisionState = "native_goal_blocked_unknown"
	GoalSupervisionLeadDown                 GoalSupervisionState = "lead_down"
	GoalSupervisionPaneBusyOrUnverified     GoalSupervisionState = "pane_busy_or_unverified"
	GoalSupervisionGoalTerminal             GoalSupervisionState = "goal_terminal"
)

type GoalSupervisionSourceStatus struct {
	Complete bool     `json:"complete"`
	Errors   []string `json:"errors,omitempty"`
}

type GoalSupervisionRuntimeIdentity struct {
	Known        bool `json:"known"`
	Live         bool `json:"live"`
	FullLive     bool `json:"full_live"`
	VerifiedDown bool `json:"verified_down"`
	PIDLive      bool `json:"pid_live"`
	PaneLive     bool `json:"pane_live"`
	PIDAlive     bool `json:"pid_alive"`
	BinaryMatch  bool `json:"binary_match"`
}

type GoalSupervisionPaneIdentity struct {
	Managed    bool   `json:"managed"`
	Session    string `json:"session,omitempty"`
	WindowID   string `json:"window_id,omitempty"`
	WindowName string `json:"window_name,omitempty"`
	PaneID     string `json:"pane_id,omitempty"`
	Target     string `json:"target,omitempty"`
	BusyKnown  bool   `json:"busy_known"`
	Busy       bool   `json:"busy"`
}

type GoalSupervisionGoalIdentity struct {
	Mode          string `json:"mode,omitempty"`
	NativeGoal    bool   `json:"native_goal"`
	StateKnown    bool   `json:"state_known"`
	Verified      bool   `json:"verified"`
	ContentExact  bool   `json:"content_exact"`
	Source        string `json:"source,omitempty"`
	DeliveryState string `json:"delivery_state,omitempty"`
	GoalDigest    string `json:"goal_digest,omitempty"`
	AttemptID     string `json:"attempt_id,omitempty"`
	BindingDigest string `json:"binding_digest,omitempty"`
	CommandDigest string `json:"command_digest,omitempty"`
}

type GoalSupervisionBinding struct {
	Project               string                         `json:"project"`
	Profile               string                         `json:"profile"`
	Session               string                         `json:"session"`
	NamespaceID           string                         `json:"namespace_id"`
	LeadRole              string                         `json:"lead_role,omitempty"`
	LeadHandle            string                         `json:"lead_handle,omitempty"`
	LaunchID              string                         `json:"launch_id,omitempty"`
	LaunchStartedAt       time.Time                      `json:"launch_started_at,omitempty"`
	LaunchRecordDigest    string                         `json:"launch_record_digest,omitempty"`
	LaunchRecordModTime   int64                          `json:"launch_record_mod_time,omitempty"`
	Runtime               GoalSupervisionRuntimeIdentity `json:"runtime"`
	Pane                  GoalSupervisionPaneIdentity    `json:"pane"`
	Goal                  GoalSupervisionGoalIdentity    `json:"goal"`
	PauseGeneration       string                         `json:"pause_generation,omitempty"`
	PreparedRunGeneration string                         `json:"prepared_run_generation,omitempty"`
	PreparedRunDigest     string                         `json:"prepared_run_digest,omitempty"`
	PreparedLaunchAttempt string                         `json:"prepared_launch_attempt,omitempty"`
	PreparedGoalNamespace string                         `json:"prepared_goal_namespace,omitempty"`
	PreparedGoalDigest    string                         `json:"prepared_goal_digest,omitempty"`
}

type GoalSupervisionBlockerEvidence struct {
	ID               string `json:"id,omitempty"`
	Known            bool   `json:"known"`
	Resolved         bool   `json:"resolved"`
	Detail           string `json:"detail,omitempty"`
	ResolutionDigest string `json:"resolution_digest,omitempty"`
}

type GoalSupervisionLifecycleEvidence struct {
	Known  bool   `json:"known"`
	Fresh  bool   `json:"fresh"`
	Source string `json:"source,omitempty"`
	Phase  string `json:"phase,omitempty"`
}

type GoalSupervisionGateEvidence struct {
	Known     bool `json:"known"`
	Open      int  `json:"open"`
	Ambiguous bool `json:"ambiguous"`
}

type GoalSupervisionLocalInputEvidence struct {
	Known       bool   `json:"known"`
	Observed    bool   `json:"observed"`
	Kind        string `json:"kind,omitempty"`
	Summary     string `json:"summary,omitempty"`
	Destructive bool   `json:"destructive,omitempty"`
}

type GoalSupervisionInvariantEvidence struct {
	OK     bool     `json:"ok"`
	Errors []string `json:"errors,omitempty"`
}

type GoalSupervisionEligibilityReason struct {
	Code   string `json:"code"`
	Passed bool   `json:"passed"`
	Detail string `json:"detail,omitempty"`
}

type GoalSupervisionClaimProjection struct {
	Known         bool   `json:"known"`
	State         string `json:"state"`
	ClaimID       string `json:"claim_id,omitempty"`
	DeliveryStage string `json:"delivery_stage,omitempty"`
	Indeterminate bool   `json:"indeterminate,omitempty"`
}

type GoalSupervisionBudgetEvidence struct {
	Known   bool `json:"known"`
	Allowed bool `json:"allowed"`
}

type GoalSupervisionAction struct {
	runtimeaction.Action
	Fingerprint  string `json:"fingerprint,omitempty"`
	AttemptID    string `json:"attempt_id,omitempty"`
	Confirmation string `json:"confirmation,omitempty"`

	// CommandDigest is the digest of the NATIVE RESUME PAYLOAD this action authorizes -- the exact
	// bytes the agent's pane must receive. It is deliberately NOT the digest of Command.
	//
	// Command and CommandDigest describe TWO DIFFERENT THINGS and conflating them was a
	// wrong-payload delivery bug (#498 U1/F2):
	//   Command       the SUPERVISOR INVOCATION -- what an operator types to trigger supervision.
	//                 Operator-facing. NEVER delivered into an agent pane; typing it there would
	//                 recursively invoke the supervisor inside the thing being supervised.
	//   CommandDigest authorizes the NATIVE PAYLOAD, whose text lives on the launch record's
	//                 GoalBinding.Command and is read at delivery time.
	// The executor validates this digest against assessment.Binding.Goal.CommandDigest before any
	// reserve, and again against the actual bytes immediately before typing them.
	CommandDigest string `json:"command_digest,omitempty"`
}

type GoalSupervisionActions struct {
	Inspect GoalSupervisionAction `json:"inspect"`
	Restore GoalSupervisionAction `json:"restore"`
	Notify  GoalSupervisionAction `json:"notify"`
	Resume  GoalSupervisionAction `json:"resume"`
}

// GoalSupervisionAssessment is the versioned, read-only #498 supervision
// object. PR5 consumes this exact binding/fingerprint before it may reserve or
// deliver anything; this type performs no claim, AMQ send, or pane mutation.
type GoalSupervisionAssessment struct {
	SchemaVersion          int                                `json:"schema_version"`
	ObservedAt             time.Time                          `json:"observed_at"`
	FreshUntil             time.Time                          `json:"fresh_until"`
	Fresh                  bool                               `json:"fresh"`
	Fingerprint            string                             `json:"fingerprint"`
	Source                 GoalSupervisionSourceStatus        `json:"source"`
	State                  GoalSupervisionState               `json:"state"`
	Eligible               bool                               `json:"eligible"`
	AutomaticResumeAllowed bool                               `json:"automatic_resume_allowed"`
	AttentionRequired      bool                               `json:"attention_required"`
	Binding                GoalSupervisionBinding             `json:"binding"`
	Lifecycle              GoalSupervisionLifecycleEvidence   `json:"lifecycle"`
	Blocker                GoalSupervisionBlockerEvidence     `json:"blocker"`
	Gates                  GoalSupervisionGateEvidence        `json:"gates"`
	LocalInput             GoalSupervisionLocalInputEvidence  `json:"local_input"`
	Invariants             GoalSupervisionInvariantEvidence   `json:"invariants"`
	Policy                 team.GoalSupervisionPolicyStatus   `json:"policy"`
	Claim                  GoalSupervisionClaimProjection     `json:"claim"`
	Budget                 GoalSupervisionBudgetEvidence      `json:"budget"`
	Reasons                []GoalSupervisionEligibilityReason `json:"eligibility_reasons"`
	Actions                GoalSupervisionActions             `json:"actions"`
}

type goalSupervisionAssessmentInput struct {
	ObservedAt      time.Time
	Now             time.Time
	MaxAge          time.Duration
	SourceErrors    []string
	Binding         GoalSupervisionBinding
	Lifecycle       GoalSupervisionLifecycleEvidence
	Blocker         GoalSupervisionBlockerEvidence
	Gates           GoalSupervisionGateEvidence
	LocalInput      GoalSupervisionLocalInputEvidence
	InvariantErrors []string
	Policy          team.GoalSupervisionPolicyStatus
	Claim           GoalSupervisionClaimProjection
	Budget          GoalSupervisionBudgetEvidence
}

type goalSupervisionGateObservation struct {
	Evidence     GoalSupervisionGateEvidence
	SourceErrors []string
}

func assessGoalSupervision(in goalSupervisionAssessmentInput) GoalSupervisionAssessment {
	if in.ObservedAt.IsZero() {
		in.ObservedAt = time.Now().UTC()
	}
	if in.MaxAge <= 0 {
		in.MaxAge = goalSupervisionFreshness
	}
	if in.Now.IsZero() {
		in.Now = in.ObservedAt
	}
	in.Binding.Runtime.FullLive = goalSupervisionFullRuntimeLive(in.Binding.Runtime)
	in.Binding.Runtime.VerifiedDown = goalSupervisionRuntimeVerifiedDown(in.Binding.Runtime)
	nativePaused := goalSupervisionNativePaused(in.Binding.Goal)
	nativeBlockedObserved := goalSupervisionNativeBlockedObserved(in.Binding.Goal)
	if !in.Binding.Goal.StateKnown {
		in.SourceErrors = append(in.SourceErrors, "native goal state is unverified")
	}
	if nativeBlockedObserved && !in.Binding.Goal.ContentExact {
		in.SourceErrors = append(in.SourceErrors, "native goal binding contents are not exact")
	}
	if !in.Lifecycle.Known || !in.Lifecycle.Fresh {
		in.SourceErrors = append(in.SourceErrors, "goal lifecycle evidence is absent, stale, or unreadable")
	}
	if !in.Gates.Known {
		in.SourceErrors = append(in.SourceErrors, "operator gate source is unknown")
	}
	if in.Gates.Ambiguous {
		in.SourceErrors = append(in.SourceErrors, "operator gate evidence is ambiguous")
	}
	if nativePaused && !in.Binding.Pane.BusyKnown {
		in.SourceErrors = append(in.SourceErrors, "pane busy state is unknown")
	}
	if nativePaused && in.Binding.Runtime.FullLive && !in.LocalInput.Known {
		in.SourceErrors = append(in.SourceErrors, "lead local-input state is unknown")
	}
	if nativePaused && !in.Blocker.Known {
		in.SourceErrors = append(in.SourceErrors, "native goal blocker is unknown")
	}
	if nativePaused {
		if !in.Claim.Known || in.Claim.Indeterminate {
			in.SourceErrors = append(in.SourceErrors, "claim state is indeterminate")
		}
		if !in.Budget.Known {
			in.SourceErrors = append(in.SourceErrors, "retry budget state is unknown")
		}
	}
	in.SourceErrors = stableUniqueStrings(in.SourceErrors)
	in.InvariantErrors = stableUniqueStrings(in.InvariantErrors)
	if in.Claim.State == "" {
		in.Claim.State = "unknown"
	}
	if in.Policy.Mode == "" {
		in.Policy = team.GoalSupervisionPolicyStatus{
			Mode: team.GoalSupervisionManual, Revision: 1, Source: "compatibility-default",
		}
	}
	a := GoalSupervisionAssessment{
		SchemaVersion: goalSupervisionAssessmentSchema,
		ObservedAt:    in.ObservedAt.UTC(),
		FreshUntil:    in.ObservedAt.UTC().Add(in.MaxAge),
		Fresh:         !in.Now.UTC().Before(in.ObservedAt.UTC()) && !in.Now.UTC().After(in.ObservedAt.UTC().Add(in.MaxAge)),
		Source: GoalSupervisionSourceStatus{
			Complete: len(in.SourceErrors) == 0,
			Errors:   in.SourceErrors,
		},
		Binding:    in.Binding,
		Lifecycle:  in.Lifecycle,
		Blocker:    in.Blocker,
		Gates:      in.Gates,
		LocalInput: in.LocalInput,
		Invariants: GoalSupervisionInvariantEvidence{
			OK: len(in.InvariantErrors) == 0, Errors: in.InvariantErrors,
		},
		Policy: in.Policy,
		Claim:  in.Claim,
		Budget: in.Budget,
	}
	a.Reasons = goalSupervisionEligibilityReasons(a, in)
	a.Eligible = allGoalSupervisionReasonsPass(a.Reasons)
	a.State = goalSupervisionStateFor(a, in)
	a.AutomaticResumeAllowed = a.Eligible && a.Policy.Mode == team.GoalSupervisionSafeAuto
	a.AttentionRequired = goalSupervisionNeedsAttention(a.State)
	a.Fingerprint = goalSupervisionFingerprint(a)
	a.Actions = goalSupervisionActions(a)
	return a
}

func goalSupervisionEligibilityReasons(a GoalSupervisionAssessment, in goalSupervisionAssessmentInput) []GoalSupervisionEligibilityReason {
	reason := func(code string, passed bool, detail string) GoalSupervisionEligibilityReason {
		return GoalSupervisionEligibilityReason{Code: code, Passed: passed, Detail: detail}
	}
	b := a.Binding
	nativePaused := goalSupervisionNativePaused(b.Goal)
	return []GoalSupervisionEligibilityReason{
		reason("fresh_assessment", a.Fresh, "assessment must remain inside its freshness window"),
		reason("sources_complete", a.Source.Complete, strings.Join(a.Source.Errors, "; ")),
		reason("exact_namespace", goalSupervisionAllNonBlank(b.Project, b.Profile, b.Session, b.NamespaceID), "project/profile/session/namespace must be exact"),
		reason("exact_lead_identity", goalSupervisionAllNonBlank(b.LeadRole, b.LeadHandle), "lead role and handle must be exact"),
		reason("lifecycle_known", a.Lifecycle.Known && a.Lifecycle.Fresh, "fresh durable lifecycle evidence is required"),
		reason("resumable_lifecycle", a.Lifecycle.Known && a.Lifecycle.Fresh && a.Lifecycle.Phase == "goal_blocked", "only a fresh recognized goal_blocked lifecycle is resume-eligible"),
		reason("native_goal_paused", nativePaused, "verified blocked native /goal binding required"),
		reason("goal_binding_content", b.Goal.ContentExact, "typed goal and attempt must match the exact generated command and prepared-run goal"),
		reason("goal_attempt", b.Goal.Mode == "native_goal_blocked" && goalSupervisionAllNonBlank(b.Goal.Source, b.Goal.DeliveryState, b.Goal.GoalDigest, b.Goal.AttemptID, b.Goal.BindingDigest, b.Goal.CommandDigest), "exact blocked native goal, attempt, binding, delivery, and command digests required"),
		reason("launch_generation", goalSupervisionAllNonBlank(b.LaunchID, b.LaunchRecordDigest) && b.LaunchRecordModTime > 0 && !b.LaunchStartedAt.IsZero(), "launch ID, digest, modtime, and start time required"),
		reason("prepared_run_binding", goalSupervisionAllNonBlank(b.PreparedRunGeneration, b.PreparedRunDigest, b.PreparedLaunchAttempt, b.PreparedGoalNamespace, b.PreparedGoalDigest) && b.PreparedGoalNamespace == b.NamespaceID, "prepared run generation, launch attempt, and exact namespace goal binding required"),
		reason("pause_generation", goalSupervisionAllNonBlank(b.PauseGeneration), "native pause generation required"),
		reason("runtime_identity", b.Runtime.FullLive, "PID, process binary, and exact pane must all be positively live"),
		reason("pane_identity", b.Pane.Managed && goalSupervisionAllNonBlank(b.Pane.PaneID) && b.Runtime.FullLive, "exact managed live pane required"),
		reason("pane_idle", b.Pane.BusyKnown && !b.Pane.Busy, "positive idle evidence required"),
		reason("blocker_known", a.Blocker.Known && goalSupervisionAllNonBlank(a.Blocker.ID), "original blocker identity required"),
		reason("blocker_resolved", a.Blocker.Resolved && goalSupervisionAllNonBlank(a.Blocker.ResolutionDigest), "durable blocker-resolution evidence required"),
		reason("gates_known", a.Gates.Known, "gate source must be available"),
		reason("no_open_gate", a.Gates.Known && a.Gates.Open == 0, "operator gates must be closed"),
		reason("no_gate_ambiguity", !a.Gates.Ambiguous, "shadowed or unbound gate evidence is ineligible"),
		reason("local_input_known", a.LocalInput.Known, "lead pane local-input scan must succeed"),
		reason("no_local_input", a.LocalInput.Known && !a.LocalInput.Observed, "permission and local-input prompts require a human"),
		reason("invariants_ok", a.Invariants.OK, strings.Join(a.Invariants.Errors, "; ")),
		reason("claim_known", a.Claim.Known && !a.Claim.Indeterminate, "durable claim state must be observed"),
		reason("claim_clear", a.Claim.Known && a.Claim.State == "none" && !a.Claim.Indeterminate, "no prior or indeterminate claim may exist"),
		reason("retry_budget_known", a.Budget.Known, "durable retry/cooldown/loop budget state must be observed"),
		reason("retry_budget", a.Budget.Known && a.Budget.Allowed, "retry/cooldown/loop budget must remain"),
	}
}

func goalSupervisionAllNonBlank(values ...string) bool {
	for _, value := range values {
		if strings.TrimSpace(value) == "" {
			return false
		}
	}
	return true
}

func goalSupervisionStateFor(a GoalSupervisionAssessment, in goalSupervisionAssessmentInput) GoalSupervisionState {
	switch {
	case a.Lifecycle.Known && a.Lifecycle.Fresh && a.Lifecycle.Phase == "goal_terminal":
		return GoalSupervisionGoalTerminal
	case a.Lifecycle.Known && a.Lifecycle.Fresh && a.Lifecycle.Phase == "parked_waiting_amq":
		return GoalSupervisionParkedWaitingAMQ
	case a.LocalInput.Known && a.LocalInput.Observed || a.Gates.Known && a.Gates.Open > 0:
		return GoalSupervisionNativeGoalBlockedHuman
	case goalSupervisionNativePaused(a.Binding.Goal) && a.Blocker.Known && !a.Blocker.Resolved:
		return GoalSupervisionNativeGoalBlockedHuman
	case goalSupervisionNativePaused(a.Binding.Goal) &&
		a.Binding.Runtime.FullLive &&
		a.Binding.Pane.Managed &&
		(!a.Binding.Pane.BusyKnown || a.Binding.Pane.Busy):
		return GoalSupervisionPaneBusyOrUnverified
	case !a.Source.Complete:
		return GoalSupervisionNativeGoalBlockedUnknown
	case a.Binding.Runtime.VerifiedDown:
		return GoalSupervisionLeadDown
	case a.Binding.Goal.StateKnown && !goalSupervisionNativePaused(a.Binding.Goal):
		return GoalSupervisionRunning
	case !a.Binding.Goal.StateKnown:
		return GoalSupervisionNativeGoalBlockedUnknown
	case !a.Binding.Runtime.FullLive || !a.Binding.Pane.Managed || !a.Binding.Pane.BusyKnown || a.Binding.Pane.Busy:
		return GoalSupervisionPaneBusyOrUnverified
	case a.Gates.Ambiguous || !a.Invariants.OK || !a.Blocker.Known:
		return GoalSupervisionNativeGoalBlockedUnknown
	case a.Eligible:
		return GoalSupervisionNativeGoalPausedEligible
	default:
		return GoalSupervisionNativeGoalBlockedUnknown
	}
}

func goalSupervisionNeedsAttention(state GoalSupervisionState) bool {
	switch state {
	case GoalSupervisionNativeGoalPausedEligible, GoalSupervisionNativeGoalBlockedHuman,
		GoalSupervisionNativeGoalBlockedUnknown, GoalSupervisionLeadDown,
		GoalSupervisionPaneBusyOrUnverified:
		return true
	default:
		return false
	}
}

func allGoalSupervisionReasonsPass(reasons []GoalSupervisionEligibilityReason) bool {
	if len(reasons) == 0 {
		return false
	}
	for _, reason := range reasons {
		if !reason.Passed {
			return false
		}
	}
	return true
}

func goalSupervisionFingerprint(a GoalSupervisionAssessment) string {
	copy := a
	copy.ObservedAt = time.Time{}
	copy.FreshUntil = time.Time{}
	copy.Fresh = false
	copy.Fingerprint = ""
	copy.Actions = GoalSupervisionActions{}
	return digestJSON(copy)
}

func goalSupervisionActions(a GoalSupervisionAssessment) GoalSupervisionActions {
	scope := runtimeActionScope(a.Binding.Project, a.Binding.Profile, a.Binding.Session)
	namespaceID := a.Binding.NamespaceID
	mutationBound := goalSupervisionAllNonBlank(a.Fingerprint, a.Binding.Goal.AttemptID)
	restoreAvailable := a.State == GoalSupervisionLeadDown &&
		a.Source.Complete &&
		a.Binding.Runtime.VerifiedDown &&
		goalSupervisionReasonPassed(a.Reasons, "exact_namespace") &&
		goalSupervisionReasonPassed(a.Reasons, "exact_lead_identity") &&
		goalSupervisionReasonPassed(a.Reasons, "launch_generation") &&
		a.Binding.Pane.Managed &&
		goalSupervisionManagedTmuxTarget(a.Binding.Pane.Target) &&
		a.Gates.Known && a.Gates.Open == 0 && !a.Gates.Ambiguous &&
		mutationBound
	resumeReason := "claim-once native goal resume execution is reserved for PR5"
	if !a.Eligible {
		resumeReason = firstFailedGoalSupervisionReason(a.Reasons)
	}
	actions := runtimeaction.ApplyCanonical([]runtimeaction.Action{
		{
			Kind: "goal_supervision_inspect", Label: "inspect goal supervision assessment",
			Scope: "session", NamespaceID: namespaceID, Command: "amq-squad status" + scope + " --json",
			Available: true,
		},
		{
			Kind: "goal_supervision_restore", Label: "restore managed lead",
			Scope: "session", NamespaceID: namespaceID, Command: "amq-squad resume" + scope + " --exec",
			Mutates: true, NeedsConfirmation: true, Available: restoreAvailable,
			Reason: unavailableUnless(restoreAvailable, "exact unambiguous lead-down scope is required"),
		},
		{
			Kind: "goal_supervision_notify", Label: "preview goal-supervision notification",
			Scope: "session", NamespaceID: namespaceID, Command: "amq-squad notify" + scope + " --dry-run --json",
			Available: a.AttentionRequired,
			Reason:    unavailableUnless(a.AttentionRequired, "assessment does not require attention"),
		},
		{
			// #498 U1/F4: this metadata was NeedsConfirmation=true / Available=false as
			// PLACEHOLDERS for a supervisor surface that did not exist. It exists now, so the
			// metadata states the truth instead:
			//   Available          mirrors the assessment's OWN conclusion. Publishing an action as
			//                      available when the assessment says otherwise invites a caller to
			//                      invoke it, and the executor now REFUSES an action the assessment
			//                      marks unavailable rather than ignoring the field.
			//   NeedsConfirmation  false ONLY under safe_auto, where the operator's consent is
			//                      already recorded in policy. Manual and notify-only still need the
			//                      human, so the flag stays true there -- the no-confirmation ruling
			//                      is scoped to safe_auto, not global.
			// The command name is corrected to the registered subcommand: it was
			// "goal supervision-resume" while goal.go registers "goal supervise-resume", so the
			// published action named a subcommand that does not exist.
			// P1: the published command is a BOUND INVOCATION, not a generic trigger. It carries the
			// attempt id and the assessment fingerprint, and the command validates both against its
			// own FRESH assessment -- so this action can only ever fire against the pause it was
			// published for. Pasting a stale one refuses instead of resuming something else.
			//
			// --supervisor renders as an EXPLICIT PLACEHOLDER, per the #579 precedent: the value must
			// come from the operator, and inventing one would be worse than asking for it. So this is
			// executable after exactly ONE human fill, which is what "supervisor is never inferred"
			// means at the surface rather than merely in the executor.
			//
			// Available=true is HONEST under that rendering: every machine-fillable parameter is
			// filled and correct, and the single placeholder is the one value that is definitionally
			// the operator's to supply.
			Kind: "native_goal_resume", Label: "resume exact native /goal attempt",
			Scope: "agent", NamespaceID: namespaceID,
			Command: "amq-squad goal supervise-resume" + scope +
				" --attempt-id " + shellQuote(a.Binding.Goal.AttemptID) +
				" --assessment-fingerprint " + shellQuote(a.Fingerprint) +
				" --supervisor " + shellQuote(supervisorIdentityPlaceholder),
			Mutates:   true,
			Available: a.AutomaticResumeAllowed,
			// Keyed on Policy.Mode, NOT on AutomaticResumeAllowed. The conjunction also folds in
			// Eligible, so an ineligible actor under a safe_auto profile would otherwise flip back to
			// needing confirmation -- which would misreport the operator's standing consent as absent
			// because of an unrelated eligibility fact. The consent question is "what did the operator
			// choose", and only Policy.Mode answers it.
			NeedsConfirmation: a.Policy.Mode != team.GoalSupervisionSafeAuto,
			// P3: Reason is populated ONLY when unavailable. An action that simultaneously reports
			// "available" and carries a reason it cannot run is a false record, and a reader has no
			// way to know which half to trust.
			Reason: unavailableUnless(a.AutomaticResumeAllowed, resumeReason),
		},
	})
	// These two are read-only display actions even though their new kinds are
	// unknown to the legacy runtimeaction classifier.
	actions[0].ActionKind = "display"
	actions[2].ActionKind = "display"
	wrap := func(action runtimeaction.Action) GoalSupervisionAction {
		out := GoalSupervisionAction{Action: action}
		if action.Mutates {
			out.Fingerprint = a.Fingerprint
			out.AttemptID = a.Binding.Goal.AttemptID
			out.Confirmation = "Reassess this exact fingerprint and attempt immediately before mutation."
		}
		// The NATIVE PAYLOAD digest travels only on the action that actually authorizes a native
		// resume. Attaching it to every mutating action would publish an authorization that those
		// actions do not carry, and the executor compares this field for equality -- so a spurious
		// value on an unrelated action is a claim that action was never entitled to make.
		if action.Kind == "native_goal_resume" {
			out.CommandDigest = a.Binding.Goal.CommandDigest
		}
		return out
	}
	return GoalSupervisionActions{
		Inspect: wrap(actions[0]), Restore: wrap(actions[1]),
		Notify: wrap(actions[2]), Resume: wrap(actions[3]),
	}
}

func goalSupervisionNativePaused(goal GoalSupervisionGoalIdentity) bool {
	return goalSupervisionNativeBlockedObserved(goal) && goal.ContentExact
}

func goalSupervisionNativeBlockedObserved(goal GoalSupervisionGoalIdentity) bool {
	return goal.StateKnown && goal.Verified && goal.NativeGoal &&
		goal.Mode == "native_goal_blocked"
}

func goalSupervisionFullRuntimeLive(runtime GoalSupervisionRuntimeIdentity) bool {
	return runtime.Known && runtime.Live && runtime.PIDLive && runtime.PaneLive &&
		runtime.PIDAlive && runtime.BinaryMatch
}

func goalSupervisionRuntimeVerifiedDown(runtime GoalSupervisionRuntimeIdentity) bool {
	return runtime.Known && !runtime.Live && !runtime.PIDLive && !runtime.PaneLive &&
		!runtime.PIDAlive
}

func goalSupervisionReasonPassed(reasons []GoalSupervisionEligibilityReason, code string) bool {
	for _, reason := range reasons {
		if reason.Code == code {
			return reason.Passed
		}
	}
	return false
}

func runtimeActionScope(project, profile, session string) string {
	return " --project " + shellQuote(project) +
		" --profile " + shellQuote(squadnamespace.NormalizeProfile(profile)) +
		" --session " + shellQuote(session)
}

// supervisorIdentityPlaceholder is the literal a published action renders where the operator's own
// identity belongs. ONE constant, used by rendering, help text, and validation, so the three cannot
// drift into a state where the surface prints one string and the validator refuses another.
//
// It is rendered SHELL-QUOTED. Unquoted, "<your-identity>" is input redirection, not an argument:
// pasting the template would make the shell try to read a file called your-identity and the CLI would
// never see the placeholder at all, so the refusal that is supposed to catch an unfilled template
// could not fire. Quoting turns shell SYNTAX into DATA, which is what makes the #579 placeholder
// behaviour observable instead of delegated to shell parsing.
const supervisorIdentityPlaceholder = "<your-identity>"

func unavailableUnless(available bool, reason string) string {
	if available {
		return ""
	}
	return reason
}

func firstFailedGoalSupervisionReason(reasons []GoalSupervisionEligibilityReason) string {
	for _, reason := range reasons {
		if !reason.Passed {
			if reason.Detail != "" {
				return reason.Code + ": " + reason.Detail
			}
			return reason.Code
		}
	}
	return "assessment is ineligible"
}

func stableUniqueStrings(values []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

var goalSupervisionPaneBusy = func(paneID string) (busy bool, known bool) {
	busy, err := tmuxpane.PaneBusy(paneID)
	return busy, err == nil
}

var goalSupervisionPaneInspector = tmuxpane.InspectPaneExactByID

var goalSupervisionLocalInputDetector = tmuxpane.DetectLocalInputBlocker

var goalSupervisionBlockedBindingVerifier = verifyGoalSupervisionBlockedBinding

func buildGoalSupervisionAssessment(
	t team.Team,
	profile, session string,
	ns squadnamespace.Ref,
	rows []statusRecord,
	gateObservation goalSupervisionGateObservation,
	invariantErrors []executionInvariantError,
	namespaceConflict *namespaceConflictData,
	probe duplicateLaunchProbe,
	now time.Time,
) GoalSupervisionAssessment {
	profile = squadnamespace.NormalizeProfile(profile)
	var lifecycle *activity.Snapshot
	input := goalSupervisionAssessmentInput{
		Policy:          team.EffectiveGoalSupervisionPolicy(t),
		Claim:           GoalSupervisionClaimProjection{State: "unknown"},
		SourceErrors:    append([]string(nil), gateObservation.SourceErrors...),
		Gates:           gateObservation.Evidence,
		InvariantErrors: goalSupervisionInvariantStrings(invariantErrors),
		Binding: GoalSupervisionBinding{
			Project: t.Project, Profile: profile, Session: session, NamespaceID: ns.ID,
			LeadRole: strings.TrimSpace(t.Lead),
		},
	}
	finish := func() GoalSupervisionAssessment {
		observedAt := time.Now().UTC()
		if probe.Now != nil {
			observedAt = probe.Now().UTC()
		}
		input.ObservedAt = observedAt
		input.Now = observedAt
		input.Lifecycle = goalSupervisionLifecycleObservation(lifecycle, observedAt)
		return assessGoalSupervision(input)
	}
	if namespaceConflict != nil {
		input.SourceErrors = append(input.SourceErrors, "namespace/profile identity is ambiguous")
		input.Gates.Ambiguous = true
	}
	if !t.Orchestrated || input.Binding.LeadRole == "" {
		input.SourceErrors = append(input.SourceErrors, "profile has no configured visible lead")
		return finish()
	}
	leadMember, ok := teamMemberByRole(t, input.Binding.LeadRole)
	if !ok {
		input.SourceErrors = append(input.SourceErrors, "configured lead role does not resolve to one member")
		return finish()
	}
	input.Binding.LeadHandle = strings.TrimSpace(leadMember.Handle)
	if input.Binding.LeadHandle == "" {
		input.Binding.LeadHandle = leadMember.Role
	}
	var leadRows []statusRecord
	for _, row := range rows {
		if row.Role == input.Binding.LeadRole && row.Handle == input.Binding.LeadHandle {
			leadRows = append(leadRows, row)
		}
	}
	if len(leadRows) != 1 {
		input.SourceErrors = append(input.SourceErrors, "visible lead runtime does not resolve to exactly one record")
		return finish()
	}
	row := leadRows[0]
	lifecycle = row.Activity
	if row.ClassificationError != "" {
		input.SourceErrors = append(input.SourceErrors, "lead runtime classification: "+row.ClassificationError)
	}
	if !row.liveness.LaunchFound {
		input.SourceErrors = append(input.SourceErrors, "lead launch record is missing")
		return finish()
	}
	rec, digest, modTime, snapshotErr := readGoalSupervisionLaunchSnapshot(row.AgentDir)
	if snapshotErr != nil {
		input.SourceErrors = append(input.SourceErrors, "capture coherent launch snapshot: "+snapshotErr.Error())
		return finish()
	}
	if digestJSON(row.liveness.LaunchRecord) != digestJSON(rec) {
		input.SourceErrors = append(input.SourceErrors, "lead launch record drifted during status assessment")
	}
	input.Binding.LaunchRecordDigest = digest
	input.Binding.LaunchRecordModTime = modTime
	if rec.BootstrapExpectation != nil {
		input.Binding.LaunchID = strings.TrimSpace(rec.BootstrapExpectation.LaunchID)
	}
	input.Binding.LaunchStartedAt = rec.StartedAt.UTC()
	input.Binding.PreparedRunGeneration = strings.TrimSpace(rec.PreparedRunGeneration)
	input.Binding.PreparedRunDigest = strings.TrimSpace(rec.PreparedRunDigest)
	input.Binding.PreparedLaunchAttempt = strings.TrimSpace(rec.PreparedRunLaunchAttempt)
	input.Binding.PreparedGoalNamespace = strings.TrimSpace(rec.PreparedRunGoalNamespace)
	input.Binding.PreparedGoalDigest = strings.TrimSpace(rec.PreparedRunGoalDigest)
	paneID := ""
	if rec.Tmux != nil {
		paneID = strings.TrimSpace(rec.Tmux.PaneID)
		input.Binding.Pane = GoalSupervisionPaneIdentity{
			Managed: paneID != "" && goalSupervisionManagedTmuxTarget(rec.Tmux.Target),
			Session: rec.Tmux.Session, WindowID: rec.Tmux.WindowID, WindowName: rec.Tmux.WindowName,
			PaneID: paneID, Target: rec.Tmux.Target,
		}
	}
	runtimeIdentity := classifyLaunchRuntimeIdentity(rec, leadMember.Binary, paneID, launchRuntimeProbeFromDuplicate(probe))
	paneInspection := goalSupervisionPaneInspector(paneID)
	paneObservationKnown := paneInspection.State == tmuxpane.PaneInspectionFound ||
		paneInspection.State == tmuxpane.PaneInspectionGone
	paneLive := false
	if paneInspection.State == tmuxpane.PaneInspectionFound {
		paneLive = goalSupervisionExactPaneIdentity(
			paneInspection.Pane, rec.Tmux, session, input.Binding.LeadRole,
		)
		if !paneLive {
			input.SourceErrors = append(input.SourceErrors, "live tmux pane does not match the exact recorded lead identity")
			paneObservationKnown = false
		}
	} else if !paneObservationKnown {
		detail := strings.TrimSpace(paneInspection.Detail)
		if detail == "" {
			detail = string(paneInspection.State)
		}
		input.SourceErrors = append(input.SourceErrors, "exact lead pane inspection unavailable: "+detail)
	}
	input.Binding.Runtime = GoalSupervisionRuntimeIdentity{
		Known: probe.PIDAlive != nil && probe.ProcessMatch != nil &&
			probe.ProcessTTY != nil && probe.ProcessStartTime != nil && paneObservationKnown,
		Live: runtimeIdentity.PIDLive || paneLive, PIDLive: runtimeIdentity.PIDLive, PaneLive: paneLive,
		PIDAlive: runtimeIdentity.PIDAlive, BinaryMatch: runtimeIdentity.BinaryMatch,
	}
	if paneLive {
		input.Binding.Pane.Busy, input.Binding.Pane.BusyKnown = goalSupervisionPaneBusy(paneID)
		blocker, observed, err := goalSupervisionLocalInputDetector(paneID)
		if err != nil {
			input.SourceErrors = append(input.SourceErrors, "inspect lead local input: "+err.Error())
		} else {
			input.LocalInput = GoalSupervisionLocalInputEvidence{
				Known: true, Observed: observed, Kind: blocker.Kind, Summary: blocker.Summary,
				Destructive: blocker.Destructive,
			}
		}
	}
	bindingData := goalBindingForStatus(
		ns, newSessionStatusContext(t, profile, session, firstLiveTmuxSession(rows)), rows,
	)
	if rec.GoalBinding != nil {
		input.Binding.Goal = GoalSupervisionGoalIdentity{
			Mode: rec.GoalBinding.Mode, NativeGoal: rec.GoalBinding.NativeGoal,
			StateKnown: bindingData.Verified, Verified: bindingData.Verified,
			Source: rec.GoalBinding.Source, DeliveryState: rec.GoalBinding.DeliveryState,
			GoalDigest: digestGoalSupervisionString(rec.GoalBinding.Goal), AttemptID: strings.TrimSpace(rec.GoalBinding.AttemptID),
			BindingDigest: digestJSON(*rec.GoalBinding), CommandDigest: digestGoalSupervisionString(rec.GoalBinding.Command),
		}
		if nativeGoalBindingBlocked(rec.GoalBinding) {
			goal, attemptID, err := goalSupervisionBlockedBindingVerifier(
				t, profile, session, leadMember, rec,
			)
			if err != nil {
				input.SourceErrors = append(input.SourceErrors, "verify blocked native goal binding: "+err.Error())
				input.Binding.Goal.StateKnown = false
				input.Binding.Goal.Verified = false
			} else {
				input.Binding.Goal.ContentExact = bindingData.Verified
				input.Binding.Goal.GoalDigest = digestGoalSupervisionString(goal)
				input.Binding.Goal.AttemptID = attemptID
			}
		} else {
			input.Binding.Goal.ContentExact = bindingData.Verified
		}
		input.Binding.PauseGeneration = goalSupervisionPauseGeneration(input.Binding)
	}
	if goalSupervisionNativePaused(input.Binding.Goal) {
		detail := strings.TrimSpace(rec.GoalBinding.Detail)
		input.Blocker = GoalSupervisionBlockerEvidence{
			ID: digestGoalSupervisionString(detail), Known: detail != "", Detail: detail,
		}
	}
	return finish()
}

func goalSupervisionPauseGeneration(binding GoalSupervisionBinding) string {
	return digestJSON(struct {
		LaunchID      string
		BindingDigest string
		AttemptID     string
		Mode          string
	}{
		LaunchID:      binding.LaunchID,
		BindingDigest: binding.Goal.BindingDigest,
		AttemptID:     binding.Goal.AttemptID,
		Mode:          binding.Goal.Mode,
	})
}

func readGoalSupervisionLaunchSnapshot(agentDir string) (launch.Record, string, int64, error) {
	path := launch.ExistingPath(agentDir)
	file, err := os.Open(path)
	if err != nil {
		return launch.Record{}, "", 0, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return launch.Record{}, "", 0, err
	}
	payload, err := io.ReadAll(file)
	if err != nil {
		return launch.Record{}, "", 0, err
	}
	var rec launch.Record
	if err := json.Unmarshal(payload, &rec); err != nil {
		return launch.Record{}, "", 0, fmt.Errorf("parse %s: %w", path, err)
	}
	return rec, digestBytes(payload), info.ModTime().UnixNano(), nil
}

func verifyGoalSupervisionBlockedBinding(
	t team.Team,
	profile, session string,
	member team.Member,
	rec launch.Record,
) (string, string, error) {
	binding := rec.GoalBinding
	goal, attemptID, err := verifyGoalSupervisionBlockedBindingContents(
		t, profile, session, member, binding,
	)
	if err != nil {
		return "", "", err
	}
	manifest, digest, err := readPreparedRunManifestSnapshot(t.Project, profile, session)
	if err != nil {
		return "", "", fmt.Errorf("read accepted prepared goal: %w", err)
	}
	if err := verifyGoalSupervisionPreparedGoal(
		goal, profile, session, rec, manifest, digest,
	); err != nil {
		return "", "", err
	}
	return goal, attemptID, nil
}

func verifyGoalSupervisionBlockedBindingContents(
	t team.Team,
	profile, session string,
	member team.Member,
	binding *launch.GoalBinding,
) (string, string, error) {
	if binding == nil || binding.Mode != "native_goal_blocked" || !binding.NativeGoal {
		return "", "", fmt.Errorf("binding is not a blocked native goal")
	}
	if binding.Source != "goal-runtime" || binding.DeliveryState != "blocked" {
		return "", "", fmt.Errorf("binding source/delivery is not exact blocked runtime evidence")
	}
	if !goalSupervisionAllNonBlank(binding.Goal, binding.AttemptID, binding.Command) {
		return "", "", fmt.Errorf("typed goal, attempt, and command are required")
	}
	goal, attemptID, err := parseNativeGoalBindingCommand(binding.Command)
	if err != nil {
		return "", "", err
	}
	if binding.Goal != goal || strings.TrimSpace(binding.AttemptID) != attemptID {
		return "", "", fmt.Errorf("typed goal or attempt differs from the generated command")
	}
	contract, err := goalDeliveryContractForBinary(member.Binary)
	if err != nil || !contract.NativeGoal {
		return "", "", fmt.Errorf("lead binary does not support a native goal contract")
	}
	if binding.Command != contract.prompt(goal, t, profile, session, member.Role, attemptID) {
		return "", "", fmt.Errorf("blocked binding command differs from the exact generated command")
	}
	return goal, attemptID, nil
}

func verifyGoalSupervisionPreparedGoal(
	goal, profile, session string,
	rec launch.Record,
	manifest preparedRunManifest,
	digest string,
) error {
	token := preparedRunTokenFromRecord(rec)
	if !token.complete() || strings.TrimSpace(token.LaunchAttempt) == "" {
		return fmt.Errorf("prepared launch identity is incomplete")
	}
	if err := validatePreparedRunToken(token, manifest, digest); err != nil {
		return fmt.Errorf("prepared generation mismatch: %w", err)
	}
	if !squadnamespace.ProfilesEqual(manifest.Profile, profile) ||
		manifest.Session != session ||
		manifest.Namespace != squadnamespace.ID(profile, session) ||
		manifest.GoalText != goal ||
		manifest.GoalNamespace != squadnamespace.ID(profile, session) ||
		manifest.GoalDigest != strings.TrimSpace(rec.PreparedRunGoalDigest) {
		return fmt.Errorf("blocked goal differs from the accepted prepared-run goal")
	}
	return nil
}

func goalSupervisionExactPaneIdentity(
	pane tmuxpane.TmuxPane,
	recorded *launch.TmuxInfo,
	session, role string,
) bool {
	if recorded == nil {
		return false
	}
	return goalSupervisionAllNonBlank(
		recorded.PaneID, recorded.WindowID, recorded.Session,
		pane.PaneID, pane.WindowID, pane.Session, pane.Title,
	) &&
		pane.PaneID == recorded.PaneID &&
		pane.WindowID == recorded.WindowID &&
		pane.Session == recorded.Session &&
		pane.Title == paneTitleToken(session, role)
}

func goalSupervisionLifecycleObservation(
	snapshot *activity.Snapshot,
	observedAt time.Time,
) GoalSupervisionLifecycleEvidence {
	if snapshot == nil || snapshot.Source != activity.SourceHeartbeat ||
		snapshot.WrittenAt.IsZero() || observedAt.Before(snapshot.WrittenAt) ||
		observedAt.Sub(snapshot.WrittenAt) > activity.DefaultStaleAfter {
		return GoalSupervisionLifecycleEvidence{}
	}
	phase := strings.TrimSpace(snapshot.Phase)
	if !goalSupervisionLifecyclePhaseKnown(phase) {
		return GoalSupervisionLifecycleEvidence{}
	}
	return GoalSupervisionLifecycleEvidence{
		Known: true, Fresh: true, Source: snapshot.Source,
		Phase: phase,
	}
}

func goalSupervisionLifecyclePhaseKnown(phase string) bool {
	switch phase {
	case "goal_blocked", "parked_waiting_amq", "goal_terminal":
		return true
	default:
		return false
	}
}

func goalSupervisionManagedTmuxTarget(target string) bool {
	switch target {
	case "current-window", "new-window", "new-session":
		return true
	default:
		return false
	}
}

func inspectGoalSupervisionGates(
	t team.Team,
	profile, session string,
	observedSessionRoot string,
	probe duplicateLaunchProbe,
	now time.Time,
) goalSupervisionGateObservation {
	operator := team.EffectiveOperator(t)
	if !team.SupportsOperatorGates(t) || !operator.Enabled {
		return goalSupervisionGateObservation{
			Evidence: GoalSupervisionGateEvidence{Known: true},
		}
	}
	ns := squadnamespace.Resolve(t.Project, profile, session)
	root := strings.TrimSpace(observedSessionRoot)
	if root == "" {
		root = strings.TrimSpace(ns.AMQRoot)
	}
	if root == "" {
		return goalSupervisionGateObservation{
			SourceErrors: []string{"operator gate namespace root is unavailable"},
		}
	}
	baseRoot := root
	if squadnamespace.ProfilesEqual(profile, team.DefaultProfile) {
		baseRoot = filepath.Dir(root)
	}
	stateProbe := state.Probe{
		PIDAlive:         probe.PIDAlive,
		ProcessMatch:     probe.ProcessMatch,
		ProcessTTY:       probe.ProcessTTY,
		ProcessStartTime: probe.ProcessStartTime,
		Now:              func() time.Time { return now },
	}
	data, err := buildOperatorStatusData(operatorExecution{
		ProjectDir: t.Project,
		Profile:    profile,
		Session:    session,
		BaseRoot:   baseRoot,
		ReadOnly:   true,
		Probe:      stateProbe,
		Now:        func() time.Time { return now },
	})
	if err != nil {
		return goalSupervisionGateObservation{
			SourceErrors: []string{"inspect durable operator gates: " + err.Error()},
		}
	}
	if data.Operator.Poll == nil {
		return goalSupervisionGateObservation{
			SourceErrors: []string{"durable operator gate projection is unavailable"},
		}
	}
	var sourceErrors []string
	for _, warning := range data.sourceWarnings {
		detail := strings.TrimSpace(warning.Reason)
		if path := strings.TrimSpace(warning.Path); path != "" {
			detail = path + ": " + detail
		}
		if detail == "" {
			detail = "durable operator gate scan reported an unspecified warning"
		}
		sourceErrors = append(sourceErrors, "inspect durable operator gates: "+detail)
	}
	return goalSupervisionGateObservation{
		Evidence: GoalSupervisionGateEvidence{
			Known:     true,
			Open:      data.Operator.Poll.OpenGates,
			Ambiguous: len(sourceErrors) > 0,
		},
		SourceErrors: stableUniqueStrings(sourceErrors),
	}
}

func digestGoalSupervisionString(value string) string {
	if strings.TrimSpace(value) == "" {
		return ""
	}
	return digestBytes([]byte(value))
}

func goalSupervisionInvariantStrings(errors []executionInvariantError) []string {
	values := make([]string, 0, len(errors))
	for _, invariant := range errors {
		code := strings.TrimSpace(invariant.Code)
		message := strings.TrimSpace(invariant.Message)
		switch {
		case code != "" && message != "":
			values = append(values, code+": "+message)
		case code != "":
			values = append(values, code)
		case message != "":
			values = append(values, message)
		}
	}
	return stableUniqueStrings(values)
}

// canonicalExpectationFrom returns the canonical resume action this assessment published. ONE
// derivation: the executor and the dry-run both compare against this rather than each restating what
// canonical means.
func (a GoalSupervisionAction) canonicalExpectationFrom(assessment GoalSupervisionAssessment) GoalSupervisionAction {
	return assessment.Actions.Resume
}

// canonicalMismatch names the FIRST field that differs, or "" when the action matches canon.
//
// One field per comparison, deliberately: a single-field mutation must fail with that field named,
// because a compound "metadata mismatch" tells an operator nothing about what to look at. Kind and ID
// are both checked because runtimeaction.Action carries both and they are separately mutable.
func (a GoalSupervisionAction) canonicalMismatch(expected GoalSupervisionAction, assessment GoalSupervisionAssessment) string {
	for _, c := range []struct{ name, got, want, why string }{
		{"kind", a.Kind, expected.Kind, "a different kind is not the native goal resume this contract governs"},
		{"id", a.ID, expected.ID, "the id identifies the published action; a different id is a different action"},
		{"action kind", a.ActionKind, expected.ActionKind, "the canonical classification must match or surface and executor disagree about what this is"},
		{"scope", a.Scope, expected.Scope, "an action scoped elsewhere targets something other than this agent"},
		{"namespace", a.NamespaceID, expected.NamespaceID, "a foreign namespace belongs to a different profile/session"},
		{"invocation command", a.Command, expected.Command, "the invocation template differs from the published one"},
	} {
		if strings.TrimSpace(c.got) != strings.TrimSpace(c.want) {
			return fmt.Sprintf("action %s %q does not match the canonical %q: %s", c.name, c.got, c.want, c.why)
		}
	}
	if !a.Mutates {
		return "the action does not declare Mutates, which would bypass every audit path keyed on that flag"
	}
	if a.Available != assessment.AutomaticResumeAllowed {
		return fmt.Sprintf("action reports Available=%t while the assessment concludes AutomaticResumeAllowed=%t",
			a.Available, assessment.AutomaticResumeAllowed)
	}
	if a.NeedsConfirmation != expected.NeedsConfirmation {
		return fmt.Sprintf("action reports NeedsConfirmation=%t while the canonical action for this policy requires %t",
			a.NeedsConfirmation, expected.NeedsConfirmation)
	}
	// P3 truth, BOTH DIRECTIONS. I enforced only the available=>blank half, which let an UNAVAILABLE
	// action carry a blank or substituted Reason straight through the canonical gate -- and the Reason
	// is the operator's only statement of WHY it cannot run, so a substituted one misdirects them and
	// a blank one tells them nothing. Comparing against the canonical expectation covers both halves
	// with one assertion and keeps the value deterministic rather than merely present.
	if strings.TrimSpace(a.Reason) != strings.TrimSpace(expected.Reason) {
		return fmt.Sprintf("action Reason %q does not match the canonical %q: the reason is the operator's "+
			"only explanation of why an action cannot run, so a substituted or missing one misdirects them",
			a.Reason, expected.Reason)
	}
	return ""
}
