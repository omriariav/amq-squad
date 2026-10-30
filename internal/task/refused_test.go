package task

import (
	"reflect"
	"testing"
	"time"
)

// 3.0.1 t16 round 7 (Codex 2): a commit refusal records its exact paths
// outside the declared files on the task, untruncated, a name with ", " or
// " (" kept whole; the list goes with the block (a reset clears it).
// Reverted (the paths only in the display text), the lead's widening had
// nothing exact to read.
func TestRefuseCommitPathsRecordsTheExactPaths(t *testing.T) {
	s := newStore(t)
	t0 := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	a := mustAdd(t, s, AddInput{Title: "a", Class: "implement", DeclaredFiles: []string{"internal/a.go"}})
	if _, err := s.Claim(a.ID, "impl-1", nil, time.Minute, t0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Done(a.ID, "impl-1", "e", "", t0); err != nil {
		t.Fatal(err)
	}
	paths := []string{"docs/a, b.md", "src/ (note)", "src/*.go", "p1", "p2", "p3", "p4", "p5", "p6", "p7"}
	r, err := s.RefuseCommitPaths(a.ID, "outside_declared_files", "outside_declared_files: docs/a, b.md, ... and 2 more", RouteLead, paths, t0)
	if err != nil || r.State != StateBlocked || !reflect.DeepEqual(r.RefusedPaths, paths) {
		t.Fatalf("refusal: %+v %v", r.RefusedPaths, err)
	}
	if got, _ := s.Get(a.ID); !reflect.DeepEqual(got.RefusedPaths, paths) {
		t.Fatalf("stored: %q", got.RefusedPaths)
	}
	if got, err := s.Reset(a.ID, nil, t0); err != nil || got.RefusedPaths != nil {
		t.Fatalf("reset keeps the refused paths: %q %v", got.RefusedPaths, err)
	}
}

// 3.0.1 t16 round 7 (Codex 3): an exact path becomes the declared entry that
// matches only it: a seat's file named like a glob never widens the scope.
// Reverted (the literal name passed as a glob), "src/*.go" would cover every
// Go file in src.
func TestLiteralGlobMatchesOnlyThePath(t *testing.T) {
	for _, c := range []struct {
		path, other string
	}{
		{"src/*.go", "src/a.go"},
		{"q?.go", "qa.go"},
		{"a[1].go", "a1.go"},
		{"x/**/y.go", "x/m/n/y.go"},
		{"docs/a, b.md", "docs/a"},
		{"src/ (note)", "src/"},
	} {
		g, err := LiteralGlob(c.path)
		if err != nil {
			t.Fatalf("%q: %v", c.path, err)
		}
		clean, err := CleanDeclaredFiles([]string{g})
		if err != nil || len(clean) != 1 {
			t.Fatalf("%q as a declared entry: %v %v", g, clean, err)
		}
		if !MatchPath(clean, c.path) {
			t.Errorf("%q (%q) does not match its own path", c.path, clean[0])
		}
		if MatchPath(clean, c.other) {
			t.Errorf("%q (%q) also matches %q", c.path, clean[0], c.other)
		}
	}
	for _, bad := range []string{"", "a\\b.go", "dir/", "/etc/passwd", "../x", "a/./b", "a\nb"} {
		if _, err := LiteralGlob(bad); err == nil {
			t.Errorf("%q was accepted", bad)
		}
	}
}
