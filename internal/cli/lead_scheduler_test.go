//go:build unix

package cli

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) { s.mu.Lock(); defer s.mu.Unlock(); return s.b.Write(p) }
func (s *syncBuf) String() string              { s.mu.Lock(); defer s.mu.Unlock(); return s.b.String() }

// Ruling 15: the lead's team mode supervises the scheduler process: started
// with the lead, restarted when it exits, stopped (and gone) when the lead
// ends.
func TestLeadSupervisesTheSchedulerProcess(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "pid")
	log := &syncBuf{}
	s := &supervisor{log: log, backoff: 20 * time.Millisecond, maxDelay: 50 * time.Millisecond, fastFail: time.Millisecond, maxFast: 100,
		command: func(ctx context.Context) *exec.Cmd {
			return exec.CommandContext(ctx, "sh", "-c", `echo $$ > "$1"; exec sleep 30`, "sh", pidFile)
		}}
	s.start()
	pid := waitPid(t, pidFile, 0)
	if err := syscall.Kill(pid, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	second := waitPid(t, pidFile, pid)
	if s.count() < 2 {
		t.Fatalf("not restarted: %d starts", s.count())
	}
	began := time.Now()
	s.stop()
	if took := time.Since(began); took > 5*time.Second {
		t.Fatalf("the scheduler process took %s to stop (SIGTERM, not the kill after the wait delay)", took)
	}
	if syscall.Kill(second, 0) == nil {
		t.Fatalf("the scheduler process %d outlived the lead", second)
	}
	if !strings.Contains(log.String(), "stopped with the lead") {
		t.Fatalf("log:\n%s", log.String())
	}
}

// Repeated fast failures stop the restarts.
func TestLeadSchedulerGivesUpOnFastFailures(t *testing.T) {
	log := &syncBuf{}
	s := &supervisor{log: log, backoff: time.Millisecond, maxDelay: time.Millisecond, fastFail: time.Hour, maxFast: 3,
		command: func(ctx context.Context) *exec.Cmd { return exec.CommandContext(ctx, "sh", "-c", "exit 1") }}
	s.start()
	select {
	case <-s.done:
	case <-time.After(10 * time.Second):
		t.Fatal("the supervisor never gave up")
	}
	if s.count() != 3 || !strings.Contains(log.String(), "not restarted") {
		t.Fatalf("%d starts\n%s", s.count(), log.String())
	}
}

func waitPid(t *testing.T, path string, not int) int {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if raw, err := os.ReadFile(path); err == nil {
			if pid, err := strconv.Atoi(strings.TrimSpace(string(raw))); err == nil && pid != not && syscall.Kill(pid, 0) == nil {
				return pid
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("the scheduler process did not start")
	return 0
}

// Brief 31-1, rows 6 and 8: --trust, --scheduler-seats and --lead are
// unknown flags on `lead`, and --trust, --seats and --lead on `scheduler
// run` (every launch carries the standing trust answer; squad.yaml is the
// one source of the seats and the lead's handle). The lead's scheduler
// child is `scheduler run --project <repo>` and nothing else.
func TestDeletedLaunchFlagsAreUnknown(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	for _, flag := range []string{"--trust", "--scheduler-seats", "--lead"} {
		args := []string{"--team", flag}
		if flag != "--trust" {
			args = append(args, "x")
		}
		if _, _, err := captureOutput(t, func() error { return runLead(args) }); err == nil || !strings.Contains(err.Error(), "flag provided but not defined: -"+strings.TrimPrefix(flag, "--")) {
			t.Fatalf("lead %s: %v", flag, err)
		}
	}
	for _, args := range [][]string{{"--project", "/p", "--trust"}, {"--seats", "/p/seats.json"}, {"--project", "/p", "--lead", "cto"}} {
		err := runSchedulerRun(context.Background(), args, io.Discard)
		if err == nil || !strings.Contains(err.Error(), "flag provided but not defined") {
			t.Fatalf("scheduler run %v: %v", args, err)
		}
	}
	cmd := leadSchedulerCommand("/bin/amq-squad", "/p", nil)(context.Background())
	if got := strings.Join(cmd.Args[1:], " "); got != "scheduler run --project /p" {
		t.Fatalf("the lead's scheduler child: %s", got)
	}
}

// t16 ruling 4: a scheduler process that exits refused (its roster launch
// refused at start) is not restarted; a crash still is.
func TestSupervisorDoesNotRestartARefusal(t *testing.T) {
	log := &syncBuf{}
	s := &supervisor{log: log, backoff: time.Millisecond, maxDelay: time.Millisecond, fastFail: time.Millisecond, maxFast: 100,
		command: func(ctx context.Context) *exec.Cmd {
			return exec.CommandContext(ctx, "sh", "-c", "exit "+strconv.Itoa(ExitSchedulerRefused))
		}}
	s.start()
	select {
	case <-s.done:
	case <-time.After(10 * time.Second):
		t.Fatal("the supervisor kept restarting a refusal")
	}
	if s.count() != 1 || !strings.Contains(log.String(), "refused to start") {
		t.Fatalf("%d starts\n%s", s.count(), log.String())
	}
}

// 3.0.1 t14: the lead's team mode starts the scheduler with no flag that
// turns the commit step's PR binding off (it binds by default under a
// merge grant).
func TestLeadSchedulerLeavesPRBindingOn(t *testing.T) {
	cmd := leadSchedulerCommand("/bin/amq-squad", "/p", []string{"pi:m:high"})(context.Background())
	for _, a := range cmd.Args {
		if a == "--no-open-pr" || strings.HasPrefix(a, "--open-pr=") || strings.HasPrefix(a, "--no-open-pr=") {
			t.Fatalf("the lead's scheduler command passes %s: %q", a, cmd.Args)
		}
	}
	if !slicesContains(cmd.Args, "--project") {
		t.Fatalf("args: %q", cmd.Args)
	}
}
