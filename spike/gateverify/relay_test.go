package main

// A local relay that behaves like a Buzz relay for a private NIP-29 DM in
// the ways the fetch path depends on: it challenges with NIP-42, admits only
// group members whose AUTH carries the expected NIP-OA tag, and refuses REQ
// before AUTH. It models the relay; it is not evidence about Buzz itself.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
)

type fakeRelay struct {
	members    []string // pubkeys admitted to the DM group
	authTag    []string // the NIP-OA tag AUTH must carry
	substitute bool     // answer every REQ with another event
	mu         sync.Mutex
	stored     [][]byte
	url        string
}

func (r *fakeRelay) serve(t *testing.T) *httptest.Server {
	srv := httptest.NewServer(httpHandler(func(ws *websocket.Conn) {
		ctx := context.Background()
		challenge := fmt.Sprintf("chal-%d", time.Now().UnixNano())
		send := func(v any) { raw, _ := json.Marshal(v); _ = ws.Write(ctx, websocket.MessageText, raw) }
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
				e, err := ParseEvent(f[1])
				reason := ""
				switch {
				case err != nil || !e.CheckID() || e.VerifySignature() != nil || e.Kind != 22242:
					reason = "invalid: auth event"
				case !slices.Contains(r.members, e.PubKey):
					reason = "restricted: not a member of this group"
				case !slices.Equal(tagValues(e, "relay"), []string{r.url}) || !slices.Equal(tagValues(e, "challenge"), []string{challenge}):
					reason = "invalid: relay or challenge tag"
				default:
					var auth [][]string
					for _, tg := range e.Tags {
						if len(tg) > 0 && tg[0] == "auth" {
							auth = append(auth, tg)
						}
					}
					if len(auth) != 1 || !slices.Equal(auth[0], r.authTag) {
						reason = "restricted: NIP-OA tag"
					}
				}
				authed = reason == ""
				send([]any{"OK", e.ID, authed, reason})
			case "EVENT":
				e, err := ParseEvent(f[1])
				if err != nil || !authed || !e.CheckID() || e.VerifySignature() != nil {
					send([]any{"OK", e.ID, false, "auth-required: or invalid"})
					continue
				}
				r.mu.Lock()
				r.stored = append(r.stored, append([]byte(nil), f[1]...))
				r.mu.Unlock()
				send([]any{"OK", e.ID, true, ""})
			case "REQ":
				var sub string
				_ = json.Unmarshal(f[1], &sub)
				if !authed {
					send([]any{"CLOSED", sub, "auth-required: this group is private"})
					continue
				}
				var filter struct {
					IDs []string `json:"ids"`
				}
				if len(f) > 2 {
					_ = json.Unmarshal(f[2], &filter)
				}
				r.mu.Lock()
				for _, raw := range r.stored {
					e, _ := ParseEvent(raw)
					if r.substitute || slices.Contains(filter.IDs, e.ID) {
						ws.Write(ctx, websocket.MessageText, []byte(`["EVENT",`+mustJSON(sub)+`,`+string(raw)+`]`))
					}
				}
				r.mu.Unlock()
				send([]any{"EOSE", sub})
			}
		}
	}))
	r.url = "ws://" + strings.TrimPrefix(srv.URL, "http://")
	return srv
}

func mustJSON(v any) string { raw, _ := json.Marshal(v); return string(raw) }

func httpHandler(fn func(*websocket.Conn)) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		ws, err := websocket.Accept(w, req, nil)
		if err != nil {
			return
		}
		defer func() { _ = ws.CloseNow() }()
		fn(ws)
	}
}

