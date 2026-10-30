//go:build unix

package seatlaunch

import (
	"context"
	"net"
	"os/exec"
	"strconv"
	"strings"
	"time"

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

// paneLookupTimeout bounds the proof's tmux pane lookup (the decision socket's
// 2 s, internal/panechain); paneLookupWaitDelay bounds the output of a killed
// tmux whose descendant still holds it.
const (
	paneLookupTimeout   = 2 * time.Second
	paneLookupWaitDelay = 500 * time.Millisecond
)

// tmuxPaneShell finds the pane AMQ labelled with the handle (the
// @amq_pane_agent pane option AMQ sets) and returns its shell pid and that
// pid's start stamp, from a read-only `tmux list-panes -a` (R6: amq-squad
// never creates, moves or closes a pane). Several panes with the handle
// (another squad's seat of the same name) make the answer unknown.
func tmuxPaneShell(handle string, env []string) (int, string, bool) {
	// P2-6: bounded like the decision socket's lookup (internal/panechain): a
	// tmux server that does not answer in time is proof_origin_unknown.
	ctx, cancel := context.WithTimeout(context.Background(), paneLookupTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "tmux", "list-panes", "-a", "-F", "#{pane_pid} #{@amq_pane_agent}")
	cmd.WaitDelay = paneLookupWaitDelay
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

// paneIDOf is the tmux pane id whose shell is pid ("" when unknown), from a
// read-only list-panes (evidence beside the shell's pid and stamp).
func paneIDOf(shell int) string {
	if shell <= 0 {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), paneLookupTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "tmux", "list-panes", "-a", "-F", "#{pane_pid} #{pane_id}")
	cmd.WaitDelay = paneLookupWaitDelay
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Fields(line)
		if len(f) == 2 && f[0] == strconv.Itoa(shell) {
			return f[1]
		}
	}
	return ""
}
