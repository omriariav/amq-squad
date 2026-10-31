package seatcommit

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/launch"
	"github.com/omriariav/amq-squad/v3/internal/task"
)

// 3.0.2 t24 and t23 (the v31 finding, docs/v3-spike/5-302-evidence/
// commitstep.md): the binding check runs before the commit and the push,
// and every task commits on its own branch from the base tip.

// branchGh is a gh stand-in that keeps the open PRs per branch: `pr list
// --head B` prints B's fixed list when a row wrote one, else the PR the
// stand-in opened on B at the origin's head of B now (a PR follows its
// branch), else "[]"; `pr create --head B` opens the next PR number on B.
// Every call is appended to the calls file.
func branchGh(t *testing.T, f *fixture) (gh, dir, calls string) {
	t.Helper()
	gh, dir, calls = filepath.Join(f.dir, "gh"), filepath.Join(f.dir, "gh-prs"), filepath.Join(f.dir, "gh-calls")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\necho \"$*\" >> " + calls + "\ncase \"$2\" in\n" +
		"list) f=" + dir + "/$(echo \"$4\" | tr / _)\n" +
		"  if [ -f \"$f\" ]; then cat \"$f\"; elif [ -f \"$f.n\" ]; then head=$(" + gitBin(t) + " --git-dir=" + f.origin + " rev-parse refs/heads/$4)\n" +
		"    printf '[{\"number\":%s,\"state\":\"OPEN\",\"headRefOid\":\"%s\",\"baseRefName\":\"main\",\"isCrossRepository\":false}]' $(cat \"$f.n\") \"$head\"\n" +
		"  else echo '[]'; fi ;;\n" +
		"create) n=$(( $(cat " + dir + "/next 2>/dev/null || echo 0) + 1 )); echo $n > " + dir + "/next\n" +
		"  echo $n > " + dir + "/$(echo \"$5\" | tr / _).n\n" +
		"  echo https://github.com/o/r/pull/$n ;;\nesac\n"
	write(t, gh, script)
	os.Chmod(gh, 0o755)
	return gh, dir, calls
}

// leased adds a task and leases it to the seat the way the scheduler does:
// the lease boundary (CleanSeat), the grant, then StartTask from the
// doorbell, before the seat works.
func (f *fixture) leased(files ...string) task.Task {
	f.t.Helper()
	if _, err := CleanSeat(f.root, f.handle, gitBin(f.t), nil, f.trust(), f.now); err != nil {
		f.t.Fatal(err)
	}
	tk, err := f.store.Add(task.AddInput{Title: "the task", Class: "implement", DeclaredFiles: files, ProjectPath: f.main}, f.now)
	if err != nil {
		f.t.Fatal(err)
	}
	if tk, err = f.store.Claim(tk.ID, f.handle, nil, time.Hour, f.now); err != nil {
		f.t.Fatal(err)
	}
	if err := StartTask(f.root, f.handle, gitBin(f.t), nil, tk, f.now); err != nil {
		f.t.Fatal(err)
	}
	return tk
}

// done files the seat's done report on a leased task.
func (f *fixture) done(id string) task.Task {
	f.t.Helper()
	harnessPass(f.t, f.store, id, f.now)
	tk, err := f.store.Done(id, f.handle, "go test ok", "", f.now)
	if err != nil {
		f.t.Fatal(err)
	}
	return tk
}

func (f *fixture) prOpts(tk task.Task, gh string) Options {
	o := f.opts(tk)
	o.OpenPR, o.Gh = true, gh
	f.storeBinding(&o)
	return o
}