// writeShare lays out an enrolled amq-remote share the way sharestate reads
// it: keys/<session>/body.key (0600) and share.json.
func writeShare(t *testing.T, root, session string, body SecretKey, owner string, notAfter int64) []string {
	t.Helper()
	dir := filepath.Join(root, "extensions", "remote", "keys", session)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "body.key"), []byte("secret "+body.Hex()+"\npublic "+body.PublicHex()+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var tags []map[string]any
	for _, kind := range []int{20003, 1059, 24200, 44200, 10100, 9, 40003} {
		tags = append(tags, map[string]any{"kind": kind, "owner_pubkey": owner, "conditions": fmt.Sprintf("kind=%d&created_at<%d", kind, notAfter), "sig": strings.Repeat("ab", 64)})
	}
	raw, _ := json.Marshal(map[string]any{"tags": tags})
	if err := os.WriteFile(filepath.Join(dir, "share.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	return []string{"auth", owner, fmt.Sprintf("kind=1059&created_at<%d", notAfter), strings.Repeat("ab", 64)}
}

func TestFetchThroughBodyKeySignIn(t *testing.T) {
	f := newFixture(t)
	root := t.TempDir()
	notAfter := time.Now().Add(24 * time.Hour).Unix()
	tag := writeShare(t, root, "gate", f.body, f.pin.Owner, notAfter)
	relay := &fakeRelay{members: []string{f.owner.PublicHex(), f.body.PublicHex()}, authTag: tag}
	srv := relay.serve(t)
	defer srv.Close()
	ctx := context.Background()

	// The owner's answer lands in the group (stored directly: the owner's
	// own client posts it).
	raw := answer(t, f.owner, testNow.Add(time.Minute), approveText(f.gate))
	relay.stored = append(relay.stored, raw)
	e, _ := ParseEvent(raw)

	ba, body, err := LoadBodyAuth(root, "gate", f.pin.Owner, time.Now())
	if err != nil || body != f.body.PublicHex() {
		t.Fatalf("load share: %v", err)
	}
	c, err := Dial(ctx, relay.url)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Auth(ctx, ba); err != nil {
		t.Fatalf("auth as body: %v", err)
	}
	got, err := c.FetchByID(ctx, e.ID)
	c.Close()
	if err != nil || string(got) != string(raw) {
		t.Fatalf("fetch: %v", err)
	}
	if _, err := Verify(got, f.gate, f.pin, testNow.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}

	// No sign-in: the private group refuses the REQ.
	c2, _ := Dial(ctx, relay.url)
	if _, err := c2.FetchByID(ctx, e.ID); err == nil || !strings.Contains(err.Error(), "auth-required") {
		t.Fatalf("unauthenticated fetch: %v", err)
	}
	c2.Close()

	// A key that is not a group member cannot sign in.
	c3, _ := Dial(ctx, relay.url)
	if err := c3.Auth(ctx, BodyAuth{Secret: f.seat, AuthTag: tag}); err == nil {
		t.Fatal("non-member key signed in")
	}
	c3.Close()

	// A relay that answers with another event is ignored.
	relay.substitute = true
	c4, _ := Dial(ctx, relay.url)
	_ = c4.Auth(ctx, ba)
	if _, err := c4.FetchByID(ctx, strings.Repeat("1", 64)); err == nil {
		t.Fatal("substituted event accepted")
	}
	c4.Close()
}

func TestLoadBodyAuthRefusals(t *testing.T) {
	f := newFixture(t)
	root := t.TempDir()
	writeShare(t, root, "old", f.body, f.pin.Owner, time.Now().Add(-time.Hour).Unix())
	if _, _, err := LoadBodyAuth(root, "old", f.pin.Owner, time.Now()); err == nil {
		t.Fatal("expired grant accepted")
	}
	writeShare(t, root, "other", f.body, f.seat.PublicHex(), time.Now().Add(time.Hour).Unix())
	if _, _, err := LoadBodyAuth(root, "other", f.pin.Owner, time.Now()); err == nil {
		t.Fatal("share of another owner accepted")
	}
	writeShare(t, root, "loose", f.body, f.pin.Owner, time.Now().Add(time.Hour).Unix())
	_ = os.Chmod(filepath.Join(root, "extensions", "remote", "keys", "loose", "body.key"), 0o644)
	if _, _, err := LoadBodyAuth(root, "loose", f.pin.Owner, time.Now()); err == nil {
		t.Fatal("world-readable body key accepted")
	}
	if _, _, err := LoadBodyAuth(root, "../x", f.pin.Owner, time.Now()); err == nil {
		t.Fatal("path session accepted")
	}
}
