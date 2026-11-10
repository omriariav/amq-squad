package uninstall

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The plan reviewer on the hold release file (3.2.0-0c-B): a seat's
// commits/<handle>.alarms.jsonl beside its ledger is never taken for a
// ledger or a seat. Uninstall's plan with alarms files present (one for
// the fixture's seat, one named for no seat) is the plan without them: no
// extra seat, worktree or ledger; the files go with the commits directory
// they sit in, removed with the ledger by --purge and kept by --write
// alone.
func TestAlarmsFilesBesideTheLedgerAddNoSeatOrLedger(t *testing.T) {
	f := newFixture(t)
	before, err := Run(f.opts)
	if err != nil {
		t.Fatal(err)
	}
	line := `{"seat":"seat-a","alarm":"3fa9c1d2","at":"2026-11-06T12:00:00Z","lead":"cto"}` + "\t" + strings.Repeat("ab", 32) + "\n"
	write(t, filepath.Join(f.x, "commits", "seat-a.alarms.jsonl"), line, 0o600)
	write(t, filepath.Join(f.x, "commits", "zz-ghost.alarms.jsonl"), strings.Replace(line, "seat-a", "zz-ghost", 1), 0o600)
	after, err := Run(f.opts)
	if err != nil {
		t.Fatal(err)
	}
	if b, a := render(before), render(after); a != b || strings.Contains(a, "zz-ghost") {
		t.Fatalf("the alarms files changed the plan:\nbefore:\n%s\nafter:\n%s", b, a)
	}
	if _, err := Run(f.writeOnly()); err != nil {
		t.Fatal(err)
	}
	if !exists(filepath.Join(f.x, "commits", "seat-a.alarms.jsonl")) {
		t.Fatal("--write alone removed the hold release file")
	}
	if _, err := Run(f.all()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(f.x, "commits")); err == nil {
		t.Fatal("the commits directory, with its hold release files, survived --purge")
	}
}
