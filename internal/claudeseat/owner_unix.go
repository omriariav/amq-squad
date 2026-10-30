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

// probeGroup runs the mode probe as the leader of a new session (Setsid,
// the narrow pass's item 6): it has no controlling terminal to prompt on,
// and its process group (the session id) is killed whole.
func probeGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}

func killProbe(cmd *exec.Cmd) {
	if cmd.Process != nil && cmd.Process.Pid > 0 {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}
