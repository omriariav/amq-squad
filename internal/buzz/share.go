// Package buzz is the Buzz attachment of amq-squad 3.0 (plan 3.6, 9; R42;
// brief 4-5): one Buzz agent, the body of an amq-remote strict share that the
// owner enrolled with the owner script (docs/v3-buzz/enroll.sh), attached to
// one mailbox. The core reads the owner's DMs from the relay with the body
// key, hands a gate answer to the 3-1 verifier, answers /status from the
// status document, and routes every other message into AMQ. amq-remote
// serve publishes the body's profile and presence; the attachment starts it
// while the bound session is present in the status document and stops it
// when it is not.
//
// No code here reads, holds or asks for the owner key (R33): the owner key
// enters amq-owner-sign only, through the owner script's password-manager
// pipe. The body key is the share's own key, minted by amq-remote.
package buzz

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/gateverify"
	"github.com/omriariav/amq-squad/v3/internal/nostr"
)

// Grant kinds the attachment uses. amq-remote presents the kind 1059 grant
// on its NIP-42 AUTH (cmd/amq-remote/relay.go authKind); a DM post carries
// the kind 9 grant (buzz-dm); the profile needs kind 0 (buzz-profile).
const (
	KindAuthGrant = 1059
	KindDM        = 9
	KindEdit      = 40003
	KindProfile   = 0
)

// BaseKinds are amq-remote's ShareKinds (internal/remote/bodykey); the
// enrollment default adds buzz-dm (9, 40003) and buzz-profile (0), so a
// complete default enrollment holds 8 grants (S1: 8 with the profile).
var BaseKinds = []int{20003, 1059, 24200, 44200, 10100}

// DefaultKinds are the kinds of the enrollment default (amendment 19).
var DefaultKinds = append(slices.Clone(BaseKinds), KindDM, KindEdit, KindProfile)

// Tag is one enrolled owner grant (NIP-OA), as share.json stores it.
type Tag struct {
	Kind        int    `json:"kind"`
	OwnerPubKey string `json:"owner_pubkey"`
	Conditions  string `json:"conditions"`
	Sig         string `json:"sig"`
}

// NotAfter is the grant's created_at bound.
func (t Tag) NotAfter() (time.Time, error) {
	prefix := fmt.Sprintf("kind=%d&created_at<", t.Kind)
	if !strings.HasPrefix(t.Conditions, prefix) {
		return time.Time{}, fmt.Errorf("kind %d grant conditions are not the canonical share form", t.Kind)
	}
	na, err := strconv.ParseInt(strings.TrimPrefix(t.Conditions, prefix), 10, 64)
	if err != nil || na <= 0 {
		return time.Time{}, fmt.Errorf("kind %d grant conditions are not the canonical share form", t.Kind)
	}
	return time.Unix(na, 0).UTC(), nil
}

// Wire is the NIP-OA tag array.
func (t Tag) Wire() []string { return []string{"auth", t.OwnerPubKey, t.Conditions, t.Sig} }

// Share is one enrolled amq-remote share: its body key and its grants.
type Share struct {
	Root    string
	Session string
	Body    nostr.SecretKey
	Owner   string // the one owner that signed every grant, 64 hex
	Tags    []Tag
}

// BodyHex is the body's public key.
func (s Share) BodyHex() string { return s.Body.PublicHex() }

// GrantCount is the number of enrolled grants.
func (s Share) GrantCount() int { return len(s.Tags) }

// Kinds lists the enrolled kinds in file order.
func (s Share) Kinds() []int {
	out := make([]int, 0, len(s.Tags))
	for _, t := range s.Tags {
		out = append(out, t.Kind)
	}
	return out
}

// Expires is the generation's not-after (every tag shares it).
func (s Share) Expires() time.Time {
	if len(s.Tags) == 0 {
		return time.Time{}
	}
	na, _ := s.Tags[0].NotAfter()
	return na
}

// Grant returns the unexpired grant for kind as a NIP-OA tag.
func (s Share) Grant(kind int, now time.Time) ([]string, error) {
	for _, t := range s.Tags {
		if t.Kind != kind {
			continue
		}
		na, err := t.NotAfter()
		if err != nil {
			return nil, err
		}
		if !now.Before(na) {
			return nil, fmt.Errorf("the kind %d grant expired at %s; renew the share with the owner script", kind, na.Format(time.RFC3339))
		}
		return t.Wire(), nil
	}
	return nil, fmt.Errorf("the share holds no kind %d grant; enroll it with the owner script's default surfaces", kind)
}

var sessionRe = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)

