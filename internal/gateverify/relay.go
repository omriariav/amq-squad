package gateverify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"slices"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/omriariav/amq-squad/v3/internal/nostr"
)

// Filter is a NIP-01 REQ filter.
type Filter struct {
	IDs     []string
	Kinds   []int
	Authors []string
	Tags    map[string][]string // "e" -> ids becomes "#e"
	Since   int64
	Limit   int
}

// Matches reports whether an event satisfies the filter, as a relay must
// apply it: every id, kind and author list the filter names holds the
// event's value, every tag filter is met by one of the event's tags, and the
// event is not older than since.
func (f Filter) Matches(e nostr.Event) bool {
	if f.IDs != nil && !slices.Contains(f.IDs, e.ID) {
		return false
	}
	if f.Kinds != nil && !slices.Contains(f.Kinds, e.Kind) {
		return false
	}
	if f.Authors != nil && !slices.Contains(f.Authors, e.PubKey) {
		return false
	}
	if f.Since > 0 && e.CreatedAt < f.Since {
		return false
	}
	for name, values := range f.Tags {
		hit := false
		for _, t := range e.Tags {
			if len(t) >= 2 && t[0] == name && slices.Contains(values, t[1]) {
				hit = true
				break
			}
		}
		if !hit {
			return false
		}
	}
	return true
}

func (f Filter) wire() map[string]any {
	m := map[string]any{}
	if f.IDs != nil {
		m["ids"] = f.IDs
	}
	if f.Kinds != nil {
		m["kinds"] = f.Kinds
	}
	if f.Authors != nil {
		m["authors"] = f.Authors
	}
	for k, v := range f.Tags {
		m["#"+k] = v
	}
	if f.Since > 0 {
		m["since"] = f.Since
	}
	if f.Limit > 0 {
		m["limit"] = f.Limit
	}
	return m
}

// Relay is the transport the verifier reads and the core posts through. A
// relay can withhold an event but cannot forge one: every event it returns is
// re-parsed and its id recomputed here, and Verify checks signature and pin.
type Relay interface {
	// FetchByID returns the stored event with this id, as the relay sent it.
	FetchByID(ctx context.Context, id string) ([]byte, error)
	// Query returns every stored event matching the filter, up to EOSE.
	Query(ctx context.Context, f Filter) ([][]byte, error)
	// Publish sends one signed event and waits for the relay's OK.
	Publish(ctx context.Context, raw []byte) error
}

const maxFrame = 1 << 20

// Query response bounds (phase 5 re-pass of 4-5): one REQ accepts at most
// MaxQueryEvents distinct events and MaxQueryBytes of event bytes; more is
// an overflow and the whole query fails (the caller's cursor does not move).
const (
	MaxQueryEvents = 2000
	MaxQueryBytes  = 8 << 20
)

// queryEventHook runs as Query takes each EVENT (a test seam: a slow
// consumer).
var queryEventHook func()

// WSRelay is one websocket connection to a NIP-01 relay, with an optional
// NIP-42 sign-in (the share body key and its owner-signed NIP-OA tag, as
// upstream amq-remote signs in). Ported from spike S1 relay.go.
type WSRelay struct {
	url       string
	ws        *websocket.Conn
	frames    chan []json.RawMessage
	challenge chan string
	done      chan struct{}
	closed    chan struct{}
	closeOnce sync.Once
	mu        sync.Mutex
	readErr   error
}

// CheckRelayURL is the transport rule of a relay URL (phase 5 re-pass of
// 4-5): the URL is parsed, never prefix-matched; userinfo is refused; wss://
// is required, and ws:// (no TLS) is accepted only when the hostname is
// exactly localhost, 127.0.0.1 or ::1. Dial also requires a ws:// connection
// to reach a loopback address, so a name that resolves elsewhere is refused.
// It returns whether the URL is plaintext loopback.
func CheckRelayURL(raw string) (loopback bool, err error) {
	u, err := url.Parse(raw)
	if err != nil {
		return false, fmt.Errorf("relay url does not parse: %v", err)
	}
	if u.User != nil || u.Opaque != "" || u.Hostname() == "" {
		return false, errors.New("relay url must be wss://HOST[/PATH], with no userinfo")
	}
	switch u.Scheme {
	case "wss":
		return false, nil
	case "ws":
		if slices.Contains([]string{"localhost", "127.0.0.1", "::1"}, u.Hostname()) {
			return true, nil
		}
	}
	return false, errors.New("relay url must be wss:// (ws:// only on loopback: localhost, 127.0.0.1 or ::1)")
}

