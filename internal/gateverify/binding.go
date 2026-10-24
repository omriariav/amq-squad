package gateverify

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"

	"github.com/omriariav/amq-squad/v3/internal/nostr"
	"github.com/omriariav/amq-squad/v3/internal/operatorauth"
)

// BindingSchema is the version of the owner's gate binding file.
const BindingSchema = 1

// Binding is the owner's gate binding, ~/.amq-squad/gate-binding.json: the
// relay and DM channel the gates go through, the owner's Buzz key (the only
// key whose answer counts), the share's body key (the signer of questions and
// re-asks), the carrier handles whose mailbox messages may carry a
// nostr:<event id> label, and the core's Ed25519 issuer key. The attachment
// (brief 4-5) writes it at enrollment; it holds no secret. It is an owner file
// guard protects, read only through the same owner-controlled, no-symlink,
// not group- or world-writable check as the trust store.
type Binding struct {
	Schema    int      `json:"schema"`
	Relay     string   `json:"relay"`
	Channel   string   `json:"channel"`
	Owner     string   `json:"owner"` // npub1... or 64 hex
	Body      string   `json:"body"`  // npub1... or 64 hex
	Carriers  []string `json:"carriers"`
	IssuerKey string   `json:"issuer_key,omitempty"` // absolute; default ~/.amq-squad/issuer-key.pem
}

var channelRe = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,128}$`)

// DefaultBindingPath and DefaultIssuerKeyPath are the owner files, "" without
// a home directory.
func DefaultBindingPath() string { return ownerFile("gate-binding.json") }

func DefaultIssuerKeyPath() string { return ownerFile("issuer-key.pem") }

// DefaultTrustStorePath is the owner's trust store (3-1 ruling 2).
func DefaultTrustStorePath() string { return ownerFile("authorization-trust.json") }

func ownerFile(name string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".amq-squad", name)
}

// LoadBinding reads and checks a binding file and returns it with its pin.
func LoadBinding(path string) (Binding, Pin, error) {
	if path == "" {
		return Binding{}, Pin{}, errors.New("no home directory for the gate binding")
	}
	raw, err := operatorauth.ReadAuthorizationEvidenceFile(path, 64<<10)
	if err != nil {
		return Binding{}, Pin{}, fmt.Errorf("gate binding %s: %w", path, err)
	}
	var b Binding
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&b); err != nil {
		return Binding{}, Pin{}, fmt.Errorf("gate binding %s: %w", path, err)
	}
	if _, err := dec.Token(); err != io.EOF {
		return Binding{}, Pin{}, fmt.Errorf("gate binding %s: trailing data", path)
	}
	owner, oerr := nostr.ParsePubKey(b.Owner)
	body, berr := nostr.ParsePubKey(b.Body)
	switch {
	case b.Schema != BindingSchema:
		return Binding{}, Pin{}, fmt.Errorf("gate binding %s: schema %d is not %d", path, b.Schema, BindingSchema)
	case oerr != nil || berr != nil || owner == body:
		return Binding{}, Pin{}, fmt.Errorf("gate binding %s: owner and body must be two different public keys", path)
	case !channelRe.MatchString(b.Channel):
		return Binding{}, Pin{}, fmt.Errorf("gate binding %s: bad channel", path)
	case b.IssuerKey != "" && (!filepath.IsAbs(b.IssuerKey) || filepath.Clean(b.IssuerKey) != b.IssuerKey):
		return Binding{}, Pin{}, fmt.Errorf("gate binding %s: issuer_key must be an absolute clean path", path)
	}
	if b.IssuerKey == "" {
		b.IssuerKey = DefaultIssuerKeyPath()
	}
	return b, Pin{Owner: owner, Channel: b.Channel, Body: body}, nil
}
