package panechain

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// fakeProcs sets the process table: parent by pid and start stamp by pid.
func fakeProcs(t *testing.T, parent map[int]int, stamp map[int]string) {
	t.Helper()
	prevP, prevS := parentOf, startTokenOf
	t.Cleanup(func() { parentOf, startTokenOf = prevP, prevS })
	parentOf = func(p int) (int, bool) { v, ok := parent[p]; return v, ok }
	startTokenOf = func(p int) (string, bool) { v, ok := stamp[p]; return v, ok }
}

// The chain walk: reaching a pane's shell names the pane; a shell pid whose
// stamp differs from the listing's (a reused pid), a hop that vanishes, or a
// sender whose stamp changed is unknown, never "outside".
func TestReachesWithStampsAtEveryHop(t *testing.T) {
	panes := []Pane{{Shell: 80, Start: tk(80), Agent: "lead"}}
	fakeProcs(t, map[int]int{100: 90, 90: 80, 80: 1}, map[int]string{100: tk(100), 90: tk(90), 80: tk(80), 1: tk(1)})
	if p, known := Reaches(100, tk(100), panes); !known || p == nil || p.Agent != "lead" {
		t.Fatalf("under the lead's pane: %+v %v", p, known)
	}
	if p, known := Reaches(100, tk(100), []Pane{{Shell: 70, Start: tk(70), Agent: "s1"}}); !known || p != nil {
		t.Fatalf("outside every pane: %+v %v", p, known)
	}
	if _, known := Reaches(100, "other", panes); known {
		t.Fatal("a changed sender stamp is known")
	}
	if _, known := Reaches(100, tk(100), []Pane{{Shell: 80, Start: "old", Agent: "lead"}}); known {
		t.Fatal("a reused shell pid is known")
	}
	fakeProcs(t, map[int]int{100: 90}, map[int]string{100: tk(100), 90: tk(90)})
	if _, known := Reaches(100, tk(100), panes); known {
		t.Fatal("a vanished hop is known")
	}
}

// tk is a Linux-style raw start stamp: a lower pid started earlier here.
func tk(n int) string { return "linux-ticks:" + strconv.Itoa(n*10) }

// 3.0.1 t16 round 7 (Codex, blocker): a parent pid reused by a process
// started after the child it claims is refused (unknown): the real parent
// exited and a lead descendant took its pid between hops. A child whose
// parent pid changes between the two reads of a hop (reparented) is
// unknown too, and a normal chain still passes. Reverted (only the child's
// stamp checked per hop), the reused pid splices the lead's ancestry onto a
// seat's process and the seat's decision is accepted as the lead's.
func TestReachesRefusesAReusedParentPid(t *testing.T) {
	lead := []Pane{{Shell: 80, Start: tk(80), Agent: "lead"}}
	// The seat process 100 (started at tick 1000) had parent 90; 90 exited
	// and the lead's descendant, started later (tick 2000), now holds pid 90
	// with the lead's shell 80 as its parent.
	fakeProcs(t, map[int]int{100: 90, 90: 80, 80: 1}, map[int]string{100: "linux-ticks:1000", 90: "linux-ticks:2000", 80: tk(80)})
	if p, known := Reaches(100, "linux-ticks:1000", lead); known || p != nil {
		t.Fatalf("a reused parent pid was followed: %+v %v", p, known)
	}
	// The same chain with the parent started first passes.
	fakeProcs(t, map[int]int{100: 90, 90: 80, 80: 1}, map[int]string{100: "linux-ticks:1000", 90: "linux-ticks:900", 80: tk(80)})
	if p, known := Reaches(100, "linux-ticks:1000", lead); !known || p == nil {
		t.Fatalf("a normal chain: %+v %v", p, known)
	}
	// Reparented between the hop's two reads of its parent pid.
	reads := 0
	prevP := parentOf
	parentOf = func(p int) (int, bool) {
		if p == 100 {
			reads++
			if reads > 1 {
				return 1, true
			}
		}
		return prevP(p)
	}
	if _, known := Reaches(100, "linux-ticks:1000", lead); known {
		t.Fatal("a child reparented mid-hop is known")
	}
}

// 3.0.1 t16 round 7 (Codex 8): a walk that runs out of its 64 hops before
// the chain ends is unknown (origin_unknown), never "outside" (not_lead).
func TestReachesBoundIsUnknown(t *testing.T) {
	parent, stamp := map[int]int{}, map[int]string{}
	for p := 1000; p > 900; p-- {
		parent[p], stamp[p] = p-1, tk(p)
	}
	fakeProcs(t, parent, stamp)
	if p, known := Reaches(1000, tk(1000), []Pane{{Shell: 5, Start: tk(5), Agent: "lead"}}); known || p != nil {
		t.Fatalf("an exhausted walk: %+v %v", p, known)
	}
}

