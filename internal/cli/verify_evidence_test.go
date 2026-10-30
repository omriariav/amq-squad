package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/v3/internal/mergetrain"
)

func TestVerifyRequiresKnownSubcommand(t *testing.T) {
	if _, _, err := captureOutput(t, func() error {
		return runVerify([]string{"bogus"})
	}); err == nil || !strings.Contains(err.Error(), "unknown 'verify' subcommand") {
		t.Fatalf("want unknown verify subcommand, got %v", err)
	}
}

// verify merge reads the merge train's audit log: merged at the head of the
// last batch digest is evidence; a refusal, a dry run or a head the digest
// does not name is not.
func TestVerifyMergeReadsTheAuditLog(t *testing.T) {
	root := t.TempDir()
	audit := mergetrain.AuditFor(root)
	add := func(e mergetrain.Entry) {
		e.Repository, e.Base = "o/r", "main"
		if err := audit.Append(e); err != nil {
			t.Fatal(err)
		}
	}
	add(mergetrain.Entry{Kind: "plan", DryRun: true, PR: 7})
	add(mergetrain.Entry{Kind: "batch", Pairs: []mergetrain.Pair{{Number: 7, Head: "h1"}, {Number: 8, Head: "h8"}}})
	add(mergetrain.Entry{Kind: "refusal", PR: 8, Head: "h8", Rules: []string{"ci_green"}, Detail: "ci_green: red"})
	add(mergetrain.Entry{Kind: "merge", PR: 7, Head: "h1", Merged: "m1"})
	run := func(pr string) (string, error) {
		out, _, err := captureOutput(t, func() error {
			return runVerify([]string{"merge", "--repository", "o/r", "--pr", pr, "--root", root, "--json"})
		})
		return out, err
	}
	if out, err := run("7"); err != nil || !strings.Contains(out, `"ok": true`) {
		t.Fatalf("merged PR: %v\n%s", err, out)
	}
	if out, err := run("8"); err == nil || !strings.Contains(out, "not_merged") {
		t.Fatalf("refused PR: %v\n%s", err, out)
	}
	if out, err := run("9"); err == nil || !strings.Contains(out, "no_merge") {
		t.Fatalf("unknown PR: %v\n%s", err, out)
	}
	add(mergetrain.Entry{Kind: "batch", Pairs: []mergetrain.Pair{{Number: 7, Head: "h2"}}})
	if out, err := run("7"); err == nil || !strings.Contains(out, "digest_mismatch") {
		t.Fatalf("merged head not the digest's: %v\n%s", err, out)
	}
}

// verify release reads the release flow's records and the gates' decisions.
func TestVerifyReleaseReadsTheFlowRecords(t *testing.T) {
	root := t.TempDir()
	ext := filepath.Join(root, "extensions", "amq-squad")
	write := func(rel, body string) {
		p := filepath.Join(ext, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("releases/release-v1-x/tag.json", `{"role":"tag","gate_id":"rel-tag-1","target":"git tag v1.2.0 abc on o/r","status":"ran"}`)
	run := func() (string, error) {
		out, _, err := captureOutput(t, func() error {
			return runVerify([]string{"release", "--tag", "v1.2.0", "--root", root, "--json"})
		})
		return out, err
	}
	if out, err := run(); err == nil || !strings.Contains(out, "not_released") {
		t.Fatalf("tag only: %v\n%s", err, out)
	}
	write("releases/release-v1-x/github_release.json", `{"role":"github_release","gate_id":"rel-gh-1","target":"gh release create v1.2.0 on o/r","status":"ran"}`)
	write("consumed/gates/rel-tag-1", "approved x\n")
	if out, err := run(); err == nil || !strings.Contains(out, "gate_not_accepted") {
		t.Fatalf("release gate not accepted: %v\n%s", err, out)
	}
	write("consumed/gates/rel-gh-1", "approved y\n")
	if out, err := run(); err != nil || !strings.Contains(out, `"ok": true`) {
		t.Fatalf("released: %v\n%s", err, out)
	}
}
