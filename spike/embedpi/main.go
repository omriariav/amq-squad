// embedpi is the amq-squad 3.0 S3 spike launcher, not the production binary.
//
// It embeds a pinned pi runtime payload (assets/payload.tar.gz plus
// assets/manifest.json, produced by build.sh), unpacks it once to
// ~/.amq-squad/runtime/<version>-<digest12>/ with SHA-256 verification, and
// then execs pi from that copy with absolute paths. It never looks up pi,
// node or bun on PATH.
//
// Environment knobs (spike only):
//
//	EMBEDPI_TIMING=1      print cache hit and unpack/verify time to stderr
//	EMBEDPI_VERIFY=full   on a warm start, re-hash every unpacked file
//	EMBEDPI_RUNTIME_DIR   override ~/.amq-squad/runtime (tests, measurements)
package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"
)

//go:embed assets/*
var assets embed.FS

// stampName marks a fully unpacked and verified runtime. It is written inside
// the staging directory before the atomic rename, so a directory that exists
// under its final name is always complete.
const stampName = ".embedpi-complete"

type entry struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
	Mode   uint32 `json:"mode"`
}

type manifest struct {
	Version    string            `json:"version"`
	Platform   string            `json:"platform"`
	SHA256     string            `json:"sha256"`
	Executable string            `json:"executable"`
	Args       []string          `json:"args"`
	Env        map[string]string `json:"env,omitempty"` // values may use ${RUNTIME}
	Files      []entry           `json:"files"`
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "embedpi:", err)
		os.Exit(1)
	}
}

func run() error {
	raw, err := assets.ReadFile("assets/manifest.json")
	if err != nil {
		return errors.New("no pi payload embedded; run spike/embedpi/build.sh first")
	}
	var m manifest
	if err = json.Unmarshal(raw, &m); err != nil {
		return err
	}
	if m.Platform != runtime.GOOS+"-"+runtime.GOARCH {
		return fmt.Errorf("payload platform %s does not match host %s-%s", m.Platform, runtime.GOOS, runtime.GOARCH)
	}
	base := os.Getenv("EMBEDPI_RUNTIME_DIR")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return err
		}
		base = filepath.Join(home, ".amq-squad", "runtime")
	}
	start := time.Now()
	load := func() ([]byte, error) { return assets.ReadFile("assets/payload.tar.gz") }
	dir, warm, err := ensure(base, m, load, os.Getenv("EMBEDPI_VERIFY") == "full")
	if err != nil {
		return err
	}
	if os.Getenv("EMBEDPI_TIMING") == "1" {
		fmt.Fprintf(os.Stderr, "embedpi cache_hit=%t unpack_verify_ms=%.3f runtime=%s\n",
			warm, float64(time.Since(start).Microseconds())/1000, dir)
	}
	exe := filepath.Join(dir, filepath.FromSlash(m.Executable))
	argv := []string{exe}
	for _, arg := range m.Args {
		argv = append(argv, filepath.Join(dir, filepath.FromSlash(arg)))
	}
	argv = append(argv, os.Args[1:]...)
	env := os.Environ()
	for k, v := range m.Env {
		env = append(env, k+"="+strings.ReplaceAll(v, "${RUNTIME}", dir))
	}
	// Absolute paths only. No PATH lookup and no host Node fallback.
	return syscall.Exec(exe, argv, env)
}

func local(p string) bool {
	return p != "." && path.Clean(p) == p && filepath.IsLocal(p) && !strings.Contains(p, "\\")
}

func digest(b []byte) string { sum := sha256.Sum256(b); return hex.EncodeToString(sum[:]) }

func validDigest(s string) bool {
	b, err := hex.DecodeString(s)
	return err == nil && len(b) == sha256.Size && strings.ToLower(s) == s
}

func (m manifest) validate() error {
	if !local(m.Version) || strings.Contains(m.Version, "/") || !validDigest(m.SHA256) {
		return errors.New("invalid version or payload digest")
	}
	if !local(m.Executable) || len(m.Files) == 0 {
		return errors.New("invalid executable or empty manifest")
	}
	seen := map[string]entry{}
	var total int64
	for _, f := range m.Files {
		if !local(f.Path) || f.Path == stampName || !validDigest(f.SHA256) || f.Size < 0 || f.Size > 1<<30 || (f.Mode != 0444 && f.Mode != 0555) {
			return fmt.Errorf("invalid manifest entry %q", f.Path)
		}
		if _, exists := seen[f.Path]; exists {
			return fmt.Errorf("duplicate manifest entry %q", f.Path)
		}
		seen[f.Path] = f
		total += f.Size
		if total > 1<<30 {
			return errors.New("unpacked payload exceeds prototype 1 GiB limit")
		}
	}
	if seen[m.Executable].Mode != 0555 {
		return errors.New("executable is missing or not executable")
	}
	for _, a := range m.Args {
		if !local(a) {
			return errors.New("nonlocal entry argument")
		}
		if _, ok := seen[a]; !ok {
			return errors.New("entry argument missing from manifest")
		}
	}
	return nil
}

