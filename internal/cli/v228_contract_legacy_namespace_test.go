package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/bootstrapack"
	"github.com/omriariav/amq-squad/v2/internal/launch"
	squadnamespace "github.com/omriariav/amq-squad/v2/internal/namespace"
	"github.com/omriariav/amq-squad/v2/internal/team"
)

// v228LegacyPreparedRunPath is the v2.27 prepared-manifest location, spelled out
// here on purpose. Production's preparedRunPath was deleted with the prepared-run
// machinery, so this test — whose whole subject is a remnant left by a version
// that no longer exists in the tree — must own the legacy layout itself. Deriving
// it from production would mean the fixture disappears exactly when the code it
// guards against recovery-failure is gone.
func v228LegacyPreparedRunPath(project, profile, session string) string {
	return filepath.Join(project, team.DirName, "prepared", squadnamespace.NormalizeProfile(profile), session+".json")
}

// AC11 (launch half): a dead-ended v2.27 namespace recovers via `start` without
// manual deletion. #644's operator was stuck in a loop precisely because the
// only escape was deleting state, and deleting it re-armed the drift comparison
// that caused the failure. Under v2.28 the remnants are inert: start reads the
// records it can, respawns what is not live, and never removes anything.

// v228SeedV227DeadEnd builds the exact shape #644 reported: a prepared-run
// manifest remnant, an AMQ root carrying only meta/config.json, and launch
// records still stamped with prepared-generation and bootstrap-expectation
// fields whose processes are long dead.
func v228SeedV227DeadEnd(t *testing.T, fixture v228StartFixture, roles []string) map[string]bool {
	t.Helper()
	stale := time.Date(2026, 7, 1, 9, 0, 0, 0, time.UTC)
	// Prepared-run manifest remnant under .amq-squad/prepared/<profile>/<session>.json.
	preparedPath := v228LegacyPreparedRunPath(fixture.Project, fixture.Profile, fixture.Session)
	if err := os.MkdirAll(filepath.Dir(preparedPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(preparedPath, []byte(`{"schema":2,"generation":"gen-v227","namespace":"v228/ac11"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	// The half-materialized AMQ root #644 reported: a real, valid AMQ config and
	// nothing else. The config must be schema-valid because that is what the
	// failed v2.27 launch actually left behind — an undecodable one would test
	// fail-closed parsing instead of dead-end recovery.
	if err := os.MkdirAll(filepath.Join(fixture.Root, "meta"), 0o755); err != nil {
		t.Fatal(err)
	}
	config, err := json.Marshal(amqRootConfigDocument{
		Version:    amqRootConfigVersion,
		CreatedUTC: stale.Format(time.RFC3339Nano),
		Agents:     roles,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fixture.Root, "meta", "config.json"), config, 0o600); err != nil {
		t.Fatal(err)
	}
	// v2.27-shaped launch records: prepared token fields plus a bootstrap
	// expectation, pids dead. Nothing may probe alive.
	dead := map[string]bool{}
	for i, role := range roles {
		pid := 4900 + i
		dead[role] = true
		v228SeedLiveRecord(t, fixture.Root, launch.Record{
			Schema: launch.SchemaVersion, Binary: "codex",
			Role: role, Handle: role, Session: fixture.Session,
			TeamProfile: fixture.Profile, TeamHome: fixture.Project, CWD: fixture.Project,
			Root: fixture.Root, BaseRoot: filepath.Dir(fixture.Root),
			AgentPID: pid, StartedAt: stale,
			PreparedRunGeneration:    "gen-v227",
			PreparedRunDigest:        "sha256:deadbeef",
			PreparedRunLaunchAttempt: "attempt-v227",
			BootstrapExpectation: &bootstrapack.Expectation{
				LaunchID: "launch-v227-" + role, PromptVersion: "v2.27.0",
				IssuedAt: stale, Required: true,
			},
		})
	}
	return dead
}

func TestV228ContractDeadEndedV227NamespaceRecoversWithoutDeletion(t *testing.T) {
	requireV228Contract(t)
	setupFakeAMQSessionRoots(t)
	swapStatusPaneLister(t, nil, nil)
	roles := []string{"cto", "dev"}
	fixture := v228NewStartFixture(t, "ac11", v228StartMembers("ac11", roles...))
	v228SeedV227DeadEnd(t, fixture, roles)

	beforeRoot := v228InventoryPaths(t, fixture.Root)
	beforeSquad := v228InventoryPaths(t, filepath.Join(fixture.Project, ".amq-squad"))
	preparedPath := v228LegacyPreparedRunPath(fixture.Project, fixture.Profile, fixture.Session)

	// No manual deletion, no --reset: just `start`.
	alive := map[int]bool{}
	pids := map[string]int{"cto": 4911, "dev": 4912}
	run := v228RunStart(t, fixture, alive, func(role string) int { return pids[role] })
	if run.Err != nil {
		t.Fatalf("start refused to recover a v2.27 dead end: %v\n%s", run.Err, run.Output)
	}

	// Both dead roles were respawned, and the plan named them as recoverable
	// rather than as a conflict.
	if len(run.SpawnedRole) != len(roles) {
		t.Errorf("start spawned %v, want all of %v respawned", run.SpawnedRole, roles)
	}
	for _, role := range roles {
		rec, err := launch.Read(filepath.Join(fixture.Root, "agents", role))
		if err != nil {
			t.Fatalf("no launch record for %s after recovery: %v", role, err)
		}
		if rec.AgentPID != pids[role] {
			t.Errorf("%s recovered with pid %d, want the respawned %d", role, rec.AgentPID, pids[role])
		}
		// The rewritten record must not carry the deleted prepared fields forward.
		if rec.PreparedRunGeneration != "" || rec.PreparedRunDigest != "" || rec.PreparedRunLaunchAttempt != "" {
			t.Errorf("%s recovered record still carries prepared-run fields: gen=%q digest=%q attempt=%q",
				role, rec.PreparedRunGeneration, rec.PreparedRunDigest, rec.PreparedRunLaunchAttempt)
		}
		if rec.BootstrapExpectation != nil {
			t.Errorf("%s recovered record still carries a bootstrap expectation", role)
		}
	}

	// Nothing was deleted: the old dirs are ignored, not reaped.
	afterRoot := v228InventoryPaths(t, fixture.Root)
	for path := range beforeRoot {
		if !afterRoot[path] {
			t.Errorf("recovery deleted %s from the namespace; #644 requires non-destructive rollforward", path)
		}
	}
	afterSquad := v228InventoryPaths(t, filepath.Join(fixture.Project, ".amq-squad"))
	for path := range beforeSquad {
		if !afterSquad[path] {
			t.Errorf("recovery deleted %s from .amq-squad", path)
		}
	}
	if _, err := os.Stat(preparedPath); err != nil {
		t.Errorf("recovery removed the prepared remnant %s: %v (old dirs are ignored, not auto-deleted)", preparedPath, err)
	}

	// A second start has nothing left to do.
	settled := v228RunStart(t, fixture, alive, func(role string) int { return pids[role] })
	if settled.Err != nil {
		t.Fatalf("settled start after recovery: %v\n%s", settled.Err, settled.Output)
	}
	if len(settled.SpawnedRole) != 0 {
		t.Errorf("second start respawned %v, want nothing (recovered roles are live)", settled.SpawnedRole)
	}
}
