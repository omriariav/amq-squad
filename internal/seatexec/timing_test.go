//go:build unix

package seatexec

import (
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/claudeseat"
)

// TestSeatExecWallTimeToExec measures the built binary: `amq-squad seat-exec
// --seat cto <claude>` from process start until the provider (a shell
// script that exits at once) has run, with the drain proof against the
// installed amq, minus the provider's own run time; median of 10 after one
// warm-up. Run with AMQ_SQUAD_TIMING=1 (it builds the binary).
func TestSeatExecWallTimeToExec(t *testing.T) {
	if os.Getenv("AMQ_SQUAD_TIMING") != "1" {
		t.Skip("set AMQ_SQUAD_TIMING=1 to measure seat-exec wall time to exec")
	}
	amq, err := exec.LookPath("amq")
	if err != nil {
		t.Skip("amq not on PATH")
	}
	f := newFixture(t, "claude")
	base := filepath.Dir(f.root)
	os.RemoveAll(base)
	env := []string{"PATH=" + os.Getenv("PATH"), "HOME=" + f.home}
	for _, args := range [][]string{{"init", "--root", base, "--agents", "operator,cto"}, {"session", "create", "s1", "--root", base, "--me", "operator", "--json"}} {
		cmd := exec.Command(amq, args...)
		cmd.Env = env
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("amq %v: %v\n%s", args, err, out)
		}
	}
	os.MkdirAll(filepath.Join(f.root, "extensions", "amq-squad", "launch", "cto"), 0o700)
	filepath.Walk(base, func(p string, info os.FileInfo, err error) error {
		if err == nil && info.IsDir() {
			os.Chmod(p, info.Mode().Perm()&^0o022)
		}
		return nil
	})
	f.rec.AMQPath = amq
	os.MkdirAll(filepath.Join(f.home, ".amq-squad"), 0o700) // the base was removed: the owner token file again
	write(t, claudeseat.TokenPath(f.home), canaryToken+"\n", 0o600)
	f.stage()
	bin := filepath.Join(f.base, "amq-squad")
	if out, err := exec.Command("go", "build", "-o", bin, "github.com/omriariav/amq-squad/v3/cmd/amq-squad").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	provider := filepath.Join(f.bin, "claude")
	run := func(argv ...string) time.Duration {
		cmd := exec.Command(argv[0], argv[1:]...)
		cmd.Env = append(f.env(), env...)
		start := time.Now()
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v: %v\n%s", argv, err, out)
		}
		return time.Since(start)
	}
	median := func(argv ...string) time.Duration {
		run(argv...)
		var d []time.Duration
		for i := 0; i < 10; i++ {
			d = append(d, run(argv...))
		}
		sort.Slice(d, func(i, j int) bool { return d[i] < d[j] })
		return (d[4] + d[5]) / 2
	}
	wrapped := median(bin, "seat-exec", "--seat", "cto", provider, "--permission-mode", "dontAsk")
	bare := median(provider)
	t.Logf("seat-exec wall time to exec: median %s (wrapped %s, provider alone %s; drain proof with %s)", (wrapped - bare).Round(100*time.Microsecond), wrapped.Round(100*time.Microsecond), bare.Round(100*time.Microsecond), amq)
}
