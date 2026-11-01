package buzz

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/gatestore"
	"github.com/omriariav/amq-squad/v3/internal/gateverify"
	"github.com/omriariav/amq-squad/v3/internal/nostr"
	"github.com/omriariav/amq-squad/v3/internal/status"
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

// Row 6, with Codex's P1 on #838: the team confirmation over Buzz. The
// lead's process sends its plan as the cockpit reply labelled
// team-plan:<content digest>; the relay records plan_for on that post. A
// bare yes whose direct reply target is the recorded plan post is the team
// confirmation: the confirmer gets the yes, the post's raw bytes and the
// recorded digest. A yes to any other post, even one whose text carries the
// live digest, is a phone continuation and confirms nothing; outside every
// thread it is a gate answer. The 3.1.0 prefix form keeps verifying
// (TestTheBuzzDoorConfirmsOnlyTheOwnersSignedYes).
func TestABareYesRepliedToTheRecordedPlanPostConfirmsTheTeam(t *testing.T) {
	const content = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	digest := "sha256:" + content + "@absent"
	pf := newPhoneFixture(t)
	type call struct{ prefix, yes, post, planFor string }
	var calls []call
	pf.h.Confirm = func(p string, raw []byte, plan *gateverify.PlanPost) (string, error) {
		y, _ := nostr.ParseEvent(raw)
		c := call{prefix: p, yes: y.ID}
		if plan != nil {
			pe, err := nostr.ParseEvent(plan.Raw)
			if err != nil || !pe.CheckID() || pe.PubKey != pf.core.Pin.Body {
				t.Fatalf("the confirmer got a post that is not the body's fetched event: %v", err)
			}
			c.post, c.planFor = pe.ID, plan.For
		}
		calls = append(calls, c)
		return digest, nil
	}
	_, rid := pf.ask(t, "propose a team for the release", "")
	// a reply that names the digest but is not marked as the plan comes first
	pf.threads.reply("lead-a", CockpitThread(rid), "I am preparing a plan; its digest will be "+digest+".", "")
	pf.threads.reply("lead-a", CockpitThread(rid), "Proposed team: lead, two implementers.\ndigest "+digest, "", gateverify.TeamPlanLabel+content)
	pf.relay(t)
	posts := pf.relayPosts()
	if len(posts) != 2 {
		t.Fatalf("%d relay posts", len(posts))
	}
	mention, plan := posts[0], posts[1]
	rec := pf.record(t, rid)
	if len(rec.Posts) != 2 || rec.Posts[0].PlanFor != "" || rec.Posts[1].PlanFor != content {
		t.Fatalf("the relay record: %+v", rec.Posts)
	}
	bad := *rec
	bad.Posts = []status.RelayPost{rec.Posts[0], rec.Posts[1]}
	bad.Posts[1].PlanFor = "0123"
	if err := bad.Validate(rid); err == nil {
		t.Fatal("a relay record with a malformed plan_for validates")
	}
	sent := len(pf.mail.sent)

	// a yes to the post that only mentions the live digest: a continuation
	_, msg := pf.dm(t, "yes", mention.ID)
	pf.step(t)
	if res, _ := pf.serve.result(msg); len(calls) != 0 || !strings.HasPrefix(res, "accepted "+rid+" ") || len(pf.mail.sent) != sent+1 {
		t.Fatalf("a bare yes to a post naming the digest: %q, calls %+v", res, calls)
	}

	// a yes to the recorded plan post: the team confirmation
	yes, msg := pf.dm(t, "yes", plan.ID)
	pf.step(t)
	if res, _ := pf.serve.result(msg); res != "confirmed team" || len(calls) != 1 || calls[0] != (call{"", yes, plan.ID, content}) {
		t.Fatalf("a bare yes replied to the plan: %q, calls %+v", res, calls)
	}
	if len(pf.mail.sent) != sent+1 {
		t.Fatal("the team yes reached the lead as a phone request")
	}

	// the same yes outside the thread confirms nothing: it is a gate answer
	_, msg = pf.dm(t, "yes", "")
	pf.step(t)
	if res, _ := pf.serve.result(msg); len(calls) != 1 || res == "confirmed team" || !strings.Contains(res, gateverify.RejectThread) {
		t.Fatalf("a bare yes outside the thread: %q, calls %+v", res, calls)
	}

	// a yes naming the plan post only as its root (its parent elsewhere) is
	// no confirmation: the direct reply target binds it
	e := nostr.Event{CreatedAt: time.Now().Unix(), Kind: KindDM, Content: "yes", Tags: [][]string{{"h", pf.core.Pin.Channel}, {"e", plan.ID, "", "root"}, {"e", mention.ID, "", "reply"}}}
	if err := pf.owner.sk.Sign(&e); err != nil {
		t.Fatal(err)
	}
	_ = pf.mem.Publish(context.Background(), e.Marshal())
	msg = pf.serve.deliver(e.ID, pf.core.Pin.Channel, e.Content)
	pf.step(t)
	if res, _ := pf.serve.result(msg); len(calls) != 1 || res == "confirmed team" {
		t.Fatalf("a yes rooted at the plan, replying elsewhere: %q", res)
	}

	// a body-signed post naming the digest that the relay never posted is no
	// plan post
	stray := nostr.Event{CreatedAt: time.Now().Unix(), Kind: KindDM, Tags: [][]string{{"h", pf.core.Pin.Channel}}, Content: "digest " + digest}
	if err := pf.body.Sign(&stray); err != nil {
		t.Fatal(err)
	}
	_ = pf.mem.Publish(context.Background(), stray.Marshal())
	_, msg = pf.dm(t, "yes", stray.ID)
	pf.step(t)
	if res, _ := pf.serve.result(msg); len(calls) != 1 || res == "confirmed team" {
		t.Fatalf("a bare yes to a post the relay never posted: %q, calls %+v", res, calls)
	}

	// a bare no to the plan is no confirmation (it continues the request)
	_, msg = pf.dm(t, "no", plan.ID)
	pf.step(t)
	if res, _ := pf.serve.result(msg); len(calls) != 1 || res == "confirmed team" {
		t.Fatalf("a bare no to the plan: %q", res)
	}
}

// planFor reads exactly one team-plan:<64 hex> label; none, two, or a
// malformed digest is no plan.
func TestTheRelayReadsThePlanFromItsLabelOnly(t *testing.T) {
	const h = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	for labels, want := range map[string]string{
		gateverify.TeamPlanLabel + h:             h,
		"result," + gateverify.TeamPlanLabel + h: h,
		"":                                       "",
		gateverify.TeamPlanLabel + "0123":        "",
		gateverify.TeamPlanLabel + h + "," + gateverify.TeamPlanLabel + h: "",
	} {
		var ls []string
		if labels != "" {
			ls = strings.Split(labels, ",")
		}
		if got := planFor(ThreadEntry{Labels: ls, Body: "digest sha256:" + h}); got != want {
			t.Errorf("labels %q: %q, want %q", labels, got, want)
		}
	}
}
