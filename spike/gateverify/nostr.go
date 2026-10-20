package main

// NIP-01 events: strict parsing, the canonical id serialization, and
// BIP-340 verification. Signing exists only for synthetic tests and the
// throwaway keys the spike generates; the owner key never enters this
// program.

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"regexp"
	"strconv"
	"unicode/utf8"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/btcec/v2/schnorr"
)

// Event is a NIP-01 event exactly as its seven fields arrive.
type Event struct {
	ID        string     `json:"id"`
	PubKey    string     `json:"pubkey"`
	CreatedAt int64      `json:"created_at"`
	Kind      int        `json:"kind"`
	Tags      [][]string `json:"tags"`
	Content   string     `json:"content"`
	Sig       string     `json:"sig"`
}

var (
	hex64  = regexp.MustCompile(`^[0-9a-f]{64}$`)
	hex128 = regexp.MustCompile(`^[0-9a-f]{128}$`)
)

var eventFields = []string{"id", "pubkey", "created_at", "kind", "tags", "content", "sig"}

// Bounds on one event. A gate answer is a few hundred bytes; the limits only
// stop a hostile relay from making the parser do unbounded work.
const (
	maxEventBytes  = 256 << 10
	maxTags        = 2000
	maxTagElements = 64
)

var canonicalUint = regexp.MustCompile(`^(0|[1-9][0-9]{0,15})$`)

// ParseEvent decodes one event strictly, from the raw bytes:
//
//   - the bytes are valid UTF-8 before any decoding (encoding/json would
//     silently replace invalid sequences with U+FFFD), and no \u escape is
//     an unpaired surrogate (which it would also replace);
//   - the token stream is walked, so a duplicate key is refused (Decode
//     keeps the last one), as is any key outside the seven NIP-01 fields;
//   - id, pubkey, sig and content are JSON strings; created_at and kind are
//     canonical unsigned integers (no sign, fraction, exponent or leading
//     zero); tags is an array of arrays of strings, and a null or non-string
//     tag element is refused (Decode would turn null into "");
//   - no string holds a C0 control other than \t \n \r (ambiguousControl);
//   - nothing follows the object.
//
// The replay ledger keys on the event id and the gate id, and the id is
// recomputed from the decoded fields, so a wire-form variant of one event
// never bypassed replay. This strictness is conformance, not a fix for a
// found bypass.
func ParseEvent(raw []byte) (Event, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) > maxEventBytes {
		return Event{}, errors.New("event: larger than 256 KiB")
	}
	if !utf8.Valid(raw) {
		return Event{}, errors.New("event: bytes are not valid UTF-8")
	}
	if err := checkSurrogateEscapes(raw); err != nil {
		return Event{}, fmt.Errorf("event: %w", err)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := expectDelim(dec, '{'); err != nil {
		return Event{}, fmt.Errorf("event: %w", err)
	}
	var e Event
	seen := map[string]bool{}
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return Event{}, fmt.Errorf("event: %w", err)
		}
		key, ok := tok.(string)
		if !ok {
			return Event{}, errors.New("event: object key is not a string")
		}
		if seen[key] {
			return Event{}, fmt.Errorf("event: duplicate key %q", key)
		}
		seen[key] = true
		switch key {
		case "id", "pubkey", "sig", "content":
			v, err := stringToken(dec)
			if err != nil {
				return Event{}, fmt.Errorf("event: %s: %w", key, err)
			}
			switch key {
			case "id":
				e.ID = v
			case "pubkey":
				e.PubKey = v
			case "sig":
				e.Sig = v
			default:
				e.Content = v
			}
		case "created_at", "kind":
			tok, err := dec.Token()
			if err != nil {
				return Event{}, fmt.Errorf("event: %s: %w", key, err)
			}
			num, ok := tok.(json.Number)
			if !ok || !canonicalUint.MatchString(num.String()) {
				return Event{}, fmt.Errorf("event: %s is not a canonical unsigned integer", key)
			}
			n, err := strconv.ParseInt(num.String(), 10, 64)
			if err != nil {
				return Event{}, fmt.Errorf("event: %s out of range", key)
			}
			if key == "kind" {
				if n > 65535 {
					return Event{}, errors.New("event: kind out of range")
				}
				e.Kind = int(n)
			} else {
				e.CreatedAt = n
			}
		case "tags":
			tags, err := parseTags(dec)
			if err != nil {
				return Event{}, fmt.Errorf("event: tags: %w", err)
			}
			e.Tags = tags
		default:
			return Event{}, fmt.Errorf("event: unknown field %q", key)
		}
	}
	if err := expectDelim(dec, '}'); err != nil {
		return Event{}, fmt.Errorf("event: %w", err)
	}
	if _, err := dec.Token(); err != io.EOF {
		return Event{}, errors.New("event: trailing data after the object")
	}
	for _, name := range eventFields {
		if !seen[name] {
			return Event{}, fmt.Errorf("event: missing field %q", name)
		}
	}
	switch {
	case !hex64.MatchString(e.ID):
		return Event{}, errors.New("event: id is not 64 lowercase hex")
	case !hex64.MatchString(e.PubKey):
		return Event{}, errors.New("event: pubkey is not 64 lowercase hex")
	case !hex128.MatchString(e.Sig):
		return Event{}, errors.New("event: sig is not 128 lowercase hex")
	}
	if ambiguousControl(e.Content) {
		return Event{}, errors.New("event: content holds a control character whose id escaping differs between nostr libraries")
	}
	for _, t := range e.Tags {
		for _, v := range t {
			if ambiguousControl(v) {
				return Event{}, errors.New("event: a tag holds a control character whose id escaping differs between nostr libraries")
			}
		}
	}
	return e, nil
}

