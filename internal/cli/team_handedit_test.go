package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Final form A6: squad.yaml changes through the digest flow, and an
// out-of-band hand edit produces what docs/squad-yaml.md names. A proposal's
// digest covers the content the flow would write, so an apply whose options
// differ from the proposal's is `digest mismatch` and writes nothing; a hand
// edit does not change that digest, so the next proposal shows the file as
// `replace` (the flow is the writer and overwrites it); an edit that widens
// the owner grant fails to load with the field named.
func TestSquadYamlHandEditIsNamedByTheDigestFlow(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	project := t.TempDir()
	base := []string{"--project", project, "--roles", "cto,fullstack", "--lead", "cto", "--session", "feature-x"}
	first := teamProposeJSON(t, append([]string{"propose"}, base...)...)
	teamProposeJSON(t, append([]string{"apply", "--digest", first.Digest}, base...)...)
	path := filepath.Join(project, "squad.yaml")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	// the options changed after the proposal: digest mismatch, nothing written
	other := append(append([]string{}, base[:len(base)-1]...), "feature-y")
	if _, _, err := captureOutput(t, func() error { return runTeam(append([]string{"apply", "--digest", first.Digest}, other...)) }); err == nil || !strings.Contains(err.Error(), "digest mismatch") {
		t.Fatalf("apply with a stale digest: %v", err)
	}
	if now, _ := os.ReadFile(path); string(now) != string(raw) {
		t.Fatal("the refused apply changed squad.yaml")
	}

	// a hand edit: the next proposal shows squad.yaml as a replace
	if err := os.WriteFile(path, append(append([]byte{}, raw...), "# edited by hand\n"...), 0o644); err != nil {
		t.Fatal(err)
	}
	again := teamProposeJSON(t, append([]string{"propose"}, base...)...)
	if again.Digest != first.Digest || len(again.Files) == 0 || again.Files[0].Action != "replace" || !strings.HasSuffix(again.Files[0].Path, "squad.yaml") {
		t.Fatalf("a hand edit is not shown as a replace: %+v", again.Files)
	}

	// a hand edit that widens the owner grant: check refuses, naming the field
	widened := strings.Replace(string(raw), "schema: 1\n", "schema: 1\nmax_seats: 99\n", 1)
	if err := os.WriteFile(path, []byte(widened), 0o644); err != nil {
		t.Fatal(err)
	}
	_, _, err = captureOutput(t, func() error { return runTeam([]string{"check", "--project", project}) })
	if err == nil || !strings.Contains(err.Error(), "max_seats") || !strings.Contains(err.Error(), "widens") {
		t.Fatalf("check of a widening hand edit: %v", err)
	}
}
