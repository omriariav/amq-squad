package sealchain

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"debug/buildinfo"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/atomicfile"
)

// RawLayout is the two-part sealed-file layout introduced for v3.2.0-rc.2.
// The document bytes and the seal line are distinct, so verification never
// depends on decoding and re-encoding a document.
const RawLayout = "raw-bytes-v1"

// FirstRawLayoutVersion is the first released version that can read the raw
// layout. It is the downgrade floor used by up --binary.
const FirstRawLayoutVersion = "v3.2.0-rc.2"

// BinaryVersion is set by the CLI entrypoint. Tests and non-CLI callers may
// leave it empty; WriterVersion then uses the executable's build information.
var BinaryVersion string

// ErrNewerWriter is a validly sealed document whose schema is newer than the
// reader's. It is an install decision, not tampering.
var ErrNewerWriter = errors.New("newer_writer")

// ErrSchemaMismatch is a validly sealed document that does not match the
// schema its seal line claims. It is a writer bug, not tampering.
var ErrSchemaMismatch = errors.New("schema_mismatch")

// RawSeal is the second JSON line of a raw-layout file. Tag covers the exact
// body bytes and every other field in this line.
type RawSeal struct {
	Seq     int    `json:"seq"`
	Prev    string `json:"prev"`
	Tag     string `json:"tag"`
	Version string `json:"version"`
	Schema  int    `json:"schema"`
}

type rawSealFields struct {
	Seq     int    `json:"seq"`
	Prev    string `json:"prev"`
	Version string `json:"version"`
	Schema  int    `json:"schema"`
}

func (s RawSeal) fields() rawSealFields {
	return rawSealFields{Seq: s.Seq, Prev: s.Prev, Version: s.Version, Schema: s.Schema}
}

func rawTag(key []byte, label string, body []byte, fields rawSealFields) string {
	meta, err := json.Marshal(fields)
	if err != nil {
		return ""
	}
	m := hmac.New(sha256.New, key)
	m.Write([]byte(label))
	m.Write([]byte{0})
	m.Write(body)
	m.Write([]byte{0})
	m.Write(meta)
	return hex.EncodeToString(m.Sum(nil))
}

// NewRawSeal seals body after prev. A nil prev starts at link one.
func NewRawSeal(key []byte, label string, prev *Link, body []byte, version string, schema int) RawSeal {
	s := RawSeal{Seq: 1, Version: normalizedWriterVersion(version), Schema: schema}
	if prev != nil {
		s.Seq, s.Prev = prev.Seq+1, prev.Tag
	}
	s.Tag = rawTag(key, label, body, s.fields())
	return s
}

// Verify reports whether the seal covers the exact body bytes and its own
// metadata. Decoding body is deliberately not part of this operation.
func (s RawSeal) Verify(key []byte, label string, body []byte) bool {
	want := rawTag(key, label, body, s.fields())
	return s.Seq > 0 && s.Tag != "" && hmac.Equal([]byte(want), []byte(s.Tag))
}

// JoinRaw returns the exact raw layout: body, newline, seal JSON, newline.
func JoinRaw(body []byte, seal RawSeal) ([]byte, error) {
	if len(body) == 0 || bytes.HasSuffix(body, []byte{'\n'}) || bytes.Contains(body, []byte{'\r'}) {
		return nil, fmt.Errorf("raw sealed body must be non-empty JSON without a trailing newline or carriage return")
	}
	line, err := json.Marshal(seal)
	if err != nil {
		return nil, err
	}
	out := make([]byte, 0, len(body)+len(line)+2)
	out = append(out, body...)
	out = append(out, '\n')
	out = append(out, line...)
	out = append(out, '\n')
	return out, nil
}

// SplitRaw enforces the exact ending and returns the bytes that were sealed.
func SplitRaw(raw []byte) ([]byte, RawSeal, error) {
	var seal RawSeal
	if len(raw) < 3 || raw[len(raw)-1] != '\n' {
		return nil, seal, fmt.Errorf("raw sealed file has no final newline")
	}
	withoutFinal := raw[:len(raw)-1]
	i := bytes.LastIndexByte(withoutFinal, '\n')
	if i <= 0 || i == len(withoutFinal)-1 {
		return nil, seal, fmt.Errorf("raw sealed file does not end with one seal line")
	}
	body, line := withoutFinal[:i], withoutFinal[i+1:]
	if bytes.Contains(line, []byte{'\r'}) || bytes.HasSuffix(body, []byte{'\r'}) {
		return nil, seal, fmt.Errorf("raw sealed file contains a carriage return at its split")
	}
	dec := json.NewDecoder(bytes.NewReader(line))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&seal); err != nil {
		return nil, seal, fmt.Errorf("raw seal line: %w", err)
	}
	if err := requireJSONEOF(dec); err != nil {
		return nil, seal, fmt.Errorf("raw seal line: %w", err)
	}
	if seal.Seq <= 0 || seal.Tag == "" || seal.Version == "" || seal.Schema <= 0 {
		return nil, seal, fmt.Errorf("raw seal line is incomplete")
	}
	return body, seal, nil
}

