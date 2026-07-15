package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/launch"
	"github.com/omriariav/amq-squad/v2/internal/team"
)

func TestRealPTYWaitPostureRefusesNamedGateBeforeTimeout(t *testing.T) {
	if testing.Short() {
		t.Skip("real tmux PTY coverage")
	}
	tmux, err := exec.LookPath("tmux")
	if err != nil {
		t.Skip("tmux is unavailable")
	}
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go is unavailable")
	}
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve repository source path")
	}
	repo := filepath.Clean(filepath.Join(filepath.Dir(source), "..", ".."))
	dir := t.TempDir()
	binary := filepath.Join(dir, "amq-squad")
	build := exec.Command(goBin, "build", "-o", binary, "./cmd/amq-squad")
	build.Dir = repo
	build.Env = os.Environ()
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build amq-squad: %v\n%s", err, out)
	}

	op := team.DefaultOperator()
	op.InteractionMode = team.OperatorInteractionLeadPane
	if err := team.Write(dir, team.Team{
		Project: dir, Orchestrated: true, Lead: "cto", ExecutionMode: executionModeProjectTeam, Operator: &op,
		Members: []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "s", CWD: dir}, {Role: "qa", Binary: "codex", Handle: "qa", Session: "s", CWD: dir}},
	}); err != nil {
		t.Fatal(err)
	}

	socket := fmt.Sprintf("a416-%d", os.Getpid())
	tmuxTmp, err := os.MkdirTemp("/private/tmp", "a416-tmux-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(tmuxTmp) })
	var tmuxEnv []string
	for _, item := range os.Environ() {
		if strings.HasPrefix(item, "TMUX=") || strings.HasPrefix(item, "TMUX_PANE=") || strings.HasPrefix(item, "TMUX_TMPDIR=") {
			continue
		}
		tmuxEnv = append(tmuxEnv, item)
	}
	tmuxEnv = append(tmuxEnv, "TMUX_TMPDIR="+tmuxTmp)
	tmuxRun := func(args ...string) ([]byte, error) {
		cmd := exec.Command(tmux, append([]string{"-L", socket}, args...)...)
		cmd.Dir = dir
		cmd.Env = tmuxEnv
		return cmd.CombinedOutput()
	}
	defer func() { _, _ = tmuxRun("kill-server") }()
	created, err := tmuxRun("new-session", "-d", "-P", "-F", "#{pane_id}\t#{pane_pid}", "-s", "wait-posture", "-c", dir)
	msg := strings.ToLower(string(created))
	if err != nil {
		msg += " " + strings.ToLower(err.Error())
	}
	if strings.Contains(msg, "operation not permitted") || strings.Contains(msg, "permission denied") {
		t.Skipf("tmux socket access unavailable: %v\n%s", err, created)
	}
	if err != nil {
		if os.Getenv("CODEX_SANDBOX") != "" {
			t.Skipf("tmux socket access unavailable: %v\n%s", err, created)
		}
		t.Fatalf("create tmux PTY: %v\n%s", err, created)
	}
	parts := strings.Split(strings.TrimSpace(string(created)), "\t")
	if len(parts) != 2 {
		if os.Getenv("CODEX_SANDBOX") != "" {
			t.Skipf("tmux socket access unavailable: invalid pane identity %q", created)
		}
		t.Fatalf("tmux pane identity = %q", created)
	}
	paneID := parts[0]
	panePID, err := strconv.Atoi(parts[1])
	if err != nil || panePID <= 0 {
		t.Fatalf("tmux pane pid = %q: %v", parts[1], err)
	}

	base := filepath.Join(dir, ".agent-mail")
	root := filepath.Join(base, "s")
	writeMemberLaunchRecord(t, base, "s", "cto", launch.Record{
		CWD: dir, Binary: "codex", Role: "cto", Handle: "cto", TeamProfile: team.DefaultProfile,
		Root: root, TeamHome: dir, AgentPID: panePID, External: true, AdoptionMode: adoptionModeExternalProjectLead,
		Tmux: &launch.TmuxInfo{PaneID: paneID, Session: "wait-posture"},
	})
	seedNotifyMessage(t, base, "s", "user", "new", notifyMsg{
		ID: "pty-gate", From: "cto", To: "user", Thread: "gate/pty", Subject: "APPROVAL: real PTY wait", Kind: "question", Created: time.Now().UTC(),
	})

	outputPath := filepath.Join(dir, "collect.out")
	donePath := filepath.Join(dir, "collect.done")
	command := fmt.Sprintf(
		"env -u AM_ME -u AM_ROOT -u AM_BASE_ROOT -u AM_SESSION %s collect --project %s --session s --me qa --timeout 30s --override-boundary --reason %s >%s 2>&1; code=$?; printf '%%s\\n' \"$code\" >%s",
		shellQuote(binary), shellQuote(dir), shellQuote("real PTY alternate mailbox"), shellQuote(outputPath), shellQuote(donePath),
	)
	if out, err := tmuxRun("send-keys", "-t", paneID, "-l", command); err != nil {
		t.Fatalf("stage PTY command: %v\n%s", err, out)
	}
	if out, err := tmuxRun("send-keys", "-t", paneID, "Enter"); err != nil {
		t.Fatalf("submit PTY command: %v\n%s", err, out)
	}

	started := time.Now()
	deadline := started.Add(5 * time.Second)
	for {
		if _, err := os.Stat(donePath); err == nil {
			break
		} else if !os.IsNotExist(err) {
			t.Fatal(err)
		}
		if time.Now().After(deadline) {
			pane, _ := tmuxRun("capture-pane", "-p", "-t", paneID, "-S", "-80")
			t.Fatalf("collect did not refuse before deadline; pane:\n%s", pane)
		}
		time.Sleep(25 * time.Millisecond)
	}
	code, err := os.ReadFile(donePath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(code)) == "0" {
		t.Fatal("guarded collect unexpectedly succeeded")
	}
	out, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	for _, want := range []string{"refusing collect before blocking", "gate/pty", "Park/end the turn"} {
		if !strings.Contains(got, want) {
			t.Fatalf("PTY output missing %q:\n%s", want, got)
		}
	}
	if elapsed := time.Since(started); elapsed >= 5*time.Second {
		t.Fatalf("named-gate refusal took %s, want fast pre-watch refusal", elapsed)
	}
}
