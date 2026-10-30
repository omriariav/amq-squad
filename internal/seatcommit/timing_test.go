package seatcommit

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"
)

// TestCommitStepTiming measures the commit step's wall time on the fixture
// repository (median of 10: snapshot, checks, commit, ledger, fast-forward
// push to a local bare origin). Run with AMQ_SQUAD_TIMING=1.
func TestCommitStepTiming(t *testing.T) {
	if os.Getenv("AMQ_SQUAD_TIMING") != "1" {
		t.Skip("timing run: set AMQ_SQUAD_TIMING=1")
	}
	f := newFixture(t)
	var commit, refuse []time.Duration
	for i := 0; i < 10; i++ {
		write(t, filepath.Join(f.wt, "cmd", "root.go"), fmt.Sprintf("package cmd\n// %d\n", i))
		tk := f.reported("cmd/")
		start := time.Now()
		res := f.run(tk)
		commit = append(commit, time.Since(start))
		if !res.OK || !res.Pushed {
			t.Fatalf("run %d: %+v", i, res)
		}
		if _, err := f.store.Complete(tk.ID, f.now); err != nil {
			t.Fatal(err)
		}
	}
	write(t, filepath.Join(f.wt, "README.md"), "outside\n")
	for i := 0; i < 10; i++ {
		write(t, filepath.Join(f.wt, "cmd", "root.go"), fmt.Sprintf("package cmd\n// r%d\n", i))
		tk := f.reported("cmd/")
		start := time.Now()
		res := f.run(tk)
		refuse = append(refuse, time.Since(start))
		if res.ReasonCode != ReasonOutside {
			t.Fatalf("refusal run %d: %+v", i, res)
		}
		if _, err := f.store.Cancel(tk.ID, "lead", "timing", f.now); err != nil {
			t.Fatal(err)
		}
	}
	median := func(d []time.Duration) time.Duration {
		sort.Slice(d, func(i, j int) bool { return d[i] < d[j] })
		return (d[4] + d[5]) / 2
	}
	t.Logf("commit step, commit and push: median %v of 10 (min %v, max %v)", median(commit), commit[0], commit[9])
	t.Logf("commit step, refusal: median %v of 10 (min %v, max %v)", median(refuse), refuse[0], refuse[9])
}
