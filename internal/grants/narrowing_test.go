package grants

import (
	"path/filepath"
	"reflect"
	"testing"
)

// Codex P2-4: a second document or a repeated grant key cannot replace an
// earlier, narrower narrowing; both are errors, so guard denies grants_invalid.
func TestSquadNarrowingRefusesSecondDocumentAndRepeatedKeys(t *testing.T) {
	for name, text := range map[string]string{
		"second document":          "protected_paths: [secrets/]\n---\nprotected_paths: []\n",
		"repeated protected_paths": "protected_paths: [secrets/]\nprotected_paths: []\n",
		"repeated max_seats":       "max_seats: 2\nmax_seats: 12\n",
		"repeated mode":            "mode: manual\nmode: auto\n",
	} {
		if n, err := ParseSquadNarrowing(text, "squad.yaml"); err == nil {
			t.Errorf("%s accepted: %+v", name, n)
		}
	}
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "squad.yaml"), "protected_paths: [secrets/]\n---\nprotected_paths: []\n", 0o644)
	if _, err := LoadFrom(filepath.Join(dir, "absent.yaml"), dir); err == nil {
		t.Fatal("LoadFrom accepted a two-document squad.yaml")
	}
	// one list of several paths still accumulates; other repeated keys are
	// not grant keys and stay lenient
	n, err := ParseSquadNarrowing("protected_paths:\n  - a/\n  - b/\nseats: [x]\n", "squad.yaml")
	if err != nil || !reflect.DeepEqual(n.ProtectedPaths, []string{"a/", "b/"}) {
		t.Fatalf("paths = %+v, %v", n, err)
	}
	// Codex P1-3: a quoted comma is one path, and an alias is no path at all
	if n, err := ParseSquadNarrowing(`protected_paths: ["secret,a"]`+"\n", "squad.yaml"); err != nil || !reflect.DeepEqual(n.ProtectedPaths, []string{"secret,a"}) {
		t.Fatalf("quoted comma = %+v, %v", n, err)
	}
	if n, err := ParseSquadNarrowing("protected_paths: [*p]\n", "squad.yaml"); err == nil {
		t.Fatalf("alias accepted as a path: %+v", n)
	}
}
