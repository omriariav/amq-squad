package seatlaunch

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/avivsinai/agent-message-queue/launchapi"

	"github.com/omriariav/amq-squad/v3/internal/claudeseat"
	"github.com/omriariav/amq-squad/v3/internal/launch"
	"github.com/omriariav/amq-squad/v3/internal/seatcommit"
)

func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@x.invalid", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@x.invalid")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// The launch records each git seat worktree's HEAD as the commit step's
// base before the seat runs (2b-2 milestone B); a non-git worktree records
// nothing. The role id travels in the staged record.
func TestLaunchRecordsTheCommitBase(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not on PATH")
	}
	q := testSquad(t)
	q.Seats[0].RoleName = "lead"
	cto := q.Seats[0].Worktree
	if err := os.MkdirAll(cto, 0o755); err != nil {
		t.Fatal(err)
	}
	gitIn(t, cto, "init", "-q", "-b", "seat/cto")
	if err := os.WriteFile(filepath.Join(cto, "a.txt"), []byte("a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, cto, "add", "-A")
	gitIn(t, cto, "commit", "-q", "-m", "base")
	head := gitIn(t, cto, "rev-parse", "HEAD")
	f := &fakeAMQ{t: t, prepare: preparedResult(), apply: launchapi.ApplyResultV1{ResultVersion: 1, Outcome: "applied", Disposition: "committed"}}
	o := launchOpts(t, f, map[string]string{"sha256:a1": "trust_exact_subject"})
	o.Wait = 50 * time.Millisecond
	first, err := Launch(context.Background(), q, o)
	if err != nil {
		t.Fatal(err)
	}
	l, err := seatcommit.ReadLedger(q.SessionRoot(), "cto")
	if err != nil || l.Base != head || l.Branch != "seat/cto" || l.Worktree != cto {
		t.Fatalf("cto ledger = %+v, %v", l, err)
	}
	if _, err := seatcommit.ReadLedger(q.SessionRoot(), "dev"); !os.IsNotExist(err) {
		t.Fatalf("a non-git worktree recorded a base: %v", err)
	}
	staged, _ := os.ReadFile(filepath.Join(launch.StagedDir(q.SessionRoot(), "cto"), launch.FileName))
	if !strings.Contains(string(staged), `"role": "lead"`) || !strings.Contains(string(staged), `"permission_mode": "dontAsk"`) || !strings.Contains(string(staged), `"credential_source": "owner token file"`) {
		t.Fatalf("staged record lacks the role id or the Claude permission mode:\n%s", staged)
	}
	if dev, _ := os.ReadFile(filepath.Join(launch.StagedDir(q.SessionRoot(), "dev"), launch.FileName)); strings.Contains(string(dev), "permission_mode") {
		t.Fatalf("a Codex record carries a Claude permission mode:\n%s", dev)
	}
	if f.applied == nil || !strings.Contains(strings.Join(f.applied.Prepare.Intent.Participants[1].Args, " "), "--permission-mode dontAsk") {
		t.Fatalf("the Claude provider arguments lack the permission mode (trust digest)")
	}
	second, err := Launch(context.Background(), q, o)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(o.Home, ".amq-squad", "preflight")); !os.IsNotExist(err) {
		t.Fatalf("the launch wrote a preflight copy: %v", err)
	}
	// Each launch writes the Claude seat its own config dir (0700, fresh);
	// the previous launch's dir is kept.
	for _, res := range []Result{first, second} {
		dir := claudeseat.Dir(q.SessionRoot(), "cto", res.LaunchID)
		st, err := os.Stat(dir)
		if err != nil || st.Mode().Perm() != 0o700 {
			t.Fatalf("config dir of launch %s: %v %v", res.LaunchID, st, err)
		}
	}
	if _, err := os.Stat(claudeseat.Dir(q.SessionRoot(), "dev", second.LaunchID)); !os.IsNotExist(err) {
		t.Fatalf("a Codex seat got a Claude config dir: %v", err)
	}
}
