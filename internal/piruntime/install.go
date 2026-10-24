package piruntime

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/flock"
)

// Runtime is one verified, unpacked runtime.
type Runtime struct {
	Dir      string
	Manifest Manifest
}

// Exe is the absolute path of the runtime's Node executable.
func (r Runtime) Exe() string { return r.abs(r.Manifest.Executable) }

// PiArgv is node plus the pi entry script, absolute paths only: the command
// prefix that starts pi without the amq-squad package (2b-3's
// registry.ProbeConfig.Pi takes exactly this).
func (r Runtime) PiArgv() []string {
	argv := []string{r.Exe()}
	for _, a := range r.Manifest.Args {
		argv = append(argv, r.abs(a))
	}
	return argv
}

// PackageDir is the absolute path of the embedded @amq-squad/pi package.
func (r Runtime) PackageDir() string {
	if r.Manifest.Package == "" {
		return ""
	}
	return r.abs(r.Manifest.Package)
}

func (r Runtime) abs(rel string) string { return filepath.Join(r.Dir, filepath.FromSlash(rel)) }

// Timing is what one Ensure call measured.
type Timing struct {
	Cold   bool          // the runtime was unpacked by this call
	Rehash time.Duration // the full re-hash of the tree (warm path), or unpack plus hash (cold)
	Total  time.Duration // the whole Ensure call
	Files  int
	Bytes  int64
}

// DefaultBase is ~/.amq-squad/runtime.
func DefaultBase(home string) string { return filepath.Join(home, ".amq-squad", "runtime") }

// Ensure returns the verified runtime for e under base. A cold call checks
// the payload digest against the manifest before it writes anything, unpacks
// into a private staging directory under an exclusive lock, re-hashes every
// file, writes the on-disk manifest and the completion marker, and renames
// the directory into place. A warm call re-hashes the whole tree against the
// embedded manifest (lead ruling for 4-1 and the 2b-3 registry review: the
// embedded manifest digest is the trust root, so every launch verifies every
// file). An existing runtime that fails any check is refused, never repaired.
func Ensure(base string, e Embedded) (Runtime, Timing, error) {
	start := time.Now()
	m := e.Manifest
	if err := m.Validate(); err != nil {
		return Runtime{}, Timing{}, err
	}
	if m.Platform != runtime.GOOS+"-"+runtime.GOARCH {
		return Runtime{}, Timing{}, fmt.Errorf("pi runtime is for %s, this host is %s-%s", m.Platform, runtime.GOOS, runtime.GOARCH)
	}
	target := filepath.Join(base, m.DirName())
	rt := Runtime{Dir: target, Manifest: m}
	var tm Timing
	if ok, err := warm(target, e, &tm); err != nil {
		return Runtime{}, tm, err
	} else if ok {
		tm.Total = time.Since(start)
		return rt, tm, nil
	}
	payload, err := e.Payload()
	if err != nil {
		return Runtime{}, tm, err
	}
	if digest(payload) != m.SHA256 {
		return Runtime{}, tm, errors.New("embedded pi payload SHA-256 does not match its manifest")
	}
	if err := os.MkdirAll(base, 0o700); err != nil {
		return Runtime{}, tm, err
	}
	if err := realDir(base); err != nil {
		return Runtime{}, tm, fmt.Errorf("runtime base %s: %w", base, err)
	}
	lock, err := flock.AcquireExclusive(filepath.Join(base, "."+m.DirName()+".lock"))
	if err != nil {
		return Runtime{}, tm, err
	}
	defer lock.Close()
	// Another process may have finished the install while this one waited.
	if ok, err := warm(target, e, &tm); err != nil {
		return Runtime{}, tm, err
	} else if ok {
		tm.Total = time.Since(start)
		return rt, tm, nil
	}
	hashStart := time.Now()
	tmp, err := os.MkdirTemp(base, ".unpack-")
	if err != nil {
		return Runtime{}, tm, err
	}
	defer os.RemoveAll(tmp)
	if err := unpack(tmp, m, payload); err != nil {
		return Runtime{}, tm, err
	}
	if tm.Files, tm.Bytes, err = verify(tmp, m); err != nil {
		return Runtime{}, tm, err
	}
	if err := writeFile(filepath.Join(tmp, ManifestName), e.Raw); err != nil {
		return Runtime{}, tm, err
	}
	if err := writeFile(filepath.Join(tmp, MarkerName), []byte(m.SHA256+"\n")); err != nil {
		return Runtime{}, tm, err
	}
	if err := os.Chmod(tmp, 0o755); err != nil {
		return Runtime{}, tm, err
	}
	if err := os.Rename(tmp, target); err != nil {
		return Runtime{}, tm, err
	}
	tm.Cold, tm.Rehash, tm.Total = true, time.Since(hashStart), time.Since(start)
	return rt, tm, nil
}

// Reject is a runtime that exists but fails verification.
type Reject struct {
	Dir    string
	Reason error
}

func (r *Reject) Error() string {
	return fmt.Sprintf("pi runtime %s rejected (run `amq-squad doctor --fix` or remove it explicitly to repair): %v", r.Dir, r.Reason)
}

func (r *Reject) Unwrap() error { return r.Reason }

