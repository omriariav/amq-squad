//go:build unix

package seatexec

import (
	"github.com/omriariav/amq-squad/v3/internal/launch"
	"github.com/omriariav/amq-squad/v3/internal/registry"
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

// Phase 5 release prep: the registry's claude pin is compared with the digest
// the staged record carries (the pin the launch probed). A pin moved to
// another file between the launch and the exec is refused
// pin_changed_since_probe, even though the new pin is internally consistent
// (path and digest agree), so the tool_unpinned check alone would pass it.
func TestSeatRefusedWhenPinChangedSinceProbe(t *testing.T) {
	f := newFixture(t, "claude")
	f.stage() // the record now carries the probed digest
	// Re-pin to another file after the launch staged the record.
	other := filepath.Join(f.base, "other")
	os.MkdirAll(other, 0o700)
	write(t, filepath.Join(other, "claude"), "#!/bin/sh\n# another build\nexit 0\n", 0o700)
	sum, err := registry.FileSHA256(filepath.Join(other, "claude"))
	if err != nil {
		t.Fatal(err)
	}
	if err := registry.Write(registry.Path(f.home), registry.Registry{Schema: 1, Pins: []registry.Pin{{Name: "claude", Path: filepath.Join(other, "claude"), SHA256: sum}}}); err != nil {
		t.Fatal(err)
	}
	err = f.run(Options{Argv: []string{filepath.Join(other, "claude"), "--permission-mode", "dontAsk"}})
	if refusalCode(err) != PinChangedSinceProbe || f.execPath != "" {
		t.Fatalf("a pin re-pinned after the probe: %v (exec %q)", err, f.execPath)
	}
	// A staged record with no probed digest is refused the same way.
	g := newFixture(t, "claude")
	g.stage()
	g.rec.ClaudePin = ""
	b, _ := launch.Marshal(g.rec)
	write(t, filepath.Join(launch.StagedDir(g.root, "cto"), launch.FileName), string(b), 0o600)
	if err := g.run(Options{}); refusalCode(err) != PinChangedSinceProbe || g.execPath != "" {
		t.Fatalf("a record with no probed digest: %v (exec %q)", err, g.execPath)
	}
}
