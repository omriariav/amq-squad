package main

// A minimal NIP-01 / NIP-42 relay client: dial, optional AUTH with the
// share's enrolled body key and its owner-signed NIP-OA tag (the same
// sign-in upstream internal/relay/relay.go:154-240 performs), REQ by id,
// EVENT publish, and a live watch. Events are returned as the exact bytes
// the relay sent, never re-encoded.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
)

const maxFrame = 1 << 20

// RelayClient is one websocket connection.
type RelayClient struct {
	url       string
	ws        *websocket.Conn
	frames    chan []json.RawMessage
	challenge chan string
	done      chan struct{}
	mu        sync.Mutex
	readErr   error
}

// Dial opens the connection. No redirect is followed, so AUTH can never be
// taken to another origin (upstream relay.go does the same).
func Dial(ctx context.Context, url string) (*RelayClient, error) {
	if !strings.HasPrefix(url, "wss://") && !strings.HasPrefix(url, "ws://127.0.0.1") && !strings.HasPrefix(url, "ws://localhost") {
		return nil, errors.New("relay url must be wss:// (ws:// only on loopback)")
	}
	noRedirect := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return errors.New("relay redirected; refusing")
	}}
	dctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	ws, _, err := websocket.Dial(dctx, url, &websocket.DialOptions{HTTPClient: noRedirect})
	if err != nil {
		return nil, fmt.Errorf("dial %s: %w", url, err)
	}
	ws.SetReadLimit(maxFrame)
	c := &RelayClient{url: url, ws: ws, frames: make(chan []json.RawMessage, 256), challenge: make(chan string, 1), done: make(chan struct{})}
	go c.readPump()
	return c, nil
}

func (c *RelayClient) readPump() {
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
		select {
		case c.frames <- frame:
		default: // a slow consumer drops frames rather than block the pump
		}
	}
}

// Close ends the connection.
func (c *RelayClient) Close() { _ = c.ws.Close(websocket.StatusNormalClosure, "") }

func (c *RelayClient) send(ctx context.Context, v any) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return c.ws.Write(ctx, websocket.MessageText, raw)
}

func (c *RelayClient) sendRaw(ctx context.Context, raw []byte) error {
	return c.ws.Write(ctx, websocket.MessageText, raw)
}

