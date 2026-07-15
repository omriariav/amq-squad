package operatorauth

import (
	"reflect"
	"sort"
	"testing"
)

func TestSupportedActionsIsSortedCanonicalDefensiveCopy(t *testing.T) {
	got := SupportedActions()
	if len(got) == 0 || !sort.StringsAreSorted(got) {
		t.Fatalf("SupportedActions() = %#v", got)
	}
	capabilities := ActionCapabilities()
	want := make([]string, len(capabilities))
	for i, capability := range capabilities {
		want[i] = capability.Action
		if normalized, err := CanonicalAction(capability.Action); err != nil || normalized != capability.Action {
			t.Fatalf("catalog action %q is not canonical: %q, %v", capability.Action, normalized, err)
		}
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("SupportedActions() = %#v, want %#v", got, want)
	}
	got[0] = "mutated"
	if again := SupportedActions(); len(again) == 0 || again[0] == "mutated" {
		t.Fatalf("SupportedActions exposed mutable storage: %#v", again)
	}
}
