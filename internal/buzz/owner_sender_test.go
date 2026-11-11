package buzz

import (
	"context"
	"testing"
	"time"
)

func TestOwnerSenderResendGetsFreshEventID(t *testing.T) {
	f := newCoreFixture(t, nil)
	relay := &wsRelay{}
	fixed := time.Now().Truncate(time.Second).Add(100 * time.Millisecond)
	sender := ownerDMSender{
		owner: f.owner, relay: relay, channel: f.core.Pin.Channel, body: f.body.PublicHex(),
		now: func() time.Time { return fixed },
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	first := sender.send(ctx, t, "resend", "thread")
	second := sender.send(ctx, t, "resend", "thread")
	if first == second {
		t.Fatalf("resend has the same event id: %s", first)
	}
	events := relay.mem.events(9, f.owner.pubHex())
	if len(events) != 2 {
		t.Fatalf("relay stored %d owner events, want 2", len(events))
	}
	if events[1].CreatedAt != events[0].CreatedAt+1 {
		t.Fatalf("second created_at %d, first %d", events[1].CreatedAt, events[0].CreatedAt)
	}
}
