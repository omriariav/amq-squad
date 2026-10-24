package registry

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	"github.com/omriariav/amq-squad/v3/internal/fsanchor"
)

// Pin is an owner-pinned harness tool (2b-2 milestone B, D2): the gh the
// harness runs for its PRs and as the HTTPS push's credential helper. The
// owner writes the absolute path and the sha256 into registry.yaml's pins
// list (doctor proposes both from `command -v gh`, but only the file write
// counts); detection never writes a pin, Build carries the list forward, and
// every launch re-hashes the file against the pinned digest. Never PATH.
type Pin struct {
	Name   string
	Path   string
	SHA256 string
}

// Pin reasons.
const (
	PinMissing  = "tool_unpinned"     // no pin with an absolute path and a digest
	PinMismatch = "tool_pin_mismatch" // the file at the path is not the pinned digest
)

func (p Pin) fields() []kv {
	return []kv{{"name", q(p.Name)}, {"path", q(p.Path)}, {"sha256", q(p.SHA256)}}
}

func (p *Pin) set(k, v string) error {
	switch k {
	case "name":
		return setStr(&p.Name, v)
	case "path":
		return setStr(&p.Path, v)
	case "sha256":
		return setStr(&p.SHA256, v)
	}
	return unknownKey(k)
}

// PinFor is the registry's pin of a tool.
func (r Registry) PinFor(name string) (Pin, bool) {
	for _, p := range r.Pins {
		if p.Name == name {
			return p, true
		}
	}
	return Pin{}, false
}

// Verify opens the pinned file through the anchored walk (no symlink in its
// path, O_NOFOLLOW on the file) and hashes that descriptor (the third pass:
// a pathname reopened after the hash is not what was hashed): it returns
// the path and its digest, or the reason code and detail of a refusal. The
// owner pins the real path (doctor proposes it resolved).
func (p Pin) Verify() (path, digest, code, detail string) {
	t, code, detail := OpenPinned(p.Name, p.Path, p.SHA256)
	if t == nil {
		return "", "", code, detail
	}
	defer t.Close()
	return t.Path, t.Digest, "", ""
}

// VerifiedTool is a pinned executable held open through the descriptor its
// digest was computed from.
type VerifiedTool struct {
	Path   string
	Digest string
	f      *os.File
}

// OpenPinned opens path through the anchored walk, hashes the descriptor and
// compares the digest with want. A nil tool carries the refusal.
func OpenPinned(name, path, want string) (*VerifiedTool, string, string) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || want == "" {
		return nil, PinMissing, fmt.Sprintf("the registry pins %s with no absolute clean path and sha256", name)
	}
	d, err := fsanchor.OpenAnchored(filepath.Dir(path), nil, false)
	if err != nil {
		return nil, PinMismatch, fmt.Sprintf("the pinned %s %s: %v (the pin names the real path, no symlink)", name, path, err)
	}
	f, kind, _, _, err := d.OpenEntry(filepath.Base(path))
	d.Close()
	if err != nil {
		return nil, PinMismatch, fmt.Sprintf("the pinned %s %s: %v", name, path, err)
	}
	if kind != fsanchor.KindRegular {
		f.Close()
		return nil, PinMismatch, fmt.Sprintf("the pinned %s %s is not a regular file", name, path)
	}
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		f.Close()
		return nil, PinMismatch, fmt.Sprintf("the pinned %s %s: %v", name, path, err)
	}
	sum := hex.EncodeToString(h.Sum(nil))
	if sum != want {
		f.Close()
		return nil, PinMismatch, fmt.Sprintf("the pinned %s %s hashes to %s, the pin is %s", name, path, sum, want)
	}
	return &VerifiedTool{Path: path, Digest: sum, f: f}, "", ""
}

// Command runs the verified tool: on Linux through /dev/fd/3, the hashed
// descriptor itself (handed to the child as its fd 3); elsewhere (darwin
// refuses execve of /dev/fd/N) by the verified path, immediately after the
// hash (4-1's shape; the window to execve is the same-UID limit).
func (t *VerifiedTool) Command(args ...string) *exec.Cmd {
	if runtime.GOOS == "linux" {
		cmd := exec.Command("/dev/fd/3", args...)
		cmd.ExtraFiles = []*os.File{t.f}
		return cmd
	}
	return exec.Command(t.Path, args...)
}

// Close releases the descriptor.
func (t *VerifiedTool) Close() {
	if t != nil && t.f != nil {
		t.f.Close()
	}
}

// FileSHA256 is a regular file's sha256 in hex.
func FileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return "", err
	}
	if !st.Mode().IsRegular() {
		return "", fmt.Errorf("%s is not a regular file", path)
	}
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// Install copies the verified tool, from the descriptor its digest was
// computed from, into dir (a core-written 0700 directory) as
// <name>-<sha256>, and returns that copy opened through the anchored walk
// and re-hashed (the fourth pass's pin mitigation: git execs its credential
// helper by pathname, so the helper path is the harness's own copy, never
// the registry path a seat can see). An existing copy with the digest is
// reused.
func (t *VerifiedTool) Install(dir, name string) (*VerifiedTool, error) {
	if t == nil || t.f == nil {
		return nil, fmt.Errorf("install %s: no verified tool", name)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return nil, err
	}
	if st, err := os.Lstat(dir); err != nil || !st.IsDir() {
		return nil, fmt.Errorf("install %s: %s is not a plain directory", name, dir)
	}
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return nil, err
	}
	final := filepath.Join(real, name+"-"+t.Digest)
	if c, _, _ := OpenPinned(name, final, t.Digest); c != nil {
		return c, nil
	}
	if _, err := t.f.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	tmp, err := os.CreateTemp(real, "."+name+"-*")
	if err != nil {
		return nil, err
	}
	_, err = io.Copy(tmp, t.f)
	if err == nil {
		err = tmp.Sync()
	}
	if err == nil {
		err = tmp.Chmod(0o500)
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp.Name(), final)
	}
	if err != nil {
		os.Remove(tmp.Name())
		return nil, fmt.Errorf("install %s: %v", name, err)
	}
	c, code, detail := OpenPinned(name, final, t.Digest)
	if c == nil {
		return nil, fmt.Errorf("%s: %s", code, detail)
	}
	return c, nil
}
