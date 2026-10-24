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
}

func (r *memRelay) Publish(_ context.Context, raw []byte) error {
	e, err := nostr.ParseEvent(raw)
	if err != nil || !e.CheckID() || e.VerifySignature() != nil {
		return fmt.Errorf("invalid event: %v", err)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.stored = append(r.stored, append([]byte(nil), raw...))
	return nil
}

func (r *memRelay) FetchByID(ctx context.Context, id string) ([]byte, error) {
	got, _ := r.Query(ctx, gateverify.Filter{IDs: []string{id}})
	if len(got) == 0 {
		return nil, fmt.Errorf("relay has no event %s", id)
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
// signer is recorded), no REQ or EVENT before AUTH, REQ up to EOSE, EVENT
// with OK. It models a relay; it is not evidence about Buzz.
type wsRelay struct {
	mem    memRelay
	srv    *httptest.Server
	self   string
	mu     sync.Mutex
	authed []string
}

func newWSRelay(t *testing.T) *wsRelay {
	t.Helper()
	selfKey, _ := nostr.NewSecretKey()
	r := &wsRelay{self: selfKey.PublicHex()}
	r.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if strings.Contains(req.Header.Get("Accept"), "application/nostr+json") {
			w.Header().Set("Content-Type", "application/nostr+json")
			_ = json.NewEncoder(w).Encode(map[string]any{"name": "4-5 test relay", "self": r.self, "supported_nips": []int{1, 11, 42}})
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

func (r *wsRelay) serve(ws *websocket.Conn) {
	ctx := context.Background()
	var wmu sync.Mutex
	write := func(raw []byte) {
		wmu.Lock()
		defer wmu.Unlock()
		_ = ws.Write(ctx, websocket.MessageText, raw)
	}
	send := func(v any) { raw, _ := json.Marshal(v); write(raw) }
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
		case "REQ":
			var sub string
			_ = json.Unmarshal(f[1], &sub)
			if !authed {
				send([]any{"CLOSED", sub, "auth-required: sign in first"})
				continue
			}
			for _, raw := range f[2:] {
				got, _ := r.mem.Query(ctx, wireFilter(raw))
				for _, ev := range got {
					sj, _ := json.Marshal(sub)
					write([]byte(`["EVENT",` + string(sj) + `,` + string(ev) + `]`))
				}
			}
			send([]any{"EOSE", sub})
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
