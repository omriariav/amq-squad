package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func fixture(t *testing.T) (manifest, []byte) {
	t.Helper()
	content := []byte("fixture only; this is not pi\n")
	var b bytes.Buffer
	gz := gzip.NewWriter(&b)
	tr := tar.NewWriter(gz)
	if err := tr.WriteHeader(&tar.Header{Name: "bin/pi", Mode: 0555, Size: int64(len(content)), Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	if _, err := tr.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := tr.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	payload := b.Bytes()
	m := manifest{Version: "test", SHA256: digest(payload), Executable: "bin/pi", Files: []entry{{Path: "bin/pi", SHA256: digest(content), Size: int64(len(content)), Mode: 0555}}}
	return m, payload
}

func TestColdWarmAndTamper(t *testing.T) {
	m, payload := fixture(t)
	base := t.TempDir()
	dir, warm, err := ensure(base, m, src(payload), true)
	if err != nil || warm {
		t.Fatalf("cold: %t %v", warm, err)
	}
	f := filepath.Join(dir, "bin/pi")
	before, err := os.Stat(f)
	if err != nil {
		t.Fatal(err)
	}
	again, warm, err := ensure(base, m, src(payload), true)
	if err != nil || !warm || again != dir {
		t.Fatalf("warm: %t %v", warm, err)
	}
	after, err := os.Stat(f)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(before, after) || !before.ModTime().Equal(after.ModTime()) {
		t.Fatal("warm path rewrote file")
	}
	if err := os.Chmod(f, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f, bytes.Repeat([]byte("X"), int(before.Size())), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(f, 0555); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ensure(base, m, src(payload), true); err == nil || !strings.Contains(err.Error(), "SHA-256") {
		t.Fatalf("tampering not caught: %v", err)
	}
}

func TestBadPayloadDigest(t *testing.T) {
	m, payload := fixture(t)
	payload[0] ^= 1
	base := filepath.Join(t.TempDir(), "runtime")
	if _, _, err := ensure(base, m, src(payload), true); err == nil {
		t.Fatal("bad payload accepted")
	}
	if _, err := os.Stat(base); !os.IsNotExist(err) {
		t.Fatal("wrote runtime before digest check")
	}
}

func TestTraversalAndSymlinks(t *testing.T) {
	for _, name := range []string{"../outside", "/tmp/outside", "a/../../outside", "a\\b"} {
		t.Run(name, func(t *testing.T) {
			m, _ := fixture(t)
			m.Files[0].Path = name
			if err := m.validate(); err == nil {
				t.Fatal("unsafe path accepted")
			}
		})
	}
	m, payload := fixture(t)
	base := t.TempDir()
	dir, _, err := ensure(base, m, src(payload), true)
	if err != nil {
		t.Fatal(err)
	}
	f := filepath.Join(dir, "bin/pi")
	if err := os.Remove(f); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/bin/sh", f); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ensure(base, m, src(payload), true); err == nil {
		t.Fatal("cache symlink accepted")
	}
}

func TestArchiveRejectsSymlinkAndMissingFile(t *testing.T) {
	for _, link := range []bool{true, false} {
		m, _ := fixture(t)
		var b bytes.Buffer
		gz := gzip.NewWriter(&b)
		tr := tar.NewWriter(gz)
		if link {
			if err := tr.WriteHeader(&tar.Header{Name: "bin/pi", Typeflag: tar.TypeSymlink, Linkname: "/bin/sh"}); err != nil {
				t.Fatal(err)
			}
		}
		if err := tr.Close(); err != nil {
			t.Fatal(err)
		}
		if err := gz.Close(); err != nil {
			t.Fatal(err)
		}
		m.SHA256 = digest(b.Bytes())
		if _, _, err := ensure(t.TempDir(), m, src(b.Bytes()), true); err == nil {
			t.Fatal("invalid archive accepted")
		}
	}
}

func TestConcurrentUnpack(t *testing.T) {
	m, payload := fixture(t)
	base := t.TempDir()
	var wg sync.WaitGroup
	results := make(chan bool, 4)
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, warm, err := ensure(base, m, src(payload), true)
			if err != nil {
				t.Error(err)
			}
			results <- warm
		}()
	}
	wg.Wait()
	close(results)
	cold := 0
	for warm := range results {
		if !warm {
			cold++
		}
	}
	if cold != 1 {
		t.Fatalf("got %d cold installs", cold)
	}
}

func src(b []byte) func() ([]byte, error) { return func() ([]byte, error) { return b, nil } }