// KeyDir is amq-remote's key directory for a share session. The layout
// <root>/extensions/remote/keys/<session>/{body.key,share.json} is
// amq-remote's (internal/remote/sharestate), not a published contract; it is
// read here as S1 and 3-1 read it (INTERIM until upstream ask 8).
func KeyDir(root, session string) (string, error) {
	if !filepath.IsAbs(root) || filepath.Clean(root) != root {
		return "", errors.New("share root must be an absolute clean path")
	}
	if !sessionRe.MatchString(session) || session == "." || session == ".." {
		return "", errors.New("share session must be one safe path component")
	}
	return filepath.Join(root, "extensions", "remote", "keys", session), nil
}

// LoadShare reads an enrolled share. The body key must be a regular 0600
// file, not a symlink; share.json must hold at least one tag, every tag
// signed by one owner under one not-after. The body secret is read into
// memory only, and the file buffer is cleared.
func LoadShare(root, session string) (Share, error) {
	dir, err := KeyDir(root, session)
	if err != nil {
		return Share{}, err
	}
	// by construction the body key is in guard's protected set: a share
	// outside ~/.amq-squad/buzz/ is refused (4-5 review item 2)
	if !gateverify.UnderBuzzDir(root) {
		return Share{}, fmt.Errorf("share root %s is not under %s, the directory guard protects", root, gateverify.BuzzDir())
	}
	for _, p := range []string{filepath.Join(root, "extensions"), filepath.Join(root, "extensions", "remote"), filepath.Join(root, "extensions", "remote", "keys"), dir} {
		info, err := os.Lstat(p)
		if err != nil {
			return Share{}, fmt.Errorf("share %s: %w (enroll it with the owner script)", session, err)
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return Share{}, fmt.Errorf("share %s: %s is not a real directory", session, p)
		}
	}
	keyPath := filepath.Join(dir, "body.key")
	info, err := os.Lstat(keyPath)
	if err != nil {
		return Share{}, fmt.Errorf("share %s: body key: %w", session, err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
		return Share{}, fmt.Errorf("share %s: body key must be a regular 0600 file", session)
	}
	raw, err := os.ReadFile(keyPath)
	if err != nil {
		return Share{}, fmt.Errorf("share %s: body key: %w", session, err)
	}
	body, err := parseBodyKey(raw)
	clear(raw)
	if err != nil {
		return Share{}, fmt.Errorf("share %s: body key: %w", session, err)
	}
	shareRaw, err := os.ReadFile(filepath.Join(dir, "share.json"))
	if err != nil {
		return Share{}, fmt.Errorf("share %s: share.json (is the bundle enrolled?): %w", session, err)
	}
	var doc struct {
		Tags []Tag `json:"tags"`
	}
	if err := json.Unmarshal(shareRaw, &doc); err != nil {
		return Share{}, fmt.Errorf("share %s: share.json: %w", session, err)
	}
	if len(doc.Tags) == 0 {
		return Share{}, fmt.Errorf("share %s: share.json holds no grant (enroll the bundle)", session)
	}
	s := Share{Root: root, Session: session, Body: body, Tags: doc.Tags}
	var notAfter time.Time
	for i, t := range doc.Tags {
		if !nostr.IsHex64(t.OwnerPubKey) {
			return Share{}, fmt.Errorf("share %s: kind %d grant names no owner key", session, t.Kind)
		}
		na, err := t.NotAfter()
		if err != nil {
			return Share{}, fmt.Errorf("share %s: %w", session, err)
		}
		if i == 0 {
			s.Owner, notAfter = t.OwnerPubKey, na
			continue
		}
		if t.OwnerPubKey != s.Owner {
			return Share{}, fmt.Errorf("share %s: grants are signed by more than one owner", session)
		}
		if !na.Equal(notAfter) {
			return Share{}, fmt.Errorf("share %s: grants span more than one window", session)
		}
	}
	return s, nil
}

// parseBodyKey reads amq-remote's body key format: a "secret <64 hex>" line
// and an optional "public <64 hex>" line that must match it.
func parseBodyKey(raw []byte) (nostr.SecretKey, error) {
	var secretHex, publicHex string
	for _, line := range strings.FieldsFunc(string(raw), func(r rune) bool { return r == '\n' || r == '\r' }) {
		f := strings.Fields(line)
		switch {
		case len(f) == 2 && f[0] == "secret" && secretHex == "":
			secretHex = f[1]
		case len(f) == 2 && f[0] == "public" && publicHex == "":
			publicHex = f[1]
		case len(f) == 0:
		default:
			return nostr.SecretKey{}, errors.New("not an amq-remote body key file")
		}
	}
	sk, err := nostr.SecretKeyFromHex(secretHex)
	if err != nil {
		return nostr.SecretKey{}, err
	}
	if publicHex != "" && publicHex != sk.PublicHex() {
		return nostr.SecretKey{}, errors.New("the public line does not match the secret")
	}
	return sk, nil
}
