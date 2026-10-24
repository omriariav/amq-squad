package piruntime

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
)

// PackSpec names what a payload starts and carries; Pack fills Files and
// SHA256 from the staged tree.
type PackSpec struct {
	Version     string
	Platform    string
	PiVersion   string
	NodeVersion string
	Executable  string
	Args        []string
	Package     string
	Tools       map[string]string
}

// Packed is a built payload: the archive, the manifest bytes and the digest
// file that pins them.
type Packed struct {
	Payload  []byte
	Manifest []byte
	Digest   []byte // "<sha256 of Manifest>  manifest.json\n"
	Info     Manifest
}

// Pack builds a normalized payload from a staged tree (ported from
// spike/embedpi/pack.py): files in sorted order, regular files only, mode
// 0555 when any exec bit is set and 0444 otherwise, no timestamps or owners,
// so the same tree packs to the same bytes. A symlink or special file in the
// tree is refused.
func Pack(tree string, spec PackSpec) (Packed, error) {
	var paths []string
	err := filepath.WalkDir(tree, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if !d.Type().IsRegular() {
			return fmt.Errorf("pack: %s is not a regular file", p)
		}
		paths = append(paths, p)
		return nil
	})
	if err != nil {
		return Packed{}, err
	}
	rels := make([]string, len(paths))
	for i, p := range paths {
		rel, err := filepath.Rel(tree, p)
		if err != nil {
			return Packed{}, err
		}
		rels[i] = filepath.ToSlash(rel)
	}
	sort.Strings(rels)
	var buf bytes.Buffer
	gz, _ := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	tw := tar.NewWriter(gz)
	m := Manifest{
		Schema: ManifestSchema, Version: spec.Version, Platform: spec.Platform,
		PiVersion: spec.PiVersion, NodeVersion: spec.NodeVersion,
		Executable: spec.Executable, Args: spec.Args, Package: spec.Package, Tools: spec.Tools,
	}
	for _, rel := range rels {
		p := filepath.Join(tree, filepath.FromSlash(rel))
		data, err := os.ReadFile(p)
		if err != nil {
			return Packed{}, err
		}
		fi, err := os.Lstat(p)
		if err != nil {
			return Packed{}, err
		}
		mode := uint32(0o444)
		if fi.Mode().Perm()&0o111 != 0 {
			mode = 0o555
		}
		hdr := &tar.Header{Name: rel, Mode: int64(mode), Size: int64(len(data)), Typeflag: tar.TypeReg, Format: tar.FormatPAX}
		if err := tw.WriteHeader(hdr); err != nil {
			return Packed{}, err
		}
		if _, err := tw.Write(data); err != nil {
			return Packed{}, err
		}
		m.Files = append(m.Files, Entry{Path: rel, SHA256: digest(data), Size: int64(len(data)), Mode: mode})
	}
	if err := tw.Close(); err != nil {
		return Packed{}, err
	}
	if err := gz.Close(); err != nil {
		return Packed{}, err
	}
	m.SHA256 = digest(buf.Bytes())
	if err := m.Validate(); err != nil {
		return Packed{}, err
	}
	raw, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return Packed{}, err
	}
	raw = append(raw, '\n')
	return Packed{Payload: buf.Bytes(), Manifest: raw, Digest: []byte(digest(raw) + "  manifest.json\n"), Info: m}, nil
}
