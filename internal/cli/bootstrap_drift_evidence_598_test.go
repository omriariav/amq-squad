package cli

import (
	"strings"
	"testing"
)

// TestPreparedBootstrapDriftEvidenceNamesEveryKnownFact is the #598 root cause 4
// regression.
//
// The row used to say only "generated bootstrap differs from accepted preview",
// which forced the operator to reproduce the pane command by hand to learn
// anything at all. That is how the fresh-namespace brick stayed undiagnosed. The
// evidence must carry every fact known at detection time.
func TestPreparedBootstrapDriftEvidenceNamesEveryKnownFact(t *testing.T) {
	got := preparedBootstrapDriftEvidence("squad", "v2-26-0", "cto", "gen0001", "sha256:accepted", "sha256:generated")
	for _, want := range []string{
		"squad/v2-26-0",      // namespace
		"role=cto",           // which member drifted
		"generation=gen0001", // which accepted generation
		"sha256:accepted",    // what was accepted
		"sha256:generated",   // what was rendered now
	} {
		if !strings.Contains(got, want) {
			t.Errorf("drift evidence omits %q\n  evidence: %s", want, got)
		}
	}
}

// TestPreparedBootstrapDriftFixPromisesOnlyWhatExists guards the fix text
// against reintroducing the exact defect it replaces.
//
// The old remedy said "review the bootstrap diff", an action the CLI cannot
// perform: the accepted bootstrap text is never persisted, so there is nothing
// to diff against. A fix text that overstates the tool's capability is the RC4
// defect itself, so the replacement must name a real action and must be honest
// that the diff does not exist yet.
func TestPreparedBootstrapDriftFixPromisesOnlyWhatExists(t *testing.T) {
	// It must name an action that exists today.
	for _, want := range []string{"--prepare", "re-run preparation"} {
		if !strings.Contains(preparedBootstrapDriftFix, want) {
			t.Errorf("fix text does not name a performable action (%q missing): %s", want, preparedBootstrapDriftFix)
		}
	}
	// It must be explicit that the diff is not available, and say where it is
	// coming from, so the gap is tracked rather than merely unmentioned.
	for _, want := range []string{"not available", "#609"} {
		if !strings.Contains(preparedBootstrapDriftFix, want) {
			t.Errorf("fix text does not disclose the missing diff capability (%q missing): %s", want, preparedBootstrapDriftFix)
		}
	}
	// It must NOT instruct the operator to review a diff, which is what the
	// defect said. Guarding the literal phrasing is deliberate: this is a
	// message-honesty regression, and the message is the whole artifact.
	if strings.Contains(preparedBootstrapDriftFix, "review the bootstrap diff") {
		t.Errorf("fix text still instructs an impossible action: %s", preparedBootstrapDriftFix)
	}
}
