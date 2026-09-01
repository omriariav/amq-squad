package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestInitPreviewIsZeroWrite is gh#762's first named acceptance test: `init`
// without --apply computes and prints a preview plus an init_digest, but
// touches no files on disk.
func TestInitPreviewIsZeroWrite(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)

	stdout, stderr, err := captureOutput(t, func() error {
		return runInit([]string{"--roles", "cto", "--binary", "cto=codex"})
	})
	if err != nil {
		t.Fatalf("init preview: %v\n%s", err, stderr)
	}
	if !strings.Contains(stdout, "init_digest:") {
		t.Fatalf("preview did not print init_digest: %q", stdout)
	}
	if _, statErr := os.Stat(filepath.Join(dir, ".amq-squad")); !os.IsNotExist(statErr) {
		t.Fatalf(".amq-squad was created by a bare preview (must be zero-write): stat err = %v", statErr)
	}
	if _, statErr := os.Stat(filepath.Join(dir, "CLAUDE.md")); !os.IsNotExist(statErr) {
		t.Fatalf("CLAUDE.md was created by a bare preview (must be zero-write): stat err = %v", statErr)
	}
	if _, statErr := os.Stat(filepath.Join(dir, "AGENTS.md")); !os.IsNotExist(statErr) {
		t.Fatalf("AGENTS.md was created by a bare preview (must be zero-write): stat err = %v", statErr)
	}
}

// TestInitApplyRejectsStaleDigest is gh#762's second named acceptance test:
// --apply with a digest that does not match a fresh recompute of the planned
// writes refuses closed and writes nothing. Also proves computeInitDigest's
// two deterministic-hash properties directly (same content -> same digest;
// any changed byte -> a different digest), per task/t12 ruling 3.
func TestInitApplyRejectsStaleDigest(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)

	_, stderr, err := captureOutput(t, func() error {
		return runInit([]string{"--roles", "cto", "--binary", "cto=codex", "--apply", "0000000000000000000000000000000000000000000000000000000000000000000000"})
	})
	if err == nil {
		t.Fatal("stale/bogus digest was accepted, want refusal")
	}
	if !strings.Contains(err.Error(), "does not match a fresh recompute") {
		t.Fatalf("refusal error does not name the mismatch: %v\n%s", err, stderr)
	}
	if _, statErr := os.Stat(filepath.Join(dir, ".amq-squad")); !os.IsNotExist(statErr) {
		t.Fatalf(".amq-squad was written despite a refused --apply: stat err = %v", statErr)
	}

	// Deterministic-hash properties: same planned inputs -> same digest;
	// changing the roster (one changed byte anywhere in the planned writes)
	// -> a different digest.
	dirA := t.TempDir()
	chdir(t, dirA)
	planA, err := computeInitPlan([]string{"--roles", "cto", "--binary", "cto=codex"})
	if err != nil {
		t.Fatalf("compute plan A: %v", err)
	}
	planA2, err := computeInitPlan([]string{"--roles", "cto", "--binary", "cto=codex"})
	if err != nil {
		t.Fatalf("compute plan A again: %v", err)
	}
	if planA.Digest != planA2.Digest {
		t.Fatalf("same planned inputs produced different digests: %q vs %q (digest must be deterministic)", planA.Digest, planA2.Digest)
	}

	dirB := t.TempDir()
	chdir(t, dirB)
	planB, err := computeInitPlan([]string{"--roles", "cto,qa", "--binary", "cto=codex,qa=codex"})
	if err != nil {
		t.Fatalf("compute plan B: %v", err)
	}
	if planA.Digest == planB.Digest {
		t.Fatalf("different planned rosters produced the SAME digest %q (any byte change must change the digest)", planA.Digest)
	}
}

