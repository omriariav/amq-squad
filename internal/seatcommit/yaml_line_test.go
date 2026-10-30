package seatcommit

import (
	"path/filepath"
	"strings"
	"testing"
)

// v3-5-verifier, the fifth round's P1: YAML on one line (a bare key, a
// quoted key, a flow mapping) and an indentless list under a credential key
// reach neither the verifier's capture in the evidence ledger nor the lead's
// tail.
func TestOneLineYAMLCredentialNeverReachesTheEvidence(t *testing.T) {
	value := "Ab9" + "xY7qR" + "Lm2Wq8Zt" // built at run time, never a literal
	for name, lines := range map[string][]string{
		"bare":       {"password: " + value},
		"quoted_key": {"'password': " + value},
		"flow":       {"creds: {password: " + value + ", user: kept}"},
		"indentless": {"password:", "- " + value, "user: kept"},
	} {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			write(t, filepath.Join(f.wt, "cmd", "root.go"), "package cmd\n\n// yaml\n")
			args := ""
			for _, l := range lines {
				args += ` "` + l + `"`
			}
			f.verifying(f.script("v", "printf '%s\\n'"+args+"\nexit 1\n"))
			res := f.run(f.reportedBare("cmd/**"))
			l, _ := f.evidence()
			if res.ReasonCode != "verdict_failed" || l.Payload.Output == "" {
				t.Fatalf("the run: %s", res.ReasonCode)
			}
			if strings.Contains(l.Payload.Output, value[:8]) || strings.Contains(res.Detail, value[:8]) {
				t.Fatal("a one-line YAML credential reached the ledger or the block detail")
			}
		})
	}
}