func expectDelim(dec *json.Decoder, want json.Delim) error {
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	if d, ok := tok.(json.Delim); !ok || d != want {
		return fmt.Errorf("want %q", string(want))
	}
	return nil
}

// stringToken reads one JSON string value; null, numbers, booleans, arrays
// and objects are refused. An array or object value leaves the decoder
// mid-value, but the caller returns an error at once.
func stringToken(dec *json.Decoder) (string, error) {
	tok, err := dec.Token()
	if err != nil {
		return "", err
	}
	v, ok := tok.(string)
	if !ok {
		return "", fmt.Errorf("not a string (%v)", tok)
	}
	return v, nil
}

func parseTags(dec *json.Decoder) ([][]string, error) {
	if err := expectDelim(dec, '['); err != nil {
		return nil, err
	}
	tags := [][]string{}
	for dec.More() {
		if len(tags) >= maxTags {
			return nil, errors.New("too many tags")
		}
		if err := expectDelim(dec, '['); err != nil {
			return nil, fmt.Errorf("tag %d: %w", len(tags), err)
		}
		tag := []string{}
		for dec.More() {
			if len(tag) >= maxTagElements {
				return nil, errors.New("tag has too many elements")
			}
			v, err := stringToken(dec)
			if err != nil {
				return nil, fmt.Errorf("tag %d element %d: %w", len(tags), len(tag), err)
			}
			tag = append(tag, v)
		}
		if err := expectDelim(dec, ']'); err != nil {
			return nil, err
		}
		tags = append(tags, tag)
	}
	return tags, expectDelim(dec, ']')
}

// checkSurrogateEscapes refuses a \uD800-\uDFFF escape that is not a valid
// high+low pair. encoding/json would decode it as U+FFFD, a silent rewrite
// of what the signer hashed. It scans inside JSON string literals only.
func checkSurrogateEscapes(raw []byte) error {
	inString := false
	for i := 0; i < len(raw); i++ {
		c := raw[i]
		if !inString {
			if c == '"' {
				inString = true
			}
			continue
		}
		switch c {
		case '"':
			inString = false
		case '\\':
			if i+1 >= len(raw) {
				return errors.New("truncated escape")
			}
			if raw[i+1] != 'u' {
				i++ // skip the escaped character
				continue
			}
			u, ok := hex4(raw, i+2)
			if !ok {
				return errors.New(`bad \u escape`)
			}
			switch {
			case u >= 0xDC00 && u <= 0xDFFF:
				return errors.New("unpaired low surrogate escape")
			case u >= 0xD800 && u <= 0xDBFF:
				lo, ok := hex4(raw, i+8)
				if !ok || raw[i+6] != '\\' || raw[i+7] != 'u' || lo < 0xDC00 || lo > 0xDFFF {
					return errors.New("unpaired high surrogate escape")
				}
				i += 11
			default:
				i += 5
			}
		}
	}
	return nil
}

func hex4(raw []byte, at int) (int, bool) {
	if at < 0 || at+4 > len(raw) {
		return 0, false
	}
	n, err := strconv.ParseUint(string(raw[at:at+4]), 16, 16)
	return int(n), err == nil
}

