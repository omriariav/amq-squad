package seatcommit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/claudeseat"
	"github.com/omriariav/amq-squad/v3/internal/launch"
	"github.com/omriariav/amq-squad/v3/internal/task"
)

// Tests of the 2b-2 milestone B fix commit 1 (the first pass on e2a59176).

// R2 (commit.go:614): the push went to the seat repository's
// remote.origin.pushurl, which the seat can rewrite. It goes to the URL the
// launch recorded from the owner's grant, passed explicitly from the
// harness's own git dir, so neither a pushurl nor an insteadOf of the seat's
// config moves it.
func TestPushGoesToTheGrantsURLNeverTheSeatsConfig(t *testing.T) {
	f := newFixture(t)
	evil := filepath.Join(f.dir, "evil.git")
	sh(t, f.dir, "init", "-q", "--bare", evil)
	sh(t, f.wt, "config", "remote.origin.pushurl", evil)
	sh(t, f.wt, "config", "url."+evil+".pushInsteadOf", f.origin)
	sh(t, f.wt, "config", "url."+evil+".insteadOf", f.origin)
	write(t, filepath.Join(f.wt, "cmd", "root.go"), "package cmd\n// pushed\n")
	res := f.run(f.reported("cmd/"))
	if !res.OK || !res.Pushed || f.originBranch() != res.Commit {
		t.Fatalf("the grant's origin did not get the push: %+v (origin %q)", res, f.originBranch())
	}
	cmd := []string{"--git-dir=" + evil, "rev-parse", "--verify", "-q", "refs/heads/" + f.branch}
	if out, err := runGit(t, f.dir, cmd...); err == nil && strings.TrimSpace(out) != "" {
		t.Fatalf("the seat's pushurl got the branch %s", out)
	}
}

func runGit(t *testing.T, dir string, args ...string) (string, error) {
	t.Helper()
	g := git{bin: gitBin(t), dir: dir, env: harnessEnv(os.Environ())}
	out, err := g.run(nil, nil, args...)
	return string(out), err
}

// R2 (commit.go:468, git.go:27): the snapshot ran git add, which executes a
// seat-configured clean filter with the harness environment. The snapshot
// is hash-object --no-filters in the harness's own git dir, and CleanSeat's
// checkout runs there too: no clean or smudge filter of the seat's
// repository ever runs, and the bytes committed are the worktree's.
func TestSeatFilterDriversNeverRunInTheHarness(t *testing.T) {
	f := newFixture(t)
	marker := filepath.Join(f.dir, "filter-ran")
	sh(t, f.wt, "config", "filter.evil.clean", "sh -c 'echo clean >> "+marker+"; cat'")
	sh(t, f.wt, "config", "filter.evil.smudge", "sh -c 'echo smudge >> "+marker+"; cat'")
	sh(t, f.wt, "config", "filter.evil.required", "true")
	common := sh(t, f.wt, "rev-parse", "--path-format=absolute", "--git-common-dir")
	write(t, filepath.Join(common, "info", "attributes"), "* filter=evil\n")
	write(t, filepath.Join(f.wt, ".gitattributes"), "* filter=evil\n")
	write(t, filepath.Join(f.wt, "cmd", "root.go"), "package cmd\n// filtered?\n")
	tk := f.reported("cmd/", ".gitattributes")
	res := f.run(tk)
	if !res.OK || !res.Pushed {
		t.Fatalf("commit: %+v", res)
	}
	if _, err := f.store.CompleteBound(tk.ID, 0, res.Commit, f.now); err != nil {
		t.Fatal(err)
	}
	if raw, err := os.ReadFile(marker); err == nil {
		t.Fatalf("a seat filter ran in the harness's git: %q", raw)
	}
	if out, _ := runGit(t, f.dir, "--git-dir="+f.origin, "cat-file", "blob", res.Commit+":cmd/root.go"); out != "package cmd\n// filtered?\n" {
		t.Fatalf("the committed bytes are not the worktree's: %q", out)
	}
	// CleanSeat's checkout: a leftover diff reset in the shadow.
	tk2, _ := f.store.Add(task.AddInput{Title: "next", Class: "implement", DeclaredFiles: []string{"cmd/"}, ProjectPath: f.main}, f.now)
	f.store.Claim(tk2.ID, f.handle, nil, time.Hour, f.now)
	f.store.Fail(tk2.ID, f.handle, "red", f.now)
	write(t, filepath.Join(f.wt, "cmd", "root.go"), "package cmd\n// leftover\n")
	if d, err := CleanSeat(f.root, f.handle, gitBin(t), nil, f.trust(), f.now); err != nil || !d {
		t.Fatalf("clean: %v %v", d, err)
	}
	if raw, err := os.ReadFile(marker); err == nil {
		t.Fatalf("a seat filter ran in CleanSeat: %q", raw)
	}
}

