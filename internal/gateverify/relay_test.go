package gateverify

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/omriariav/amq-squad/v3/internal/nostr"
	"github.com/omriariav/amq-squad/v3/internal/operatorauth"
)

// wsTestRelay is a local NIP-01 relay over a real websocket, in the ways the
// verifier depends on: NIP-42 challenge, only members may sign in, no REQ or
// EVENT before AUTH, REQ by filter up to EOSE, EVENT with OK. It models a
// relay; it is not evidence about Buzz.
type wsTestRelay struct {
	members []string
	mem     memRelay
	srv     *httptest.Server
}

func newWSTestRelay(t *testing.T, members ...string) *wsTestRelay {
	r := &wsTestRelay{members: members}
	r.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		ws, err := websocket.Accept(w, req, nil)
		if err != nil {
			return
		}
		defer ws.CloseNow()
		r.serve(ws)
	}))
	t.Cleanup(r.srv.Close)
	return r
}

func (r *wsTestRelay) url() string {
	return "ws://127.0.0.1" + strings.TrimPrefix(r.srv.URL, "http://127.0.0.1")
}

func (r *wsTestRelay) serve(ws *websocket.Conn) {
	ctx := context.Background()
	var wmu sync.Mutex
	send := func(v any) {
		raw, _ := json.Marshal(v)
		wmu.Lock()
		defer wmu.Unlock()
		_ = ws.Write(ctx, websocket.MessageText, raw)
	}
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
			ok := err == nil && e.CheckID() && e.VerifySignature() == nil && e.Kind == 22242 && slices.Contains(r.members, e.PubKey) &&
				slices.Contains(nostr.TagValues(e, "challenge"), challenge)
			authed = authed || ok
			send([]any{"OK", e.ID, ok, map[bool]string{true: "", false: "restricted: not a member"}[ok]})
		case "EVENT":
			e, err := nostr.ParseEvent(f[1])
			if !authed || err != nil || !e.CheckID() || e.VerifySignature() != nil {
				send([]any{"OK", e.ID, false, "auth-required: or invalid"})
				continue
			}
			r.mem.inject(f[1])
			send([]any{"OK", e.ID, true, ""})
		case "REQ":
			var sub string
			_ = json.Unmarshal(f[1], &sub)
			if !authed {
				send([]any{"CLOSED", sub, "auth-required: sign in first"})
				continue
			}
			var filters []Filter
			for _, raw := range f[2:] {
				filters = append(filters, wireFilter(raw))
			}
			for _, flt := range filters {
				got, _ := r.mem.Query(ctx, flt)
				for _, raw := range got {
					wmu.Lock()
					_ = ws.Write(ctx, websocket.MessageText, []byte(`["EVENT",`+mustJSONString(sub)+`,`+string(raw)+`]`))
					wmu.Unlock()
				}
			}
			send([]any{"EOSE", sub})
		}
	}
}

func mustJSONString(s string) string { raw, _ := json.Marshal(s); return string(raw) }

