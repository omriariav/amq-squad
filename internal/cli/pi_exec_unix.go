//go:build unix

package cli

import (
	"errors"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
)

// runProcess runs the embedded pi as a child and returns its exit code (a
// signal death returns 128 + the signal). The launcher stays the parent so
// it can consume pi's own exit codes (review 2, item 2 (c): exit 70 is the
// in-process runtime refusal). The child shares the terminal and the
// process group, so terminal signals (Ctrl-C in print mode, SIGWINCH) reach
// it directly; the parent catches SIGINT and SIGQUIT (caught signals reset
// to default in the child at exec, ignored ones would not) and forwards
// SIGTERM and SIGHUP.
func runProcess(path string, argv, env []string) (int, error) {
	cmd := &exec.Cmd{Path: path, Args: argv, Env: env, Stdin: os.Stdin, Stdout: os.Stdout, Stderr: os.Stderr}
	if piSpawnNonce != "" {
		// the lead's nonce for its pi (Codex's re-check on #837, item 1):
		// over an inherited pipe at fd 3, never the environment or a file;
		// the parent keeps neither end
		r, w, err := os.Pipe()
		if err != nil {
			return -1, err
		}
		_, werr := w.Write([]byte(piSpawnNonce + "\n"))
		w.Close()
		if werr != nil {
			r.Close()
			return -1, werr
		}
		cmd.ExtraFiles = []*os.File{r}
		defer r.Close()
	}
	sigs := make(chan os.Signal, 4)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGQUIT, syscall.SIGTERM, syscall.SIGHUP)
	defer signal.Stop(sigs)
	if err := cmd.Start(); err != nil {
		return -1, err
	}
	if piStartedHook != nil {
		// the solo lead records its pi (pid and start stamp) at the spawn
		piStartedHook(cmd.Process.Pid)
	}
	done := make(chan struct{})
	go func() {
		for {
			select {
			case s := <-sigs:
				if s == syscall.SIGTERM || s == syscall.SIGHUP {
					_ = cmd.Process.Signal(s)
				}
			case <-done:
				return
			}
		}
	}()
	err := cmd.Wait()
	close(done)
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		if ws, ok := ee.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
			return 128 + int(ws.Signal()), nil
		}
		return ee.ExitCode(), nil
	}
	if err != nil {
		return -1, err
	}
	return 0, nil
}
