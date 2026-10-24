package fsanchor

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// realTemp is a test directory by its canonical path (macOS puts temp
// directories under /var, a symlink that OpenAnchored refuses).
func realTemp(t *testing.T) string {
	t.Helper()
	d, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// The anchor is opened from / with O_NOFOLLOW: a symlinked anchor, or a
// symlink above it, is refused (Codex review of 4-3, P1 4, second pass).
func TestOpenAnchoredRefusesASymlinkedAnchor(t *testing.T) {
	real, outside := realTemp(t), realTemp(t)
	link := filepath.Join(outside, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	if d, err := OpenAnchored(link, nil, false); err == nil {
		d.Close()
		t.Fatal("a symlinked anchor was opened")
	}
	if err := os.MkdirAll(filepath.Join(real, "sub"), 0o700); err != nil {
		t.Fatal(err)
	}
	if d, err := OpenAnchored(filepath.Join(link, "sub"), nil, false); err == nil {
		d.Close()
		t.Fatal("an anchor below a symlinked ancestor was opened")
	}
	if _, err := OpenAnchored("relative/path", nil, false); err == nil {
		t.Fatal("a relative anchor was opened")
	}
	d, err := OpenAnchored(real, []string{"sub"}, false)
	if err != nil {
		t.Fatal(err)
	}
	d.Close()
}

// Eight concurrent first OpenLock calls in a fresh directory all open the
// same lock file (Darwin's create race returns ENOENT to the losers without
// the retry).
func TestOpenLockConcurrentCreate(t *testing.T) {
	for round := 0; round < 20; round++ {
		dir := realTemp(t)
		var wg sync.WaitGroup
		for i := 0; i < 8; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				d, err := OpenAnchored(dir, []string{".amq-squad"}, true)
				if err != nil {
					t.Error(err)
					return
				}
				defer d.Close()
				f, err := d.OpenLock("x.lock")
				if err != nil {
					t.Error(err)
					return
				}
				f.Close()
			}()
		}
		wg.Wait()
	}
}

// A symlinked component below the anchor and a symlinked lock are refused.
func TestOpenLockRefusesSymlinks(t *testing.T) {
	dir, outside := realTemp(t), realTemp(t)
	if err := os.Symlink(outside, filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenAnchored(dir, []string{"link"}, true); err == nil {
		t.Fatal("a symlinked component was opened")
	}
	d, err := OpenAnchored(dir, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if err := os.Symlink(filepath.Join(outside, "target"), filepath.Join(dir, "x.lock")); err != nil {
		t.Fatal(err)
	}
	if _, err := d.OpenLock("x.lock"); err == nil {
		t.Fatal("a symlinked lock was opened")
	}
	if _, err := os.Stat(filepath.Join(outside, "target")); !os.IsNotExist(err) {
		t.Fatal("the symlink target was created")
	}
}
