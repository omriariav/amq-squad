package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/v3/internal/migrate"
)

// M1: one output funnel at the verb boundary. Whatever the verb would print
// (stdout, stderr, the error the CLI renders) passes redact.Block once; if it
// would change, the verb prints a fixed line naming a closed location class
// and exits 2, with nothing else on either stream. S is a credential-shaped
// string built at run time.
func TestMigrateVerbFunnelsEveryByteThroughOneCheck(t *testing.T) {
	secret := "gh" + "p_" + strings.Repeat("a1B2c3", 6)
	const minimal = `{"schema": 6, "members": [{"role": "fullstack", "binary": "codex", "handle": "dev", "actor_mode": "implementation"}]}`
	project := func(t *testing.T, name string) (dir, profile string) {
		t.Helper()
		t.Setenv("HOME", t.TempDir())
		root, err := filepath.EvalSymlinks(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		dir = filepath.Join(root, name)
		profile = filepath.Join(dir, ".amq-squad", "team.json")
		if err := os.MkdirAll(filepath.Dir(profile), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(profile, []byte(minimal), 0o644); err != nil {
			t.Fatal(err)
		}
		return dir, profile
	}
	run := func(t *testing.T, args ...string) (string, error) {
		t.Helper()
		stdout, stderr, err := captureOutput(t, func() error { return runMigrate(args) })
		streams := stdout + stderr
		if err != nil {
			streams += err.Error()
		}
		if strings.Contains(streams, secret) {
			t.Fatalf("the credential-shaped string reached a stream:\n%s", streams)
		}
		return stdout + stderr, err
	}
	fixed := func(t *testing.T, stdout string, err error, class string) {
		t.Helper()
		if ExitCode(err) != ExitSystem || err.Error() != "migrate_refused_credential: "+class || strings.TrimSpace(stdout) != "" {
			t.Fatalf("want exit 2 with %q and nothing else, got %v (exit %d), stdout %q", class, err, ExitCode(err), stdout)
		}
	}
	t.Run("a key nested under a key", func(t *testing.T) {
		_, profile := project(t, "p")
		os.WriteFile(profile, []byte(`{"`+secret+`": {"`+secret+`": 1}, "members": [{"role": "fullstack", "binary": "codex", "handle": "dev"}]}`), 0o644)
		out, err := run(t, profile)
		fixed(t, out, err, "a key under the profile")
	})
	t.Run("a key and its value", func(t *testing.T) {
		_, profile := project(t, "p")
		os.WriteFile(profile, []byte(`{"`+secret+`": "`+secret+`", "members": [{"role": "fullstack", "binary": "codex", "handle": "dev"}]}`), 0o644)
		out, err := run(t, profile)
		fixed(t, out, err, "a key under the profile")
	})
	t.Run("a briefs entry named by the credential that is not a file", func(t *testing.T) {
		dir, profile := project(t, "p")
		os.MkdirAll(filepath.Join(dir, ".amq-squad", "briefs", secret+".md"), 0o755)
		out, err := run(t, profile)
		fixed(t, out, err, "an input file name")
	})
	t.Run("an absent profile under a credential-shaped directory, relative argument", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		base, _ := filepath.EvalSymlinks(t.TempDir())
		cwd := filepath.Join(base, secret)
		os.MkdirAll(cwd, 0o755)
		t.Chdir(cwd)
		out, err := run(t, filepath.Join(".amq-squad", "team.json"))
		if err == nil || strings.TrimSpace(out) != "" || strings.Contains(err.Error(), secret) {
			t.Fatalf("got %v, stdout %q", err, out)
		}
	})
	t.Run("a role under a credential-shaped session directory, plan and removal", func(t *testing.T) {
		dir, profile := project(t, "p")
		role := filepath.Join(dir, ".agent-mail", secret, "agents", "dev", "extensions", "io.github.omriariav.amq-squad")
		os.MkdirAll(role, 0o755)
		os.WriteFile(filepath.Join(role, "role.md"), []byte("# dev\n"), 0o644)
		out, err := run(t, profile)
		if err == nil || ExitCode(err) != ExitUser || !strings.Contains(err.Error(), "migrate_refused_credential") || strings.TrimSpace(out) != "" {
			t.Fatalf("plan over a credential-shaped input file name: %v, stdout %q", err, out)
		}
		// removal needs a squad.yaml that loads; write one by hand
		os.WriteFile(filepath.Join(dir, "squad.yaml"), []byte("schema: 1\nseats:\n  - handle: dev\n    runtime: codex\n"), 0o644)
		for _, args := range [][]string{{"--remove-v2", profile}, {"--remove-v2", "--write", profile}} {
			out, err = run(t, args...)
			if err == nil || ExitCode(err) != ExitUser || !strings.Contains(err.Error(), "migrate_refused_credential") || !strings.Contains(err.Error(), "a removal path") || strings.TrimSpace(out) != "" {
				t.Fatalf("removal over a credential-shaped path: %v, stdout %q", err, out)
			}
		}
		if _, statErr := os.Stat(filepath.Join(role, "role.md")); statErr != nil {
			t.Fatalf("a refused removal removed the file: %v", statErr)
		}
	})
	t.Run("a flag named by the credential", func(t *testing.T) {
		_, profile := project(t, "p")
		out, err := run(t, "--"+secret, profile)
		if err == nil || strings.TrimSpace(out) != "" || strings.Contains(err.Error(), secret) {
			t.Fatalf("got %v, stdout %q", err, out)
		}
	})
}

// M2: a change during the apply stops at the first refusal with a partial
// report and exit 3; the plan's own refusals (exit 1) say nothing was touched.
// The verb plans and applies in one call, so the stop is driven through
// migrateStopped with what Apply returned (the retargeted root and the symlink
// introduced after the plan are TestMigrateRemovalStopsAtASymlinkIntroducedAfterThePlan
// and TestMigrateApplyRefusesAMovedRoot in internal/migrate).
func TestMigrateStoppedIsAPartialResultWithExitThree(t *testing.T) {
	refusal := &migrate.Refusal{Code: migrate.CodeSymlink, Fields: []string{"agents/lead"}, Detail: "a symlink on the way"}
	var out strings.Builder
	err := migrateStopped("--remove-v2", []string{"/p/a"}, 3, refusal, &out)
	if ExitCode(err) != ExitPartial || !strings.Contains(out.String(), "stopped at the first refusal after 1 of 3") || !strings.Contains(out.String(), "/p/a") || !strings.Contains(out.String(), "migrate_refused_symlink") {
		t.Fatalf("partial: %v (exit %d)\n%s", err, ExitCode(err), out.String())
	}
	out.Reset()
	err = migrateStopped("--remove-v2", nil, 3, refusal, &out)
	if ExitCode(err) != ExitUser || out.Len() != 0 {
		t.Fatalf("nothing done: %v (exit %d), out %q", err, ExitCode(err), out.String())
	}
}
