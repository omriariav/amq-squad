package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/launch"
)

// sbxGit runs git in dir with no global or system config.
func sbxGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1", "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// 3.3.1 item 5, proposed row 6: the Write and Edit tools are not sandboxed,
// and Claude Code reloads settings files mid-session, so the guard refuses a
// seat's write to the files Claude Code loads configuration from for the
// seat's session: in a linked worktree that includes the MAIN checkout's
// .claude/settings.local.json (Claude Code reads the local file at the main
// checkout's root), and the project .mcp.json.
func TestSbxTheGuardRefusesTheSessionsConfigFilesInAWorktree(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not on PATH")
	}
	f := newHookFixture(t)
	main := filepath.Join(filepath.Dir(f.repo), "main")
	if err := os.MkdirAll(main, 0o755); err != nil {
		t.Fatal(err)
	}
	sbxGit(t, main, "init", "-q", "-b", "main")
	sbxGit(t, main, "commit", "-q", "--allow-empty", "-m", "base")
	wt := filepath.Join(main, ".worktrees", "cto")
	sbxGit(t, main, "worktree", "add", "-q", "-b", "seat/cto", wt)
	for _, p := range []string{
		filepath.Join(main, ".claude", "settings.local.json"),
		filepath.Join(main, ".claude", "settings.json"),
		filepath.Join(main, ".mcp.json"),
		filepath.Join(wt, ".mcp.json"),
		filepath.Join(wt, ".claude", "settings.local.json"),
	} {
		for _, call := range []guardCall{
			{Tool: "Write", Args: map[string]string{"file_path": p}, CWD: wt},
			{Tool: "Edit", Args: map[string]string{"file_path": p, "old_string": "a", "new_string": "b"}, CWD: wt},
			{Tool: "Bash", Args: map[string]string{"command": "cp /tmp/x " + p}, CWD: wt},
			{Tool: "Bash", Args: map[string]string{"command": "echo '{}' > " + p}, CWD: wt},
		} {
			if v := evaluateGuard(f.id(), call, f.now); v.ReasonCode != "protected_path" {
				t.Errorf("%s %s from the worktree: %s %s", call.Tool, p, v.Verdict, v.ReasonCode)
			}
		}
	}
	// in a plain checkout (the seat's tree is the main one) its .mcp.json too
	if v := evaluateGuard(f.id(), guardCall{Tool: "Write", Args: map[string]string{"file_path": filepath.Join(main, ".mcp.json")}, CWD: main}, f.now); v.ReasonCode != "protected_path" {
		t.Errorf("Write .mcp.json in the main checkout: %s %s", v.Verdict, v.ReasonCode)
	}
	// ordinary work in the seat's own tree stays allowed (Codex pass 4): a
	// copy into it, a new directory, a file beside the config files
	for _, cmd := range []string{"cp /tmp/x .", "cp -r /tmp/src " + wt, "mkdir -p " + filepath.Join(wt, "build"), "touch " + filepath.Join(wt, "notes.md")} {
		if v := evaluateGuard(f.id(), guardCall{Tool: "Bash", Args: map[string]string{"command": cmd}, CWD: wt}, f.now); v.Verdict != "allow" {
			t.Errorf("Bash %q in the seat's tree: %s %s", cmd, v.Verdict, v.ReasonCode)
		}
	}
	// the set is the config files only: the main checkout's other files and
	// the worktree's own sources stay writable
	for _, p := range []string{filepath.Join(main, "README.md"), filepath.Join(wt, "main.go"), filepath.Join(wt, ".claude", "notes.md")} {
		if v := evaluateGuard(f.id(), guardCall{Tool: "Write", Args: map[string]string{"file_path": p}, CWD: wt}, f.now); v.Verdict != "allow" {
			t.Errorf("Write %s: %s %s", p, v.Verdict, v.ReasonCode)
		}
	}
}