// ensure returns the unpacked runtime directory and whether it was already
// present (warm). A warm start does not touch the embedded payload: it checks
// the completion stamp and the executable, or re-hashes everything when full
// is set. A cold start verifies the embedded payload digest before writing.
func ensure(base string, m manifest, load func() ([]byte, error), full bool) (string, bool, error) {
	if err := m.validate(); err != nil {
		return "", false, err
	}
	name := m.Version + "-" + m.SHA256[:12]
	target := filepath.Join(base, name)
	// Fast path without the lock: a renamed directory is complete by construction.
	if ok, err := warmOK(target, m, full); err != nil {
		return "", false, err
	} else if ok {
		return target, true, nil
	}
	payload, err := load()
	if err != nil {
		return "", false, err
	}
	if digest(payload) != m.SHA256 {
		return "", false, errors.New("embedded payload SHA-256 mismatch")
	}
	if err := os.MkdirAll(base, 0700); err != nil {
		return "", false, err
	}
	info, err := os.Lstat(base)
	if err != nil || !info.IsDir() {
		return "", false, errors.New("runtime base is not a real directory")
	}
	// Serialize installation; the OS releases flock even after a process crash.
	fd, err := syscall.Open(filepath.Join(base, "."+name+".lock"), syscall.O_CREAT|syscall.O_RDWR|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return "", false, err
	}
	defer syscall.Close(fd)
	if err = syscall.Flock(fd, syscall.LOCK_EX); err != nil {
		return "", false, err
	}
	defer syscall.Flock(fd, syscall.LOCK_UN)
	// Another process may have finished while we waited for the lock.
	if ok, err := warmOK(target, m, full); err != nil {
		return "", false, err
	} else if ok {
		return target, true, nil
	}
	tmp, err := os.MkdirTemp(base, ".unpack-")
	if err != nil {
		return "", false, err
	}
	defer os.RemoveAll(tmp)
	if err = unpack(tmp, m, payload); err != nil {
		return "", false, err
	}
	if err = verify(tmp, m); err != nil {
		return "", false, err
	}
	if err = os.WriteFile(filepath.Join(tmp, stampName), []byte(m.SHA256+"\n"), 0444); err != nil {
		return "", false, err
	}
	if err = os.Chmod(tmp, 0755); err != nil {
		return "", false, err
	}
	if err = os.Rename(tmp, target); err != nil {
		return "", false, err
	}
	return target, false, nil
}

// warmOK reports whether target holds a complete runtime for m. It returns
// (false, nil) when target is absent and an error when target exists but is
// not a valid runtime (fail closed; the operator removes it explicitly).
func warmOK(target string, m manifest, full bool) (bool, error) {
	info, err := os.Lstat(target)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	reject := func(why error) (bool, error) {
		return false, fmt.Errorf("existing runtime %s rejected (remove it explicitly to repair): %w", target, why)
	}
	if !info.IsDir() {
		return reject(errors.New("not a directory"))
	}
	stamp, err := os.ReadFile(filepath.Join(target, stampName))
	if err != nil || strings.TrimSpace(string(stamp)) != m.SHA256 {
		return reject(errors.New("completion stamp missing or for another payload"))
	}
	if full {
		if err := verify(target, m); err != nil {
			return reject(err)
		}
		return true, nil
	}
	// Cheap checks on the files the launch depends on directly.
	for _, f := range m.Files {
		if f.Path != m.Executable && !contains(m.Args, f.Path) {
			continue
		}
		fi, err := os.Lstat(filepath.Join(target, filepath.FromSlash(f.Path)))
		if err != nil || !fi.Mode().IsRegular() || fi.Size() != f.Size || uint32(fi.Mode().Perm()) != f.Mode {
			return reject(fmt.Errorf("launch file %s missing or changed", f.Path))
		}
	}
	return true, nil
}

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

func unpack(dir string, m manifest, payload []byte) error {
	gz, err := gzip.NewReader(bytes.NewReader(payload))
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	wanted := map[string]entry{}
	for _, f := range m.Files {
		wanted[f.Path] = f
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
		f, ok := wanted[h.Name]
		// The packer emits regular files only; reject links, devices and directories.
		if !ok || seen[h.Name] || h.Typeflag != tar.TypeReg || h.Size != f.Size {
			return fmt.Errorf("unexpected archive entry %q", h.Name)
		}
		seen[h.Name] = true
		dest := filepath.Join(dir, filepath.FromSlash(h.Name))
		if parent := filepath.Dir(dest); !made[parent] {
			if err := os.MkdirAll(parent, 0755); err != nil {
				return err
			}
			made[parent] = true
		}
		out, err := os.OpenFile(dest, os.O_CREATE|os.O_EXCL|os.O_WRONLY, fs.FileMode(f.Mode)|0200)
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
		if err = os.Chmod(dest, fs.FileMode(f.Mode)); err != nil {
			return err
		}
	}
	// Consume the gzip footer so truncation and CRC failures cannot be ignored.
	if _, err = io.Copy(io.Discard, gz); err != nil {
		return err
	}
	if len(seen) != len(wanted) {
		return errors.New("archive is missing files")
	}
	return nil
}

func verify(dir string, m manifest) error {
	wanted := map[string]entry{}
	for _, f := range m.Files {
		wanted[f.Path] = f
	}
	count := 0
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		if rel == stampName {
			return nil
		}
		f, ok := wanted[filepath.ToSlash(rel)]
		if !ok || !d.Type().IsRegular() {
			return fmt.Errorf("unexpected runtime entry %q", rel)
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if info.Size() != f.Size || uint32(info.Mode().Perm()) != f.Mode {
			return fmt.Errorf("size/mode mismatch: %s", rel)
		}
		in, err := os.Open(p)
		if err != nil {
			return err
		}
		hash := sha256.New()
		_, readErr := io.Copy(hash, in)
		closeErr := in.Close()
		if readErr != nil {
			return readErr
		}
		if closeErr != nil {
			return closeErr
		}
		if hex.EncodeToString(hash.Sum(nil)) != f.SHA256 {
			return fmt.Errorf("SHA-256 mismatch: %s", rel)
		}
		count++
		return nil
	})
	if err != nil {
		return err
	}
	if count != len(wanted) {
		return errors.New("runtime is missing files")
	}
	return nil
}
