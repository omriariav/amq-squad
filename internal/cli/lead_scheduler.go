package cli

import (
	"context"
	"errors"
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

	"github.com/omriariav/amq-squad/v3/internal/piruntime"
	"github.com/omriariav/amq-squad/v3/internal/procinfo"
	"github.com/omriariav/amq-squad/v3/internal/schedproc"
	"github.com/omriariav/amq-squad/v3/internal/team"
)

// The lead's team mode starts and supervises its squad's scheduler process
// (plan 16.5, briefs ruling 15; brief 31-1): `amq-squad lead --team` in a
// repository with a squad.yaml takes the root's lead lock (one lead per
// root: a second lead is refused lead_running), then runs `amq-squad
// scheduler run --project <repo>` as a child for as long as the lead runs,
// restarts it when it crashes (with a backoff, giving up after repeated fast
// failures; a refusal is not restarted), and stops it when the lead ends,
// on a non-zero exit too. squad.yaml is the one source of the squad: its
// seats, its session and its lead handle.

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
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() == ExitSchedulerRefused {
			// A refusal (3.0.1 t16: the roster launch refused at prepare or
			// apply, a decision the owner has to make; brief 31-1: another
			// scheduler holds the squad's lock) is not a crash: a restart
			// would be refused the same way.
			fmt.Fprintf(s.log, "lead: scheduler process refused to start (exit %d, the reason is above); not restarted\n", ExitSchedulerRefused)
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

// ExitSchedulerRefused is scheduler run's exit when its roster launch was
// refused at start (3.0.1 t16) or another scheduler holds the squad's lock
// (ErrRunning, brief 31-1): the lead's supervisor does not restart it.
const ExitSchedulerRefused = 17

// resolveTeamLeadIdentity reads the repository's squad.yaml, the file the
// supervised scheduler reads, and turns its lead handle and session into
// the pi lead's complete AMQ identity. The handle is squad.yaml's lead
// (brief 31-1: no --lead, no default). The rule (3.0.2 t19 fix): an identity
// the environment carries (AM_ME and AM_ROOT, the proved identity under an
// AMQ launch) must agree with squad.yaml's handle, root and session, else
// the lead refuses at start naming both; without one, squad.yaml fills it.
func resolveTeamLeadIdentity(project string) (piruntime.TeamIdentity, error) {
	s, err := team.LoadSquad(project)
	if err != nil {
		return piruntime.TeamIdentity{}, err
	}
	src := team.SquadPath(project)
	if strings.TrimSpace(s.Lead) == "" {
		return piruntime.TeamIdentity{}, fmt.Errorf("%s names no lead (lead: <seat handle>)", src)
	}
	if s.Session == "" {
		return piruntime.TeamIdentity{}, fmt.Errorf("%s names no session (session: <name>)", src)
	}
	handle := s.Lead
	if inherited := strings.TrimSpace(os.Getenv("AM_ME")); inherited != "" && inherited != handle {
		return piruntime.TeamIdentity{}, fmt.Errorf("%s's lead %q disagrees with AM_ME=%q", src, handle, inherited)
	}
	// scheduler run takes its root from seatlaunch.Squad.Normalize and
	// SessionRoot over the same project root, so both processes pin the same
	// root.
	identity, err := piruntime.NewTeamIdentity(handle, filepath.Join(project, ".agent-mail"), s.Session)
	if err != nil {
		return piruntime.TeamIdentity{}, err
	}
	if os.Getenv("AM_ME") != "" && os.Getenv("AM_ROOT") != "" {
		for _, c := range []struct{ name, want string }{
			{"AM_ROOT", identity.Root}, {"AM_BASE_ROOT", identity.BaseRoot}, {"AM_SESSION", identity.Session},
		} {
			got := os.Getenv(c.name)
			if got == "" {
				continue
			}
			if c.name != "AM_SESSION" {
				got = filepath.Clean(got)
			}
			if got != c.want {
				return piruntime.TeamIdentity{}, fmt.Errorf("the environment's %s=%q disagrees with %s (%q)", c.name, got, src, c.want)
			}
		}
	}
	return identity, nil
}

// leadSchedulerCommand is the child the lead's team mode runs: `scheduler
// run --project` over the repository's squad.yaml with one --lead-pool per
// lead pool. It passes no PR flag: the commit step binds by default under a
// merge grant (3.0.1 t14), and the lead never turns that off.
func leadSchedulerCommand(self, project string, pools []string) func(ctx context.Context) *exec.Cmd {
	return func(ctx context.Context) *exec.Cmd {
		args := []string{"scheduler", "run", "--project", project}
		for _, p := range pools {
			args = append(args, "--lead-pool", p)
		}
		return exec.CommandContext(ctx, self, args...)
	}
}

// leadStartEnv is the environment variable that carries the start stamp
// of a squad's launch (brief 31-1, the launch budget's start): set by the
// command the owner ran (`amq-squad up` in brief 31-1's second PR, else
// `lead --team` at its own start), inherited by the scheduler, which records
// it in its launch record.
const leadStartEnv = "AMQ_SQUAD_START"

// squadPresent reports whether the repository has a squad.yaml.
func squadPresent(project string) bool {
	_, err := os.Stat(team.SquadPath(project))
	return err == nil
}

// takeLeadLock takes the root's lead lock for this process (one lead per
// root, brief 31-1): a held lock is lead_running with the first lead's pid
// and pane.
var takeLeadLock = func(root string) (*schedproc.LeadLock, error) {
	pid := os.Getpid()
	start, _ := procinfo.StartToken(pid)
	return schedproc.AcquireLeadLock(root, schedproc.LeadRecord{PID: pid, Start: start, Pane: os.Getenv("TMUX_PANE")})
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
// count (a pi lead on the Ollama GPU). Its log is the scheduler directory's
// scheduler.log.
var startLeadScheduler = func(project, root string, leadPools []string) (*supervisor, error) {
	self, err := os.Executable()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(schedproc.Dir(root), 0o700); err != nil {
		return nil, err
	}
	logf, err := os.OpenFile(filepath.Join(schedproc.Dir(root), "scheduler.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	s := &supervisor{log: logf, backoff: time.Second, maxDelay: 30 * time.Second, fastFail: 10 * time.Second, maxFast: 5,
		command: leadSchedulerCommand(self, project, leadPools)}
	s.start()
	return s, nil
}
