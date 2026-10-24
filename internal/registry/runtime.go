package registry

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path/filepath"
	"runtime"
	"sync"
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

// RehashResult is one full re-hash.
type RehashResult struct {
	Files int
	Bytes int64
}

// EmbeddedRuntimeManifestSHA256 is the SHA-256 of the runtime manifest the
// binary embeds. The build that embeds the pi runtime (4-1) sets it; in a
// build that embeds none it is "", and no on-disk runtime can be trusted.
var EmbeddedRuntimeManifestSHA256 = ""

// RehashRuntime re-hashes every file of an unpacked runtime against its
// on-disk manifest: no file missing, none extra, no symlink at any depth,
// each with the manifest's size, mode and SHA-256. Ruled for 2b-3 (S3 open
// question 1): the full re-hash runs at every launch, not only the
// executable subset. wantManifestSHA is the SHA-256 of the manifest the
// binary embeds and is required: an on-disk manifest is replaceable, so a
// matching file list proves nothing without the trusted digest. Every file,
// the manifest included, is opened from the root one component at a time
// without following a symlink, and the opened descriptor is what is
// checked and hashed (no check-then-open window).
func RehashRuntime(dir, wantManifestSHA string) (RehashResult, error) {
	if wantManifestSHA == "" {
		return RehashResult{}, errors.New("no trusted runtime manifest digest: this build embeds no pi runtime")
	}
	root, err := openTrustedRoot(dir)
	if err != nil {
		return RehashResult{}, err
	}
	defer root.Close()
	raw, err := readTrusted(root, RuntimeManifestName)
	if err != nil {
		return RehashResult{}, fmt.Errorf("runtime manifest: %w", err)
	}
	sum := sha256.Sum256(raw)
	if hex.EncodeToString(sum[:]) != wantManifestSHA {
		return RehashResult{}, errors.New("on-disk runtime manifest differs from the embedded one")
	}
	var m struct {
		Files []runtimeFile `json:"files"`
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		return RehashResult{}, fmt.Errorf("runtime manifest: %w", err)
	}
	want := make(map[string]runtimeFile, len(m.Files))
	var res RehashResult
	for _, f := range m.Files {
		want[f.Path] = f
		res.Bytes += f.Size
	}
	// Nothing outside the manifest: every entry is a real directory or a
	// listed regular file.
	err = filepath.WalkDir(dir, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if p == dir {
			return nil
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		switch {
		case d.Type()&fs.ModeSymlink != 0:
			return fmt.Errorf("runtime entry %s is a symlink", rel)
		case d.IsDir():
			return nil
		case rel == RuntimeManifestName || rel == RuntimeMarkerName:
			return nil
		}
		if _, ok := want[rel]; !ok || !d.Type().IsRegular() {
			return fmt.Errorf("unexpected runtime entry %q", rel)
		}
		return nil
	})
	if err != nil {
		return RehashResult{}, err
	}
	errs := make(chan error, len(want))
	sem := make(chan struct{}, runtime.NumCPU())
	var wg sync.WaitGroup
	for _, f := range want {
		wg.Add(1)
		go func(f runtimeFile) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			errs <- checkRuntimeFile(root, f)
		}(f)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			return RehashResult{}, err
		}
	}
	res.Files = len(want)
	return res, nil
}

// readTrusted reads a regular file opened through the trusted root.
func readTrusted(root *trustedRoot, rel string) ([]byte, error) {
	f, err := root.Open(rel)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !st.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file", rel)
	}
	return io.ReadAll(io.LimitReader(f, 64<<20))
}

// checkRuntimeFile checks and hashes the descriptor it opened, so the file
// checked is the file hashed.
func checkRuntimeFile(root *trustedRoot, f runtimeFile) error {
	in, err := root.Open(f.Path)
	if err != nil {
		return err
	}
	defer in.Close()
	st, err := in.Stat()
	if err != nil {
		return err
	}
	if !st.Mode().IsRegular() || st.Size() != f.Size || (runtime.GOOS != "windows" && uint32(st.Mode().Perm()) != f.Mode) {
		return fmt.Errorf("type, size or mode mismatch: %s", f.Path)
	}
	h := sha256.New()
	if _, err := io.Copy(h, in); err != nil {
		return err
	}
	if hex.EncodeToString(h.Sum(nil)) != f.SHA256 {
		return fmt.Errorf("SHA-256 mismatch: %s", f.Path)
	}
	return nil
}
