// Package panechain answers whether a process descends from a pane's shell:
// the parent-chain walk with the start stamp re-read at every hop
// (procinfo.ParentPID, procinfo.StartToken), so a hop replaced mid-walk
// breaks the chain. The scheduler's acceptance of the lead's decisions uses
// it (R4).
package panechain

import (
	"strconv"
	"strings"

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

// Seams for tests.
var (
	parentOf     = procinfo.ParentPID
	startTokenOf = procinfo.StartToken
)

// maxHops bounds the walk.
const maxHops = 64

// Reaches walks pid's parent chain (at most maxHops hops). It returns the
// pane whose shell the chain reaches (nil when none does) and whether the
// answer is known. Every hop reads the child's start stamp, its parent pid,
// the parent's start stamp, then the child's parent pid and stamp again
// (3.0.1 t16 round 7): the parent must have started no later than the child
// (a pid reused after the real parent exited belongs to a process started
// after the child it claims), the child's parent pid and stamp must be
// unchanged, and the next hop's stamp must be the one read for it as a
// parent. A pane's shell must carry the stamp the listing read, and the
// sender's own stamp is checked before and after the walk. A process that
// vanishes or changes mid-walk, a stamp that cannot be read or ordered, and
// a walk that runs out of hops before the root are unknown, never
// "outside".
func Reaches(pid int, start string, panes []Pane) (*Pane, bool) {
	shells := map[int]*Pane{}
	for i := range panes {
		shells[panes[i].Shell] = &panes[i]
	}
	if now, ok := startTokenOf(pid); !ok || now != start {
		return nil, false
	}
	p, expect := pid, start
	reachedRoot := false
	for i := 0; i < maxHops; i++ {
		if p <= 1 {
			reachedRoot = true
			break
		}
		stamp, ok := startTokenOf(p)
		if !ok || stamp != expect {
			return nil, false // the hop changed since it was read as a parent
		}
		if pn := shells[p]; pn != nil {
			if stamp != pn.Start {
				return nil, false // the pid of a pane shell, reused
			}
			if now, ok := startTokenOf(pid); !ok || now != start {
				return nil, false
			}
			return pn, true
		}
		parent, ok := parentOf(p)
		if !ok {
			return nil, false
		}
		if parent == p || parent <= 1 {
			if after, ok := startTokenOf(p); !ok || after != stamp {
				return nil, false
			}
			reachedRoot = true
			break
		}
		parentStamp, ok := startTokenOf(parent)
		if !ok {
			return nil, false
		}
		if order, ok := stampOrder(parentStamp, stamp); !ok || order > 0 {
			return nil, false // a parent started after its child: a reused pid
		}
		if again, ok := parentOf(p); !ok || again != parent {
			return nil, false // reparented mid-hop
		}
		if after, ok := startTokenOf(p); !ok || after != stamp {
			return nil, false
		}
		p, expect = parent, parentStamp
	}
	if !reachedRoot {
		return nil, false // the bound ran out before the chain ended: unknown
	}
	if now, ok := startTokenOf(pid); !ok || now != start {
		return nil, false
	}
	return nil, true
}

// stampOrder compares two raw start stamps of the same kind (procinfo:
// darwin-tv:SEC.USEC, linux-ticks:N): -1, 0 or 1 as a started before, with
// or after b; ok is false for stamps that cannot be read or are of
// different kinds.
func stampOrder(a, b string) (int, bool) {
	pa, oka := parseStamp(a)
	pb, okb := parseStamp(b)
	if !oka || !okb || pa.kind != pb.kind {
		return 0, false
	}
	for i := range pa.v {
		switch {
		case pa.v[i] < pb.v[i]:
			return -1, true
		case pa.v[i] > pb.v[i]:
			return 1, true
		}
	}
	return 0, true
}

type parsedStamp struct {
	kind string
	v    [2]uint64
}

func parseStamp(s string) (parsedStamp, bool) {
	if rest, ok := strings.CutPrefix(s, "linux-ticks:"); ok {
		n, err := strconv.ParseUint(rest, 10, 64)
		return parsedStamp{kind: "linux", v: [2]uint64{n, 0}}, err == nil
	}
	if rest, ok := strings.CutPrefix(s, "darwin-tv:"); ok {
		sec, usec, found := strings.Cut(rest, ".")
		if !found {
			return parsedStamp{}, false
		}
		a, err1 := strconv.ParseUint(sec, 10, 64)
		b, err2 := strconv.ParseUint(usec, 10, 64)
		return parsedStamp{kind: "darwin", v: [2]uint64{a, b}}, err1 == nil && err2 == nil && b < 1000000
	}
	return parsedStamp{}, false
}
