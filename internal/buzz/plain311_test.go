package buzz

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/gatestore"
	"github.com/omriariav/amq-squad/v3/internal/gateverify"
	"github.com/omriariav/amq-squad/v3/internal/nostr"
)

// Brief 3.1.1-1, row 2 at the handler: a bare yes is a gate answer, never a
// phone request. Outside every thread it gets the plain re-ask in the one
// open gate's thread; replied in that thread it is accepted for that gate.
func TestABareYesIsAGateAnswerAtTheHandler(t *testing.T) {
	hf := newHandlerFixture(t)
	rec, q, _ := raiseRel(t, hf.core, "rel-bare")
	_, msg := hf.dm(t, "yes", "")
	got := hf.step(t)
	res, _ := hf.serve.result(msg)
	if len(got) != 1 || got[0].Kind != "gate" || got[0].Gate == nil || got[0].Gate.Outcome != "reasked" || !strings.HasPrefix(res, "refused rel-bare "+gateverify.RejectThread) {
		t.Fatalf("a bare yes outside the thread: reply %q handled %+v", res, got)
	}
	if len(hf.mail.sent) != 0 {
		t.Fatalf("a bare yes reached a lead: %+v", hf.mail.sent)
	}
	ra, _ := hf.core.Store.ReadReask(rec.GateID)
	if ra == nil || !strings.HasPrefix(mustContent(t, ra.RawEvent), "Reply yes or no to this message.\n\n"+gatestore.Rule+"\n") {
		t.Fatalf("the re-ask: %+v", ra)
	}
	_, msg = hf.dm(t, "Yes.", q.EventID)
	hf.step(t)
	if res, _ := hf.serve.result(msg); res != "accepted rel-bare tag" {
		t.Fatalf("a bare yes in the thread: %q", res)
	}
}

// With two squads, a bare answer goes to the squad whose question its
// thread names (coreFor's bare path), and a bare answer outside every
// thread while both squads hold an open gate is ambiguous.
func TestABareYesGoesToTheSquadWhoseThreadItNames(t *testing.T) {
	hf, a, b, _ := twoSquads(t)
	ctx := context.Background()
	spec := gatestore.Spec{GateID: "rel-9", Action: "tag", Target: "git tag v3.1.1", RaisedBy: "cto", Window: 30 * time.Minute}
	if _, _, err := a.Raise(ctx, spec); err != nil {
		t.Fatal(err)
	}
	_, qB, err := b.Raise(ctx, spec)
	if err != nil {
		t.Fatal(err)
	}
	_, msg := hf.dm(t, "yes", "")
	hf.step(t)
	if res, _ := hf.serve.result(msg); res != "refused - ambiguous_gate" {
		t.Fatalf("a bare yes outside every thread with two squads: %q", res)
	}
	_, msg = hf.dm(t, "yes", qB.EventID)
	hf.step(t)
	if res, _ := hf.serve.result(msg); res != "accepted rel-9 tag" {
		t.Fatalf("a bare yes in squad B's thread: %q", res)
	}
	if ans, _ := b.Store.ReadAnswer("rel-9"); ans == nil {
		t.Fatal("squad B holds no answer")
	}
	if ans, _ := a.Store.ReadAnswer("rel-9"); ans != nil {
		t.Fatal("squad A took squad B's answer")
	}
}

