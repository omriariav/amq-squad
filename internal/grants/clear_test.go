package grants

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// clear.signature (phase 5, the Buzz-signed seat clear): required by default
// whenever the owner's Buzz enrolment exists; pty_only only without one.
func TestClearSignatureModes(t *testing.T) {
	dir := t.TempDir()
	owner := filepath.Join(dir, "grants.yaml")
	write := func(text string) {
		t.Helper()
		if err := os.WriteFile(owner, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	binding := filepath.Join(dir, BindingFile)

	write("schema: 1\n")
	r, err := LoadFrom(owner, "")
	if err != nil || r.ClearMode(false) != ClearPtyOnly || r.ClearMode(true) != ClearRequired {
		t.Fatalf("defaults: %q %q %v", r.ClearMode(false), r.ClearMode(true), err)
	}
	write("schema: 1\nclear:\n  signature: required\n")
	if r, err = LoadFrom(owner, ""); err != nil || r.ClearMode(false) != ClearRequired {
		t.Fatalf("required: %q %v", r.ClearMode(false), err)
	}
	write("schema: 1\nclear:\n  signature: pty_only\n")
	if r, err = LoadFrom(owner, ""); err != nil || r.ClearMode(false) != ClearPtyOnly {
		t.Fatalf("pty_only without an enrolment: %q %v", r.ClearMode(false), err)
	}
	if err := os.WriteFile(binding, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err = LoadFrom(owner, ""); err == nil || !strings.Contains(err.Error(), "pty_only") {
		t.Fatalf("pty_only beside an enrolment loaded: %v", err)
	}
	write("schema: 1\nclear:\n  signature: required\n")
	if _, err = LoadFrom(owner, ""); err != nil {
		t.Fatalf("required beside an enrolment: %v", err)
	}
	for _, bad := range []string{"clear:\n  signature: maybe\n", "clear:\n  other: 1\n", "clear: required\n"} {
		write("schema: 1\n" + bad)
		if _, err := LoadFrom(owner, ""); err == nil {
			t.Errorf("%q parsed", bad)
		}
	}
}
