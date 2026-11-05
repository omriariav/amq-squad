package uninstall

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// B8 (3.2.0-0c-B rule 6): the commits ledger's quarantine is listed in the
// dry run in the same group as the commits ledger (the sealed ledgers),
// beside it; it is kept by --write alone and removed only with --purge,
// with the ledger; nothing removes it on its own. Reverted (the directory
// left to the default group), --write alone removes the evidence.
func TestQuarantineIsListedAndRemovedWithTheCommitsLedger(t *testing.T) {
	f := newFixture(t)
	slot := filepath.Join(f.x, "quarantine", "commits.seat-a.seq1")
	for name, body := range map[string]string{"body.json": "{}\n", "head.json": "{}\n", "quarantine.txt": "amq-squad commits ledger quarantine\n"} {
		write(t, filepath.Join(slot, name), body, 0o600)
	}
	res, err := Run(f.opts)
	if err != nil {
		t.Fatal(err)
	}
	out := render(res)
	group := func(title string) string {
		i := strings.Index(out, title)
		if i < 0 {
			t.Fatalf("the dry run has no %q group:\n%s", title, out)
		}
		rest := out[i+len(title):]
		if j := strings.Index(rest, "\n\n"); j >= 0 {
			rest = rest[:j]
		}
		return rest
	}
	sealed := group("sealed ledgers")
	if !strings.Contains(sealed, filepath.Join(f.x, "commits")) || !strings.Contains(sealed, filepath.Join(f.x, "quarantine")) {
		t.Fatalf("the sealed group does not list the commits ledger and its quarantine:\n%s", sealed)
	}
	if !strings.Contains(sealed, "the commits ledger's quarantine") {
		t.Fatalf("the quarantine's note is missing:\n%s", sealed)
	}
	if session := group("session state under the AMQ root"); strings.Contains(session, "quarantine") {
		t.Fatalf("the quarantine is listed under the session state:\n%s", session)
	}
	if len(res.Removed) > 0 {
		t.Fatalf("a dry run removed %v", res.Removed)
	}
	if _, err := Run(f.writeOnly()); err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{"commits/seat-a.json", "quarantine/commits.seat-a.seq1/body.json", "quarantine/commits.seat-a.seq1/quarantine.txt"} {
		if !exists(filepath.Join(f.x, rel)) {
			t.Errorf("%s was removed without --purge", rel)
		}
	}
	if _, err := Run(f.all()); err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{"commits", "quarantine"} {
		if _, err := os.Lstat(filepath.Join(f.x, rel)); err == nil {
			t.Errorf("%s survived --purge", rel)
		}
	}
}