// Rule 1 and the second row of amendment 2c: a task branch that carries an
// open PR bound to another task (v31: seat/dev carried PR #815 of t12 when
// t20 was committed onto it) refuses with pr_bound_elsewhere before
// anything is written: no commit, no push, the ledger unchanged. A gh that
// fails at the check defers the same way. Fails when the check moves back
// after the push.
func TestBindingCheckRunsBeforeTheCommit(t *testing.T) {
	for _, name := range []string{"bound elsewhere", "gh fails"} {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			gh, prs, calls := branchGh(t, f)
			other, _ := f.store.Add(task.AddInput{Title: "t12", Class: "implement", DeclaredFiles: []string{"z/"}}, f.now)
			f.store.Claim(other.ID, "s9", nil, time.Hour, f.now)
			harnessPass(t, f.store, other.ID, f.now)
			f.store.Done(other.ID, "s9", "ok", "", f.now)
			if _, err := f.store.CompleteBound(other.ID, fixtureRepo, 815, strings.Repeat("c", 40), f.now); err != nil {
				t.Fatal(err)
			}
			tk := f.leased("cmd/")
			branch := TaskBranch(f.handle, tk.ID)
			write(t, filepath.Join(prs, strings.ReplaceAll(branch, "/", "_")), `[{"number":815,"state":"OPEN","headRefOid":"`+strings.Repeat("c", 40)+`","baseRefName":"main","isCrossRepository":false}]`)
			if name == "gh fails" {
				write(t, gh, "#!/bin/sh\necho \"$*\" >> "+calls+"\necho 'gh: HTTP 502' >&2\nexit 1\n")
			}
			write(t, filepath.Join(f.wt, "cmd", "root.go"), "package cmd\n// t20\n")
			tk = f.done(tk.ID)
			before, _ := os.ReadFile(ledgerPath(f.root, f.handle))
			res := Run(f.prOpts(tk, gh))
			after, _ := os.ReadFile(ledgerPath(f.root, f.handle))
			raw, _ := os.ReadFile(calls)
			switch {
			case name == "bound elsewhere" && (res.ReasonCode != ReasonPRBoundElsewhere || !strings.Contains(res.Detail, "#815") || !strings.Contains(res.Detail, "nothing is committed or pushed")):
				t.Fatalf("want %s before the commit, got %+v", ReasonPRBoundElsewhere, res)
			case name == "gh fails" && (!res.Deferred || res.ReasonCode != ReasonPRPending):
				t.Fatalf("want a %s deferral before the commit, got %+v", ReasonPRPending, res)
			case res.Commit != "" || res.Pushed || f.originBranch() != "":
				t.Fatalf("something was committed or pushed: %+v (origin %q)", res, f.originBranch())
			case string(before) != string(after):
				t.Fatal("the commit ledger changed")
			case strings.Contains(string(raw), "pr create"):
				t.Fatalf("a PR was opened:\n%s", raw)
			}
			if out, err := runGit(t, f.wt, "rev-parse", "--verify", "-q", commitRefName(f.handle, tk.ID)); err == nil && strings.TrimSpace(out) != "" {
				t.Fatalf("a harness commit was anchored: %s", out)
			}
			if got, _ := f.store.Get(other.ID); got.PRNumber != 815 || got.PRHead != strings.Repeat("c", 40) {
				t.Fatalf("t12's binding moved: %+v", got)
			}
		})
	}
}

