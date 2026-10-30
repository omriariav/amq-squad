package piruntime

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/flock"
	"github.com/omriariav/amq-squad/v3/internal/fsanchor"
)

// RuntimeRel is the runtime base below the home directory, the trusted
// anchor: ~/.amq-squad/runtime.
var RuntimeRel = []string{".amq-squad", "runtime"}

// Runtime is one verified, unpacked runtime.
type Runtime struct {
	Dir      string
	Manifest Manifest
	// ManifestSHA256 is the embedded manifest digest (the trust root), passed
	// to the package for its in-process re-hash.
	ManifestSHA256 string
	home           string
	name           string
	// ids is the identity (device, inode, size, mtime) of every file the
	// re-hash hashed, taken from the descriptor it hashed; CheckLaunch
	// compares the launch set against it right before exec (review item 2).
	ids map[string]os.FileInfo
}

// Exe is the absolute path of the runtime's Node executable.
func (r Runtime) Exe() string { return r.abs(r.Manifest.Executable) }

// PiArgv is node plus the pi entry script, absolute paths only: the command
// prefix that starts pi without the amq-squad package (2b-3's
// registry.ProbeConfig.Pi takes exactly this).
func (r Runtime) PiArgv() []string {
	argv := []string{r.Exe()}
	if r.Manifest.Preload != "" {
		// The runtime self-check runs before the pi entry (review 2, 2 (b)).
		argv = append(argv, "--require", r.abs(r.Manifest.Preload))
	}
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
	Rehash time.Duration // the full re-hash of the tree (cold: unpack plus re-hash)
	Total  time.Duration // the whole Ensure call
	Files  int
	Bytes  int64
}

// DefaultBase is ~/.amq-squad/runtime.
func DefaultBase(home string) string { return filepath.Join(append([]string{home}, RuntimeRel...)...) }