// R2 (commit.go:465, git.go:49): replacement refs of the seat's repository
// were honored, so a replace object could show the diff against another
// parent tree. The harness's git never sees them (its own git dir, and
// GIT_NO_REPLACE_OBJECTS=1).
func TestReplaceRefsDoNotHideAProtectedChange(t *testing.T) {
	f := newFixture(t)
	f.grants.ProtectedPaths = []string{".github/workflows/"}
	write(t, filepath.Join(f.wt, ".github", "workflows", "ci.yml"), "on: [push, pull_request]\n")
	write(t, filepath.Join(f.wt, "cmd", "root.go"), "package cmd\n// x\n")
	// The seat commits its tree and replaces the base commit with that one,
	// so a replace-honoring diff from the base shows nothing.
	sh(t, f.wt, "add", "-A")
	sh(t, f.wt, "commit", "-q", "-m", "seat")
	fake := sh(t, f.wt, "rev-parse", "HEAD")
	sh(t, f.wt, "replace", f.base, fake)
	res := f.run(f.reported("cmd/"))
	if res.ReasonCode != ReasonProtected || !strings.Contains(res.Detail, ".github/workflows/ci.yml") {
		t.Fatalf("a replace ref hid the protected change: %+v", res)
	}
}

// The R2 environment and command-line set of every harness git call.
func TestHarnessGitEnvironmentIsHardened(t *testing.T) {
	env := strings.Join(harnessEnv([]string{"GIT_DIR=/x", "GIT_CONFIG_GLOBAL=/home/u/.gitconfig", "ANTHROPIC_API_KEY=k", "PATH=/bin"}), " ")
	for _, want := range []string{"GIT_CONFIG_GLOBAL=" + os.DevNull, "GIT_CONFIG_NOSYSTEM=1", "GIT_NO_REPLACE_OBJECTS=1", "GIT_ATTR_NOSYSTEM=1", "PATH=/bin"} {
		if !strings.Contains(env, want) {
			t.Fatalf("missing %s", want)
		}
	}
	for _, gone := range []string{"GIT_DIR=/x", "/home/u/.gitconfig", "ANTHROPIC_API_KEY"} {
		if strings.Contains(env, gone) {
			t.Fatalf("%s reached the harness's git", gone)
		}
	}
	cfg := strings.Join(safeConfig(), " ")
	for _, want := range []string{"core.hooksPath=" + os.DevNull, "core.attributesFile=" + os.DevNull, "core.fsmonitor=false"} {
		if !strings.Contains(cfg, want) {
			t.Fatalf("missing -c %s", want)
		}
	}
}

