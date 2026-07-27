package wizard

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// defaultCursorExempt lists choice stages that legitimately need no defaultCursor case, with
// the reason each is safe. Everything else in choices() MUST position its cursor from the
// spec, because omitting a stage leaves the cursor at index 0 and accepting defaults then
// commits whatever happens to be listed first.
//
// That failure has now occurred twice: stageGlobalPosture escalated a stored safer sandbox
// posture to danger-full-access, and stageLaunchShape committed a prefilled lead-only-staged
// spec as working-team-together, launching workers meant to stay behind spawn gates. Both
// were fixed as instances. This test is what makes it a class.
var defaultCursorExempt = map[string]string{
	"stageConfirm":          "single choice; index 0 is the only option",
	"stageResumeBrief":      "single choice; index 0 is the only option",
	"stageExistingOverride": "a per-launch decision, not a stored spec value to restore",
	"stageSelfOperatorAllow": "encodes spec state in the CHOICE SET rather than the cursor: " +
		"the rows differ depending on m.spec.SelfOperatorAllow",
}

func bubbleStageCases(t *testing.T, fn string) map[string]bool {
	t.Helper()
	raw, err := os.ReadFile("bubbletea.go")
	if err != nil {
		t.Fatalf("read bubbletea.go: %v", err)
	}
	body := regexp.MustCompile(`(?s)func \(m BubbleModel\) ` + fn + `\(\).*?\n\}\n`).FindString(string(raw))
	if body == "" {
		t.Fatalf("could not locate %s() in bubbletea.go", fn)
	}
	out := map[string]bool{}
	for _, m := range regexp.MustCompile(`case ([^:\n]+):`).FindAllStringSubmatch(body, -1) {
		for _, stage := range strings.Split(m[1], ",") {
			if stage = strings.TrimSpace(stage); strings.HasPrefix(stage, "stage") {
				out[stage] = true
			}
		}
	}
	return out
}

// TestEveryChoiceStagePositionsItsCursor derives both stage sets from the source rather than
// a hand-maintained list, so a stage added to choices() without a defaultCursor case fails
// here instead of silently committing the first option.
func TestEveryChoiceStagePositionsItsCursor(t *testing.T) {
	choiceStages := bubbleStageCases(t, "choices")
	cursorStages := bubbleStageCases(t, "defaultCursor")

	// Anti-vacuity: a regex that matched nothing would report perfect coverage.
	if len(choiceStages) < 20 {
		t.Fatalf("found only %d choice stages; the source scan is broken", len(choiceStages))
	}

	for stage := range choiceStages {
		if cursorStages[stage] {
			continue
		}
		if reason, ok := defaultCursorExempt[stage]; ok {
			t.Logf("exempt: %s (%s)", stage, reason)
			continue
		}
		t.Errorf("%s appears in choices() but not defaultCursor(): a prefilled spec value will "+
			"be ignored and accepting defaults commits the FIRST option. Add a case, or add an "+
			"exemption with a reason to defaultCursorExempt.", stage)
	}
}

// TestDefaultCursorExemptionsAreNotStale is the self-destructing half: an exemption for a
// stage that no longer exists, or that has since gained a defaultCursor case, must be deleted
// rather than left to accumulate.
func TestDefaultCursorExemptionsAreNotStale(t *testing.T) {
	choiceStages := bubbleStageCases(t, "choices")
	cursorStages := bubbleStageCases(t, "defaultCursor")
	for stage := range defaultCursorExempt {
		if !choiceStages[stage] {
			t.Errorf("%s is exempted but no longer appears in choices(); delete the exemption", stage)
		}
		if cursorStages[stage] {
			t.Errorf("%s is exempted but now HAS a defaultCursor case; delete the exemption", stage)
		}
	}
}