// Codex pass 1 on 3.3.1 item 5 (D3): the config files are the seat's own,
// whatever the hook's cwd; a symlinked file is protected through its link
// and at its target; a case alias on a case-insensitive filesystem is the
// same file; glob characters in a checkout path are literal; and a linked
// worktree of a bare repository has its root at the bare directory.
func TestSbxTheGuardProtectsTheSeatsConfigFilesInEveryForm(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not on PATH")
	}
	f := newHookFixture(t)
	base := filepath.Dir(f.repo)
	main := filepath.Join(base, "proj[1]")
	if err := os.MkdirAll(main, 0o755); err != nil {
		t.Fatal(err)
	}
	sbxGit(t, main, "init", "-q", "-b", "main")
	sbxGit(t, main, "commit", "-q", "--allow-empty", "-m", "base")
	wt := filepath.Join(main, ".worktrees", "cto")
	sbxGit(t, main, "worktree", "add", "-q", "-b", "seat/cto", wt)
	stageSeatRecord(t, f.root, launch.Record{Handle: "cto", Root: f.root, Session: "s1", Worktree: wt})
	elsewhere := t.TempDir()
	// the main checkout's local settings file is a symlink to a file outside
	outside := filepath.Join(base, "outside", "seat-settings.json")
	if err := os.MkdirAll(filepath.Dir(outside), 0o755); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, outside, "{}")
	if err := os.MkdirAll(filepath.Join(main, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(main, ".claude", "settings.local.json")); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(wt, ".mcp.json"), "{}")
	// the worktree's settings file is a link to a file that does not exist
	// yet (Codex pass 3): a write there would create the session's settings
	future := filepath.Join(base, "future", "settings.json")
	if err := os.MkdirAll(filepath.Join(wt, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(future, filepath.Join(wt, ".claude", "settings.json")); err != nil {
		t.Fatal(err)
	}
	cases := []struct{ path, cwd string }{
		{filepath.Join(wt, ".mcp.json"), elsewhere},                  // the hook's cwd is outside the seat's tree
		{filepath.Join(main, ".claude", "settings.json"), elsewhere}, // and the main checkout is still the seat's
		{filepath.Join(main, ".claude", "settings.local.json"), wt},  // the symlink's name
		{outside, wt},                        // the symlink's target
		{filepath.Join(wt, ".MCP.JSON"), wt}, // a case alias
		{filepath.Join(main, ".claude", "SETTINGS.LOCAL.JSON"), wt}, // a case alias through the symlink's directory
		{filepath.Join(main, ".mcp.json"), wt},                      // glob characters in the checkout path
		{future, wt},                                                // a dangling link's target
		{filepath.Join(main, ".worktrees", ".mcp.json"), wt},        // an ancestor's .mcp.json (Codex pass 9)
		{filepath.Join(base, ".mcp.json"), wt},                      // a farther ancestor's (Codex pass 10)
		{"/.mcp.json", wt},                                          // the filesystem root's
	}
	for _, c := range cases {
		if v := evaluateGuard(f.id(), guardCall{Tool: "Write", Args: map[string]string{"file_path": c.path}, CWD: c.cwd}, f.now); v.ReasonCode != "protected_path" {
			t.Errorf("Write %s (cwd %s): %s %s", c.path, c.cwd, v.Verdict, v.ReasonCode)
		}
	}
	// replacing the settings by moving directories (Codex pass 2): the
	// .claude directory moved away, a prepared one moved in, removed, or
	// the main checkout itself moved
	for _, cmd := range []string{
		"mv " + filepath.Join(main, ".claude") + " " + filepath.Join(base, "old-claude"),
		"mv " + filepath.Join(base, "replacement") + " " + filepath.Join(main, ".claude"),
		"rm -rf " + filepath.Join(main, ".claude"),
		"mv " + main + " " + filepath.Join(base, "moved"),
		"rm -rf " + filepath.Join(wt, ".claude"),
	} {
		if v := evaluateGuard(f.id(), guardCall{Tool: "Bash", Args: map[string]string{"command": cmd}, CWD: wt}, f.now); v.ReasonCode != "protected_path" {
			t.Errorf("Bash %q: %s %s", cmd, v.Verdict, v.ReasonCode)
		}
	}
	// a linked worktree of a bare repository: Claude Code's repository root
	// is the bare directory
	bare := filepath.Join(base, "repo.git")
	sbxGit(t, base, "init", "-q", "--bare", "-b", "main", bare)
	bwt := filepath.Join(base, "bare-wt")
	sbxGit(t, bare, "worktree", "add", "-q", "--orphan", "-b", "seat/b", bwt)
	// its .claude directory is a link to a real directory elsewhere: a write
	// through the real path is the same settings file
	real := filepath.Join(base, "real-claude")
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, filepath.Join(bare, ".claude")); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{filepath.Join(bare, ".claude", "settings.local.json"), filepath.Join(bare, ".mcp.json"), filepath.Join(real, "settings.local.json")} {
		if v := evaluateGuard(f.id(), guardCall{Tool: "Write", Args: map[string]string{"file_path": p}, CWD: bwt}, f.now); v.ReasonCode != "protected_path" {
			t.Errorf("Write %s from a bare repository's worktree: %s %s", p, v.Verdict, v.ReasonCode)
		}
	}
}