// AddWorktree, the scheduler's seat worktree: no hook or filter of the
// shared repository config runs, and the files are the branch's bytes.
func TestAddWorktreeRunsNoSeatHookOrFilter(t *testing.T) {
	f := newFixture(t)
	marker := filepath.Join(f.dir, "ran")
	sh(t, f.main, "config", "filter.evil.smudge", "sh -c 'echo smudge >> "+marker+"; cat'")
	write(t, filepath.Join(sh(t, f.main, "rev-parse", "--path-format=absolute", "--git-common-dir"), "info", "attributes"), "* filter=evil\n")
	hook := filepath.Join(f.main, ".git", "hooks", "post-checkout")
	write(t, hook, "#!/bin/sh\necho hook >> "+marker+"\n")
	os.Chmod(hook, 0o755)
	wt := filepath.Join(f.dir, "wt", "s2")
	if err := AddWorktree(gitBin(t), f.main, wt, "seat/s2", "main", nil); err != nil {
		t.Fatal(err)
	}
	if raw, err := os.ReadFile(marker); err == nil {
		t.Fatalf("a repository hook or filter ran: %q", raw)
	}
	if raw, _ := os.ReadFile(filepath.Join(wt, "cmd", "root.go")); string(raw) != "package cmd\n" {
		t.Fatalf("checkout: %q", raw)
	}
	if rp, err := worktreePaths(wt); err != nil || rp.HeadRef != "refs/heads/seat/s2" {
		t.Fatalf("worktree: %+v %v", rp, err)
	}
}

// R3 (scheduler.go:233): the exit check ran only on a done report. ExitCheck
// is the same check at a failed, gone or expired exit: a global config
// change quarantines the seat with its tree kept on the ledger.
func TestExitCheckQuarantinesAtAnyExit(t *testing.T) {
	f := newFixture(t)
	orig := `{"a":1}`
	write(t, filepath.Join(f.home, ".claude", "settings.json"), orig)
	write(t, filepath.Join(f.home, ".amq-squad", "preflight", "claude-settings.json"), orig)
	o := f.opts(task.Task{ID: "t1"})
	if r := ExitCheck(o, "failed"); !r.OK || r.Quarantine {
		t.Fatalf("no change: %+v", r)
	}
	write(t, filepath.Join(f.wt, "cmd", "root.go"), "package cmd\n// left\n")
	write(t, filepath.Join(f.home, ".claude", "settings.json"), `{"a":2}`)
	r := ExitCheck(o, "gone")
	if !r.Quarantine || r.ReasonCode != ReasonGlobalConfig || !strings.Contains(r.Detail, "(gone)") {
		t.Fatalf("a config change at a gone exit: %+v", r)
	}
	l, _ := ReadLedger(f.root, f.handle)
	if l.Quarantine == nil || l.Quarantine.Tree == "" || l.Quarantine.Task != "t1" {
		t.Fatalf("the tree was not kept: %+v", l.Quarantine)
	}
}

// P1 at the exit: a Claude seat whose config dir no longer has its launch
// digest is claude_config_changed, quarantined like a global config change.
func TestClaudeConfigDriftAtTheExitQuarantines(t *testing.T) {
	f := newFixture(t)
	dir := claudeseat.Dir(f.root, f.handle, "launch-1")
	w, err := claudeseat.Write(dir, "dontAsk", f.wt, f.now)
	if err != nil {
		t.Fatal(err)
	}
	rec := launch.Record{Handle: f.handle, Root: f.root, Session: "sess", Runtime: "claude", Worktree: f.wt, Branch: f.branch, LaunchID: "launch-1", ClaudeConfig: w.Digest}
	if err := launch.WriteFile(filepath.Join(launch.StagedDir(f.root, f.handle), launch.FileName), rec); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(f.wt, "cmd", "root.go"), "package cmd\n// c\n")
	tk := f.reported("cmd/")
	write(t, filepath.Join(dir, "settings.json"), `{"permissions":{"allow":["Bash(*)"]}}`)
	if res := f.run(tk); res.ReasonCode != ReasonClaudeConfig || !res.Quarantine || res.Route != task.RouteOwner {
		t.Fatalf("a changed config dir at the done report: %+v", res)
	}
}

