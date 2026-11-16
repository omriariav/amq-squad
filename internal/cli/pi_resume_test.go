package cli

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
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
//
// followups-b (Codex on #1021): each dry run prints its launch, the
// pi_launch envelope of the current schema with the seat's own agent dir and
// the runtime's argv, and runs nothing: the stand-in pi is never called.
func TestPiRunAcceptsTheSquadsHandles(t *testing.T) {
	call, home := piFixture(t)
	chdir(t, t.TempDir())
	t.Setenv("AM_ME", "")
	t.Setenv("AM_ROOT", "")
	for _, h := range []string{"lead", "dev", "dev2", "reviewer", "cto", "seat-a", "seat_b", "2dev", strings.Repeat("a", 64)} {
		out, _, err := captureOutput(t, func() error { return Run([]string{"pi", "run", "--seat", h, "--dry-run"}, "test") })
		if err != nil {
			t.Errorf("pi run --seat %s: %v", h, err)
			continue
		}
		var launch struct {
			SchemaVersion int    `json:"schema_version"`
			Kind          string `json:"kind"`
			Data          struct {
				Runtime  string   `json:"runtime"`
				AgentDir string   `json:"agent_dir"`
				Argv     []string `json:"argv"`
			} `json:"data"`
		}
		if err := json.Unmarshal([]byte(out), &launch); err != nil || launch.SchemaVersion != JSONSchemaVersion || launch.Kind != "pi_launch" {
			t.Errorf("pi run --seat %s --dry-run printed no pi_launch envelope of schema %d (%v):\n%s", h, JSONSchemaVersion, err, out)
			continue
		}
		// the expected values are built here, not by the launcher's helpers
		// and not from the output: the seat's agent dir (plan 4.1), and the
		// one runtime the fixture unpacked under home, its node, its pi entry
		// and the package
		if want := filepath.Join(home, ".amq-squad", "pi-agent", h); launch.Data.AgentDir != want {
			t.Errorf("pi run --seat %s --dry-run: agent_dir %q, want %q", h, launch.Data.AgentDir, want)
		}
		entries, err := os.ReadDir(filepath.Join(home, ".amq-squad", "runtime"))
		var unpacked []string
		for _, e := range entries {
			if e.IsDir() {
				unpacked = append(unpacked, e.Name())
			}
		}
		if err != nil || len(unpacked) != 1 {
			t.Fatalf("the fixture's unpacked runtimes: %v (%v)", entries, err)
		}
		rt := filepath.Join(home, ".amq-squad", "runtime", unpacked[0])
		argv := []string{filepath.Join(rt, "node", "bin", "node"), filepath.Join(rt, "pi", "node_modules", "pi", "cli.js"), "-e", filepath.Join(rt, "pi")}
		if launch.Data.Runtime != rt || !slices.Equal(launch.Data.Argv, argv) {
			t.Errorf("pi run --seat %s --dry-run: runtime %q, argv %q, want %q and %q", h, launch.Data.Runtime, launch.Data.Argv, rt, argv)
		}
	}
	if call.path != "" || call.argv != nil || call.exit != -1 {
		t.Fatalf("a dry run ran the stand-in pi: %q %q (exit %d)", call.path, call.argv, call.exit)
	}
}
