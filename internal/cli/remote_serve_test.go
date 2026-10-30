package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/omriariav/amq-squad/v3/internal/buzz"
)

// cliServe stands in for amq-remote serve's mailbox adapter in the CLI
// tests (the shape of v0.116.2 internal/remote/mailbox/mailbox.go, prompt):
// an owner DM becomes one message from buzz labelled nostr:<event id> in the
// attach inbox; the newest non-status reply is the request's result.
type cliServe struct {
	mu      sync.Mutex
	n       int
	inbox   []buzz.Prompt
	replies []serveRow
}

type serveRow struct {
	To, Text string
	Status   bool
}

func (s *cliServe) deliver(eventID, text string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.n++
	id := fmt.Sprintf("2026-10-28T12-00-00.%03dZ_pid1_cli", s.n)
	s.inbox = append(s.inbox, buzz.Prompt{ID: id, From: buzz.CarrierHandle, Thread: "cockpit/dm-chan",
		Labels: []string{"acp", "buzz", "nostr:" + eventID}, Body: text + buzz.ServePromptSuffix + " Answer with `amq reply --id " + id + "`.\n"})
	return id
}

func (s *cliServe) New(context.Context) ([]buzz.Prompt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.inbox), nil
}

func (s *cliServe) Take(_ context.Context, id string) (buzz.Prompt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, p := range s.inbox {
		if p.ID == id {
			s.inbox = slices.Delete(s.inbox, i, i+1)
			return p, nil
		}
	}
	return buzz.Prompt{}, fmt.Errorf("no message %s", id)
}

func (s *cliServe) Reply(_ context.Context, id, text string, status bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.replies = append(s.replies, serveRow{To: id, Text: text, Status: status})
	return nil
}

// result is the newest non-status reply to id and the count of them.
func (s *cliServe) result(id string) (string, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	res, n := "", 0
	for _, r := range s.replies {
		if r.To == id && !r.Status && strings.TrimSpace(r.Text) != "" {
			res, n = r.Text, n+1
		}
	}
	return res, n
}

// fixtureServe is the fake serve of a gate CLI fixture: the share root's
// manifest bound to the mailbox adapter, the attach inbox, and the
// remoteInbox seam pointed at the fake.
var (
	fixtureServesMu sync.Mutex
	fixtureServes   = map[string]*cliServe{}
)

func installServe(t *testing.T, home string) *cliServe {
	t.Helper()
	root := filepath.Join(home, ".amq-squad", "buzz", "root")
	mustWrite(t, filepath.Join(root, "extensions", "remote", "manifest.json"), `{"schema_version":2,"layer":"remote",`+
		`"adapters":[{"kind":"mailbox","target":"amq-squad","config":{"handle":"attach","reply_timeout":"60s"}}],`+
		`"relay":{"url":"wss://relay.test","shares":[{"target":"amq-squad","session":"amq-squad","owner_pubkey":"`+strings.Repeat("a", 64)+
		`","dm_channel_id":"dm-chan","native_session_id":"attach","commands":true,"min_evidence":"submitted","presence":true,"name":"amq-squad"}]}}`)
	if err := os.MkdirAll(filepath.Join(root, "agents", "attach", "inbox", "new"), 0o700); err != nil {
		t.Fatal(err)
	}
	s := &cliServe{}
	fixtureServesMu.Lock()
	fixtureServes[home] = s
	fixtureServesMu.Unlock()
	prev := remoteInbox
	remoteInbox = func(string) buzz.Inbox { return s }
	t.Cleanup(func() {
		remoteInbox = prev
		fixtureServesMu.Lock()
		delete(fixtureServes, home)
		fixtureServesMu.Unlock()
	})
	return s
}

// serveOf is the fixture's fake serve.
func (f gateCLIFixture) serve() *cliServe {
	fixtureServesMu.Lock()
	defer fixtureServesMu.Unlock()
	return fixtureServes[f.home]
}

// writeShareManifest writes the share root's amq-remote manifest: the share
// bound to target, of kind (mailbox, or the retired fake), DM commands on
// for channel.
func writeShareManifest(t *testing.T, root, relay, channel, kind string) {
	t.Helper()
	adapter := `{"kind":"mailbox","target":"amq-squad","config":{"handle":"attach","reply_timeout":"60s"}}`
	target := "amq-squad"
	if kind == "fake" {
		adapter, target = `{"kind":"fake","target":"buzz-presence","epoch":"amq-squad-buzz-1"}`, "buzz-presence"
	}
	mustWrite(t, filepath.Join(root, "extensions", "remote", "manifest.json"), `{"schema_version":2,"layer":"remote","adapters":[`+adapter+`],`+
		`"relay":{"url":"`+relay+`","shares":[{"target":"`+target+`","session":"amq-squad","owner_pubkey":"`+strings.Repeat("a", 64)+
		`","dm_channel_id":"`+channel+`","native_session_id":"attach","commands":true,"min_evidence":"submitted","presence":true,"name":"amq-squad"}]}}`)
	if err := os.MkdirAll(filepath.Join(root, "agents", "attach", "inbox", "new"), 0o700); err != nil {
		t.Fatal(err)
	}
}
