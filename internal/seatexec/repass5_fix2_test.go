//go:build unix

package seatexec

import (
	"os"
	"path/filepath"
	"testing"
)

// The narrow pass's HIGH: seat-exec runs the registry's claude pin, re-opened
// and re-hashed immediately before exec, so the bytes the launch probed are
// the bytes the seat runs. A provider that is not the pin, and a pinned file
// replaced after the probe (here: after the proof, before exec), are
// refused tool_unpinned.
func TestClaudeSeatExecsOnlyThePinnedBytes(t *testing.T) {
	f := newFixture(t, "claude")
	f.stage()
	other := filepath.Join(f.base, "other")
	os.MkdirAll(other, 0o700)
	write(t, filepath.Join(other, "claude"), "#!/bin/sh\nexit 0\n", 0o700)
	if err := f.run(Options{Argv: []string{filepath.Join(other, "claude"), "--permission-mode", "dontAsk"}}); refusalCode(err) != ToolUnpinned || f.execPath != "" {
		t.Fatalf("a provider that is not the pin: %v (exec %q)", err, f.execPath)
	}
	g := newFixture(t, "claude")
	g.stage()
	o := Options{BeforeFinalCheck: func() {
		write(t, filepath.Join(g.bin, "claude"), "#!/bin/sh\n# replaced after the probe\nexit 0\n", 0o700)
	}}
	if err := g.run(o); refusalCode(err) != ToolUnpinned || g.execPath != "" {
		t.Fatalf("a pinned file replaced before exec: %v (exec %q)", err, g.execPath)
	}
	h := newFixture(t, "claude")
	h.stage()
	if err := h.run(Options{}); err != nil || h.execPath != filepath.Join(h.bin, "claude") {
		t.Fatalf("the pinned claude: %v (exec %q)", err, h.execPath)
	}
}
