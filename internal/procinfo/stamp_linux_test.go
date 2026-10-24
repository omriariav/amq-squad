//go:build linux

package procinfo

import "testing"

func TestParentFromStatAfterComplexComm(t *testing.T) {
	stat := "4242 (a b) c)) S 17 4242 4242 0 -1 4194560 0 0 0 0 0 0 0 0 20 0 1 0 98765 0 0"
	if ppid, ok := parentFromStat(stat); !ok || ppid != 17 {
		t.Fatalf("parentFromStat = %d %v", ppid, ok)
	}
	if ticks, ok := processStartTicksFromStat(stat); !ok || ticks != 98765 {
		t.Fatalf("start ticks = %d %v", ticks, ok)
	}
}
