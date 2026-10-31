package schedproc

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/launch"
	"github.com/omriariav/amq-squad/v3/internal/registry"
	"github.com/omriariav/amq-squad/v3/internal/scheduler"
	"github.com/omriariav/amq-squad/v3/internal/seatcommit"
	"github.com/omriariav/amq-squad/v3/internal/seatlaunch"
	"github.com/omriariav/amq-squad/v3/internal/task"
)

// seatBaseFixture is the squad fixture with a fake remote (a bare repository
// in the temp dir, the grant's push URL) holding main, and a second clone
// that moves the remote's main as another merge would.
func seatBaseFixture(t *testing.T) (q seatlaunch.Squad, remote, other string) {
	t.Helper()
	q = squadFixture(t)
	base := filepath.Dir(q.ProjectRoot)
	remote = filepath.Join(base, "remote.git")
	gitRun(t, base, "init", "-q", "--bare", "-b", "main", remote)
	gitRun(t, q.ProjectRoot, "push", "-q", remote, "main")
	other = filepath.Join(base, "other")
	gitRun(t, base, "clone", "-q", remote, other)
	return q, remote, other
}

func commitFile(t *testing.T, dir, name, body, msg string) string {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRun(t, dir, "add", "-A")
	gitRun(t, dir, "commit", "-q", "-m", msg)
	return gitRun(t, dir, "rev-parse", "HEAD")
}

var seatBasePool = task.Pool{Runtime: "claude", Model: "sonnet", Effort: "medium"}

// 3.0.2 t22: a relaunched seat whose branch and worktree sit on an old base
// (its first launch's base plus its own commit, the ledger's chain; the
// commit pushed and merged, so the remote seat branch is behind) gets
// its worktree and resting branch at the base branch's tip fetched from the
// grant's push URL (the local main is left behind), on the roster launch
// too, from another branch its worktree was left on; its next commit's
// parent is the tip, and the launch record carries the base.
func TestSeatBaseRelaunchAtTheTip(t *testing.T) {
	q, remote, other := seatBaseFixture(t)
	old := gitRun(t, q.ProjectRoot, "rev-parse", "HEAD")
	s := &Slots{Squad: q, Target: seatcommit.Target{PushURL: remote, BaseBranch: "main"}}
	first, err := s.Plan(scheduler.SeatRequest{ID: "r1", Pool: seatBasePool})
	if err != nil || first.Seats[0].Base != old {
		t.Fatalf("first launch: %+v %v", first.Seats, err)
	}
	wt := first.Seats[0].Worktree
	seatCommit := commitFile(t, wt, "t5.txt", "t5\n", "the seat's t5 commit")
	gitRun(t, wt, "push", "-q", remote, "seat/c1")   // pushed and merged, as a harness commit is
	gitRun(t, wt, "checkout", "-q", "-b", "task-t5") // left on another branch
	s.ReleaseRequest("r1")
	gitRun(t, other, "pull", "-q", "--no-rebase", "--no-edit", "origin", "seat/c1")
	tip := commitFile(t, other, "v3.txt", "two days of v3\n", "v3 moves")
	gitRun(t, other, "push", "-q", "origin", "main")

	got, err := s.PlanRoster([]scheduler.SeatRequest{{ID: "r2", Pool: seatBasePool, Handle: "c1"}})
	if err != nil {
		t.Fatal(err)
	}
	if got.Seats[0].Base != tip {
		t.Fatalf("the launch's base is %s, not the tip %s (old %s, seat commit %s)", got.Seats[0].Base, tip, old, seatCommit)
	}
	if h := gitRun(t, wt, "rev-parse", "HEAD"); h != tip {
		t.Fatalf("the worktree is at %s, not the tip %s", h, tip)
	}
	if b := gitRun(t, q.ProjectRoot, "rev-parse", "refs/heads/seat/c1"); b != tip {
		t.Fatalf("seat/c1 is at %s, not the tip", b)
	}
	if s := gitRun(t, wt, "symbolic-ref", "HEAD"); s != "refs/heads/seat/c1" {
		t.Fatalf("the worktree's HEAD names %s", s)
	}
	if raw, err := os.ReadFile(filepath.Join(wt, "v3.txt")); err != nil || string(raw) != "two days of v3\n" {
		t.Fatalf("the tip's file in the worktree: %q %v", raw, err)
	}
	if st := gitRun(t, wt, "status", "--porcelain"); st != "" {
		t.Fatalf("the moved worktree is not clean:\n%s", st)
	}
	commitFile(t, wt, "t12.txt", "harness\n", "the seat's next commit")
	if p := gitRun(t, wt, "rev-parse", "HEAD^"); p != tip {
		t.Fatalf("the next commit's parent is %s, not the tip %s", p, tip)
	}
	got.ClaudePins = map[string]registry.Pin{"c1": {Path: "/opt/pins/claude", SHA256: strings.Repeat("a", 64)}}
	c, err := seatlaunch.Compile(got, "", func(n string) (string, error) { return "/usr/bin/" + n, nil }, time.Unix(1, 0))
	if err != nil {
		t.Fatal(err)
	}
	if c.Records[0].Base != tip {
		t.Fatalf("the launch record's base is %q, not the tip %s", c.Records[0].Base, tip)
	}
	raw, err := launch.Marshal(c.Records[0])
	if err != nil || !strings.Contains(string(raw), `"base": "`+tip+`"`) {
		t.Fatalf("the launch record's JSON carries no \"base\" key with the tip: %v\n%s", err, raw)
	}
}