// Ensure returns the verified runtime for e under home/.amq-squad/runtime.
//
// The home directory is the trusted anchor. Every component below it
// (.amq-squad, runtime, the runtime directory) is opened through its
// parent's descriptor with O_NOFOLLOW, and the tree is enumerated, typed and
// hashed through descriptors (fsanchor, lifted from 2b-3), so a symlinked
// ~/.amq-squad or any symlink inside the tree is refused (review item 3).
//
// A cold call checks the payload digest against the manifest before it
// writes anything, unpacks into a private staging directory under an
// exclusive lock, writes the on-disk manifest and the completion marker,
// renames the directory into place, and then verifies it like a warm call.
// A warm call re-hashes the whole tree against the embedded manifest
// (amendment 25: the embedded manifest digest is the trust root). An
// existing runtime that fails any check is refused, never repaired.
func Ensure(home string, e Embedded) (Runtime, Timing, error) {
	start := time.Now()
	m := e.Manifest
	if err := m.Validate(); err != nil {
		return Runtime{}, Timing{}, err
	}
	if m.Platform != runtime.GOOS+"-"+runtime.GOARCH {
		return Runtime{}, Timing{}, fmt.Errorf("pi runtime is for %s, this host is %s-%s", m.Platform, runtime.GOOS, runtime.GOARCH)
	}
	name := m.DirName()
	var tm Timing
	rt, present, err := verifyAnchored(home, name, e, &tm)
	if err != nil {
		return Runtime{}, tm, err
	}
	if present {
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
	// Review 2, item 3: the base is created and held through descriptors,
	// and the whole cold install (lock, staging directory, every directory
	// and file, the manifest, the marker and the final rename) is made
	// relative to that held descriptor with O_NOFOLLOW: no pathname step, so
	// a symlink swapped in at .amq-squad/runtime cannot redirect a write.
	baseDir, err := fsanchor.OpenAnchored(home, RuntimeRel, true)
	if err != nil {
		return Runtime{}, tm, fmt.Errorf("runtime base: %w", err)
	}
	defer baseDir.Close()
	lockFile, err := baseDir.StageLock("." + name + ".lock")
	if err != nil {
		return Runtime{}, tm, err
	}
	lock, err := flock.AcquireExclusiveFile(lockFile)
	if err != nil {
		lockFile.Close()
		return Runtime{}, tm, err
	}
	defer lock.Close()
	// Another process may have finished the install while this one waited.
	if rt, present, err := verifyAnchored(home, name, e, &tm); err != nil {
		return Runtime{}, tm, err
	} else if present {
		tm.Total = time.Since(start)
		return rt, tm, nil
	}
	hashStart := time.Now()
	if coldInstallHook != nil {
		coldInstallHook() // tests: swap the base path after it was opened and re-checked
	}
	var rnd [8]byte
	if _, err := rand.Read(rnd[:]); err != nil {
		return Runtime{}, tm, err
	}
	staging := ".unpack-" + hex.EncodeToString(rnd[:])
	stage, err := baseDir.StageMkdir(staging, 0o700)
	if err != nil {
		return Runtime{}, tm, err
	}
	installed := false
	defer func() {
		if !installed {
			_ = baseDir.StageRemoveAll(staging)
		}
	}()
	err = unpackAnchored(stage, m, payload)
	if err == nil {
		err = stageFile(stage, ManifestName, e.Raw)
	}
	if err == nil {
		err = stageFile(stage, MarkerName, []byte(m.SHA256+"\n"))
	}
	if err == nil {
		err = stage.StageChmod(0o755)
	}
	stage.Close()
	if err != nil {
		return Runtime{}, tm, err
	}
	if err := baseDir.StageRename(staging, name); err != nil {
		return Runtime{}, tm, err
	}
	installed = true
	// The renamed tree is verified through descriptors like any warm launch.
	rt, present, err = verifyAnchored(home, name, e, &tm)
	if err == nil && !present {
		err = errors.New("unpacked runtime vanished before verification")
	}
	if err != nil {
		return Runtime{}, tm, err
	}
	tm.Cold, tm.Rehash, tm.Total = true, time.Since(hashStart), time.Since(start)
	return rt, tm, nil
}

// coldInstallHook is a test seam: it runs after the cold install opened the
// runtime base and before it writes anything.
var coldInstallHook func()

// Reject is a runtime that exists but fails verification.
type Reject struct {
	Dir    string
	Reason error
}

func (r *Reject) Error() string {
	return fmt.Sprintf("pi runtime %s rejected (run `amq-squad doctor --fix` or remove it explicitly to repair): %v", r.Dir, r.Reason)
}

func (r *Reject) Unwrap() error { return r.Reason }

// Open verifies the unpacked runtime of e under home like a launch does and
// returns it, without unpacking: an absent runtime is fs.ErrNotExist (doctor
// and its probe never write the runtime).
func Open(home string, e Embedded) (Runtime, Timing, error) {
	var tm Timing
	rt, present, err := verifyAnchored(home, e.Manifest.DirName(), e, &tm)
	if err == nil && !present {
		err = fs.ErrNotExist
	}
	return rt, tm, err
}

// Verify re-hashes the runtime of e under home (the doctor check). It is the
// same check every launch runs.
func Verify(home string, e Embedded) (Timing, error) {
	var tm Timing
	_, present, err := verifyAnchored(home, e.Manifest.DirName(), e, &tm)
	if err == nil && !present {
		err = fs.ErrNotExist
	}
	return tm, err
}

// verifyAnchored opens home/.amq-squad/runtime/<name> through descriptors
// and re-hashes it. It returns present=false (and no error) only when the
// runtime directory does not exist and no component on the way is a
// symlink or a non-directory.
func verifyAnchored(home, name string, e Embedded, tm *Timing) (Runtime, bool, error) {
	dir := filepath.Join(DefaultBase(home), name)
	reject := func(why error) (Runtime, bool, error) { return Runtime{}, false, &Reject{Dir: dir, Reason: why} }
	root, err := fsanchor.OpenAnchored(home, append(append([]string{}, RuntimeRel...), name), false)
	if err != nil {
		// Absent is a clean miss only when every component that exists is
		// a real directory: check each step by Lstat after the failure.
		if missingCleanly(home, append(append([]string{}, RuntimeRel...), name)) {
			return Runtime{}, false, nil
		}
		return reject(err)
	}
	defer root.Close()
	raw, err := fsanchor.ReadEntry(root, ManifestName)
	if err != nil || digest(raw) != e.Digest {
		return reject(errors.New("on-disk manifest missing or differs from the embedded one"))
	}
	marker, err := fsanchor.ReadEntry(root, MarkerName)
	if err != nil || strings.TrimSpace(string(marker)) != e.Manifest.SHA256 {
		return reject(errors.New("completion marker missing or for another payload"))
	}
	start := time.Now()
	ids, files, bytes, err := rehash(root, e.Manifest)
	tm.Rehash, tm.Files, tm.Bytes = time.Since(start), files, bytes
	if err != nil {
		return reject(err)
	}
	return Runtime{Dir: dir, Manifest: e.Manifest, ManifestSHA256: e.Digest, home: home, name: name, ids: ids}, true, nil
}

// missingCleanly reports whether the first missing component of rel below
// home is preceded only by real directories.
func missingCleanly(home string, rel []string) bool {
	cur := home
	for _, part := range rel {
		cur = filepath.Join(cur, part)
		fi, err := os.Lstat(cur)
		if errors.Is(err, fs.ErrNotExist) {
			return true
		}
		if err != nil || fi.Mode()&fs.ModeSymlink != 0 || !fi.IsDir() {
			return false
		}
	}
	return false
}

// rehash walks the runtime through descriptors: every file must be a
// regular file named in m with its size, mode and SHA-256 (hashed from the
// descriptor that was typed and checked), every directory must be a parent
// of a manifest file, nothing may be missing or extra, and no entry may be a
// symlink, FIFO or device.
func rehash(root *fsanchor.AnchoredDir, m Manifest) (map[string]os.FileInfo, int, int64, error) {
	want := make(map[string]Entry, len(m.Files))
	dirs := map[string]bool{}
	for _, f := range m.Files {
		want[f.Path] = f
		for d := path.Dir(f.Path); d != "."; d = path.Dir(d) {
			dirs[d] = true
		}
	}
	type job struct {
		f *os.File
		e Entry
	}
	jobs := make(chan job, runtime.NumCPU())
	var mu sync.Mutex
	var firstErr error
	ids := make(map[string]os.FileInfo, len(want))
	fail := func(err error) {
		mu.Lock()
		if firstErr == nil {
			firstErr = err
		}
		mu.Unlock()
	}
	var wg sync.WaitGroup
	for i := 0; i < runtime.NumCPU(); i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range jobs {
				fi, err := hashOpened(j.f, j.e)
				j.f.Close()
				if err != nil {
					fail(err)
					continue
				}
				mu.Lock()
				ids[j.e.Path] = fi
				mu.Unlock()
			}
		}()
	}
	seen := 0
	var total int64
	walkErr := fsanchor.WalkAnchored(root, "", []string{ManifestName, MarkerName},
		func(rel string, f *os.File, kind fsanchor.EntryKind, size int64, mode uint32) error {
			switch {
			case kind == fsanchor.KindDir:
				if !dirs[rel] {
					return fmt.Errorf("unexpected runtime directory %q", rel)
				}
				return nil
			case kind != fsanchor.KindRegular:
				return fmt.Errorf("runtime entry %q is not a regular file or directory", rel)
			}
			e, ok := want[rel]
			if !ok {
				return fmt.Errorf("unexpected runtime entry %q", rel)
			}
			if size != e.Size || (runtime.GOOS != "windows" && mode != e.Mode) {
				return fmt.Errorf("type, size or mode mismatch: %s", rel)
			}
			seen++
			total += size
			jobs <- job{f, e}
			return nil
		})
	close(jobs)
	wg.Wait()
	switch {
	case walkErr != nil:
		return nil, 0, 0, walkErr
	case firstErr != nil:
		return nil, 0, 0, firstErr
	case seen != len(want):
		return nil, 0, 0, fmt.Errorf("runtime is missing %d file(s)", len(want)-seen)
	}
	return ids, seen, total, nil
}

