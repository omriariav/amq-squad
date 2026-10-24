//go:build unix

package registry

import (
	"errors"
	"os/exec"
	"syscall"
	"time"
)

// ownProcessGroup starts the probe's pi in its own process group, so the
// whole tree the model spawns can be killed before the verifier looks.
func ownProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return killProcessGroup(cmd) }
}

func killProcessGroup(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
}

// endProcessGroup kills what is left of the group and waits until no
// process of it remains (or the deadline passes, which is an error).
func endProcessGroup(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	pgid := cmd.Process.Pid
	_ = syscall.Kill(-pgid, syscall.SIGKILL)
	deadline := time.Now().Add(10 * time.Second)
	for {
		if err := syscall.Kill(-pgid, 0); errors.Is(err, syscall.ESRCH) {
			return nil
		}
		if time.Now().After(deadline) {
			return errors.New("the probe's process group did not exit")
		}
		time.Sleep(20 * time.Millisecond)
	}
}
