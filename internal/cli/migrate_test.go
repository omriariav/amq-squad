package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The migrate verb: a dry run writes nothing, --write writes squad.yaml, a
// refusal is a user error (exit 1) naming its code, and --remove-v2 refuses
// without a squad.yaml.
func TestMigrateVerb(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	project := t.TempDir()
	profile := filepath.Join(project, ".amq-squad", "team.json")
	if err := os.MkdirAll(filepath.Dir(profile), 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{"schema": 6, "members": [{"role": "fullstack", "binary": "codex", "handle": "dev", "actor_mode": "implementation"}]}`
	if err := os.WriteFile(profile, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, ok := lookupCommand("migrate", ""); !ok {
		t.Fatal("migrate is not a registered command")
	}
	out, _, err := captureOutput(t, func() error { return runMigrate([]string{profile}) })
	if err != nil || !strings.Contains(out, "dry run") {
		t.Fatalf("dry run: %v\n%s", err, out)
	}
	if _, err := os.Stat(filepath.Join(project, "squad.yaml")); err == nil {
		t.Fatal("a dry run wrote squad.yaml")
	}
	if _, _, err := captureOutput(t, func() error { return runMigrate([]string{"--remove-v2", "--write", profile}) }); err == nil || ExitCode(err) != ExitUser || !strings.Contains(err.Error(), "migrate_remove_needs_squad") {
		t.Fatalf("--remove-v2 without squad.yaml: %v", err)
	}
	if _, err := os.Stat(profile); err != nil {
		t.Fatal("the profile was removed")
	}
	if _, _, err := captureOutput(t, func() error { return runMigrate([]string{"--write", profile}) }); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(filepath.Join(project, "squad.yaml")); err != nil || !strings.Contains(string(b), "handle: dev") {
		t.Fatalf("squad.yaml: %v %s", err, b)
	}
	if _, err := os.Stat(profile); err != nil {
		t.Fatal("migrate --write deleted the profile")
	}
	_, _, err = captureOutput(t, func() error { return runMigrate([]string{"--write", profile}) })
	if err == nil || ExitCode(err) != ExitUser || !strings.Contains(err.Error(), "squad_yaml_exists") {
		t.Fatalf("second migrate: %v", err)
	}
	if _, _, err := captureOutput(t, func() error { return runMigrate(nil) }); err == nil || ExitCode(err) != ExitUser {
		t.Fatalf("no path: %v", err)
	}
	if _, _, err := captureOutput(t, func() error { return runMigrate([]string{"--remove-v2", "--write", profile}) }); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(profile); err == nil {
		t.Fatal("--remove-v2 --write left the profile")
	}
}
