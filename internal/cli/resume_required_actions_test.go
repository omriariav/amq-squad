package cli

import (
	"strings"
	"testing"
)

func TestResumeActionIDIsNamespacedAndDeterministic(t *testing.T) {
	id := resumeActionID(resumeActionKindLeadNotLive, "qa")
	if id != "amq-squad/lead_not_live:qa" {
		t.Fatalf("resumeActionID = %q, want amq-squad/lead_not_live:qa", id)
	}
	if !isResumeNamespacedActionID(id) {
		t.Fatalf("isResumeNamespacedActionID(%q) = false, want true", id)
	}
	if isResumeNamespacedActionID("launchapi-required-action-42") {
		t.Fatal("a plain launchapi ActionID must not be classified as amq-squad-namespaced")
	}
}

func TestSplitResumeDecisionsSeparatesNamespaces(t *testing.T) {
	supplied := map[string]string{
		resumeActionID(resumeActionKindLeadNotLive, "qa"):             string(resumeDecisionProceedWithoutLead),
		resumeActionID(resumeActionKindExternalLeadRecordDead, "cto"): string(resumeDecisionAbort),
		"some-launchapi-action-id":                                    "trust_exact_subject",
	}
	resumeAnswers, launchapiAnswers := splitResumeDecisions(supplied)
	if len(resumeAnswers) != 2 {
		t.Fatalf("resumeAnswers = %+v, want exactly the 2 amq-squad-namespaced entries", resumeAnswers)
	}
	if len(launchapiAnswers) != 1 || launchapiAnswers["some-launchapi-action-id"] != "trust_exact_subject" {
		t.Fatalf("launchapiAnswers = %+v, want exactly the 1 non-namespaced entry untouched", launchapiAnswers)
	}
	// The amq-squad half must never leak into what would be forwarded to
	// launchapi's ApplyRequest.Decisions.
	for actionID := range launchapiAnswers {
		if isResumeNamespacedActionID(actionID) {
			t.Fatalf("amq-squad-namespaced action %q leaked into the launchapi answer set", actionID)
		}
	}
}

func TestSplitResumeDecisionsEmptyInputYieldsNilMaps(t *testing.T) {
	resumeAnswers, launchapiAnswers := splitResumeDecisions(nil)
	if resumeAnswers != nil || launchapiAnswers != nil {
		t.Fatalf("splitResumeDecisions(nil) = (%+v, %+v), want (nil, nil)", resumeAnswers, launchapiAnswers)
	}
}

func TestResolveResumeRequiredActionsDecidesAndReportsMissing(t *testing.T) {
	actions := []resumeRequiredAction{
		newLeadNotLiveAction("qa"),
		newExternalLeadRecordDeadAction("cto"),
	}
	supplied := map[string]string{
		resumeActionID(resumeActionKindLeadNotLive, "qa"): string(resumeDecisionProceedWithoutLead),
	}
	decided, missing, err := resolveResumeRequiredActions(actions, supplied)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(decided) != 1 || decided[resumeActionID(resumeActionKindLeadNotLive, "qa")] != resumeDecisionProceedWithoutLead {
		t.Fatalf("decided = %+v, want exactly the qa lead_not_live decision", decided)
	}
	if len(missing) != 1 || missing[0].ActionID != resumeActionID(resumeActionKindExternalLeadRecordDead, "cto") {
		t.Fatalf("missing = %+v, want exactly the undecided cto external_lead_record_dead action", missing)
	}
}

func TestResolveResumeRequiredActionsRejectsStaleAnswer(t *testing.T) {
	actions := []resumeRequiredAction{newLeadNotLiveAction("qa")}
	supplied := map[string]string{
		resumeActionID(resumeActionKindLeadNotLive, "other-role"): string(resumeDecisionAbort),
	}
	_, _, err := resolveResumeRequiredActions(actions, supplied)
	if err == nil || !strings.Contains(err.Error(), "stale answer") {
		t.Fatalf("err = %v, want a stale-answer error", err)
	}
}

func TestResolveResumeRequiredActionsRejectsDisallowedChoice(t *testing.T) {
	actions := []resumeRequiredAction{newExternalLeadRecordDeadAction("cto")}
	supplied := map[string]string{
		resumeActionID(resumeActionKindExternalLeadRecordDead, "cto"): string(resumeDecisionProceedWithoutLead),
	}
	_, _, err := resolveResumeRequiredActions(actions, supplied)
	if err == nil || !strings.Contains(err.Error(), "not in the allowed set") {
		t.Fatalf("err = %v, want a not-in-the-allowed-set error", err)
	}
	if !strings.Contains(err.Error(), "abort") {
		t.Fatalf("err = %v, want it to name the allowed choice (abort)", err)
	}
}

func TestNewExternalLeadRecordDeadActionHasNoProceedBypass(t *testing.T) {
	action := newExternalLeadRecordDeadAction("cto")
	for _, choice := range action.AllowedDecisions {
		if choice == resumeDecisionProceedWithoutLead {
			t.Fatal("external_lead_record_dead must not offer a proceed-style bypass -- no prior mechanism ever allowed skipping this check")
		}
	}
	if len(action.AllowedDecisions) != 1 || action.AllowedDecisions[0] != resumeDecisionAbort {
		t.Fatalf("AllowedDecisions = %v, want exactly [abort]", action.AllowedDecisions)
	}
}
