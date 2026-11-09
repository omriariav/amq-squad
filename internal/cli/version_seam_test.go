package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/launch"
	"github.com/omriariav/amq-squad/v3/internal/sealchain"
	"github.com/omriariav/amq-squad/v3/internal/seatcommit"
	"github.com/omriariav/amq-squad/v3/internal/task"
)

func TestRunThreadsStampedVersionIntoLedgerWriters(t *testing.T) {
	oldSeat, oldSeal := seatcommit.BinaryVersion, sealchain.BinaryVersion
	t.Cleanup(func() {
		seatcommit.BinaryVersion = oldSeat
		sealchain.BinaryVersion = oldSeal
	})
	const stamped = "v3.2.0-rc.2"
	_ = Run([]string{"not-a-command"}, stamped)
	if seatcommit.BinaryVersion != stamped || sealchain.WriterVersion() != stamped {
		t.Fatalf("seatcommit=%q sealchain=%q; want %q", seatcommit.BinaryVersion, sealchain.WriterVersion(), stamped)
	}
}

func runVersionProbeCommand(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Dir = dir
	cmd.Env = os.Environ()
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// E11 builds the real entrypoint with its release ldflag, makes that binary
// take the public seat-commit base path, and inspects the raw seal it wrote.
func TestStampedMainBinaryWritesItsVersionInTheLedgerSeal(t *testing.T) {
	const stamped = "v8.7.6"
	bin := filepath.Join(t.TempDir(), "amq-squad")
	build := exec.Command("go", "build", "-ldflags=-X main.version="+stamped, "-o", bin, "../../cmd/amq-squad")
	build.Env = append(os.Environ(), "GOPROXY=off")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("real stamped build: %v\n%s", err, out)
	}

	repo := filepath.Join(t.TempDir(), "repo")
	if err := os.MkdirAll(filepath.Join(repo, "cmd"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "cmd", "root.go"), []byte("package cmd\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runVersionProbeCommand(t, repo, "git", "init", "-b", "main")
	runVersionProbeCommand(t, repo, "git", "add", ".")
	runVersionProbeCommand(t, repo, "git", "-c", "user.name=probe", "-c", "user.email=probe@example.invalid", "commit", "-m", "base")
	runVersionProbeCommand(t, repo, "git", "checkout", "-b", "seat/qa")
	base := runVersionProbeCommand(t, repo, "git", "rev-parse", "HEAD")

	root := t.TempDir()
	staged := launch.Record{Schema: launch.SchemaVersion, Handle: "qa", Root: root, Session: "probe", Runtime: "codex", Worktree: repo, Branch: "seat/qa", CWD: repo, Binary: bin, StartedAt: time.Now()}
	if err := os.MkdirAll(launch.StagedDir(root, "qa"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := launch.WriteFile(filepath.Join(launch.StagedDir(root, "qa"), launch.FileName), staged); err != nil {
		t.Fatal(err)
	}
	store, err := task.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	pool := task.Pool{Runtime: "codex", Model: "probe", Effort: "high"}
	tk, err := store.Add(task.AddInput{Title: "version probe", Class: "implement", DeclaredFiles: []string{"cmd/"}, Pool: &pool, ProjectPath: repo}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Grant(tk.ID, "qa", &pool, time.Hour, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Claim(tk.ID, "qa", nil, time.Hour, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Done(tk.ID, "qa", "version probe", "", time.Now()); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(bin, "seat", "commit", "--seat", "qa", "--root", root, "--base", base)
	for _, e := range os.Environ() {
		if !strings.HasPrefix(e, "AM_") && !strings.HasPrefix(e, "AMQ_") {
			cmd.Env = append(cmd.Env, e)
		}
	}
	// The later commit step may refuse the intentionally minimal fixture;
	// --base has already written the ledger whose seal is the evidence.
	_, _ = cmd.CombinedOutput()
	raw, err := os.ReadFile(filepath.Join(seatcommit.LedgerDir(root), "qa.json"))
	if err != nil {
		t.Fatal(err)
	}
	_, seal, err := sealchain.SplitRaw(raw)
	if err != nil || seal.Version != stamped {
		t.Fatalf("written seal = %+v, %v; file %s", seal, err, filepath.Join(seatcommit.LedgerDir(root), "qa.json"))
	}
}
