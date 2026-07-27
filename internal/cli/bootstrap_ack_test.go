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
	if err := runBootstrapAck([]string{"--skill-version", "2.19.0", "--steps", "startup-files,initial-drain,context-review"}, "v2.25.0"); err != nil {
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
	if err := runBootstrapAck([]string{"--skill-version", "2.19.0", "--steps", "startup-files"}, "v2.25.0"); err == nil {
		t.Fatal("expected rejection")
	}
}

// bootstrapAckEnv is the shared setup the skill-version resolution tests need: a roster,
// a launch record expecting acknowledgement, the launch-provided environment, and the
// verified-actor seam stubbed. Factored from
// TestBootstrapAckUsesVerifiedRosterActorAndCustomRoot rather than invented, so the
// resolution tests exercise the same path a real ack takes.
type bootstrapAckEnv struct{ agentDir string }

func newBootstrapAckEnv(t *testing.T) bootstrapAckEnv {
	t.Helper()
	project := t.TempDir()
	root := filepath.Join(t.TempDir(), "custom", "named", "issue-534")
	agentDir := filepath.Join(root, "agents", "qa")
	if err := team.WriteProfile(project, "named", team.Team{Project: project, Members: []team.Member{{Role: "qa", Handle: "qa", Binary: "codex", Session: "issue-534"}}}); err != nil {
		t.Fatal(err)
	}
	expect, err := bootstrapack.NewExpectation(true, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	rec := launch.Record{CWD: project, Binary: "codex", Handle: "qa", Role: "qa", Session: "issue-534", Root: root, TeamHome: project, TeamProfile: "named", BootstrapExpectation: &expect, StartedAt: time.Now()}
	if err := launch.Write(agentDir, rec); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AM_ROOT", root)
	t.Setenv("AM_ME", "qa")
	old := resolveVerifiedOperatorActor
	t.Cleanup(func() { resolveVerifiedOperatorActor = old })
	resolveVerifiedOperatorActor = func(gotProject, profile, session, role, handle string) (verifiedOperatorActor, error) {
		return verifiedOperatorActor{Role: role, Handle: handle, Profile: profile, Session: session, Root: root, PaneID: "%9"}, nil
	}
	return bootstrapAckEnv{agentDir: agentDir}
}

func (e bootstrapAckEnv) marker(t *testing.T) bootstrapack.Marker {
	t.Helper()
	marker, err := bootstrapack.Read(e.agentDir)
	if err != nil {
		t.Fatalf("read ack marker: %v", err)
	}
	return marker
}

// #534 made --skill-version optional: the preamble an agent read it from is gone, so
// requiring the flag made the documented ack instruction unfollowable. These cover the
// three resolution paths and the launch-path fail-open posture.

func TestBootstrapAckResolvesSkillVersionWithoutTheFlag(t *testing.T) {
	env := newBootstrapAckEnv(t)
	restore := bootstrapAckSkillVersion
	bootstrapAckSkillVersion = func(string) (string, bool) { return "2.25.0", true }
	t.Cleanup(func() { bootstrapAckSkillVersion = restore })

	if err := runBootstrapAck([]string{"--steps", "startup-files,initial-drain,context-review"}, "v2.25.0"); err != nil {
		t.Fatalf("ack without --skill-version: %v", err)
	}
	if got := env.marker(t).SkillVersion; got != "2.25.0" {
		t.Errorf("recorded skill version = %q, want the resolved 2.25.0", got)
	}
}

func TestBootstrapAckFlagOverridesResolution(t *testing.T) {
	env := newBootstrapAckEnv(t)
	restore := bootstrapAckSkillVersion
	bootstrapAckSkillVersion = func(string) (string, bool) { return "2.25.0", true }
	t.Cleanup(func() { bootstrapAckSkillVersion = restore })

	if err := runBootstrapAck([]string{"--skill-version", "9.9.9", "--steps", "startup-files,initial-drain,context-review"}, "v2.25.0"); err != nil {
		t.Fatalf("ack with explicit --skill-version: %v", err)
	}
	if got := env.marker(t).SkillVersion; got != "9.9.9" {
		t.Errorf("recorded skill version = %q, want the explicit override 9.9.9", got)
	}
}

func TestBootstrapAckFailsOpenWhenTheBundleIsUnreadable(t *testing.T) {
	env := newBootstrapAckEnv(t)
	restore := bootstrapAckSkillVersion
	bootstrapAckSkillVersion = func(string) (string, bool) { return "", false }
	t.Cleanup(func() { bootstrapAckSkillVersion = restore })

	// Launch-path posture: an unreadable bundle must not kill an otherwise healthy
	// launch. doctor stays fail-closed for the same data.
	if err := runBootstrapAck([]string{"--steps", "startup-files,initial-drain,context-review"}, "v2.25.0"); err != nil {
		t.Fatalf("ack must succeed when the bundle is unreadable, got: %v", err)
	}
	if got := env.marker(t).SkillVersion; got != "unknown" {
		t.Errorf("recorded skill version = %q, want \"unknown\"", got)
	}
}
