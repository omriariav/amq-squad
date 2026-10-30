package buzz

import (
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// treeGrace is how long a process tree has after TERM before KILL, and how
// long reap waits for the KILLed processes to be gone. It bounds cleanup
// only; it never fails a step.
const treeGrace = 3 * time.Second

// treeGuard makes the process tree a command starts end with the run. The
// root leads its own process group (Setpgid). When the command's context
// ends, Cancel records the root's descendants (from ps, while the root still
// links them) and the group, and sends them TERM. A pty child under
// script(1) starts its own session outside the group, so the descendants
// walk is what reaches it. After Run returns, reap KILLs what is left and
// waits until no recorded pid and no group member is alive, before any
// cleanup removes the scratch HOME.
type treeGuard struct {
	mu   sync.Mutex
	pgid int
	pids []int
}

func guardTree(cmd *exec.Cmd) *treeGuard {
	g := &treeGuard{}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		g.record(cmd.Process.Pid)
		g.signal(syscall.SIGTERM)
		return nil
	}
	cmd.WaitDelay = treeGrace // exec KILLs the root itself if it outlives TERM
	return g
}

// record adds root, its process group and every descendant of root alive
// now.
func (g *treeGuard) record(root int) {
	tree := descendants(root)
	g.mu.Lock()
	defer g.mu.Unlock()
	g.pgid = root
	for _, p := range append(tree, root) {
		if !slices.Contains(g.pids, p) {
			g.pids = append(g.pids, p)
		}
	}
}

// signal sends sig to every recorded pid, descendants before the root, and
// to the group.
func (g *treeGuard) signal(sig syscall.Signal) {
	g.mu.Lock()
	pids, pgid := slices.Clone(g.pids), g.pgid
	g.mu.Unlock()
	for _, p := range pids { // record appends the root after its descendants
		_ = syscall.Kill(p, sig)
	}
	if pgid > 0 {
		_ = syscall.Kill(-pgid, sig)
	}
}

// alive is the recorded pids still running, and whether the group still
// has a member.
func (g *treeGuard) alive() ([]int, bool) {
	g.mu.Lock()
	pids, pgid := slices.Clone(g.pids), g.pgid
	g.mu.Unlock()
	var out []int
	for _, p := range pids {
		if syscall.Kill(p, 0) == nil && !zombie(p) {
			out = append(out, p)
		}
	}
	group := pgid > 0 && syscall.Kill(-pgid, 0) == nil && groupHasLiving(pgid)
	return out, group
}

// reap ends a recorded tree: what is still alive gets TERM, then after
// treeGrace KILL, and reap waits until nothing recorded is alive. A
// survivor fails the test (Errorf: ptyRun may run off the test goroutine).
// It logs the check.
func (g *treeGuard) reap(t *testing.T, name string) {
	t.Helper()
	g.mu.Lock()
	recorded := len(g.pids)
	g.mu.Unlock()
	if recorded == 0 {
		return // never cancelled, nothing recorded
	}
	gone := func() bool { pids, group := g.alive(); return len(pids) == 0 && !group }
	waitGone := func() bool {
		for end := time.Now().Add(treeGrace); time.Now().Before(end); time.Sleep(50 * time.Millisecond) {
			if gone() {
				return true
			}
		}
		return gone()
	}
	for _, sig := range []syscall.Signal{syscall.SIGTERM, syscall.SIGKILL} {
		if waitGone() {
			break
		}
		g.signal(sig)
	}
	waitGone()
	pids, group := g.alive()
	g.mu.Lock()
	all, pgid := slices.Clone(g.pids), g.pgid
	g.mu.Unlock()
	if len(pids) > 0 || group {
		t.Errorf("hung: %s: processes survived cleanup: pids %v, group %d alive %v", name, pids, pgid, group)
		return
	}
	t.Logf("REAPED: %s: process group %d and pids %v (the root and its descendants when recorded), none alive", name, pgid, all)
}

// psRows is ps's pid, ppid, pgid and state for every process.
func psRows() [][4]string {
	out, err := exec.Command("ps", "-A", "-o", "pid=,ppid=,pgid=,stat=").Output()
	if err != nil {
		return nil
	}
	var rows [][4]string
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Fields(line)
		if len(f) >= 4 {
			rows = append(rows, [4]string{f[0], f[1], f[2], f[3]})
		}
	}
	return rows
}

// descendants is every process below root, from one ps snapshot.
func descendants(root int) []int {
	children := map[string][]string{}
	for _, r := range psRows() {
		children[r[1]] = append(children[r[1]], r[0])
	}
	var out []int
	queue := []string{strconv.Itoa(root)}
	for len(queue) > 0 {
		p := queue[0]
		queue = queue[1:]
		for _, c := range children[p] {
			n, _ := strconv.Atoi(c)
			out = append(out, n)
			queue = append(queue, c)
		}
	}
	return out
}

// zombie reports whether pid has exited and waits for its parent to reap
// it: it no longer runs.
func zombie(pid int) bool {
	for _, r := range psRows() {
		if r[0] == strconv.Itoa(pid) {
			return strings.HasPrefix(r[3], "Z")
		}
	}
	return true // not listed: gone
}

// groupHasLiving reports whether a process of group pgid other than a
// zombie is listed.
func groupHasLiving(pgid int) bool {
	for _, r := range psRows() {
		if r[2] == strconv.Itoa(pgid) && !strings.HasPrefix(r[3], "Z") {
			return true
		}
	}
	return false
}
