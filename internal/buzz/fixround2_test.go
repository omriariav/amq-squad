package buzz

import (
	"errors"
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/v3/internal/redact"
)

// Fix round 2, item 1: a route marks its event only after the forward
// succeeded; a failing forward leaves no mark and the next delivery retries.
func TestFailedForwardLeavesNoMark(t *testing.T) {
	hf := newHandlerFixture(t)
	hf.mail.err = errors.New("amq down")
	id, msg := hf.dm(t, "@lead-b rebase #12", "")
	hf.step(t)
	if res, _ := hf.serve.result(msg); res != "refused - not_delivered" {
		t.Fatalf("failed forward reply %q", res)
	}
	hf.mail.err = nil
	again := hf.serve.deliver(id, hf.core.Pin.Channel, "@lead-b rebase #12")
	hf.step(t)
	if res, _ := hf.serve.result(again); res != "routed lead-b" || len(hf.mail.sent) != 2 {
		t.Fatalf("the retry: %q, sent %d", res, len(hf.mail.sent))
	}
}

// Fix round 2, item 2: the forwarded text passes redact.Block (ruling 18),
// the owner's own signed text included. The canary is a credential-named
// pair, not a real credential shape.
func TestForwardedTextIsRedacted(t *testing.T) {
	const canary = "GH_TOKEN=zebracanary5amqbvalue"
	for _, re := range redact.RealCredentialShapes {
		if re.MatchString(canary) {
			t.Fatalf("the canary matches a real credential shape %s", re)
		}
	}
	if redact.Block(canary) == canary {
		t.Fatal("redact.Block does not redact the canary; pick another")
	}
	hf := newHandlerFixture(t)
	hf.dm(t, "@lead-b deploy with "+canary, "")
	hf.step(t)
	if len(hf.mail.sent) != 2 {
		t.Fatalf("sent %d", len(hf.mail.sent))
	}
	for _, m := range hf.mail.sent {
		if strings.Contains(m.Body, "zebracanary5amqbvalue") {
			t.Fatalf("a forwarded body to %s carries the secret: %q", m.To, m.Body)
		}
		if !strings.Contains(m.Body, "deploy with") {
			t.Fatalf("the forward lost the message: %q", m.Body)
		}
	}
}
