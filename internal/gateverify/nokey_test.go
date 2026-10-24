package gateverify

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// Acceptance 10 (R33): no model process holds the owner key. No package under
// internal/ reads an nsec: no non-test Go file names one in code (an
// identifier, a string, an environment variable) or imports a NIP-19/NIP-49
// secret-key decoder. Comments may mention it; code may not. The owner signs
// on the phone; the Buzz nsec enters only amq-owner-sign, through a
// password-manager pipe (plan 9).
func TestNoPackageUnderInternalReadsAnNsec(t *testing.T) {
	root := filepath.Join("..")
	word := regexp.MustCompile(`(?i)(?:^|[^i])nsec`) // any nsec, but not "insecure"
	checked := 0
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return err
		}
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			return err
		}
		checked++
		for _, imp := range file.Imports {
			p, _ := strconv.Unquote(imp.Path.Value)
			if strings.Contains(p, "nip19") || strings.Contains(p, "nip49") || strings.Contains(p, "fiatjaf") {
				t.Errorf("%s imports %s", path, p)
			}
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		// blank out comments, keep everything else
		code := []byte(string(src))
		for _, cg := range file.Comments {
			for i := fset.Position(cg.Pos()).Offset; i < fset.Position(cg.End()).Offset; i++ {
				if code[i] != '\n' {
					code[i] = ' '
				}
			}
		}
		for n, line := range strings.Split(string(code), "\n") {
			if word.MatchString(line) {
				t.Errorf("%s:%d names an nsec in code: %s", path, n+1, strings.TrimSpace(line))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if checked < 100 {
		t.Fatalf("walked only %d files from %s", checked, root)
	}
	t.Logf("checked %d non-test Go files under internal/", checked)
}
