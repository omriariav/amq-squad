package cli

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/v3/internal/piruntime"
)

// pi-resume: the seat handle is checked before any path is built from it.

const piResumeEscape = "../../../../outside"

// R1: pi run --seat with a handle that leaves the AMQ root is refused before
// --resume reads the ledger its path would name: a ledger planted there
// (whose tool result would refuse the resume) is never read.
func TestPiRunRefusesAnEscapingSeatBeforeTheResumeRead(t *testing.T) {
	piFixture(t)
	chdir(t, t.TempDir())
	root := filepath.Join(t.TempDir(), "root")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AM_ROOT", root)
	t.Setenv("AM_ME", "")
	planted := piruntime.LedgerPath(root, piResumeEscape)
	if strings.HasPrefix(planted, root+string(filepath.Separator)) {
		t.Fatalf("the escaping seat's ledger %s is inside the root", planted)
	}
	if err := os.MkdirAll(filepath.Dir(planted), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(planted, []byte(`{"event":"tool_result","tool":"bash","outcome":"ok"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := Run([]string{"pi", "run", "--seat", piResumeEscape, "--resume", "--", "--session-id", "nonce-1"}, "test")
	var rr *piruntime.ResumeRefusal
	if errors.As(err, &rr) {
		t.Fatalf("the ledger outside the root was read: %v", err)
	}
	if err == nil || !strings.Contains(err.Error(), `pi: seat "`+piResumeEscape+`"`) {
		t.Fatalf("an escaping --seat: %v", err)
	}
}

// R2: the lead under an AMQ launch takes AM_ME as its seat (pi_lead.go); an
// AM_ME that leaves its directory is refused by the same check, before any
// path is built from it: no TMPDIR or agent dir appears outside the root.
func TestLeadRefusesAnEscapingAmMeBeforeAnyPath(t *testing.T) {
	_, home := piFixture(t)
	chdir(t, t.TempDir())
	root := filepath.Join(t.TempDir(), "root")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AM_ROOT", root)
	t.Setenv("AM_ME", piResumeEscape)
	_, _, err := captureOutput(t, func() error { return runLead(nil) })
	if err == nil || !strings.Contains(err.Error(), `pi: seat "`+piResumeEscape+`"`) {
		t.Fatalf("an escaping AM_ME: %v", err)
	}
	for _, p := range []string{filepath.Join(root, "agents", piResumeEscape), piruntime.AgentDirFor(home, piResumeEscape)} {
		if _, err := os.Lstat(p); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("a path built from the escaping AM_ME exists: %s (%v)", p, err)
		}
	}
}

// R3: the squad's own handles launch (a dry run): the check refuses no
// handle the squad can have, its live ones and the edges of the squad's
// handle pattern (a hyphen, an underscore, a leading digit, 64 characters).
func TestPiRunAcceptsTheSquadsHandles(t *testing.T) {
	piFixture(t)
	chdir(t, t.TempDir())
	t.Setenv("AM_ME", "")
	t.Setenv("AM_ROOT", "")
	for _, h := range []string{"lead", "dev", "dev2", "reviewer", "cto", "seat-a", "seat_b", "2dev", strings.Repeat("a", 64)} {
		if _, _, err := captureOutput(t, func() error { return Run([]string{"pi", "run", "--seat", h, "--dry-run"}, "test") }); err != nil {
			t.Errorf("pi run --seat %s: %v", h, err)
		}
	}
}
