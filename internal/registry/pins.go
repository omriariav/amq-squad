package registry

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
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

// Verify re-hashes the pinned file: it returns the resolved path and its
// digest, or the reason code and detail of a refusal.
func (p Pin) Verify() (path, digest, code, detail string) {
	if !filepath.IsAbs(p.Path) || p.SHA256 == "" {
		return "", "", PinMissing, fmt.Sprintf("the registry pins %s with no absolute path and sha256", p.Name)
	}
	real, err := filepath.EvalSymlinks(p.Path)
	if err != nil {
		return "", "", PinMismatch, fmt.Sprintf("the pinned %s %s: %v", p.Name, p.Path, err)
	}
	sum, err := FileSHA256(real)
	if err != nil {
		return "", "", PinMismatch, fmt.Sprintf("the pinned %s %s: %v", p.Name, real, err)
	}
	if sum != p.SHA256 {
		return real, sum, PinMismatch, fmt.Sprintf("the pinned %s %s hashes to %s, the registry pins %s", p.Name, real, sum, p.SHA256)
	}
	return real, sum, "", ""
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