// warm reports whether target holds a valid runtime. It returns (false, nil)
// only when target is absent.
func warm(target string, e Embedded, tm *Timing) (bool, error) {
	info, err := os.Lstat(target)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	reject := func(why error) (bool, error) { return false, &Reject{Dir: target, Reason: why} }
	if !info.IsDir() {
		return reject(errors.New("not a real directory (symlink or file)"))
	}
	if err := realDir(filepath.Dir(target)); err != nil {
		return reject(fmt.Errorf("runtime base: %w", err))
	}
	if b, err := os.ReadFile(filepath.Join(target, ManifestName)); err != nil || digest(b) != e.Digest {
		return reject(errors.New("on-disk manifest missing or differs from the embedded one"))
	}
	if b, err := os.ReadFile(filepath.Join(target, MarkerName)); err != nil || strings.TrimSpace(string(b)) != e.Manifest.SHA256 {
		return reject(errors.New("completion marker missing or for another payload"))
	}
	start := time.Now()
	files, bytes, err := verify(target, e.Manifest)
	tm.Rehash, tm.Files, tm.Bytes = time.Since(start), files, bytes
	if err != nil {
		return reject(err)
	}
	return true, nil
}

// Verify re-hashes an unpacked runtime against the embedded manifest (the
// doctor check). It is the same check every launch runs.
func Verify(dir string, e Embedded) (Timing, error) {
	var tm Timing
	_, err := warm(dir, e, &tm)
	return tm, err
}

func realDir(p string) error {
	fi, err := os.Lstat(p)
	if err != nil {
		return err
	}
	if fi.Mode()&fs.ModeSymlink != 0 || !fi.IsDir() {
		return errors.New("not a real directory")
	}
	return nil
}

// verify walks dir: every file must be a regular file named in m with its
// size, mode and SHA-256, nothing may be missing or extra, and no entry may
// be a symlink (a symlinked directory shows as a non-directory entry, so the
// path to the entrypoint and every tool is covered).
func verify(dir string, m Manifest) (int, int64, error) {
	want := make(map[string]Entry, len(m.Files))
	for _, f := range m.Files {
		want[f.Path] = f
	}
	var todo []Entry
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			return nil
		}
		if rel == ManifestName || rel == MarkerName {
			if !d.Type().IsRegular() {
				return fmt.Errorf("runtime entry %s is not a regular file", rel)
			}
			return nil
		}
		f, ok := want[rel]
		if !ok {
			return fmt.Errorf("unexpected runtime entry %q", rel)
		}
		if !d.Type().IsRegular() {
			return fmt.Errorf("runtime entry %q is not a regular file (symlink?)", rel)
		}
		todo = append(todo, f)
		return nil
	})
	if err != nil {
		return 0, 0, err
	}
	if len(todo) != len(want) {
		return 0, 0, fmt.Errorf("runtime is missing %d file(s)", len(want)-len(todo))
	}
	var total int64
	errs := make(chan error, len(todo))
	sem := make(chan struct{}, runtime.NumCPU())
	var wg sync.WaitGroup
	for _, f := range todo {
		total += f.Size
		wg.Add(1)
		go func(f Entry) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			errs <- checkFile(filepath.Join(dir, filepath.FromSlash(f.Path)), f)
		}(f)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			return 0, 0, err
		}
	}
	return len(todo), total, nil
}

func checkFile(p string, f Entry) error {
	fi, err := os.Lstat(p)
	if err != nil {
		return err
	}
	if !fi.Mode().IsRegular() || fi.Size() != f.Size || (runtime.GOOS != "windows" && uint32(fi.Mode().Perm()) != f.Mode) {
		return fmt.Errorf("type, size or mode mismatch: %s", f.Path)
	}
	in, err := os.Open(p)
	if err != nil {
		return err
	}
	defer in.Close()
	h := sha256.New()
	if _, err := io.Copy(h, in); err != nil {
		return err
	}
	if hex.EncodeToString(h.Sum(nil)) != f.SHA256 {
		return fmt.Errorf("SHA-256 mismatch: %s", f.Path)
	}
	return nil
}

func unpack(dir string, m Manifest, payload []byte) error {
	gz, err := gzip.NewReader(bytes.NewReader(payload))
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	want := map[string]Entry{}
	for _, f := range m.Files {
		want[f.Path] = f
	}
	seen := map[string]bool{}
	made := map[string]bool{}
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		f, ok := want[h.Name]
		// The packer writes regular files only: links, devices and
		// directories are refused.
		if !ok || seen[h.Name] || h.Typeflag != tar.TypeReg || h.Size != f.Size {
			return fmt.Errorf("unexpected archive entry %q", h.Name)
		}
		seen[h.Name] = true
		dest := filepath.Join(dir, filepath.FromSlash(h.Name))
		if parent := filepath.Dir(dest); !made[parent] {
			if err := os.MkdirAll(parent, 0o755); err != nil {
				return err
			}
			made[parent] = true
		}
		out, err := os.OpenFile(dest, os.O_CREATE|os.O_EXCL|os.O_WRONLY, fs.FileMode(f.Mode)|0o200)
		if err != nil {
			return err
		}
		_, copyErr := io.CopyN(out, tr, f.Size)
		closeErr := out.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
		if err := os.Chmod(dest, fs.FileMode(f.Mode)); err != nil {
			return err
		}
	}
	// Read the gzip footer so truncation and CRC failures surface.
	if _, err := io.Copy(io.Discard, gz); err != nil {
		return err
	}
	if len(seen) != len(want) {
		return errors.New("archive is missing files")
	}
	return nil
}

func writeFile(p string, b []byte) error {
	_ = os.Remove(p)
	return os.WriteFile(p, b, 0o444)
}
