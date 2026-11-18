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
	stageSeatRecord(t, f.root, launch.Record{Handle: "cto", Runtime: "claude", Root: f.root, Session: "s1", Worktree: wt})
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
			if v := evaluateGuard(f.id(), call, f.now); v.Verdict != "deny" || v.ReasonCode != "protected_path" {
				t.Errorf("%s %s from the worktree: %s %s", call.Tool, p, v.Verdict, v.ReasonCode)
			}
		}
	}
	// in a plain checkout (the seat's tree is the main one) its .mcp.json too
	if v := evaluateGuard(f.id(), guardCall{Tool: "Write", Args: map[string]string{"file_path": filepath.Join(main, ".mcp.json")}, CWD: main}, f.now); v.Verdict != "deny" || v.ReasonCode != "protected_path" {
		t.Errorf("Write .mcp.json in the main checkout: %s %s", v.Verdict, v.ReasonCode)
	}
	// ordinary work in the seat's own tree stays allowed (Codex pass 4): a
	// copy into it, a new directory, a file beside the config files
	for _, cmd := range []string{"cp /tmp/x .", "cp -r /tmp/src " + wt, "mkdir -p " + filepath.Join(wt, "build"), "touch " + filepath.Join(wt, "notes.md"), "mv /tmp/draft.md ."} {
		if v := evaluateGuard(f.id(), guardCall{Tool: "Bash", Args: map[string]string{"command": cmd}, CWD: wt}, f.now); v.Verdict != "allow" {
			t.Errorf("Bash %q in the seat's tree: %s %s", cmd, v.Verdict, v.ReasonCode)
		}
	}
	// the set is the config files only: the main checkout's other files and
	// the worktree's own sources stay writable
	// a tool's file path is literal: no glob or brace in it is expanded
	// (Codex r2 pass 2)
	mustWrite(t, filepath.Join(wt, ".mcp.json"), "{}")
	for _, p := range []string{filepath.Join(wt, "{.mcp.json,notes.md}"), filepath.Join(wt, ".mcp.jso?"), filepath.Join(wt, ".mcp.*")} {
		if v := evaluateGuard(f.id(), guardCall{Tool: "Write", Args: map[string]string{"file_path": p}, CWD: wt}, f.now); v.Verdict != "allow" {
			t.Errorf("Write the literal %s: %s %s", p, v.Verdict, v.ReasonCode)
		}
	}
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
	stageSeatRecord(t, f.root, launch.Record{Handle: "cto", Runtime: "claude", Root: f.root, Session: "s1", Worktree: wt})
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
		if v := evaluateGuard(f.id(), guardCall{Tool: "Write", Args: map[string]string{"file_path": c.path}, CWD: c.cwd}, f.now); v.Verdict != "deny" || v.ReasonCode != "protected_path" {
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
		"rm -rf " + main,
		"rm -rf " + filepath.Join(wt, ".claude"),
	} {
		if v := evaluateGuard(f.id(), guardCall{Tool: "Bash", Args: map[string]string{"command": cmd}, CWD: wt}, f.now); v.Verdict != "deny" || v.ReasonCode != "protected_path" {
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
		if v := evaluateGuard(f.id(), guardCall{Tool: "Write", Args: map[string]string{"file_path": p}, CWD: bwt}, f.now); v.Verdict != "deny" || v.ReasonCode != "protected_path" {
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
	stageSeatRecord(t, f.root, launch.Record{Handle: "cto", Runtime: "claude", Root: f.root, Session: "s1", Worktree: wt, LaunchID: "L1"})
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
		if v := evaluateGuard(f.id(), guardCall{Tool: "Write", Args: map[string]string{"file_path": p}, CWD: wt}, f.now); v.Verdict != "deny" || v.ReasonCode != "protected_path" {
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
		if v := evaluateGuard(f.id(), c, f.now); v.Verdict != "deny" || v.ReasonCode != "protected_path" {
			t.Errorf("%s %v: %s %s", c.Tool, c.Args, v.Verdict, v.ReasonCode)
		}
	}
	// an earlier launch's exec record names another root: not protected for
	// this launch (the seat's tree is elsewhere, and the cwd is outside git)
	other := filepath.Join(base, "other")
	stageSeatRecord(t, f.root, launch.Record{Handle: "cto", Runtime: "claude", Root: f.root, Session: "s1", Worktree: wt, LaunchID: "L2"})
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
	stageSeatRecord(t, f.root, launch.Record{Handle: "cto", Runtime: "claude", Root: f.root, Session: "s1", Worktree: wt})
	decomposed := filepath.Join(base, "cafe\u0301", ".mcp.json") // NFD
	if v := evaluateGuard(f.id(), guardCall{Tool: "Write", Args: map[string]string{"file_path": decomposed}, CWD: wt}, f.now); v.Verdict != "deny" || v.ReasonCode != "protected_path" {
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
	stageSeatRecord(t, f.root, launch.Record{Handle: "cto", Runtime: "claude", Root: f.root, Session: "s1", Worktree: wt, LaunchID: "L1"})
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
		if v.Verdict != "deny" || v.ReasonCode != "protected_path" {
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
	stageSeatRecord(t, f.root, launch.Record{Handle: "cto", Runtime: "claude", Root: f.root, Session: "s1", Worktree: filepath.Join(top, "short", "repo")})
	for _, p := range []string{filepath.Join(top, "data", "project", ".mcp.json"), filepath.Join(top, "data", ".mcp.json")} {
		if v := evaluateGuard(f.id(), guardCall{Tool: "Write", Args: map[string]string{"file_path": p}, CWD: real}, f.now); v.Verdict != "deny" || v.ReasonCode != "protected_path" {
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
	stageSeatRecord(t, f.root, launch.Record{Handle: "cto", Runtime: "claude", Root: f.root, Session: "s1", Worktree: wt, LaunchID: "L1"})
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
		if v := evaluateGuard(f.id(), guardCall{Tool: "Bash", Args: map[string]string{"command": cmd}, CWD: wt}, f.now); v.Verdict != "deny" || v.ReasonCode != "protected_path" {
			t.Errorf("Bash %q: %s %s", cmd, v.Verdict, v.ReasonCode)
		}
	}
}

// sbxR2Squad is a main checkout with a linked worktree and the seat's staged
// record for runtime (round 2 of 3.3.1 item 5).
func sbxR2Squad(t *testing.T, f hookFixture, runtime string) (main, wt string) {
	t.Helper()
	base := filepath.Dir(f.repo)
	main = filepath.Join(base, "main")
	if err := os.MkdirAll(main, 0o755); err != nil {
		t.Fatal(err)
	}
	sbxGit(t, main, "init", "-q", "-b", "main")
	sbxGit(t, main, "commit", "-q", "--allow-empty", "-m", "base")
	wt = filepath.Join(main, ".worktrees", "cto")
	sbxGit(t, main, "worktree", "add", "-q", "-b", "seat/cto", wt)
	stageSeatRecord(t, f.root, launch.Record{Handle: "cto", Root: f.root, Session: "s1", Worktree: wt, Runtime: runtime, LaunchID: "L1"})
	return main, wt
}

// Round 2, fix 1 (P1): Claude Code reloads agent definitions (and skills)
// mid-session, so the guard refuses a Claude seat's write anywhere under
// .claude/agents/ and .claude/skills/, in the worktree and the main
// checkout: everything the launch check reads.
func TestSbxR2TheGuardProtectsAgentsAndSkills(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not on PATH")
	}
	f := newHookFixture(t)
	main, wt := sbxR2Squad(t, f, "claude")
	for _, p := range []string{
		filepath.Join(wt, ".claude", "agents", "fs.md"),
		filepath.Join(wt, ".claude", "agents", "review", "fs.md"),
		filepath.Join(main, ".claude", "agents", "fs.md"),
		filepath.Join(wt, ".claude", "skills", "release", "SKILL.md"),
		filepath.Join(main, ".claude", "skills", "demo", ".claude-plugin", "plugin.json"),
		filepath.Join(wt, ".claude", "skills", "demo", "agents", "fs.md"),
	} {
		for _, call := range []guardCall{
			{Tool: "Write", Args: map[string]string{"file_path": p}, CWD: wt},
			{Tool: "Edit", Args: map[string]string{"file_path": p, "old_string": "a", "new_string": "b"}, CWD: wt},
			{Tool: "Bash", Args: map[string]string{"command": "echo x > " + p}, CWD: wt},
		} {
			if v := evaluateGuard(f.id(), call, f.now); v.Verdict != "deny" || v.ReasonCode != "protected_path" {
				t.Errorf("%s %s: %s %s", call.Tool, p, v.Verdict, v.ReasonCode)
			}
		}
	}
}

// Round 2, fix 3 (P2, no pi change): the protection of Claude Code's
// configuration applies to Claude seats only; a pi or Codex seat's write to
// its worktree's .mcp.json or agents is allowed, as before.
func TestSbxR2TheClaudeConfigProtectionIsForClaudeSeatsOnly(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not on PATH")
	}
	for _, runtime := range []string{"pi", "codex", "claude"} {
		t.Run(runtime, func(t *testing.T) {
			f := newHookFixture(t)
			main, wt := sbxR2Squad(t, f, runtime)
			payload := t.TempDir()
			sbxWrite(t, filepath.Join(payload, ".mcp.json"), `{"mcpServers":{"x":{"command":"/tmp/server"}}}`)
			for _, c := range []guardCall{
				{Tool: "Write", Args: map[string]string{"file_path": filepath.Join(wt, ".mcp.json")}, CWD: wt},
				{Tool: "Write", Args: map[string]string{"file_path": filepath.Join(wt, ".claude", "agents", "fs.md")}, CWD: wt},
				{Tool: "Write", Args: map[string]string{"file_path": filepath.Join(main, ".mcp.json")}, CWD: wt},
				{Tool: "Bash", Args: map[string]string{"command": "cp " + payload + "/.mcp.json " + filepath.Join(wt, ".mcp.json")}, CWD: wt},
				{Tool: "Bash", Args: map[string]string{"command": "echo x > .claude/skills/x/SKILL.md"}, CWD: wt},
			} {
				v := evaluateGuard(f.id(), c, f.now)
				if runtime == "claude" && (v.Verdict != "deny" || v.ReasonCode != "protected_path") {
					t.Errorf("claude seat %s %v: %s %s", c.Tool, c.Args, v.Verdict, v.ReasonCode)
				}
				if runtime != "claude" && v.Verdict != "allow" {
					t.Errorf("%s seat %s %v: %s %s", runtime, c.Tool, c.Args, v.Verdict, v.ReasonCode)
				}
			}
		})
	}
}

// Codex r2 pass 1: a staged record the guard cannot read fails closed by
// name (any .mcp.json, .claude directory, settings file, agents or skills),
// whatever the hook's cwd;
// the .claude directory's other files stay writable.
func TestSbxR2AnUnreadableStagedRecordProtectsByName(t *testing.T) {
	f := newHookFixture(t)
	outside, wt := t.TempDir(), t.TempDir()
	if err := os.MkdirAll(filepath.Join(wt, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{
		filepath.Join(wt, ".mcp.json"),
		filepath.Join(wt, ".claude"),
		filepath.Join(wt, ".claude", "settings.json"),
		filepath.Join(wt, ".claude", "settings.local.json"),
		filepath.Join(wt, ".claude", "agents", "fs.md"),
		filepath.Join(wt, ".claude", "skills", "x", "SKILL.md"),
	} {
		if v := evaluateGuard(f.id(), guardCall{Tool: "Write", Args: map[string]string{"file_path": p}, CWD: outside}, f.now); v.Verdict != "deny" || v.ReasonCode != "protected_path" {
			t.Errorf("Write %s with no staged record: %s %s", p, v.Verdict, v.ReasonCode)
		}
	}
	if v := evaluateGuard(f.id(), guardCall{Tool: "Bash", Args: map[string]string{"command": "cp /tmp/e/x " + filepath.Join(wt, ".mcp.json")}, CWD: outside}, f.now); v.Verdict != "deny" || v.ReasonCode != "protected_path" {
		t.Errorf("a copy onto %s/.mcp.json with no staged record: %s %s", wt, v.Verdict, v.ReasonCode)
	}
	for _, p := range []string{filepath.Join(wt, ".claude", "notes.md"), filepath.Join(wt, "docs", "a.md")} {
		if v := evaluateGuard(f.id(), guardCall{Tool: "Write", Args: map[string]string{"file_path": p}, CWD: outside}, f.now); v.Verdict != "allow" {
			t.Errorf("Write %s with no staged record: %s %s", p, v.Verdict, v.ReasonCode)
		}
	}
}

func sbxWrite(t *testing.T, p, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// Codex r2b pass 3: every ln operand is a write, the link it makes (last)
// and the middle ones too, not only a protected source.
func TestSbxR2TheGuardHoldsEveryLnOperand(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not on PATH")
	}
	f := newHookFixture(t)
	_, wt := sbxR2Squad(t, f, "claude")
	for _, cmd := range []string{
		"ln /tmp/source " + filepath.Join(wt, ".mcp.json"),
		"ln -f /tmp/a " + filepath.Join(wt, ".claude", "settings.json") + " /tmp/dir",
	} {
		if v := evaluateGuard(f.id(), guardCall{Tool: "Bash", Args: map[string]string{"command": cmd}, CWD: wt}, f.now); v.Verdict != "deny" || v.ReasonCode != "protected_path" {
			t.Errorf("Bash %q: %s %s", cmd, v.Verdict, v.ReasonCode)
		}
	}
}

// Codex r2b pass 1: a tool's path is checked whatever it starts with (a
// relative "-alias/..." through a link to the worktree is no option), and
// MultiEdit and NotebookEdit are held as Write and Edit are.
func TestSbxR2TheGuardHoldsEveryToolPath(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not on PATH")
	}
	f := newHookFixture(t)
	main, wt := sbxR2Squad(t, f, "claude")
	sbxWrite(t, filepath.Join(wt, ".claude", "settings.json"), "{}")
	if err := os.Symlink(".", filepath.Join(wt, "-alias")); err != nil {
		t.Fatal(err)
	}
	for _, c := range []guardCall{
		{Tool: "Edit", Args: map[string]string{"file_path": "-alias/.claude/settings.json", "old_string": "{}", "new_string": "{\"enabledPlugins\":{\"x@y\":true}}"}, CWD: wt},
		{Tool: "Write", Args: map[string]string{"file_path": "-alias/.mcp.json"}, CWD: wt},
		{Tool: "MultiEdit", Args: map[string]string{"file_path": filepath.Join(main, ".claude", "settings.local.json")}, CWD: wt},
		{Tool: "NotebookEdit", Args: map[string]string{"notebook_path": filepath.Join(wt, ".claude", "agents", "fs.md")}, CWD: wt},
	} {
		if v := evaluateGuard(f.id(), c, f.now); v.Verdict != "deny" || v.ReasonCode != "protected_path" {
			t.Errorf("%s %v: %s %s", c.Tool, c.Args, v.Verdict, v.ReasonCode)
		}
	}
	if v := evaluateGuard(f.id(), guardCall{Tool: "Write", Args: map[string]string{"file_path": "-alias/docs/a.md"}, CWD: wt}, f.now); v.Verdict != "allow" {
		t.Errorf("Write -alias/docs/a.md: %s %s", v.Verdict, v.ReasonCode)
	}
}

// Codex r2b pass 1: a staged record that exists but cannot be decoded fails
// closed as a missing one does.
func TestSbxR2ACorruptStagedRecordProtectsByName(t *testing.T) {
	f := newHookFixture(t)
	wt := t.TempDir()
	if err := os.MkdirAll(launch.StagedDir(f.root, "cto"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(launch.StagedDir(f.root, "cto"), launch.FileName), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{filepath.Join(wt, ".mcp.json"), filepath.Join(wt, ".claude", "agents", "fs.md")} {
		if v := evaluateGuard(f.id(), guardCall{Tool: "Write", Args: map[string]string{"file_path": p}, CWD: wt}, f.now); v.Verdict != "deny" || v.ReasonCode != "protected_path" {
			t.Errorf("Write %s with a corrupt staged record: %s %s", p, v.Verdict, v.ReasonCode)
		}
	}
}

// Codex r2b pass 2: a case-insensitive filesystem folds more than ASCII: a
// long s (U+017F) spells s, the Kelvin sign (U+212A) spells k. The guard
// folds every case orbit alike.
func TestSbxR2TheGuardFoldsUnicodeCase(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not on PATH")
	}
	f := newHookFixture(t)
	_, wt := sbxR2Squad(t, f, "claude")
	sbxWrite(t, filepath.Join(wt, ".claude", "settings.json"), "{}")
	sbxWrite(t, filepath.Join(wt, ".claude", "skills", "demo", "SKILL.md"), "x")
	for _, p := range []string{
		filepath.Join(wt, ".claude", "\u017fettings.json"),
		filepath.Join(wt, ".claude", "\u017fkills", "demo", "SKILL.md"),
		filepath.Join(wt, ".claude", "s\u212Aills", "demo", "SKILL.md"),
		filepath.Join(wt, ".mcp.j\u017fon"),
	} {
		if v := evaluateGuard(f.id(), guardCall{Tool: "Write", Args: map[string]string{"file_path": p}, CWD: wt}, f.now); v.Verdict != "deny" || v.ReasonCode != "protected_path" {
			t.Errorf("Write %q: %s %s", p, v.Verdict, v.ReasonCode)
		}
	}
}
