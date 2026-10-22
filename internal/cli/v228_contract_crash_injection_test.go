// AC2 / AC6 (#644 class) crash-injection contract tests.
//
// This file is written against the REAL Phase 2 seam in simple_start.go:
// runStartWithDependencies, simpleStartDependencies.AfterCheckpoint, the four
// simpleStartCheckpoint* constants, and *simpleStartCheckpointError. Those
// symbols live on wt/v2-28-0-senior and are not on this branch yet, so the file
// is gated behind the v228seam build tag: main cannot see it and stays green.
//
// P2 INTEGRATION STEP: delete the //go:build line above. That is the whole
// change — no placeholder types to unwind, no assertions to rewrite. Until then:
//
//	go test ./internal/cli/ -tags v228seam -run TestV228   # fails to compile pre-merge, by design
//
// The env guard is kept so the tests stay opt-in after the tag goes away.
//
// Only the announced contract is used. Internal helpers of the simple launcher
// (plan building, record verification) are deliberately not touched: they were
// still being renamed while this was written, and depending on them would make
// the file break on refactors that do not change the seam.
package cli

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/omriariav/amq-squad/v3/internal/rules"
	"github.com/omriariav/amq-squad/v3/internal/team"
)

var errV228InjectedCrash = errors.New("v2.28 contract: injected launcher crash")

// v228CrashFixture is one project/profile/session with two roles.
type v228CrashFixture struct {
	Project string
	Profile string
	Session string
	Root    string
	Roles   []string
	PIDs    map[string]int
}

func v228NewCrashFixture(t *testing.T) v228CrashFixture {
	t.Helper()
	project := canonicalFilesystemPath(t.TempDir())
	const (
		profile = "v228"
		session = "ac2"
	)
	roles := []string{"cto", "dev"}
	members := make([]team.Member, 0, len(roles))
	for _, role := range roles {
		members = append(members, team.Member{Role: role, Binary: "codex", Handle: role, Session: session})
	}
	v228SeedProfile(t, project, profile, session, members)
	// start reads the team rules to compose the one startup instruction.
	if err := os.MkdirAll(filepath.Dir(rules.Path(project)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(rules.Path(project), []byte("# team rules\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return v228CrashFixture{
		Project: project, Profile: profile, Session: session,
		Root:  v228CanonicalRoot(project, profile, session),
		Roles: roles,
		PIDs:  map[string]int{"cto": 4601, "dev": 4602},
	}
}

// v228CrashRun is one `start` invocation plus what the launcher did on the way.
type v228CrashRun struct {
	Err      error
	Output   string
	Launches int
	Reached  []simpleStartCheckpoint
}
