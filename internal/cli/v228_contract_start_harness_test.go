package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/omriariav/amq-squad/v3/internal/rules"
	"github.com/omriariav/amq-squad/v3/internal/team"
)

// Shared hermetic harness for the tranche-2 criteria that drive `start`
// (AC11 legacy-namespace recovery, AC12 roster changes).
//
// Names are deliberately distinct from the tranche-1 crash-injection file's
// v228Crash* helpers: that file is behind the v228seam build tag until step 3
// removes it, and both files must coexist in one package afterward.

// v228StartFixture is a project with a profile, team rules, and a canonical root.
type v228StartFixture struct {
	Project string
	Profile string
	Session string
	Root    string
}

func v228NewStartFixture(t *testing.T, session string, members []team.Member) v228StartFixture {
	t.Helper()
	project := canonicalFilesystemPath(t.TempDir())
	const profile = "v228"
	v228SeedProfile(t, project, profile, session, members)
	if err := os.MkdirAll(filepath.Dir(rules.Path(project)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(rules.Path(project), []byte("# team rules\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return v228StartFixture{
		Project: project, Profile: profile, Session: session,
		Root: v228CanonicalRoot(project, profile, session),
	}
}

func v228StartMembers(session string, roles ...string) []team.Member {
	members := make([]team.Member, 0, len(roles))
	for _, role := range roles {
		members = append(members, team.Member{Role: role, Binary: "codex", Handle: role, Session: session})
	}
	return members
}

// v228StartRun is one `start` invocation and what the fake backend observed.
type v228StartRun struct {
	Err         error
	Output      string
	SpawnedRole []string
}
