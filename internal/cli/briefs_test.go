package cli

import (
	"os"
	"path/filepath"
	"testing"

	squadnamespace "github.com/omriariav/amq-squad/v3/internal/namespace"
)

func TestBriefPathResolution(t *testing.T) {
	teamHome := "/team/home"
	got := briefPathForProfile(teamHome, squadnamespace.DefaultProfile, "issue-96")
	want := filepath.Join(teamHome, ".amq-squad", "briefs", "issue-96.md")
	if got != want {
		t.Errorf("briefPath = %q, want %q", got, want)
	}
	if briefPathForProfile("", squadnamespace.DefaultProfile, "issue-96") != "" {
		t.Error("briefPath with empty teamHome must return empty")
	}
	if briefPathForProfile(teamHome, squadnamespace.DefaultProfile, "") != "" {
		t.Error("briefPath with empty session must return empty")
	}
}

func TestBriefPathAbsolutizesRelativeTeamHome(t *testing.T) {
	home := t.TempDir()
	chdir(t, home)
	got := briefPathForProfile(".", squadnamespace.DefaultProfile, "issue-96")
	if !filepath.IsAbs(got) {
		t.Errorf("briefPath('.', 'issue-96') = %q, want absolute", got)
	}
	// macOS /var -> /private/var; evaluate symlinks on both sides so the
	// comparison is path-equivalent rather than byte-equal.
	gotResolved, err := filepath.EvalSymlinks(got)
	if err != nil {
		// Path may not exist yet; that's fine, EvalSymlinks fails. Use
		// Clean as a best-effort fallback.
		gotResolved = filepath.Clean(got)
	}
	wantBase, err := filepath.EvalSymlinks(home)
	if err != nil {
		wantBase = home
	}
	want := filepath.Join(wantBase, ".amq-squad", "briefs", "issue-96.md")
	if gotResolved != want {
		t.Errorf("briefPath('.', 'issue-96') = %q (resolved %q), want %q", got, gotResolved, want)
	}
}

func TestResolveBriefHomePrefersExplicitTeamHome(t *testing.T) {
	cwd := t.TempDir()
	if got := resolveBriefHome("/some/team-home", cwd); got != "/some/team-home" {
		t.Errorf("explicit teamHome dropped: %q", got)
	}
}

func TestResolveBriefHomeFallsThroughOnlyWhenCWDIsConfigured(t *testing.T) {
	emptyCWD := t.TempDir()
	if got := resolveBriefHome("", emptyCWD); got != "" {
		t.Errorf("unconfigured cwd should not resolve a brief home, got %q", got)
	}

	configuredCWD := t.TempDir()
	if err := os.MkdirAll(filepath.Join(configuredCWD, ".amq-squad"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configuredCWD, ".amq-squad", "team-rules.md"), []byte("# Team Rules\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := resolveBriefHome("", configuredCWD); got != configuredCWD {
		t.Errorf("configured cwd fallback failed: got %q, want %q", got, configuredCWD)
	}
}