// ambiguousControl reports a C0 control other than \t \n \r. NIP-01 escapes
// \b and \f, and serde-style encoders escape the rest as \u00XX, but
// fiatjaf.com/nostr (the library amq-remote v0.102.0 verifies with) writes
// them verbatim (event.go escTable: only " \ \n \r \t). The same signed
// event then has two ids depending on who hashes it. The conventions agree
// exactly when none of these bytes occur, so the verifier refuses them
// rather than pick a side. This rejects compliant events that carry control
// characters in tags or content. That is accepted as fail-closed. Real Buzz
// behaviour is untested, and ask 11 stands.
func ambiguousControl(s string) bool {
	for i := 0; i < len(s); i++ {
		if c := s[i]; c < 0x20 && c != '\t' && c != '\n' && c != '\r' {
			return true
		}
	}
	return false
}

func strictDecode(raw []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	dec.UseNumber()
	if err := dec.Decode(v); err != nil {
		return err
	}
	var trailing any
	if err := dec.Decode(&trailing); err != io.EOF {
		return errors.New("trailing data after the JSON value")
	}
	return nil
}

// SerializeForID is the NIP-01 commitment [0,pubkey,created_at,kind,tags,
// content] with no whitespace. Strings escape only what NIP-01 names (\n \"
// \\ \r \t \b \f) plus the remaining C0 controls as \u00XX, and keep every
// other character verbatim, so encoding/json's HTML and U+2028 escaping can
// never change an id.
func SerializeForID(e Event) []byte {
	var b bytes.Buffer
	b.WriteString(`[0,`)
	writeString(&b, e.PubKey)
	b.WriteByte(',')
	b.WriteString(strconv.FormatInt(e.CreatedAt, 10))
	b.WriteByte(',')
	b.WriteString(strconv.Itoa(e.Kind))
	b.WriteByte(',')
	writeTags(&b, e.Tags)
	b.WriteByte(',')
	writeString(&b, e.Content)
	b.WriteByte(']')
	return b.Bytes()
}

func writeTags(b *bytes.Buffer, tags [][]string) {
	b.WriteByte('[')
	for i, t := range tags {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteByte('[')
		for j, s := range t {
			if j > 0 {
				b.WriteByte(',')
			}
			writeString(b, s)
		}
		b.WriteByte(']')
	}
	b.WriteByte(']')
}

func writeString(b *bytes.Buffer, s string) {
	b.WriteByte('"')
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch c {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		case '\b':
			b.WriteString(`\b`)
		case '\f':
			b.WriteString(`\f`)
		default:
			if c < 0x20 {
				fmt.Fprintf(b, `\u%04x`, c)
			} else {
				b.WriteByte(c)
			}
		}
	}
	b.WriteByte('"')
}

// ComputeID is sha256 over the NIP-01 commitment.
func (e Event) ComputeID() [32]byte { return sha256.Sum256(SerializeForID(e)) }

// CheckID reports whether the stated id is the hash of the event's content.
func (e Event) CheckID() bool {
	id := e.ComputeID()
	return hex.EncodeToString(id[:]) == e.ID
}

// VerifySignature checks the BIP-340 signature over the stated id under the
// event's own x-only pubkey. Callers check CheckID first and compare the
// pubkey with their pin; this proves only that the key signed this id.
func (e Event) VerifySignature() error {
	id, err := hex.DecodeString(e.ID)
	if err != nil || len(id) != 32 {
		return errors.New("id is not 32 bytes of hex")
	}
	pubRaw, err := hex.DecodeString(e.PubKey)
	if err != nil {
		return errors.New("pubkey is not hex")
	}
	pub, err := schnorr.ParsePubKey(pubRaw)
	if err != nil {
		return fmt.Errorf("pubkey: %w", err)
	}
	sigRaw, err := hex.DecodeString(e.Sig)
	if err != nil {
		return errors.New("sig is not hex")
	}
	if err := CheckSigScalars(sigRaw); err != nil {
		return err
	}
	sig, err := schnorr.ParseSignature(sigRaw)
	if err != nil {
		return fmt.Errorf("sig: %w", err)
	}
	if !libraryVerify(sig, id, pub) {
		return errors.New("BIP-340 signature does not verify")
	}
	return nil
}

// libraryVerify is the one call into btcec's BIP-340 verify. It is a
// variable only so a test can prove the range checks reject first.
var libraryVerify = func(sig *schnorr.Signature, hash []byte, pub *btcec.PublicKey) bool {
	return sig.Verify(hash, pub)
}

