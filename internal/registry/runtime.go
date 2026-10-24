package registry

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
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

// RehashRuntime re-hashes every file of an unpacked runtime against its
// on-disk manifest: no file missing, none extra, none a symlink, each with
// the manifest's size, mode and SHA-256. Ruled for 2b-3 (S3 open question 1):
// the full re-hash runs at every launch (S3 measured 190 ms), not only the
// executable subset. wantManifestSHA, when set, is the SHA-256 of the
// manifest the binary embeds; a different on-disk manifest is refused.
func RehashRuntime(dir, wantManifestSHA string) (RehashResult, error) {
	raw, err := os.ReadFile(filepath.Join(dir, RuntimeManifestName))
	if err != nil {
		return RehashResult{}, fmt.Errorf("runtime manifest: %w", err)
	}
	if wantManifestSHA != "" {
		sum := sha256.Sum256(raw)
		if hex.EncodeToString(sum[:]) != wantManifestSHA {
			return RehashResult{}, errors.New("on-disk runtime manifest differs from the embedded one")
		}
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
	var paths []string
	err = filepath.WalkDir(dir, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			if d.Type()&fs.ModeSymlink != 0 {
				return fmt.Errorf("runtime entry %s is a symlink", p)
			}
			return nil
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if rel == RuntimeManifestName || rel == RuntimeMarkerName {
			return nil
		}
		if _, ok := want[rel]; !ok || !d.Type().IsRegular() {
			return fmt.Errorf("unexpected runtime entry %q", rel)
		}
		paths = append(paths, rel)
		return nil
	})
	if err != nil {
		return RehashResult{}, err
	}
	if len(paths) != len(want) {
		return RehashResult{}, fmt.Errorf("runtime is missing %d file(s)", len(want)-len(paths))
	}
	errs := make(chan error, len(paths))
	sem := make(chan struct{}, runtime.NumCPU())
	var wg sync.WaitGroup
	for _, rel := range paths {
		wg.Add(1)
		go func(f runtimeFile) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			errs <- checkRuntimeFile(filepath.Join(dir, filepath.FromSlash(f.Path)), f)
		}(want[rel])
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			return RehashResult{}, err
		}
	}
	res.Files = len(paths)
	return res, nil
}

func checkRuntimeFile(p string, f runtimeFile) error {
	st, err := os.Lstat(p)
	if err != nil {
		return err
	}
	if !st.Mode().IsRegular() || st.Size() != f.Size || (runtime.GOOS != "windows" && uint32(st.Mode().Perm()) != f.Mode) {
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