// Rule 2: two consecutive tasks on one seat yield two branches and two PRs,
// each bound to its own task, each commit's parent the base tip at its
// dispatch (the base moved between them), never the seat's previous task;
// the ledger names the branch per commit. The worktree is on the task's
// branch during the lease and back on the resting seat branch after it.
// Fails when the step commits on one seat branch again.
func TestTwoTasksOnOneSeatGetTwoBranchesAndTwoPRs(t *testing.T) {
	f := newFixture(t)
	gh, _, calls := branchGh(t, f)
	t1 := f.leased("cmd/")
	if got := sh(t, f.wt, "symbolic-ref", "HEAD"); got != "refs/heads/"+TaskBranch(f.handle, t1.ID) {
		t.Fatalf("the leased worktree is on %s", got)
	}
	write(t, filepath.Join(f.wt, "cmd", "root.go"), "package cmd\n// one\n")
	first := Run(f.prOpts(f.done(t1.ID), gh))
	if !first.OK || first.Branch != "seat/s1-"+t1.ID || first.Parent != f.base || first.PRNumber != 1 {
		t.Fatalf("first task: %+v", first)
	}
	if got := sh(t, f.wt, "symbolic-ref", "HEAD"); got != "refs/heads/"+f.branch || sh(t, f.wt, "status", "--porcelain", "--untracked-files=all") != "" {
		t.Fatalf("after the commit the worktree is on %s, not clean on the seat branch", got)
	}
	if _, err := f.store.CompleteBound(t1.ID, fixtureRepo, first.PRNumber, first.Commit, f.now); err != nil {
		t.Fatal(err)
	}
	// The base moves (another PR merged) and a launch records it before the
	// next dispatch.
	write(t, filepath.Join(f.main, "README.md"), "readme, merged since\n")
	sh(t, f.main, "commit", "-q", "-am", "merged on main")
	tip := sh(t, f.main, "rev-parse", "main")
	f.setLaunchBase(tip)
	t2 := f.leased("internal/client/")
	if got := sh(t, f.wt, "rev-parse", "HEAD"); got != tip {
		t.Fatalf("the second lease starts at %s, not the base tip %s", got, tip)
	}
	write(t, filepath.Join(f.wt, "internal", "client", "factory_test.go"), "package client\n// two\n")
	second := Run(f.prOpts(f.done(t2.ID), gh))
	if !second.OK || second.Branch != "seat/s1-"+t2.ID || second.Parent != tip || second.PRNumber != 2 || len(second.Changes) != 1 {
		t.Fatalf("second task: %+v", second)
	}
	if got := sh(t, f.origin, "log", "-1", "--format=%P", second.Commit); got != tip {
		t.Fatalf("the second commit's parent is %s, not the base tip %s", got, tip)
	}
	if f.originRef(first.Branch) != first.Commit || f.originRef(second.Branch) != second.Commit {
		t.Fatalf("origin: %s", f.originBranch())
	}
	if _, err := runGit(t, f.origin, "merge-base", "--is-ancestor", first.Commit, second.Commit); err == nil {
		t.Fatal("the second task's branch carries the first task's commit")
	}
	l, _ := ReadLedger(f.root, f.handle)
	if len(l.Commits) != 2 || l.Commits[0].Branch != first.Branch || l.Commits[1].Branch != second.Branch || l.Commits[0].Base != f.base || l.Commits[1].Base != tip {
		t.Fatalf("ledger commits: %+v", l.Commits)
	}
	g1, _ := f.store.Get(t1.ID)
	g2, _ := f.store.Get(t2.ID)
	raw, _ := os.ReadFile(calls)
	if g1.PRNumber != 1 || g2.PRNumber != 2 || g2.PRHead != second.Commit || strings.Count(string(raw), "pr create") != 2 ||
		!strings.Contains(string(raw), "pr create --draft --head "+first.Branch) || !strings.Contains(string(raw), "pr create --draft --head "+second.Branch) {
		t.Fatalf("bindings %d %d\n%s", g1.PRNumber, g2.PRNumber, raw)
	}
}

// The first row of amendment 2c: a task whose PR exists from an earlier
// attempt starts its next lease on its branch at that attempt's commit,
// appends its commit there and binds the same PR again at the new head: no
// refusal, no second PR. Fails when the later attempt starts from the base
// or the PR is not bound again.
func TestLaterAttemptAppendsToItsOwnBranchAndPR(t *testing.T) {
	f := newFixture(t)
	gh, _, calls := branchGh(t, f)
	tk := f.leased("cmd/")
	write(t, filepath.Join(f.wt, "cmd", "root.go"), "package cmd\n// attempt one\n")
	first := Run(f.prOpts(f.done(tk.ID), gh))
	if !first.OK || first.PRNumber != 1 {
		t.Fatalf("first attempt: %+v", first)
	}
	// The lead sends the task back (the train's CI failed, say).
	if _, err := f.store.RefuseCommit(tk.ID, task.BlockVerdictFailed, "ci red", task.RouteLead, f.now); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.Reset(tk.ID, nil, f.now); err != nil {
		t.Fatal(err)
	}
	if _, err := CleanSeat(f.root, f.handle, gitBin(t), nil, f.trust(), f.now); err != nil {
		t.Fatal(err)
	}
	again, err := f.store.Claim(tk.ID, f.handle, nil, time.Hour, f.now)
	if err != nil {
		t.Fatal(err)
	}
	if err := StartTask(f.root, f.handle, gitBin(t), nil, again, f.now); err != nil {
		t.Fatal(err)
	}
	if raw, _ := os.ReadFile(filepath.Join(f.wt, "cmd", "root.go")); string(raw) != "package cmd\n// attempt one\n" || sh(t, f.wt, "rev-parse", "HEAD") != first.Commit {
		t.Fatalf("the later attempt does not start at the earlier commit: %q", raw)
	}
	write(t, filepath.Join(f.wt, "cmd", "flags.go"), "package cmd\n// attempt two\n")
	second := Run(f.prOpts(f.done(tk.ID), gh))
	if !second.OK || second.Branch != first.Branch || second.Parent != first.Commit || second.PRNumber != 1 || len(second.Changes) != 1 {
		t.Fatalf("second attempt: %+v", second)
	}
	got, _ := f.store.Get(tk.ID)
	raw, _ := os.ReadFile(calls)
	if f.originRef(first.Branch) != second.Commit || got.PRNumber != 1 || got.PRHead != second.Commit || strings.Count(string(raw), "pr create") != 1 {
		t.Fatalf("origin %s, binding #%d at %s\n%s", f.originRef(first.Branch), got.PRNumber, got.PRHead, raw)
	}
}

