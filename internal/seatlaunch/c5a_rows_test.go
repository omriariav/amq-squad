package seatlaunch

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// C5a PR 1 (the sandbox argument, ruling B2, Codex pass 3): a Codex seat
// writes its worktree under the workspace-write sandbox with no other
// writable root, so every session's staged directory is outside its writes
// only while no seat's worktree overlaps the AMQ base root. Compile refuses
// a worktree that holds it (the project, an ancestor, the same through a
// symlink) or lies under it (this session's root or extensions/, the
// launch directory, a seat's staged directory, another session's root,
// through a symlink too), and names the fix; a worktree beside the base
// root stays allowed.
func TestC5aCompileRefusesASeatWorktreeOverlappingTheBaseRoot(t *testing.T) {
	q := testSquad(t)
	base := filepath.Dir(q.ProjectRoot)
	link := filepath.Join(base, "link-to-proj")
	if err := os.Symlink(q.ProjectRoot, link); err != nil {
		t.Fatal(err)
	}
	ext := filepath.Join(q.SessionRoot(), "extensions")
	if err := os.MkdirAll(filepath.Join(ext, "amq-squad", "launch"), 0o700); err != nil {
		t.Fatal(err)
	}
	extLink := filepath.Join(base, "link-to-ext")
	if err := os.Symlink(ext, extLink); err != nil {
		t.Fatal(err)
	}
	for _, wt := range []string{q.SessionRoot(), q.BaseRoot, q.ProjectRoot, base, "/", link, filepath.Join(q.ProjectRoot, "x", ".."),
		ext, filepath.Join(ext, "amq-squad"), filepath.Join(ext, "amq-squad", "launch"), filepath.Join(ext, "amq-squad", "launch", "dev"),
		filepath.Join(ext, "amq-squad", "launch", "dev", "exec", "wt"), filepath.Join(extLink, "amq-squad", "launch", "dev"),
		filepath.Join(link, ".agent-mail", "s1", "extensions", "x"),
		filepath.Join(q.BaseRoot, "s2"), filepath.Join(q.BaseRoot, "prof", "s2", "extensions"), filepath.Join(q.SessionRoot(), "agents", "dev"), filepath.Join(q.SessionRoot(), "extensions-other")} {
		r := q
		r.Seats = append([]Seat(nil), q.Seats...)
		r.Seats[1].Worktree = wt
		_, err := r.Normalize()
		if err == nil || !strings.Contains(err.Error(), "overlaps the AMQ base root "+q.BaseRoot) || !strings.Contains(err.Error(), "use a worktree outside "+q.BaseRoot) || !strings.Contains(err.Error(), ".worktrees/dev is fine") {
			t.Errorf("worktree %s: %v", wt, err)
		}
	}
	// both through aliases: the squad's project named through a symlink,
	// the worktree through the canonical path into its base root (Codex
	// pass 3, test strength)
	aliased := q
	aliased.ProjectRoot, aliased.BaseRoot = link, ""
	aliased.Seats = append([]Seat(nil), q.Seats...)
	aliased.Seats[1].Worktree = filepath.Join(q.ProjectRoot, ".agent-mail", "s1", "extensions", "amq-squad")
	if _, err := aliased.Normalize(); err == nil || !strings.Contains(err.Error(), "overlaps the AMQ base root") {
		t.Errorf("an aliased squad and a canonical worktree under its base root: %v", err)
	}
	for _, wt := range []string{filepath.Join(q.ProjectRoot, ".worktrees", "dev"), filepath.Join(base, "wt-dev"), filepath.Join(base, "proj-other"), filepath.Join(q.ProjectRoot, ".agent-mail-other", "wt")} {
		r := q
		r.Seats = append([]Seat(nil), q.Seats...)
		r.Seats[1].Worktree = wt
		if _, err := r.Normalize(); err != nil {
			t.Errorf("worktree %s: %v", wt, err)
		}
	}
}
