package main

// A local relay that behaves like a Buzz relay for a private NIP-29 DM in
// the ways the fetch and watch paths depend on: it challenges with NIP-42,
// admits only group members whose AUTH carries the expected NIP-OA tag,
// refuses REQ before AUTH, and fans new events out to open subscriptions. It
// models the relay; it is not evidence about Buzz itself.

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
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
)

type relayFilter struct {
	IDs     []string `json:"ids"`
	Kinds   []int    `json:"kinds"`
	Authors []string `json:"authors"`
	H       []string `json:"#h"`
	Since   int64    `json:"since"`
}

func (f relayFilter) matches(e Event) bool {
	switch {
	case len(f.IDs) > 0 && !slices.Contains(f.IDs, e.ID):
		return false
	case len(f.Kinds) > 0 && !slices.Contains(f.Kinds, e.Kind):
		return false
	case len(f.Authors) > 0 && !slices.Contains(f.Authors, e.PubKey):
		return false
	case len(f.H) > 0 && !slices.ContainsFunc(tagValues(e, "h"), func(v string) bool { return slices.Contains(f.H, v) }):
		return false
	case f.Since > 0 && e.CreatedAt < f.Since:
		return false
	}
	return true
}

type liveSub struct {
	id     string
	filter relayFilter
	send   func([]byte)
}

type fakeRelay struct {
	members    []string // pubkeys admitted to the DM group
	authTag    []string // the NIP-OA tag AUTH must carry
	substitute bool     // answer every REQ with every stored event
	// closeReqs answers the first N REQs with CLOSED; dropConns closes the
	// first N connections right after AUTH. Both exercise watch recovery.
	closeReqs atomic.Int32
	dropConns atomic.Int32
	conns     atomic.Int32
	mu        sync.Mutex
	stored    [][]byte
	live      map[*liveSub]bool
	url       string
}

func (r *fakeRelay) inject(raw []byte) {
	e, _ := ParseEvent(raw)
	r.mu.Lock()
	defer r.mu.Unlock()
	r.stored = append(r.stored, append([]byte(nil), raw...))
	for s := range r.live {
		if s.filter.matches(e) {
			s.send([]byte(`["EVENT",` + mustJSON(s.id) + `,` + string(raw) + `]`))
		}
	}
}

