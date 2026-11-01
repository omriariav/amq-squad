package cli

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/buzz"
	"github.com/omriariav/amq-squad/v3/internal/gateverify"
	"github.com/omriariav/amq-squad/v3/internal/nostr"
	"github.com/omriariav/amq-squad/v3/internal/status"
)

// cliThreads is the cockpit threads for the CLI rows: the lead's replies,
// by thread.
type cliThreads struct {
	mu      sync.Mutex
	replies map[string][]buzz.ThreadEntry
}

func (c *cliThreads) Thread(_ context.Context, _, thread string) ([]buzz.ThreadEntry, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]buzz.ThreadEntry{}, c.replies[thread]...), nil
}

// Brief 31-4 through remote attach: the attachment relays the lead's reply
// on the request's cockpit thread to the owner's DM, signed by the share
// body over the core's transport (no key leaves the attachment); a state
// change the attachment reads from status makes no post of its own.
func TestRemoteAttachRelaysTheLeadsReply(t *testing.T) {
	f := newGateCLIFixture(t)
	ep, mailer := &remoteFakeEndpoint{}, &remoteFakeMailer{}
	threads := &cliThreads{replies: map[string][]buzz.ThreadEntry{}}
	prevE, prevM, prevS, prevT := remoteEndpoint, remoteMailer, remoteService, remoteThreads
	remoteEndpoint = func(string) buzz.Endpoint { return ep }
	remoteMailer = mailer
	remoteService = remoteFakeService{buzz.ServiceState{State: "current", Root: "/m", Running: true}}
	remoteThreads = threads
	t.Cleanup(func() { remoteEndpoint, remoteMailer, remoteService, remoteThreads = prevE, prevM, prevS, prevT })
	args := []string{"attach", "--once", "--json", "--project", f.project, "--session", "s", "--me", "cto", "--lead", "cto"}
	live := func(state string) {
		raw, _ := json.Marshal(map[string]any{"status": state, "last_seen": time.Now().UTC()})
		mustWrite(t, filepath.Join(f.root, "agents", "cto", "presence.json"), string(raw))
	}
	live("active")
	ev := f.answer(t, "is the build green?", "")
	stdout, _, err := captureOutput(t, func() error { return runRemote(args, "test") })
	rid := buzz.RequestID(ev)
	if err != nil || !strings.Contains(stdout, `"reply":"accepted `+rid+` routed cto"`) || len(mailer.sent) != 1 || mailer.sent[0].Thread != buzz.CockpitThread(rid) {
		t.Fatalf("the question: %v sent %+v\n%s", err, mailer.sent, stdout)
	}
	posts := func() []nostr.Event {
		raws, _ := f.relay.Query(context.Background(), gateverify.Filter{Kinds: []int{9}, Authors: []string{f.body.PublicHex()}})
		var out []nostr.Event
		for _, raw := range raws {
			if e, err := nostr.ParseEvent(raw); err == nil && strings.HasPrefix(e.Content, "re q-") {
				out = append(out, e)
			}
		}
		return out
	}
	if len(posts()) != 0 {
		t.Fatal("a post before the lead replied")
	}
	threads.mu.Lock()
	threads.replies[buzz.CockpitThread(rid)] = []buzz.ThreadEntry{{ID: "r-1", From: "cto", Thread: buzz.CockpitThread(rid), Body: "Green, merged #12.\n"}}
	threads.mu.Unlock()
	stdout, _, err = captureOutput(t, func() error { return runRemote(args, "test") })
	got := posts()
	if err != nil || len(got) != 1 || got[0].Content != "re "+rid+"\nGreen, merged #12." || !strings.Contains(stdout, `"kind":"relayed"`) {
		t.Fatalf("the relay through attach: %v posts %+v\n%s", err, got, stdout)
	}
	if refs := nostr.ThreadRefs(got[0]); len(refs) != 1 || refs[0] != ev {
		t.Fatalf("the post is not in the request's conversation: %v", refs)
	}
	// the seat's state changes in the status document: no post of its own
	live("idle")
	stdout, _, err = captureOutput(t, func() error { return runRemote(args, "test") })
	if err != nil || len(posts()) != 1 || strings.Contains(stdout, `"kind":"relayed"`) {
		t.Fatalf("a state change made a post: %v\n%s", err, stdout)
	}
	// the lead's status shows the request as relayed
	in := status.Collect(status.CollectOptions{Root: f.root, Session: "s", Now: time.Now()})
	if len(in.Requests) != 1 || in.Requests[0].RequestID != rid || in.Requests[0].RelayedAt == nil {
		t.Fatalf("status requests: %+v", in.Requests)
	}
}
