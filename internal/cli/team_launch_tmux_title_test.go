package cli

import (
	"testing"
)

// paneTitleToken is the single source of truth for the stamped title and MUST
// mirror the resolver's expectedPaneToken. Format: amq:<session>:<role>.
func TestPaneTitleToken_Format(t *testing.T) {
	if got := paneTitleToken("beta", "cpo"); got != "amq:beta:cpo" {
		t.Fatalf("paneTitleToken = %q, want amq:beta:cpo", got)
	}
	if got := paneTitleToken("issue-96", "cto"); got != "amq:issue-96:cto" {
		t.Fatalf("paneTitleToken = %q, want amq:issue-96:cto", got)
	}
}
