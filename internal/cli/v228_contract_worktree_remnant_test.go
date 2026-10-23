package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
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

func TestV228ContractWorktreeRemnantStatusReportsLiveCanonicalActor(t *testing.T) {
	requireV228Contract(t)
	setupFakeAMQSessionRoots(t)
	fixture, pids := v228SeedWorktreeRemnantFixture(t)

	out, err := runStatusExec(t, statusExecution{
		ProjectDir:       fixture.Project,
		Profile:          fixture.Profile,
		RequestedSession: fixture.Session,
		ExplicitSession:  true,
		JSON:             true,
		Probe:            v228Probe(pids["cto"], pids["dev"]),
	})
	if err != nil {
		t.Fatalf("executeStatus: %v\n%s", err, out)
	}
	env := decodeJSONEnvelope[statusEnvelopeData](t, out)
	if len(env.Data.Records) != 2 {
		t.Fatalf("status records = %+v, want two rows", env.Data.Records)
	}
	for _, row := range env.Data.Records {
		if row.Status != statusStateLive {
			t.Errorf("%s status = %q (%s), want live", row.Role, row.Status, row.Detail)
		}
		if row.Root != fixture.CanonicalRoot {
			t.Errorf("%s root = %q, want the canonical root %q", row.Role, row.Root, fixture.CanonicalRoot)
		}
		if strings.HasPrefix(row.Root, fixture.Worktree) {
			t.Errorf("%s resolved the worktree-local remnant root %q", row.Role, row.Root)
		}
		if row.Signals.AgentPID != pids[row.Role] {
			t.Errorf("%s probed pid = %d, want the recorded pid %d", row.Role, row.Signals.AgentPID, pids[row.Role])
		}
	}
	// The remnant must survive untouched: it is an observation, not a target.
	if _, err := os.Stat(filepath.Join(fixture.RemnantRoot, "meta", "config.json")); err != nil {
		t.Errorf("status mutated the informational worktree remnant: %v", err)
	}
}

func TestV228ContractWorktreeRemnantDoctorAgreesWithStatus(t *testing.T) {
	requireV228Contract(t)
	setupFakeAMQSessionRoots(t)
	fixture, pids := v228SeedWorktreeRemnantFixture(t)

	d := newDoctorExec(t, fixture.Project)
	d.Profile = fixture.Profile
	d.WorkstreamHint = fixture.Session
	d.Probe = v228Probe(pids["cto"], pids["dev"])
	// newDoctorExec injects a canned wake builder; the whole point here is the
	// real record-first member classification, so drop the override.
	d.WakeOverride = nil

	checks, workstream := doctorCheckWake(d)
	if workstream != fixture.Session {
		t.Fatalf("doctor workstream = %q, want %q", workstream, fixture.Session)
	}
	for _, role := range []string{"cto", "dev"} {
		check := findCheck(checks, "wake "+role)
		if check == nil {
			t.Fatalf("doctor emitted no check for %s: %+v", role, checks)
		}
		if check.Status != doctorOK {
			t.Errorf("doctor %s = %q (%s), want ok for a live recorded actor", role, check.Status, check.Detail)
		}
		// doctorCheckFromStatus maps BOTH live and missing to ok, so status alone
		// cannot tell "found it alive" from "decided it was never there". The
		// detail is the discriminator: it must carry the probed pid evidence.
		if !strings.Contains(check.Detail, fmt.Sprintf("pid %d", pids[role])) {
			t.Errorf("doctor %s detail %q does not report the live recorded pid %d", role, check.Detail, pids[role])
		}
		if strings.Contains(check.Detail, fixture.Worktree) {
			t.Errorf("doctor %s detail names the worktree-local remnant: %s", role, check.Detail)
		}
	}
}
