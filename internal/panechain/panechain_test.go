package panechain

import (
	"errors"
	"os"
	"path/filepath"
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
	panes := []Pane{{Shell: 80, Start: "s80", Agent: "lead"}}
	fakeProcs(t, map[int]int{100: 90, 90: 80, 80: 1}, map[int]string{100: "s100", 90: "s90", 80: "s80", 1: "s1"})
	if p, known := Reaches(100, "s100", panes); !known || p == nil || p.Agent != "lead" {
		t.Fatalf("under the lead's pane: %+v %v", p, known)
	}
	if p, known := Reaches(100, "s100", []Pane{{Shell: 70, Start: "s70", Agent: "s1"}}); !known || p != nil {
		t.Fatalf("outside every pane: %+v %v", p, known)
	}
	if _, known := Reaches(100, "other", panes); known {
		t.Fatal("a changed sender stamp is known")
	}
	if _, known := Reaches(100, "s100", []Pane{{Shell: 80, Start: "old", Agent: "lead"}}); known {
		t.Fatal("a reused shell pid is known")
	}
	fakeProcs(t, map[int]int{100: 90}, map[int]string{100: "s100", 90: "s90"})
	if _, known := Reaches(100, "s100", panes); known {
		t.Fatal("a vanished hop is known")
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
