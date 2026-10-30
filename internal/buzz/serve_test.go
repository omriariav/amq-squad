package buzz

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/omriariav/amq-squad/v3/internal/gateverify"
	"github.com/omriariav/amq-squad/v3/internal/nostr"
)

// fakeServe stands in for amq-remote serve's mailbox adapter: it delivers an
// owner DM into the handle's inbox in the shape the v0.116.2 adapter writes
// (internal/remote/mailbox/mailbox.go, prompt: from buzz, thread
// cockpit/<channel>, labels acp, buzz, nostr:<event id>, the DM text and the
// adapter's instructions), and takes the handle's newest reply that is not
// a status as the request's result (mailbox.go, finalReply). It is the
// handler's Inbox.
type fakeServe struct {
	mu      sync.Mutex
	n       int
	inbox   []Prompt // new
	cur     []Prompt
	replies []serveReply
	takeErr error
}

type serveReply struct {
	To     string
	Text   string
	Status bool
}

// deliver is serve's submit of one owner DM (eventID, text) in channel.
func (s *fakeServe) deliver(eventID, channel, text string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.n++
	id := fmt.Sprintf("2026-10-28T12-00-%02d.000Z_pid1_%08d", s.n%60, s.n)
	s.inbox = append(s.inbox, Prompt{ID: id, From: CarrierHandle, Thread: "cockpit/" + channel,
		Labels: []string{"acp", "buzz", "nostr:" + eventID},
		Body: text + ServePromptSuffix + " Answer with `amq reply --id " + id + "`; your reply is shown in that DM. " +
			"A reply with `--kind status` is not shown; any other reply is the final answer.\n"})
	return id
}

// put drops an arbitrary message into the inbox (a sender that is not serve).
func (s *fakeServe) put(p Prompt) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.inbox = append(s.inbox, p)
}

func (s *fakeServe) New(context.Context) ([]Prompt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Prompt, 0, len(s.inbox))
	for _, p := range s.inbox {
		q := p
		q.Body = ""
		out = append(out, q)
	}
	return out, nil
}

func (s *fakeServe) Take(_ context.Context, id string) (Prompt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.takeErr != nil {
		return Prompt{}, s.takeErr
	}
	for i, p := range s.inbox {
		if p.ID == id {
			s.inbox = slices.Delete(s.inbox, i, i+1)
			s.cur = append(s.cur, p)
			return p, nil
		}
	}
	return Prompt{}, fmt.Errorf("no message %s", id)
}

func (s *fakeServe) Reply(_ context.Context, id, text string, status bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.replies = append(s.replies, serveReply{To: id, Text: text, Status: status})
	return nil
}

// result is the request's result as serve reads it: the newest non-status
// reply with a body, and how many non-status replies there were.
func (s *fakeServe) result(id string) (string, int) {
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

func (s *fakeServe) statuses(id string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for _, r := range s.replies {
		if r.To == id && r.Status {
			out = append(out, r.Text)
		}
	}
	return out
}

func (s *fakeServe) allReplies() []serveReply {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.replies)
}

// countingDialer is the verifier's dialer over an in-process relay: every
// call is one connection, counted while open.
type countingDialer struct {
	mu          sync.Mutex
	relay       gateverify.Relay
	body        nostr.SecretKey
	dials, open int
	err         error
}

func (d *countingDialer) dial(context.Context) (gateverify.Relay, nostr.SecretKey, []string, func(), error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.err != nil {
		return nil, nostr.SecretKey{}, nil, nil, d.err
	}
	d.dials++
	d.open++
	return d.relay, d.body, nil, func() { d.mu.Lock(); d.open--; d.mu.Unlock() }, nil
}

func (d *countingDialer) counts() (int, int) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.dials, d.open
}

// handlerFixture is a handler over a core with a raised-gate-ready store, an
// in-memory relay, a fake serve and a fake mailer, bound to lead-a with
// lead-b routable.
type handlerFixture struct {
	coreFixture
	h     *Handler
	serve *fakeServe
	mail  *fakeMailer
	dial  *countingDialer
	mem   *memRelay
}

func newHandlerFixture(t *testing.T) handlerFixture {
	t.Helper()
	mem := &memRelay{}
	f := newCoreFixture(t, mem)
	hf := handlerFixture{coreFixture: f, serve: &fakeServe{}, mail: &fakeMailer{}, mem: mem}
	hf.dial = &countingDialer{relay: mem, body: f.body}
	hf.h = &Handler{Core: f.core, Dial: hf.dial.dial, Inbox: hf.serve, Mail: hf.mail,
		Target: Target{Root: f.root, Handle: "lead-a", Leads: []string{"lead-a", "lead-b"}, Registry: fakeLeads{"lead-a": true, "lead-b": true}}}
	return hf
}

// dm is the owner's DM on the relay, delivered by serve; it returns the
// event id and the AMQ message id.
func (hf handlerFixture) dm(t *testing.T, content, thread string) (string, string) {
	t.Helper()
	id := hf.ownerDM(t, content, thread)
	return id, hf.serve.deliver(id, hf.core.Pin.Channel, content)
}

func (hf handlerFixture) step(t *testing.T) []Handled {
	t.Helper()
	got, err := hf.h.Step(context.Background())
	if err != nil {
		t.Fatalf("step: %v", err)
	}
	return got
}
