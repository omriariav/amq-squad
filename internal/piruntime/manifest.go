// Package piruntime is the built-in pi runtime of amq-squad 3.0 (plan 4.1,
// R1, R2; ported from spike/embedpi with tests, brief 4-1).
//
// A release build (build tag amqpi, darwin-arm64) embeds a payload: the
// official Node binary, the pinned npm tree of
// @earendil-works/pi-coding-agent from pi/package-lock.json, the
// @amq-squad/pi package, and digest-pinned fd and rg. The first launch
// unpacks it to ~/.amq-squad/runtime/<version>-<sha256[:12]>/. Every launch
// re-hashes the whole tree against the embedded manifest, whose SHA-256 is
// the trust root: a missing or mismatched digest, a changed, missing or extra
// file, a symlink anywhere in the tree, or an on-disk manifest that differs
// from the embedded one makes the launch refuse. The launcher never repairs a
// rejected runtime itself; `doctor --fix` removes runtimes other than the
// last two.
//
// Builds without the tag carry no payload (Load returns ErrNotEmbedded),
// so linux and windows builds compile and refuse to start a pi lead.
package piruntime

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"path/filepath"
	"strings"
)

// File names inside an unpacked runtime. They match the S3 spike and the
// constants 2b-3's registry.RehashRuntime reads (RuntimeManifestName,
// RuntimeMarkerName), so doctor and the launcher agree on the layout.
const (
	ManifestName = ".embedpi-manifest.json"
	MarkerName   = ".embedpi-complete"
)

// ManifestSchema is the payload manifest schema this binary reads.
const ManifestSchema = 1

// ErrNotEmbedded is returned by Load in a build without the payload.
var ErrNotEmbedded = errors.New("this amq-squad build carries no pi runtime (build with scripts/build-pi-runtime.sh, tag amqpi)")

// Entry is one file of the payload.
type Entry struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
	Mode   uint32 `json:"mode"`
}

// Manifest describes one payload: what it contains and how to start pi.
type Manifest struct {
	Schema      int    `json:"schema"`
	Version     string `json:"version"`
	Platform    string `json:"platform"`
	SHA256      string `json:"sha256"` // of payload.tar.gz
	PiVersion   string `json:"pi_version"`
	NodeVersion string `json:"node_version"`
	// Executable and Args start pi: node/bin/node and the pi bundle entry.
	// 2b-3's registry.PiCommand reads exactly these two fields.
	Executable string   `json:"executable"`
	Args       []string `json:"args"`
	// Package is the directory of the @amq-squad/pi package, loaded with -e.
	Package string `json:"package"`
	// Preload is the runtime self-check node loads with --require before
	// the pi entry (review 2, item 2 (b)): a one-purpose CommonJS module that
	// re-hashes every manifest file and exits 70 on a mismatch.
	Preload string `json:"preload,omitempty"`
	// Tools maps a helper pi looks up in <agent-dir>/bin (fd, rg) to its path.
	Tools map[string]string `json:"tools,omitempty"`
	Files []Entry           `json:"files"`
}

// Embedded is the payload compiled into this binary: the manifest bytes, the
// digest file that pins them, and a loader for the archive.
type Embedded struct {
	Manifest Manifest
	Raw      []byte // manifest bytes as embedded
	Digest   string // lowercase hex SHA-256 of Raw, the trust root
	Payload  func() ([]byte, error)
}

// ParseEmbedded checks the trust root and decodes the manifest. digestFile is
// the embedded manifest.sha256: its first field must be the SHA-256 of raw.
// A missing or mismatched digest is a failure, never a warning.
func ParseEmbedded(raw, digestFile []byte, payload func() ([]byte, error)) (Embedded, error) {
	want := strings.Fields(string(digestFile))
	if len(want) == 0 {
		return Embedded{}, errors.New("embedded runtime manifest digest is missing")
	}
	if !validDigest(want[0]) {
		return Embedded{}, errors.New("embedded runtime manifest digest is malformed")
	}
	if got := digest(raw); got != want[0] {
		return Embedded{}, fmt.Errorf("embedded runtime manifest digest mismatch: manifest %s, pinned %s", got, want[0])
	}
	var m Manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return Embedded{}, fmt.Errorf("embedded runtime manifest: %w", err)
	}
	if err := m.Validate(); err != nil {
		return Embedded{}, err
	}
	return Embedded{Manifest: m, Raw: raw, Digest: want[0], Payload: payload}, nil
}

// DirName is the runtime directory name under the runtime base.
func (m Manifest) DirName() string { return m.Version + "-" + m.SHA256[:12] }

// maxUnpacked bounds the unpacked size (S3: 179 MB).
const maxUnpacked = 1 << 30

// Validate rejects a manifest whose paths could escape the runtime directory
// or whose launch entries are not in the payload.
func (m Manifest) Validate() error {
	if m.Schema != ManifestSchema {
		return fmt.Errorf("runtime manifest schema %d, want %d", m.Schema, ManifestSchema)
	}
	if !local(m.Version) || strings.Contains(m.Version, "/") || !validDigest(m.SHA256) {
		return errors.New("runtime manifest: invalid version or payload digest")
	}
	if !local(m.Executable) || len(m.Files) == 0 {
		return errors.New("runtime manifest: invalid executable or no files")
	}
	seen := map[string]Entry{}
	var total int64
	for _, f := range m.Files {
		if !local(f.Path) || f.Path == MarkerName || f.Path == ManifestName || !validDigest(f.SHA256) ||
			f.Size < 0 || (f.Mode != 0o444 && f.Mode != 0o555) {
			return fmt.Errorf("runtime manifest: invalid entry %q", f.Path)
		}
		if _, dup := seen[f.Path]; dup {
			return fmt.Errorf("runtime manifest: duplicate entry %q", f.Path)
		}
		seen[f.Path] = f
		total += f.Size
		if total > maxUnpacked {
			return errors.New("runtime manifest: unpacked payload exceeds 1 GiB")
		}
	}
	if seen[m.Executable].Mode != 0o555 {
		return errors.New("runtime manifest: executable missing or not executable")
	}
	for _, a := range m.Args {
		if _, ok := seen[a]; !ok || !local(a) {
			return fmt.Errorf("runtime manifest: entry argument %q not in the payload", a)
		}
	}
	if m.Preload != "" {
		if _, ok := seen[m.Preload]; !ok || !local(m.Preload) {
			return errors.New("runtime manifest: the preload is not in the payload")
		}
	}
	if m.Package != "" {
		if !local(m.Package) {
			return errors.New("runtime manifest: invalid package path")
		}
		if _, ok := seen[m.Package+"/package.json"]; !ok {
			return errors.New("runtime manifest: package has no package.json")
		}
	}
	for name, p := range m.Tools {
		if name == "" || strings.ContainsAny(name, `/\`) || !local(p) || seen[p].Mode != 0o555 {
			return fmt.Errorf("runtime manifest: invalid tool %q", name)
		}
	}
	return nil
}

func local(p string) bool {
	return p != "" && p != "." && path.Clean(p) == p && filepath.IsLocal(p) && !strings.Contains(p, `\`)
}

func digest(b []byte) string { sum := sha256.Sum256(b); return hex.EncodeToString(sum[:]) }

func validDigest(s string) bool {
	b, err := hex.DecodeString(s)
	return err == nil && len(b) == sha256.Size && strings.ToLower(s) == s
}