// hashOpened hashes the descriptor that was checked and returns its
// identity from the same descriptor.
func hashOpened(in *os.File, f Entry) (os.FileInfo, error) {
	fi, err := in.Stat()
	if err != nil {
		return nil, err
	}
	h := sha256.New()
	if _, err := io.Copy(h, in); err != nil {
		return nil, err
	}
	if hex.EncodeToString(h.Sum(nil)) != f.SHA256 {
		return nil, fmt.Errorf("SHA-256 mismatch: %s", f.Path)
	}
	return fi, nil
}

// LaunchSet is what node opens to start pi: the executable, the entry and
// every file beside it in the bundle directory, the package files (outside
// its node_modules) and the tools.
func (r Runtime) LaunchSet() []string {
	var out []string
	bundle := ""
	if len(r.Manifest.Args) > 0 {
		bundle = path.Dir(r.Manifest.Args[0]) + "/"
	}
	pkg := ""
	if r.Manifest.Package != "" {
		pkg = r.Manifest.Package + "/"
	}
	tools := map[string]bool{}
	for _, p := range r.Manifest.Tools {
		tools[p] = true
	}
	for _, f := range r.Manifest.Files {
		switch {
		case f.Path == r.Manifest.Executable, f.Path == r.Manifest.Preload, tools[f.Path],
			bundle != "" && strings.HasPrefix(f.Path, bundle),
			pkg != "" && strings.HasPrefix(f.Path, pkg) && !strings.HasPrefix(f.Path, pkg+"node_modules/"):
			out = append(out, f.Path)
		}
	}
	return out
}

