package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/flock"
	"github.com/omriariav/amq-squad/v2/internal/launch"
	squadnamespace "github.com/omriariav/amq-squad/v2/internal/namespace"
	"github.com/omriariav/amq-squad/v2/internal/team"
)

// AC5: two (here: eight) concurrent `start` invocations must produce exactly one
// squad — one spawn per role, no duplicate workers. The mechanism the plan keeps
// is the session launch lock plus a liveness re-read UNDER that lock: whoever
// gets in first spawns, everyone after that observes the live record and rolls
// forward instead of spawning again.
//
// The reconcile body below is driven by the production pieces it must use: the
// duplicate-launch preflight (the liveness re-read) and locked atomic launch
// record writes. Spawning itself is represented by the record write, because a
// real child process is not hermetic — the contract under test is "how many
// times does the launcher decide to spawn", which is decided before exec.
//
// TODO(P2): replace v228SessionLaunchLockPath with the launcher's exported
// session launch lock once the simple launcher lands, so the test locks the same
// file production does instead of an agreed-upon path.
func v228SessionLaunchLockPath(project, profile, session string) string {
	dir := filepath.Join(project, team.DirName, "locks")
	return filepath.Join(dir, squadnamespace.NormalizeProfile(profile)+"."+session+".launch.lock")
}

func TestV228ContractConcurrentStartsLaunchOneSquad(t *testing.T) {
	requireV228Contract(t)
	setupFakeAMQSessionRoots(t)

	project := canonicalFilesystemPath(t.TempDir())
	const (
		profile     = "v228"
		session     = "ac5"
		starters    = 8
		basePIDUnit = 1000
	)
	roles := []string{"cto", "dev"}
	members := make([]team.Member, 0, len(roles))
	for _, role := range roles {
		members = append(members, team.Member{Role: role, Binary: "codex", Handle: role, Session: session})
	}
	v228SeedProfile(t, project, profile, session, members)

	root := v228CanonicalRoot(project, profile, session)
	lockPath := v228SessionLaunchLockPath(project, profile, session)
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o755); err != nil {
		t.Fatal(err)
	}

	var (
		mu           sync.Mutex
		spawns       = map[string][]int{}
		holders      int
		maxHolders   int
		aliveByPID   = map[int]bool{}
		spawnFailure error
	)
	probe := duplicateLaunchProbe{
		PIDAlive: func(pid int) bool {
			mu.Lock()
			defer mu.Unlock()
			return aliveByPID[pid]
		},
		ProcessMatch: func(pid int, _ func(args string) bool) bool {
			mu.Lock()
			defer mu.Unlock()
			return aliveByPID[pid]
		},
		Now: func() time.Time { return v228Now },
	}

	// startOnce is one `start` invocation: under the session launch lock, for each
	// role, re-read liveness and spawn only what is not already live.
	startOnce := func(attempt int) error {
		return flock.WithLock(lockPath, func() error {
			mu.Lock()
			holders++
			if holders > maxHolders {
				maxHolders = holders
			}
			mu.Unlock()
			defer func() {
				mu.Lock()
				holders--
				mu.Unlock()
			}()

			for i, role := range roles {
				agentDir := filepath.Join(root, "agents", role)
				plan := agentLaunchPreflight{
					Role: role, CWD: project, AgentDir: agentDir, Handle: role,
					Workstream: session, Root: root, BaseRoot: filepath.Dir(root),
					Binary: "codex",
					// Read-only inspection: a concurrent start must never delete
					// another starter's in-flight artifacts.
					DryRun: true,
				}
				blocker, err := plan.check(probe)
				if err != nil {
					return fmt.Errorf("preflight %s: %w", role, err)
				}
				if blocker != nil && len(blocker.Reasons) > 0 {
					// Already live: keep it, roll forward.
					continue
				}
				pid := basePIDUnit*(attempt+1) + i
				if err := launch.Write(agentDir, launch.Record{
					Binary: "codex", Role: role, Handle: role, Session: session,
					TeamProfile: profile, TeamHome: project, CWD: project,
					Root: root, BaseRoot: filepath.Dir(root),
					AgentPID: pid, StartedAt: v228Now,
				}); err != nil {
					return fmt.Errorf("record %s: %w", role, err)
				}
				mu.Lock()
				aliveByPID[pid] = true
				spawns[role] = append(spawns[role], pid)
				mu.Unlock()
			}
			return nil
		})
	}

	release := make(chan struct{})
	var wg sync.WaitGroup
	for attempt := 0; attempt < starters; attempt++ {
		wg.Add(1)
		go func(attempt int) {
			defer wg.Done()
			<-release
			if err := startOnce(attempt); err != nil {
				mu.Lock()
				if spawnFailure == nil {
					spawnFailure = err
				}
				mu.Unlock()
			}
		}(attempt)
	}
	close(release)
	wg.Wait()

	if spawnFailure != nil {
		t.Fatalf("concurrent start failed: %v", spawnFailure)
	}
	if maxHolders != 1 {
		t.Errorf("session launch lock allowed %d concurrent holders, want 1 (a shared lock cannot prevent double-spawn)", maxHolders)
	}
	for _, role := range roles {
		if got := spawns[role]; len(got) != 1 {
			t.Errorf("%s spawned %d times (pids %v), want exactly 1", role, len(got), got)
		}
	}
	entries, err := launch.ScanEntries(project)
	if err != nil {
		t.Fatal(err)
	}
	perRole := map[string]int{}
	for _, entry := range entries {
		perRole[entry.Record.Role]++
	}
	for _, role := range roles {
		if perRole[role] != 1 {
			t.Errorf("%s has %d launch records on disk, want 1", role, perRole[role])
		}
	}
	if len(entries) != len(roles) {
		t.Errorf("scanned %d launch records, want %d (one squad)", len(entries), len(roles))
	}
}
