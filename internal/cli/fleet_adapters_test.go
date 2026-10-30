package cli

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/v3/internal/fleet"
	"github.com/omriariav/amq-squad/v3/internal/seatlaunch"
)

const adapterSquadYAML = "schema: 1\nsession: app\nlead: lead\nseats:\n  - handle: lead\n    role: cto\n    runtime: claude\n  - handle: dev\n    role: senior-dev\n    runtime: codex\n"

// The SquadWriter adapter is 2b-5's digest flow: Propose writes nothing,
// Apply writes the proposed squad.yaml only for its digest.
func TestTeamSquadWriterIsTheDigestFlow(t *testing.T) {
	t.Setenv("HOME", fleetTempDir(t))
	repo := fleetTempDir(t)
	w := &teamSquadWriter{}
	digest, err := w.Propose(repo, adapterSquadYAML)
	if err != nil || !strings.HasPrefix(digest, "sha256:") {
		t.Fatalf("propose: %q %v", digest, err)
	}
	if _, err := os.Stat(filepath.Join(repo, "squad.yaml")); !os.IsNotExist(err) {
		t.Fatal("propose wrote squad.yaml")
	}
	if err := w.Apply(repo, "sha256:0000"); err == nil {
		t.Fatal("apply with another digest wrote")
	}
	if err := w.Apply(repo, digest); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(repo, "squad.yaml"))
	if err != nil || string(raw) != adapterSquadYAML {
		t.Fatalf("squad.yaml = %q %v", raw, err)
	}
	if _, err := os.Stat(filepath.Join(repo, ".amq-squad", "team-rules.md")); err != nil {
		t.Fatalf("team rules not written: %v", err)
	}
	if err := (&teamSquadWriter{}).Apply(repo, digest); err == nil {
		t.Fatal("apply with no proposal wrote")
	}
}

// The mayor's adapter applies through the same guard: a squad.yaml edited
// after the proposal is refused as squad_yaml_changed and left as edited.
func TestTeamSquadWriterRefusesAnEditAfterThePropose(t *testing.T) {
	t.Setenv("HOME", fleetTempDir(t))
	repo := fleetTempDir(t)
	path := filepath.Join(repo, "squad.yaml")
	w := &teamSquadWriter{}
	digest, err := w.Propose(repo, adapterSquadYAML)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("# written by hand\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := w.Apply(repo, digest); err == nil || !strings.Contains(err.Error(), "squad_yaml_changed") {
		t.Fatalf("apply over a file that appeared after the proposal: %v", err)
	}
	if b, _ := os.ReadFile(path); string(b) != "# written by hand\n" {
		t.Fatalf("squad.yaml = %q", b)
	}
}

// The RosterLauncher adapter compiles squad.yaml into 2b-2's typed squad and
// maps the seat launch's outcome to the mayor's: all up, partial, none.
func TestSeatRosterCompilesAndMapsOutcomes(t *testing.T) {
	t.Setenv("HOME", fleetTempDir(t))
	repo := fleetTempDir(t)
	q, err := rosterSquad(fleet.LaunchRequest{ProjectPath: repo, Root: filepath.Join(repo, ".agent-mail", "app"), Session: "app", SquadYAML: adapterSquadYAML})
	if err != nil {
		t.Fatal(err)
	}
	if q.ProjectRoot != repo || q.BaseRoot != filepath.Join(repo, ".agent-mail") || q.Session != "app" || len(q.Seats) != 2 {
		t.Fatalf("squad = %+v", q)
	}
	if q.Seats[0].Handle != "lead" || q.Seats[0].Runtime != "claude" || !strings.Contains(q.Seats[0].Role, "# ") || q.Seats[1].Runtime != "codex" {
		t.Fatalf("seats = %+v", q.Seats)
	}
	if _, err := roleText(repo, "no-such-role"); err == nil {
		t.Fatal("an unknown role resolved")
	}
	up := seatlaunch.Result{Stage: "applied", Seats: []seatlaunch.SeatState{{Handle: "lead", Up: true}, {Handle: "dev", Up: true}}}
	if err := launchOutcome(up, nil); err != nil {
		t.Fatalf("all up: %v", err)
	}
	half := seatlaunch.Result{Stage: "applied", Seats: []seatlaunch.SeatState{{Handle: "lead", Up: true}, {Handle: "dev"}}}
	var partial *fleet.PartialLaunchError
	if err := launchOutcome(half, nil); !errors.As(err, &partial) || len(partial.Live) != 1 || partial.Live[0] != "lead" {
		t.Fatalf("partial: %v", err)
	}
	none := seatlaunch.Result{Stage: "apply_failed"}
	if err := launchOutcome(none, errors.New("apply failed")); err == nil || errors.As(err, &partial) {
		t.Fatalf("none up: %v", err)
	}
}

// With the real squad.yaml writer wired, `operator start-squad` inside the
// grant writes the squad's squad.yaml through the digest flow, launches the
// roster and registers the squad.
func TestOperatorStartSquadWritesSquadYAMLThroughTheDigestFlow(t *testing.T) {
	dir := fleetTempDir(t)
	home := filepath.Join(dir, "home")
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, ".amq-squad"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".amq-squad", "grants.yaml"), []byte("schema: 1\nfleet:\n  max_squads: 1\n  permitted_repositories: [owner/app]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	repo := filepath.Join(dir, "app")
	if err := os.MkdirAll(filepath.Join(repo, ".amq-squad"), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{".amqrc": "{}\n", fleet.IdentityFile: "owner/app\n"} {
		if err := os.WriteFile(filepath.Join(repo, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	squadFile := filepath.Join(dir, "squad.yaml")
	if err := os.WriteFile(squadFile, []byte(adapterSquadYAML), 0o600); err != nil {
		t.Fatal(err)
	}
	l := &recordingLauncher{}
	prevW, prevL := mayorSquadWriter, mayorLauncher
	mayorSquadWriter, mayorLauncher = &teamSquadWriter{}, l
	t.Cleanup(func() { mayorSquadWriter, mayorLauncher = prevW, prevL })
	file := filepath.Join(home, ".amq-squad", "fleet.yaml")
	stdout, _, err := captureOutput(t, func() error {
		return runOperator([]string{"start-squad", "--fleet-file", file, "--name", "app", "--root", filepath.Join(repo, ".agent-mail", "app"),
			"--project-path", repo, "--repository", "owner/app", "--lead", "lead", "--squad-yaml", squadFile, "--json"})
	})
	if err != nil {
		t.Fatal(err)
	}
	if r := decodeJSONEnvelope[fleet.Result](t, stdout).Data; r.Outcome != fleet.OutcomeStarted || !strings.HasPrefix(r.Digest, "sha256:") {
		t.Fatalf("start = %+v", r)
	}
	if raw, err := os.ReadFile(filepath.Join(repo, "squad.yaml")); err != nil || string(raw) != adapterSquadYAML {
		t.Fatalf("squad.yaml = %q %v", raw, err)
	}
	if l.n != 1 {
		t.Fatalf("launches = %d", l.n)
	}
	if reg, err := fleet.Read(file); err != nil || len(reg.Squads) != 1 || reg.Squads[0].Phase != "" {
		t.Fatalf("registry = %+v %v", reg, err)
	}
}
