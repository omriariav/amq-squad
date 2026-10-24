package seatcommit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/grants"
	"github.com/omriariav/amq-squad/v3/internal/task"
)

// Acceptance 7, row 1: a diff inside declared_files commits under the
// harness identity and pushes; the seat never committed.
func TestCommitInsideDeclaredFilesCommitsAndPushes(t *testing.T) {
	f := newFixture(t)
	write(t, filepath.Join(f.wt, "cmd", "root.go"), "package cmd\n\nfunc Root() {}\n")
	write(t, filepath.Join(f.wt, "cmd", "flags.go"), "package cmd\n")
	tk := f.reported("cmd/")
	res := f.run(tk)
	if !res.OK || res.Commit == "" || !res.Pushed {
		t.Fatalf("want a pushed harness commit, got %+v", res)
	}
	if got := f.originBranch(); got != res.Commit {
		t.Fatalf("origin %s = %q, want %s", f.branch, got, res.Commit)
	}
	if got := sh(t, f.wt, "log", "-1", "--format=%an <%ae>|%cn|%P", res.Commit); got != "amq-squad harness <harness@amq-squad.invalid>|amq-squad harness|"+f.base {
		t.Fatalf("commit identity and parent = %q", got)
	}
	msg := sh(t, f.wt, "log", "-1", "--format=%B", res.Commit)
	for _, want := range []string{"t1: the task", "Amq-Squad-Task: t1", "Amq-Squad-Seat: s1", "Amq-Squad-Declared-Files: " + res.DeclaredDigest, "Amq-Squad-Diff: " + res.DiffDigest} {
		if !strings.Contains(msg, want) {
			t.Fatalf("commit message lacks %q:\n%s", want, msg)
		}
	}
	if got := sh(t, f.wt, "rev-parse", "refs/heads/"+f.branch); got != res.Commit {
		t.Fatalf("the seat branch is at %s, want %s", got, res.Commit)
	}
	if st := sh(t, f.wt, "status", "--porcelain", "--untracked-files=all"); st != "" {
		t.Fatalf("worktree not clean after the harness commit: %q", st)
	}
	l, _ := ReadLedger(f.root, f.handle)
	if l.Head != res.Commit || len(l.Commits) != 1 || !l.Commits[0].Pushed {
		t.Fatalf("ledger = %+v", l)
	}
	if res.Files != 2 || res.Lines != 3 {
		t.Fatalf("size = %d lines, %d files", res.Lines, res.Files)
	}
}

// Acceptance 7, row 2 (the S6 W5 case): one file outside the declared set
// is refused with a reason code; nothing is committed or pushed.
func TestCommitOutsideDeclaredFilesIsRefused(t *testing.T) {
	f := newFixture(t)
	write(t, filepath.Join(f.wt, "cmd", "root.go"), "package cmd\n// rate limit\n")
	write(t, filepath.Join(f.wt, "internal", "client", "factory_test.go"), "package client\n// touched\n")
	tk := f.reported("cmd/root.go")
	res := f.run(tk)
	if res.OK || res.ReasonCode != ReasonOutside || res.Route != task.RouteLead {
		t.Fatalf("want %s routed to the lead, got %+v", ReasonOutside, res)
	}
	if !strings.Contains(res.Detail, "internal/client/factory_test.go") || strings.Contains(res.Detail, "cmd/root.go,") {
		t.Fatalf("detail should name only the outside file: %q", res.Detail)
	}
	if res.Commit != "" || f.originBranch() != "" {
		t.Fatalf("a refused diff was committed or pushed: %+v origin=%q", res, f.originBranch())
	}
	if l, _ := ReadLedger(f.root, f.handle); l.Head != "" {
		t.Fatalf("ledger moved on a refusal: %+v", l)
	}
}

