package yamlsub

import (
	"strings"
	"testing"
)

// Review pass 3, item 2: a mapping item is refused, not read as a path.
func TestMappingItemsAreRefused(t *testing.T) {
	for _, text := range []string{"p:\n  - secrets/: true\n", "p: [secrets/: true]\n", "p:\n  - secrets/:\n", "p: [a:]\n"} {
		if got, err := list(t, text); err == nil || !strings.Contains(err.Error(), "mapping") {
			t.Errorf("%q = %q, %v", text, got, err)
		}
	}
	// a quoted colon is a value
	if got, err := list(t, `p: ["a: b", 'c:']`+"\n"); err != nil || len(got) != 2 || got[0] != "a: b" || got[1] != "c:" {
		t.Fatalf("quoted colons = %q, %v", got, err)
	}
}

// Review pass 3, item 3: a repeated key is an error in both modes.
func TestRepeatedKeysAreAnErrorInBothModes(t *testing.T) {
	for _, strict := range []bool{true, false} {
		if _, err := Split("name: a\nname: b\n", "f", strict); err == nil || !strings.Contains(err.Error(), "repeated") {
			t.Errorf("strict=%t: %v", strict, err)
		}
	}
}

// Review pass 3, item 4: --- followed by any whitespace is a document marker.
func TestDocumentMarkerFollowedByATab(t *testing.T) {
	for _, strict := range []bool{true, false} {
		for _, text := range []string{"a: 1\n---\tprotected_paths: [secrets/]\n", "a: 1\n...\tx\n"} {
			if _, err := Split(text, "f", strict); err == nil || !strings.Contains(err.Error(), "document marker") {
				t.Errorf("strict=%t %q: %v", strict, text, err)
			}
		}
	}
	// ---x is not a marker (it is an ordinary malformed or key line)
	if _, err := Split("---x: 1\n", "f", false); err != nil && strings.Contains(err.Error(), "document marker") {
		t.Fatalf("---x read as a marker: %v", err)
	}
}
