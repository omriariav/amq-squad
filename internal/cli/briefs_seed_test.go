package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// frozenClock returns a fixed timestamp so provenance assertions are
// deterministic.
func frozenClock() time.Time {
	return time.Date(2026, 5, 17, 12, 0, 0, 0, time.UTC)
}

// fakeGh returns a seedGhRun stub that responds with the supplied issue
// JSON for any `issue view` call. err overrides the response.
func fakeGh(t *testing.T, payload map[string]string, returnErr error) {
	t.Helper()
	prev := seedGhRun
	seedGhRun = func(args ...string) ([]byte, error) {
		if returnErr != nil {
			return nil, returnErr
		}
		// Sanity check: tests expect issue view ... --json title,body,url
		if len(args) < 4 || args[0] != "issue" || args[1] != "view" {
			t.Fatalf("unexpected gh args: %v", args)
		}
		b, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		return b, nil
	}
	t.Cleanup(func() { seedGhRun = prev })
}

func TestResolveSeedFileRendersExactBody(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "brief.md")
	body := "# Custom brief\n\nKeep me verbatim.\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := resolveSeed("file:" + path)
	if err != nil {
		t.Fatalf("resolveSeed: %v", err)
	}
	if got != body {
		t.Errorf("body modified by resolver:\ngot:\n%s\nwant:\n%s", got, body)
	}
}

func TestResolveSeedFileMissingFails(t *testing.T) {
	_, err := resolveSeed("file:/no/such/path.md")
	if err == nil || !strings.Contains(err.Error(), "file:/no/such/path.md") {
		t.Fatalf("missing file should name the source: %v", err)
	}
}

func TestResolveSeedRejectsClaudeAndCodex(t *testing.T) {
	for _, ref := range []string{"claude:abc123", "codex:session-1"} {
		_, err := resolveSeed(ref)
		if err == nil || !strings.Contains(err.Error(), "not implemented") {
			t.Errorf("%s: want not-implemented error, got %v", ref, err)
		}
	}
}

func TestResolveSeedIssueUsesGhCurrentRepo(t *testing.T) {
	fakeGh(t, map[string]string{
		"title": "Reshape #31",
		"body":  "Epic body lines.\nMore body.",
		"url":   "https://github.com/o/r/issues/31",
	}, nil)
	got, err := resolveSeed("issue:31")
	if err != nil {
		t.Fatalf("resolveSeed: %v", err)
	}
	for _, want := range []string{"# Reshape #31", "URL: https://github.com/o/r/issues/31", "Epic body lines."} {
		if !strings.Contains(got, want) {
			t.Errorf("issue body missing %q in:\n%s", want, got)
		}
	}
}

func TestResolveSeedGhExplicitRepo(t *testing.T) {
	captured := []string{}
	prev := seedGhRun
	seedGhRun = func(args ...string) ([]byte, error) {
		captured = append([]string(nil), args...)
		return []byte(`{"title":"T","body":"B","url":"U"}`), nil
	}
	t.Cleanup(func() { seedGhRun = prev })

	got, err := resolveSeed("gh:omriariav/amq-squad#31")
	if err != nil {
		t.Fatalf("resolveSeed: %v", err)
	}
	if !strings.Contains(got, "# T") || !strings.Contains(got, "URL: U") {
		t.Errorf("rendered issue missing title/url:\n%s", got)
	}
	wantArgs := []string{"issue", "view", "31", "--json", "title,body,url", "--repo", "omriariav/amq-squad"}
	if fmt.Sprint(captured) != fmt.Sprint(wantArgs) {
		t.Errorf("gh args = %v, want %v", captured, wantArgs)
	}
}

func TestResolveSeedGhBadShape(t *testing.T) {
	for _, ref := range []string{"gh:owner-repo-no-hash", "gh:owner/repo", "gh:no-slash#1"} {
		if _, err := resolveSeed(ref); err == nil {
			t.Errorf("%s: should fail validation", ref)
		}
	}
}

func TestResolveSeedIssueGhErrorNamesSource(t *testing.T) {
	fakeGh(t, nil, errors.New("gh: not authenticated"))
	_, err := resolveSeed("issue:42")
	if err == nil || !strings.Contains(err.Error(), "issue:42") || !strings.Contains(err.Error(), "gh:") {
		t.Fatalf("error should name source and gh: got %v", err)
	}
}

