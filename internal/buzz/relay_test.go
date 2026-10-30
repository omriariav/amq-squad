package buzz

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/omriariav/amq-squad/v3/internal/gateverify"
	"github.com/omriariav/amq-squad/v3/internal/nostr"
)

// memRelay stores events and answers filters, in process.
type memRelay struct {
	mu     sync.Mutex
	stored [][]byte
	// forge returns a fetched event with its signature broken; stall makes
	// a fetch wait for its context; onQuery runs at each fetch (tests).
	forge   bool
	stall   bool
	onQuery func()
	// onPublish runs after an event is stored (the websocket relay's live
	// subscriptions and its bell).
	onPublish func(raw []byte)
}

func (r *memRelay) Publish(_ context.Context, raw []byte) error {
	e, err := nostr.ParseEvent(raw)
	if err != nil || !e.CheckID() || e.VerifySignature() != nil {
		return fmt.Errorf("invalid event: %v", err)
	}
	r.mu.Lock()
	r.stored = append(r.stored, append([]byte(nil), raw...))
	hook := r.onPublish
	r.mu.Unlock()
	if hook != nil {
		hook(raw)
	}
	return nil
}

func (r *memRelay) FetchByID(ctx context.Context, id string) ([]byte, error) {
	r.mu.Lock()
	stall, forge, hook := r.stall, r.forge, r.onQuery
	r.mu.Unlock()
	if hook != nil {
		hook()
	}
	if stall {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	got, _ := r.Query(ctx, gateverify.Filter{IDs: []string{id}})
	if len(got) == 0 {
		return nil, fmt.Errorf("relay has no event %s", id)
	}
	if forge {
		e, _ := nostr.ParseEvent(got[0])
		e.Sig = strings.Repeat("0", 128)
		return e.Marshal(), nil
	}
	return got[0], nil
}

func (r *memRelay) Query(_ context.Context, f gateverify.Filter) ([][]byte, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out [][]byte
	for _, raw := range r.stored {
		e, err := nostr.ParseEvent(raw)
		if err != nil || !matches(e, f) {
			continue
		}
		out = append(out, raw)
	}
	if f.Limit > 0 && len(out) > f.Limit {
		out = out[len(out)-f.Limit:]
	}
	return out, nil
}

func (r *memRelay) events(kind int, author string) []nostr.Event {
	got, _ := r.Query(context.Background(), gateverify.Filter{Kinds: []int{kind}, Authors: []string{author}})
	var out []nostr.Event
	for _, raw := range got {
		e, _ := nostr.ParseEvent(raw)
		out = append(out, e)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].CreatedAt < out[j].CreatedAt })
	return out
}

func matches(e nostr.Event, f gateverify.Filter) bool {
	switch {
	case f.IDs != nil && !slices.Contains(f.IDs, e.ID),
		f.Kinds != nil && !slices.Contains(f.Kinds, e.Kind),
		f.Authors != nil && !slices.Contains(f.Authors, e.PubKey),
		f.Since > 0 && e.CreatedAt < f.Since:
		return false
	}
	for name, want := range f.Tags {
		if !slices.ContainsFunc(nostr.TagValues(e, name), func(v string) bool { return slices.Contains(want, v) }) {
			return false
		}
	}
	return true
}

// wsRelay is a local NIP-01 relay over a real websocket: NIP-11 document
// with a self key, NIP-42 challenge (any valid kind 22242 signs in, and the
// signer is recorded), no REQ or EVENT before AUTH, REQ up to EOSE and then
// live (a stored event matching an open subscription is pushed, as serve's
// DM subscription needs), CLOSE, EVENT with OK. It counts its connections.
// It rings its bell after each event stored and each connection opened,
// signed in or closed: a test awaits what the serve loop emits on it
// instead of polling. It models a relay; it is not evidence about Buzz.
type wsRelay struct {
	bell    bell
	mem     memRelay
	srv     *httptest.Server
	self    string
	selfKey nostr.SecretKey
	mu      sync.Mutex
	authed  []string
	conns   map[*wsConn]bool
	total   int
	// idsSeen records the open connection count at each REQ by event id
	// (the verifier's fetch); stallIDs leaves such a REQ unanswered.
	idsSeen  []int
	stallIDs bool
}