// With no push URL in the grant, the local base branch's tip is the base,
// and a worktree created at a launch starts on it with a stale seat branch
// reset to it.
func TestSeatBaseLocalBranchWithoutPushURL(t *testing.T) {
	q := squadFixture(t)
	old := gitRun(t, q.ProjectRoot, "rev-parse", "HEAD")
	gitRun(t, q.ProjectRoot, "branch", "seat/c1", old)
	tip := commitFile(t, q.ProjectRoot, "b.txt", "b\n", "main moves")
	s := &Slots{Squad: q}
	got, err := s.Plan(scheduler.SeatRequest{ID: "r1", Pool: seatBasePool})
	if err != nil {
		t.Fatal(err)
	}
	if got.Seats[0].Base != tip || gitRun(t, got.Seats[0].Worktree, "rev-parse", "HEAD") != tip {
		t.Fatalf("a new worktree on a stale seat branch: base %s, want %s", got.Seats[0].Base, tip)
	}
}

// A remote seat branch ahead of the tip and unmerged refuses the launch by
// name (the branch and its commits); nothing is moved. Once merged, the
// launch proceeds and resets the branch to the tip.
func TestSeatBaseRemoteSeatBranchAheadRefuses(t *testing.T) {
	q, remote, other := seatBaseFixture(t)
	s := &Slots{Squad: q, Target: seatcommit.Target{PushURL: remote, BaseBranch: "main"}}
	first, err := s.Plan(scheduler.SeatRequest{ID: "r1", Pool: seatBasePool})
	if err != nil {
		t.Fatal(err)
	}
	wt := first.Seats[0].Worktree
	pr := commitFile(t, wt, "pr.txt", "pr\n", "an open PR's commit")
	gitRun(t, wt, "push", "-q", remote, "seat/c1")
	s.ReleaseRequest("r1")
	_, err = s.Plan(scheduler.SeatRequest{ID: "r2", Pool: seatBasePool})
	if err == nil || !strings.Contains(err.Error(), ReasonSeatBranchUnmerged) || !strings.Contains(err.Error(), "seat/c1") || !strings.Contains(err.Error(), pr[:7]) {
		t.Fatalf("an unmerged remote seat branch: %v", err)
	}
	if h := gitRun(t, wt, "rev-parse", "HEAD"); h != pr {
		t.Fatalf("a refused launch moved the worktree to %s", h)
	}
	s.ReleaseRequest("r2")
	gitRun(t, other, "pull", "-q", "--no-rebase", "--no-edit", "origin", "seat/c1")
	gitRun(t, other, "push", "-q", "origin", "main")
	tip := commitFile(t, other, "c.txt", "c\n", "v3 moves on")
	gitRun(t, other, "push", "-q", "origin", "main")
	got, err := s.Plan(scheduler.SeatRequest{ID: "r3", Pool: seatBasePool})
	if err != nil || got.Seats[0].Base != tip || gitRun(t, wt, "rev-parse", "HEAD") != tip {
		t.Fatalf("a merged remote seat branch: %+v %v", got.Seats, err)
	}
}

