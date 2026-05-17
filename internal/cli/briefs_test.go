package cli

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestBriefPathResolution(t *testing.T) {
	teamHome := "/team/home"
	got := briefPath(teamHome, "issue-96")
	want := filepath.Join(teamHome, ".amq-squad", "briefs", "issue-96.md")
	if got != want {
		t.Errorf("briefPath = %q, want %q", got, want)
	}
	if briefPath("", "issue-96") != "" {
		t.Error("briefPath with empty teamHome must return empty")
	}
	if briefPath(teamHome, "") != "" {
		t.Error("briefPath with empty session must return empty")
	}
}

func TestEnsureBriefStubCreatesMissing(t *testing.T) {
	home := t.TempDir()
	path, created, err := ensureBriefStub(home, "issue-96")
	if err != nil {
		t.Fatalf("ensureBriefStub: %v", err)
	}
	if !created {
		t.Error("created = false on first call, want true")
	}
	if path == "" {
		t.Fatal("path empty after successful creation")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"# issue-96", "## Goal", "## Status"} {
		if !strings.Contains(string(data), want) {
			t.Errorf("brief stub missing %q in:\n%s", want, data)
		}
	}
}

func TestEnsureBriefStubPreservesExisting(t *testing.T) {
	home := t.TempDir()
	briefDir := filepath.Join(home, ".amq-squad", "briefs")
	if err := os.MkdirAll(briefDir, 0o755); err != nil {
		t.Fatal(err)
	}
	existing := "# Hand-written brief\n\nDo not touch.\n"
	target := filepath.Join(briefDir, "issue-96.md")
	if err := os.WriteFile(target, []byte(existing), 0o644); err != nil {
		t.Fatal(err)
	}
	path, created, err := ensureBriefStub(home, "issue-96")
	if err != nil {
		t.Fatalf("ensureBriefStub: %v", err)
	}
	if created {
		t.Error("created = true when brief already existed, want false")
	}
	if path != target {
		t.Errorf("path = %q, want %q", path, target)
	}
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != existing {
		t.Errorf("existing brief modified:\n%s", data)
	}
}

func TestEnsureBriefStubSilentWithoutInputs(t *testing.T) {
	path, created, err := ensureBriefStub("", "issue-96")
	if err != nil || created || path != "" {
		t.Errorf("ensureBriefStub('', s) = (%q, %v, %v); want (\"\", false, nil)", path, created, err)
	}
	path, created, err = ensureBriefStub("/home", "")
	if err != nil || created || path != "" {
		t.Errorf("ensureBriefStub(home, '') = (%q, %v, %v); want (\"\", false, nil)", path, created, err)
	}
}

func TestBriefPathAbsolutizesRelativeTeamHome(t *testing.T) {
	home := t.TempDir()
	chdir(t, home)
	got := briefPath(".", "issue-96")
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
	want := filepath.Join(wantBase, ".amq-squad", briefsDirName, "issue-96.md")
	if gotResolved != want {
		t.Errorf("briefPath('.', 'issue-96') = %q (resolved %q), want %q", got, gotResolved, want)
	}
}

// TestEnsureBriefStubConcurrentExclusive proves the O_EXCL guard: when N
// goroutines race to create the same brief, exactly one observes created=true
// and the on-disk content is the canonical stub (not interleaved bytes).
func TestEnsureBriefStubConcurrentExclusive(t *testing.T) {
	home := t.TempDir()
	const N = 32
	var (
		wg          sync.WaitGroup
		mu          sync.Mutex
		createCount int
		errs        []error
	)
	start := make(chan struct{})
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, created, err := ensureBriefStub(home, "issue-96")
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				errs = append(errs, err)
				return
			}
			if created {
				createCount++
			}
		}()
	}
	close(start)
	wg.Wait()
	if len(errs) > 0 {
		t.Fatalf("ensureBriefStub returned errors under race: %v", errs)
	}
	if createCount != 1 {
		t.Errorf("createCount = %d, want exactly 1", createCount)
	}
	data, err := os.ReadFile(filepath.Join(home, ".amq-squad", briefsDirName, "issue-96.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != briefStubContent("issue-96") {
		t.Errorf("brief contents not canonical after race:\n%s", data)
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