// CheckLaunch re-opens the launch set through descriptors from the home
// anchor right before exec and refuses when any file is not the one the
// re-hash hashed (another inode, size or modification time): a rename or a
// rewrite between Ensure and exec (review item 2 (a); execve of /dev/fd/N
// is refused with EACCES on macOS, so node cannot be exec'd through the held
// descriptor). The window from this check to execve, and node's own reads
// after it, stay the accepted same-UID limit; the package re-hashes the
// bundle in process (item 2 (b)).
func (r Runtime) CheckLaunch() error {
	if r.ids == nil {
		return errors.New("runtime was not verified by this process")
	}
	root, err := fsanchor.OpenAnchored(r.home, append(append([]string{}, RuntimeRel...), r.name), false)
	if err != nil {
		return &Reject{Dir: r.Dir, Reason: err}
	}
	defer root.Close()
	want := map[string]Entry{}
	for _, f := range r.Manifest.Files {
		want[f.Path] = f
	}
	content := r.contentChecked()
	type job struct {
		f *os.File
		e Entry
	}
	var mu sync.Mutex
	var firstErr error
	fail := func(err error) {
		mu.Lock()
		if firstErr == nil {
			firstErr = err
		}
		mu.Unlock()
	}
	jobs := make(chan job, runtime.NumCPU())
	var wg sync.WaitGroup
	for i := 0; i < runtime.NumCPU(); i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range jobs {
				if _, err := hashOpened(j.f, j.e); err != nil {
					fail(&Reject{Dir: r.Dir, Reason: fmt.Errorf("launch file %s changed after verification: %w", j.e.Path, err)})
				}
				j.f.Close()
			}
		}()
	}
	for _, p := range r.LaunchSet() {
		f, err := fsanchor.OpenPath(root, strings.Split(p, "/"))
		if err != nil {
			fail(&Reject{Dir: r.Dir, Reason: fmt.Errorf("launch file %s: %w", p, err)})
			break
		}
		fi, err := f.Stat()
		old := r.ids[p]
		if err != nil || old == nil || !os.SameFile(old, fi) || old.Size() != fi.Size() || !old.ModTime().Equal(fi.ModTime()) {
			f.Close()
			fail(&Reject{Dir: r.Dir, Reason: fmt.Errorf("launch file %s changed after verification", p)})
			break
		}
		if content[p] {
			// Review 2, item 2 (a): the files the first instructions run are
			// re-hashed by content, so an equal-length rewrite with its mtime
			// restored is caught.
			jobs <- job{f, want[p]}
			continue
		}
		f.Close()
	}
	close(jobs)
	wg.Wait()
	return firstErr
}

