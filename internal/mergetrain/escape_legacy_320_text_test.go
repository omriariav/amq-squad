package mergetrain_test

import (
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/v3/internal/cli"
	"github.com/omriariav/amq-squad/v3/internal/mergetrain"
)

// Brief 320-train-escape-legacy, L3, the printed form: a train dry run over
// a root whose gate store holds a legacy record prints that record's line
// once, on its own line after the protection line, and nothing else about
// it; a run over a store with no such record prints what it printed before.
// Reverted (the line not printed), it is missing.
func TestTrainDryRunPrintsASkippedLegacyGateRecordOnce(t *testing.T) {
	before, after, line := mergetrain.LegacyDryRuns(t)
	if text := cli.FormatMergeTrain(before); strings.Contains(text, "legacy") {
		t.Fatalf("a run with no legacy record printed one:\n%s", text)
	}
	text := cli.FormatMergeTrain(after)
	lines := strings.Split(text, "\n")
	if len(lines) < 3 || !strings.HasPrefix(lines[1], "branch protection met: ") || lines[2] != line || strings.Count(text, line) != 1 || strings.Count(text, "legacy") != 1 {
		t.Fatalf("the legacy line, once, on its own line after the protection line (%q):\n%s", line, text)
	}
	if strings.Contains(text, "gate_store_unreadable") || strings.Contains(text, "could not be read") {
		t.Fatalf("the run printed an alert for the legacy record:\n%s", text)
	}
}
