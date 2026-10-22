package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/bootstrapack"
	"github.com/omriariav/amq-squad/v3/internal/launch"
	squadnamespace "github.com/omriariav/amq-squad/v3/internal/namespace"
	"github.com/omriariav/amq-squad/v3/internal/team"
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