// contentChecked is the part of the launch set CheckLaunch re-hashes by
// content: node, the preload, the pi entry and the package's files (outside
// its node_modules); the rest of the launch set is checked by identity.
func (r Runtime) contentChecked() map[string]bool {
	out := map[string]bool{r.Manifest.Executable: true}
	if r.Manifest.Preload != "" {
		out[r.Manifest.Preload] = true
	}
	for _, a := range r.Manifest.Args {
		out[a] = true
	}
	if r.Manifest.Package != "" {
		pkg := r.Manifest.Package + "/"
		for _, f := range r.Manifest.Files {
			if strings.HasPrefix(f.Path, pkg) && !strings.HasPrefix(f.Path, pkg+"node_modules/") {
				out[f.Path] = true
			}
		}
	}
	return out
}

// unpackAnchored writes the payload below the staging directory through
// descriptors only (review 2, item 3): each directory is made relative to
// its parent's descriptor and opened without following a symlink, each file
// is created relative to its directory with O_EXCL|O_NOFOLLOW, written,
// and given its final mode through its own descriptor.
func unpackAnchored(stage *fsanchor.AnchoredDir, m Manifest, payload []byte) error {
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
	dirs := map[string]*fsanchor.AnchoredDir{}
	defer func() {
		for _, d := range dirs {
			d.Close()
		}
	}()
	dirOf := func(rel string) (*fsanchor.AnchoredDir, error) {
		if rel == "." {
			return stage, nil
		}
		cur, curPath := stage, ""
		for _, part := range strings.Split(rel, "/") {
			if curPath == "" {
				curPath = part
			} else {
				curPath += "/" + part
			}
			if d, ok := dirs[curPath]; ok {
				cur = d
				continue
			}
			d, err := cur.StageMkdir(part, 0o755)
			if err != nil {
				return nil, fmt.Errorf("staging %s: %w", curPath, err)
			}
			dirs[curPath] = d
			cur = d
		}
		return cur, nil
	}
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
		parent, err := dirOf(path.Dir(h.Name))
		if err != nil {
			return err
		}
		out, err := parent.StageCreate(path.Base(h.Name), f.Mode|0o200)
		if err != nil {
			return fmt.Errorf("staging %s: %w", h.Name, err)
		}
		_, copyErr := io.CopyN(out, tr, f.Size)
		chmodErr := out.Chmod(fs.FileMode(f.Mode))
		closeErr := out.Close()
		if copyErr != nil {
			return copyErr
		}
		if chmodErr != nil {
			return chmodErr
		}
		if closeErr != nil {
			return closeErr
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

// stageFile writes a read-only file relative to d through a descriptor.
func stageFile(d *fsanchor.AnchoredDir, name string, b []byte) error {
	f, err := d.StageCreate(name, 0o644)
	if err != nil {
		return err
	}
	_, werr := f.Write(b)
	cerr := f.Chmod(0o444)
	if err := f.Close(); werr == nil && cerr == nil {
		werr = err
	}
	if werr != nil {
		return werr
	}
	return cerr
}