func mustContent(t *testing.T, raw string) string {
	t.Helper()
	e, err := nostr.ParseEvent([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	return e.Content
}

// Row 6: the team confirmation over Buzz. The lead's plan reaches the
// owner's DM as a relayed post (brief 31-4); a bare yes replied to that post
// is the team confirmation: the confirmer gets the yes and the post's raw
// bytes, fetched on the verifier's connection. The same yes outside the
// thread confirms nothing (it is a gate answer), and replied to a relay post
// that names no proposal it continues that request. The 3.1.0 prefix form
// keeps verifying (TestTheBuzzDoorConfirmsOnlyTheOwnersSignedYes).
func TestABareYesRepliedToTheRelayedPlanConfirmsTheTeam(t *testing.T) {
	const content = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	digest := "sha256:" + content + "@absent"
	pf := newPhoneFixture(t)
	type call struct {
		prefix string
		yes    string
		post   string
	}
	var calls []call
	pf.h.Confirm = func(p string, raw, post []byte) (string, error) {
		y, _ := nostr.ParseEvent(raw)
		c := call{prefix: p, yes: y.ID}
		if post != nil {
			pe, err := nostr.ParseEvent(post)
			if err != nil || !pe.CheckID() || pe.PubKey != pf.core.Pin.Body {
				t.Fatalf("the confirmer got a post that is not the body's fetched event: %v", err)
			}
			c.post = pe.ID
			if !slices.Contains(gateverify.PlanPrefixes(pe.Content), content[:16]) {
				calls = append(calls, c)
				return "", fmt.Errorf("%w: the post names no proposal", gateverify.ErrNotAPlan)
			}
		}
		calls = append(calls, c)
		return digest, nil
	}
	_, rid := pf.ask(t, "propose a team for the release", "")
	pf.threads.reply("lead-a", CockpitThread(rid), "Proposed team: lead, two implementers.\ndigest "+digest+"\nprefix "+content[:16], "")
	pf.relay(t)
	plan := pf.relayPosts()[0]
	sent := len(pf.mail.sent)

	yes, msg := pf.dm(t, "yes", plan.ID)
	pf.step(t)
	if res, _ := pf.serve.result(msg); res != "confirmed team" || len(calls) != 1 || calls[0] != (call{"", yes, plan.ID}) {
		t.Fatalf("a bare yes replied to the plan: %q, calls %+v", res, calls)
	}
	if len(pf.mail.sent) != sent || len(pf.record(t, rid).Turns) != 1 {
		t.Fatal("the team yes reached the lead as a phone request")
	}

	// the same yes outside the thread confirms nothing: it is a gate answer
	_, msg = pf.dm(t, "yes", "")
	pf.step(t)
	if res, _ := pf.serve.result(msg); len(calls) != 1 || res == "confirmed team" || !strings.Contains(res, gateverify.RejectThread) {
		t.Fatalf("a bare yes outside the thread: %q, calls %+v", res, calls)
	}

	// replied to a relay post that names no digest, it continues the request
	pf.threads.reply("lead-a", CockpitThread(rid), "Shall I start it now?", "")
	pf.relay(t)
	other := pf.relayPosts()[1]
	_, msg = pf.dm(t, "yes", other.ID)
	pf.step(t)
	if res, _ := pf.serve.result(msg); len(calls) != 1 || !strings.HasPrefix(res, "accepted "+rid+" ") || len(pf.record(t, rid).Turns) != 2 {
		t.Fatalf("a bare yes to a post with no plan: %q, calls %+v", res, calls)
	}

	// replied to a post naming a digest no proposal carries, it continues too
	pf.threads.reply("lead-a", CockpitThread(rid), "Merged as fedcba9876543210fedcba9876543210fedcba98.", "")
	pf.relay(t)
	merged := pf.relayPosts()[2]
	_, msg = pf.dm(t, "yes", merged.ID)
	pf.step(t)
	if res, _ := pf.serve.result(msg); len(calls) != 2 || calls[1].post != merged.ID || !strings.HasPrefix(res, "accepted "+rid+" ") {
		t.Fatalf("a bare yes to a post naming no proposal: %q, calls %+v", res, calls)
	}

	// a body-signed post naming the digest that the relay never posted is no
	// plan post: the yes to it confirms nothing
	stray := nostr.Event{CreatedAt: time.Now().Unix(), Kind: KindDM, Tags: [][]string{{"h", pf.core.Pin.Channel}}, Content: "digest " + digest}
	if err := pf.body.Sign(&stray); err != nil {
		t.Fatal(err)
	}
	_ = pf.mem.Publish(context.Background(), stray.Marshal())
	_, msg = pf.dm(t, "yes", stray.ID)
	pf.step(t)
	if res, _ := pf.serve.result(msg); len(calls) != 2 || res == "confirmed team" {
		t.Fatalf("a bare yes to a post the relay never posted: %q, calls %+v", res, calls)
	}

	// a bare no to the plan is no confirmation (it continues the request)
	_, msg = pf.dm(t, "no", plan.ID)
	pf.step(t)
	if res, _ := pf.serve.result(msg); len(calls) != 2 || res == "confirmed team" {
		t.Fatalf("a bare no to the plan: %q", res)
	}
}

func TestPlanPrefixes(t *testing.T) {
	got := gateverify.PlanPrefixes("digest sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef@absent\nprefix 0123456789abcdef; head 4852f761")
	if len(got) != 1 || got[0] != "0123456789abcdef" {
		t.Fatalf("PlanPrefixes = %v", got)
	}
	if got := gateverify.PlanPrefixes("The plan is ready."); len(got) != 0 {
		t.Fatalf("PlanPrefixes = %v", got)
	}
	if !errors.Is(fmt.Errorf("%w: x", gateverify.ErrNotAPlan), gateverify.ErrNotAPlan) {
		t.Fatal("ErrNotAPlan does not wrap")
	}
}
