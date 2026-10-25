package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/v3/internal/team"
)

// Final form A6: squad.yaml changes through the digest flow. The one token
// apply takes covers the proposed content and the squad.yaml on disk when it
// was proposed: options changed after the proposal is
// squad_yaml_digest_mismatch, an edit of the file after the proposal is
// squad_yaml_changed (both disk digests in the message), neither writes
// anything, and the next proposal shows the edit as a replace. An edit that
// widens the owner grant fails to load with the field named.
func TestSquadYamlHandEditIsRefusedByApplyAndShownByThePropose(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	project := t.TempDir()
	base := []string{"--project", project, "--roles", "cto,fullstack", "--lead", "cto", "--session", "feature-x"}
	first := teamProposeJSON(t, append([]string{"propose"}, base...)...)
	if !strings.HasSuffix(first.Digest, "@absent") || first.Disk != "absent" {
		t.Fatalf("a proposal over no squad.yaml: %+v", first)
	}
	teamProposeJSON(t, append([]string{"apply", "--digest", first.Digest}, base...)...)
	path := filepath.Join(project, "squad.yaml")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	// the options changed after the proposal: squad_yaml_digest_mismatch
	other := append(append([]string{}, base[:len(base)-1]...), "feature-y")
	apply := func(digest string, args []string) error {
		_, _, err := captureOutput(t, func() error { return runTeam(append([]string{"apply", "--digest", digest}, args...)) })
		return err
	}
	proposed := teamProposeJSON(t, append([]string{"propose"}, base...)...)
	if err := apply(first.Digest, other); err == nil || !strings.Contains(err.Error(), team.CodeDigestMismatch) {
		t.Fatalf("apply with a stale digest: %v", err)
	}

	// the file edited after the proposal: squad_yaml_changed, nothing written
	edited := append(append([]byte{}, raw...), "# edited by hand\n"...)
	if err := os.WriteFile(path, edited, 0o644); err != nil {
		t.Fatal(err)
	}
	err = apply(proposed.Digest, base)
	if err == nil || !strings.Contains(err.Error(), team.CodeSquadChanged) || !strings.Contains(err.Error(), proposed.Disk) {
		t.Fatalf("apply over a hand edit: %v", err)
	}
	if now, _ := os.ReadFile(path); string(now) != string(edited) {
		t.Fatal("the refused apply changed the hand-edited file")
	}

	// the next proposal shows the edit as a replace, with a new disk digest
	again := teamProposeJSON(t, append([]string{"propose"}, base...)...)
	if again.Disk == proposed.Disk || again.Files[0].Action != "replace" || !strings.HasSuffix(again.Files[0].Path, "squad.yaml") {
		t.Fatalf("the edit is not shown: %+v", again)
	}
	teamProposeJSON(t, append([]string{"apply", "--digest", again.Digest}, base...)...)
	if now, _ := os.ReadFile(path); string(now) != string(raw) {
		t.Fatal("the approved apply did not write the proposal")
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