// TestInitRerunOnExistingProfileIsNoOp is gh#762's third named acceptance
// test: rerunning init against an existing, unchanged profile computes the
// same digest, and re-applying it performs no observable state change.
func TestInitRerunOnExistingProfileIsNoOp(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	args := []string{"--roles", "cto", "--binary", "cto=codex"}

	plan1, err := computeInitPlan(args)
	if err != nil {
		t.Fatalf("compute plan 1: %v", err)
	}
	if _, _, err := captureOutput(t, func() error {
		return runInit(append(append([]string{}, args...), "--apply", plan1.Digest))
	}); err != nil {
		t.Fatalf("first apply: %v", err)
	}

	profilePath := filepath.Join(dir, ".amq-squad", "team.json")
	rulesPath := filepath.Join(dir, ".amq-squad", "team-rules.md")
	claudePath := filepath.Join(dir, "CLAUDE.md")
	// team.json is excluded from the byte-identity check: team.WriteProfile
	// (pre-existing, shared with the deprecated `team init`) unconditionally
	// re-stamps created_at on every write, even a content-identical rerun --
	// that is not a regression init introduces, and init_digest correctly
	// excludes it (both plans below hash equal despite the timestamp
	// differing). team-rules.md and the pointer stubs carry no such stamp,
	// so those DO get the strict byte-identity check.
	before := map[string][]byte{}
	for _, p := range []string{rulesPath, claudePath} {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("read %s after first apply: %v", p, err)
		}
		before[p] = b
	}

	plan2, err := computeInitPlan(args)
	if err != nil {
		t.Fatalf("compute plan 2 (rerun): %v", err)
	}
	if plan2.Digest != plan1.Digest {
		t.Fatalf("rerunning init against an unchanged profile produced a different digest: %q vs %q", plan1.Digest, plan2.Digest)
	}
	if _, _, err := captureOutput(t, func() error {
		return runInit(append(append([]string{}, args...), "--apply", plan2.Digest))
	}); err != nil {
		t.Fatalf("second apply (rerun): %v", err)
	}
	if _, err := os.Stat(profilePath); err != nil {
		t.Fatalf("team.json missing after rerun: %v", err)
	}
	for _, p := range []string{rulesPath, claudePath} {
		after, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("read %s after second apply: %v", p, err)
		}
		if string(after) != string(before[p]) {
			t.Fatalf("%s content changed on a same-digest rerun (want a no-op)", p)
		}
	}
}

// TestDeprecatedCreateVerbsRedirectToInit is gh#762's fourth named acceptance
// test: `new team`, `new profile`, `team init`, `team rules init`, and `team
// sync --apply` each print a deprecation notice AND still perform their
// original, unchanged write (they are functional redirects, not the hard
// removal t9/gh#761 used for the goal-delivery subcommands).
func TestDeprecatedCreateVerbsRedirectToInit(t *testing.T) {
	cases := []struct {
		name   string
		args   []string
		notice string
		check  func(t *testing.T, dir string)
	}{
		{
			name:   "new team",
			args:   []string{"new", "team", "--roles", "cto", "--binary", "cto=codex"},
			notice: "amq-squad new team is deprecated",
			check: func(t *testing.T, dir string) {
				if _, err := os.Stat(filepath.Join(dir, ".amq-squad", "team.json")); err != nil {
					t.Fatalf("new team did not write team.json: %v", err)
				}
			},
		},
		{
			name:   "new profile",
			args:   []string{"new", "profile", "review", "--roles", "cto", "--binary", "cto=codex"},
			notice: "amq-squad new profile is deprecated",
			check: func(t *testing.T, dir string) {
				if _, err := os.Stat(filepath.Join(dir, ".amq-squad", "teams", "review.json")); err != nil {
					t.Fatalf("new profile did not write teams/review.json: %v", err)
				}
			},
		},
		{
			name:   "team init",
			args:   []string{"team", "init", "--roles", "cto", "--binary", "cto=codex"},
			notice: "amq-squad team init is deprecated",
			check: func(t *testing.T, dir string) {
				if _, err := os.Stat(filepath.Join(dir, ".amq-squad", "team.json")); err != nil {
					t.Fatalf("team init did not write team.json: %v", err)
				}
			},
		},
		{
			name:   "team rules init",
			args:   []string{"team", "rules", "init"},
			notice: "amq-squad team rules init is deprecated",
			check: func(t *testing.T, dir string) {
				if _, err := os.Stat(filepath.Join(dir, ".amq-squad", "team-rules.md")); err != nil {
					t.Fatalf("team rules init did not write team-rules.md: %v", err)
				}
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			chdir(t, dir)
			_, stderr, err := captureOutput(t, func() error { return Run(tc.args, "test") })
			if err != nil {
				t.Fatalf("%v: %v\n%s", tc.args, err, stderr)
			}
			if !strings.Contains(stderr, tc.notice) {
				t.Fatalf("%v: stderr missing deprecation notice %q: %q", tc.args, tc.notice, stderr)
			}
			tc.check(t, dir)
		})
	}

	// team sync --apply is tested separately: it needs an existing profile
	// (and pointer stubs) to have something to sync.
	t.Run("team sync --apply", func(t *testing.T) {
		dir := t.TempDir()
		chdir(t, dir)
		if _, _, err := captureOutput(t, func() error {
			return Run([]string{"team", "init", "--roles", "cto", "--binary", "cto=codex"}, "test")
		}); err != nil {
			t.Fatalf("seed team init: %v", err)
		}
		_ = os.Remove(filepath.Join(dir, "CLAUDE.md"))
		_, stderr, err := captureOutput(t, func() error {
			return Run([]string{"team", "sync", "--apply"}, "test")
		})
		if err != nil {
			t.Fatalf("team sync --apply: %v\n%s", err, stderr)
		}
		if !strings.Contains(stderr, "amq-squad team sync is deprecated") {
			t.Fatalf("stderr missing deprecation notice: %q", stderr)
		}
		if _, err := os.Stat(filepath.Join(dir, "CLAUDE.md")); err != nil {
			t.Fatalf("team sync --apply did not (re)write CLAUDE.md: %v", err)
		}
	})
}

