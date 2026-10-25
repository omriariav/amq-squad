//go:build unix

package cli

import (
	"os/exec"
	"syscall"
)

// guardGroup runs the guard child in its own process group, so the
// watchdog's kill reaches everything it started.
func guardGroup(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true} }

// killGuardGroup kills the guard child's process group.
func killGuardGroup(cmd *exec.Cmd) {
	if cmd.Process != nil && cmd.Process.Pid > 0 {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}
