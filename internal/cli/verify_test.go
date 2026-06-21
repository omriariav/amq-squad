package cli

import (
	"os"
	"strings"
	"testing"
)

func TestVerifyMergePassesCleanEvidence(t *testing.T) {
	dir := t.TempDir()
	evidence := writeVerifyEvidence(t, dir, `{
  "subject": "PR #164",
  "head_sha": "abc123",
  "ci": {"state": "success", "sha": "abc123", "source": "local make ci", "checked_at": "2026-06-21T16:00:00Z"},
  "review": {"state": "clean", "sha": "abc123", "source": "cto", "checked_at": "2026-06-21T16:01:00Z"},
  "exceptions": []
}`)

	stdout, _, err := captureOutput(t, func() error {
		return runVerify([]string{"merge", "--evidence", evidence})
	})
	if err != nil {
		t.Fatalf("verify merge: %v", err)
	}
	if !strings.Contains(stdout, "merge preflight passed for PR #164 at abc123") {
		t.Fatalf("unexpected pass output:\n%s", stdout)
	}
}

func TestVerifyMergeReportsFailuresInJSON(t *testing.T) {
	dir := t.TempDir()
	evidence := writeVerifyEvidence(t, dir, `{
  "subject": "PR #164",
  "head_sha": "abc123",
  "ci": {"state": "pending", "sha": "old456", "source": "ci", "checked_at": "2026-06-21T16:00:00Z"},
  "review": {"state": "dirty", "sha": "abc123", "source": "review", "checked_at": "2026-06-21T16:01:00Z"},
  "exceptions": [{"name": "shared infra", "approved": false}]
}`)

	stdout, _, err := captureOutput(t, func() error {
		return runVerify([]string{"merge", "--evidence", evidence, "--json"})
	})
	if err == nil || !strings.Contains(err.Error(), "merge preflight failed") {
		t.Fatalf("want failed preflight error, got %v", err)
	}
	for _, want := range []string{
		`"kind": "verify_merge"`,
		`"ok": false`,
		`"ci_state_not_success"`,
		`"ci_stale_sha"`,
		`"review_state_not_clean"`,
		`"unapproved_exception"`,
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("missing %q in JSON output:\n%s", want, stdout)
		}
	}
}

func TestVerifyMergeFailureCodes(t *testing.T) {
	tests := []struct {
		name     string
		evidence verifyMergeEvidence
		wantCode string
	}{
		{
			name: "missing_head_sha",
			evidence: verifyMergeEvidence{
				CI:     verifyMergeCheck{State: "success", SHA: "abc123"},
				Review: verifyMergeCheck{State: "clean", SHA: "abc123"},
			},
			wantCode: "missing_head_sha",
		},
		{
			name: "missing_state",
			evidence: verifyMergeEvidence{
				HeadSHA: "abc123",
				CI:      verifyMergeCheck{SHA: "abc123"},
				Review:  verifyMergeCheck{State: "clean", SHA: "abc123"},
			},
			wantCode: "ci_missing_state",
		},
		{
			name: "missing_sha",
			evidence: verifyMergeEvidence{
				HeadSHA: "abc123",
				CI:      verifyMergeCheck{State: "success"},
				Review:  verifyMergeCheck{State: "clean", SHA: "abc123"},
			},
			wantCode: "ci_missing_sha",
		},
		{
			name: "unnamed_exception",
			evidence: verifyMergeEvidence{
				HeadSHA:    "abc123",
				CI:         verifyMergeCheck{State: "success", SHA: "abc123"},
				Review:     verifyMergeCheck{State: "clean", SHA: "abc123"},
				Exceptions: []verifyMergeException{{Approved: true}},
			},
			wantCode: "unnamed_exception",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := validateVerifyMergeEvidence(tc.evidence)
			if result.OK {
				t.Fatalf("validateVerifyMergeEvidence OK, want failure %q", tc.wantCode)
			}
			for _, failure := range result.Failures {
				if failure.Code == tc.wantCode {
					return
				}
			}
			t.Fatalf("missing failure code %q in %#v", tc.wantCode, result.Failures)
		})
	}
}

func TestVerifyMergeRejectsMissingEvidenceFlag(t *testing.T) {
	if _, _, err := captureOutput(t, func() error {
		return runVerify([]string{"merge"})
	}); err == nil || !strings.Contains(err.Error(), "--evidence is required") {
		t.Fatalf("want --evidence required, got %v", err)
	}
}

func TestVerifyMergeRejectsUnknownFields(t *testing.T) {
	dir := t.TempDir()
	evidence := writeVerifyEvidence(t, dir, `{
  "subject": "PR #164",
  "head_sha": "abc123",
  "ci": {"state": "success", "sha": "abc123", "source": "ci", "checked_at": "2026-06-21T16:00:00Z"},
  "review": {"state": "clean", "sha": "abc123", "source": "review", "checked_at": "2026-06-21T16:01:00Z"},
  "exceptions": [],
  "provider": "github"
}`)
	if _, _, err := captureOutput(t, func() error {
		return runVerify([]string{"merge", "--evidence", evidence})
	}); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("want unknown-field decode error, got %v", err)
	}
}

func TestReadVerifyMergeEvidenceFromStdinReader(t *testing.T) {
	evidence, err := readVerifyMergeEvidence("-", strings.NewReader(`{
  "subject": "PR #164",
  "head_sha": "abc123",
  "ci": {"state": "success", "sha": "abc123", "source": "ci", "checked_at": "2026-06-21T16:00:00Z"},
  "review": {"state": "clean", "sha": "abc123", "source": "review", "checked_at": "2026-06-21T16:01:00Z"},
  "exceptions": [{"name": "sign-off pending", "approved": true, "gate": "gate/merge"}]
}`))
	if err != nil {
		t.Fatalf("read stdin evidence: %v", err)
	}
	result := validateVerifyMergeEvidence(evidence)
	if !result.OK {
		t.Fatalf("approved named exception should pass, got failures: %#v", result.Failures)
	}
}

func TestVerifyRequiresKnownSubcommand(t *testing.T) {
	if _, _, err := captureOutput(t, func() error {
		return runVerify([]string{"bogus"})
	}); err == nil || !strings.Contains(err.Error(), "unknown 'verify' subcommand") {
		t.Fatalf("want unknown verify subcommand, got %v", err)
	}
}

func writeVerifyEvidence(t *testing.T, dir, body string) string {
	t.Helper()
	path := dir + "/evidence.json"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}
