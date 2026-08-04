package cli

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/launch"
	squadnamespace "github.com/omriariav/amq-squad/v2/internal/namespace"
	"github.com/omriariav/amq-squad/v2/internal/team"
)

// v2.28 "Simple Mode" class-level contract tests (docs/v2.28-simple-mode-plan.md).
//
// These tests pin the v2.28 acceptance criteria for #643/#644/#645. They are
// opt-in: without AMQ_SQUAD_V228_CONTRACT=1 every one of them skips, so main
// stays green while the target behavior is still being built. Under the env var
// they are expected to FAIL on main — that is what they are for.
//
// Every test here is hermetic: temp dirs only, injected probes, no real tmux
// server and no reads of the developer's ~/.amq state.
const v228ContractEnv = "AMQ_SQUAD_V228_CONTRACT"

// requireV228Contract is the opt-in guard every v2.28 contract test starts with.
func requireV228Contract(t *testing.T) {
	t.Helper()
	if os.Getenv(v228ContractEnv) != "1" {
		t.Skip("v2.28 contract test; enable with AMQ_SQUAD_V228_CONTRACT=1")
	}
}

// v228Probe is a deterministic liveness probe: the listed PIDs are alive and
// binary-matched, everything else is dead. Birth time and TTY are left unset so
// the shared classifier takes its record-only path.
func v228Probe(alivePIDs ...int) duplicateLaunchProbe {
	alive := map[int]bool{}
	for _, pid := range alivePIDs {
		alive[pid] = true
	}
	return duplicateLaunchProbe{
		PIDAlive:     func(pid int) bool { return alive[pid] },
		ProcessMatch: func(pid int, _ func(args string) bool) bool { return alive[pid] },
		Now:          func() time.Time { return v228Now },
	}
}

var v228Now = time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)

// v228ContractWaitBudget bounds every wait in this suite.
//
// SUITE RULE: no deadline here is shorter than a minute. These are failure
// bounds, never ordering — correctness comes from channel handoff — so a
// generous budget costs only the wall time of a genuine failure. A short one
// costs far more: this suite is the release's acceptance record, and under a
// loaded machine a tight bound makes a healthy system look like the exact defect
// the test guards. A test that accuses production of its own subject matter
// because CI was busy is worse than no test. Every timeout message must name the
// saturated-machine possibility alongside the defect it is hunting.
const v228ContractWaitBudget = 60 * time.Second

// v228SeedProfile writes a named profile without going through seedTeam's cwd
// switch, so a test can address the same project through two path spellings.
func v228SeedProfile(t *testing.T, projectDir, profile, session string, members []team.Member) {
	t.Helper()
	if err := team.WriteProfile(projectDir, profile, team.Team{
		Project:            projectDir,
		Workstream:         session,
		Members:            members,
		SharedCwdException: "v2.28 contract fixture: not exercising #497 worktree isolation",
	}); err != nil {
		t.Fatal(err)
	}
}

// v228SeedLiveRecord writes a launch record for handle under the exact root the
// caller names, so a test controls which root is authoritative.
func v228SeedLiveRecord(t *testing.T, root string, rec launch.Record) string {
	t.Helper()
	agentDir := filepath.Join(root, "agents", rec.Handle)
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := launch.Write(agentDir, rec); err != nil {
		t.Fatal(err)
	}
	return agentDir
}

// v228CanonicalRoot is the one canonical AMQ root for a project/profile/session
// under v2.28: no custom roots, no multi-candidate election.
func v228CanonicalRoot(project, profile, session string) string {
	return squadnamespace.AMQRoot(canonicalFilesystemPath(project), profile, session)
}

// v228InventoryPaths lists every path under dir, used to assert that a rerun of
// `start` after an aborted launch deletes nothing.
func v228InventoryPaths(t *testing.T, dir string) map[string]bool {
	t.Helper()
	found := map[string]bool{}
	err := filepath.Walk(dir, func(path string, _ os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, relErr := filepath.Rel(dir, path)
		if relErr != nil {
			return relErr
		}
		found[rel] = true
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		t.Fatalf("inventory %s: %v", dir, err)
	}
	return found
}
