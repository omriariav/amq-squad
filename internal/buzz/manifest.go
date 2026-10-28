package buzz

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// The share root's amq-remote manifest, the fields the binding reads
// (v0.116.2 internal/remote/manifest/manifest.go; cmd/amq-remote/README.md
// "Manifest" and "Strict share in one owner step"). amq-remote writes it
// (`share --target --relay`); amq-squad only reads it.
type manifestFile struct {
	Adapters []struct {
		Kind   string          `json:"kind"`
		Target string          `json:"target"`
		Config json.RawMessage `json:"config"`
	} `json:"adapters"`
	Relay *struct {
		URL    string `json:"url"`
		Shares []struct {
			Target          string `json:"target"`
			Session         string `json:"session"`
			OwnerPubKey     string `json:"owner_pubkey"`
			DMChannelID     string `json:"dm_channel_id"`
			NativeSessionID string `json:"native_session_id"`
			Commands        bool   `json:"commands"`
			MinEvidence     string `json:"min_evidence"`
			Presence        bool   `json:"presence"`
		} `json:"shares"`
	} `json:"relay"`
}

// MailboxBinding is the share's binding as the manifest states it.
type MailboxBinding struct {
	Target       string `json:"target"`
	Kind         string `json:"kind"`
	Handle       string `json:"handle"`
	ReplyTimeout string `json:"reply_timeout"`
	Channel      string `json:"dm_channel_id"`
	Commands     bool   `json:"commands"`
	MinEvidence  string `json:"min_evidence"`
	Presence     bool   `json:"presence"`
}

// CheckMailboxShare reads the share root's manifest and requires the one
// path of ruling 20: the session's relay share bound to the mailbox adapter
// target with config.handle MailboxHandle and reply_timeout 60s, DM commands
// on for channel with native_session_id the handle and min_evidence
// submitted, presence on, and the handle's inbox in the share root. A share
// still bound to the retired presence-only fake adapter (brief 4-5
// deviation 1) is refused: there is no fallback; enroll again.
func CheckMailboxShare(root, session, channel, relay string) (MailboxBinding, error) {
	path := filepath.Join(root, "extensions", "remote", "manifest.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		return MailboxBinding{}, fmt.Errorf("share manifest: %w (enroll with docs/v3-buzz/enroll.sh)", err)
	}
	var m manifestFile
	if err := json.Unmarshal(raw, &m); err != nil {
		return MailboxBinding{}, fmt.Errorf("share manifest %s: %w", path, err)
	}
	if m.Relay == nil {
		return MailboxBinding{}, fmt.Errorf("share manifest %s has no relay block (enroll with docs/v3-buzz/enroll.sh)", path)
	}
	if relay != "" && m.Relay.URL != relay {
		return MailboxBinding{}, fmt.Errorf("share manifest relay is %s, not %s", m.Relay.URL, relay)
	}
	var b MailboxBinding
	found := false
	for _, s := range m.Relay.Shares {
		if s.Session != session {
			continue
		}
		found = true
		b = MailboxBinding{Target: s.Target, Channel: s.DMChannelID, Commands: s.Commands, MinEvidence: s.MinEvidence, Presence: s.Presence}
		if s.NativeSessionID != MailboxHandle {
			return b, fmt.Errorf("share %s: native_session_id is %q, not the mailbox handle %q (rerun enroll.sh with BUZZ_DM_CHANNEL)", session, s.NativeSessionID, MailboxHandle)
		}
	}
	if !found {
		return b, fmt.Errorf("share manifest names no relay share for session %s", session)
	}
	if b.Target != MailboxTarget {
		return b, fmt.Errorf("share %s is bound to target %q, not the mailbox adapter %q: the presence-only fake adapter of 4-5 is retired; enroll again with docs/v3-buzz/enroll.sh", session, b.Target, MailboxTarget)
	}
	for _, a := range m.Adapters {
		if a.Target != b.Target {
			continue
		}
		b.Kind = a.Kind
		var c struct {
			Handle       string `json:"handle"`
			ReplyTimeout string `json:"reply_timeout"`
		}
		_ = json.Unmarshal(a.Config, &c)
		b.Handle, b.ReplyTimeout = c.Handle, c.ReplyTimeout
	}
	switch d, _ := time.ParseDuration(b.ReplyTimeout); {
	case b.Kind != "mailbox":
		return b, fmt.Errorf("share target %s is kind %q, not mailbox (amq-remote v0.116.2 or later)", b.Target, b.Kind)
	case b.Handle != MailboxHandle:
		return b, fmt.Errorf("mailbox target %s delivers to %q, not %q", b.Target, b.Handle, MailboxHandle)
	case d != ReplyTimeout:
		return b, fmt.Errorf("mailbox target %s reply_timeout is %q, not %s", b.Target, b.ReplyTimeout, ReplyTimeout)
	case !b.Commands || b.Channel != channel:
		return b, fmt.Errorf("share %s: DM commands are not on for channel %s (rerun enroll.sh with BUZZ_DM_CHANNEL)", session, channel)
	case b.MinEvidence != "submitted":
		return b, fmt.Errorf("share %s: min_evidence is %q; the mailbox adapter proves delivery only (submitted)", session, b.MinEvidence)
	case !b.Presence:
		return b, fmt.Errorf("share %s: presence is off", session)
	}
	if info, err := os.Stat(filepath.Join(root, "agents", MailboxHandle, "inbox")); err != nil || !info.IsDir() {
		return b, fmt.Errorf("the mailbox handle %s has no inbox in %s (amq init --agents %s)", MailboxHandle, root, MailboxHandle)
	}
	return b, nil
}
