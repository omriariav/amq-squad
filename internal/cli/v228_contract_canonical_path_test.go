package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/omriariav/amq-squad/v3/internal/launch"
	squadnamespace "github.com/omriariav/amq-squad/v3/internal/namespace"
	"github.com/omriariav/amq-squad/v3/internal/team"
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
