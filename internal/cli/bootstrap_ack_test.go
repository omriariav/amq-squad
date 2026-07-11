package cli

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/bootstrapack"
	"github.com/omriariav/amq-squad/v2/internal/launch"
	"github.com/omriariav/amq-squad/v2/internal/team"
)

func TestBootstrapAckUsesVerifiedRosterActorAndCustomRoot(t *testing.T) {
	project := t.TempDir()
	root := filepath.Join(t.TempDir(), "custom", "named", "issue-396")
	agentDir := filepath.Join(root, "agents", "qa")
	if err := team.WriteProfile(project, "named", team.Team{Project: project, Members: []team.Member{{Role: "qa", Handle: "qa", Binary: "codex", Session: "issue-396"}}}); err != nil {
		t.Fatal(err)
	}
	expect, err := bootstrapack.NewExpectation(true, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	rec := launch.Record{CWD: project, Binary: "codex", Handle: "qa", Role: "qa", Session: "issue-396", Root: root, TeamHome: project, TeamProfile: "named", BootstrapExpectation: &expect, StartedAt: time.Now()}
	if err := launch.Write(agentDir, rec); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AM_ROOT", root)
	t.Setenv("AM_ME", "qa")
	old := resolveVerifiedOperatorActor
	t.Cleanup(func() { resolveVerifiedOperatorActor = old })
	called := false
	resolveVerifiedOperatorActor = func(gotProject, profile, session, role, handle string) (verifiedOperatorActor, error) {
		called = true
		return verifiedOperatorActor{Role: role, Handle: handle, Profile: profile, Session: session, Root: root, PaneID: "%9"}, nil
	}
	if err := runBootstrapAck([]string{"--skill-version", "2.19.0", "--steps", "startup-files,initial-drain,context-review"}); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("verified actor seam not used")
	}
	marker, err := bootstrapack.Read(agentDir)
	if err != nil {
		t.Fatal(err)
	}
	if marker.LaunchID != expect.LaunchID || marker.Profile != "named" || marker.Root != root {
		t.Fatalf("marker=%+v", marker)
	}
}

func TestBootstrapAckRejectsIncompleteSteps(t *testing.T) {
	t.Setenv("AM_ROOT", filepath.Join(t.TempDir(), "root"))
	t.Setenv("AM_ME", "qa")
	if err := os.MkdirAll(os.Getenv("AM_ROOT"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := runBootstrapAck([]string{"--skill-version", "2.19.0", "--steps", "startup-files"}); err == nil {
		t.Fatal("expected rejection")
	}
}