// Acceptance 7, row 3: the refusal reaches the lead through the store; a
// lead widening inside the grant re-arms the commit, which then commits.
func TestLeadWideningCommits(t *testing.T) {
	f := newFixture(t)
	write(t, filepath.Join(f.wt, "cmd", "root.go"), "package cmd\n// rate limit\n")
	write(t, filepath.Join(f.wt, "internal", "client", "factory_test.go"), "package client\n// touched\n")
	tk := f.reported("cmd/root.go")
	res := f.run(tk)
	blocked, err := f.store.RefuseCommit(tk.ID, res.ReasonCode, res.Detail, res.Route, f.now)
	if err != nil || blocked.State != task.StateBlocked || blocked.BlockReason != ReasonOutside || blocked.BlockRoute != task.RouteLead {
		t.Fatalf("refusal record: %+v %v", blocked, err)
	}
	opts := f.opts(blocked)
	d := CheckWiden(WidenOptions{Root: f.root, Task: blocked, Added: []string{"internal/client/factory_test.go"}, Grants: f.grants, RepoRoot: f.main, Home: f.home, Commit: &opts})
	if !d.Allowed || len(d.Crossings) != 0 || len(d.Notes) != 0 {
		t.Fatalf("widening inside the grant: %+v", d)
	}
	rearmed, err := f.store.WidenGated(tk.ID, "lead", "the rate limit test lives there", []string{"internal/client/factory_test.go"}, d.Gates, time.Hour, f.now)
	if err != nil || rearmed.State != task.StateInProgress || rearmed.Report == nil {
		t.Fatalf("widen: %+v %v", rearmed, err)
	}
	res = f.run(rearmed)
	if !res.OK || !res.Pushed {
		t.Fatalf("after the widening: %+v", res)
	}
	if _, err := f.store.Complete(tk.ID, f.now); err != nil {
		t.Fatal(err)
	}
}

// Acceptance 7, row 4: a widening onto a protected path is refused by the
// core and names the growth-gate target; with the owner's gate accepted the
// same widening goes through and the commit step honors it.
func TestWideningOntoProtectedPathNeedsTheGrowthGate(t *testing.T) {
	f := newFixture(t)
	f.grants.ProtectedPaths = []string{".github/workflows/"}
	write(t, filepath.Join(f.wt, "cmd", "root.go"), "package cmd\n// ci\n")
	write(t, filepath.Join(f.wt, ".github", "workflows", "ci.yml"), "on: [push, pull_request]\n")
	tk := f.reported("cmd/")
	res := f.run(tk)
	if res.ReasonCode != ReasonProtected || !strings.Contains(res.Detail, ".github/workflows/ci.yml") {
		t.Fatalf("protected path change: %+v", res)
	}
	blocked, _ := f.store.RefuseCommit(tk.ID, res.ReasonCode, res.Detail, res.Route, f.now)
	widen := WidenOptions{Root: f.root, Task: blocked, Added: []string{".github/workflows/ci.yml"}, Grants: f.grants, RepoRoot: f.main, Home: f.home}
	d := CheckWiden(widen)
	want := ProtectedTarget(f.root, tk.ID, []string{".github/workflows/ci.yml"})
	if d.Allowed || d.ReasonCode != ReasonGrowthGate || len(d.Crossings) != 1 || d.Crossings[0].Target != want || d.Crossings[0].Accepted {
		t.Fatalf("want the growth gate for %q, got %+v", want, d)
	}
	if !strings.HasPrefix(want, "grow "+f.root+" task=t1 protected_paths=") {
		t.Fatalf("target %q", want)
	}
	// The owner accepts that exact gate.
	f.gates = fakeGates{want: true}
	widen.Gates = f.gates
	d = CheckWiden(widen)
	if !d.Allowed || len(d.Gates) != 1 || d.Gates[0] != want {
		t.Fatalf("with the gate accepted: %+v", d)
	}
	rearmed, err := f.store.WidenGated(tk.ID, "lead", "ci change approved by the owner", widen.Added, d.Gates, time.Hour, f.now)
	if err != nil {
		t.Fatal(err)
	}
	if res := f.run(rearmed); !res.OK || !res.Pushed {
		t.Fatalf("commit after the accepted gate: %+v", res)
	}
}