func (c *RelayClient) next(ctx context.Context) ([]json.RawMessage, string, error) {
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

// BodyAuth is the sign-in material of an enrolled amq-remote share: the
// body secret and the owner's NIP-OA grant for the AUTH kind.
type BodyAuth struct {
	Secret  SecretKey
	AuthTag []string // ["auth", owner, conditions, sig]
}

// Auth waits for the relay's challenge and answers it with a kind 22242
// event carrying relay, challenge and exactly one NIP-OA tag, then requires
// a positive OK.
func (c *RelayClient) Auth(ctx context.Context, a BodyAuth) error {
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
	evt := Event{CreatedAt: time.Now().Unix(), Kind: 22242, Tags: [][]string{{"relay", c.url}, {"challenge", ch}, a.AuthTag}}
	if err := a.Secret.Sign(&evt); err != nil {
		return err
	}
	frame := append(append([]byte(`["AUTH",`), evt.Marshal()...), ']')
	if err := c.sendRaw(actx, frame); err != nil {
		return err
	}
	return c.awaitOK(actx, evt.ID)
}

func (c *RelayClient) awaitOK(ctx context.Context, id string) error {
	for {
		f, typ, err := c.next(ctx)
		if err != nil {
			return err
		}
		if typ != "OK" || len(f) < 3 {
			continue
		}
		var gotID string
		var ok bool
		var msg string
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
func (c *RelayClient) Publish(ctx context.Context, raw []byte) error {
	e, err := ParseEvent(raw)
	if err != nil {
		return err
	}
	pctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	frame := append(append([]byte(`["EVENT",`), raw...), ']')
	if err := c.sendRaw(pctx, frame); err != nil {
		return err
	}
	return c.awaitOK(pctx, e.ID)
}

// FetchByID sends REQ {"ids":[id]} and returns the event bytes the relay
// sent. A returned event whose recomputed id is not id is ignored (a relay
// cannot substitute another event); signature and pin checks are Verify's.
func (c *RelayClient) FetchByID(ctx context.Context, id string) ([]byte, error) {
	if !hex64.MatchString(id) {
		return nil, errors.New("event id must be 64 lowercase hex")
	}
	fctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	sub := fmt.Sprintf("gv-%d", time.Now().UnixNano())
	if err := c.send(fctx, []any{"REQ", sub, map[string]any{"ids": []string{id}, "limit": 1}}); err != nil {
		return nil, err
	}
	defer func() { _ = c.send(context.Background(), []any{"CLOSE", sub}) }()
	var found []byte
	for {
		f, typ, err := c.next(fctx)
		if err != nil {
			return nil, err
		}
		var gotSub string
		if len(f) > 1 {
			_ = json.Unmarshal(f[1], &gotSub)
		}
		if gotSub != sub {
			continue
		}
		switch typ {
		case "EVENT":
			if len(f) < 3 || found != nil {
				continue
			}
			e, err := ParseEvent(f[2])
			if err == nil && e.ID == id && e.CheckID() {
				found = append([]byte(nil), f[2]...)
			}
		case "EOSE":
			if found == nil {
				return nil, fmt.Errorf("relay has no event %s", id)
			}
			return found, nil
		case "CLOSED":
			var msg string
			if len(f) > 2 {
				_ = json.Unmarshal(f[2], &msg)
			}
			return nil, fmt.Errorf("relay closed the request: %s", msg)
		}
	}
}

// WatchWithRetry keeps a watch alive until ctx ends: a dial failure, a
// relay CLOSED, or a dropped connection is logged, and the watch reconnects
// with backoff (1 s doubling to 10 s) and resubscribes. The filter's since
// bound makes a resubscribe re-read stored events, so an answer that arrived
// while the watch was down is still found.
func WatchWithRetry(ctx context.Context, dial func(context.Context) (*RelayClient, error), filter map[string]any, accept func(Event) bool, logf func(string, ...any)) ([]byte, error) {
	backoff := time.Second
	for attempt := 1; ; attempt++ {
		c, err := dial(ctx)
		if err == nil {
			var raw []byte
			raw, err = c.Watch(ctx, filter, accept)
			c.Close()
			if err == nil {
				return raw, nil
			}
		}
		if ctx.Err() != nil {
			return nil, fmt.Errorf("gave up after %d attempt(s): %w", attempt, err)
		}
		logf("attempt %d failed: %v; reconnecting in %s", attempt, err, backoff)
		select {
		case <-time.After(backoff):
		case <-ctx.Done():
			return nil, fmt.Errorf("gave up after %d attempt(s): %w", attempt, err)
		}
		if backoff *= 2; backoff > 10*time.Second {
			backoff = 10 * time.Second
		}
	}
}

// Watch subscribes with filter and returns the first stored or live event
// that accept admits. It is the fallback event-id resolution when no
// journal names the answer: the owner's kind 9 in the DM channel.
func (c *RelayClient) Watch(ctx context.Context, filter map[string]any, accept func(Event) bool) ([]byte, error) {
	sub := fmt.Sprintf("gw-%d", time.Now().UnixNano())
	if err := c.send(ctx, []any{"REQ", sub, filter}); err != nil {
		return nil, err
	}
	defer func() { _ = c.send(context.Background(), []any{"CLOSE", sub}) }()
	for {
		f, typ, err := c.next(ctx)
		if err != nil {
			return nil, err
		}
		var gotSub string
		if len(f) > 1 {
			_ = json.Unmarshal(f[1], &gotSub)
		}
		if gotSub != sub {
			continue
		}
		switch typ {
		case "EVENT":
			if len(f) < 3 {
				continue
			}
			e, err := ParseEvent(f[2])
			if err == nil && e.CheckID() && accept(e) {
				return append([]byte(nil), f[2]...), nil
			}
		case "CLOSED":
			var msg string
			if len(f) > 2 {
				_ = json.Unmarshal(f[2], &msg)
			}
			return nil, fmt.Errorf("relay closed the watch: %s", msg)
		}
	}
}
