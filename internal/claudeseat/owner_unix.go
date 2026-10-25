//go:build unix

package claudeseat

import (
	"os"
	"os/exec"
	"syscall"
)

func ownerUID(st os.FileInfo) (int, bool) {
	sys, ok := st.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, false
	}
	return int(sys.Uid), true
}

// probeGroup runs the mode probe in its own process group, killed whole.
func probeGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { killProbe(cmd); return nil }
}

func killProbe(cmd *exec.Cmd) {
	if cmd.Process != nil && cmd.Process.Pid > 0 {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}
