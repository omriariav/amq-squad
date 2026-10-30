package gateverify

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/omriariav/amq-squad/v3/internal/nostr"
)

// scriptedRelay answers every REQ with the frames answer returns for its
// subscription id, then EOSE: an in-process relay that can misbehave.
type scriptedRelay struct {
	srv    *httptest.Server
	answer func(sub string) [][]byte
}

func newScriptedRelay(t *testing.T, answer func(sub string) [][]byte) *scriptedRelay {
	r := &scriptedRelay{answer: answer}
	r.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		ws, err := websocket.Accept(w, req, nil)
		if err != nil {
			return
		}
		defer ws.CloseNow()
		ctx := context.Background()
		for {
			_, data, err := ws.Read(ctx)
			if err != nil {
				return
			}
			var f []json.RawMessage
			if json.Unmarshal(data, &f) != nil || len(f) < 2 {
				continue
			}
			var typ, sub string
			_ = json.Unmarshal(f[0], &typ)
			_ = json.Unmarshal(f[1], &sub)
			if typ != "REQ" {
				continue
			}
			for _, raw := range r.answer(sub) {
				if ws.Write(ctx, websocket.MessageText, []byte(`["EVENT",`+mustJSONString(sub)+`,`+string(raw)+`]`)) != nil {
					return
				}
			}
			_ = ws.Write(ctx, websocket.MessageText, []byte(`["EOSE",`+mustJSONString(sub)+`]`))
		}
	}))
	t.Cleanup(r.srv.Close)
	return r
}

func (r *scriptedRelay) url() string {
	return "ws://127.0.0.1" + strings.TrimPrefix(r.srv.URL, "http://127.0.0.1")
}

func signedEvents(t *testing.T, sk nostr.SecretKey, n, kind int, content string) [][]byte {
	t.Helper()
	out := make([][]byte, n)
	for i := range out {
		e := nostr.Event{CreatedAt: 1_700_000_000 + int64(i), Kind: kind, Tags: [][]string{{"h", "chan"}}, Content: content}
		if err := sk.Sign(&e); err != nil {
			t.Fatal(err)
		}
		out[i] = e.Marshal()
	}
	return out
}

// Phase 5 re-pass of 4-5, HIGH: the plaintext rule parses the URL: no
// userinfo, ws:// only for exactly localhost, 127.0.0.1 or ::1, wss://
// otherwise; a refused URL never dials.
func TestRelayURLRuleParsesTheHost(t *testing.T) {
	bad := []string{
		"ws://localhost.attacker.example/", "ws://127.0.0.1@relay.attacker.example/", "wss://user@relay.example/",
		"ws://relay.example/", "ws://127.0.0.1.nip.io/", "http://localhost/", "ws://LOCALHOST.example/", "wss:///x", "ws://[::1].evil/",
	}
	for _, u := range bad {
		if _, err := CheckRelayURL(u); err == nil {
			t.Fatalf("%s passed the relay rule", u) // stop: Dial must never be tried on it
		}
	}
	for _, u := range bad {
		if c, err := Dial(context.Background(), u); err == nil {
			c.Close()
			t.Errorf("%s dialed", u)
		}
	}
	for u, loop := range map[string]bool{"wss://relay.example/": false, "wss://relay.example:443/x": false, "ws://127.0.0.1:7777/": true, "ws://localhost/": true, "ws://[::1]:9/": true} {
		if got, err := CheckRelayURL(u); err != nil || got != loop {
			t.Errorf("%s: loopback %v, %v", u, got, err)
		}
	}
	// A ws:// connection must reach a loopback address: this host's own
	// non-loopback interface address stands in for a name that resolves
	// elsewhere (no packet leaves the host).
	var lan net.IP
	addrs, _ := net.InterfaceAddrs()
	for _, a := range addrs {
		if ip, ok := a.(*net.IPNet); ok && ip.IP.To4() != nil && !ip.IP.IsLoopback() {
			lan = ip.IP
			break
		}
	}
	if lan == nil {
		t.Log("no non-loopback interface address: the loopback-dial half is not exercised")
		return
	}
	ln, err := net.Listen("tcp", net.JoinHostPort(lan.String(), "0"))
	if err != nil {
		t.Logf("listen on %s: %v; the loopback-dial half is not exercised", lan, err)
		return
	}
	defer ln.Close()
	go func() {
		if c, err := ln.Accept(); err == nil {
			c.Close()
		}
	}()
	if c, err := loopbackOnly(context.Background(), "tcp", ln.Addr().String()); err == nil {
		c.Close()
		t.Fatalf("a plaintext dial to %s (not loopback) was kept", ln.Addr())
	}
}