// A gate recorded on the scope change but not accepted (or for another
// task) never lets a protected path through.
func TestRecordedButUnacceptedGateDoesNotOpen(t *testing.T) {
	f := newFixture(t)
	f.grants.ProtectedPaths = []string{".github/workflows/"}
	write(t, filepath.Join(f.wt, ".github", "workflows", "ci.yml"), "on: [push]\n")
	tk := f.reported("cmd/")
	if _, err := f.store.RefuseCommit(tk.ID, ReasonProtected, "x", task.RouteLead, f.now); err != nil {
		t.Fatal(err)
	}
	target := ProtectedTarget(f.root, tk.ID, []string{".github/"})
	other := ProtectedTarget(f.root, "t9", []string{".github/"})
	rearmed, err := f.store.WidenGated(tk.ID, "lead", "forged", []string{".github/"}, []string{target, other}, time.Hour, f.now)
	if err != nil {
		t.Fatal(err)
	}
	f.gates = fakeGates{other: true}
	if res := f.run(rearmed); res.ReasonCode != ReasonProtected {
		t.Fatalf("an unaccepted gate opened a protected path: %+v", res)
	}
}

// Acceptance 7, row 5: a Codex seat under workspace-write cannot write .git;
// it only changes the tree, and the branch the remote gets is the
// harness's commit.
func TestCodexSeatEndsWithAHarnessPushedBranch(t *testing.T) {
	f := newFixture(t)
	f.writeRecord("codex")
	common := sh(t, f.wt, "rev-parse", "--path-format=absolute", "--git-common-dir")
	// The sandbox: .git is not writable while the seat works.
	if err := os.Chmod(common, 0o555); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(f.wt, "cmd", "root.go"), "package cmd\n// codex\n")
	if err := os.WriteFile(filepath.Join(common, "probe"), []byte("x"), 0o644); err == nil {
		t.Fatal("the simulated sandbox let the seat write .git")
	}
	if err := os.Chmod(common, 0o755); err != nil {
		t.Fatal(err)
	}
	res := f.run(f.reported("cmd/root.go"))
	if !res.OK || !res.Pushed || f.originBranch() != res.Commit {
		t.Fatalf("codex seat: %+v", res)
	}
	if got := sh(t, f.origin, "log", "-1", "--format=%an", res.Commit); got != DefaultIdentity.Name {
		t.Fatalf("pushed commit author %q", got)
	}
}

// The seat's own commits never enter the pushed history, and a file a
// seat commit added outside the set is still caught: the diff base is the
// core-recorded base, not the branch history.
func TestSeatCommitsAreNotTheBase(t *testing.T) {
	f := newFixture(t)
	write(t, filepath.Join(f.wt, "cmd", "root.go"), "package cmd\n// seat\n")
	sh(t, f.wt, "commit", "-q", "-am", "seat commit in scope")
	res := f.run(f.reported("cmd/root.go"))
	if !res.OK || res.Parent != f.base {
		t.Fatalf("in-scope seat commit: %+v", res)
	}
	if got := sh(t, f.origin, "rev-list", "--count", f.base+".."+res.Commit); got != "1" {
		t.Fatalf("pushed history carries %s commits past the base, want only the harness commit", got)
	}

	g := newFixture(t)
	write(t, filepath.Join(g.wt, "internal", "client", "factory_test.go"), "package client\n// sneaky\n")
	sh(t, g.wt, "commit", "-q", "-am", "seat commit out of scope")
	if res := g.run(g.reported("cmd/root.go")); res.ReasonCode != ReasonOutside {
		t.Fatalf("an out-of-scope file in a seat commit passed: %+v", res)
	}
}

// A seat that moves the shared base branch ref to include its own commit
// (everything under .git is same-UID writable for a Claude seat) changes
// nothing: the base comes from the core's ledger.
func TestMovedBaseRefDoesNotHideAChange(t *testing.T) {
	f := newFixture(t)
	write(t, filepath.Join(f.wt, "internal", "client", "factory_test.go"), "package client\n// sneaky\n")
	sh(t, f.wt, "commit", "-q", "-am", "out of scope")
	sh(t, f.wt, "update-ref", "refs/heads/main", "HEAD")
	if res := f.run(f.reported("cmd/root.go")); res.ReasonCode != ReasonOutside || res.Parent != f.base {
		t.Fatalf("a moved main ref hid an out-of-scope change: %+v", res)
	}
}