func wireFilter(raw json.RawMessage) Filter {
	var m map[string]json.RawMessage
	_ = json.Unmarshal(raw, &m)
	f := Filter{Tags: map[string][]string{}}
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

// Acceptance 5: a mailbox prompt labelled nostr:<event id> resolves, fetches
// and verifies against a test relay (real websocket, NIP-42 sign-in with the
// body key); a label for an event whose e tag names another gate's question
// is rejected; a label from a handle that is not the carrier is refused
// before any fetch.
func TestLabelPathAgainstATestRelay(t *testing.T) {
	f := newFixture(t)
	relay := newWSTestRelay(t, f.body.PublicHex())
	ws, err := Dial(ctx, relay.url())
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()
	authTag := []string{"auth", f.owner.PublicHex(), "kind=22242&created_at<9999999999", strings.Repeat("0", 128)}
	if err := ws.Auth(ctx, BodyAuth{Secret: f.body, AuthTag: authTag}); err != nil {
		t.Fatalf("body sign-in: %v", err)
	}
	f.core.Relay, f.core.RelayURL = ws, relay.url()
	f.clock = time.Now().UTC().Truncate(time.Second)
	f.core.Now = time.Now

	posted := time.Now()
	rec, q := f.raise("g-label", "github_release", "gh release create v3.0.0")
	other, _ := f.raise("g-other", "tag", "git tag v3.0.0")
	at := time.Now()
	good := f.answer(f.owner, tuple("approve", rec), q.EventID, at)
	wrongThread := f.answer(f.owner, tuple("approve", other), q.EventID, at) // names g-other, threads under g-label's question
	for _, raw := range [][]byte{good, wrongThread} {
		relay.mem.inject(raw)
	}
	id := func(raw []byte) string { e, _ := nostr.ParseEvent(raw); return e.ID }

	if r := f.core.HandleLabel(ctx, MailboxMessage{ID: "m0", From: "cto", Labels: []string{"nostr:" + id(good)}}); r.Code != RejectSender {
		t.Fatalf("label from a seat: %+v", r)
	}
	for _, labels := range [][]string{{"buzz"}, {"nostr:" + id(good), "nostr:" + id(wrongThread)}, {"nostr:XYZ"}} {
		if r := f.core.HandleLabel(ctx, MailboxMessage{ID: "m1", From: "buzz", Labels: labels}); r.Code != RejectLabel {
			t.Fatalf("labels %v: %+v", labels, r)
		}
	}
	if r := f.core.HandleLabel(ctx, MailboxMessage{ID: "m2", From: "buzz", Labels: []string{"acp", "nostr:" + strings.Repeat("5", 64)}}); r.Code != RejectFetch {
		t.Fatalf("unknown event: %+v", r)
	}
	if r := f.core.HandleLabel(ctx, MailboxMessage{ID: "m3", From: "buzz", Labels: []string{"acp", "buzz", "nostr:" + id(wrongThread)}}); r.Code != RejectWrongThread {
		t.Fatalf("another gate's thread: %+v", r)
	}
	r := f.core.HandleLabel(ctx, MailboxMessage{ID: "m4", From: "buzz", Labels: []string{"acp", "buzz", "nostr:" + id(good)}})
	if r.Outcome != operatorauth.DecisionApproved {
		t.Fatalf("label path: %+v", r)
	}
	t.Logf("test relay: raise to verified envelope %v (local websocket, synthetic keys)", time.Since(posted))
	ans, _ := f.store.ReadAnswer(rec.GateID)
	if err := VerifyAnswerFile(*ans, f.trust, f.core.Pin, rec, &q, nil, f.core.Namespace); err != nil {
		t.Fatal(err)
	}
	if ans.Evidence.Relay != relay.url() {
		t.Fatalf("evidence relay = %q", ans.Evidence.Relay)
	}
}

// A relay that is not signed in refuses REQ; FetchByID then fails instead of
// returning nothing.
func TestRelayRequiresSignIn(t *testing.T) {
	f := newFixture(t)
	relay := newWSTestRelay(t, f.body.PublicHex())
	ws, err := Dial(ctx, relay.url())
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()
	if _, err := ws.FetchByID(ctx, strings.Repeat("1", 64)); err == nil || !strings.Contains(err.Error(), "auth-required") {
		t.Fatalf("fetch before AUTH: %v", err)
	}
	stranger := mustKey(t)
	ws2, _ := Dial(ctx, relay.url())
	defer ws2.Close()
	if err := ws2.Auth(ctx, BodyAuth{Secret: stranger, AuthTag: []string{"auth", f.owner.PublicHex(), "c", "s"}}); err == nil {
		t.Fatal("a non-member signed in")
	}
	if _, err := Dial(ctx, "ws://relay.example"); err == nil {
		t.Fatal("plain ws:// off loopback accepted")
	}
}
