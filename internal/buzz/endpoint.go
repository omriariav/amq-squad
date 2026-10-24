package buzz

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Endpoint is the share's amq-remote endpoint. amq-remote serve publishes the
// body's kind 0 profile and kind 10100 status: online while it runs with the
// share's target attached, offline at a graceful stop (cmd/amq-remote
// relay_presence.go). Running it only while the bound session is present is
// how the presence the phone shows follows the status document.
type Endpoint interface {
	Running() bool
	Start() error
	Stop() error
}

// Supervisor applies a presence to the endpoint.
type Supervisor struct{ Endpoint Endpoint }

// Apply starts the endpoint when the session is present and it is not
// running, and stops it when the session is not present. It returns
// "started", "stopped" or "".
func (s Supervisor) Apply(p Presence) (string, error) {
	running := s.Endpoint.Running()
	switch {
	case p.Present && !running:
		return "started", s.Endpoint.Start()
	case !p.Present && running:
		return "stopped", s.Endpoint.Stop()
	}
	return "", nil
}

// ExecEndpoint runs `amq-remote up` for the share root as a child process.
// Its registry is under the share root, so it never touches the keepalive
// companion registry in ~/.amq (S1 finding). Its log is the share root's
// attach/up.log, mode 0600; amq-remote prints no key.
type ExecEndpoint struct {
	Bin      string // amq-remote, default from PATH
	Root     string // the share root
	StopWait time.Duration

	mu   sync.Mutex
	cmd  *exec.Cmd
	done chan struct{}
	err  error
}

func (e *ExecEndpoint) bin() string {
	if e.Bin != "" {
		return e.Bin
	}
	return "amq-remote"
}

// Running reports whether the child started here is alive.
func (e *ExecEndpoint) Running() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.done == nil {
		return false
	}
	select {
	case <-e.done:
		return false
	default:
		return true
	}
}

// Start launches `amq-remote up --root R --registry R/attach/keepalive-registry.json`.
func (e *ExecEndpoint) Start() error {
	if e.Running() {
		return nil
	}
	if !filepath.IsAbs(e.Root) {
		return errors.New("endpoint root must be absolute")
	}
	dir := filepath.Join(e.Root, "attach")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	// serve dates its offline strictly after its last status, which can be a
	// second ahead of the clock; a relay keeps the replaceable status with
	// the newest created_at, so an online from a restart in that second would
	// lose to it. Start waits out RestartGap after the last stop, whichever
	// process stopped it (the stamp is in the share root).
	if raw, err := os.ReadFile(filepath.Join(dir, "stopped-at")); err == nil {
		if at, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(string(raw))); err == nil {
			if wait := time.Until(at.Add(RestartGap)); wait > 0 && wait <= RestartGap {
				time.Sleep(wait)
			}
		}
	}
	log, err := os.OpenFile(filepath.Join(dir, "up.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	cmd := exec.Command(e.bin(), "up", "--root", e.Root, "--registry", filepath.Join(dir, "keepalive-registry.json"))
	cmd.Stdout, cmd.Stderr = log, log
	if err := cmd.Start(); err != nil {
		_ = log.Close()
		return fmt.Errorf("start amq-remote up: %w", err)
	}
	done := make(chan struct{})
	e.mu.Lock()
	e.cmd, e.done, e.err = cmd, done, nil
	e.mu.Unlock()
	go func() {
		err := cmd.Wait()
		_ = log.Close()
		e.mu.Lock()
		e.err = err
		e.mu.Unlock()
		close(done)
	}()
	return nil
}

// Stop asks the endpoint to shut down gracefully (SIGTERM; serve then
// publishes offline) and kills it after StopWait (default 15 s). Where the
// platform has no SIGTERM for a child (Windows), it kills it.
func (e *ExecEndpoint) Stop() error {
	e.mu.Lock()
	cmd, done := e.cmd, e.done
	e.mu.Unlock()
	if cmd == nil || done == nil {
		return nil
	}
	select {
	case <-done:
		return nil
	default:
	}
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		_ = cmd.Process.Kill()
	}
	wait := e.StopWait
	if wait <= 0 {
		wait = 15 * time.Second
	}
	defer func() {
		_ = os.WriteFile(filepath.Join(e.Root, "attach", "stopped-at"), []byte(time.Now().UTC().Format(time.RFC3339Nano)+"\n"), 0o600)
	}()
	select {
	case <-done:
		return nil
	case <-time.After(wait):
		_ = cmd.Process.Kill()
		<-done
		return errors.New("amq-remote up did not stop in time and was killed (no offline status published)")
	}
}

// RestartGap is the pause between a stop and the next start of the share's
// endpoint (see Start).
const RestartGap = 2 * time.Second