func requireJSONEOF(dec *json.Decoder) error {
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return fmt.Errorf("more than one JSON value")
		}
		return err
	}
	return nil
}

var unknownField = regexp.MustCompile(`^json: unknown field "([^"]+)"$`)

// DecodeStrict runs only after a raw tag verified. Unknown fields are then a
// versioned compatibility decision rather than a tamper alarm.
func DecodeStrict(what string, body []byte, seal RawSeal, readerSchema int, out any) error {
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	err := dec.Decode(out)
	if err == nil {
		err = requireJSONEOF(dec)
	}
	if err == nil {
		return nil
	}
	field := "document"
	if m := unknownField.FindStringSubmatch(err.Error()); len(m) == 2 {
		field = m[1]
	}
	if seal.Schema > readerSchema {
		return fmt.Errorf("%w: %s was written by a newer amq-squad (%s); install it with amq-squad up --binary <that build>: unknown field %q (writer schema %d, reader schema %d)", ErrNewerWriter, what, seal.Version, field, seal.Schema, readerSchema)
	}
	return fmt.Errorf("%w: %s has unknown field %q under schema %d written by %s: %v", ErrSchemaMismatch, what, field, seal.Schema, seal.Version, err)
}

const rawHeadLabel = "amq-squad sealed head raw v1"

type parsedHead struct {
	head
	rawSeal *RawSeal
}

// HeadLayout verifies headBytes and reports its layout. Empty means the
// legacy embedded-tag head.
func HeadLayout(name string, s Seed, headBytes []byte) (string, error) {
	h, err := parseHeadBytes(name, s, headBytes)
	if err != nil {
		return "", err
	}
	return h.Layout, nil
}

// SchemaError classifies a validly sealed document whose declared document
// schema is not the reader's schema.
func SchemaError(what string, seal RawSeal, readerSchema, documentSchema int) error {
	if seal.Schema > readerSchema {
		return fmt.Errorf("%w: %s was written by a newer amq-squad (%s); install it with amq-squad up --binary <that build> (writer schema %d, reader schema %d)", ErrNewerWriter, what, seal.Version, seal.Schema, readerSchema)
	}
	return fmt.Errorf("%w: %s declares document schema %d under seal schema %d written by %s; reader schema is %d", ErrSchemaMismatch, what, documentSchema, seal.Schema, seal.Version, readerSchema)
}

func parseHeadBytes(name string, s Seed, raw []byte) (parsedHead, error) {
	var out parsedHead
	if body, seal, err := SplitRaw(raw); err == nil {
		var h head
		dec := json.NewDecoder(bytes.NewReader(body))
		dec.DisallowUnknownFields()
		if derr := dec.Decode(&h); derr != nil || requireJSONEOF(dec) != nil || h.Schema != 1 || h.Name != name || h.Layout != RawLayout || h.Tag != "" || !seal.Verify(s.Key, rawHeadLabel, body) {
			return out, fmt.Errorf("%w: the head of %s does not decode or its raw tag fails", ErrTampered, name)
		}
		if err := CheckSeedIdentity("the session seed", h.SeedDev, h.SeedIno, s.Dev, s.Ino); err != nil {
			return out, fmt.Errorf("%w: the head of %s: %v", ErrTampered, name, err)
		}
		out.head, out.rawSeal = h, &seal
		return out, nil
	}
	var h head
	if json.Unmarshal(raw, &h) != nil || h.Schema != 1 || h.Name != name || h.Layout != "" || h.Tag == "" || !hmac.Equal([]byte(h.sign(s.Key).Tag), []byte(h.Tag)) {
		return out, fmt.Errorf("%w: the head of %s does not decode or its tag fails", ErrTampered, name)
	}
	if err := CheckSeedIdentity("the session seed", h.SeedDev, h.SeedIno, s.Dev, s.Ino); err != nil {
		return out, fmt.Errorf("%w: the head of %s: %v", ErrTampered, name, err)
	}
	out.head = h
	return out, nil
}

// CheckRaw verifies a raw document and the exact head bytes the caller read,
// then strictly decodes it. Sealchain does not reopen the head.
func CheckRaw(name string, s Seed, label, what string, document, headBytes []byte, readerSchema int, out any) (RawSeal, error) {
	var zero RawSeal
	h, err := parseHeadBytes(name, s, headBytes)
	if err != nil {
		return zero, err
	}
	if h.Layout != RawLayout {
		return zero, fmt.Errorf("%w: %s has a legacy head", ErrSchemaMismatch, what)
	}
	body, seal, err := SplitRaw(document)
	if err != nil || !seal.Verify(s.Key, label, body) {
		return zero, fmt.Errorf("%w: the tag of %s raw link fails: %v", ErrTampered, name, err)
	}
	switch {
	case seal.Seq == h.Seq && seal.Tag == h.Link:
	case seal.Seq == h.Seq+1 && seal.Prev == h.Link:
	default:
		return zero, fmt.Errorf("%w: %s is at link %d, its head records link %d", ErrTampered, name, seal.Seq, h.Seq)
	}
	if err := DecodeStrict(what, body, seal, readerSchema, out); err != nil {
		return seal, err
	}
	return seal, nil
}

