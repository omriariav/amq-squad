package registry

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"github.com/omriariav/amq-squad/v3/internal/fsanchor"
)

// The embedded pi runtime (R1, S3) is unpacked under
// ~/.amq-squad/runtime/<version>-<sha256[:12]>/ with an on-disk manifest of
// every file (path, size, mode, SHA-256) and a completion marker.
const (
	RuntimeManifestName = ".embedpi-manifest.json"
	RuntimeMarkerName   = ".embedpi-complete"
)

type runtimeFile struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
	Mode   uint32 `json:"mode"`
}

// EmbeddedRuntimeManifestSHA256 is the SHA-256 of the runtime manifest the
// binary embeds. The build that embeds the pi runtime (4-1) sets it; in a
// build that embeds none it is "", and no on-disk runtime can be trusted.
var EmbeddedRuntimeManifestSHA256 = ""

// EmbeddedRuntimeDirName is the runtime directory name (<version>-<digest12>)
// of the payload the binary embeds, set with EmbeddedRuntimeManifestSHA256 by
// the 4-1 build (cli: syncRegistryRuntimePin); "" in a build without one.
var EmbeddedRuntimeDirName = ""

// RuntimeManifest is the part of a verified manifest that names the pi
// command. It comes only from the bytes RehashRuntime verified.
type RuntimeManifest struct {
	Version    string   `json:"version"`
	Executable string   `json:"executable"`
	Args       []string `json:"args"`
}

// RehashResult is one full re-hash.
type RehashResult struct {
	Files    int
	Bytes    int64
	Dir      string          // the verified runtime directory
	Manifest RuntimeManifest // parsed from the verified manifest bytes
}

// RehashRuntime re-hashes every file of the unpacked runtime at base/rel
// against its on-disk manifest: no file missing, no entry extra (files or
// directories), no symlink, FIFO or device at any depth, each file with the
// manifest's size, mode and SHA-256. Ruled for 2b-3 (S3 open question 1):
// the full re-hash runs at every launch. wantManifestSHA is the digest of
// the manifest the binary embeds and is required.
//
// base is the trusted anchor (the home directory). Every component of rel
// and every entry below it is opened through its parent's descriptor with
// O_NOFOLLOW; directories are listed through their descriptors, never by
// pathname; each file is typed, checked and hashed from the descriptor that
// was opened. The manifest is read the same way.
func RehashRuntime(base, rel, wantManifestSHA string) (RehashResult, error) {
	if wantManifestSHA == "" {
		return RehashResult{}, errors.New("no trusted runtime manifest digest: this build embeds no pi runtime")
	}
	parts := strings.Split(filepath.ToSlash(filepath.Clean(rel)), "/")
	root, err := fsanchor.OpenAnchored(base, parts, false)
	if err != nil {
		return RehashResult{}, fmt.Errorf("runtime root: %w", err)
	}
	defer root.Close()
	raw, err := fsanchor.ReadEntry(root, RuntimeManifestName)
	if err != nil {
		return RehashResult{}, fmt.Errorf("runtime manifest: %w", err)
	}
	sum := sha256.Sum256(raw)
	if hex.EncodeToString(sum[:]) != wantManifestSHA {
		return RehashResult{}, errors.New("on-disk runtime manifest differs from the embedded one")
	}
	var m struct {
		RuntimeManifest
		Files []runtimeFile `json:"files"`
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		return RehashResult{}, fmt.Errorf("runtime manifest: %w", err)
	}
	want := make(map[string]runtimeFile, len(m.Files))
	dirs := map[string]bool{}
	res := RehashResult{Dir: filepath.Join(base, rel), Manifest: m.RuntimeManifest}
	for _, f := range m.Files {
		want[f.Path] = f
		res.Bytes += f.Size
		for d := path.Dir(f.Path); d != "."; d = path.Dir(d) {
			dirs[d] = true
		}
	}
	type job struct {
		f *os.File
		e runtimeFile
	}
	jobs := make(chan job, runtime.NumCPU())
	var mu sync.Mutex
	var firstErr error
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
				if err := hashOpened(j.f, j.e); err != nil {
					fail(err)
				}
				j.f.Close()
			}
		}()
	}
	seen := 0
	walkErr := fsanchor.WalkAnchored(root, "", []string{RuntimeManifestName, RuntimeMarkerName}, func(rel string, f *os.File, kind fsanchor.EntryKind, size int64, mode uint32) error {
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
		jobs <- job{f, e}
		return nil
	})
	close(jobs)
	wg.Wait()
	switch {
	case walkErr != nil:
		return RehashResult{}, walkErr
	case firstErr != nil:
		return RehashResult{}, firstErr
	case seen != len(want):
		return RehashResult{}, fmt.Errorf("runtime is missing %d file(s)", len(want)-seen)
	}
	res.Files = seen
	return res, nil
}

// hashOpened hashes the descriptor that was checked.
func hashOpened(in *os.File, f runtimeFile) error {
	h := sha256.New()
	if _, err := io.Copy(h, in); err != nil {
		return err
	}
	if hex.EncodeToString(h.Sum(nil)) != f.SHA256 {
		return fmt.Errorf("SHA-256 mismatch: %s", f.Path)
	}
	return nil
}
