// Package scripts holds the Go test of scripts/manifest-check.sh, so the
// standard gate (go test ./...) runs it.
package scripts

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// fixture builds a repository root with the script, three Go files under
// internal/ and a manifest whose retired row carries the given extra lines.
func fixture(t *testing.T, retired string) (root string) {
	t.Helper()
	root = t.TempDir()
	script, err := os.ReadFile("manifest-check.sh")
	if err != nil {
		t.Fatal(err)
	}
	for path, body := range map[string]string{
		"scripts/manifest-check.sh": string(script),
		"internal/x/kept.go":        strings.Repeat("// kept\n", 10),
		"internal/x/new.go":         strings.Repeat("// new\n", 5),
	} {
		full := filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	manifest := `entries:
- path: internal/x/kept.go
  v2_lines: 40000
  current_lines: 10
  fate: shrink
  target_lines: 33100
  cluster: x
  extract: ""
  reason: "kept"
  status: shrunk
- path: internal/x/new.go
  v2_lines: 0
  current_lines: 5
  fate: new
  target_lines: 5
  cluster: x
  extract: ""
  reason: "3.0 code"
  status: new
- path: internal/x/old2a.go
  v2_lines: 100
  fate: delete
  target_lines: 0
  cluster: x
  extract: ""
  reason: "cut in 2a"
  status: deleted
- path: internal/x/old.go
  v2_lines: 100
  fate: delete
  target_lines: 0
  cluster: x
  extract: ""
  reason: "retired"
  status: deleted
` + retired
	if err := os.MkdirAll(filepath.Join(root, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "docs", "v3-manifest.yaml"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func run(t *testing.T, root string) (string, error) {
	t.Helper()
	cmd := exec.Command("bash", filepath.Join(root, "scripts", "manifest-check.sh"), "--final")
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// Check 8 (2b-5 merge ruling): a row retired from 2b on names what replaced
// it; a 2a row is exempt; the report prints the ceiling only, the 3.0
// additions and their sum.
func TestManifestCheckRequiresReplacedByFor2bRetirements(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("bash script")
	}
	cases := []struct {
		name, extra string
		ok          bool
	}{
		{"note names a brief, no replaced_by", `  note: "deleted in 2b-5"` + "\n", false},
		{"retired_in, no replaced_by", "  retired_in: 2b-1\n", false},
		{"replaced by a fate-new row", "  replaced_by: \"internal/x/new.go\"\n  note: \"deleted in 2b-5\"\n", true},
		{"replaced by an acceptance item", "  replaced_by: \"2b-5 acceptance 4\"\n  retired_in: 2b-5\n", true},
		{"replaced by a row that is not new", "  replaced_by: \"internal/x/kept.go\"\n  note: \"deleted in 2b-5\"\n", false},
		{"replaced by free text", "  replaced_by: \"something else\"\n  note: \"deleted in 2b-5\"\n", false},
		{"no brief named (2a)", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := run(t, fixture(t, tc.extra))
			if (err == nil) != tc.ok {
				t.Fatalf("ok=%t, want %t:\n%s", err == nil, tc.ok, out)
			}
			if !tc.ok && !strings.Contains(out, "internal/x/old.go") {
				t.Fatalf("the violation does not name the row:\n%s", out)
			}
			if strings.Contains(out, "internal/x/old2a.go:") {
				t.Fatalf("a 2a row was checked:\n%s", out)
			}
			for _, want := range []string{"v2 rows kept: 10, ceiling 36,410", "3.0 additions: 1 files, 5 lines", "size of 3.0: 15 lines"} {
				if !strings.Contains(out, want) {
					t.Fatalf("report missing %q:\n%s", want, out)
				}
			}
			if strings.Contains(out, "NOT MET") {
				t.Fatalf("a sub-floor total is reported as NOT MET:\n%s", out)
			}
		})
	}
}
