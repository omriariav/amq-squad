package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/procinfo"
)

// The lead's team mode starts and supervises its squad's scheduler process
// (plan 16.5, briefs ruling 15): `amq-squad lead --team --scheduler-seats
// FILE` runs `amq-squad scheduler run --seats FILE --lead <me>` as a child
// for as long as the lead runs, restarts it when it exits (with a backoff,
// giving up after repeated fast failures), and stops it when the lead ends.
// The scheduler process's own lock keeps it to one per squad.

// supervisor runs one child command and restarts it.
type supervisor struct {
	mu       sync.Mutex
	cancel   context.CancelFunc
	done     chan struct{}
	starts   int
	log      io.Writer
	command  func(ctx context.Context) *exec.Cmd
	backoff  time.Duration // first restart delay; doubles to maxDelay
	maxDelay time.Duration
	fastFail time.Duration // a run shorter than this is a fast failure
	maxFast  int           // fast failures in a row before giving up
}

func (s *supervisor) start() {
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel, s.done = cancel, make(chan struct{})
	go s.loop(ctx)
}

func (s *supervisor) loop(ctx context.Context) {
	defer close(s.done)
	delay, fast := s.backoff, 0
	for {
		cmd := s.command(ctx)
		cmd.Stdout, cmd.Stderr = s.log, s.log
		cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGTERM) }
		cmd.WaitDelay = 10 * time.Second
		began := time.Now()
		s.mu.Lock()
		s.starts++
		n := s.starts
		s.mu.Unlock()
		fmt.Fprintf(s.log, "lead: scheduler process start %d\n", n)
		err := cmd.Run()
		if ctx.Err() != nil {
			fmt.Fprintf(s.log, "lead: scheduler process stopped with the lead\n")
			return
		}
		if time.Since(began) < s.fastFail {
			fast++
		} else {
			fast, delay = 0, s.backoff
		}
		fmt.Fprintf(s.log, "lead: scheduler process exited (%v); restart in %s\n", err, delay)
		if fast >= s.maxFast {
			fmt.Fprintf(s.log, "lead: scheduler process failed %d times in a row; not restarted\n", fast)
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}
		if delay *= 2; delay > s.maxDelay {
			delay = s.maxDelay
		}
	}
}

// stop ends the child (SIGTERM, then kill after the wait delay) and waits.
func (s *supervisor) stop() {
	if s.cancel == nil {
		return
	}
	s.cancel()
	<-s.done
}

func (s *supervisor) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.starts
}

// leadPools are the lead's own pools for --lead-pool: one per model it may
// use, at its thinking level.
func leadPools(models []string, thinking string) []string {
	var out []string
	for _, m := range models {
		out = append(out, "pi:"+m+":"+thinking)
	}
	return out
}

// leadSchedulerCommand is the child the lead's team mode runs: `scheduler
// run` with the lead's handle and one --lead-pool per lead pool.
func leadSchedulerCommand(self, seats, lead string, pools []string) func(ctx context.Context) *exec.Cmd {
	return func(ctx context.Context) *exec.Cmd {
		args := []string{"scheduler", "run", "--seats", seats, "--lead", lead}
		for _, p := range pools {
			args = append(args, "--lead-pool", p)
		}
		return exec.CommandContext(ctx, self, args...)
	}
}

// startedByLeadTeam reports whether this process's parent is the lead's
// team mode (`amq-squad lead --team`), the only starter allowed to pass
// --lead-pool (narrow pass, R2 (b)).
var startedByLeadTeam = func() bool {
	return procinfo.Match(os.Getppid(), func(args string) bool {
		f := strings.Fields(args)
		return len(f) > 1 && filepath.Base(f[0]) == "amq-squad" && f[1] == "lead" && slices.Contains(f, "--team")
	})
}

// startLeadScheduler supervises `amq-squad scheduler run` for the lead;
// leadPools are the lead's own pools, which the router's concurrency caps
// count (a pi lead on the Ollama GPU).
func startLeadScheduler(seats, lead string, leadPools []string) (*supervisor, error) {
	self, err := os.Executable()
	if err != nil {
		return nil, err
	}
	logf, err := os.OpenFile(seats+".scheduler.log", os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	s := &supervisor{log: logf, backoff: time.Second, maxDelay: 30 * time.Second, fastFail: 10 * time.Second, maxFast: 5,
		command: leadSchedulerCommand(self, seats, lead, leadPools)}
	s.start()
	return s, nil
}