// loopbackOnly dials and keeps a connection only when its remote address is
// a loopback address: a ws:// relay never carries AUTH or a DM off the host.
func loopbackOnly(ctx context.Context, network, addr string) (net.Conn, error) {
	conn, err := (&net.Dialer{}).DialContext(ctx, network, addr)
	if err != nil {
		return nil, err
	}
	if ta, ok := conn.RemoteAddr().(*net.TCPAddr); !ok || !ta.IP.IsLoopback() {
		conn.Close()
		return nil, fmt.Errorf("a ws:// relay must resolve to loopback; %s reached %s", addr, conn.RemoteAddr())
	}
	return conn, nil
}

// BodyAuth is the sign-in material of an enrolled amq-remote share.
type BodyAuth struct {
	Secret  nostr.SecretKey
	AuthTag []string // ["auth", owner, conditions, sig]
}

// Dial opens the connection. No redirect is followed, so AUTH never goes to
// another origin. ws:// is accepted only on loopback (tests).
func Dial(ctx context.Context, url string) (*WSRelay, error) {
	loopback, err := CheckRelayURL(url)
	if err != nil {
		return nil, err
	}
	noRedirect := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return errors.New("relay redirected; refusing")
	}}
	if loopback {
		noRedirect.Transport = &http.Transport{DialContext: loopbackOnly, Proxy: nil}
	}
	dctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	ws, _, err := websocket.Dial(dctx, url, &websocket.DialOptions{HTTPClient: noRedirect})
	if err != nil {
		return nil, fmt.Errorf("dial %s: %w", url, err)
	}
	ws.SetReadLimit(maxFrame)
	c := &WSRelay{url: url, ws: ws, frames: make(chan []json.RawMessage, 256), challenge: make(chan string, 1), done: make(chan struct{}), closed: make(chan struct{})}
	go c.readPump()
	return c, nil
}

func (c *WSRelay) readPump() {
	defer close(c.done)
	for {
		_, data, err := c.ws.Read(context.Background())
		if err != nil {
			c.mu.Lock()
			c.readErr = err
			c.mu.Unlock()
			return
		}
		var frame []json.RawMessage
		if json.Unmarshal(data, &frame) != nil || len(frame) == 0 {
			continue
		}
		var typ string
		if json.Unmarshal(frame[0], &typ) != nil {
			continue
		}
		if typ == "AUTH" && len(frame) >= 2 {
			var ch string
			if json.Unmarshal(frame[1], &ch) == nil {
				select {
				case c.challenge <- ch:
				default:
				}
			}
			continue
		}
		// Backpressure, never a drop (phase 5 re-pass of 4-5): a full queue
		// stops this reader, and the relay's writes wait on the socket; a
		// dropped EVENT followed by EOSE would read as a complete answer.
		select {
		case c.frames <- frame:
		case <-c.closed:
			return
		}
	}
}

// Close ends the connection.
func (c *WSRelay) Close() {
	c.closeOnce.Do(func() { close(c.closed) })
	_ = c.ws.Close(websocket.StatusNormalClosure, "")
}

func (c *WSRelay) send(ctx context.Context, v any) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return c.ws.Write(ctx, websocket.MessageText, raw)
}

func (c *WSRelay) next(ctx context.Context) ([]json.RawMessage, string, error) {
	select {
	case f := <-c.frames:
		var typ string
		_ = json.Unmarshal(f[0], &typ)
		return f, typ, nil
	case <-c.done:
		c.mu.Lock()
		defer c.mu.Unlock()
		return nil, "", fmt.Errorf("relay closed: %v", c.readErr)
	case <-ctx.Done():
		return nil, "", ctx.Err()
	}
}

// Auth answers the relay's NIP-42 challenge with a kind 22242 event carrying
// relay, challenge and exactly one NIP-OA tag, and requires a positive OK.
func (c *WSRelay) Auth(ctx context.Context, a BodyAuth) error {
	actx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	var ch string
	select {
	case ch = <-c.challenge:
	case <-c.done:
		return errors.New("relay closed before its AUTH challenge")
	case <-actx.Done():
		return errors.New("relay sent no AUTH challenge in time")
	}
	evt := nostr.Event{CreatedAt: time.Now().Unix(), Kind: 22242, Tags: [][]string{{"relay", c.url}, {"challenge", ch}, a.AuthTag}}
	if err := a.Secret.Sign(&evt); err != nil {
		return err
	}
	if err := c.ws.Write(actx, websocket.MessageText, append(append([]byte(`["AUTH",`), evt.Marshal()...), ']')); err != nil {
		return err
	}
	return c.awaitOK(actx, evt.ID)
}

