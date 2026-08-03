package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/launch"
	squadnamespace "github.com/omriariav/amq-squad/v2/internal/namespace"
	"github.com/omriariav/amq-squad/v2/internal/team"
)

// AC1 (#643 class): a fresh project whose path the operator spelled with the
// wrong on-disk case must launch on the first try. The v2.28 contract is that
// the project path is canonicalized ONCE (EvalSymlinks + on-disk case) and every
// later component consumes that canonical value instead of re-deriving its own
// spelling. #643 was two components deriving the same path differently and then
// comparing the results.

// v228MiscasedProject creates a real directory with a mixed-case leaf and
// returns (realSpelling, lowercasedSpelling). It skips when the filesystem is
// case sensitive, because the defect class only exists on a case-insensitive one.
func v228MiscasedProject(t *testing.T) (string, string) {
	t.Helper()
	parent := t.TempDir()
	real := filepath.Join(parent, "Project")
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatal(err)
	}
	miscased := filepath.Join(parent, "project")
	if _, err := os.Stat(miscased); err != nil {
		t.Skipf("case-insensitive filesystem required for the #643 repro: %v", err)
	}
	return real, miscased
}

func TestV228ContractMiscasedProjectRendersOneCanonicalBootstrap(t *testing.T) {
	requireV228Contract(t)
	real, miscased := v228MiscasedProject(t)
	const (
		profile = "v228"
		session = "ac1"
		handle  = "cto"
	)
	canonical := canonicalFilesystemPath(real)
	v228SeedProfile(t, real, profile, session, []team.Member{
		{Role: handle, Binary: "codex", Handle: handle, Session: session},
	})

	build := func(project string) (bootstrapContext, string) {
		t.Helper()
		root := squadnamespace.AMQRoot(project, profile, session)
		agentDir := filepath.Join(root, "agents", handle)
		rec := launch.Record{
			Binary: "codex", Role: handle, Handle: handle, Session: session,
			TeamProfile: profile, TeamHome: project, CWD: project, Root: root,
			AgentPID: 4101, StartedAt: v228Now,
		}
		ctx := bootstrapContextFor(rec, agentDir, project)
		prompt, err := buildBootstrapPrompt(ctx)
		if err != nil {
			t.Fatalf("buildBootstrapPrompt(%s): %v", project, err)
		}
		return ctx, prompt
	}

	canonicalCtx, canonicalPrompt := build(canonical)
	miscasedCtx, miscasedPrompt := build(miscased)

	// The rendered prompt must never embed a non-canonical spelling: the bootstrap
	// bytes are what #643 hashed and compared across the prepare/spawn boundary.
	for _, field := range []struct {
		name string
		got  string
	}{
		{"TeamHome", miscasedCtx.TeamHome},
		{"CWD", miscasedCtx.CWD},
		{"Root", miscasedCtx.Root},
		{"AgentDir", miscasedCtx.AgentDir},
		{"BriefPath", miscasedCtx.BriefPath},
		{"TeamRulesPath", miscasedCtx.TeamRulesPath},
		{"LaunchPath", miscasedCtx.LaunchPath},
	} {
		if field.got == "" {
			continue
		}
		if want := canonicalFilesystemPath(field.got); want != field.got {
			t.Errorf("bootstrap context %s = %q, want the canonical spelling %q", field.name, field.got, want)
		}
	}
	if miscasedPrompt != canonicalPrompt {
		t.Errorf("bootstrap prompt differs between two spellings of the same directory;\ncanonical context = %+v\nmiscased context  = %+v", canonicalCtx, miscasedCtx)
	}
}

func TestV228ContractMiscasedProjectStatusReportsCanonicalCoordinates(t *testing.T) {
	requireV228Contract(t)
	setupFakeAMQSessionRoots(t)
	swapStatusPaneLister(t, nil, nil)
	real, miscased := v228MiscasedProject(t)
	const (
		profile = "v228"
		session = "ac1"
		handle  = "cto"
		pid     = 4102
	)
	canonical := canonicalFilesystemPath(real)
	// The operator typed the wrong case, so that is what the profile records.
	v228SeedProfile(t, miscased, profile, session, []team.Member{
		{Role: handle, Binary: "codex", Handle: handle, Session: session},
	})
	canonicalRoot := v228CanonicalRoot(real, profile, session)
	v228SeedLiveRecord(t, canonicalRoot, launch.Record{
		Binary: "codex", Role: handle, Handle: handle, Session: session,
		TeamProfile: profile, TeamHome: canonical, CWD: canonical,
		Root: canonicalRoot, BaseRoot: filepath.Dir(canonicalRoot),
		AgentPID: pid, StartedAt: v228Now.Add(-time.Minute),
	})

	out, err := runStatusExec(t, statusExecution{
		ProjectDir:       miscased,
		Profile:          profile,
		RequestedSession: session,
		ExplicitSession:  true,
		JSON:             true,
		Probe:            v228Probe(pid),
	})
	if err != nil {
		t.Fatalf("executeStatus with a miscased --project: %v\n%s", err, out)
	}
	env := decodeJSONEnvelope[statusEnvelopeData](t, out)
	if env.Data.TeamHome != canonical {
		t.Errorf("status team_home = %q, want the canonical spelling %q", env.Data.TeamHome, canonical)
	}
	if len(env.Data.Records) != 1 {
		t.Fatalf("status records = %+v, want one row", env.Data.Records)
	}
	row := env.Data.Records[0]
	if row.Status != statusStateLive {
		t.Errorf("status for a live agent reached through a miscased project = %q (%s), want live", row.Status, row.Detail)
	}
	if row.Root != canonicalRoot {
		t.Errorf("status root = %q, want the canonical root %q", row.Root, canonicalRoot)
	}
	if row.CWD != canonical {
		t.Errorf("status cwd = %q, want the canonical spelling %q", row.CWD, canonical)
	}
	if strings.Contains(row.Root, filepath.Base(miscased)+string(os.PathSeparator)) {
		t.Errorf("status re-derived the root from the operator's spelling: %q", row.Root)
	}
}