type wsConn struct {
	mu   sync.Mutex
	ws   *websocket.Conn
	subs map[string][]gateverify.Filter
}

func (c *wsConn) write(raw []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	_ = c.ws.Write(context.Background(), websocket.MessageText, raw)
}

func newWSRelay(t *testing.T) *wsRelay {
	t.Helper()
	selfKey, _ := nostr.NewSecretKey()
	r := &wsRelay{self: selfKey.PublicHex(), selfKey: selfKey, conns: map[*wsConn]bool{}}
	r.mem.onPublish = func(raw []byte) {
		r.broadcast(raw)
		r.bell.ring()
	}
	r.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if strings.Contains(req.Header.Get("Accept"), "application/nostr+json") {
			w.Header().Set("Content-Type", "application/nostr+json")
			_ = json.NewEncoder(w).Encode(map[string]any{"name": "4-5 test relay", "self": r.self, "supported_nips": []int{1, 11, 29, 42}})
			return
		}
		ws, err := websocket.Accept(w, req, nil)
		if err != nil {
			return
		}
		defer ws.CloseNow()
		ws.SetReadLimit(1 << 20)
		r.serve(ws)
	}))
	t.Cleanup(r.srv.Close)
	return r
}

func (r *wsRelay) url() string {
	return "ws://127.0.0.1" + strings.TrimPrefix(r.srv.URL, "http://127.0.0.1")
}

func (r *wsRelay) signedIn(pub string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Contains(r.authed, pub)
}

// open is the number of connections open now.
func (r *wsRelay) open() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.conns)
}

func (r *wsRelay) idsREQs() []int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.idsSeen)
}

// dmChannel stores the relay-signed NIP-29 snapshots of a private DM whose
// members are exactly owner and body (the DM surface check of serve,
// v0.116.2 internal/remote/buzzio/membership.go).
func (r *wsRelay) dmChannel(t *testing.T, channel, owner, body string) {
	t.Helper()
	for _, e := range []nostr.Event{
		{CreatedAt: time.Now().Unix(), Kind: 39000, Tags: [][]string{{"d", channel}, {"private"}, {"t", "dm"}}},
		{CreatedAt: time.Now().Unix(), Kind: 39002, Tags: [][]string{{"d", channel}, {"p", owner}, {"p", body}}},
	} {
		if err := r.selfKey.Sign(&e); err != nil {
			t.Fatal(err)
		}
		if err := r.mem.Publish(context.Background(), e.Marshal()); err != nil {
			t.Fatal(err)
		}
	}
}

// broadcast pushes a newly stored event to every open subscription it
// matches.
func (r *wsRelay) broadcast(raw []byte) {
	e, err := nostr.ParseEvent(raw)
	if err != nil {
		return
	}
	r.mu.Lock()
	conns := make([]*wsConn, 0, len(r.conns))
	for c := range r.conns {
		conns = append(conns, c)
	}
	r.mu.Unlock()
	for _, c := range conns {
		c.mu.Lock()
		var hits []string
		for sub, fs := range c.subs {
			for _, f := range fs {
				f.Limit, f.Since = 0, 0 // a live event is past since and outside a stored page
				if matches(e, f) {
					hits = append(hits, sub)
					break
				}
			}
		}
		c.mu.Unlock()
		for _, sub := range hits {
			sj, _ := json.Marshal(sub)
			c.write([]byte(`["EVENT",` + string(sj) + `,` + string(raw) + `]`))
		}
	}
}

