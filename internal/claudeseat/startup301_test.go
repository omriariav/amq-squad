package claudeseat

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// 3.0.1 t9 and t6: StartupScreensClosed is true for the dir Write produces
// for a worktree (the seeded onboarding, the NFC trust, the hooks plugin),
// and false with the matching reason when any one is missing, the root has
// an entry, or projects carries anything else. Each reason is the one
// seat-exec's Check gives on the same dir (one source of truth: screensGap).
func TestStartupScreensClosed(t *testing.T) {
	seat := func(t *testing.T) (string, string) {
		base, _ := filepath.EvalSymlinks(t.TempDir())
		repo, wt := gitWorktree(t, base)
		dir := filepath.Join(base, "config")
		if _, err := Write(dir, "dontAsk", wt, time.Unix(0, 0)); err != nil {
			t.Fatal(err)
		}
		return dir, repo
	}
	t.Run("the seeded dir", func(t *testing.T) {
		dir, _ := seat(t)
		if ok, why := StartupScreensClosed(dir); !ok || why != "" {
			t.Fatalf("%t %q", ok, why)
		}
	})
	t.Run("no dir", func(t *testing.T) {
		if ok, why := StartupScreensClosed(filepath.Join(t.TempDir(), "absent")); ok || why != OnboardingUnverified {
			t.Fatalf("%t %q", ok, why)
		}
	})
	t.Run("no worktree", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "config")
		if _, err := Write(dir, "dontAsk", "", time.Unix(0, 0)); err != nil {
			t.Fatal(err)
		}
		if ok, why := StartupScreensClosed(dir); ok || why != TrustMissing {
			t.Fatalf("%t %q", ok, why)
		}
	})
	for name, tc := range map[string]struct {
		code   string
		mutate func(t *testing.T, dir, repo string)
	}{
		"onboarding not complete": {OnboardingMissing, func(t *testing.T, dir, _ string) {
			editJSON(t, filepath.Join(dir, ".claude.json"), func(m map[string]any) { delete(m, "hasCompletedOnboarding") })
		}},
		"no theme": {OnboardingMissing, func(t *testing.T, dir, _ string) {
			editJSON(t, filepath.Join(dir, "settings.json"), func(m map[string]any) { delete(m, "theme") })
		}},
		"worktree not trusted": {TrustMissing, func(t *testing.T, dir, _ string) {
			editJSON(t, filepath.Join(dir, ".claude.json"), func(m map[string]any) { m["projects"] = map[string]any{} })
		}},
		"a canonical-root entry": {TrustRootRefused, func(t *testing.T, dir, repo string) {
			editJSON(t, filepath.Join(dir, ".claude.json"), func(m map[string]any) {
				m["projects"].(map[string]any)[repo] = map[string]any{"hasTrustDialogAccepted": true}
			})
		}},
		"another entry": {ConfigUnexpected, func(t *testing.T, dir, _ string) {
			editJSON(t, filepath.Join(dir, ".claude.json"), func(m map[string]any) {
				m["projects"].(map[string]any)["/elsewhere"] = map[string]any{"hasTrustDialogAccepted": true}
			})
		}},
		"a permission input": {ConfigUnexpected, func(t *testing.T, dir, _ string) {
			editJSON(t, filepath.Join(dir, ".claude.json"), func(m map[string]any) { m["allowedTools"] = []any{"Bash(*)"} })
		}},
		"plugin not enabled": {HooksMissing, func(t *testing.T, dir, _ string) {
			editJSON(t, filepath.Join(dir, "settings.json"), func(m map[string]any) { delete(m, "enabledPlugins") })
		}},
		"hooks not wired": {HooksMissing, func(t *testing.T, dir, _ string) {
			matches, _ := filepath.Glob(filepath.Join(dir, "plugins", "cache", "amq-squad", "amq-squad", "*", "hooks", "hooks.json"))
			for _, m := range matches {
				os.WriteFile(m, []byte(`{"hooks":{}}`), 0o600)
			}
		}},
	} {
		t.Run(name, func(t *testing.T) {
			dir, repo := seat(t)
			tc.mutate(t, dir, repo)
			if ok, why := StartupScreensClosed(dir); ok || why != tc.code {
				t.Fatalf("StartupScreensClosed: %t %q, want %s", ok, why, tc.code)
			}
			digest, _ := Digest(dir)
			if code, _ := Check(dir, os.Getuid(), digest, "dontAsk"); code != tc.code {
				t.Fatalf("Check gives %q, StartupScreensClosed %q: not one source of truth", code, tc.code)
			}
		})
	}
}

// StartupScreensClosed depends only on what Write seeds: two dirs Write
// produces for the same mode and worktree (the launch's scratch read and the
// seat's own dir) give the same answer, and Write's time does not enter it.
func TestStartupScreensClosedDependsOnlyOnTheSeed(t *testing.T) {
	base, _ := filepath.EvalSymlinks(t.TempDir())
	_, wt := gitWorktree(t, base)
	scratch, seatDir := filepath.Join(base, "scratch", "config"), Dir(base, "c1", "launch-1")
	if _, err := Write(scratch, "dontAsk", wt, time.Unix(0, 0)); err != nil {
		t.Fatal(err)
	}
	if _, err := Write(seatDir, "dontAsk", wt, time.Now()); err != nil {
		t.Fatal(err)
	}
	a, whyA := StartupScreensClosed(scratch)
	b, whyB := StartupScreensClosed(seatDir)
	if !a || a != b || whyA != whyB {
		t.Fatalf("scratch %t %q, seat %t %q", a, whyA, b, whyB)
	}
}
