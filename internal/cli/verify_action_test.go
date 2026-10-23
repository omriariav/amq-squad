package cli

import (
	"testing"
)

func TestVerifyActionDecisionRequiresPrefixToken(t *testing.T) {
	for _, tc := range []struct {
		text string
		want string
	}{
		{"APPROVED: release", actionDecisionApproved},
		{"approved", actionDecisionApproved},
		{"DENIED: hold", actionDecisionDenied},
		{"ANSWER: not approved yet", actionDecisionPending},
		{"DISAPPROVED: no", actionDecisionPending},
		{"unapproved", actionDecisionPending},
		{"Approved? No, denied.", actionDecisionPending},
		{"APPROVED: ship\nDENIED: hold", actionDecisionDenied},
	} {
		if got := classifyDecision(tc.text); got != tc.want {
			t.Fatalf("classifyDecision(%q) = %q, want %q", tc.text, got, tc.want)
		}
	}
}