func (r *fakeRelay) serve(t *testing.T) *httptest.Server {
	srv := httptest.NewServer(httpHandler(func(ws *websocket.Conn) {
		r.conns.Add(1)
		ctx := context.Background()
		var wmu sync.Mutex
		sendRaw := func(b []byte) { wmu.Lock(); defer wmu.Unlock(); _ = ws.Write(ctx, websocket.MessageText, b) }
		send := func(v any) { raw, _ := json.Marshal(v); sendRaw(raw) }
		challenge := fmt.Sprintf("chal-%d", time.Now().UnixNano())
		send([]any{"AUTH", challenge})
		authed := false
		var mine []*liveSub
		defer func() {
			r.mu.Lock()
			for _, s := range mine {
				delete(r.live, s)
			}
			r.mu.Unlock()
		}()
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
				if authed && r.dropConns.Load() > 0 {
					r.dropConns.Add(-1)
					return
				}
			case "EVENT":
				e, err := ParseEvent(f[1])
				if err != nil || !authed || !e.CheckID() || e.VerifySignature() != nil {
					send([]any{"OK", e.ID, false, "auth-required: or invalid"})
					continue
				}
				send([]any{"OK", e.ID, true, ""})
				r.inject(f[1])
			case "REQ":
				var sub string
				_ = json.Unmarshal(f[1], &sub)
				if !authed {
					send([]any{"CLOSED", sub, "auth-required: this group is private"})
					continue
				}
				if r.closeReqs.Load() > 0 {
					r.closeReqs.Add(-1)
					send([]any{"CLOSED", sub, "error: relay restarting"})
					continue
				}
				var filter relayFilter
				if len(f) > 2 {
					_ = json.Unmarshal(f[2], &filter)
				}
				s := &liveSub{id: sub, filter: filter, send: sendRaw}
				r.mu.Lock()
				for _, raw := range r.stored {
					e, _ := ParseEvent(raw)
					if r.substitute || filter.matches(e) {
						sendRaw([]byte(`["EVENT",` + mustJSON(sub) + `,` + string(raw) + `]`))
					}
				}
				if r.live == nil {
					r.live = map[*liveSub]bool{}
				}
				r.live[s] = true
				mine = append(mine, s)
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
	tag := writeShare(t, root, "gate", f.body, f.pin.Owner, time.Now().Add(24*time.Hour).Unix())
	relay := &fakeRelay{members: []string{f.owner.PublicHex(), f.body.PublicHex()}, authTag: tag}
	srv := relay.serve(t)
	defer srv.Close()
	ctx := context.Background()

	// The owner's answer lands in the group (the owner's own client posts it).
	raw := answer(t, f.owner, testNow.Add(time.Minute), approveText(f.gate))
	relay.inject(raw)
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
	if _, err := Verify(got, f.gate, nil, f.pin, testNow.Add(2*time.Minute)); err != nil {
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

// TestWatchFailureAndRecovery: the watch survives a CLOSED subscription and
// a dropped connection, skips events that are not this gate's owner answer,
// finds an answer that arrives live after it subscribed, and gives up
// cleanly at its deadline.
func TestWatchFailureAndRecovery(t *testing.T) {
	f := newFixture(t)
	root := t.TempDir()
	tag := writeShare(t, root, "gate", f.body, f.pin.Owner, time.Now().Add(24*time.Hour).Unix())
	relay := &fakeRelay{members: []string{f.owner.PublicHex(), f.body.PublicHex()}, authTag: tag}
	srv := relay.serve(t)
	defer srv.Close()
	ba, _, err := LoadBodyAuth(root, "gate", f.pin.Owner, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	dial := func(ctx context.Context) (*RelayClient, error) {
		c, err := Dial(ctx, relay.url)
		if err != nil {
			return nil, err
		}
		if err := c.Auth(ctx, ba); err != nil {
			c.Close()
			return nil, err
		}
		return c, nil
	}
	filter := map[string]any{"kinds": []int{9}, "authors": []string{f.pin.Owner}, "#h": []string{testChannel}, "since": testNow.Add(-clockSkew).Unix()}
	accept := answerFilter(f.gate, f.pin)

	// Noise already stored: another gate's answer, and the body posing as
	// the owner (the relay filter is not trusted, answerFilter rechecks).
	relay.inject(answer(t, f.owner, testNow.Add(time.Minute), approveText(f.other)))
	relay.inject(mutate(t, answer(t, f.body, testNow.Add(time.Minute), approveText(f.gate)), func(e *Event) { e.PubKey = f.owner.PublicHex() }))

	relay.closeReqs.Store(1) // first subscription: CLOSED
	relay.dropConns.Store(1) // second connection: dropped after AUTH
	var logs []string
	var lmu sync.Mutex
	logf := func(format string, v ...any) {
		lmu.Lock()
		logs = append(logs, fmt.Sprintf(format, v...))
		lmu.Unlock()
	}
	want := answer(t, f.owner, testNow.Add(2*time.Minute), approveText(f.gate))
	go func() {
		// Arrives live, after the watch has recovered and subscribed.
		for relay.conns.Load() < 3 {
			time.Sleep(20 * time.Millisecond)
		}
		time.Sleep(200 * time.Millisecond)
		relay.inject(want)
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	got, err := WatchWithRetry(ctx, dial, filter, accept, logf)
	if err != nil {
		t.Fatalf("watch: %v (logs %v)", err, logs)
	}
	if string(got) != string(want) {
		t.Fatalf("watch returned the wrong event: %s", got)
	}
	if len(logs) < 2 || !strings.Contains(strings.Join(logs, "\n"), "relay restarting") {
		t.Fatalf("expected two logged recoveries, got %v", logs)
	}

	// Deadline: no answer ever comes.
	ctx2, cancel2 := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel2()
	none := answerFilter(f.other, Pin{Owner: f.seat.PublicHex(), Channel: testChannel})
	if _, err := WatchWithRetry(ctx2, dial, filter, none, func(string, ...any) {}); err == nil || !strings.Contains(err.Error(), "gave up") {
		t.Fatalf("deadline: %v", err)
	}
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
