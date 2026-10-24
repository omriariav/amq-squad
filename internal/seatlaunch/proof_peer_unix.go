//go:build unix

package seatlaunch

import (
	"net"
	"os/exec"
	"strconv"
	"strings"

	"github.com/omriariav/amq-squad/v3/internal/procinfo"
)

// peerPID is the pid of the process on the other end of conn, from the
// kernel (LOCAL_PEERPID on darwin, SO_PEERCRED on Linux); known is false
// where the platform does not say.
func peerPID(conn *net.UnixConn) (pid int, known bool) {
	raw, err := conn.SyscallConn()
	if err != nil {
		return 0, false
	}
	raw.Control(func(fd uintptr) { pid, known = procinfo.SocketPeerPID(int(fd)) })
	return pid, known
}

// tmuxPaneShell finds the pane AMQ labelled with the handle (the
// @amq_pane_agent pane option AMQ sets) and returns its shell pid and that
// pid's start stamp, from a read-only `tmux list-panes -a` (R6: amq-squad
// never creates, moves or closes a pane). Several panes with the handle
// (another squad's seat of the same name) make the answer unknown.
func tmuxPaneShell(handle string, env []string) (int, string, bool) {
	cmd := exec.Command("tmux", "list-panes", "-a", "-F", "#{pane_pid} #{@amq_pane_agent}")
	if env != nil {
		cmd.Env = env
	}
	out, err := cmd.Output()
	if err != nil {
		return 0, "", false
	}
	found := 0
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Fields(line)
		if len(f) == 2 && f[1] == handle {
			if found != 0 {
				return 0, "", false
			}
			found, _ = strconv.Atoi(f[0])
		}
	}
	if found <= 0 {
		return 0, "", false
	}
	start, ok := procinfo.StartToken(found)
	return found, start, ok
}
