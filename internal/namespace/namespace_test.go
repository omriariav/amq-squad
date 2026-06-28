package namespace

import (
	"path/filepath"
	"testing"
)

func TestResolveNamedProfileSession(t *testing.T) {
	ref := Resolve("/repo", "release", "main")
	if ref.ID != "release/main" || ref.Display != "release/main" {
		t.Fatalf("namespace identity = %q / %q", ref.ID, ref.Display)
	}
	if ref.Profile != "release" || ref.Session != "main" || ref.AMQSession != "main" {
		t.Fatalf("namespace fields = %+v", ref)
	}
	if got, want := ref.Paths.ProfileConfig, filepath.Join("/repo", ".amq-squad", "teams", "release.json"); got != want {
		t.Fatalf("profile path = %q, want %q", got, want)
	}
	if got, want := ref.AMQRoot, filepath.Join("/repo", ".agent-mail", "release", "main"); got != want {
		t.Fatalf("amq root = %q, want %q", got, want)
	}
	if got, want := ref.Paths.AMQRoot, filepath.Join("/repo", ".agent-mail", "release", "main"); got != want {
		t.Fatalf("amq path = %q, want %q", got, want)
	}
	if got, want := ref.Paths.Brief, filepath.Join("/repo", ".amq-squad", "briefs", "release", "main.md"); got != want {
		t.Fatalf("brief path = %q, want %q", got, want)
	}
	if got, want := ref.Paths.Tasks, filepath.Join("/repo", ".amq-squad", "tasks", "release", "main"); got != want {
		t.Fatalf("tasks path = %q, want %q", got, want)
	}
}

func TestResolveDefaultsProfileAndRootSession(t *testing.T) {
	ref := Resolve("", "", "")
	if ref.Profile != "default" || ref.ID != "default/_root" || ref.Display != "default/<root>" {
		t.Fatalf("default/root namespace = %+v", ref)
	}
	if ref.Paths.ProfileConfig != "" || ref.Paths.Brief != "" || ref.Paths.Tasks != "" {
		t.Fatalf("empty team-home should not emit paths: %+v", ref.Paths)
	}
}

func TestProfilesEqualNormalizesDefault(t *testing.T) {
	if !ProfilesEqual("", "default") {
		t.Fatal("empty launch profile should match the default profile")
	}
	if ProfilesEqual("release", "") {
		t.Fatal("named profile must not match an empty/default launch profile")
	}
}