// CheckSigScalars enforces BIP-340's ranges on the encoded signature before
// any library call: r < p (the field prime) and s < n (the group order).
// btcec v2.5.0 schnorr.ParseSignature checks r but reduces s modulo n,
// discarding the overflow (schnorr/signature.go:93-94), so an s >= n
// encoding would otherwise reach Verify as s - n. BIP-340 says to fail if
// s >= n.
func CheckSigScalars(sig []byte) error {
	if len(sig) != 64 {
		return errors.New("sig is not 64 bytes")
	}
	if new(big.Int).SetBytes(sig[:32]).Cmp(btcec.S256().P) >= 0 {
		return errors.New("sig r >= p")
	}
	if new(big.Int).SetBytes(sig[32:]).Cmp(btcec.S256().N) >= 0 {
		return errors.New("sig s >= n")
	}
	return nil
}

// Marshal renders the event as compact JSON in NIP-01 field order. Used for
// events this program creates; received events are kept as received.
func (e Event) Marshal() []byte {
	var b bytes.Buffer
	b.WriteString(`{"id":`)
	writeString(&b, e.ID)
	b.WriteString(`,"pubkey":`)
	writeString(&b, e.PubKey)
	b.WriteString(`,"created_at":`)
	b.WriteString(strconv.FormatInt(e.CreatedAt, 10))
	b.WriteString(`,"kind":`)
	b.WriteString(strconv.Itoa(e.Kind))
	b.WriteString(`,"tags":`)
	writeTags(&b, e.Tags)
	b.WriteString(`,"content":`)
	writeString(&b, e.Content)
	b.WriteString(`,"sig":`)
	writeString(&b, e.Sig)
	b.WriteByte('}')
	return b.Bytes()
}

// SecretKey is a throwaway secp256k1 key for synthetic events.
type SecretKey struct{ k *btcec.PrivateKey }

// NewSecretKey mints a random throwaway key.
func NewSecretKey() (SecretKey, error) {
	k, err := btcec.NewPrivateKey()
	if err != nil {
		return SecretKey{}, err
	}
	return SecretKey{k: k}, nil
}

// SecretKeyFromHex parses 64 hex characters.
func SecretKeyFromHex(s string) (SecretKey, error) {
	raw, err := hex.DecodeString(s)
	if err != nil || len(raw) != 32 {
		return SecretKey{}, errors.New("secret key: want 64 hex characters")
	}
	k, _ := btcec.PrivKeyFromBytes(raw)
	if k.Key.IsZero() {
		return SecretKey{}, errors.New("secret key: zero scalar")
	}
	return SecretKey{k: k}, nil
}

// Hex is the secret as 64 lowercase hex. Callers write it only to 0600
// scratch files outside the repository.
func (s SecretKey) Hex() string { return hex.EncodeToString(s.k.Serialize()) }

// PublicHex is the x-only public key as 64 lowercase hex.
func (s SecretKey) PublicHex() string {
	return hex.EncodeToString(schnorr.SerializePubKey(s.k.PubKey()))
}

// Sign fills pubkey, id and sig.
func (s SecretKey) Sign(e *Event) error {
	if e.Tags == nil {
		e.Tags = [][]string{}
	}
	e.PubKey = s.PublicHex()
	id := e.ComputeID()
	e.ID = hex.EncodeToString(id[:])
	sig, err := schnorr.Sign(s.k, id[:])
	if err != nil {
		return err
	}
	e.Sig = hex.EncodeToString(sig.Serialize())
	return nil
}

// tagValues returns the second element of every tag named name.
func tagValues(e Event, name string) []string {
	var out []string
	for _, t := range e.Tags {
		if len(t) >= 2 && t[0] == name {
			out = append(out, t[1])
		}
	}
	return out
}

// threadRefs mirrors upstream buzzio.threadRefs (internal/remote/buzzio/
// carrier.go:768): e tags marked reply, then root, then unmarked.
func threadRefs(e Event) []string {
	var ids []string
	for _, marker := range []string{"reply", "root", ""} {
		for _, t := range e.Tags {
			if len(t) < 2 || t[0] != "e" || !hex64.MatchString(t[1]) || len(t) >= 4 && t[3] != marker || len(t) < 4 && marker != "" {
				continue
			}
			ids = append(ids, t[1])
		}
	}
	return ids
}
