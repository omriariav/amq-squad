package piruntime

import (
	"errors"
	"fmt"
	"testing"
)

// `amq-squad up` names a child's pi refusal from its text (brief 31-1's
// second PR): the text of ErrNotEmbedded and of a *Reject, as main prints
// them, maps to their names, and nothing else does.
func TestRefusalCodeTextNamesTheTwoRefusals(t *testing.T) {
	for text, want := range map[string]string{
		"error: " + ErrNotEmbedded.Error(): ReasonNotEmbedded,
		"error: " + (&Reject{Dir: "/h/.amq-squad/runtime/x", Reason: errors.New("a file changed")}).Error(): ReasonRejected,
		fmt.Sprintf("error: runtime %s is not unpacked yet", "/h/x"):                                        "",
		"": "",
	} {
		if got := RefusalCodeText(text); got != want {
			t.Errorf("%q: %q, want %q", text, got, want)
		}
	}
}
