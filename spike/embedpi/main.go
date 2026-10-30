// embedpi is the amq-squad 3.0 S3 spike launcher, not the production binary.
//
// It embeds a pinned pi runtime payload (assets/payload.tar.gz plus
// assets/manifest.json, produced by build.sh), unpacks it once to
// ~/.amq-squad/runtime/<version>-<digest12>/ with SHA-256 verification, and
// then execs pi from that copy with absolute paths. It never looks up pi,
// node or bun on PATH.
//
// Unless the caller sets them, pi gets the 2b defaults PI_OFFLINE=1,
// PI_SKIP_VERSION_CHECK=1, PI_TELEMETRY=0 and a seat-specific
// PI_CODING_AGENT_DIR=~/.amq-squad/pi-agent/<seat> (seat from
// AMQ_SQUAD_SEAT, then AM_ME, else "lead"); the embedded fd and rg are
// linked into <agent-dir>/bin, where pi looks before PATH or a download.
// These do not isolate the network: model requests still go to the provider.
//
// Every warm launch runs launchCheck (manifest hash, SHA-256 of every
// executable, no symlinks on the launch paths); see there for the limits.
//
// Environment knobs (spike only):
//
//	EMBEDPI_TIMING=1      print cache hit and install/check time to stderr
//	EMBEDPI_VERIFY=full   doctor mode: re-hash every unpacked file
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
	"sync"
	"syscall"
	"time"
)

//go:embed assets/*
var assets embed.FS

// stampName marks a fully unpacked and verified runtime. It is written inside
// the staging directory before the atomic rename, so a directory that exists
// under its final name is always complete.
const stampName = ".embedpi-complete"

// manifestName is the per-file manifest (path, size, mode, SHA-256) written
// at cold install; it is byte-identical to the embedded assets/manifest.json.
const manifestName = ".embedpi-manifest.json"

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
	// Tools maps a helper name pi looks up in <agent-dir>/bin (fd, rg) to its
	// payload path. The launcher links them there so pi never downloads them.
	Tools map[string]string `json:"tools,omitempty"`
	Files []entry           `json:"files"`

	raw []byte // the embedded manifest bytes, as read from assets
}

// rawBytes returns the manifest bytes the on-disk copy must equal.
func (m manifest) rawBytes() []byte {
	if m.raw != nil {
		return m.raw
	}
	b, _ := json.Marshal(m) // tests build manifests in code
	return b
}

// seatName picks the seat for the per-seat pi agent directory.
func seatName() string {
	for _, k := range []string{"AMQ_SQUAD_SEAT", "AM_ME"} {
		if v := os.Getenv(k); v != "" && v != "." && v != ".." &&
			strings.Trim(v, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789._-") == "" {
			return v
		}
	}
	return "lead"
}

// piDefaults are the 2b defaults for the embedded lead and pi seats. Each is
// applied only when the caller has not set the variable.
func piDefaults(home string) [][2]string {
	return [][2]string{
		{"PI_OFFLINE", "1"},
		{"PI_SKIP_VERSION_CHECK", "1"},
		{"PI_TELEMETRY", "0"},
		{"PI_CODING_AGENT_DIR", filepath.Join(home, ".amq-squad", "pi-agent", seatName())},
	}
}

// linkTools points <agentDir>/bin/<name> at the unpacked helper. An existing
// symlink (for example from an older runtime) is replaced atomically; a
// regular file the user put there is left alone.
func linkTools(agentDir, runtimeDir string, tools map[string]string) error {
	if len(tools) == 0 {
		return nil
	}
	bin := filepath.Join(agentDir, "bin")
	if err := os.MkdirAll(bin, 0700); err != nil {
		return err
	}
	for name, rel := range tools {
		want := filepath.Join(runtimeDir, filepath.FromSlash(rel))
		dst := filepath.Join(bin, name)
		if cur, err := os.Readlink(dst); err == nil && cur == want {
			continue
		} else if err != nil {
			if fi, statErr := os.Lstat(dst); statErr == nil && fi.Mode()&fs.ModeSymlink == 0 {
				continue // user-provided binary
			}
		}
		tmp := dst + ".embedpi-tmp"
		_ = os.Remove(tmp)
		if err := os.Symlink(want, tmp); err != nil {
			return err
		}
		if err := os.Rename(tmp, dst); err != nil {
			return err
		}
	}
	return nil
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "embedpi:", err)
		os.Exit(1)
	}
}

