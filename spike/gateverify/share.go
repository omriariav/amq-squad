package main

// INTERIM (until upstream ask 8): the Go core reads the NIP-29 DM group by
// signing in with the share's enrolled body key, as the upstream Claude
// approval hook does (cmd/amq-remote/claude_approval.go:50-57 via
// relayConfigFor, cmd/amq-remote/relay.go:64-95). This depends on the
// amq-remote key layout <root>/extensions/remote/keys/<session>/{body.key,
// share.json} (internal/remote/sharestate/sharestate.go:46-51,71-155), which
// is not a published contract. The secret is read into memory only.

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// authGrantKind is the kind whose owner grant amq-remote attaches to its
// NIP-42 AUTH event (cmd/amq-remote/relay.go:29, authKind = 1059).
const authGrantKind = 1059

var shareSessionPattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)

// LoadBodyAuth reads the body secret and the owner's kind 1059 grant of an
// enrolled share, and checks the grant names the pinned owner and is
// unexpired. It does not verify the grant signature: the relay does at AUTH.
func LoadBodyAuth(root, session, owner string, now time.Time) (BodyAuth, string, error) {
	if !filepath.IsAbs(root) || !shareSessionPattern.MatchString(session) || session == "." || session == ".." {
		return BodyAuth{}, "", errors.New("share root must be absolute and session one safe path component")
	}
	dir := filepath.Join(root, "extensions", "remote", "keys", session)
	keyPath := filepath.Join(dir, "body.key")
	info, err := os.Lstat(keyPath)
	if err != nil {
		return BodyAuth{}, "", fmt.Errorf("body key: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
		return BodyAuth{}, "", errors.New("body key must be a regular 0600 file")
	}
	raw, err := os.ReadFile(keyPath)
	if err != nil {
		return BodyAuth{}, "", err
	}
	defer clear(raw)
	var secretHex, publicHex string
	for _, line := range strings.Split(string(raw), "\n") {
		f := strings.Fields(line)
		switch {
		case len(f) == 2 && f[0] == "secret":
			secretHex = f[1]
		case len(f) == 2 && f[0] == "public":
			publicHex = f[1]
		}
	}
	sk, err := SecretKeyFromHex(secretHex)
	if err != nil {
		return BodyAuth{}, "", fmt.Errorf("body key: %w", err)
	}
	body := sk.PublicHex()
	if publicHex != "" && publicHex != body {
		return BodyAuth{}, "", errors.New("body key public line does not match its secret")
	}
	shareRaw, err := os.ReadFile(filepath.Join(dir, "share.json"))
	if err != nil {
		return BodyAuth{}, "", fmt.Errorf("share.json (is the bundle enrolled?): %w", err)
	}
	var doc struct {
		Tags []struct {
			Kind        int    `json:"kind"`
			OwnerPubKey string `json:"owner_pubkey"`
			Conditions  string `json:"conditions"`
			Sig         string `json:"sig"`
		} `json:"tags"`
	}
	if err := json.Unmarshal(shareRaw, &doc); err != nil {
		return BodyAuth{}, "", fmt.Errorf("share.json: %w", err)
	}
	for _, t := range doc.Tags {
		if t.Kind != authGrantKind {
			continue
		}
		if t.OwnerPubKey != owner {
			return BodyAuth{}, "", fmt.Errorf("share is enrolled to owner %s, pinned owner is %s", t.OwnerPubKey, owner)
		}
		prefix := fmt.Sprintf("kind=%d&created_at<", authGrantKind)
		na, err := strconv.ParseInt(strings.TrimPrefix(t.Conditions, prefix), 10, 64)
		if !strings.HasPrefix(t.Conditions, prefix) || err != nil || now.Unix() >= na {
			return BodyAuth{}, "", errors.New("the share's AUTH grant is missing, non-canonical or expired; renew the share")
		}
		return BodyAuth{Secret: sk, AuthTag: []string{"auth", t.OwnerPubKey, t.Conditions, t.Sig}}, body, nil
	}
	return BodyAuth{}, "", errors.New("share.json has no kind 1059 AUTH grant")
}

// GrantTag returns the enrolled NIP-OA tag for kind (for the interim gate
// question post, which the body signs as amq-remote serve would:
// internal/remote/buzzio/carrier.go:96-108).
func GrantTag(root, session string, kind int, now time.Time) ([]string, error) {
	raw, err := os.ReadFile(filepath.Join(root, "extensions", "remote", "keys", session, "share.json"))
	if err != nil {
		return nil, err
	}
	var doc struct {
		Tags []struct {
			Kind        int    `json:"kind"`
			OwnerPubKey string `json:"owner_pubkey"`
			Conditions  string `json:"conditions"`
			Sig         string `json:"sig"`
		} `json:"tags"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, err
	}
	for _, t := range doc.Tags {
		prefix := fmt.Sprintf("kind=%d&created_at<", kind)
		if t.Kind != kind || !strings.HasPrefix(t.Conditions, prefix) {
			continue
		}
		na, err := strconv.ParseInt(strings.TrimPrefix(t.Conditions, prefix), 10, 64)
		if err != nil || now.Unix() >= na {
			return nil, fmt.Errorf("kind %d grant expired", kind)
		}
		return []string{"auth", t.OwnerPubKey, t.Conditions, t.Sig}, nil
	}
	return nil, fmt.Errorf("no enrolled grant for kind %d (enable buzz-dm)", kind)
}