func TestLinkTools(t *testing.T) {
	agent, rt := t.TempDir(), t.TempDir()
	tools := map[string]string{"fd": "tools/fd", "rg": "tools/rg"}
	if err := linkTools(agent, rt, tools); err != nil {
		t.Fatal(err)
	}
	if cur, err := os.Readlink(filepath.Join(agent, "bin", "fd")); err != nil || cur != filepath.Join(rt, "tools/fd") {
		t.Fatalf("fd link: %q %v", cur, err)
	}
	// A newer runtime replaces the link; a user-provided rg is kept.
	rt2 := t.TempDir()
	if err := os.Remove(filepath.Join(agent, "bin", "rg")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agent, "bin", "rg"), []byte("user"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := linkTools(agent, rt2, tools); err != nil {
		t.Fatal(err)
	}
	if cur, _ := os.Readlink(filepath.Join(agent, "bin", "fd")); cur != filepath.Join(rt2, "tools/fd") {
		t.Fatalf("fd not relinked: %q", cur)
	}
	if b, _ := os.ReadFile(filepath.Join(agent, "bin", "rg")); string(b) != "user" {
		t.Fatal("user rg overwritten")
	}
	m, _ := fixture(t)
	m.Tools = map[string]string{"fd": "missing/fd"}
	if m.validate() == nil {
		t.Fatal("tool outside manifest accepted")
	}
}

// fixture2 has an executable (bin/pi) and a non-executable (lib/app.js).
func fixture2(t *testing.T) (manifest, []byte) {
	t.Helper()
	files := []struct {
		name string
		mode uint32
		data []byte
	}{{"bin/pi", 0555, []byte("exe fixture\n")}, {"lib/app.js", 0444, []byte("js fixture\n")}}
	var b bytes.Buffer
	gz := gzip.NewWriter(&b)
	tr := tar.NewWriter(gz)
	m := manifest{Version: "test2", Executable: "bin/pi", Args: []string{"lib/app.js"}}
	for _, f := range files {
		if err := tr.WriteHeader(&tar.Header{Name: f.name, Mode: int64(f.mode), Size: int64(len(f.data)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tr.Write(f.data); err != nil {
			t.Fatal(err)
		}
		m.Files = append(m.Files, entry{Path: f.name, SHA256: digest(f.data), Size: int64(len(f.data)), Mode: f.mode})
	}
	if err := tr.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	m.SHA256 = digest(b.Bytes())
	return m, b.Bytes()
}

// overwrite replaces a read-only file's content with same-size bytes.
func overwrite(t *testing.T, p string, mode os.FileMode) {
	t.Helper()
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, bytes.Repeat([]byte("X"), int(fi.Size())), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p, mode); err != nil {
		t.Fatal(err)
	}
}

// Default warm launch: never loads the payload, hashes executables, checks
// the on-disk manifest and launch-path symlinks; marker loss forces a full hash.
func TestLaunchCheck(t *testing.T) {
	m, payload := fixture2(t)
	noLoad := func() ([]byte, error) { t.Fatal("warm start loaded payload"); return nil, nil }
	fresh := func() (string, string) {
		base := t.TempDir()
		dir, warm, err := ensure(base, m, src(payload), false)
		if err != nil || warm {
			t.Fatalf("cold: %t %v", warm, err)
		}
		if _, err := os.Stat(filepath.Join(dir, manifestName)); err != nil {
			t.Fatal("no on-disk manifest")
		}
		return base, dir
	}
	base, dir := fresh()
	if again, warm, err := ensure(base, m, noLoad, false); err != nil || !warm || again != dir {
		t.Fatalf("warm: %t %v", warm, err)
	}

	// Same-size tamper of the executable is caught at launch.
	base, dir = fresh()
	overwrite(t, filepath.Join(dir, "bin/pi"), 0555)
	if _, _, err := ensure(base, m, noLoad, false); err == nil || !strings.Contains(err.Error(), "SHA-256") {
		t.Fatalf("executable tamper not caught at launch: %v", err)
	}

	// Documented limit: a same-size tamper of a non-executable file passes the
	// launch check and is caught only by the full (doctor) re-hash.
	base, dir = fresh()
	overwrite(t, filepath.Join(dir, "lib/app.js"), 0444)
	if _, _, err := ensure(base, m, noLoad, false); err != nil {
		t.Fatalf("launch check should not hash non-executables: %v", err)
	}
	if _, _, err := ensure(base, m, noLoad, true); err == nil {
		t.Fatal("full re-hash missed non-executable tamper")
	}

	// A changed on-disk manifest is rejected.
	base, dir = fresh()
	mf := filepath.Join(dir, manifestName)
	if err := os.Chmod(mf, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(mf, []byte("{}"), 0444); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ensure(base, m, noLoad, false); err == nil {
		t.Fatal("changed on-disk manifest accepted")
	}

	// A symlinked directory on the entry path is rejected.
	base, dir = fresh()
	lib := filepath.Join(dir, "lib")
	moved := filepath.Join(t.TempDir(), "lib")
	if err := os.Rename(lib, moved); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(moved, lib); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ensure(base, m, noLoad, false); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("symlinked launch path accepted: %v", err)
	}

	// Missing marker: full re-hash, then the marker is rewritten.
	base, dir = fresh()
	if err := os.Remove(filepath.Join(dir, stampName)); err != nil {
		t.Fatal(err)
	}
	if _, warm, err := ensure(base, m, noLoad, false); err != nil || !warm {
		t.Fatalf("intact unmarked runtime: %t %v", warm, err)
	}
	if _, err := os.Stat(filepath.Join(dir, stampName)); err != nil {
		t.Fatal("marker not rewritten")
	}
	if err := os.Remove(filepath.Join(dir, stampName)); err != nil {
		t.Fatal(err)
	}
	overwrite(t, filepath.Join(dir, "lib/app.js"), 0444)
	if _, _, err := ensure(base, m, noLoad, false); err == nil {
		t.Fatal("unmarked tampered runtime accepted")
	}
}

func TestSeatName(t *testing.T) {
	t.Setenv("AMQ_SQUAD_SEAT", "")
	t.Setenv("AM_ME", "")
	if seatName() != "lead" {
		t.Fatal("default seat")
	}
	t.Setenv("AM_ME", "pi-1")
	if seatName() != "pi-1" {
		t.Fatal("AM_ME seat")
	}
	t.Setenv("AMQ_SQUAD_SEAT", "../x")
	if seatName() != "pi-1" {
		t.Fatal("unsafe seat accepted")
	}
}
