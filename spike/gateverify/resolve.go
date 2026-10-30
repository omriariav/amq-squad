package main

// Event-id resolution: from a delivered AMQ message (or from amq-remote's
// own ingress ledger) to the nostr event id of the owner's answer. The id is
// only a pointer: authority comes from fetching that event from the relay
// and verifying it, so a forged journal entry can at worst point at an
// event that then fails Verify.
//
// Three journals exist upstream at v0.102.0, one per delivery path:
//
//  1. amq-acp cockpit/BYOH mode (AMQ_ACP_TO): internal/acp/event.go:28-35,98
//     <root>/agents/<AM_ME>/outbox/acp-events/<event_id>.json =
//     {schema, event_id, message_id, to, thread, created}; the delivered
//     message also carries the label "nostr:<event_id>"
//     (internal/acp/deliver.go:72-73).
//  2. amq-acp binding mode, mailbox carrier (amq-remote attach --self):
//     internal/acp/mailbox.go:389-419, remote.go:458-475
//     <~/.amq/remote/acp>/remote-events/<agent pubkey>/<event_id>.mailbox.json
//     = {message_id, created, thread, channel, deadline}; the message is
//     from "buzz" (mailbox.go:25) with labels [acp, buzz] and no nostr label.
//  3. amq-remote serve, strict share DM edge: internal/remote/buzzio/
//     ledger.go:41-91, opened per share at cmd/amq-remote/relay_dm.go:128:
//     <root>/extensions/remote/shares/<session>/buzz/ingress/<event_id>.json
//     = Claim{event_id, owner, body, relay, channel, dm_channel, ...,
//     created_at}; the owner's DM is submitted to the share's target, not
//     delivered as an AMQ message.

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
)

// AMQHeader is the part of an AMQ message header resolution reads.
type AMQHeader struct {
	ID      string   `json:"id"`
	From    string   `json:"from"`
	To      []string `json:"to"`
	Thread  string   `json:"thread"`
	Created string   `json:"created"`
	Labels  []string `json:"labels"`
}

// ReadAMQHeader parses the ---json frontmatter of one message file.
func ReadAMQHeader(path string) (AMQHeader, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return AMQHeader{}, err
	}
	rest, ok := bytes.CutPrefix(raw, []byte("---json\n"))
	if !ok {
		return AMQHeader{}, errors.New("message has no ---json frontmatter")
	}
	end := bytes.Index(rest, []byte("\n---\n"))
	if end < 0 {
		return AMQHeader{}, errors.New("message frontmatter is not closed")
	}
	var h AMQHeader
	if err := json.Unmarshal(rest[:end], &h); err != nil {
		return AMQHeader{}, err
	}
	return h, nil
}

// ResolveCockpit maps a delivered message to its event id through the
// amq-acp cockpit journal. sender is the amq-acp AM_ME handle; operator is
// the handle the message must be addressed to.
func ResolveCockpit(root, sender, operator, messagePath string) (string, error) {
	h, err := ReadAMQHeader(messagePath)
	if err != nil {
		return "", err
	}
	if h.From != sender {
		return "", reject(RejectSender, "message is from %q, answers arrive from %q", h.From, sender)
	}
	if !slices.Contains(h.To, operator) {
		return "", reject(RejectSender, "message is not addressed to the operator handle %q", operator)
	}
	var labelled string
	for _, l := range h.Labels {
		if id, ok := strings.CutPrefix(l, "nostr:"); ok && hex64.MatchString(id) {
			if labelled != "" {
				return "", errors.New("message carries more than one nostr label")
			}
			labelled = id
		}
	}
	dir := filepath.Join(root, "agents", sender, "outbox", "acp-events")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", fmt.Errorf("acp journal: %w", err)
	}
	for _, ent := range entries {
		name := ent.Name()
		if !strings.HasSuffix(name, ".json") || !hex64.MatchString(strings.TrimSuffix(name, ".json")) {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			continue
		}
		var rec struct {
			EventID   string `json:"event_id"`
			MessageID string `json:"message_id"`
			To        string `json:"to"`
		}
		if json.Unmarshal(raw, &rec) != nil || rec.MessageID != h.ID {
			continue
		}
		if rec.EventID+".json" != name || rec.To != operator {
			return "", errors.New("acp journal entry is inconsistent with its name or recipient")
		}
		if labelled != "" && labelled != rec.EventID {
			return "", errors.New("message nostr label and acp journal disagree")
		}
		return rec.EventID, nil
	}
	return "", fmt.Errorf("no acp journal entry for message %s", h.ID)
}

