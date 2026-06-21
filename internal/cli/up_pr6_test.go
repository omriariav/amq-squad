package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/v2/internal/launch"
	"github.com/omriariav/amq-squad/v2/internal/state"
	"github.com/omriariav/amq-squad/v2/internal/team"
)

// withResetSeams installs deterministic reset seams (a scripted confirm reader
// and a liveness probe) for the duration of t, restoring the previous values
// on cleanup. Globals are swapped, so callers must not run in parallel.
func withResetSeams(t *testing.T, confirm string, probe state.Probe) {
	t.Helper()
	prevConfirm := resetConfirmOverride
	prevProbe := resetProbeOverride
	resetConfirmOverride = strings.NewReader(confirm)
	resetProbeOverride = &probe
	t.Cleanup(func() {
		resetConfirmOverride = prevConfirm
		resetProbeOverride = prevProbe
	})
}

// teamWith returns a one-member team with the given role and member session.
func teamWith(role, session string) team.Team {
	return team.Team{
		Members: []team.Member{
			{Role: role, Binary: "codex", Handle: role, Session: session},
		},
	}
}

// TestRunUpNewSessionAutoStubsAndWarns proves the brief escape hatch: `up`
// against a NEW session with no brief source launches fresh, auto-stubs the
// brief, and prints the warn-if-stub notice on stderr.
func TestRunUpNewSessionAutoStubsAndWarns(t *testing.T) {
	backend := useFakeBackend(t)
	setupFakeAMQSessionRoots(t)
	dir := seedTeam(t, teamWith("cto", ""))

	_, stderr, err := captureOutput(t, func() error {
		return runUp([]string{"--terminal", "fake", "--session", "issue-200", "--no-bootstrap"})
	})
	if err != nil {
		t.Fatalf("up new session: %v\n%s", err, stderr)
	}
	if len(backend.launches) != 1 {
		t.Fatalf("up should launch once; got %d", len(backend.launches))
	}
	brief := filepath.Join(dir, ".amq-squad", "briefs", "issue-200.md")
	body, readErr := os.ReadFile(brief)
	if readErr != nil {
		t.Fatalf("brief should be auto-stubbed at %s: %v", brief, readErr)
	}
	if !strings.Contains(string(body), briefStubFirstLine) {
		t.Errorf("auto-stub brief should contain stub prose:\n%s", body)
	}
	if !strings.Contains(stderr, "stub brief") || !strings.Contains(stderr, "--seed-from") {
		t.Errorf("expected stub-brief warning on stderr:\n%s", stderr)
	}
}

// TestRunUpSeedFromAuthorsRealBriefNoWarning proves --seed-from authors the
// real brief and suppresses the stub warning entirely.
func TestRunUpSeedFromAuthorsRealBriefNoWarning(t *testing.T) {
	backend := useFakeBackend(t)
	setupFakeAMQSessionRoots(t)
	dir := seedTeam(t, teamWith("cto", ""))

	src := filepath.Join(t.TempDir(), "goal.md")
	if err := os.WriteFile(src, []byte("Ship the new workstream gate.\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, stderr, err := captureOutput(t, func() error {
		return runUp([]string{"--terminal", "fake", "--session", "issue-201", "--no-bootstrap", "--seed-from", "file:" + src})
	})
	if err != nil {
		t.Fatalf("up --seed-from: %v\n%s", err, stderr)
	}
	if len(backend.launches) != 1 {
		t.Fatalf("up --seed-from should launch once; got %d", len(backend.launches))
	}
	brief := filepath.Join(dir, ".amq-squad", "briefs", "issue-201.md")
	body, readErr := os.ReadFile(brief)
	if readErr != nil {
		t.Fatalf("seed brief should be written at %s: %v", brief, readErr)
	}
	if !strings.Contains(string(body), "Ship the new workstream gate.") {
		t.Errorf("seed brief should carry the source body:\n%s", body)
	}
	if strings.Contains(string(body), briefStubFirstLine) {
		t.Errorf("seed brief must not be the stub:\n%s", body)
	}
	if strings.Contains(stderr, "stub brief") {
		t.Errorf("--seed-from must suppress the stub warning:\n%s", stderr)
	}
}

