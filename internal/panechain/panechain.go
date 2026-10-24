// Package panechain answers whether a process descends from an AMQ seat
// pane: the pane lookup of amendment 28's proof (a read-only `tmux
// list-panes -a` of the panes AMQ labelled @amq_pane_agent; R6: amq-squad
// never creates, moves or closes a pane) and the parent-chain walk with the
// start stamp re-read at every hop (procinfo.ParentPID, procinfo.StartToken),
// so a hop replaced mid-walk breaks the chain. The owner proof of seat clear
// and the scheduler's acceptance of the lead's decisions use it.
package panechain

import (
	"context"
	"errors"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/procinfo"
)

// Pane is one AMQ-labelled tmux pane: its shell pid, the shell's start
// stamp, the seat handle, and the pane's tty.
type Pane struct {
	Shell int
	Start string
	Agent string
	TTY   string
}

// ErrUnknown is a pane listing that could not be read (a tmux server that
// exists but does not answer, a pane whose shell stamp is unreadable): the
// answer is unknown, never "no panes".
var ErrUnknown = errors.New("the seat panes cannot be read")

// tmuxTimeout bounds the pane query (the third pass: a hung server is
// unknown, never a wait).
var tmuxTimeout = 2 * time.Second

// Seams for tests.
var (
	listPanes    = tmuxListPanes
	parentOf     = procinfo.ParentPID
	startTokenOf = procinfo.StartToken
)

// List returns every pane AMQ labelled with a seat handle. No tmux, or no
// tmux server running, is no pane; any other failure is ErrUnknown.
func List() ([]Pane, error) { return listPanes() }

func tmuxListPanes() ([]Pane, error) {
	tmux, err := exec.LookPath("tmux")
	if err != nil {
		return nil, nil
	}
	// A tmux server that does not answer within tmuxTimeout is unknown.
	ctx, cancel := context.WithTimeout(context.Background(), tmuxTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, tmux, "list-panes", "-a", "-F", "#{pane_pid} #{pane_tty} #{@amq_pane_agent}")
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		if ctx.Err() == nil && strings.Contains(stderr.String(), "no server running") {
			return nil, nil
		}
		return nil, ErrUnknown
	}
	var panes []Pane
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Fields(line)
		if len(f) != 3 {
			continue // an unlabelled pane
		}
		pid, err := strconv.Atoi(f[0])
		if err != nil || pid <= 1 {
			return nil, ErrUnknown
		}
		start, ok := startTokenOf(pid)
		if !ok {
			return nil, ErrUnknown
		}
		panes = append(panes, Pane{Shell: pid, Start: start, Agent: f[2], TTY: f[1]})
	}
	return panes, nil
}

// Reaches walks pid's parent chain (at most 64 hops). It returns the pane
// whose shell the chain reaches (nil when none does) and whether the answer
// is known: at every hop the start stamp is read before and after the
// parent, a pane's shell must carry the stamp the listing read, and the
// sender's own stamp is re-checked after the walk; a process that vanishes
// or changes mid-walk makes the answer unknown, never "outside".
func Reaches(pid int, start string, panes []Pane) (*Pane, bool) {
	shells := map[int]*Pane{}
	for i := range panes {
		shells[panes[i].Shell] = &panes[i]
	}
	p := pid
	for i := 0; i < 64 && p > 1; i++ {
		before, ok := startTokenOf(p)
		if !ok {
			return nil, false
		}
		if pn := shells[p]; pn != nil {
			if before != pn.Start {
				return nil, false // the pid of a pane shell, reused
			}
			if now, ok := startTokenOf(pid); !ok || now != start {
				return nil, false
			}
			return pn, true
		}
		next, ok := parentOf(p)
		after, ok2 := startTokenOf(p)
		if !ok || !ok2 || after != before {
			return nil, false
		}
		if next == p {
			break
		}
		p = next
	}
	if now, ok := startTokenOf(pid); !ok || now != start {
		return nil, false
	}
	return nil, true
}