// The reviewer's rule on seat/<handle>-<task id>: the name is a label only,
// never parsed (a handle may carry a hyphen); the ledger's per-task record
// says which task a branch carries. On seat "qa-2" a branch the seat made
// that only looks like a task branch ("seat/qa-2-t9") is no task branch to
// CleanSeat, which refuses and touches nothing; the branch the ledger
// records for t1 is the task's, and the commit is recorded with it. Fails
// when CleanSeat goes by the name's prefix.
func TestTaskBranchIsALabelTheRecordDecides(t *testing.T) {
	f := newFixtureFor(t, "qa-2")
	gh, _, _ := branchGh(t, f)
	sh(t, f.wt, "checkout", "-q", "-b", "seat/qa-2-t9")
	write(t, filepath.Join(f.wt, "cmd", "root.go"), "package cmd\n// the seat's own\n")
	if _, err := CleanSeat(f.root, f.handle, gitBin(t), nil, f.trust(), f.now); err == nil || !strings.Contains(err.Error(), "task branch of the lease its commit ledger records") {
		t.Fatalf("a branch the ledger does not record was taken for a task branch: %v", err)
	}
	if raw, _ := os.ReadFile(filepath.Join(f.wt, "cmd", "root.go")); !strings.Contains(string(raw), "the seat's own") || sh(t, f.wt, "symbolic-ref", "HEAD") != "refs/heads/seat/qa-2-t9" {
		t.Fatal("CleanSeat touched a worktree on a branch the ledger does not record")
	}
	// The refused clean keeps the seat unclean; the next one takes the
	// marker over after its TTL.
	sh(t, f.wt, "checkout", "-q", "-f", f.branch)
	f.now = f.now.Add(task.SeatReservationTTL + time.Minute)
	tk := f.leased("cmd/")
	if got := sh(t, f.wt, "symbolic-ref", "HEAD"); got != "refs/heads/seat/qa-2-"+tk.ID {
		t.Fatalf("the leased worktree is on %s", got)
	}
	write(t, filepath.Join(f.wt, "cmd", "root.go"), "package cmd\n// qa-2\n")
	res := Run(f.prOpts(f.done(tk.ID), gh))
	l, _ := ReadLedger(f.root, f.handle)
	if !res.OK || res.Branch != "seat/qa-2-"+tk.ID || len(l.Commits) != 1 || l.Commits[0].Task != tk.ID || l.Commits[0].Branch != res.Branch || f.originRef(res.Branch) != res.Commit {
		t.Fatalf("the qa-2 task: %+v\nledger %+v", res, l.Commits)
	}
	// After the step the lease is over: t1's branch, though a commit names it,
	// is no lease of the seat, and CleanSeat refuses a worktree on it.
	if _, err := f.store.CompleteBound(tk.ID, fixtureRepo, res.PRNumber, res.Commit, f.now); err != nil {
		t.Fatal(err)
	}
	sh(t, f.wt, "checkout", "-q", res.Branch)
	f.now = f.now.Add(task.SeatReservationTTL + time.Minute)
	if _, err := CleanSeat(f.root, f.handle, gitBin(t), nil, f.trust(), f.now); err == nil || !strings.Contains(err.Error(), "task branch of the lease") {
		t.Fatalf("a branch with no lease behind it was taken for the lease's: %v", err)
	}
}

// setLaunchBase writes the launch's base tip into the seat's staged launch
// record (3.0.2 t22's "base"), as a relaunch does.
func (f *fixture) setLaunchBase(base string) {
	f.t.Helper()
	p := filepath.Join(launch.StagedDir(f.root, f.handle), launch.FileName)
	raw, err := os.ReadFile(p)
	if err != nil {
		f.t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		f.t.Fatal(err)
	}
	m["base"] = base
	if raw, err = json.Marshal(m); err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(p, raw, 0o600); err != nil {
		f.t.Fatal(err)
	}
}