func (c *WSRelay) awaitOK(ctx context.Context, id string) error {
	for {
		f, typ, err := c.next(ctx)
		if err != nil {
			return err
		}
		if typ != "OK" || len(f) < 3 {
			continue
		}
		var gotID, msg string
		var ok bool
		_ = json.Unmarshal(f[1], &gotID)
		_ = json.Unmarshal(f[2], &ok)
		if len(f) > 3 {
			_ = json.Unmarshal(f[3], &msg)
		}
		if gotID != id {
			continue
		}
		if !ok {
			return fmt.Errorf("relay refused %s: %s", id, msg)
		}
		return nil
	}
}

// Publish sends one signed event (raw bytes, as signed) and waits for OK.
func (c *WSRelay) Publish(ctx context.Context, raw []byte) error {
	e, err := nostr.ParseEvent(raw)
	if err != nil {
		return err
	}
	pctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if err := c.ws.Write(pctx, websocket.MessageText, append(append([]byte(`["EVENT",`), raw...), ']')); err != nil {
		return err
	}
	return c.awaitOK(pctx, e.ID)
}

// FetchByID sends REQ {"ids":[id]} and returns the event bytes the relay sent.
// An event whose recomputed id is not id is ignored: a relay cannot substitute
// another event.
func (c *WSRelay) FetchByID(ctx context.Context, id string) ([]byte, error) {
	if !nostr.IsHex64(id) {
		return nil, errors.New("event id must be 64 lowercase hex")
	}
	got, err := c.Query(ctx, Filter{IDs: []string{id}, Limit: 1})
	if err != nil {
		return nil, err
	}
	for _, raw := range got {
		if e, err := nostr.ParseEvent(raw); err == nil && e.ID == id {
			return raw, nil
		}
	}
	return nil, fmt.Errorf("relay has no event %s", id)
}

// Query reads every stored event matching f up to EOSE, within 15 s. An
// event that does not parse, whose id does not recompute, or that does not
// match the filter is dropped, and an event id already received is not
// returned twice; a CLOSED, a timeout or an overflow (more than
// MaxQueryEvents events, MaxQueryBytes bytes, or the filter's limit) is an
// error (the caller then does not know the set, and its cursor stays).
func (c *WSRelay) Query(ctx context.Context, f Filter) ([][]byte, error) {
	qctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	sub := fmt.Sprintf("gv-%d", time.Now().UnixNano())
	if err := c.send(qctx, []any{"REQ", sub, f.wire()}); err != nil {
		return nil, err
	}
	defer func() {
		// a stalled socket never holds the caller past the query: the CLOSE
		// gets CloseTimeout of its own (5-amq-B fix round)
		cctx, ccancel := context.WithTimeout(context.Background(), CloseTimeout)
		defer ccancel()
		_ = c.send(cctx, []any{"CLOSE", sub})
	}()
	var out [][]byte
	seen, received, size := map[string]bool{}, 0, 0
	for {
		fr, typ, err := c.next(qctx)
		if err != nil {
			return nil, err
		}
		var gotSub string
		if len(fr) > 1 {
			_ = json.Unmarshal(fr[1], &gotSub)
		}
		if gotSub != sub {
			continue
		}
		switch typ {
		case "EVENT":
			if len(fr) < 3 {
				continue
			}
			if queryEventHook != nil {
				queryEventHook()
			}
			// The caps count every EVENT the relay sends for this REQ, kept
			// or not: a relay cannot stream an unbounded answer of strays.
			received, size = received+1, size+len(fr[2])
			if received > MaxQueryEvents || size > MaxQueryBytes {
				return nil, fmt.Errorf("relay answered with more than %d events or %d bytes; the query fails as a whole", MaxQueryEvents, MaxQueryBytes)
			}
			e, err := nostr.ParseEvent(fr[2])
			if err != nil || !e.CheckID() || !f.Matches(e) || seen[e.ID] {
				continue
			}
			seen[e.ID] = true
			if f.Limit > 0 && len(out) >= f.Limit {
				return nil, fmt.Errorf("relay answered with more than the filter's limit of %d events; the query fails as a whole", f.Limit)
			}
			out = append(out, append([]byte(nil), fr[2]...))
		case "EOSE":
			return out, nil
		case "CLOSED":
			var msg string
			if len(fr) > 2 {
				_ = json.Unmarshal(fr[2], &msg)
			}
			return nil, fmt.Errorf("relay closed the request: %s", msg)
		}
	}
}

// CloseTimeout bounds the CLOSE a query sends when it ends.
const CloseTimeout = 2 * time.Second
