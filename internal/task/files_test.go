package task

import (
	"strings"
	"testing"
)

func TestCleanExactFilesSeparatesFileNamesFromGlobsAndDirectories(t *testing.T) {
	got, err := CleanExactFiles([]string{"./src/[name].go", "docs/a, b.md", "src/*.go", "src/[name].go"})
	if err != nil || strings.Join(got, "|") != "src/[name].go|docs/a, b.md|src/*.go" {
		t.Fatalf("exact files = %q, %v", got, err)
	}
	for _, bad := range []string{"", "   ", ".", "src/", "../x.go", "/x.go", "a/../x.go", "a\\x.go"} {
		if _, err := CleanExactFiles([]string{bad}); err == nil {
			t.Errorf("exact file %q was accepted", bad)
		}
	}
	if literal, err := LiteralGlob(got[0]); err != nil || literal != "src/[[]name].go" {
		t.Fatalf("literal [ file = %q, %v", literal, err)
	}
	if literal, err := LiteralGlob(got[2]); err != nil || literal != "src/[*].go" {
		t.Fatalf("literal * file = %q, %v", literal, err)
	}
}
