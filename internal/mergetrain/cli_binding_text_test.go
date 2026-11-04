package mergetrain_test

import (
	"regexp"
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/v3/internal/cli"
	"github.com/omriariav/amq-squad/v3/internal/mergetrain"
)

func TestBindingFailureCLITextIsWordsFirst(t *testing.T) {
	head, bound := strings.Repeat("a", 40), strings.Repeat("b", 40)
	text := cli.FormatMergeTrainError(&mergetrain.MergeBindingMismatch{Task: "t1", MergeHead: head, BoundHead: bound}).Error()
	first, _, _ := strings.Cut(text, "\n")
	if strings.Contains(first, "binding_failed") || regexp.MustCompile(`[0-9a-f]{8,}`).MatchString(first) {
		t.Fatalf("binding failure text is not words-first with full evidence below:\n%s", text)
	}
	for _, want := range []string{"task t1", "rule: task binding", head, bound, "code: binding_failed"} {
		if !strings.Contains(text, want) {
			t.Fatalf("binding failure text missing %q:\n%s", want, text)
		}
	}
}