// TestRunUpPositionalSessionName proves `up <name>` sets the session like rm.
func TestRunUpPositionalSessionName(t *testing.T) {
	backend := useFakeBackend(t)
	setupFakeAMQSessionRoots(t)
	seedTeam(t, teamWith("cto", ""))

	if _, _, err := captureOutput(t, func() error {
		return runUp([]string{"--terminal", "fake", "--no-bootstrap", "issue-202"})
	}); err != nil {
		t.Fatalf("up issue-202: %v", err)
	}
	if len(backend.launches) != 1 {
		t.Fatalf("positional up should launch once; got %d", len(backend.launches))
	}
	if got := backend.launches[0].Workstream; got != "issue-202" {
		t.Errorf("positional session = %q, want issue-202", got)
	}
}

// TestRunUpPositionalAndSessionConflict proves passing both a positional and
// --session is a usage error.
func TestRunUpPositionalAndSessionConflict(t *testing.T) {
	useFakeBackend(t)
	setupFakeAMQSessionRoots(t)
	seedTeam(t, teamWith("cto", ""))

	_, _, err := captureOutput(t, func() error {
		return runUp([]string{"--terminal", "fake", "--no-bootstrap", "--session", "issue-203", "issue-204"})
	})
	if err == nil {
		t.Fatal("positional + --session must be a usage error")
	}
	if _, ok := err.(UsageError); !ok {
		t.Fatalf("want UsageError, got %T: %v", err, err)
	}
}

