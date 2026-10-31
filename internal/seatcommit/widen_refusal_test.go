package seatcommit

import (
	"os"
	"strings"
	"testing"
)

// The widen dry run refuses what the commit step refuses (a tampered
// ledger), and only a diff-policy refusal becomes crossings.

func TestWidenRefusesWhatTheDryRunRefuses(t *testing.T) {
	// a ledger edited without resealing
	g := newFixture(t)
	gtk := g.reported("cmd/")
	raw, err := os.ReadFile(ledgerPath(g.root, g.handle))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ledgerPath(g.root, g.handle), []byte(strings.Replace(string(raw), `"seat/s1"`, `"seat/s1x"`, 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	go_ := g.opts(gtk)
	d := CheckWiden(WidenOptions{Root: g.root, Task: gtk, Added: []string{"cmd/other.go"}, Grants: g.grants, RepoRoot: g.main, Home: g.home, Commit: &go_})
	if d.Allowed || d.ReasonCode != ReasonLedgerTampered {
		t.Fatalf("widen on a tampered ledger: %+v", d)
	}
}

// The diff-policy whitelist is exact: violations under another code refuse.
func TestOnlyTheDiffPolicyRefusalIsTurnedIntoCrossings(t *testing.T) {
	v := []Violation{{Reason: ReasonSizeCap}}
	if !diffPolicyRefusal(Result{ReasonCode: ReasonSizeCap, Violations: v}) {
		t.Fatal("a size-cap refusal is diff policy")
	}
	for name, res := range map[string]Result{
		"violations under another code": {ReasonCode: ReasonLedgerTampered, Violations: v},
		"no violations":                 {ReasonCode: ReasonBaseUnrecorded},
		"clean":                         {OK: true},
	} {
		if diffPolicyRefusal(res) {
			t.Errorf("%s was taken for diff policy", name)
		}
	}
}