// The repository's hooks (or a seat-installed hook) never run in the
// harness's git: commit-tree, update-ref and push run with hooks off.
func TestHarnessGitRunsNoHooks(t *testing.T) {
	f := newFixture(t)
	common := sh(t, f.wt, "rev-parse", "--path-format=absolute", "--git-common-dir")
	marker := filepath.Join(f.dir, "hook-ran")
	hook := "#!/bin/sh\necho ran >> " + marker + "\n"
	for _, name := range []string{"reference-transaction", "pre-push", "pre-commit", "post-index-change"} {
		write(t, filepath.Join(common, "hooks", name), hook)
		if err := os.Chmod(filepath.Join(common, "hooks", name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write(t, filepath.Join(f.wt, "cmd", "root.go"), "package cmd\n// hooks\n")
	if res := f.run(f.reported("cmd/")); !res.OK || !res.Pushed {
		t.Fatalf("commit: %+v", res)
	}
	if _, err := os.Stat(marker); err == nil {
		raw, _ := os.ReadFile(marker)
		t.Fatalf("a repository hook ran in the harness's git: %q", raw)
	}
}

// Amendment 28: the owner's global agent config is diffed against the
// preflight copy at every seat exit; a change fails the task, routed to
// the owner, before anything is committed.
func TestGlobalConfigChangeFailsTheTask(t *testing.T) {
	f := newFixture(t)
	write(t, filepath.Join(f.home, ".claude", "settings.json"), `{"a":1}`)
	write(t, filepath.Join(f.home, ".amq-squad", "preflight", "claude-settings.json"), `{"a":1}`)
	write(t, filepath.Join(f.wt, "cmd", "root.go"), "package cmd\n// x\n")
	tk := f.reported("cmd/")
	write(t, filepath.Join(f.home, ".claude", "settings.json"), `{"a":1,"hooks":{}}`)
	res := f.run(tk)
	if res.ReasonCode != ReasonGlobalConfig || res.Route != task.RouteOwner || len(res.Config) != 1 || res.Commit != "" {
		t.Fatalf("want %s to the owner, got %+v", ReasonGlobalConfig, res)
	}
	if f.originBranch() != "" {
		t.Fatal("pushed despite a global config change")
	}
}

// Reviewer conditions on the global_config_changed block: it comes before
// the commit step; the seat's tree is kept on the ledger (tree, diff digest,
// the config diff as evidence), nothing is committed or pushed, and a re-run
// stays refused until the owner clears; after the clear exactly that tree
// commits, and a tree changed since is refused.
func TestGlobalConfigQuarantineKeepsTheTreeAndPushesNothing(t *testing.T) {
	f := newFixture(t)
	write(t, filepath.Join(f.home, ".claude", "settings.json"), `{"a":1}`)
	write(t, filepath.Join(f.home, ".amq-squad", "preflight", "claude-settings.json"), `{"a":1}`)
	write(t, filepath.Join(f.wt, "cmd", "root.go"), "package cmd\n// work\n")
	tk := f.reported("cmd/")
	write(t, filepath.Join(f.home, ".claude", "settings.json"), `{"a":2}`)
	res := f.run(tk)
	if res.ReasonCode != ReasonGlobalConfig || !res.Quarantine || res.Commit != "" || res.Tree == "" {
		t.Fatalf("quarantine: %+v", res)
	}
	l, err := ReadLedger(f.root, f.handle)
	if err != nil || l.Quarantine == nil || l.Quarantine.Tree != res.Tree || l.Quarantine.DiffDigest == "" || l.Quarantine.Task != tk.ID ||
		len(l.Quarantine.Config) != 1 || !strings.HasSuffix(l.Quarantine.Config[0].File, filepath.Join(".claude", "settings.json")) || l.Head != "" {
		t.Fatalf("ledger evidence: %+v %v", l.Quarantine, err)
	}
	if got := sh(t, f.wt, "rev-parse", "refs/heads/"+f.branch); got != f.base || f.originBranch() != "" {
		t.Fatalf("the seat branch moved (%s) or something was pushed (%q)", got, f.originBranch())
	}
	if got := sh(t, f.wt, "cat-file", "-t", l.Quarantine.Tree); got != "tree" {
		t.Fatalf("the kept tree is not in the object store: %q", got)
	}
	// The config is restored by the owner, but the seat stays quarantined
	// until the owner clears it.
	write(t, filepath.Join(f.home, ".claude", "settings.json"), `{"a":1}`)
	if res := f.run(tk); res.ReasonCode != ReasonGlobalConfig || !res.Quarantine || f.originBranch() != "" {
		t.Fatalf("an uncleared quarantine committed: %+v", res)
	}
	if _, err := ClearQuarantine(f.root, f.handle, "user", f.now); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(f.wt, "cmd", "root.go"), "package cmd\n// changed after the quarantine\n")
	if res := f.run(tk); res.ReasonCode != ReasonQuarantineTree {
		t.Fatalf("a tree changed after the quarantine: %+v", res)
	}
	write(t, filepath.Join(f.wt, "cmd", "root.go"), "package cmd\n// work\n")
	res = f.run(tk)
	if !res.OK || res.Tree != l.Quarantine.Tree || !res.Pushed {
		t.Fatalf("after the clear: %+v", res)
	}
	if l2, _ := ReadLedger(f.root, f.handle); l2.Quarantine != nil {
		t.Fatalf("the cleared quarantine stayed: %+v", l2.Quarantine)
	}
}

// codex-cli's own last_updated rewrite of its config is not a change; any
// other byte is.
func TestPreflightDiffIgnoresOnlyCodexSelfRewrite(t *testing.T) {
	f := newFixture(t)
	orig := "model = \"x\"\n[marketplaces.main]\nlast_updated = \"2026-10-01\"\nurl = \"u\"\n"
	write(t, filepath.Join(f.home, ".codex", "config.toml"), strings.Replace(orig, "2026-10-01", "2026-10-24", 1))
	write(t, filepath.Join(f.home, ".amq-squad", "preflight", "codex-config.toml"), orig)
	write(t, filepath.Join(f.wt, "cmd", "root.go"), "package cmd\n// y\n")
	if res := f.run(f.reported("cmd/")); !res.OK {
		t.Fatalf("codex's own rewrite failed the task: %+v", res)
	}
	write(t, filepath.Join(f.home, ".codex", "config.toml"), orig+"approval_policy = \"never\"\n")
	write(t, filepath.Join(f.wt, "cmd", "root.go"), "package cmd\n// z\n")
	if _, err := f.store.Complete("t1", f.now); err != nil {
		t.Fatal(err)
	}
	if res := f.run(f.reported("cmd/")); res.ReasonCode != ReasonGlobalConfig {
		t.Fatalf("a real codex config change passed: %+v", res)
	}
}

// The size cap: a diff over the grant's cap is refused; the check uses an
// explicit small cap (the default is unmeasured, ruling 5).
func TestSizeCapRefuses(t *testing.T) {
	f := newFixture(t)
	repo := repositoryOf(git{bin: gitBin(t), dir: f.wt, env: harnessEnv(os.Environ())}, "origin")
	f.grants.Merge = []grants.Merge{{Repository: repo, BaseBranch: "main", SizeCapLines: 3}}
	write(t, filepath.Join(f.wt, "cmd", "root.go"), "package cmd\n1\n2\n3\n4\n")
	res := f.run(f.reported("cmd/"))
	if res.ReasonCode != ReasonSizeCap || res.CapLines != 3 || res.Lines != 4 || !strings.Contains(res.Detail, "size_cap_lines: 4 changed lines over the cap of 3") || strings.Contains(res.Detail, "size_cap_files") {
		t.Fatalf("want %s (lines) at cap 3, got %+v", ReasonSizeCap, res)
	}
}

// The file half of the cap: more changed files than size_cap_files is the
// same reason code, naming the file half.
func TestSizeCapFilesRefuses(t *testing.T) {
	f := newFixture(t)
	repo := repositoryOf(git{bin: gitBin(t), dir: f.wt, env: harnessEnv(os.Environ())}, "origin")
	f.grants.Merge = []grants.Merge{{Repository: repo, BaseBranch: "main", SizeCapLines: 900, SizeCapFiles: 2}}
	for _, n := range []string{"a", "b", "c"} {
		write(t, filepath.Join(f.wt, "cmd", n+".go"), "package cmd\n")
	}
	res := f.run(f.reported("cmd/"))
	if res.ReasonCode != ReasonSizeCap || !strings.Contains(res.Detail, "size_cap_files: 3 changed files over the cap of 2") || strings.Contains(res.Detail, "size_cap_lines") {
		t.Fatalf("want %s (files) at cap 2, got %+v", ReasonSizeCap, res)
	}
}

// The managed block of CLAUDE.md is protected; the rest of the file is not.
func TestManagedBlockIsProtected(t *testing.T) {
	f := newFixture(t)
	write(t, filepath.Join(f.wt, "CLAUDE.md"), strings.Replace(claudeMD, "free text", "free text, edited", 1))
	res := f.run(f.reported("CLAUDE.md"))
	if !res.OK {
		t.Fatalf("an edit outside the managed block was refused: %+v", res)
	}
	if _, err := f.store.Complete("t1", f.now); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(f.wt, "CLAUDE.md"), strings.Replace(claudeMD, "managed rules", "rules I like", 1))
	if res := f.run(f.reported("CLAUDE.md")); res.ReasonCode != ReasonProtected || !strings.Contains(res.Detail, "managed block") {
		t.Fatalf("a managed block edit passed: %+v", res)
	}
}

// Protected paths fold case: .CLAUDE/Settings.json is the implicit
// .claude/settings*.json on a case-insensitive filesystem.
func TestProtectedPathsFoldCase(t *testing.T) {
	ps := protectedFor(grants.Defaults(), "/repo", "/home")
	for _, p := range []string{".claude/settings.json", ".CLAUDE/Settings.local.json", ".claude/hooks/x.sh", "SQUAD.yaml"} {
		if _, ok := ps.match(p); !ok {
			t.Errorf("%s is not protected", p)
		}
	}
	for _, p := range []string{"claude/settings.json", "docs/squad.yaml.md", "internal/a.go"} {
		if e, ok := ps.match(p); ok {
			t.Errorf("%s matched %s", p, e)
		}
	}
}

// A nested repository (a gitlink) is refused.
func TestNestedRepositoryIsRefused(t *testing.T) {
	f := newFixture(t)
	sub := filepath.Join(f.wt, "cmd", "sub")
	write(t, filepath.Join(sub, "x.go"), "package sub\n")
	sh(t, sub, "init", "-q")
	sh(t, sub, "add", "-A")
	sh(t, sub, "commit", "-q", "-m", "nested")
	if res := f.run(f.reported("cmd/")); res.ReasonCode != ReasonNested {
		t.Fatalf("a nested repository passed: %+v", res)
	}
}

// Deleting a declared file commits; deleting an undeclared file is refused.
func TestDeletionsAreChecked(t *testing.T) {
	f := newFixture(t)
	if err := os.Remove(filepath.Join(f.wt, "README.md")); err != nil {
		t.Fatal(err)
	}
	if res := f.run(f.reported("cmd/")); res.ReasonCode != ReasonOutside || !strings.Contains(res.Detail, "README.md") {
		t.Fatalf("an undeclared deletion passed: %+v", res)
	}
}

// A non-fast-forward push is refused, never forced.
func TestPushRejectedIsNeverForced(t *testing.T) {
	f := newFixture(t)
	other := filepath.Join(f.dir, "other")
	sh(t, f.dir, "clone", "-q", f.origin, other)
	write(t, filepath.Join(other, "x.txt"), "x\n")
	sh(t, other, "checkout", "-q", "-b", f.branch)
	sh(t, other, "add", "-A")
	sh(t, other, "commit", "-q", "-m", "diverged")
	sh(t, other, "push", "-q", "origin", f.branch)
	before := f.originBranch()
	write(t, filepath.Join(f.wt, "cmd", "root.go"), "package cmd\n// p\n")
	res := f.run(f.reported("cmd/"))
	if res.ReasonCode != ReasonPushRejected || f.originBranch() != before {
		t.Fatalf("want %s and the remote untouched, got %+v", ReasonPushRejected, res)
	}
}

// A re-run after a crash between the commit and task.Complete finds the
// recorded commit and makes no second one.
func TestRerunAfterCommitIsIdempotent(t *testing.T) {
	f := newFixture(t)
	write(t, filepath.Join(f.wt, "cmd", "root.go"), "package cmd\n// once\n")
	tk := f.reported("cmd/")
	first := f.run(tk)
	second := f.run(tk)
	if !first.OK || !second.OK || second.Commit != first.Commit {
		t.Fatalf("re-run: first %+v second %+v", first, second)
	}
	if l, _ := ReadLedger(f.root, f.handle); len(l.Commits) != 1 {
		t.Fatalf("ledger has %d commits", len(l.Commits))
	}
}

// A second task on the same seat diffs against the first task's harness
// commit, so the first task's files are not charged to the second.
func TestSecondTaskDiffsFromTheLastHarnessCommit(t *testing.T) {
	f := newFixture(t)
	write(t, filepath.Join(f.wt, "cmd", "root.go"), "package cmd\n// one\n")
	first := f.run(f.reported("cmd/"))
	if _, err := f.store.Complete("t1", f.now); err != nil || !first.OK {
		t.Fatal(err, first)
	}
	write(t, filepath.Join(f.wt, "internal", "client", "factory_test.go"), "package client\n// two\n")
	res := f.run(f.reported("internal/client/"))
	if !res.OK || res.Parent != first.Commit {
		t.Fatalf("second task: %+v", res)
	}
}

// The R19 live finding: after a refused task was reset, the seat's next
// lease inherited the refused diff and every later commit was refused for
// it. CleanSeat returns the worktree to the ledger parent before a new lease,
// keeping the discarded tree on the ledger; the next task then commits only
// its own change. A clean worktree is left alone.
func TestCleanSeatDiscardsALeftoverDiff(t *testing.T) {
	f := newFixture(t)
	if d, err := CleanSeat(f.root, f.handle, gitBin(t), nil, f.now); err != nil || d {
		t.Fatalf("a clean worktree: %v %v", d, err)
	}
	write(t, filepath.Join(f.wt, "README.md"), "outside\n")
	write(t, filepath.Join(f.wt, "stray.txt"), "untracked\n")
	sh(t, f.wt, "commit", "-q", "-am", "a seat commit")
	tk := f.reported("cmd/")
	res := f.run(tk)
	if res.ReasonCode != ReasonOutside {
		t.Fatalf("refusal: %+v", res)
	}
	if _, err := f.store.RefuseCommit(tk.ID, res.ReasonCode, res.Detail, res.Route, f.now); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.Reset(tk.ID, nil, f.now); err != nil {
		t.Fatal(err)
	}
	d, err := CleanSeat(f.root, f.handle, gitBin(t), nil, f.now)
	if err != nil || !d {
		t.Fatalf("clean: %v %v", d, err)
	}
	l, _ := ReadLedger(f.root, f.handle)
	if len(l.Discarded) != 1 || l.Discarded[0].Tree != res.Tree || l.Discarded[0].Parent != f.base || l.Discarded[0].DiffDigest == "" ||
		l.Discarded[0].TaskID != tk.ID || l.Discarded[0].Reason != "commit_refused: outside_declared_files" {
		t.Fatalf("ledger evidence: %+v (refused tree %s)", l.Discarded, res.Tree)
	}
	if st := sh(t, f.wt, "status", "--porcelain", "--untracked-files=all"); st != "" {
		t.Fatalf("worktree not clean: %q", st)
	}
	if head := sh(t, f.wt, "rev-parse", "HEAD"); head != f.base {
		t.Fatalf("the seat branch is at %s, not the parent %s", head, f.base)
	}
	write(t, filepath.Join(f.wt, "cmd", "root.go"), "package cmd\n// in scope\n")
	f.store.Claim(tk.ID, f.handle, nil, time.Hour, f.now)
	again, _ := f.store.Done(tk.ID, f.handle, "ok", "", f.now)
	if res := f.run(again); !res.OK {
		t.Fatalf("the next attempt was refused for the old diff: %+v", res)
	}
}

// CleanSeat runs only between leases: a seat that holds a lease (mid-task)
// or a refused report keeps its worktree untouched.
func TestCleanSeatNeverRunsMidTask(t *testing.T) {
	f := newFixture(t)
	tk, _ := f.store.Add(task.AddInput{Title: "mid", Class: "implement", DeclaredFiles: []string{"cmd/"}}, f.now)
	if _, err := f.store.Claim(tk.ID, f.handle, nil, time.Hour, f.now); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(f.wt, "cmd", "root.go"), "package cmd\n// work in progress\n")
	if d, err := CleanSeat(f.root, f.handle, gitBin(t), nil, f.now); err == nil || d || !strings.Contains(err.Error(), "holds task "+tk.ID) {
		t.Fatalf("mid-task clean: %v %v", d, err)
	}
	if raw, _ := os.ReadFile(filepath.Join(f.wt, "cmd", "root.go")); !strings.Contains(string(raw), "work in progress") {
		t.Fatal("a mid-task worktree was touched")
	}
}

