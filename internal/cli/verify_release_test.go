package cli

import (
	"strings"
	"testing"
)

func validReleaseEvidence() verifyReleaseEvidence {
	return verifyReleaseEvidence{
		Subject:          "release v2.14.0",
		Version:          "v2.14.0",
		ReleaseCommitSHA: "deadbeef",
		CI:               verifyMergeCheck{State: "success", SHA: "deadbeef", Source: "make ci", CheckedAt: "2026-06-30T20:00:00Z"},
		Cosign:           verifyReleaseCosign{State: "approved", SHA: "deadbeef", Reviewer: "senior-dev", DistinctFromReleaseLead: true, Source: "AMQ cosign", CheckedAt: "2026-06-30T20:01:00Z"},
		OperatorApproval: verifyReleaseOperatorApproval{Approved: true, Gate: "gate/release-2-14-0", Source: "operator", CheckedAt: "2026-06-30T20:02:00Z"},
	}
}

func TestVerifyReleasePassesExactSHACosign(t *testing.T) {
	dir := t.TempDir()
	evidence := writeVerifyEvidence(t, dir, `{
  "subject": "release v2.14.0",
  "version": "v2.14.0",
  "release_commit_sha": "deadbeef",
  "ci": {"state": "success", "sha": "deadbeef", "source": "make ci", "checked_at": "2026-06-30T20:00:00Z"},
  "cosign": {"state": "approved", "sha": "deadbeef", "reviewer": "senior-dev", "distinct_from_release_lead": true, "source": "AMQ", "checked_at": "2026-06-30T20:01:00Z"},
  "operator_release_approval": {"approved": true, "gate": "gate/release-2-14-0", "source": "operator", "checked_at": "2026-06-30T20:02:00Z"}
}`)
	stdout, _, err := captureOutput(t, func() error {
		return runVerify([]string{"release", "--evidence", evidence})
	})
	if err != nil {
		t.Fatalf("verify release: %v", err)
	}
	if !strings.Contains(stdout, "release preflight passed for release v2.14.0 at deadbeef") {
		t.Fatalf("unexpected pass output:\n%s", stdout)
	}
}

func TestVerifyReleaseJSONEnvelope(t *testing.T) {
	dir := t.TempDir()
	evidence := writeVerifyEvidence(t, dir, `{
  "subject": "release v2.14.0",
  "version": "v2.14.0",
  "release_commit_sha": "deadbeef",
  "ci": {"state": "success", "sha": "deadbeef", "source": "make ci", "checked_at": "t"},
  "cosign": {"state": "approved", "sha": "deadbeef", "reviewer": "senior-dev", "distinct_from_release_lead": true, "source": "AMQ", "checked_at": "t"},
  "operator_release_approval": {"approved": true, "gate": "gate/release-2-14-0", "source": "operator", "checked_at": "t"}
}`)
	stdout, _, err := captureOutput(t, func() error {
		return runVerify([]string{"release", "--evidence", evidence, "--json"})
	})
	if err != nil {
		t.Fatalf("verify release --json: %v", err)
	}
	env := decodeJSONEnvelope[verifyReleaseResult](t, stdout)
	if env.Kind != "verify_release" || !env.Data.OK || env.Data.ReleaseCommitSHA != "deadbeef" || env.Data.EvidenceSummary == nil {
		t.Fatalf("unexpected release envelope: %+v", env)
	}
}

func TestVerifyReleaseFailureCodes(t *testing.T) {
	tests := []struct {
		name     string
		mutate   func(e *verifyReleaseEvidence)
		wantCode string
	}{
		{"missing_release_commit_sha", func(e *verifyReleaseEvidence) { e.ReleaseCommitSHA = ""; e.CI.SHA = ""; e.Cosign.SHA = "" }, "missing_release_commit_sha"},
		{"missing_cosign", func(e *verifyReleaseEvidence) { e.Cosign = verifyReleaseCosign{} }, "cosign_missing_state"},
		{"cosign_not_approved", func(e *verifyReleaseEvidence) { e.Cosign.State = "pending" }, "cosign_state_not_approved"},
		{"stale_cosign_sha", func(e *verifyReleaseEvidence) { e.Cosign.SHA = "oldsha" }, "cosign_stale_sha"},
		{"missing_distinct_actor", func(e *verifyReleaseEvidence) { e.Cosign.DistinctFromReleaseLead = false }, "cosign_not_distinct_actor"},
		{"missing_reviewer", func(e *verifyReleaseEvidence) { e.Cosign.Reviewer = "" }, "cosign_missing_reviewer"},
		{"ci_sha_mismatch", func(e *verifyReleaseEvidence) { e.CI.SHA = "othersha" }, "ci_stale_sha"},
		{"ci_not_success", func(e *verifyReleaseEvidence) { e.CI.State = "failure" }, "ci_state_not_success"},
		{"missing_operator_approval", func(e *verifyReleaseEvidence) { e.OperatorApproval.Approved = false }, "operator_approval_not_approved"},
		{"operator_approval_missing_reference", func(e *verifyReleaseEvidence) { e.OperatorApproval.Gate = ""; e.OperatorApproval.Source = "" }, "operator_approval_missing_reference"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := validReleaseEvidence()
			tc.mutate(&e)
			result := validateVerifyReleaseEvidence(e)
			if result.OK {
				t.Fatalf("validateVerifyReleaseEvidence OK, want failure %q", tc.wantCode)
			}
			for _, f := range result.Failures {
				if f.Code == tc.wantCode {
					return
				}
			}
			t.Fatalf("missing failure code %q in %#v", tc.wantCode, result.Failures)
		})
	}
}

// TestVerifyReleaseGatesAreNonSubstitutable proves the key invariant: an operator
// approval cannot satisfy the co-sign, and a co-sign cannot satisfy the operator
// approval — both are required for a publish-ready release.
func TestVerifyReleaseGatesAreNonSubstitutable(t *testing.T) {
	// Operator approved, but no co-sign → still fails.
	e1 := validReleaseEvidence()
	e1.Cosign = verifyReleaseCosign{}
	if validateVerifyReleaseEvidence(e1).OK {
		t.Fatal("operator approval must not substitute for the developer co-sign")
	}
	// Co-sign present, but operator not approved → still fails.
	e2 := validReleaseEvidence()
	e2.OperatorApproval.Approved = false
	if validateVerifyReleaseEvidence(e2).OK {
		t.Fatal("developer co-sign must not substitute for operator release approval")
	}
}