func TestStampOrder(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want int
		ok   bool
	}{
		{"linux-ticks:5", "linux-ticks:7", -1, true},
		{"darwin-tv:10.9", "darwin-tv:10.10", -1, true}, // microseconds are numbers, not text
		{"darwin-tv:11.0", "darwin-tv:10.999999", 1, true},
		{"darwin-tv:10.5", "darwin-tv:10.5", 0, true},
		{"darwin-tv:10.5", "linux-ticks:5", 0, false},
		{"s80", "s90", 0, false},
	} {
		if got, ok := stampOrder(c.a, c.b); got != c.want || ok != c.ok {
			t.Errorf("stampOrder(%q, %q) = %d %v, want %d %v", c.a, c.b, got, ok, c.want, c.ok)
		}
	}
}

// The listing: labelled panes only; no tmux server is no pane; a server
// that does not answer is ErrUnknown.
func TestListReadsOnlyLabelledPanesAndRefusesAnUnreadableServer(t *testing.T) {
	dir := t.TempDir()
	tmux := filepath.Join(dir, "tmux")
	t.Setenv("PATH", dir)
	prevTimeout := tmuxTimeout
	tmuxTimeout = 20 * time.Second // a freshly written fake tmux can take a while to start on a busy host
	t.Cleanup(func() { tmuxTimeout = prevTimeout })
	fakeProcs(t, nil, map[int]string{4242: "s4242", 4343: "s4343"})
	write := func(body string) {
		if err := os.WriteFile(tmux, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write("echo '4242 /dev/ttys004 lead'\necho '4343 /dev/ttys005'\n")
	panes, err := tmuxListPanes()
	if err != nil || len(panes) != 1 || panes[0].Shell != 4242 || panes[0].Agent != "lead" || panes[0].TTY != "/dev/ttys004" || panes[0].Start != "s4242" {
		t.Fatalf("panes: %+v %v", panes, err)
	}
	write("echo 'no server running on /tmp/tmux-501/default' >&2\nexit 1\n")
	if panes, err := tmuxListPanes(); err != nil || len(panes) != 0 {
		t.Fatalf("no server: %+v %v", panes, err)
	}
	write("echo 'error connecting to /tmp/tmux-501/default (Permission denied)' >&2\nexit 1\n")
	if _, err := tmuxListPanes(); !errors.Is(err, ErrUnknown) {
		t.Fatalf("an unreadable server: %v", err)
	}
}

// The pane query is bounded (2 s): a tmux server that hangs is ErrUnknown.
func TestListTimesOutOnAHungServer(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "tmux"), []byte("#!/bin/sh\nexec sleep 30\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+":/bin:/usr/bin")
	prev := tmuxTimeout
	tmuxTimeout = 300 * time.Millisecond
	t.Cleanup(func() { tmuxTimeout = prev })
	began := time.Now()
	if _, err := tmuxListPanes(); !errors.Is(err, ErrUnknown) || time.Since(began) > 5*time.Second {
		t.Fatalf("a hung server: %v after %s", err, time.Since(began))
	}
}

// 3.0.1 t16 round 7 (reviewer): the chain is a sequence of (pid, stamp)
// pairs, each the pair the previous hop read and validated as a parent: the
// next hop walks that pid only while it still carries that stamp. A parent
// replaced between being read as one and being walked (its pid reused by a
// process with another stamp) is unknown, never followed. Reverted (each hop
// taking a fresh stamp as the child's), the replacement's ancestry is walked.
func TestReachesWalksOnlyTheValidatedPairs(t *testing.T) {
	lead := []Pane{{Shell: 80, Start: tk(80), Agent: "lead"}}
	parent := map[int]int{100: 90, 90: 80, 80: 1}
	reads := map[int]int{}
	prevP, prevS := parentOf, startTokenOf
	t.Cleanup(func() { parentOf, startTokenOf = prevP, prevS })
	parentOf = func(p int) (int, bool) { v, ok := parent[p]; return v, ok }
	startTokenOf = func(p int) (string, bool) {
		reads[p]++
		switch p {
		case 100:
			return "linux-ticks:1000", true
		case 90:
			if reads[90] > 1 {
				return "linux-ticks:850", true // after its read as a parent: another process, between 80 and 100 in age
			}
			return "linux-ticks:900", true
		case 80:
			return tk(80), true
		}
		return "", false
	}
	if p, known := Reaches(100, "linux-ticks:1000", lead); known || p != nil {
		t.Fatalf("a parent replaced between hops was walked: %+v %v", p, known)
	}
}
