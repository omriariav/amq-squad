package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/v3/internal/registry"
)

// Phase 5 release prep: doctor says "pinned claude is behind the installed
// claude" with the re-pin command (the hard link and the registry line) when
// the installed claude on PATH, followed through its symlink chain, hashes
// differently from the pin. The installed binary is only hashed, never run:
// the fake would leave a marker if it were executed.
func TestDoctorWarnsWhenThePinnedClaudeIsBehindTheInstalledClaude(t *testing.T) {
	base, _ := filepath.EvalSymlinks(t.TempDir())
	home := filepath.Join(base, "home")
	versions := filepath.Join(home, "versions")
	pinDir := filepath.Join(home, ".amq-squad", "bin")
	for _, d := range []string{versions, pinDir, filepath.Join(home, "local-bin")} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	marker := filepath.Join(base, "executed")
	oldBin := filepath.Join(versions, "1.0.0")
	newBin := filepath.Join(versions, "1.0.1")
	os.WriteFile(oldBin, []byte("#!/bin/sh\ntouch "+marker+"\n# old\n"), 0o700)
	os.WriteFile(newBin, []byte("#!/bin/sh\ntouch "+marker+"\n# new\n"), 0o700)
	pinPath := filepath.Join(pinDir, "claude")
	if err := os.Link(oldBin, pinPath); err != nil { // the owner's hard link keeps the old bytes
		t.Fatal(err)
	}
	// The installed claude on PATH: a symlink chain to the new version.
	mid := filepath.Join(home, "local-bin", "claude-next")
	installed := filepath.Join(home, "local-bin", "claude")
	if err := os.Symlink(newBin, mid); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(mid, installed); err != nil {
		t.Fatal(err)
	}
	oldSum, _ := registry.FileSHA256(oldBin)
	newSum, _ := registry.FileSHA256(newBin)
	if err := registry.Write(registry.Path(home), registry.Registry{Schema: 1, Pins: []registry.Pin{{Name: "claude", Path: pinPath, SHA256: oldSum}}}); err != nil {
		t.Fatal(err)
	}
	look := func(string) (string, error) { return installed, nil }

	rows := claudePinCheck(home, look)
	if len(rows) != 1 || rows[0].Status != doctorWarn || !strings.Contains(rows[0].Detail, "pinned claude is behind the installed claude") {
		t.Fatalf("the stale pin row: %+v", rows)
	}
	for _, want := range []string{"ln -f " + newBin + " " + pinPath, newSum, registry.Path(home)} {
		if !strings.Contains(rows[0].Detail, want) {
			t.Errorf("the re-pin command lacks %q: %s", want, rows[0].Detail)
		}
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("doctor executed the installed claude")
	}

	// Re-pinned to the installed bytes: the row is ok.
	if err := registry.Write(registry.Path(home), registry.Registry{Schema: 1, Pins: []registry.Pin{{Name: "claude", Path: pinPath, SHA256: newSum}}}); err != nil {
		t.Fatal(err)
	}
	if rows := claudePinCheck(home, look); len(rows) != 1 || rows[0].Status != doctorOK {
		t.Fatalf("a current pin: %+v", rows)
	}
}

// The installed claude's path is one a same-UID writer controls: a chain that
// leaves the owner's HOME (and /opt/homebrew) or runs past the hop bound is
// reported unresolvable and never followed or hashed.
func TestDoctorClaudePinRefusesAnUnresolvableInstalledChain(t *testing.T) {
	base, _ := filepath.EvalSymlinks(t.TempDir())
	home := filepath.Join(base, "home")
	outside := filepath.Join(base, "outside")
	bin := filepath.Join(home, "bin")
	for _, d := range []string{bin, outside} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	os.WriteFile(filepath.Join(outside, "claude"), []byte("#!/bin/sh\n"), 0o700)
	leave := filepath.Join(bin, "claude")
	if err := os.Symlink(filepath.Join(outside, "claude"), leave); err != nil {
		t.Fatal(err)
	}
	rows := claudePinCheck(home, func(string) (string, error) { return leave, nil })
	if len(rows) != 1 || rows[0].Status != doctorWarn || !strings.Contains(rows[0].Detail, "unresolvable") || !strings.Contains(rows[0].Detail, "outside") {
		t.Fatalf("a chain that leaves HOME: %+v", rows)
	}
	// A loop of links exceeds the hop bound.
	a, b := filepath.Join(bin, "a"), filepath.Join(bin, "b")
	os.Symlink(b, a)
	os.Symlink(a, b)
	rows = claudePinCheck(home, func(string) (string, error) { return a, nil })
	if len(rows) != 1 || rows[0].Status != doctorWarn || !strings.Contains(rows[0].Detail, "unresolvable") || !strings.Contains(rows[0].Detail, "hops") {
		t.Fatalf("a link loop: %+v", rows)
	}
}