// Codex pass 5 on 3.3.1 item 5 (D3): the main checkout is the one seat-exec
// pinned on the current launch's exec record; a seat that rewrites its
// worktree's .git pointer to a decoy does not move it out of the
// protected set.
func TestSbxTheGuardKeepsThePinnedMainCheckout(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not on PATH")
	}
	f := newHookFixture(t)
	base := filepath.Dir(f.repo)
	main := filepath.Join(base, "main")
	if err := os.MkdirAll(main, 0o755); err != nil {
		t.Fatal(err)
	}
	sbxGit(t, main, "init", "-q", "-b", "main")
	sbxGit(t, main, "commit", "-q", "--allow-empty", "-m", "base")
	wt := filepath.Join(main, ".worktrees", "cto")
	sbxGit(t, main, "worktree", "add", "-q", "-b", "seat/cto", wt)
	stageSeatRecord(t, f.root, launch.Record{Handle: "cto", Root: f.root, Session: "s1", Worktree: wt, LaunchID: "L1"})
	execDir := filepath.Join(launch.StagedDir(f.root, "cto"), "exec")
	if err := os.MkdirAll(execDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := launch.WriteFile(filepath.Join(execDir, launch.FileName), launch.Record{Handle: "cto", Root: f.root, Worktree: wt, LaunchID: "L1", MainCheckout: main}); err != nil {
		t.Fatal(err)
	}
	// the seat points its worktree's .git at a decoy
	decoy := filepath.Join(base, "decoy", ".git", "worktrees", "cto")
	if err := os.MkdirAll(decoy, 0o755); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(decoy, "commondir"), "../..\n")
	mustWrite(t, filepath.Join(wt, ".git"), "gitdir: "+decoy+"\n")
	for _, p := range []string{filepath.Join(main, ".claude", "settings.local.json"), filepath.Join(main, ".mcp.json")} {
		if v := evaluateGuard(f.id(), guardCall{Tool: "Write", Args: map[string]string{"file_path": p}, CWD: wt}, f.now); v.ReasonCode != "protected_path" {
			t.Errorf("Write %s after the .git pointer moved: %s %s", p, v.Verdict, v.ReasonCode)
		}
	}
}

