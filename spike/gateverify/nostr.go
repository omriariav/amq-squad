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

// ParseEvent decodes one event and refuses anything that is not exactly
// the seven NIP-01 fields with canonical lowercase hex encodings. A relay or
// a file may not add, drop or null a field.
func ParseEvent(raw []byte) (Event, error) {
	raw = bytes.TrimSpace(raw)
	var fields map[string]json.RawMessage
	if err := strictDecode(raw, &fields); err != nil {
		return Event{}, fmt.Errorf("event: %w", err)
	}
	if len(fields) != len(eventFields) {
		return Event{}, fmt.Errorf("event: want exactly the %d NIP-01 fields, got %d", len(eventFields), len(fields))
	}
	for _, name := range eventFields {
		v, ok := fields[name]
		if !ok {
			return Event{}, fmt.Errorf("event: missing field %q", name)
		}
		if bytes.Equal(bytes.TrimSpace(v), []byte("null")) {
			return Event{}, fmt.Errorf("event: field %q is null", name)
		}
	}
	var e Event
	if err := strictDecode(raw, &e); err != nil {
		return Event{}, fmt.Errorf("event: %w", err)
	}
	switch {
	case !hex64.MatchString(e.ID):
		return Event{}, errors.New("event: id is not 64 lowercase hex")
	case !hex64.MatchString(e.PubKey):
		return Event{}, errors.New("event: pubkey is not 64 lowercase hex")
	case !hex128.MatchString(e.Sig):
		return Event{}, errors.New("event: sig is not 128 lowercase hex")
	case e.CreatedAt < 0 || e.Kind < 0 || e.Kind > 65535:
		return Event{}, errors.New("event: created_at or kind out of range")
	case !utf8.ValidString(e.Content):
		return Event{}, errors.New("event: content is not UTF-8")
	}
	if ambiguousControl(e.Content) {
		return Event{}, errors.New("event: content holds a control character whose id escaping differs between nostr libraries")
	}
	for _, t := range e.Tags {
		if t == nil {
			return Event{}, errors.New("event: a tag is null")
		}
		for _, s := range t {
			if !utf8.ValidString(s) {
				return Event{}, errors.New("event: a tag value is not UTF-8")
			}
			if ambiguousControl(s) {
				return Event{}, errors.New("event: a tag holds a control character whose id escaping differs between nostr libraries")
			}
		}
	}
	return e, nil
}

// ambiguousControl reports a C0 control other than \t \n \r. NIP-01 escapes
// \b and \f, and serde-style encoders escape the rest as \u00XX, but
// fiatjaf.com/nostr (the library amq-remote v0.102.0 verifies with) writes
// them verbatim (event.go escTable: only " \ \n \r \t). The same signed
// event then has two ids depending on who hashes it. The conventions agree
// exactly when none of these bytes occur, so the verifier refuses them
// rather than pick a side. A gate answer is one line of ASCII tokens.
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
	sig, err := schnorr.ParseSignature(sigRaw)
	if err != nil {
		return fmt.Errorf("sig: %w", err)
	}
	if !sig.Verify(id, pub) {
		return errors.New("BIP-340 signature does not verify")
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