func (r *wsRelay) serve(ws *websocket.Conn) {
	ctx := context.Background()
	c := &wsConn{ws: ws, subs: map[string][]gateverify.Filter{}}
	r.mu.Lock()
	r.conns[c] = true
	r.total++
	r.mu.Unlock()
	r.bell.ring()
	defer func() {
		r.mu.Lock()
		delete(r.conns, c)
		r.mu.Unlock()
		r.bell.ring()
	}()
	send := func(v any) { raw, _ := json.Marshal(v); c.write(raw) }
	challenge := fmt.Sprintf("chal-%d", time.Now().UnixNano())
	send([]any{"AUTH", challenge})
	authed := false
	for {
		_, data, err := ws.Read(ctx)
		if err != nil {
			return
		}
		var f []json.RawMessage
		if json.Unmarshal(data, &f) != nil || len(f) < 2 {
			continue
		}
		var typ string
		_ = json.Unmarshal(f[0], &typ)
		switch typ {
		case "AUTH":
			e, err := nostr.ParseEvent(f[1])
			ok := err == nil && e.CheckID() && e.VerifySignature() == nil && e.Kind == 22242 && slices.Contains(nostr.TagValues(e, "challenge"), challenge)
			if ok {
				authed = true
				r.mu.Lock()
				r.authed = append(r.authed, e.PubKey)
				r.mu.Unlock()
				r.bell.ring()
			}
			send([]any{"OK", e.ID, ok, map[bool]string{true: "", false: "restricted: bad auth"}[ok]})
		case "EVENT":
			e, err := nostr.ParseEvent(f[1])
			if !authed || err != nil {
				send([]any{"OK", e.ID, false, "auth-required: sign in first"})
				continue
			}
			if err := r.mem.Publish(ctx, f[1]); err != nil {
				send([]any{"OK", e.ID, false, "invalid: " + err.Error()})
				continue
			}
			send([]any{"OK", e.ID, true, ""})
		case "CLOSE":
			var sub string
			_ = json.Unmarshal(f[1], &sub)
			c.mu.Lock()
			delete(c.subs, sub)
			c.mu.Unlock()
		case "REQ":
			var sub string
			_ = json.Unmarshal(f[1], &sub)
			if !authed {
				send([]any{"CLOSED", sub, "auth-required: sign in first"})
				continue
			}
			var filters []gateverify.Filter
			byID := false
			for _, raw := range f[2:] {
				wf := wireFilter(raw)
				filters = append(filters, wf)
				byID = byID || len(wf.IDs) > 0
			}
			if byID {
				r.mu.Lock()
				r.idsSeen = append(r.idsSeen, len(r.conns))
				stall := r.stallIDs
				r.mu.Unlock()
				if stall {
					continue
				}
			}
			for _, wf := range filters {
				got, _ := r.mem.Query(ctx, wf)
				for _, ev := range got {
					sj, _ := json.Marshal(sub)
					c.write([]byte(`["EVENT",` + string(sj) + `,` + string(ev) + `]`))
				}
			}
			send([]any{"EOSE", sub})
			c.mu.Lock()
			c.subs[sub] = filters
			c.mu.Unlock()
		}
	}
}

func wireFilter(raw json.RawMessage) gateverify.Filter {
	var m map[string]json.RawMessage
	_ = json.Unmarshal(raw, &m)
	f := gateverify.Filter{Tags: map[string][]string{}}
	for k, v := range m {
		switch {
		case k == "ids":
			_ = json.Unmarshal(v, &f.IDs)
		case k == "kinds":
			_ = json.Unmarshal(v, &f.Kinds)
		case k == "authors":
			_ = json.Unmarshal(v, &f.Authors)
		case k == "since":
			_ = json.Unmarshal(v, &f.Since)
		case k == "limit":
			_ = json.Unmarshal(v, &f.Limit)
		case strings.HasPrefix(k, "#"):
			var vals []string
			_ = json.Unmarshal(v, &vals)
			f.Tags[k[1:]] = vals
		}
	}
	return f
}

func (r *wsRelay) totalConns() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.total
}
