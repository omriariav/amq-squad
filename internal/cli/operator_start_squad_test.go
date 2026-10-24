package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/v3/internal/fleet"
)

type recordingWriter struct{ applied []string }

func (w *recordingWriter) Propose(p, _ string) (string, error) {
	return "sha256:" + filepath.Base(p), nil
}
func (w *recordingWriter) Apply(p, _ string) error { w.applied = append(w.applied, p); return nil }

type recordingLauncher struct{ n int }

func (l *recordingLauncher) Launch(context.Context, fleet.LaunchRequest) error { l.n++; return nil }

// `operator start-squad` runs the mayor on the owner's fleet grant: a
// permitted repository starts and registers, the next start past max_squads
// is refused (no running session to queue it) and shown as fleet_cap, an unpermitted repository is refused, a root with no .amqrc gives a
// proposal; nothing but the started squad is written or launched.
func TestOperatorStartSquadInsideTheOwnerFleetGrant(t *testing.T) {
	dir := t.TempDir()
	home := filepath.Join(dir, "home")
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, ".amq-squad"), 0o700); err != nil {
		t.Fatal(err)
	}
	grantsYAML := "schema: 1\nfleet:\n  max_squads: 1\n  permitted_repositories:\n    - owner/app\n    - owner/web\n    - owner/fresh\n"
	if err := os.WriteFile(filepath.Join(home, ".amq-squad", "grants.yaml"), []byte(grantsYAML), 0o600); err != nil {
		t.Fatal(err)
	}
	w, l := &recordingWriter{}, &recordingLauncher{}
	prevW, prevL := mayorSquadWriter, mayorLauncher
	mayorSquadWriter, mayorLauncher = w, l
	t.Cleanup(func() { mayorSquadWriter, mayorLauncher = prevW, prevL })
	squadYAML := filepath.Join(dir, "squad.yaml")
	if err := os.WriteFile(squadYAML, []byte("schema: 1\nseats:\n  - handle: lead\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(home, ".amq-squad", "fleet.yaml")
	start := func(name string, amqrc bool) fleet.Result {
		t.Helper()
		repo := filepath.Join(dir, name)
		if err := os.MkdirAll(repo, 0o755); err != nil {
			t.Fatal(err)
		}
		if amqrc {
			if err := os.WriteFile(filepath.Join(repo, ".amqrc"), []byte("{}\n"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		stdout, _, err := captureOutput(t, func() error {
			return runOperator([]string{"start-squad", "--fleet-file", file, "--name", name, "--root", filepath.Join(repo, ".agent-mail", name),
				"--project-path", repo, "--repository", "owner/" + name, "--lead", "lead", "--squad-yaml", squadYAML, "--json"})
		})
		if err != nil {
			t.Fatal(err)
		}
		return decodeJSONEnvelope[fleet.Result](t, stdout).Data
	}
	if r := start("fresh", false); r.Outcome != fleet.OutcomeProposed || r.Reason != fleet.ReasonProvisioning {
		t.Fatalf("unprovisioned root: %+v", r)
	}
	if r := start("evil", true); r.Outcome != fleet.OutcomeRefused || r.Reason != fleet.ReasonRepository {
		t.Fatalf("unpermitted: %+v", r)
	}
	if r := start("app", true); r.Outcome != fleet.OutcomeStarted {
		t.Fatalf("permitted: %+v", r)
	}
	// the CLI verb is not a running operator session: past max_squads is
	// refused by name, never queued, and status --fleet shows why
	if r := start("web", true); r.Outcome != fleet.OutcomeRefused || r.Reason != fleet.ReasonMaxSquads {
		t.Fatalf("past max_squads: %+v", r)
	}
	doc, _, err := captureOutput(t, func() error {
		return runStatusWithVersion([]string{"--fleet", "--fleet-file", file, "--json"}, "x")
	})
	if err != nil || !strings.Contains(doc, `"wait_reason": "fleet_cap"`) || !strings.Contains(doc, `"squad": "web"`) {
		t.Fatalf("status --fleet does not show the capped squad (%v):\n%s", err, doc)
	}
	if len(w.applied) != 1 || l.n != 1 {
		t.Fatalf("writes %v launches %d, want only app", w.applied, l.n)
	}
	reg, err := fleet.Read(file)
	if err != nil || len(reg.Squads) != 1 || reg.Squads[0].Name != "app" {
		t.Fatalf("registry %+v %v", reg, err)
	}
	// without the adapters (v3 before 2b-5 and 2b-2) a start inside the grant
	// stops before any write
	mayorSquadWriter, mayorLauncher = nil, nil
	_, _, err = captureOutput(t, func() error {
		return runOperator([]string{"start-squad", "--fleet-file", filepath.Join(dir, "other.yaml"), "--name", "app", "--root", filepath.Join(dir, "app", ".agent-mail", "app"),
			"--project-path", filepath.Join(dir, "app"), "--repository", "owner/app", "--lead", "lead", "--squad-yaml", squadYAML})
	})
	if err == nil || !strings.Contains(err.Error(), "writer") {
		t.Fatalf("no adapters: %v", err)
	}
}
