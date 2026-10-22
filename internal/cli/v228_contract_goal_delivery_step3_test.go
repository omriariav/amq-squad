// AC9 goal-delivery bodies, written against the step-3 seam
// simpleStartDependencies.DeliverGoal (simple_start.go, called after every role
// verifies live and only when this start actually spawned something).
//
// That seam does not exist on this branch's base (63b9161, step 2); it arrives
// with step 3. The file is therefore gated behind the v228step3 build tag so the
// base stays green.
//
// P3 INTEGRATION STEP: delete the //go:build line above. Nothing else changes.
//
//	go test ./internal/cli/ -tags v228step3 -run TestV228   # pre-merge: does not compile, by design
package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/omriariav/amq-squad/v3/internal/rules"
	"github.com/omriariav/amq-squad/v3/internal/team"
)

// v228SeedOrchestratedProfile writes a profile with an orchestration lead, which
// is what deliverSimpleStartGoal requires to resolve a goal recipient.
func v228SeedOrchestratedProfile(t *testing.T, projectDir, profile, session, lead string, members []team.Member) {
	t.Helper()
	if err := team.WriteProfile(projectDir, profile, team.Team{
		Project:            projectDir,
		Workstream:         session,
		Members:            members,
		Orchestrated:       true,
		Lead:               lead,
		SharedCwdException: "v2.28 contract fixture: not exercising #497 worktree isolation",
	}); err != nil {
		t.Fatal(err)
	}
}

// v228GoalFixture is an orchestrated project ready for `start --goal`.
type v228GoalFixture struct {
	Project string
	Profile string
	Session string
	Root    string
	Lead    string
	Roles   []string
}

func v228NewGoalFixture(t *testing.T, session, lead string, roles []string) v228GoalFixture {
	t.Helper()
	project := canonicalFilesystemPath(t.TempDir())
	const profile = "v228"
	v228SeedOrchestratedProfile(t, project, profile, session, lead, v228StartMembers(session, roles...))
	if err := os.MkdirAll(filepath.Dir(rules.Path(project)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(rules.Path(project), []byte("# team rules\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	briefPath := briefPathForProfile(project, profile, session)
	if err := os.MkdirAll(filepath.Dir(briefPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(briefPath, []byte("# existing reviewed brief\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return v228GoalFixture{
		Project: project, Profile: profile, Session: session,
		Root: v228CanonicalRoot(project, profile, session), Lead: lead, Roles: roles,
	}
}
