package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/gatestore"
	"github.com/omriariav/amq-squad/v3/internal/launch"
	"github.com/omriariav/amq-squad/v3/internal/schedproc"
	"github.com/omriariav/amq-squad/v3/internal/seatcommit"
	"github.com/omriariav/amq-squad/v3/internal/task"
)

func gitT(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(sanitizedReviewEnv(os.Environ()), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.invalid", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.invalid")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// seatRepo makes a repository with a bare origin and a seat worktree on
// seat/s1, stages the seat's launch record under root and records its base.
// ownerGrant writes the owner's grants file with the merge grant of the
// test repository o/r (push_url the bare origin, acknowledged as local)
// under a test HOME; a HOME that is not a test directory is replaced first,
// so the owner's real ~/.amq-squad is never written.
func ownerGrant(t *testing.T, origin string) {
	t.Helper()
	home := os.Getenv("HOME")
	tmp, _ := filepath.EvalSymlinks(os.TempDir())
	if home == "" || !(strings.HasPrefix(home, os.TempDir()) || strings.HasPrefix(home, tmp)) {
		home, _ = filepath.EvalSymlinks(t.TempDir())
		t.Setenv("HOME", home)
	}
	dir := filepath.Join(home, ".amq-squad")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	os.Chmod(dir, 0o700)
	body := "schema: 1\nmerge:\n  - repository: o/r\n    base_branch: main\n    size_cap_lines: 300\n    push_url: " + origin + "\n    local_push_ack: true\n"
	if err := os.WriteFile(filepath.Join(dir, "grants.yaml"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func seatRepo(t *testing.T, root string) (repo, wt string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not on PATH")
	}
	dir, _ := filepath.EvalSymlinks(t.TempDir())
	repo, wt = filepath.Join(dir, "repo"), filepath.Join(dir, "wt-s1")
	origin := filepath.Join(dir, "origin.git")
	gitT(t, dir, "init", "-q", "--bare", "-b", "main", origin)
	gitT(t, origin, "config", "receive.autogc", "false")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	gitT(t, repo, "init", "-q", "-b", "main")
	mustWrite(t, filepath.Join(repo, "cmd", "root.go"), "package cmd\n")
	mustWrite(t, filepath.Join(repo, ".claude", "settings.json"), "{}\n")
	gitT(t, repo, "add", "-A")
	gitT(t, repo, "commit", "-q", "-m", "initial")
	gitT(t, repo, "remote", "add", "origin", origin)
	gitT(t, repo, "push", "-q", "origin", "main")
	gitT(t, repo, "worktree", "add", "-q", "-b", "seat/s1", wt, "main")
	staged := launch.StagedDir(root, "s1")
	if err := os.MkdirAll(staged, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := launch.WriteFile(filepath.Join(staged, launch.FileName), launch.Record{Handle: "s1", Root: root, Session: "s", Runtime: "claude", Worktree: wt, Branch: "seat/s1"}); err != nil {
		t.Fatal(err)
	}
	// The owner's grant names the bare origin as the acknowledged local push
	// target (D2); the launch records it, and the push never reads a URL
	// from the seat's repository (R2).
	ownerGrant(t, origin)
	if _, err := seatcommit.RecordWorktreeBase("", root, "s1", wt, "seat/s1", seatcommit.Target{Repository: "o/r", BaseBranch: "main", PushURL: origin, LocalPushAck: true}, time.Now()); err != nil {
		t.Fatal(err)
	}
	return repo, wt
}

// Acceptance 7 through the verbs: seat commit refuses a protected path
// (the implicit .claude/settings*.json) and records the refusal for the
// lead; task widen onto it is refused by the core, raises the growth gate
// in the gate store and routes the task to the owner; after the owner's
// signed answer the same widen goes through and seat commit pushes.
func TestSeatCommitAndWidenThroughTheGrowthGate(t *testing.T) {
	f := newGateCLIFixture(t)
	repo, wt := seatRepo(t, f.root)
	s, _ := task.Open(f.root)
	tk, err := s.Add(task.AddInput{Title: "tune settings", Class: "implement", DeclaredFiles: []string{"cmd/"}, ProjectPath: repo}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Claim(tk.ID, "s1", nil, time.Hour, time.Now()); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(wt, "cmd", "root.go"), "package cmd\n// tuned\n")
	mustWrite(t, filepath.Join(wt, ".claude", "settings.json"), `{"permissions":{"allow":["Bash(*)"]}}`+"\n")
	harnessPass(t, s, tk.ID, time.Now())
	if _, err := s.Done(tk.ID, "s1", "ok", "", time.Now()); err != nil {
		t.Fatal(err)
	}
	scoped := []string{"--project", f.project, "--session", "s"}
	_, _, err = captureOutput(t, func() error { return runSeatCommit(append([]string{"--seat", "s1"}, scoped...), os.Stdout) })
	if ExitCode(err) != ExitCommitRefused {
		t.Fatalf("seat commit: %v", err)
	}
	got, _ := s.Get(tk.ID)
	if got.State != task.StateBlocked || got.BlockReason != seatcommit.ReasonProtected || got.BlockRoute != task.RouteLead {
		t.Fatalf("after the refusal: %+v", got)
	}
	// The lead's decision reaches the scheduler as the lead's AMQ message (R1).
	sel := scopedSelection(t, scoped)
	req := schedproc.WidenRequest{Kind: schedproc.WidenRequestKind, Task: tk.ID, Files: []string{".claude/settings.json"}, Reason: "the seat needs the permission"}
	reply := leadDecision(t, sel, repo, req)
	if reply.OK || reply.Code != seatcommit.ReasonGrowthGate {
		t.Fatalf("widen onto a protected path: %+v", reply)
	}
	var env struct{ Data taskWidenData }
	env.Data = replyData(t, reply)
	if len(env.Data.GateIDs) != 1 {
		t.Fatalf("widen decision: %+v", env.Data)
	}
	target := seatcommit.ProtectedTarget(f.root, tk.ID, []string{".claude/settings.json"})
	store, _ := gatestore.Open(f.root)
	rec, err := store.Load(env.Data.GateIDs[0])
	if err != nil || rec.Class != "spawn" || rec.Target != target || rec.RaisedBy != "lead" {
		t.Fatalf("growth gate record = %+v, %v", rec, err)
	}
	got, _ = s.Get(tk.ID)
	if got.BlockRoute != task.RouteOwner || got.BlockReason != seatcommit.ReasonGrowthGate {
		t.Fatalf("the task did not go to the owner: %+v", got)
	}
	// The owner answers in the gate thread.
	q, _ := store.Question(rec.GateID)
	if q == nil {
		t.Fatal("no question posted")
	}
	tuple := "approve " + rec.GateID + " grow " + strings.TrimPrefix(rec.Digest, "sha256:")[:16]
	ev := f.answer(t, tuple, q.EventID)
	if _, _, err := captureOutput(t, func() error {
		return runGate(append([]string{"answer", "--from", "buzz", "--label", "nostr:" + ev}, scoped...))
	}); err != nil {
		t.Fatalf("gate answer: %v", err)
	}
	if reply := leadDecision(t, sel, repo, req); !reply.OK {
		t.Fatalf("widen after the accepted gate: %+v", reply)
	}
	got, _ = s.Get(tk.ID)
	if got.State != task.StateInProgress || len(got.ScopeChanges) != 1 || got.ScopeChanges[0].Gates[0] != target {
		t.Fatalf("after the widening: %+v", got)
	}
	harnessPass(t, s, tk.ID, time.Now()) // the re-armed attempt's own verdict
	if out, _, err := captureOutput(t, func() error { return runSeatCommit(append([]string{"--seat", "s1", "--json"}, scoped...), os.Stdout) }); err != nil {
		t.Fatalf("seat commit after the gate: %v\n%s", err, out)
	}
	if got, _ = s.Get(tk.ID); got.State != task.StateCompleted {
		t.Fatalf("task = %+v", got)
	}
}
