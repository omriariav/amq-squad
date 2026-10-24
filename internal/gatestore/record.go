// Package gatestore is the gate store the Go core writes (plan 7.3, R32):
// under the AMQ session root, <root>/extensions/amq-squad/
//
//	gates/<id>.json            the gate record, created once (O_EXCL, fsynced) before the question is sent
//	gates/<id>.question.json   the question as posted, created once
//	reasks/<id>.json           the one "reply here to confirm" re-post (amendment 19), created once
//	answers/<id>.json          a verified approval: the operatorauth envelope plus the owner's signed event
//	denials/<id>.json          a verified owner deny: the signed deny record
//	consumed/{gates,events,locks}/   the single-use ledger: marks written after the answer file, both fsynced
//
// The record is immutable and holds the full target digest; the verifier
// compares the owner's typed prefix against it only, and the action after an
// approval runs against the record's digest, never against anything a seat
// presents later. The 2a-3 readers (context, guard) read gates/ and answers/
// in the S1 layout; this package is the only writer.
//
// Threat model: same-UID processes can write here; that is accepted for phase
// 1 on a single-user Mac (plan 7.3). guard keeps the directory a protected
// path, so a seat's tool call that writes it is denied.
package gatestore

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/gatetarget"
	"github.com/omriariav/amq-squad/v3/internal/operatorauth"
)

// RecordSchema names the record format.
const RecordSchema = "amq-squad-gate/1"

const (
	// ClockSkew is the one clock allowance (S1 1.2): an answer may be dated
	// up to 60 s before the gate was issued and up to 60 s after the
	// verifier's clock.
	ClockSkew = 60 * time.Second
	// DefaultWindow is the gate window (3-1 ruling 5, a proposal).
	DefaultWindow = 30 * time.Minute
	// MaxWindow bounds a gate window.
	MaxWindow = 24 * time.Hour
	// PrefixLen is the digest prefix the owner types: 16 hex (R32).
	PrefixLen = 16
)

// Gate ids follow operatorauth's gate grammar restricted to one path segment
// the owner can type: lowercase letters, digits, '.', '_' and '-', 1..64
// characters, not "." or "..", leading dot allowed (2a-3 fix13). Ids ending
// in ".question" are refused, because gates/<id>.question.json is the
// question of gate <id>.
var gateIDPattern = regexp.MustCompile(`^[a-z0-9._-]{1,64}$`)

// ValidGateID checks a gate id.
func ValidGateID(id string) error {
	if !gateIDPattern.MatchString(id) || id == "." || id == ".." || strings.HasSuffix(id, ".question") {
		return fmt.Errorf("gate id %q must be 1..64 of [a-z0-9._-], not . or .., not ending in .question", id)
	}
	if _, err := operatorauth.CanonicalGateThread("gate/" + id); err != nil {
		return fmt.Errorf("gate id %q: %w", id, err)
	}
	return nil
}

// Record is the immutable gate record. Class is the operatorauth action (the
// 2a-3 reader compares it with the envelope's payload action); TypedClass is
// what the owner reads and types (release, send, credential, grow).
type Record struct {
	Schema      string `json:"schema"`
	GateID      string `json:"gate_id"`
	Class       string `json:"class"`
	GateKind    string `json:"gate_kind"`
	TypedClass  string `json:"typed_class"`
	Target      string `json:"target"`
	Digest      string `json:"digest"`
	Text        string `json:"text"`
	TextSHA256  string `json:"text_sha256"`
	IssuedAt    string `json:"issued_at"`
	NotBefore   string `json:"not_before"`
	NotAfter    string `json:"not_after"`
	RaisedBy    string `json:"raised_by"`
	OwnerPubKey string `json:"owner_pubkey"`  // 64 hex, the one owner whose BIP-340 answer counts
	IssuerKeyID string `json:"issuer_key_id"` // the one Ed25519 key that may sign the envelope
	// Squad is the identity of the squad whose store holds the gate
	// (SquadID of its AMQ root; 4-5 re-pass, amendment 28). It is in the
	// gate text, so two squads' questions for the same gate id, action and
	// target never share an event id. Additive; a record without it is
	// unverifiable (gateverify refuses it).
	Squad string `json:"squad,omitempty"`
}

// Spec is the core's input to Render.
type Spec struct {
	GateID   string
	Action   string // operatorauth action, a typed-gate action (gatetarget.Actions)
	Target   string // a gatetarget target string
	RaisedBy string // the handle that raised the gate
	Window   time.Duration
	Squad    string // SquadID of the store's root; Store.Raise fills it
}