// ResolveMailbox maps a binding-mode delivered message (from "buzz") to its
// event id through amq-acp's per-agent claims under stateDir/remote-events.
func ResolveMailbox(stateDir, operator, messagePath string) (string, error) {
	h, err := ReadAMQHeader(messagePath)
	if err != nil {
		return "", err
	}
	if h.From != "buzz" {
		return "", reject(RejectSender, "message is from %q, binding-mode answers arrive from \"buzz\"", h.From)
	}
	if !slices.Contains(h.To, operator) {
		return "", reject(RejectSender, "message is not addressed to the operator handle %q", operator)
	}
	matches, _ := filepath.Glob(filepath.Join(stateDir, "remote-events", "*", "*.mailbox.json"))
	legacy, _ := filepath.Glob(filepath.Join(stateDir, "remote-events", "*.mailbox.json"))
	for _, path := range append(matches, legacy...) {
		id := strings.TrimSuffix(filepath.Base(path), ".mailbox.json")
		if !hex64.MatchString(id) {
			continue
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var claim struct {
			MessageID string `json:"message_id"`
		}
		if json.Unmarshal(raw, &claim) == nil && claim.MessageID == h.ID {
			return id, nil
		}
	}
	return "", fmt.Errorf("no mailbox claim for message %s", h.ID)
}

// IngressClaim is the part of an amq-remote serve ingress claim used here.
type IngressClaim struct {
	EventID   string `json:"event_id"`
	Owner     string `json:"owner"`
	Body      string `json:"body"`
	Channel   string `json:"channel"`
	DMChannel string `json:"dm_channel"`
	CreatedAt int64  `json:"created_at"`
}

// ResolveIngress lists the owner DM events amq-remote serve claimed in the
// pinned channel at or after since (unix seconds), newest first. Each
// candidate is only a pointer; the caller fetches and verifies it.
//
// An unreadable or malformed claim file is skipped and reported in skipped,
// never fatal: one bad file must not hide a good claim. A missing ingress
// directory is an empty result (serve creates it on its first claim).
// serve's settlement files (<id>.settled.json) are not claims and are
// ignored silently.
func ResolveIngress(root, session, owner, channel string, since int64) (claims []IngressClaim, skipped []string, err error) {
	if !shareSessionPattern.MatchString(session) || session == "." || session == ".." {
		return nil, nil, fmt.Errorf("share session %q is not one safe path component", session)
	}
	dir := filepath.Join(root, "extensions", "remote", "shares", session, "buzz", "ingress")
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, fmt.Errorf("serve ingress ledger: %w", err)
	}
	for _, ent := range entries {
		name := ent.Name()
		if !strings.HasSuffix(name, ".json") || strings.HasSuffix(name, ".settled.json") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			skipped = append(skipped, name+": "+err.Error())
			continue
		}
		var c IngressClaim
		if err := json.Unmarshal(raw, &c); err != nil {
			skipped = append(skipped, name+": "+err.Error())
			continue
		}
		if c.EventID+".json" != name || !hex64.MatchString(c.EventID) {
			skipped = append(skipped, name+": claim does not match its file name")
			continue
		}
		if c.Owner == owner && c.Channel == channel && c.CreatedAt >= since {
			claims = append(claims, c)
		}
	}
	sort.Slice(claims, func(i, j int) bool { return claims[i].CreatedAt > claims[j].CreatedAt })
	return claims, skipped, nil
}