// CheckLegacy verifies a legacy document against the exact head bytes the
// caller read. It is the one-time migration path.
func CheckLegacy(name string, s Seed, label string, link *Link, body any, headBytes []byte) error {
	h, err := parseHeadBytes(name, s, headBytes)
	if err != nil {
		return err
	}
	if h.Layout != "" {
		return fmt.Errorf("%w: %s carries legacy bytes after its head recorded %s", ErrTampered, name, h.Layout)
	}
	switch {
	case link == nil:
		return fmt.Errorf("%w: %s carries no seal", ErrTampered, name)
	case !link.Verify(s.Key, label, body):
		return fmt.Errorf("%w: the tag of %s link %d fails", ErrTampered, name, link.Seq)
	case link.Seq == h.Seq && link.Tag == h.Link:
		return nil
	case link.Seq == h.Seq+1 && link.Prev == h.Link:
		return nil
	default:
		return fmt.Errorf("%w: %s is at link %d, its head records link %d", ErrTampered, name, link.Seq, h.Seq)
	}
}

// AbsentBytes checks a missing document against the exact head bytes read by
// its caller.
func AbsentBytes(name string, s Seed, headBytes []byte) error {
	if len(headBytes) == 0 {
		return nil
	}
	h, err := parseHeadBytes(name, s, headBytes)
	if err != nil {
		return err
	}
	if h.Seq > 0 {
		return fmt.Errorf("%w: %s is missing and its head records link %d", ErrTampered, name, h.Seq)
	}
	return nil
}

func writeRawHead(root, name string, s Seed, link *Link, version string, now time.Time) error {
	h := head{Schema: 1, Layout: RawLayout, Name: name, Seq: link.Seq, Link: link.Tag, SeedDev: s.Dev, SeedIno: s.Ino, At: now.UTC()}
	body, err := json.Marshal(h)
	if err != nil {
		return err
	}
	seal := NewRawSeal(s.Key, rawHeadLabel, nil, body, version, 1)
	raw, err := JoinRaw(body, seal)
	if err != nil {
		return err
	}
	p := HeadPath(root, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	return atomicfile.Write(p, raw, 0o600)
}

// WriteRaw writes a new raw-layout document and then its raw-sealed head.
// save receives the complete two-part bytes to persist atomically.
func WriteRaw(root, name string, s Seed, label string, body []byte, version string, schema int, save func([]byte) error, now time.Time) (RawSeal, error) {
	var zero RawSeal
	if !headName.MatchString(name) || schema <= 0 {
		return zero, fmt.Errorf("sealed head %q or schema %d", name, schema)
	}
	h, found, err := readHead(root, name, s)
	if err != nil {
		return zero, err
	}
	if !found {
		h = head{Schema: 1, Name: name}
		if err := writeRawHead(root, name, s, &Link{}, version, now); err != nil {
			return zero, err
		}
	}
	prev := &Link{Seq: h.Seq, Tag: h.Link}
	seal := NewRawSeal(s.Key, label, prev, body, version, schema)
	raw, err := JoinRaw(body, seal)
	if err != nil {
		return zero, err
	}
	if err := save(raw); err != nil {
		return zero, err
	}
	return seal, writeRawHead(root, name, s, &Link{Seq: seal.Seq, Prev: seal.Prev, Tag: seal.Tag}, version, now)
}

// SessionRawLayout reports the writer version recorded by any verified raw
// head. It is used before up replaces the binary.
func SessionRawLayout(root string) (string, bool, error) {
	dir := filepath.Join(Dir(root), "heads")
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	s, ok, err := ReadKey(root)
	if err != nil || !ok {
		return "", false, err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		name := strings.TrimSuffix(entry.Name(), ".json")
		raw, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			return "", false, err
		}
		h, err := parseHeadBytes(name, s, raw)
		if err != nil {
			return "", false, err
		}
		if h.Layout == RawLayout && h.rawSeal != nil {
			return h.rawSeal.Version, true, nil
		}
	}
	return "", false, nil
}

// WriterVersion returns the CLI version, else the executable's module
// version. Development builds remain literal because compatibility decisions
// key on schema, not on parsing this string.
func WriterVersion() string { return normalizedWriterVersion(BinaryVersion) }

func normalizedWriterVersion(v string) string {
	if strings.TrimSpace(v) != "" {
		return strings.TrimSpace(v)
	}
	if info, err := buildinfo.ReadFile(os.Args[0]); err == nil && info.Main.Version != "" {
		return info.Main.Version
	}
	return "(devel)"
}