// The PR binding for 3-2: with OpenPR the commit step opens the pushed seat
// branch's draft PR with gh and returns its number and the pushed head; a
// re-run finds the PR instead of opening a second one.
func TestCommitStepOpensAndBindsThePR(t *testing.T) {
	f := newFixture(t)
	calls := filepath.Join(f.dir, "gh-calls")
	gh := filepath.Join(f.dir, "gh")
	write(t, gh, "#!/bin/sh\necho \"$*\" >> "+calls+"\nif [ \"$2\" = view ]; then [ -f "+calls+".made ] && echo 42 && exit 0; exit 1; fi\ntouch "+calls+".made\necho https://github.com/o/r/pull/42\n")
	os.Chmod(gh, 0o755)
	write(t, filepath.Join(f.wt, "cmd", "root.go"), "package cmd\n// pr\n")
	tk := f.reported("cmd/")
	o := f.opts(tk)
	o.OpenPR, o.Gh = true, gh
	res := Run(o)
	if !res.OK || res.PRNumber != 42 || res.PRHead != res.Commit || res.Commit == "" {
		t.Fatalf("PR binding: %+v", res)
	}
	again := Run(o)
	raw, _ := os.ReadFile(calls)
	if again.PRNumber != 42 || strings.Count(string(raw), "pr create") != 1 || !strings.Contains(string(raw), "pr create --draft --head seat/s1 --base main") {
		t.Fatalf("re-run: %+v\n%s", again, raw)
	}
}