// Codex pass 6 on 3.3.1 item 5 (D3): a hard link to a config file is a write
// to it; a move of an ancestor of a main checkout outside the seat's tree
// replaces its config files; a link chain is followed to its end; and an
// earlier launch's pinned root is not the current launch's.
func TestSbxTheGuardHoldsLinksAncestorsAndThePinsLaunch(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not on PATH")
	}
	f := newHookFixture(t)
	base := filepath.Dir(f.repo)
	main := filepath.Join(base, "projects", "app")
	if err := os.MkdirAll(main, 0o755); err != nil {
		t.Fatal(err)
	}
	sbxGit(t, main, "init", "-q", "-b", "main")
	sbxGit(t, main, "commit", "-q", "--allow-empty", "-m", "base")
	wt := filepath.Join(base, "seats", "cto")
	sbxGit(t, main, "worktree", "add", "-q", "-b", "seat/cto", wt)
	mustWrite(t, filepath.Join(wt, ".mcp.json"), "{}")
	// a two-hop chain whose end does not exist yet
	if err := os.MkdirAll(filepath.Join(wt, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	hop1, end := filepath.Join(base, "hop1.json"), filepath.Join(base, "later", "settings.json")
	if err := os.Symlink(end, hop1); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(hop1, filepath.Join(wt, ".claude", "settings.local.json")); err != nil {
		t.Fatal(err)
	}
	for _, c := range []guardCall{
		{Tool: "Bash", Args: map[string]string{"command": "ln " + filepath.Join(wt, ".mcp.json") + " " + filepath.Join(base, "alias.json")}, CWD: wt},
		{Tool: "Bash", Args: map[string]string{"command": "mv " + filepath.Join(base, "projects") + " " + filepath.Join(base, "saved")}, CWD: wt},
		{Tool: "Bash", Args: map[string]string{"command": "mv " + filepath.Join(base, "prepared") + " " + filepath.Join(base, "projects")}, CWD: wt},
		{Tool: "Write", Args: map[string]string{"file_path": end}, CWD: wt},
	} {
		if v := evaluateGuard(f.id(), c, f.now); v.ReasonCode != "protected_path" {
			t.Errorf("%s %v: %s %s", c.Tool, c.Args, v.Verdict, v.ReasonCode)
		}
	}
	// an earlier launch's exec record names another root: not protected for
	// this launch (the seat's tree is elsewhere, and the cwd is outside git)
	other := filepath.Join(base, "other")
	stageSeatRecord(t, f.root, launch.Record{Handle: "cto", Root: f.root, Session: "s1", Worktree: wt, LaunchID: "L2"})
	execDir := filepath.Join(launch.StagedDir(f.root, "cto"), "exec")
	if err := os.MkdirAll(execDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := launch.WriteFile(filepath.Join(execDir, launch.FileName), launch.Record{Handle: "cto", Root: f.root, Worktree: wt, LaunchID: "L1", MainCheckout: other}); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(other, ".claude", "settings.local.json")
	if v := evaluateGuard(f.id(), guardCall{Tool: "Write", Args: map[string]string{"file_path": p}, CWD: t.TempDir()}, f.now); v.Verdict != "allow" {
		t.Errorf("Write %s, pinned only by an earlier launch: %s %s", p, v.Verdict, v.ReasonCode)
	}
}

// Codex pass 8 on 3.3.1 item 5 (D3): a path that differs only by Unicode
// normalization names the same file on macOS.
func TestSbxTheGuardComparesUnicodeNormalForms(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not on PATH")
	}
	f := newHookFixture(t)
	base := filepath.Dir(f.repo)
	main := filepath.Join(base, "caf\u00e9") // composed (NFC)
	if err := os.MkdirAll(main, 0o755); err != nil {
		t.Fatal(err)
	}
	sbxGit(t, main, "init", "-q", "-b", "main")
	sbxGit(t, main, "commit", "-q", "--allow-empty", "-m", "base")
	wt := filepath.Join(base, "seat-cto")
	sbxGit(t, main, "worktree", "add", "-q", "-b", "seat/cto", wt)
	decomposed := filepath.Join(base, "cafe\u0301", ".mcp.json") // NFD
	if v := evaluateGuard(f.id(), guardCall{Tool: "Write", Args: map[string]string{"file_path": decomposed}, CWD: wt}, f.now); v.ReasonCode != "protected_path" {
		t.Errorf("Write %q: %s %s", decomposed, v.Verdict, v.ReasonCode)
	}
}

// Codex pass 10 on 3.3.1 item 5 (D3): the guard's own reads of the git
// metadata a seat can point its .git at are bounded and non-blocking: a
// FIFO HEAD neither hangs the hook (whose watchdog fails open) nor lifts
// the pinned protection.
func TestSbxTheGuardDoesNotBlockOnPlantedGitMetadata(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not on PATH")
	}
	f := newHookFixture(t)
	base := filepath.Dir(f.repo)
	main := filepath.Join(base, "main")
	if err := os.MkdirAll(main, 0o755); err != nil {
		t.Fatal(err)
	}
	sbxGit(t, main, "init", "-q", "-b", "main")
	sbxGit(t, main, "commit", "-q", "--allow-empty", "-m", "base")
	wt := filepath.Join(main, ".worktrees", "cto")
	sbxGit(t, main, "worktree", "add", "-q", "-b", "seat/cto", wt)
	stageSeatRecord(t, f.root, launch.Record{Handle: "cto", Root: f.root, Session: "s1", Worktree: wt, LaunchID: "L1"})
	execDir := filepath.Join(launch.StagedDir(f.root, "cto"), "exec")
	if err := os.MkdirAll(execDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := launch.WriteFile(filepath.Join(execDir, launch.FileName), launch.Record{Handle: "cto", Root: f.root, Worktree: wt, LaunchID: "L1", MainCheckout: main}); err != nil {
		t.Fatal(err)
	}
	planted := filepath.Join(base, "planted-git")
	if err := os.MkdirAll(planted, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(filepath.Join(planted, "HEAD"), 0o600); err != nil {
		t.Skip(err)
	}
	mustWrite(t, filepath.Join(wt, ".git"), "gitdir: "+planted+"\n")
	done := make(chan guardVerdict, 1)
	go func() {
		done <- evaluateGuard(f.id(), guardCall{Tool: "Write", Args: map[string]string{"file_path": filepath.Join(main, ".claude", "settings.local.json")}, CWD: wt}, f.now)
	}()
	select {
	case v := <-done:
		if v.ReasonCode != "protected_path" {
			t.Errorf("Write the main checkout's settings after planting a FIFO HEAD: %s %s", v.Verdict, v.ReasonCode)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the guard blocked on a planted FIFO")
	}
}

// Codex pass 11 on 3.3.1 item 5 (D3): a seat whose worktree is recorded
// through a symlink has the real directory's ancestors protected too.
func TestSbxTheGuardProtectsTheRealAncestorsOfALinkedWorktree(t *testing.T) {
	f := newHookFixture(t)
	top := filepath.Dir(f.repo)
	real := filepath.Join(top, "data", "project", "repo")
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(top, "data", "project"), filepath.Join(top, "short")); err != nil {
		t.Fatal(err)
	}
	stageSeatRecord(t, f.root, launch.Record{Handle: "cto", Root: f.root, Session: "s1", Worktree: filepath.Join(top, "short", "repo")})
	for _, p := range []string{filepath.Join(top, "data", "project", ".mcp.json"), filepath.Join(top, "data", ".mcp.json")} {
		if v := evaluateGuard(f.id(), guardCall{Tool: "Write", Args: map[string]string{"file_path": p}, CWD: real}, f.now); v.ReasonCode != "protected_path" {
			t.Errorf("Write %s: %s %s", p, v.Verdict, v.ReasonCode)
		}
	}
}

// Codex pass 12 on 3.3.1 item 5 (D3): the pinned main checkout named
// through an alias has its real ancestors protected too, whatever the
// seat's .git pointer says now.
func TestSbxTheGuardProtectsThePinnedRootsRealAncestors(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not on PATH")
	}
	f := newHookFixture(t)
	top := filepath.Dir(f.repo)
	realMain := filepath.Join(top, "storage", "container", "repo")
	if err := os.MkdirAll(realMain, 0o755); err != nil {
		t.Fatal(err)
	}
	sbxGit(t, realMain, "init", "-q", "-b", "main")
	sbxGit(t, realMain, "commit", "-q", "--allow-empty", "-m", "base")
	if err := os.Symlink(filepath.Join(top, "storage", "container"), filepath.Join(top, "alias")); err != nil {
		t.Fatal(err)
	}
	wt := filepath.Join(top, "seats", "cto")
	sbxGit(t, realMain, "worktree", "add", "-q", "-b", "seat/cto", wt)
	stageSeatRecord(t, f.root, launch.Record{Handle: "cto", Root: f.root, Session: "s1", Worktree: wt, LaunchID: "L1"})
	execDir := filepath.Join(launch.StagedDir(f.root, "cto"), "exec")
	if err := os.MkdirAll(execDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := launch.WriteFile(filepath.Join(execDir, launch.FileName), launch.Record{Handle: "cto", Root: f.root, Worktree: wt, LaunchID: "L1", MainCheckout: filepath.Join(top, "alias", "repo")}); err != nil {
		t.Fatal(err)
	}
	decoy := filepath.Join(top, "decoy", ".git", "worktrees", "cto")
	if err := os.MkdirAll(decoy, 0o755); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(decoy, "commondir"), "../..\n")
	mustWrite(t, filepath.Join(wt, ".git"), "gitdir: "+decoy+"\n")
	for _, cmd := range []string{"mv " + filepath.Join(top, "storage") + " " + filepath.Join(top, "saved"), "mv " + filepath.Join(top, "prepared") + " " + filepath.Join(top, "storage")} {
		if v := evaluateGuard(f.id(), guardCall{Tool: "Bash", Args: map[string]string{"command": cmd}, CWD: wt}, f.now); v.ReasonCode != "protected_path" {
			t.Errorf("Bash %q: %s %s", cmd, v.Verdict, v.ReasonCode)
		}
	}
}
