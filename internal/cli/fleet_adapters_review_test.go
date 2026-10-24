package cli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/avivsinai/agent-message-queue/launchapi"

	"github.com/omriariav/amq-squad/v3/internal/fleet"
	"github.com/omriariav/amq-squad/v3/internal/seatlaunch"
)

// Codex re-pass of d02dd2fc, the adapters. Each fails with its fix reverted
// (docs/v3-spike/4-3-evidence/c9-revert-check.txt).

type failingRoster struct{}

func (failingRoster) Launch(context.Context, fleet.LaunchRequest) error {
	return errors.New("the installed amq is required on PATH (R51)")
}
func (failingRoster) Close(context.Context, fleet.LaunchRequest) error { return nil }

// Adapters a: squad.yaml and the team files the digest flow wrote are
// rolled back when the launch fails before promotion (a file that existed
// gets its bytes back), and the reservation is released.
func TestLaunchFailureRollsBackTheSquadFiles(t *testing.T) {
	t.Setenv("HOME", fleetTempDir(t))
	repo := fleetTempDir(t)
	if err := os.WriteFile(filepath.Join(repo, "CLAUDE.md"), []byte("# mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	w := &teamSquadWriter{}
	digest, err := w.Propose(repo, adapterSquadYAML)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Apply(repo, digest); err != nil {
		t.Fatal(err)
	}
	if err := w.Rollback(repo, digest); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"squad.yaml", ".amq-squad/team-rules.md"} {
		if _, err := os.Stat(filepath.Join(repo, p)); !os.IsNotExist(err) {
			t.Fatalf("%s survived the rollback: %v", p, err)
		}
	}
	if raw, _ := os.ReadFile(filepath.Join(repo, "CLAUDE.md")); string(raw) != "# mine\n" {
		t.Fatalf("CLAUDE.md not restored: %q", raw)
	}

	// through the verb: a launch that fails leaves no squad files and no row
	dir := fleetTempDir(t)
	home := filepath.Join(dir, "home")
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, ".amq-squad"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".amq-squad", "grants.yaml"), []byte("schema: 1\nfleet:\n  max_squads: 1\n  permitted_repositories: [owner/app]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	app := filepath.Join(dir, "app")
	if err := os.MkdirAll(filepath.Join(app, ".amq-squad"), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{".amqrc": "{}\n", fleet.IdentityFile: "owner/app\n"} {
		if err := os.WriteFile(filepath.Join(app, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	squadFile := filepath.Join(dir, "squad.yaml")
	if err := os.WriteFile(squadFile, []byte(adapterSquadYAML), 0o600); err != nil {
		t.Fatal(err)
	}
	prevW, prevL := mayorSquadWriter, mayorLauncher
	mayorSquadWriter, mayorLauncher = &teamSquadWriter{}, failingRoster{}
	t.Cleanup(func() { mayorSquadWriter, mayorLauncher = prevW, prevL })
	file := filepath.Join(home, ".amq-squad", "fleet.yaml")
	stdout, _, err := captureOutput(t, func() error {
		return runOperator([]string{"start-squad", "--fleet-file", file, "--name", "app", "--root", filepath.Join(app, ".agent-mail", "app"),
			"--project-path", app, "--repository", "owner/app", "--lead", "lead", "--squad-yaml", squadFile, "--json"})
	})
	if err != nil {
		t.Fatal(err)
	}
	if r := decodeJSONEnvelope[fleet.Result](t, stdout).Data; r.Outcome != fleet.OutcomeRefused || r.Reason != fleet.ReasonLaunchFailure {
		t.Fatalf("start = %+v", r)
	}
	if _, err := os.Stat(filepath.Join(app, "squad.yaml")); !os.IsNotExist(err) {
		t.Fatalf("squad.yaml survived a failed launch: %v", err)
	}
	if reg, err := fleet.Read(file); err != nil || len(reg.Squads) != 0 {
		t.Fatalf("registry = %+v %v", reg, err)
	}
}

// Adapters b: a launch that stops on decisions_pending with no seats is a
// refusal carrying the required actions, never a success; an applied launch
// with no seats is not a success either.
func TestDecisionsPendingIsNotASuccess(t *testing.T) {
	res := seatlaunch.Result{Stage: "decisions_pending", Pending: []launchapi.RequiredActionV1{{ActionID: "a1", Kind: "trust_folder", ReasonCode: "untrusted",
		AllowedDecisions: []launchapi.DecisionChoiceV1{"approve", "deny"}}}}
	err := launchOutcome(res, nil)
	var pending *fleet.DecisionsPendingError
	if !errors.As(err, &pending) || len(pending.Actions) != 1 || !strings.Contains(pending.Actions[0], "a1: trust_folder untrusted (approve|deny)") {
		t.Fatalf("decisions pending = %v", err)
	}
	if err := launchOutcome(seatlaunch.Result{Stage: "applied"}, nil); err == nil {
		t.Fatal("an applied launch with no seats is a success")
	}
}

// Adapters c: start-squad takes no --session; the roster launches under
// the registered root (its parent the base, its name the session).
func TestStartSquadRefusesASessionFlag(t *testing.T) {
	_, _, err := captureOutput(t, func() error {
		return runOperator([]string{"start-squad", "--session", "elsewhere", "--name", "app", "--squad-yaml", "x"})
	})
	if err == nil {
		t.Fatal("start-squad accepted --session")
	}
	t.Setenv("HOME", fleetTempDir(t))
	q, err := rosterSquad(fleet.LaunchRequest{ProjectPath: fleetTempDir(t), Root: "/r/base/app", Session: "elsewhere", SquadYAML: adapterSquadYAML})
	if err != nil || q.Session != "app" || q.BaseRoot != "/r/base" {
		t.Fatalf("roster squad = %+v %v, want session app under /r/base", q, err)
	}
}