// TestRunUpResetDeclinedMakesZeroChanges proves a declined --reset confirm
// cancels the whole up: the existing session footprint survives and the
// backend is never invoked.
func TestRunUpResetDeclinedMakesZeroChanges(t *testing.T) {
	backend := useFakeBackend(t)
	base := setupFakeAMQSessionRoots(t)
	// Decline ("n") with a dead probe so liveness never blocks before confirm.
	withResetSeams(t, "n\n", deadStateProbe())
	dir := seedTeam(t, teamWith("cto", ""))

	root := filepath.Join(base, "issue-205")
	if err := os.MkdirAll(filepath.Join(root, "agents", "cto"), 0o755); err != nil {
		t.Fatal(err)
	}
	brief := filepath.Join(dir, ".amq-squad", "briefs", "issue-205.md")
	if err := os.MkdirAll(filepath.Dir(brief), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(brief, []byte("# issue-205\nreal brief\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	out, _, err := captureOutput(t, func() error {
		return runUp([]string{"--terminal", "fake", "--no-bootstrap", "--reset", "issue-205"})
	})
	if err != nil {
		t.Fatalf("declined reset should not error: %v", err)
	}
	if len(backend.launches) != 0 {
		t.Errorf("declined reset must not launch; got %d", len(backend.launches))
	}
	if _, statErr := os.Stat(root); statErr != nil {
		t.Errorf("session root must survive a declined reset: %v", statErr)
	}
	if _, statErr := os.Stat(brief); statErr != nil {
		t.Errorf("brief must survive a declined reset: %v", statErr)
	}
	if !strings.Contains(out, "no changes made") {
		t.Errorf("declined reset should report no changes:\n%s", out)
	}
}

// TestRunUpResetYesWipesAndRelaunches proves --reset --yes tears down the
// existing session (root + brief) and then launches fresh.
func TestRunUpResetYesWipesAndRelaunches(t *testing.T) {
	backend := useFakeBackend(t)
	base := setupFakeAMQSessionRoots(t)
	withResetSeams(t, "", deadStateProbe())
	dir := seedTeam(t, teamWith("cto", ""))

	root := filepath.Join(base, "issue-206")
	if err := os.MkdirAll(filepath.Join(root, "agents", "cto"), 0o755); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(root, "agents", "cto", "stale.marker")
	if err := os.WriteFile(marker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	brief := filepath.Join(dir, ".amq-squad", "briefs", "issue-206.md")
	if err := os.MkdirAll(filepath.Dir(brief), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(brief, []byte("# issue-206\nstale brief\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, _, err := captureOutput(t, func() error {
		return runUp([]string{"--terminal", "fake", "--no-bootstrap", "--reset", "--yes", "issue-206"})
	}); err != nil {
		t.Fatalf("up --reset --yes: %v", err)
	}
	if len(backend.launches) != 1 {
		t.Fatalf("reset --yes should relaunch once; got %d", len(backend.launches))
	}
	if _, statErr := os.Stat(marker); !os.IsNotExist(statErr) {
		t.Errorf("reset should wipe the old session root; stat err = %v", statErr)
	}
	// The relaunch re-stubs the brief, so a brief exists again, but it is the
	// fresh stub, not the stale one.
	body, readErr := os.ReadFile(brief)
	if readErr != nil {
		t.Fatalf("relaunch should re-stub the brief: %v", readErr)
	}
	if strings.Contains(string(body), "stale brief") {
		t.Errorf("reset should have removed the stale brief before relaunch:\n%s", body)
	}
}

// TestRunUpResetRefusesLiveSessionWithoutForce proves --reset refuses to tear
// down a session with LIVE agents unless --force, and never launches.
func TestRunUpResetRefusesLiveSessionWithoutForce(t *testing.T) {
	backend := useFakeBackend(t)
	base := setupFakeAMQSessionRoots(t)
	// Live: PID alive AND binary matches.
	liveProbe := rmStateProbe(map[int]bool{4242: true}, map[int]bool{4242: true})
	withResetSeams(t, "y\n", liveProbe)
	seedTeam(t, teamWith("cto", ""))

	root := filepath.Join(base, "issue-207")
	seedAgentRecord(t, base, "issue-207", "cto", launch.Record{
		Binary: "codex", Handle: "cto", AgentPID: 4242, Root: root, Session: "issue-207",
	})

	_, _, err := captureOutput(t, func() error {
		return runUp([]string{"--terminal", "fake", "--no-bootstrap", "--reset", "issue-207"})
	})
	if err == nil || !strings.Contains(err.Error(), "live agents") {
		t.Fatalf("reset of a live session must be refused without --force: %v", err)
	}
	if len(backend.launches) != 0 {
		t.Errorf("refused reset must not launch; got %d", len(backend.launches))
	}
	if _, statErr := os.Stat(root); statErr != nil {
		t.Errorf("refused reset must leave the root intact: %v", statErr)
	}
}

// TestRunUpResetForceWipesLiveSession proves --reset --force --yes tears down
// a live session anyway and relaunches.
func TestRunUpResetForceWipesLiveSession(t *testing.T) {
	backend := useFakeBackend(t)
	base := setupFakeAMQSessionRoots(t)
	liveProbe := rmStateProbe(map[int]bool{4242: true}, map[int]bool{4242: true})
	withResetSeams(t, "", liveProbe)
	seedTeam(t, teamWith("cto", ""))

	root := filepath.Join(base, "issue-208")
	seedAgentRecord(t, base, "issue-208", "cto", launch.Record{
		Binary: "codex", Handle: "cto", AgentPID: 4242, Root: root, Session: "issue-208",
	})

	if _, _, err := captureOutput(t, func() error {
		return runUp([]string{"--terminal", "fake", "--no-bootstrap", "--reset", "--force", "--yes", "issue-208"})
	}); err != nil {
		t.Fatalf("up --reset --force --yes of a live session should succeed: %v", err)
	}
	if len(backend.launches) != 1 {
		t.Fatalf("forced reset should relaunch once; got %d", len(backend.launches))
	}
	if _, statErr := os.Stat(root); !os.IsNotExist(statErr) {
		t.Errorf("forced reset should remove the live session root; stat err = %v", statErr)
	}
}

// TestRunUpResetRefusedByDefaultMentionsReset proves the default refusal (no
// --reset) points the operator at --reset for restartable sessions whose only
// signal is a restorable launch record (no live AMQ root dir on disk).
func TestRunUpRefusesOnRestorableRecord(t *testing.T) {
	backend := useFakeBackend(t)
	base := setupFakeAMQSessionRoots(t)
	seedTeam(t, teamWith("cto", ""))

	// A restorable launch record under the session, but no live process; the
	// root dir created by seedAgentRecord also makes teamWorkstreamExists true,
	// which is itself a refuse signal — both paths converge on refusal.
	root := filepath.Join(base, "issue-209")
	seedAgentRecord(t, base, "issue-209", "cto", launch.Record{
		Binary: "codex", Handle: "cto", AgentPID: 4242, Root: root, Session: "issue-209",
	})

	_, _, err := captureOutput(t, func() error {
		return runUp([]string{"--terminal", "fake", "--no-bootstrap", "issue-209"})
	})
	if err == nil {
		t.Fatal("up against a restorable session must be refused")
	}
	if !strings.Contains(err.Error(), "amq-squad up --reset") {
		t.Errorf("refusal should mention --reset: %v", err)
	}
	if len(backend.launches) != 0 {
		t.Errorf("refused up must not launch; got %d", len(backend.launches))
	}
}
