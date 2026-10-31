package releaseflow

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeGH puts a gh script first on PATH. It answers the three GET calls the
// flow makes by the endpoint (commits, compare, releases) and fails as the
// mode says: gh's own HTTP status token on stderr for 404 and 422, or a
// transport error naming the endpoint (and so the SHA or id) with no status.
func fakeGH(t *testing.T, mode, compare string) {
	t.Helper()
	bin := t.TempDir()
	script := `#!/bin/sh
ep="$2"
case "$FAKE_GH_MODE" in
404) echo "gh: Not Found (HTTP 404)" >&2; exit 1 ;;
422) echo "gh: No commit found for SHA: ${ep##*/} (HTTP 422)" >&2; exit 1 ;;
transport) echo "Get \"https://api.github.com/$ep\": dial tcp 140.82.121.6:443: i/o timeout" >&2; exit 1 ;;
upstream) echo "gh: upstream reported HTTP 404 (HTTP 503)" >&2; exit 1 ;;
transport404) echo "Get \"https://api.github.com/$ep\": proxy answered HTTP 404 Not Found" >&2; exit 1 ;;
esac
case "$ep" in
*/commits/*) echo "${ep##*/}" ;;
*/compare/*) echo "$FAKE_GH_COMPARE" ;;
*/git/ref/tags/*) echo "commit $FAKE_GH_TAG_SHA" ;;
*/releases/*) # as gh's --jq projection: the body only when the expression asks for it
  case "$4" in
  *body*) printf '%s\n' '{"id":1404,"tag":"v1.2.0","target_commitish":"abc","draft":true,"body":"the notes\n\n<!-- amq-squad-release-nonce: 00 -->"}' ;;
  *) printf '%s\n' '{"id":1404,"tag":"v1.2.0","target_commitish":"abc","draft":true}' ;;
  esac ;;
esac
`
	if err := os.WriteFile(filepath.Join(bin, "gh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("FAKE_GH_MODE", mode)
	t.Setenv("FAKE_GH_COMPARE", compare)
}

// 3.0.2 (Codex P2): the GH adapter's own methods, through a fake gh. A 404 or
// 422 is read from gh's HTTP status token; a transport failure stays an error
// even when the endpoint, and so the SHA or the release id, contains "404" or
// "422".
func TestGHAdapterClassifiesOnTheHTTPStatusOnly(t *testing.T) {
	ctx := context.Background()
	g := &GH{}
	for _, sha := range []string{strings.Repeat("404", 13) + "4", strings.Repeat("422", 13) + "4"} {
		for _, c := range []struct {
			mode, compare  string
			exists, within bool
			isErr          bool
		}{
			{"ok", "behind", true, true, false},
			{"ok", "identical", true, true, false},
			{"ok", "diverged", true, false, false},
			{"404", "", false, false, false},
			{"422", "", false, false, false},
			{"transport", "", false, false, true},
			{"upstream", "", false, false, true},     // "HTTP 404 (HTTP 503)": the status is 503
			{"transport404", "", false, false, true}, // "HTTP 404" in the text, no gh suffix
		} {
			fakeGH(t, c.mode, c.compare)
			ok, err := g.CommitExists(ctx, "owner/fixture", sha)
			if (err != nil) != c.isErr || (err == nil && ok != c.exists) {
				t.Fatalf("CommitExists %s %s: %v %v", sha, c.mode, ok, err)
			}
			if c.mode == "422" {
				continue // compare answers 404 for an unknown commit or branch
			}
			ok, err = g.BranchContains(ctx, "owner/fixture", "main", sha)
			if (err != nil) != c.isErr || (err == nil && ok != c.within) {
				t.Fatalf("BranchContains %s %s/%s: %v %v", sha, c.mode, c.compare, ok, err)
			}
		}
	}
	for _, c := range []struct {
		mode         string
		found, isErr bool
	}{
		{"ok", true, false},
		{"404", false, false},
		{"transport", false, true},
	} {
		fakeGH(t, c.mode, "")
		d, found, err := g.ReadDraft(ctx, "owner/fixture", 1404)
		if (err != nil) != c.isErr || found != c.found {
			t.Fatalf("ReadDraft %s: %+v %v %v", c.mode, d, found, err)
		}
		if c.found && (d.ID != 1404 || d.Body != "the notes\n\n<!-- amq-squad-release-nonce: 00 -->") {
			t.Fatalf("ReadDraft read %+v; the body must come back with the draft", d)
		}
	}
}

// 3.0.2 (Codex revert gap, round 3): TagCommit through the fake gh. A tag
// name containing "404" and a transport error stay apart: 404 is "no tag",
// a transport failure (with or without "HTTP 404" in its text) is an error.
func TestGHTagCommitClassifiesOnTheHTTPStatusOnly(t *testing.T) {
	ctx := context.Background()
	sha := strings.Repeat("ab", 20)
	for _, c := range []struct {
		mode, want string
		isErr      bool
	}{
		{"ok", sha, false},
		{"404", "", false},
		{"transport", "", true},
		{"transport404", "", true},
		{"upstream", "", true},
	} {
		fakeGH(t, c.mode, "")
		t.Setenv("FAKE_GH_TAG_SHA", sha)
		got, err := (&GH{}).TagCommit(ctx, "owner/fixture", "v1.404.0")
		if (err != nil) != c.isErr || got != c.want {
			t.Fatalf("TagCommit %s: %q %v", c.mode, got, err)
		}
	}
}