// R3 (commit.go:329-370): CleanSeat checked the holder without a
// reservation, so a claim between the check and reset --hard raced. The
// whole reset runs under the task store's lock: a claim waits until the
// reset is done.
func TestCleanSeatHoldsTheStoreLockForTheWholeReset(t *testing.T) {
	f := newFixture(t)
	tk, _ := f.store.Add(task.AddInput{Title: "a", Class: "implement", DeclaredFiles: []string{"cmd/"}, ProjectPath: f.main}, f.now)
	f.store.Claim(tk.ID, f.handle, nil, time.Hour, f.now)
	f.store.Fail(tk.ID, f.handle, "red", f.now)
	write(t, filepath.Join(f.wt, "cmd", "root.go"), "package cmd\n// leftover\n")
	next, _ := f.store.Add(task.AddInput{Title: "b", Class: "implement", DeclaredFiles: []string{"docs/"}, ProjectPath: f.main}, f.now)
	claimed := make(chan time.Time, 1)
	var hookEnded time.Time
	beforeReset = func() {
		go func() {
			if _, err := f.store.Claim(next.ID, f.handle, nil, time.Hour, f.now); err == nil {
				claimed <- time.Now()
			} else {
				claimed <- time.Time{}
			}
		}()
		time.Sleep(300 * time.Millisecond)
		hookEnded = time.Now()
	}
	t.Cleanup(func() { beforeReset = nil })
	if d, err := CleanSeat(f.root, f.handle, gitBin(t), nil, f.trust(), f.now); err != nil || !d {
		t.Fatalf("clean: %v %v", d, err)
	}
	select {
	case at := <-claimed:
		if !at.IsZero() && at.Before(hookEnded) {
			t.Fatal("a claim landed between CleanSeat's check and its reset")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the claim never finished")
	}
}

// P2 (policy.go:185, widen.go:109): a managed-block change was a note and
// the widening succeeded while the commit stayed refused. It is a
// protected-path growth crossing: the gate is raised for those files, and
// once the owner accepts it the commit goes through.
func TestManagedBlockChangeRaisesTheGrowthGate(t *testing.T) {
	f := newFixture(t)
	write(t, filepath.Join(f.wt, "CLAUDE.md"), strings.Replace(claudeMD, "managed rules", "managed rules, edited", 1))
	tk := f.reported("cmd/")
	res := f.run(tk)
	if res.ReasonCode != ReasonProtected {
		t.Fatalf("managed block change: %+v", res)
	}
	blocked, _ := f.store.RefuseCommit(tk.ID, res.ReasonCode, res.Detail, res.Route, f.now)
	widen := WidenOptions{Root: f.root, Task: blocked, Added: []string{"CLAUDE.md"}, Grants: f.grants, RepoRoot: f.main, Home: f.home,
		Commit: &Options{Root: f.root, Handle: f.handle, Git: gitBin(t), Now: func() time.Time { return f.now }}}
	d := CheckWiden(widen)
	want := ProtectedTarget(f.root, tk.ID, []string{"CLAUDE.md"})
	if d.Allowed || d.ReasonCode != ReasonGrowthGate || len(d.Crossings) != 1 || d.Crossings[0].Target != want {
		t.Fatalf("want the growth gate for the managed block, got %+v", d)
	}
	f.gates = fakeGates{want: true}
	widen.Gates, widen.Commit.Gates = f.gates, f.gates
	if d = CheckWiden(widen); !d.Allowed || len(d.Gates) != 1 {
		t.Fatalf("with the gate accepted: %+v", d)
	}
	rearmed, err := f.store.WidenGated(tk.ID, "lead", "rules edit approved", widen.Added, d.Gates, time.Hour, f.now)
	if err != nil {
		t.Fatal(err)
	}
	if res := f.run(rearmed); !res.OK || !res.Pushed {
		t.Fatalf("commit after the accepted gate: %+v", res)
	}
}
