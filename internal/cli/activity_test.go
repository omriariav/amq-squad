package cli

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/activity"
	"github.com/omriariav/amq-squad/v2/internal/team"
)

func withFixedActivityNow(t *testing.T) time.Time {
	t.Helper()
	now := time.Date(2026, 7, 6, 10, 0, 0, 0, time.UTC)
	prev := activityNow
	activityNow = func() time.Time { return now }
	t.Cleanup(func() { activityNow = prev })
	return now
}

func TestActivitySetWritesHeartbeat(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	writeDispatchTeam(t, dir)
	now := withFixedActivityNow(t)
	_ = withAMQCommandSeams(t, amqEnv{Root: ".agent-mail/{session}", BaseRoot: ".agent-mail"}, "")

	stdout, _, err := captureOutput(t, func() error {
		return runActivity([]string{"set", "--session", "issue-96", "--me", "qa", "--phase", "testing", "--task", "t11", "--detail", "make ci", "--json"})
	})
	if err != nil {
		t.Fatalf("activity set: %v\n%s", err, stdout)
	}
	env := decodeJSONEnvelope[mutationResult](t, stdout)
	if env.Kind != "activity" || env.Data.Status != "written" || env.Data.Session != "issue-96" || env.Data.Handle != "qa" || env.Data.TaskID != "t11" {
		t.Fatalf("activity result = %+v", env)
	}

	snap, ok, err := activity.Read(filepath.Join(dir, ".agent-mail", "issue-96", "agents", "qa"), now, activity.DefaultStaleAfter)
	if err != nil {
		t.Fatalf("read activity: %v", err)
	}
	if !ok || snap.Source != activity.SourceHeartbeat || snap.Quality != activity.StateFresh ||
		snap.TaskID != "t11" || snap.Phase != "testing" || snap.Detail != "make ci" {
		t.Fatalf("activity snapshot = %+v ok=%v", snap, ok)
	}
}

func TestActivityClearRemovesHeartbeat(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	writeDispatchTeam(t, dir)
	_ = withAMQCommandSeams(t, amqEnv{Root: ".agent-mail/{session}", BaseRoot: ".agent-mail"}, "")
	agentDir := filepath.Join(dir, ".agent-mail", "issue-96", "agents", "qa")
	if err := activity.Write(agentDir, activity.File{Handle: "qa", Phase: "testing"}); err != nil {
		t.Fatalf("seed activity: %v", err)
	}

	stdout, _, err := captureOutput(t, func() error {
		return runActivity([]string{"clear", "--session", "issue-96", "--me", "qa", "--json"})
	})
	if err != nil {
		t.Fatalf("activity clear: %v\n%s", err, stdout)
	}
	env := decodeJSONEnvelope[mutationResult](t, stdout)
	if env.Data.Status != "cleared" || env.Data.Handle != "qa" {
		t.Fatalf("activity clear result = %+v", env.Data)
	}
	if _, ok, err := activity.Read(agentDir, time.Now(), activity.DefaultStaleAfter); err != nil || ok {
		t.Fatalf("activity should be gone, ok=%v err=%v", ok, err)
	}
}

func TestActivitySetRequiresSession(t *testing.T) {
	chdir(t, t.TempDir())
	_, _, err := captureOutput(t, func() error {
		return runActivity([]string{"set", "--me", "qa", "--phase", "testing"})
	})
	if err == nil || !strings.Contains(err.Error(), "requires --session") {
		t.Fatalf("want --session error, got %v", err)
	}
}

func TestActivitySetRejectsUnsafeHandle(t *testing.T) {
	chdir(t, t.TempDir())
	_, _, err := captureOutput(t, func() error {
		return runActivity([]string{"set", "--session", "issue-96", "--me", "../qa", "--phase", "testing"})
	})
	if err == nil || !strings.Contains(err.Error(), "invalid --me") {
		t.Fatalf("want invalid --me error, got %v", err)
	}
}

func TestUnprofiledActivitySetRefusesNamedProfileShadow(t *testing.T) {
	dir := t.TempDir()
	resumeChdir(t, dir)
	seedProfile(t, dir, team.DefaultProfile, team.Team{
		Workstream: "main",
		Members: []team.Member{
			{Role: "qa", Binary: "codex", Handle: "qa", Session: "main"},
		},
	})
	seedProfile(t, dir, "release", team.Team{
		Workstream: "main",
		Members: []team.Member{
			{Role: "qa", Binary: "codex", Handle: "qa", Session: "main"},
		},
	})
	namedRoot := filepath.Join(dir, ".agent-mail", "release", "main")
	if err := activity.Write(filepath.Join(namedRoot, "agents", "qa"), activity.File{Handle: "qa", Phase: "testing"}); err != nil {
		t.Fatalf("seed named activity: %v", err)
	}

	_, _, err := captureOutput(t, func() error {
		return runActivity([]string{"set", "--session", "main", "--me", "qa", "--phase", "testing"})
	})
	if err == nil {
		t.Fatal("unprofiled activity set should refuse before writing legacy root")
	}
	for _, want := range []string{"default-profile", "release", "--profile release", "--profile default", "refusing before write"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("activity set error missing %q:\n%v", want, err)
		}
	}
	legacyRoot := filepath.Join(dir, ".agent-mail", "main")
	if _, ok, statErr := activity.Read(filepath.Join(legacyRoot, "agents", "qa"), time.Now(), activity.DefaultStaleAfter); statErr != nil || ok {
		t.Fatalf("refused activity set must not write legacy root; ok=%v err=%v", ok, statErr)
	}
}

func TestExplicitDefaultActivitySetEscapesNamedProfileShadow(t *testing.T) {
	dir := t.TempDir()
	resumeChdir(t, dir)
	seedProfile(t, dir, team.DefaultProfile, team.Team{
		Workstream: "main",
		Members: []team.Member{
			{Role: "qa", Binary: "codex", Handle: "qa", Session: "main"},
		},
	})
	seedProfile(t, dir, "release", team.Team{
		Workstream: "main",
		Members: []team.Member{
			{Role: "qa", Binary: "codex", Handle: "qa", Session: "main"},
		},
	})
	if err := activity.Write(filepath.Join(dir, ".agent-mail", "release", "main", "agents", "qa"), activity.File{Handle: "qa", Phase: "testing"}); err != nil {
		t.Fatalf("seed named activity: %v", err)
	}
	now := withFixedActivityNow(t)
	_ = withAMQCommandSeams(t, amqEnv{Root: ".agent-mail/{session}", BaseRoot: ".agent-mail"}, "")

	if _, _, err := captureOutput(t, func() error {
		return runActivity([]string{"set", "--profile", "default", "--session", "main", "--me", "qa", "--phase", "coding"})
	}); err != nil {
		t.Fatalf("explicit default activity set should proceed: %v", err)
	}
	snap, ok, err := activity.Read(filepath.Join(dir, ".agent-mail", "main", "agents", "qa"), now, activity.DefaultStaleAfter)
	if err != nil {
		t.Fatalf("read explicit default activity: %v", err)
	}
	if !ok || snap.Phase != "coding" || snap.Quality != activity.StateFresh {
		t.Fatalf("explicit default activity = %+v ok=%v", snap, ok)
	}
}