// A local change the move to the tip would overwrite refuses the launch and
// is kept in the worktree (never lost without evidence).
func TestSeatBaseConflictingLocalChangeRefuses(t *testing.T) {
	q, remote, other := seatBaseFixture(t)
	s := &Slots{Squad: q, Target: seatcommit.Target{PushURL: remote, BaseBranch: "main"}}
	first, err := s.Plan(scheduler.SeatRequest{ID: "r1", Pool: seatBasePool})
	if err != nil {
		t.Fatal(err)
	}
	wt := first.Seats[0].Worktree
	s.ReleaseRequest("r1")
	commitFile(t, other, "a.txt", "upstream\n", "a changes upstream")
	gitRun(t, other, "push", "-q", "origin", "main")
	if err := os.WriteFile(filepath.Join(wt, "a.txt"), []byte("seat's leftover\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Plan(scheduler.SeatRequest{ID: "r2", Pool: seatBasePool}); err == nil || !strings.Contains(err.Error(), "to the base tip") {
		t.Fatalf("a conflicting local change: %v", err)
	}
	if raw, _ := os.ReadFile(filepath.Join(wt, "a.txt")); string(raw) != "seat's leftover\n" {
		t.Fatalf("the local change was lost: %q", raw)
	}
}

// fakeFetchGit is a git that records the argv of ls-remote and fetch (with
// whether GH_TOKEN reached it, never its value) and answers them for tip,
// passing every other call to the real git: an HTTPS push URL without the
// network.
func fakeFetchGit(t *testing.T, dir, tip string, fetched ...string) (bin, log string) {
	t.Helper()
	fetchTip := tip // what the fetch writes; another id fakes a move between the calls
	if len(fetched) > 0 {
		fetchTip = fetched[0]
	}
	real, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git is not on PATH")
	}
	bin, log = filepath.Join(dir, "fake-git"), filepath.Join(dir, "fetch.log")
	script := "#!/bin/sh\ncase \" $* \" in\n" +
		"*\" ls-remote \"*) echo \"argv: $* gh_token=${GH_TOKEN:+set} ssh_variant=${GIT_SSH_VARIANT:+set} ssh_extra=${GIT_SSH_EXTRA:+set} home=$HOME\" >> '" + log + "'; printf '%s\\trefs/heads/main\\n' " + tip + "; exit 0;;\n" +
		"*\" fetch \"*) echo \"argv: $* gh_token=${GH_TOKEN:+set}\" >> '" + log + "'; exec '" + real + "' update-ref refs/base " + fetchTip + ";;\n" +
		"esac\nexec '" + real + "' \"$@\"\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin, log
}