// Phase 5 re-pass of 4-5, MEDIUM 1: a burst larger than the receive queue
// is never dropped (the reader waits); a dropped EVENT followed by EOSE
// would read as a complete answer and move the pump's cursor past it.
func TestQueryBurstIsNeverDropped(t *testing.T) {
	sk, _ := nostr.NewSecretKey()
	burst := signedEvents(t, sk, 700, KindDMForTest, "x")
	r := newScriptedRelay(t, func(string) [][]byte { return burst })
	c, err := Dial(context.Background(), r.url())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	var once sync.Once
	queryEventHook = func() { once.Do(func() { time.Sleep(300 * time.Millisecond) }) } // a slow consumer while the burst lands
	t.Cleanup(func() { queryEventHook = nil })
	got, err := c.Query(context.Background(), Filter{Kinds: []int{KindDMForTest}})
	if err != nil || len(got) != len(burst) {
		t.Fatalf("a %d-event burst: got %d, %v", len(burst), len(got), err)
	}
}

// Phase 5 re-pass of 4-5, MEDIUM 2: one REQ's answer is held to the filter,
// deduplicated by event id and capped; an overflow fails the whole query.
func TestQueryEnforcesTheFilterDedupsAndCaps(t *testing.T) {
	sk, _ := nostr.NewSecretKey()
	other, _ := nostr.NewSecretKey()
	good := signedEvents(t, sk, 3, KindDMForTest, "ok")
	stray := append(signedEvents(t, other, 2, KindDMForTest, "not the author"), signedEvents(t, sk, 2, 1, "another kind")...)
	r := newScriptedRelay(t, func(string) [][]byte {
		return append(append(append([][]byte{}, good...), stray...), good...) // duplicates too
	})
	c, err := Dial(context.Background(), r.url())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	got, err := c.Query(context.Background(), Filter{Kinds: []int{KindDMForTest}, Authors: []string{sk.PublicHex()}, Tags: map[string][]string{"h": {"chan"}}})
	if err != nil || len(got) != len(good) {
		t.Fatalf("filtered and deduplicated: got %d, %v; want %d", len(got), err, len(good))
	}
	many := signedEvents(t, sk, MaxQueryEvents+1, KindDMForTest, "m")
	r2 := newScriptedRelay(t, func(string) [][]byte { return many })
	c2, err := Dial(context.Background(), r2.url())
	if err != nil {
		t.Fatal(err)
	}
	defer c2.Close()
	if got, err := c2.Query(context.Background(), Filter{Kinds: []int{KindDMForTest}}); err == nil {
		t.Fatalf("%d events were accepted (%d returned)", len(many), len(got))
	}
	big := signedEvents(t, sk, 40, KindDMForTest, strings.Repeat("b", 220*1000)) // about 8.8 MB in events a parser accepts
	r3 := newScriptedRelay(t, func(string) [][]byte { return big })
	c3, err := Dial(context.Background(), r3.url())
	if err != nil {
		t.Fatal(err)
	}
	defer c3.Close()
	if got, err := c3.Query(context.Background(), Filter{Kinds: []int{KindDMForTest}}); err == nil {
		t.Fatalf("about 8.8 MB of events were accepted (%d returned)", len(got))
	}
}

// KindDMForTest is a regular kind for these rows.
const KindDMForTest = 1059
