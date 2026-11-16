package routing

import (
	"testing"

	"github.com/omriariav/amq-squad/v3/internal/task"
)

// S2-1 (3.3.0): the three outcome sets task.OutcomeSets declares with a
// reader in this package, each tied to its reader. Every one of the eight
// outcomes is given to the reader; the reader's answer must equal the
// declared membership, and each conditional entry is driven both ways. The
// set's condition text is compared with the one this test drives, so a
// changed condition fails here until the test drives the new one.

func s2Set(t *testing.T, name string) task.OutcomeSetContract {
	t.Helper()
	for _, set := range task.OutcomeSets() {
		if set.Name == name {
			if set.ReaderPackage != "internal/routing" {
				t.Fatalf("outcome set %s is read in %s, not internal/routing", name, set.ReaderPackage)
			}
			return set
		}
	}
	t.Fatalf("no outcome set %s", name)
	return task.OutcomeSetContract{}
}

func s2In(values []string, v string) bool {
	for _, x := range values {
		if x == v {
			return true
		}
	}
	return false
}

func s2Condition(t *testing.T, set task.OutcomeSetContract, outcome, want string) bool {
	t.Helper()
	for _, c := range set.Conditional {
		if c.Outcome == outcome {
			if c.Condition != want {
				t.Fatalf("%s: the condition of %s is %q; this test drives %q", set.Name, outcome, c.Condition, want)
			}
			return true
		}
	}
	return false
}

// The three refusal details the conditions are driven with: a detail
// RefusalWorkFailure classifies true, one it classifies false, and one it
// does not classify.
var s2Details = []struct {
	name, detail string
	classified   *bool
}{
	{"classified true", task.BlockVerdictFailed, s2Bool(true)},
	{"classified false", task.ReasonWidenRequested, s2Bool(false)},
	{"unclassified", "s21_unclassified", nil},
}

func s2Bool(b bool) *bool { return &b }

func TestS2RouterRetryEffectSetIsItsReader(t *testing.T) {
	set := s2Set(t, "router_retry_effect")
	if set.ReaderFunc != "effect" {
		t.Fatalf("reader %s, want effect", set.ReaderFunc)
	}
	for _, d := range s2Details {
		if got, ok := RefusalWorkFailure[d.detail]; (d.classified == nil) != !ok || (ok && got != *d.classified) {
			t.Fatalf("the %s detail %q is not classified as this test assumes", d.name, d.detail)
		}
	}
	const classified = "the attempt detail is classified true in RefusalWorkFailure"
	const blockedDefault = "the attempt detail is classified true in RefusalWorkFailure, or is unclassified (blocked defaults to a work failure)"
	for _, outcome := range task.Outcomes() {
		for _, d := range s2Details {
			want := s2In(set.Outcomes, outcome)
			switch {
			case outcome == task.OutcomeCommitRefused && s2Condition(t, set, outcome, classified):
				want = want && d.classified != nil && *d.classified
			case outcome == task.OutcomeBlocked && s2Condition(t, set, outcome, blockedDefault):
				want = want && (d.classified == nil || *d.classified)
			}
			if got := effect(task.Attempt{Outcome: outcome, Detail: d.detail}).count; got != want {
				t.Errorf("effect(%s, %s).count = %v, the set says %v", outcome, d.name, got, want)
			}
		}
	}
}

func TestS2WeeklyReportFailedSetIsItsReader(t *testing.T) {
	set := s2Set(t, "weekly_report_failed")
	if set.ReaderFunc != "failedOutcome" || len(set.Conditional) != 0 {
		t.Fatalf("reader %s with %d conditionals, want failedOutcome with none", set.ReaderFunc, len(set.Conditional))
	}
	for _, outcome := range task.Outcomes() {
		if got, want := failedOutcome(outcome), s2In(set.Outcomes, outcome); got != want {
			t.Errorf("failedOutcome(%s) = %v, the set says %v", outcome, got, want)
		}
	}
}

func TestS2VerifierFailWithoutVerdictSetIsItsReader(t *testing.T) {
	set := s2Set(t, "verifier_fail_without_verdict")
	if set.ReaderFunc != "verifierResult" {
		t.Fatalf("reader %s, want verifierResult", set.ReaderFunc)
	}
	const noVerdict = "the verifier is mechanical and the attempt has no verdict"
	const blockedNoVerdict = "the verifier is mechanical, the attempt has no verdict, and the detail is not verdict_missing"
	cases := []struct {
		name, verifier, detail string
		verdict                *task.Verdict
		mechanical, unverdict  bool
	}{
		{"mechanical, no verdict", task.VerifierTests, "", nil, true, true},
		{"mechanical, no verdict, verdict_missing", task.VerifierTests, task.BlockVerdictMissing, nil, true, true},
		{"not mechanical", task.VerifierNone, "", nil, false, true},
		{"mechanical, a pass verdict", task.VerifierTests, "", &task.Verdict{Result: task.VerdictPass}, true, false},
	}
	for _, outcome := range task.Outcomes() {
		for _, c := range cases {
			want := s2In(set.Outcomes, outcome)
			switch {
			case outcome == task.OutcomeBlocked && s2Condition(t, set, outcome, blockedNoVerdict):
				want = want && c.mechanical && c.unverdict && c.detail != task.BlockVerdictMissing
			case s2Condition(t, set, outcome, noVerdict):
				want = want && c.mechanical && c.unverdict
			}
			got := verifierResult(c.verifier, task.Attempt{Outcome: outcome, Detail: c.detail, Verdict: c.verdict}) == VerifierFail
			if got != want {
				t.Errorf("verifierResult(%s, %s) is fail: %v, the set says %v", outcome, c.name, got, want)
			}
		}
	}
}
