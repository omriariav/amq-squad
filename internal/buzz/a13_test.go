package buzz

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/gatestore"
	"github.com/omriariav/amq-squad/v3/internal/gateverify"
	"github.com/omriariav/amq-squad/v3/internal/nostr"
)

// A1-3 at the handler: a thread answer after the gate's window gets the
// window re-ask receipt; a bare yes replied to the window re-ask, after it,
// is accepted for that gate (bareCore and bareGate name it by that id).
func TestA13WindowReaskAtTheHandler(t *testing.T) {
	hf := newHandlerFixture(t)
	hf.core.Now = func() time.Time { return time.Now().Add(-2 * time.Hour) }
	_, q, a := raiseRel(t, hf.core, "rel-w") // raised two hours ago: lapsed
	hf.core.Now = nil
	_, msg := hf.dm(t, a, q.EventID)
	got := hf.step(t)
	res, _ := hf.serve.result(msg)
	if len(got) != 1 || got[0].Gate == nil || got[0].Gate.Outcome != "reasked" || !strings.HasPrefix(res, "refused rel-w "+gateverify.RejectLapsed+"\nre-ask: ") {
		t.Fatalf("the lapsed answer: reply %q handled %+v", res, got)
	}
	w, _ := hf.core.Store.ReadWindow("rel-w")
	if w == nil || !strings.HasSuffix(res, "(event "+w.EventID[:12]+")") {
		t.Fatalf("the receipt does not name the window re-ask: %q %+v", res, w)
	}
	// the relay's gate threads hold the window re-ask (relay.go: a lead
	// reply never posts in it; an owner DM replying to it posts top level)
	if g, err := hf.h.gateView(time.Now()); err != nil || !g.threads[w.EventID] {
		t.Fatalf("the relay's gate threads miss the window re-ask: %v", err)
	}
	time.Sleep(1100 * time.Millisecond) // strictly after the window re-ask's second
	_, msg = hf.dm(t, "yes", w.EventID)
	hf.step(t)
	if res, _ := hf.serve.result(msg); res != "accepted rel-w tag approved" {
		t.Fatalf("a bare yes to the window re-ask: %q", res)
	}
}

// With two squads holding the same gate ids, an answer replied to squad B's
// window re-ask goes to squad B: a bare yes (bareCore) and a typed answer
// (coreFor) each name B's gate only through that re-ask's id.
func TestA13TheWindowReaskNamesItsSquad(t *testing.T) {
	hf, a, b, _ := twoSquads(t)
	ctx := context.Background()
	past := func() time.Time { return time.Now().Add(-2 * time.Hour) }
	a.Now, b.Now = past, past
	windows := map[string]string{}
	for _, id := range []string{"rel-wx", "rel-wy"} {
		spec := gatestore.Spec{GateID: id, Action: "tag", Target: "git tag " + id, RaisedBy: "cto", Window: 30 * time.Minute}
		if _, _, err := a.Raise(ctx, spec); err != nil {
			t.Fatal(err)
		}
		recB, qB, err := b.Raise(ctx, spec)
		if err != nil {
			t.Fatal(err)
		}
		a.Now, b.Now = nil, nil
		_, msg := hf.dm(t, "approve "+id+" release "+strings.TrimPrefix(recB.Digest, "sha256:")[:16], qB.EventID)
		hf.step(t)
		if res, _ := hf.serve.result(msg); !strings.HasPrefix(res, "refused "+id+" "+gateverify.RejectLapsed+"\n") {
			t.Fatalf("%s lapsed in squad B: %q", id, res)
		}
		w, _ := b.Store.ReadWindow(id)
		if w == nil {
			t.Fatalf("%s: no window re-ask in squad B", id)
		}
		if wa, _ := a.Store.ReadWindow(id); wa != nil {
			t.Fatalf("%s: a window re-ask in squad A", id)
		}
		windows[id] = w.EventID
		a.Now, b.Now = past, past
	}
	a.Now, b.Now = nil, nil
	time.Sleep(1100 * time.Millisecond) // strictly after the window re-asks' second
	recY, _ := b.Store.Load("rel-wy")
	for id, content := range map[string]string{"rel-wx": "yes", "rel-wy": "approve rel-wy release " + strings.TrimPrefix(recY.Digest, "sha256:")[:16]} {
		_, msg := hf.dm(t, content, windows[id])
		hf.step(t)
		if res, _ := hf.serve.result(msg); res != "accepted "+id+" tag approved" {
			t.Fatalf("%s replied to squad B's window re-ask: %q", id, res)
		}
		if ans, _ := b.Store.ReadAnswer(id); ans == nil {
			t.Fatalf("%s: squad B holds no answer", id)
		}
	}
}

// a13FailRelay is the verifier's relay with Publish failing while fail is set.
type a13FailRelay struct {
	gateverify.Relay
	fail *bool
}

func (r a13FailRelay) Publish(ctx context.Context, raw []byte) error {
	if *r.fail {
		return errors.New("relay down")
	}
	return r.Relay.Publish(ctx, raw)
}

// H3 (the second Codex pass, finding 2): the window re-ask's publish fails
// at the handler; the receipt says so and the event is not recorded as
// handled, so serve delivering it again reaches the core, which publishes
// the SAME window re-ask and refuses the trigger.
func TestA13AFailedWindowPublishIsRetriedThroughTheHandler(t *testing.T) {
	hf := newHandlerFixture(t)
	hf.core.Now = func() time.Time { return time.Now().Add(-2 * time.Hour) }
	_, q, a := raiseRel(t, hf.core, "rel-wp") // raised two hours ago: lapsed
	hf.core.Now = nil
	fail := true
	dial := hf.h.Dial
	hf.h.Dial = func(ctx context.Context) (gateverify.Relay, nostr.SecretKey, []string, func(), error) {
		r, body, grant, closeFn, err := dial(ctx)
		return a13FailRelay{Relay: r, fail: &fail}, body, grant, closeFn, err
	}
	id, msg := hf.dm(t, a, q.EventID)
	hf.step(t)
	if res, _ := hf.serve.result(msg); res != "refused rel-wp "+gateverify.RejectPublish {
		t.Fatalf("the lapsed answer while the relay refuses posts: %q", res)
	}
	w, _ := hf.core.Store.ReadWindow("rel-wp")
	if w == nil || hf.core.Store.WindowPublished("rel-wp") {
		t.Fatalf("the window re-ask after the failed post: %+v", w)
	}
	fail = false
	again := hf.serve.deliver(id, hf.core.Pin.Channel, a)
	hf.step(t)
	res, _ := hf.serve.result(again)
	if !strings.HasPrefix(res, "refused rel-wp "+gateverify.RejectBeforeReask+"\nre-ask: ") || !strings.HasSuffix(res, "(event "+w.EventID[:12]+")") {
		t.Fatalf("the same event delivered again: %q", res)
	}
	w2, _ := hf.core.Store.ReadWindow("rel-wp")
	if w2 == nil || w2.EventID != w.EventID || !hf.core.Store.WindowPublished("rel-wp") {
		t.Fatalf("after the retry: %+v published %v", w2, hf.core.Store.WindowPublished("rel-wp"))
	}
	if got, err := hf.mem.FetchByID(context.Background(), w.EventID); err != nil || got == nil {
		t.Fatalf("the same window re-ask is not on the relay: %v", err)
	}
}