// TestTeamSubcommandHelpDispatch is gh#762's fifth named acceptance test:
// every second- and third-level `team` subcommand must intercept -h/--help
// and print this repo's own help text -- not fail with a validation error
// (the `team member add/rm --help` bug) and not fall through to Go's default
// flag.Usage banner (the `team member list`/`team lead show --help` bug).
// Pinned once, table-driven, so the class cannot regress subcommand by
// subcommand (task/t12's ruling: fix all four confirmed instances, not just
// two, and cover the whole surface here). `team member control-continue`
// (team_member_control_continue.go) was found to carry the exact same bug
// during review and is included below -- fixed alongside the rest so the
// whole peelPositional-before-help-check class is actually eliminated, not
// just the two originally-suspected instances.
func TestTeamSubcommandHelpDispatch(t *testing.T) {
	cases := [][]string{
		{"team", "--help"},
		{"team", "init", "--help"},
		{"team", "resume", "--help"},
		{"team", "rules", "--help"},
		{"team", "rules", "init", "--help"},
		{"team", "rules", "show", "--help"},
		{"team", "rules", "templates", "--help"},
		{"team", "lead", "--help"},
		{"team", "lead", "set", "--help"},
		{"team", "lead", "clear", "--help"},
		{"team", "lead", "show", "--help"},
		{"team", "overlay", "--help"},
		{"team", "overlay", "init", "--help"},
		{"team", "member", "--help"},
		{"team", "member", "add", "--help"},
		{"team", "member", "update", "--help"},
		{"team", "member", "rm", "--help"},
		{"team", "member", "list", "--help"},
		{"team", "member", "control-continue", "--help"},
		{"team", "autonomous", "--help"},
		{"team", "operator", "--help"},
		{"team", "sync", "--help"},
		{"team", "profiles", "--help"},
		{"team", "rm", "--help"},
		{"team", "shared-cwd-exception", "--help"},
	}
	for _, args := range cases {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			dir := t.TempDir()
			chdir(t, dir)
			_, stderr, err := captureOutput(t, func() error { return Run(args, "test") })
			if err != nil {
				t.Fatalf("%v returned an error instead of showing help: %v", args, err)
			}
			if strings.TrimSpace(stderr) == "" {
				t.Fatalf("%v produced no help output on stderr", args)
			}
			if strings.Contains(stderr, "Usage of ") {
				t.Fatalf("%v fell through to Go's default flag.Usage banner instead of this repo's help: %q", args, stderr)
			}
			if strings.Contains(stderr, "is required") || strings.Contains(stderr, "unexpected argument") {
				t.Fatalf("%v printed a validation error instead of help: %q", args, stderr)
			}
		})
	}
}
