package buzz

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/gateverify"
	"github.com/omriariav/amq-squad/v3/internal/nostr"
)

// Brief 31-3, row 1d (the handler's half): the Buzz door of R55. The
// owner's signed DM `yes <16-hex prefix>` takes the typed path of a gate
// answer (the event fetched by its id on the verifier's connection, the
// owner's pinned key, the DM kind, the pinned channel) and only then
// reaches the confirmer, which gets the raw bytes the handler fetched (for
// the lead to verify itself). A DM signed by another key, or with a broken signature, never
// reaches it; a prefix no proposal carries is refused by the confirmer's
// name. The test relay (memRelay) carries every event.
func TestTheBuzzDoorConfirmsOnlyTheOwnersSignedYes(t *testing.T) {
	const prefix = "0123456789abcdef"
	type call struct{ prefix, event, owner string }
	rig := func(t *testing.T) (handlerFixture, *[]call) {
		hf := newHandlerFixture(t)
		var calls []call
		hf.h.Confirm = func(p string, raw, _ []byte) (string, error) {
			e, err := nostr.ParseEvent(raw)
			if err != nil || !e.CheckID() {
				t.Fatalf("the confirmer got bytes that are not the fetched event: %v", err)
			}
			calls = append(calls, call{p, e.ID, e.PubKey})
			if p != prefix {
				return "", errors.New("no_proposal: no proposal starts with it")
			}
			return "sha256:" + prefix, nil
		}
		return hf, &calls
	}
	t.Run("signed by the owner", func(t *testing.T) {
		hf, calls := rig(t)
		id, msg := hf.dm(t, "yes "+prefix, "")
		got := hf.step(t)
		if res, _ := hf.serve.result(msg); res != "confirmed team" || len(got) != 1 || got[0].Kind != "team_confirm" {
			t.Fatalf("reply %q handled %+v", res, got)
		}
		if len(*calls) != 1 || (*calls)[0] != (call{prefix, id, hf.owner.pubHex()}) {
			t.Fatalf("confirmer calls %+v", *calls)
		}
		again := hf.serve.deliver(id, hf.core.Pin.Channel, "yes "+prefix)
		hf.step(t)
		if res, _ := hf.serve.result(again); len(*calls) != 1 || res == "confirmed team" {
			t.Fatalf("a redelivery confirmed again: %q %+v", res, *calls)
		}
	})
	t.Run("signed by another key", func(t *testing.T) {
		hf, calls := rig(t)
		stranger := newThrowaway(t)
		e := nostr.Event{CreatedAt: time.Now().Unix(), Kind: 9, Tags: [][]string{{"h", hf.core.Pin.Channel}}, Content: "yes " + prefix}
		_ = stranger.sk.Sign(&e)
		_ = hf.mem.Publish(context.Background(), e.Marshal())
		msg := hf.serve.deliver(e.ID, hf.core.Pin.Channel, e.Content)
		hf.step(t)
		if res, _ := hf.serve.result(msg); res != "refused - "+gateverify.RejectWrongPubkey || len(*calls) != 0 {
			t.Fatalf("reply %q calls %+v", res, *calls)
		}
	})
	t.Run("a broken signature", func(t *testing.T) {
		hf, calls := rig(t)
		hf.mem.forge = true
		_, msg := hf.dm(t, "yes "+prefix, "")
		hf.step(t)
		if res, _ := hf.serve.result(msg); res != "refused - "+gateverify.RejectBadSignature || len(*calls) != 0 {
			t.Fatalf("reply %q calls %+v", res, *calls)
		}
	})
	t.Run("a wrong prefix", func(t *testing.T) {
		hf, _ := rig(t)
		_, msg := hf.dm(t, "yes fedcba9876543210", "")
		hf.step(t)
		if res, _ := hf.serve.result(msg); res != "refused team no_proposal" {
			t.Fatalf("reply %q", res)
		}
	})
	t.Run("serve's copy says yes, the signed event does not", func(t *testing.T) {
		hf, calls := rig(t)
		id := hf.ownerDM(t, "hello", "")
		msg := hf.serve.deliver(id, hf.core.Pin.Channel, "yes "+prefix)
		hf.step(t)
		if res, _ := hf.serve.result(msg); !routedTo(res, "lead-a") || len(*calls) != 0 { // brief 31-4's receipt
			t.Fatalf("reply %q calls %+v", res, *calls)
		}
	})
	if p, ok := gateverify.ParseTeamYes("YES 0123456789ABCDEF"); !ok || p != prefix {
		t.Fatalf("ParseTeamYes = %q %v", p, ok)
	}
	for _, bad := range []string{"yes 0123", "yes 0123456789abcdef please", "approve g1 tag 0123456789abcdef"} {
		if _, ok := gateverify.ParseTeamYes(bad); ok {
			t.Fatalf("ParseTeamYes(%q) ok", bad)
		}
	}
}