// No core-recorded base: refused, never inferred from the seat's refs.
func TestNoRecordedBaseIsRefused(t *testing.T) {
	f := newFixture(t)
	if err := os.Remove(ledgerPath(f.root, f.handle)); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(f.wt, "cmd", "root.go"), "package cmd\n// b\n")
	if res := f.run(f.reported("cmd/")); res.ReasonCode != ReasonBaseUnrecorded {
		t.Fatalf("got %+v", res)
	}
}

// The seat that holds or reported the task never widens its own set.
func TestSeatNeverWidensItsOwnSet(t *testing.T) {
	f := newFixture(t)
	tk := f.reported("cmd/")
	_, _ = f.store.RefuseCommit(tk.ID, ReasonOutside, "x", task.RouteLead, f.now)
	_, err := f.store.Widen(tk.ID, f.handle, "mine", []string{"README.md"}, 0, f.now)
	var refusal *task.WidenRefusal
	if err == nil || !asWiden(err, &refusal) || refusal.ReasonCode != task.RefusalSelfWiden {
		t.Fatalf("a seat widened its own set: %v", err)
	}
}

func asWiden(err error, out **task.WidenRefusal) bool {
	r, ok := err.(*task.WidenRefusal)
	*out = r
	return ok
}

// A file-lock conflict on a widening is a refusal for the lead.
func TestWideningFileLockConflict(t *testing.T) {
	f := newFixture(t)
	tk := f.reported("cmd/")
	_, _ = f.store.RefuseCommit(tk.ID, ReasonOutside, "x", task.RouteLead, f.now)
	other, _ := f.store.Add(task.AddInput{Title: "other", Class: "implement", DeclaredFiles: []string{"README.md"}}, f.now)
	if _, err := f.store.Grant(other.ID, "s2", nil, time.Hour, f.now); err != nil {
		t.Fatal(err)
	}
	_, err := f.store.Widen(tk.ID, "lead", "readme", []string{"README.md"}, 0, f.now)
	var refusal *task.WidenRefusal
	if !asWiden(err, &refusal) || refusal.ReasonCode != task.RefusalFileLock || refusal.Conflicts[0] != other.ID {
		t.Fatalf("want %s against %s, got %v", task.RefusalFileLock, other.ID, err)
	}
}