// Amendment 2b, one base (the day-2 finding: a relaunch kept the ledger of
// the same worktree and branch, and CleanSeat reset the worktree to that
// ledger's old parent, undoing the launch's move to the tip): the lease
// boundary resets to the base the launch recorded on the launch record,
// never the ledger's first base (and not a local base branch that moved
// since), and the task's commit takes it as its base and parent. Fails when
// CleanSeat takes the ledger's parent or the local base branch.
func TestLeaseBoundaryResetsToTheLaunchRecordsBase(t *testing.T) {
	f := newFixture(t)
	for _, msg := range []string{"merged before the relaunch", "merged after it"} {
		write(t, filepath.Join(f.main, "README.md"), msg+"\n")
		sh(t, f.main, "commit", "-q", "-am", msg)
		if msg == "merged before the relaunch" {
			f.setLaunchBase(sh(t, f.main, "rev-parse", "main"))
		}
	}
	launched := sh(t, f.main, "rev-parse", "main~1")
	if _, err := CleanSeat(f.root, f.handle, gitBin(t), nil, f.trust(), f.now); err != nil {
		t.Fatal(err)
	}
	if got := sh(t, f.wt, "rev-parse", "HEAD"); got != launched {
		t.Fatalf("the lease boundary reset to %s, not the launch's base %s (ledger base %s)", got, launched, f.base)
	}
	tk := f.leased("cmd/")
	write(t, filepath.Join(f.wt, "cmd", "root.go"), "package cmd\n// on the launch base\n")
	res := f.run(f.done(tk.ID))
	l, _ := ReadLedger(f.root, f.handle)
	if !res.OK || res.Parent != launched || len(l.Commits) != 1 || l.Commits[0].Base != launched || l.Rest != launched {
		t.Fatalf("the task's base: %+v\nledger rest %s commits %+v", res, l.Rest, l.Commits)
	}
}

// The task branch seat/<handle>-<task id> sits beside the seat branch: a
// remote that still carries seat/s1 (the 3.0.1 seat branch, v31's
// origin/seat/dev under PR #815) takes seat/s1-t1 beside it, and seat/s1
// is left as it was. Fails when the task branch goes under seat/<handle>/.
func TestTaskBranchCoexistsWithTheSeatBranchOnTheRemote(t *testing.T) {
	f := newFixture(t)
	other := filepath.Join(f.dir, "other")
	sh(t, f.dir, "clone", "-q", f.origin, other)
	write(t, filepath.Join(other, "x.txt"), "the 3.0.1 seat branch\n")
	sh(t, other, "checkout", "-q", "-b", f.branch)
	sh(t, other, "add", "-A")
	sh(t, other, "commit", "-q", "-m", "a 3.0.1 seat commit")
	sh(t, other, "push", "-q", "origin", f.branch)
	old := f.originRef(f.branch)
	write(t, filepath.Join(f.wt, "cmd", "root.go"), "package cmd\n// beside\n")
	res := f.run(f.reported("cmd/"))
	if !res.OK || !res.Pushed || res.Branch != "seat/s1-t1" || f.originRef(res.Branch) != res.Commit || f.originRef(f.branch) != old {
		t.Fatalf("the task branch beside seat/s1: %+v (seat/s1 %s, was %s)", res, f.originRef(f.branch), old)
	}
}

// A launch record without "base" (a launch before 3.0.2): the worktree
// keeps the ledger's resting commit, and the step notes it; it never
// follows a local branch of the shared repository (the owner's local main
// moved ahead here, unpushed). Fails when the fallback reads the local base
// branch.
func TestRecordWithoutBaseKeepsTheRestingCommit(t *testing.T) {
	f := newFixture(t)
	write(t, filepath.Join(f.main, "README.md"), "an unpushed local commit\n")
	sh(t, f.main, "commit", "-q", "-am", "local only")
	tk := f.leased("cmd/")
	if got := sh(t, f.wt, "rev-parse", "HEAD"); got != f.base {
		t.Fatalf("the lease starts at %s, not the resting commit %s", got, f.base)
	}
	write(t, filepath.Join(f.wt, "cmd", "root.go"), "package cmd\n// no launch base\n")
	res := f.run(f.done(tk.ID))
	l, _ := ReadLedger(f.root, f.handle)
	if !res.OK || res.Parent != f.base || l.Rest != f.base || sh(t, f.wt, "rev-parse", "HEAD") != f.base || !strings.Contains(strings.Join(res.Notes, "\n"), "launch_base_missing") {
		t.Fatalf("no launch base: %+v (rest %s)", res, l.Rest)
	}
}
