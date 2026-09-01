package cli

import (
	"fmt"
	"strings"
)

// gh#758/t11 slice B: amq-squad's own synthesized safety facts that can
// block a `resume --exec` launch (a stale/dead lead) are shaped like
// launchapi.RequiredActionV1 for a consistent --launchapi-decision-style
// CLI answer surface, but are namespaced under "amq-squad/" so an ActionID
// can never collide with one launchapi mints, and are resolved entirely by
// resume's own code below -- never forwarded into launchapi's
// ApplyRequest.Decisions, which has no idea what they mean.
//
// Per cto's ruling these are two separate facts with separate remedies, not
// one unified action:
//   - lead_not_live: the launch-time check that the configured lead has a
//     live, operator-addressable pane (previously bypassed by
//     --skip-lead-check; the decision below replaces that flag).
//   - external_lead_record_dead: a dead externally-adopted lead record,
//     which always blocks (no bypass existed for this under the old
//     --skip-lead-check path either).

// resumeRequiredActionKind is the amq-squad-namespaced counterpart to
// launchapi.RequiredActionKindV1.
type resumeRequiredActionKind string

const (
	resumeActionKindLeadNotLive            resumeRequiredActionKind = "amq-squad/lead_not_live"
	resumeActionKindExternalLeadRecordDead resumeRequiredActionKind = "amq-squad/external_lead_record_dead"
)

// resumeDecisionChoice is resume's own local decision vocabulary for its
// synthesized required actions. Deliberately a distinct type from
// launchapi.DecisionChoiceV1 (not reused, not convertible): these decisions
// are answered entirely by resume's own code and never reach launchapi.
type resumeDecisionChoice string

const (
	resumeDecisionProceedWithoutLead resumeDecisionChoice = "proceed_without_lead"
	resumeDecisionAbort              resumeDecisionChoice = "abort"
)

// resumeRequiredAction is one amq-squad-synthesized safety fact blocking a
// resume --exec launch for a given role.
type resumeRequiredAction struct {
	ActionID         string
	Kind             resumeRequiredActionKind
	Role             string
	ReasonCode       string
	AllowedDecisions []resumeDecisionChoice
}

// resumeActionID builds the namespaced ACTION_ID for one (kind, role) pair,
// e.g. "amq-squad/lead_not_live:qa". Stable and deterministic so the same
// gate always gets the same ID across a preview and its --launchapi-decision
// answer.
func resumeActionID(kind resumeRequiredActionKind, role string) string {
	return string(kind) + ":" + role
}

// isResumeNamespacedActionID reports whether an ACTION_ID belongs to
// amq-squad's own synthesized-action namespace, as opposed to one of
// launchapi's own RequiredActionV1 ids.
func isResumeNamespacedActionID(actionID string) bool {
	return strings.HasPrefix(actionID, "amq-squad/")
}

// splitResumeDecisions partitions a combined ACTION_ID=CHOICE answer map
// (as parsed from repeated --launchapi-decision flags by
// parseLaunchapiDecisions) into amq-squad's own namespaced entries and
// everything else. The amq-squad half must never be forwarded into
// launchapi's ApplyRequest.Decisions; the remainder is launchapi's own
// concern, unchanged.
func splitResumeDecisions(supplied map[string]string) (resumeAnswers, launchapiAnswers map[string]string) {
	if len(supplied) == 0 {
		return nil, nil
	}
	resumeAnswers = make(map[string]string)
	launchapiAnswers = make(map[string]string)
	for actionID, choice := range supplied {
		if isResumeNamespacedActionID(actionID) {
			resumeAnswers[actionID] = choice
		} else {
			launchapiAnswers[actionID] = choice
		}
	}
	return resumeAnswers, launchapiAnswers
}

// newLeadNotLiveAction builds the synthesized action for the launch-time
// lead-liveness gate (replaces --skip-lead-check): the operator may
// explicitly proceed without a live lead pane, or abort.
func newLeadNotLiveAction(role string) resumeRequiredAction {
	return resumeRequiredAction{
		ActionID:         resumeActionID(resumeActionKindLeadNotLive, role),
		Kind:             resumeActionKindLeadNotLive,
		Role:             role,
		ReasonCode:       "lead_not_live",
		AllowedDecisions: []resumeDecisionChoice{resumeDecisionProceedWithoutLead, resumeDecisionAbort},
	}
}

// newExternalLeadRecordDeadAction builds the synthesized action for a dead,
// externally-adopted lead record. No prior mechanism ever offered a bypass
// for this specific fact (--skip-lead-check never covered it), so the only
// allowed decision is an explicit abort -- surfacing it as a required
// action still gives the operator a clean, named answer instead of a bare
// launch failure.
func newExternalLeadRecordDeadAction(role string) resumeRequiredAction {
	return resumeRequiredAction{
		ActionID:         resumeActionID(resumeActionKindExternalLeadRecordDead, role),
		Kind:             resumeActionKindExternalLeadRecordDead,
		Role:             role,
		ReasonCode:       "external_lead_record_dead",
		AllowedDecisions: []resumeDecisionChoice{resumeDecisionAbort},
	}
}

// resolveResumeRequiredActions mirrors resolveLaunchapiDecisions's contract
// (team_launch_launchapi.go) for amq-squad's own synthesized actions: a
// supplied answer for an action this call did not surface is a stale
// answer and errors; a choice outside that action's AllowedDecisions
// errors naming the allowed set; every action without a supplied answer is
// returned in missing for the caller to surface as a gate.
func resolveResumeRequiredActions(actions []resumeRequiredAction, supplied map[string]string) (decided map[string]resumeDecisionChoice, missing []resumeRequiredAction, err error) {
	byID := make(map[string]resumeRequiredAction, len(actions))
	for _, a := range actions {
		byID[a.ActionID] = a
	}
	for actionID := range supplied {
		if _, ok := byID[actionID]; !ok {
			return nil, nil, fmt.Errorf("resume: --launchapi-decision for action %q does not match any action this resume run surfaced (stale answer)", actionID)
		}
	}
	decided = make(map[string]resumeDecisionChoice, len(supplied))
	for _, action := range actions {
		raw, ok := supplied[action.ActionID]
		if !ok {
			missing = append(missing, action)
			continue
		}
		choice := resumeDecisionChoice(raw)
		allowed := false
		for _, c := range action.AllowedDecisions {
			if c == choice {
				allowed = true
				break
			}
		}
		if !allowed {
			return nil, nil, fmt.Errorf("resume: --launchapi-decision %s=%s is not in the allowed set for this %s action: allowed choices are %s",
				action.ActionID, raw, action.Kind, resumeDecisionChoicesToStrings(action.AllowedDecisions))
		}
		decided[action.ActionID] = choice
	}
	return decided, missing, nil
}

func resumeDecisionChoicesToStrings(choices []resumeDecisionChoice) string {
	strs := make([]string, len(choices))
	for i, c := range choices {
		strs[i] = string(c)
	}
	return strings.Join(strs, ", ")
}
