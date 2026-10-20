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

// The default warm path must not read the embedded payload at all.
func TestFastWarmSkipsPayload(t *testing.T) {
	m, payload := fixture(t)
	base := t.TempDir()
	dir, warm, err := ensure(base, m, src(payload), false)
	if err != nil || warm {
		t.Fatalf("cold: %t %v", warm, err)
	}
	if _, err := os.Stat(filepath.Join(dir, stampName)); err != nil {
		t.Fatal("no completion stamp")
	}
	noLoad := func() ([]byte, error) { t.Fatal("warm start loaded payload"); return nil, nil }
	if again, warm, err := ensure(base, m, noLoad, false); err != nil || !warm || again != dir {
		t.Fatalf("warm: %t %v", warm, err)
	}
	// A changed executable or a missing stamp fails closed.
	exe := filepath.Join(dir, "bin/pi")
	if err := os.Chmod(exe, 0755); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ensure(base, m, noLoad, false); err == nil {
		t.Fatal("changed executable mode accepted on fast warm path")
	}
	if err := os.Chmod(exe, 0555); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, stampName)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ensure(base, m, noLoad, false); err == nil {
		t.Fatal("unstamped runtime accepted")
	}
}