func run() error {
	start := time.Now() // includes manifest read and JSON parse
	raw, err := assets.ReadFile("assets/manifest.json")
	if err != nil {
		return errors.New("no pi payload embedded; run spike/embedpi/build.sh first")
	}
	var m manifest
	if err = json.Unmarshal(raw, &m); err != nil {
		return err
	}
	m.raw = raw
	if m.Platform != runtime.GOOS+"-"+runtime.GOARCH {
		return fmt.Errorf("payload platform %s does not match host %s-%s", m.Platform, runtime.GOOS, runtime.GOARCH)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	base := os.Getenv("EMBEDPI_RUNTIME_DIR")
	if base == "" {
		base = filepath.Join(home, ".amq-squad", "runtime")
	}
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
	for _, kv := range piDefaults(home) {
		if _, set := os.LookupEnv(kv[0]); !set {
			env = append(env, kv[0]+"="+kv[1])
		}
	}
	for k, v := range m.Env {
		env = append(env, k+"="+strings.ReplaceAll(v, "${RUNTIME}", dir))
	}
	agentDir := os.Getenv("PI_CODING_AGENT_DIR")
	if agentDir == "" {
		agentDir = filepath.Join(home, ".amq-squad", "pi-agent", seatName())
	}
	if err := linkTools(agentDir, dir, m.Tools); err != nil {
		return fmt.Errorf("link fd/rg into %s/bin: %w", agentDir, err)
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
		if !local(f.Path) || f.Path == stampName || f.Path == manifestName || !validDigest(f.SHA256) || f.Size < 0 || f.Size > 1<<30 || (f.Mode != 0444 && f.Mode != 0555) {
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
	for name, p := range m.Tools {
		if name == "" || strings.ContainsAny(name, "/\\") || !local(p) || seen[p].Mode != 0555 {
			return fmt.Errorf("invalid tool %q", name)
		}
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
// present (warm). A cold start verifies the embedded payload digest before
// writing, re-hashes every unpacked file, then writes the on-disk manifest
// and the completion marker before an atomic rename.
//
// A warm start never reads the payload. By default it runs launchCheck (see
// there). full=true (doctor, EMBEDPI_VERIFY=full) re-hashes every file. An
// existing directory without a valid marker gets a full re-hash; if that
// passes the marker is written, otherwise the launch is refused.
func ensure(base string, m manifest, load func() ([]byte, error), full bool) (string, bool, error) {
	if err := m.validate(); err != nil {
		return "", false, err
	}
	raw := m.rawBytes()
	name := m.Version + "-" + m.SHA256[:12]
	target := filepath.Join(base, name)
	// Fast path without the lock: a renamed directory is complete by construction.
	if ok, err := warmOK(target, m, raw, full); err != nil {
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
	if ok, err := warmOK(target, m, raw, full); err != nil {
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
	if err = writeMarker(tmp, m, raw); err != nil {
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

// writeMarker writes the per-file manifest (path, size, mode, SHA-256) and
// then the completion marker, which records the payload digest.
func writeMarker(dir string, m manifest, raw []byte) error {
	mf := filepath.Join(dir, manifestName)
	_ = os.Remove(mf)
	if err := os.WriteFile(mf, raw, 0444); err != nil {
		return err
	}
	st := filepath.Join(dir, stampName)
	_ = os.Remove(st)
	return os.WriteFile(st, []byte(m.SHA256+"\n"), 0444)
}

// warmOK reports whether target holds a valid runtime for m. It returns
// (false, nil) only when target is absent. When target exists but fails a
// check it returns an error and the launcher exits 1 without exec'ing pi; it
// never repairs or deletes the directory itself.
func warmOK(target string, m manifest, raw []byte, full bool) (bool, error) {
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
		return reject(errors.New("not a real directory (symlink or file)"))
	}
	stamp, err := os.ReadFile(filepath.Join(target, stampName))
	_, mfErr := os.Lstat(filepath.Join(target, manifestName))
	if err != nil || strings.TrimSpace(string(stamp)) != m.SHA256 || errors.Is(mfErr, fs.ErrNotExist) {
		// No valid marker (or a runtime from a launcher that predates the
		// on-disk manifest): hash everything, and only then mark it complete.
		if err := verify(target, m); err != nil {
			return reject(fmt.Errorf("no valid completion marker and full re-hash failed: %w", err))
		}
		if err := writeMarker(target, m, raw); err != nil {
			return reject(err)
		}
		return true, nil
	}
	if full {
		if err := verify(target, m); err != nil {
			return reject(err)
		}
		return true, nil
	}
	if err := launchCheck(target, m, raw); err != nil {
		return reject(err)
	}
	return true, nil
}

// launchCheck is the integrity check run on every warm launch:
//  1. the on-disk manifest is byte-identical to the embedded one (SHA-256);
//  2. every executable file (mode 0555: Node or the Bun binary, native .node
//     addons, fd, rg, and any script with an exec bit) is a regular file
//     with the manifest's size and mode and its full SHA-256 matches;
//  3. no component of the path from the runtime root to the executable, the
//     entry script or a tool is a symlink, and each intermediate is a
//     directory.
//
// Non-executable files (the JavaScript tree, assets) are only re-hashed on a
// cold install, when the marker is absent, or under full (doctor).
func launchCheck(target string, m manifest, raw []byte) error {
	disk, err := os.ReadFile(filepath.Join(target, manifestName))
	if err != nil || digest(disk) != digest(raw) {
		return errors.New("on-disk manifest missing or differs from the embedded one")
	}
	paths := append([]string{m.Executable}, m.Args...)
	for _, p := range m.Tools {
		paths = append(paths, p)
	}
	for _, p := range paths {
		if err := noSymlinks(target, p); err != nil {
			return err
		}
	}
	var exec []entry
	for _, f := range m.Files {
		if f.Mode == 0555 {
			exec = append(exec, f)
		}
	}
	errs := make(chan error, len(exec))
	var wg sync.WaitGroup
	sem := make(chan struct{}, runtime.NumCPU())
	for _, f := range exec {
		wg.Add(1)
		go func(f entry) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			errs <- checkFile(filepath.Join(target, filepath.FromSlash(f.Path)), f)
		}(f)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}

// noSymlinks walks rel from root and rejects any symlinked component.
func noSymlinks(root, rel string) error {
	parts := strings.Split(rel, "/")
	cur := root
	for i, part := range parts {
		cur = filepath.Join(cur, part)
		fi, err := os.Lstat(cur)
		if err != nil {
			return fmt.Errorf("launch path %s: %w", rel, err)
		}
		if fi.Mode()&fs.ModeSymlink != 0 {
			return fmt.Errorf("launch path %s: %s is a symlink", rel, cur)
		}
		if last := i == len(parts)-1; !last && !fi.IsDir() {
			return fmt.Errorf("launch path %s: %s is not a directory", rel, cur)
		} else if last && !fi.Mode().IsRegular() {
			return fmt.Errorf("launch path %s is not a regular file", rel)
		}
	}
	return nil
}

// checkFile compares one unpacked file with its manifest entry.
func checkFile(p string, f entry) error {
	fi, err := os.Lstat(p)
	if err != nil {
		return err
	}
	if !fi.Mode().IsRegular() || fi.Size() != f.Size || uint32(fi.Mode().Perm()) != f.Mode {
		return fmt.Errorf("type/size/mode mismatch: %s", f.Path)
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
		if rel == stampName || rel == manifestName {
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