// Identity is who may answer a gate and who may sign its envelope.
type Identity struct {
	OwnerPubKey string
	IssuerKeyID string
}

var (
	hex64       = regexp.MustCompile(`^[0-9a-f]{64}$`)
	issuerKeyID = regexp.MustCompile(`^ed25519-sha256:[0-9a-f]{64}$`)
	squadIDRe   = regexp.MustCompile(`^root-sha256:[0-9a-f]{64}$`)
	handleRe    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
)

// Text is the only renderer of the owner-facing gate text. It carries the
// gate id, the class and action, the target string, the full digest and the
// two answer lines, and says "reply in this thread" (amendment 19).
func Text(id, typedClass, action, target, digest string) string {
	return TextFor("", id, typedClass, action, target, digest)
}

// TextFor is Text with the squad identity line (when squad is set).
func TextFor(squad, id, typedClass, action, target, digest string) string {
	prefix := strings.TrimPrefix(digest, "sha256:")[:PrefixLen]
	head := []string{"amq-squad gate " + id}
	if squad != "" {
		head = append(head, "squad: "+squad)
	}
	return strings.Join(append(head,
		"class: "+typedClass+" ("+action+")",
		"target: "+target,
		"digest: "+digest,
		"Reply in this thread with one line:",
		fmt.Sprintf("approve %s %s %s", id, typedClass, prefix),
		fmt.Sprintf("deny %s %s %s", id, typedClass, prefix),
	), "\n")
}

// SquadID is the squad identity of a gate store's AMQ root: the SHA-256 of
// its clean absolute path.
func SquadID(root string) string {
	sum := sha256.Sum256([]byte(filepath.Clean(root)))
	return "root-sha256:" + hex.EncodeToString(sum[:])
}

// Render builds the record at now; Store.Raise writes it.
func Render(s Spec, now time.Time, id Identity) (Record, error) {
	if err := ValidGateID(s.GateID); err != nil {
		return Record{}, err
	}
	if err := gatetarget.Validate(s.Action, s.Target); err != nil {
		return Record{}, err
	}
	if len(s.Target) > 512 {
		return Record{}, errors.New("target string is longer than 512 bytes")
	}
	if !handleRe.MatchString(s.RaisedBy) {
		return Record{}, fmt.Errorf("raised_by %q is not a handle", s.RaisedBy)
	}
	if s.Window == 0 {
		s.Window = DefaultWindow
	}
	if s.Window < time.Minute || s.Window > MaxWindow || s.Window%time.Second != 0 {
		return Record{}, errors.New("gate window must be 1 minute .. 24 hours in whole seconds")
	}
	if !hex64.MatchString(id.OwnerPubKey) || !issuerKeyID.MatchString(id.IssuerKeyID) {
		return Record{}, errors.New("a gate needs the owner pubkey (64 hex) and the issuer key id (ed25519-sha256:<hex>)")
	}
	if s.Squad != "" && !squadIDRe.MatchString(s.Squad) {
		return Record{}, errors.New("squad identity must be root-sha256:<64 hex>")
	}
	capability, err := operatorauth.LookupAction(s.Action)
	if err != nil {
		return Record{}, err
	}
	digest := gatetarget.Digest(s.Action, s.Target)
	class := gatetarget.Class(s.Action)
	text := TextFor(s.Squad, s.GateID, class, s.Action, s.Target, digest)
	now = now.UTC().Truncate(time.Second)
	return Record{
		Schema: RecordSchema, GateID: s.GateID, Class: s.Action, GateKind: capability.GateKind, TypedClass: class,
		Target: s.Target, Digest: digest, Text: text, TextSHA256: sha256Text([]byte(text)),
		IssuedAt: now.Format(time.RFC3339), NotBefore: now.Add(-ClockSkew).Format(time.RFC3339),
		NotAfter: now.Add(s.Window).Format(time.RFC3339), RaisedBy: s.RaisedBy,
		OwnerPubKey: id.OwnerPubKey, IssuerKeyID: id.IssuerKeyID, Squad: s.Squad,
	}, nil
}

