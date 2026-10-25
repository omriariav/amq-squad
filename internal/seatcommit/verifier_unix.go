//go:build unix

package seatcommit

import (
	"os/exec"
	"syscall"
)

// ownGroup starts the verifier as the leader of its own process group, so
// the bound kills the whole group (rule 12).
func ownGroup(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true} }

// killGroup kills the verifier's process group.
func killGroup(cmd *exec.Cmd) {
	if cmd.Process != nil && cmd.Process.Pid > 0 {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}
