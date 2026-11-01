package seatcommit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/v3/internal/grants"
)

func TestResolveExactFilesFollowsLinksAndRefusesUnsafeTargets(t *testing.T) {
	repo, root := t.TempDir(), t.TempDir()
	write(t, filepath.Join(repo, "real", "target.txt"), "ok\n")
	write(t, filepath.Join(repo, "CLAUDE.md"), "managed\n")
	nfd := "cafe\u0301.txt"
	write(t, filepath.Join(repo, nfd), "unicode\n")
	if err := os.Symlink("chain-b", filepath.Join(repo, "chain-a")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	os.Symlink(filepath.Join("real", "target.txt"), filepath.Join(repo, "chain-b"))
	os.Symlink("real", filepath.Join(repo, "parent-link"))
	os.Symlink("CLAUDE.md", filepath.Join(repo, "managed-link"))
	outside := filepath.Join(t.TempDir(), "outside.txt")
	write(t, outside, "outside\n")
	os.Symlink(outside, filepath.Join(repo, "outside-link"))
	os.Symlink("missing.txt", filepath.Join(repo, "dangling-link"))
	os.Symlink(filepath.Join(filepath.Dir(outside), "missing.txt"), filepath.Join(repo, "dangling-outside"))
	os.Symlink("dangling-outside", filepath.Join(repo, "broken-chain"))
	os.Symlink("cycle-b", filepath.Join(repo, "cycle-a"))
	os.Symlink("cycle-a", filepath.Join(repo, "cycle-b"))
	os.Mkdir(filepath.Join(repo, "directory"), 0o755)

	g := grants.Defaults()
	g.ProtectedPaths = append(g.ProtectedPaths, "caf\u00e9.txt")
	for _, tc := range []struct {
		alias, want string
		protected   bool
	}{
		{"chain-a", "real/target.txt", false},
		{"parent-link/target.txt", "real/target.txt", false},
		{"managed-link", "CLAUDE.md", true},
		{nfd, nfd, true},
	} {
		got, err := ResolveExactFiles(g, root, repo, t.TempDir(), []string{tc.alias})
		if err != nil || len(got) != 1 || got[0].Path != tc.want || got[0].Protected != tc.protected {
			t.Errorf("resolve %q = %+v, %v; want path %q protected %v", tc.alias, got, err, tc.want, tc.protected)
		}
	}
	for _, tc := range []struct {
		alias, want string
	}{
		{"outside-link", outside},
		{"dangling-link", "missing.txt"},
		{"dangling-outside", "missing.txt"},
		{"broken-chain", filepath.Dir(outside)},
		{"cycle-a", "cycle-a"},
		{"directory", "existing directory"},
	} {
		if _, err := ResolveExactFiles(g, root, repo, t.TempDir(), []string{tc.alias}); err == nil || !strings.Contains(err.Error(), tc.alias) || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("unsafe %q: %v, want alias and %q", tc.alias, err, tc.want)
		}
	}
}