// 3.0.2 t22 (ruling 2026-10-31): an HTTPS push URL fetches the base tip with
// the commit step's push authentication: the pinned gh the launch recorded
// as the only credential helper on ls-remote and fetch, GH_TOKEN passed
// through, so a private repository launches.
func TestSeatBaseHTTPSFetchUsesThePinnedGh(t *testing.T) {
	realHome := os.Getenv("HOME")
	q := squadFixture(t)
	dir := filepath.Dir(q.ProjectRoot)
	tip := gitRun(t, q.ProjectRoot, "rev-parse", "HEAD")
	bin, log := fakeFetchGit(t, dir, tip)
	gh := filepath.Join(dir, "gh-pinned")
	if err := os.WriteFile(gh, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	sum, err := registry.FileSHA256(gh)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("GH_TOKEN", "fetch-canary-value")
	t.Setenv("GIT_SSH_VARIANT", "ssh") // on the push's list
	t.Setenv("GIT_SSH_EXTRA", "x")     // a GIT_SSH* name not on it
	s := &Slots{Squad: q, Git: bin, Target: seatcommit.Target{PushURL: "https://github.com/o/r", BaseBranch: "main", GhPath: gh, GhSHA256: sum}}
	got, err := s.Plan(scheduler.SeatRequest{ID: "r1", Pool: seatBasePool})
	if err != nil || got.Seats[0].Base != tip {
		t.Fatalf("an HTTPS launch: %+v %v", got.Seats, err)
	}
	raw, _ := os.ReadFile(log)
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	helper := "credential.helper=!'" + gh + "' auth git-credential"
	if len(lines) != 2 {
		t.Fatalf("want ls-remote and fetch, got %d calls", len(lines))
	}
	if home := os.Getenv("HOME"); home == realHome || !strings.HasSuffix(lines[0], " home="+home) {
		t.Fatal("the production git did not run with the fixture's temp HOME")
	}
	if !strings.Contains(lines[0], "ssh_variant=set") || strings.Contains(lines[0], "ssh_extra=set") {
		t.Fatalf("the fetch's environment is not the push's list: %s", lines[0][strings.Index(lines[0], "ssh_variant"):])
	}
	for i, l := range lines {
		if !strings.Contains(l, "-c credential.helper= -c "+helper) || !strings.Contains(l, "gh_token=set") || !strings.Contains(l, "https://github.com/o/r") {
			t.Fatalf("call %d ran without the pinned gh as the only helper or without GH_TOKEN", i)
		}
	}
}

// An HTTPS push URL with no pinned gh on the launch's record refuses with the
// pin-missing reason and never fetches anonymously.
func TestSeatBaseHTTPSWithoutPinnedGhRefuses(t *testing.T) {
	q := squadFixture(t)
	dir := filepath.Dir(q.ProjectRoot)
	bin, log := fakeFetchGit(t, dir, gitRun(t, q.ProjectRoot, "rev-parse", "HEAD"))
	s := &Slots{Squad: q, Git: bin, Target: seatcommit.Target{PushURL: "https://github.com/o/r", BaseBranch: "main"}}
	if _, err := s.Plan(scheduler.SeatRequest{ID: "r1", Pool: seatBasePool}); err == nil || !strings.Contains(err.Error(), registry.PinMissing) {
		t.Fatalf("no pinned gh: %v", err)
	}
	if raw, err := os.ReadFile(log); err == nil && len(raw) > 0 {
		t.Fatalf("an anonymous fetch ran:\n%s", raw)
	}
}

// Codex P1-1: a local commit on the resting branch that the tip does not
// hold (no remote seat branch) refuses the launch by name; the commit and
// its files survive.
func TestSeatBaseLocalUnpushedCommitRefuses(t *testing.T) {
	q, remote, other := seatBaseFixture(t)
	s := &Slots{Squad: q, Target: seatcommit.Target{PushURL: remote, BaseBranch: "main"}}
	first, err := s.Plan(scheduler.SeatRequest{ID: "r1", Pool: seatBasePool})
	if err != nil {
		t.Fatal(err)
	}
	wt := first.Seats[0].Worktree
	local := commitFile(t, wt, "local.txt", "unpushed\n", "a local-only commit")
	s.ReleaseRequest("r1")
	commitFile(t, other, "v3.txt", "v3\n", "v3 moves")
	gitRun(t, other, "push", "-q", "origin", "main")
	_, err = s.Plan(scheduler.SeatRequest{ID: "r2", Pool: seatBasePool})
	if err == nil || !strings.Contains(err.Error(), ReasonSeatBranchUnpushed) || !strings.Contains(err.Error(), "seat/c1") || !strings.Contains(err.Error(), local[:7]) {
		t.Fatalf("a local-only commit: %v", err)
	}
	if b := gitRun(t, q.ProjectRoot, "rev-parse", "refs/heads/seat/c1"); b != local {
		t.Fatalf("seat/c1 moved to %s", b)
	}
	if raw, err := os.ReadFile(filepath.Join(wt, "local.txt")); err != nil || string(raw) != "unpushed\n" {
		t.Fatalf("the commit's file: %q %v", raw, err)
	}
}

// Codex P1-1: every update-ref of the resting branch passes the expected old
// value, so a branch moved between the read and the update refuses instead of
// being overwritten, on a new worktree and on an existing one.
func TestSeatBaseConcurrentBranchMoveRefuses(t *testing.T) {
	q, remote, other := seatBaseFixture(t)
	real, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git is not on PATH")
	}
	dir := filepath.Dir(q.ProjectRoot)
	moved := commitFile(t, q.ProjectRoot, "side.txt", "side\n", "a commit the racer moves the branch to")
	racer := filepath.Join(dir, "racing-git")
	script := "#!/bin/sh\ncase \" $* \" in\n" +
		"*\"launch base c1\"*) '" + real + "' update-ref refs/heads/seat/c1 " + moved + ";;\n" +
		"*\"launch base p1\"*) '" + real + "' update-ref refs/heads/seat/p1 " + moved + ";;\n" +
		"esac\nexec '" + real + "' \"$@\"\n"
	if err := os.WriteFile(racer, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	s := &Slots{Squad: q, Target: seatcommit.Target{PushURL: remote, BaseBranch: "main"}}
	if _, err := s.Plan(scheduler.SeatRequest{ID: "r1", Pool: seatBasePool}); err != nil {
		t.Fatal(err)
	}
	s.ReleaseRequest("r1")
	commitFile(t, other, "v3.txt", "v3\n", "v3 moves")
	gitRun(t, other, "push", "-q", "origin", "main")
	s.Git = racer
	if _, err := s.Plan(scheduler.SeatRequest{ID: "r2", Pool: seatBasePool}); err == nil || !strings.Contains(err.Error(), "update-ref") {
		t.Fatalf("an existing worktree's branch moved mid-launch: %v", err)
	}
	if b := gitRun(t, q.ProjectRoot, "rev-parse", "refs/heads/seat/c1"); b != moved {
		t.Fatalf("the concurrent move was overwritten: seat/c1 at %s", b)
	}
	if _, err := s.Plan(scheduler.SeatRequest{ID: "r3", Pool: task.Pool{Runtime: "pi"}}); err == nil || !strings.Contains(err.Error(), "update-ref") {
		t.Fatalf("a new worktree's branch created mid-launch: %v", err)
	}
	if b := gitRun(t, q.ProjectRoot, "rev-parse", "refs/heads/seat/p1"); b != moved {
		t.Fatalf("the concurrent create was overwritten: seat/p1 at %s", b)
	}
}

// Codex P1-2: an existing worktree whose HEAD names nothing (unborn) refuses
// the launch by name; an uncommitted file is never reset over.
func TestSeatBaseUnbornHeadRefuses(t *testing.T) {
	q, remote, other := seatBaseFixture(t)
	s := &Slots{Squad: q, Target: seatcommit.Target{PushURL: remote, BaseBranch: "main"}}
	first, err := s.Plan(scheduler.SeatRequest{ID: "r1", Pool: seatBasePool})
	if err != nil {
		t.Fatal(err)
	}
	wt := first.Seats[0].Worktree
	s.ReleaseRequest("r1")
	commitFile(t, other, "v3.txt", "v3\n", "v3 moves")
	gitRun(t, other, "push", "-q", "origin", "main")
	gitRun(t, wt, "symbolic-ref", "HEAD", "refs/heads/nothing")
	if err := os.WriteFile(filepath.Join(wt, "a.txt"), []byte("uncommitted\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Plan(scheduler.SeatRequest{ID: "r2", Pool: seatBasePool}); err == nil || !strings.Contains(err.Error(), ReasonSeatHeadUnreadable) {
		t.Fatalf("an unborn HEAD: %v", err)
	}
	if raw, _ := os.ReadFile(filepath.Join(wt, "a.txt")); string(raw) != "uncommitted\n" {
		t.Fatalf("the uncommitted file was reset over: %q", raw)
	}
}

// Codex P2-4: a seat worktree that is a plain clone (its .git a directory)
// moves to the tip the same way: its own repository, its resting branch.
func TestSeatBasePlainCloneWorktree(t *testing.T) {
	q, remote, other := seatBaseFixture(t)
	wt := q.Seats[0].Worktree
	if err := os.MkdirAll(filepath.Dir(wt), 0o755); err != nil {
		t.Fatal(err)
	}
	gitRun(t, filepath.Dir(wt), "clone", "-q", remote, wt)
	tip := commitFile(t, other, "v3.txt", "v3\n", "v3 moves")
	gitRun(t, other, "push", "-q", "origin", "main")
	s := &Slots{Squad: q, Target: seatcommit.Target{PushURL: remote, BaseBranch: "main"}}
	got, err := s.Plan(scheduler.SeatRequest{ID: "r1", Pool: seatBasePool})
	if err != nil || got.Seats[0].Base != tip {
		t.Fatalf("a plain clone: %+v %v", got.Seats, err)
	}
	if h := gitRun(t, wt, "rev-parse", "HEAD"); h != tip {
		t.Fatalf("the clone is at %s, not the tip", h)
	}
	if b := gitRun(t, wt, "symbolic-ref", "HEAD"); b != "refs/heads/seat/c1" {
		t.Fatalf("the clone's HEAD names %s", b)
	}
	if raw, err := os.ReadFile(filepath.Join(wt, "v3.txt")); err != nil || string(raw) != "v3\n" {
		t.Fatalf("the tip's file: %q %v", raw, err)
	}
}

// Codex rule 6: a pinned gh whose sha256 does not match refuses with the pin
// mismatch reason before any network call.
func TestSeatBaseHTTPSPinMismatchRefuses(t *testing.T) {
	q := squadFixture(t)
	dir := filepath.Dir(q.ProjectRoot)
	bin, log := fakeFetchGit(t, dir, gitRun(t, q.ProjectRoot, "rev-parse", "HEAD"))
	gh := filepath.Join(dir, "gh-pinned")
	if err := os.WriteFile(gh, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	s := &Slots{Squad: q, Git: bin, Target: seatcommit.Target{PushURL: "https://github.com/o/r", BaseBranch: "main", GhPath: gh, GhSHA256: strings.Repeat("0", 64)}}
	if _, err := s.Plan(scheduler.SeatRequest{ID: "r1", Pool: seatBasePool}); err == nil || !strings.Contains(err.Error(), registry.PinMismatch) {
		t.Fatalf("a pinned gh that fails its hash: %v", err)
	}
	if raw, err := os.ReadFile(log); err == nil && len(raw) > 0 {
		t.Fatalf("a fetch ran with a gh that fails its pin:\n%s", raw)
	}
}

// Codex rule 6: the throwaway git directories are removed after a launch
// (the fetch's and the move's) and after a refused one (after the fetch, and
// in the move).
func TestSeatBaseShadowRemoved(t *testing.T) {
	q, remote, other := seatBaseFixture(t)
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	left := func(when string) {
		t.Helper()
		if m, _ := filepath.Glob(filepath.Join(tmp, "amq-squad-base-*")); len(m) != 0 {
			t.Fatalf("%s: throwaway git directories left: %d", when, len(m))
		}
	}
	s := &Slots{Squad: q, Target: seatcommit.Target{PushURL: remote, BaseBranch: "main"}}
	first, err := s.Plan(scheduler.SeatRequest{ID: "r1", Pool: seatBasePool})
	if err != nil {
		t.Fatal(err)
	}
	left("a first launch")
	wt := first.Seats[0].Worktree
	s.ReleaseRequest("r1")
	commitFile(t, other, "v3.txt", "v3\n", "v3 moves")
	gitRun(t, other, "push", "-q", "origin", "main")
	if _, err := s.Plan(scheduler.SeatRequest{ID: "r2", Pool: seatBasePool}); err != nil {
		t.Fatal(err)
	}
	left("a relaunch that moved the worktree")
	s.ReleaseRequest("r2")
	commitFile(t, other, "a.txt", "upstream\n", "a changes upstream")
	gitRun(t, other, "push", "-q", "origin", "main")
	if err := os.WriteFile(filepath.Join(wt, "a.txt"), []byte("leftover\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Plan(scheduler.SeatRequest{ID: "r3", Pool: seatBasePool}); err == nil {
		t.Fatal("a conflicting move launched")
	}
	left("a launch refused in the move")
	gitRun(t, wt, "checkout", "-q", "--", "a.txt")
	pr := commitFile(t, wt, "pr.txt", "pr\n", "an open PR's commit")
	gitRun(t, wt, "push", "-q", remote, "seat/c1")
	if _, err := s.Plan(scheduler.SeatRequest{ID: "r4", Pool: seatBasePool}); err == nil || !strings.Contains(err.Error(), pr[:7]) {
		t.Fatalf("an unmerged remote seat branch: %v", err)
	}
	left("a launch refused after the fetch")
}

// Codex rule 9: the fetched base must be the object id ls-remote reported
// for the base branch; a move between the two calls refuses the launch.
func TestSeatBaseFetchedTipMustMatchLsRemote(t *testing.T) {
	q := squadFixture(t)
	dir := filepath.Dir(q.ProjectRoot)
	reported := gitRun(t, q.ProjectRoot, "rev-parse", "HEAD")
	moved := commitFile(t, q.ProjectRoot, "b.txt", "b\n", "the branch moves between the calls")
	bin, _ := fakeFetchGit(t, dir, reported, moved)
	gh := filepath.Join(dir, "gh-pinned")
	if err := os.WriteFile(gh, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	sum, _ := registry.FileSHA256(gh)
	s := &Slots{Squad: q, Git: bin, Target: seatcommit.Target{PushURL: "https://github.com/o/r", BaseBranch: "main", GhPath: gh, GhSHA256: sum}}
	if _, err := s.Plan(scheduler.SeatRequest{ID: "r1", Pool: seatBasePool}); err == nil || !strings.Contains(err.Error(), ReasonBaseTipMoved) {
		t.Fatalf("a base that moved between ls-remote and fetch: %v", err)
	}
	if _, err := os.Stat(q.Seats[0].Worktree); !os.IsNotExist(err) {
		t.Fatalf("a refused launch created the worktree: %v", err)
	}
}

// Codex round 3, P1: a worktree whose HEAD is detached at a local-only commit
// refuses by name before any move; the commit and its files survive.
func TestSeatBaseDetachedLocalCommitRefuses(t *testing.T) {
	q, remote, other := seatBaseFixture(t)
	s := &Slots{Squad: q, Target: seatcommit.Target{PushURL: remote, BaseBranch: "main"}}
	first, err := s.Plan(scheduler.SeatRequest{ID: "r1", Pool: seatBasePool})
	if err != nil {
		t.Fatal(err)
	}
	wt := first.Seats[0].Worktree
	s.ReleaseRequest("r1")
	gitRun(t, wt, "checkout", "-q", "--detach")
	local := commitFile(t, wt, "detached.txt", "local\n", "a detached local commit")
	commitFile(t, other, "v3.txt", "v3\n", "v3 moves")
	gitRun(t, other, "push", "-q", "origin", "main")
	_, err = s.Plan(scheduler.SeatRequest{ID: "r2", Pool: seatBasePool})
	if err == nil || !strings.Contains(err.Error(), ReasonSeatHeadDetachedUnmerged) || !strings.Contains(err.Error(), local[:7]) {
		t.Fatalf("a detached local commit: %v", err)
	}
	if h := gitRun(t, wt, "rev-parse", "HEAD"); h != local {
		t.Fatalf("HEAD moved to %s", h)
	}
	if raw, err := os.ReadFile(filepath.Join(wt, "detached.txt")); err != nil || string(raw) != "local\n" {
		t.Fatalf("the commit's file: %q %v", raw, err)
	}
}
