package cli

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/launch"
	"github.com/omriariav/amq-squad/v3/internal/team"
)

// AC3 (#645 class): an isolated-worktree actor with a stale worktree-local
// .agent-mail remnant AND a live canonical launch record must be reported as the
// same live actor at the same canonical root by status, doctor, and down. A stale
// remnant is informational; it is never grounds for "missing".
//
// The v2.27 defect is that each surface re-resolves the AMQ root from the
// member's cwd (the worktree) instead of reading the launch record, so the
// worktree remnant wins over the live canonical record.

type v228RemnantFixture struct {
	Project       string
	Worktree      string
	CanonicalRoot string
	RemnantRoot   string
	Profile       string
	Session       string
}

func v228SeedWorktreeRemnantFixture(t *testing.T) (v228RemnantFixture, map[string]int) {
	t.Helper()
	project := canonicalFilesystemPath(t.TempDir())
	const (
		profile = "v228"
		session = "ac3"
	)
	worktree := filepath.Join(project, ".amq-squad", "worktrees", "dev")
	if err := os.MkdirAll(worktree, 0o755); err != nil {
		t.Fatal(err)
	}
	v228SeedProfile(t, project, profile, session, []team.Member{
		{Role: "cto", Binary: "codex", Handle: "cto", Session: session},
		{Role: "dev", Binary: "codex", Handle: "dev", Session: session, CWD: worktree},
	})

	// The stale remnant from a prior failed launch: a worktree-local root that
	// carries AMQ config but no live agent.
	remnantRoot := filepath.Join(worktree, ".agent-mail", profile, session)
	if err := os.MkdirAll(filepath.Join(remnantRoot, "meta"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(remnantRoot, "meta", "config.json"), []byte(`{"agents":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}

	canonicalRoot := v228CanonicalRoot(project, profile, session)
	pids := map[string]int{"cto": 4301, "dev": 4302}
	for _, member := range []struct {
		handle string
		cwd    string
	}{{"cto", project}, {"dev", worktree}} {
		v228SeedLiveRecord(t, canonicalRoot, launch.Record{
			Binary: "codex", Role: member.handle, Handle: member.handle, Session: session,
			TeamProfile: profile, TeamHome: project, CWD: member.cwd,
			Root: canonicalRoot, BaseRoot: filepath.Dir(canonicalRoot),
			AgentPID: pids[member.handle], StartedAt: v228Now.Add(-time.Hour),
		})
	}
	return v228RemnantFixture{
		Project: project, Worktree: worktree, CanonicalRoot: canonicalRoot,
		RemnantRoot: remnantRoot, Profile: profile, Session: session,
	}, pids
}