func sha256Text(b []byte) string {
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// Window returns the record's issue time, not_before and not_after.
func (r Record) Window() (issued, notBefore, notAfter time.Time, err error) {
	if issued, err = time.Parse(time.RFC3339, r.IssuedAt); err != nil {
		return issued, notBefore, notAfter, errors.New("gate record issued_at is invalid")
	}
	if notBefore, err = time.Parse(time.RFC3339, r.NotBefore); err != nil || !notBefore.Equal(issued.Add(-ClockSkew)) {
		return issued, notBefore, notAfter, errors.New("gate record not_before is not issued_at - 60 s")
	}
	if notAfter, err = time.Parse(time.RFC3339, r.NotAfter); err != nil || !notAfter.After(issued) || notAfter.Sub(issued) > MaxWindow {
		return issued, notBefore, notAfter, errors.New("gate record not_after is invalid")
	}
	return issued, notBefore, notAfter, nil
}

// Validate checks that a record is self-consistent: the digest is the target's,
// the text is the rendering of the record's own fields, the window is sound.
func (r Record) Validate() error {
	capability, err := operatorauth.LookupAction(r.Class)
	switch {
	case r.Schema != RecordSchema || ValidGateID(r.GateID) != nil || gatetarget.Validate(r.Class, r.Target) != nil || err != nil:
		return errors.New("gate record is malformed")
	case capability.GateKind != r.GateKind || gatetarget.Class(r.Class) != r.TypedClass:
		return errors.New("gate record kind or class is not its action's")
	case r.Digest != gatetarget.Digest(r.Class, r.Target):
		return errors.New("gate record digest is not its target's")
	case !hex64.MatchString(r.OwnerPubKey) || !issuerKeyID.MatchString(r.IssuerKeyID) || !handleRe.MatchString(r.RaisedBy):
		return errors.New("gate record identities are malformed")
	case r.Squad != "" && !squadIDRe.MatchString(r.Squad):
		return errors.New("gate record squad identity is malformed")
	case r.Text != TextFor(r.Squad, r.GateID, r.TypedClass, r.Class, r.Target, r.Digest):
		return errors.New("gate record text is not the rendering of its own fields")
	case r.TextSHA256 != sha256Text([]byte(r.Text)):
		return errors.New("gate record text digest does not match its text")
	}
	_, _, _, err = r.Window()
	return err
}

// Answer is a parsed owner reply: "approve|deny <gate-id> <class> <prefix>".
type Answer struct {
	Verb         string `json:"verb"`
	GateID       string `json:"gate_id"`
	Class        string `json:"class"`
	DigestPrefix string `json:"digest_prefix"`
}

var prefixRe = regexp.MustCompile(`^[0-9a-f]{16,64}$`)

// ParseAnswer reads exactly one answer tuple. Any CR or LF anywhere is
// refused (plan 7.3); case is folded and runs of blanks are one separator,
// because a phone keyboard capitalizes the first word; any extra token or a
// prefix shorter than 16 hex is refused.
func ParseAnswer(content string) (Answer, error) {
	if strings.ContainsAny(content, "\r\n") {
		return Answer{}, errors.New("answer must be one line (no CR or LF)")
	}
	f := strings.Fields(strings.ToLower(content))
	if len(f) != 4 {
		return Answer{}, fmt.Errorf("answer must be exactly 4 tokens, got %d", len(f))
	}
	a := Answer{Verb: f[0], GateID: f[1], Class: f[2], DigestPrefix: f[3]}
	switch {
	case a.Verb != "approve" && a.Verb != "deny":
		return Answer{}, fmt.Errorf("answer verb %q is not approve or deny", f[0])
	case ValidGateID(a.GateID) != nil:
		return Answer{}, errors.New("answer gate id is malformed")
	case !validTypedClass(a.Class):
		return Answer{}, errors.New("answer class is not a typed gate class")
	case !prefixRe.MatchString(a.DigestPrefix):
		return Answer{}, fmt.Errorf("answer digest prefix must be %d..64 hex", PrefixLen)
	}
	return a, nil
}

func validTypedClass(c string) bool {
	switch c {
	case gatetarget.ClassRelease, gatetarget.ClassSend, gatetarget.ClassCredential, gatetarget.ClassGrow:
		return true
	}
	return false
}

// Matches reports whether an answer names this gate: id, class and a prefix
// of the record's full digest.
func (r Record) Matches(a Answer) bool {
	return a.GateID == r.GateID && a.Class == r.TypedClass && strings.HasPrefix(strings.TrimPrefix(r.Digest, "sha256:"), a.DigestPrefix)
}