func TestBuildSeedBriefIncludesProvenance(t *testing.T) {
	out := buildSeedBrief("file:./brief.md", "BODY-X\n", frozenClock())
	for _, want := range []string{
		"---",
		"source: file:./brief.md",
		"generated_at: 2026-05-17T12:00:00Z",
		"generator: deterministic",
		"BODY-X",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("seed brief missing %q in:\n%s", want, out)
		}
	}
	if !strings.HasPrefix(out, "---\n") {
		t.Errorf("seed brief must start with frontmatter:\n%s", out)
	}
}

// TestBuildSeedBriefBodyByteForByte guards the file: passthrough contract:
// leading newlines and the absence of a trailing newline must survive
// the frontmatter prepend.
func TestBuildSeedBriefBodyByteForByte(t *testing.T) {
	body := "\n\nNo trailing newline"
	out := buildSeedBrief("file:./b.md", body, frozenClock())
	const frontmatterEnd = "---\n\n"
	idx := strings.Index(out, frontmatterEnd)
	if idx < 0 {
		t.Fatalf("frontmatter end not found in:\n%s", out)
	}
	got := out[idx+len(frontmatterEnd):]
	if got != body {
		t.Fatalf("body after frontmatter is not byte-for-byte:\ngot:%q\nwant:%q", got, body)
	}
}

func TestWriteSeedBriefRefusesExistingWithoutForce(t *testing.T) {
	home := t.TempDir()
	if _, err := writeSeedBrief(home, "issue-96", "first content\n", false); err != nil {
		t.Fatalf("first write: %v", err)
	}
	if _, err := writeSeedBrief(home, "issue-96", "second content\n", false); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("second write should refuse: %v", err)
	}
	// Existing content must be untouched.
	got, _ := os.ReadFile(filepath.Join(home, ".amq-squad", "briefs", "issue-96.md"))
	if string(got) != "first content\n" {
		t.Errorf("refused write still modified body: %s", got)
	}
}

// TestWriteSeedBriefNoForceConcurrentExclusive guards first-writer-wins
// when N goroutines race on the same target without --force. Exactly one
// must succeed, the rest must fail with the existing-brief error, and the
// final on-disk content must be one of the attempted contents verbatim
// (no interleaving from a partial write).
func TestWriteSeedBriefNoForceConcurrentExclusive(t *testing.T) {
	home := t.TempDir()
	const N = 32
	contents := make([]string, N)
	for i := range contents {
		contents[i] = fmt.Sprintf("body-%02d\n", i)
	}
	var (
		wg          sync.WaitGroup
		mu          sync.Mutex
		successes   int
		alreadyErrs int
		otherErrs   []error
	)
	start := make(chan struct{})
	for i := 0; i < N; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := writeSeedBrief(home, "issue-96", contents[i], false)
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				successes++
			case strings.Contains(err.Error(), "already exists"):
				alreadyErrs++
			default:
				otherErrs = append(otherErrs, err)
			}
		}()
	}
	close(start)
	wg.Wait()

	if len(otherErrs) > 0 {
		t.Fatalf("unexpected errors under race: %v", otherErrs)
	}
	if successes != 1 {
		t.Errorf("successes = %d, want exactly 1", successes)
	}
	if alreadyErrs != N-1 {
		t.Errorf("already-exists errors = %d, want %d", alreadyErrs, N-1)
	}
	got, err := os.ReadFile(filepath.Join(home, ".amq-squad", "briefs", "issue-96.md"))
	if err != nil {
		t.Fatal(err)
	}
	matched := false
	for _, c := range contents {
		if string(got) == c {
			matched = true
			break
		}
	}
	if !matched {
		t.Errorf("final brief is not one of the attempted contents (possible interleaving):\n%q", got)
	}
}

func TestWriteSeedBriefForceOverwrites(t *testing.T) {
	home := t.TempDir()
	if _, err := writeSeedBrief(home, "issue-96", "first content\n", false); err != nil {
		t.Fatal(err)
	}
	if _, err := writeSeedBrief(home, "issue-96", "second content\n", true); err != nil {
		t.Fatalf("force overwrite failed: %v", err)
	}
	got, _ := os.ReadFile(filepath.Join(home, ".amq-squad", "briefs", "issue-96.md"))
	if string(got) != "second content\n" {
		t.Errorf("force did not overwrite: %s", got)
	}
}
