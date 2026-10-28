package buzz

import (
	"context"
	"errors"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/gatestore"
	"github.com/omriariav/amq-squad/v3/internal/gateverify"
	"github.com/omriariav/amq-squad/v3/internal/nostr"
	"github.com/omriariav/amq-squad/v3/internal/redact"
)

func raiseRel(t *testing.T, c *gateverify.Core, id string) (gatestore.Record, gatestore.Question, string) {
	t.Helper()
	rec, q, err := c.Raise(context.Background(), gatestore.Spec{GateID: id, Action: "tag", Target: "git tag v3.0.0-" + id, RaisedBy: "lead-a", Window: 30 * time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	return rec, q, "approve " + id + " release " + strings.TrimPrefix(rec.Digest, "sha256:")[:16]
}

// 5-amq-B acceptance 4, 5 and 6 (in process): serve delivers, the handler
// replies once per message; the gate question says "reply in this thread";
// a plain answer is re-asked with a pointer at the thread; a phone message
// is routed; a thread answer is accepted and its answer file verifies; a
// malformed answer goes to the core, never to a lead.
func TestHandlerGatesAndRouting(t *testing.T) {
	hf := newHandlerFixture(t)
	rec, q, answer := raiseRel(t, hf.core, "rel-1")
	posted := hf.mem.events(9, hf.body.PublicHex())
	if len(posted) != 1 || posted[0].ID != q.EventID || !strings.Contains(strings.ToLower(posted[0].Content), "reply in this thread") {
		t.Fatalf("question on the relay = %+v", posted)
	}
	_, plainMsg := hf.dm(t, answer, "")
	_, routeMsg := hf.dm(t, "@lead-b rebase #12", "")
	got := hf.step(t)
	if len(got) != 2 || got[0].Kind != "gate" || got[0].Gate.Outcome != "reasked" || got[1].Kind != "route" || got[1].Delivery.Lead != "lead-b" || len(hf.mail.sent) != 2 {
		t.Fatalf("step 1 = %+v, sent %d", got, len(hf.mail.sent))
	}
	reask, _ := hf.core.Store.ReadReask("rel-1")
	if res, n := hf.serve.result(plainMsg); n != 1 || res != "refused rel-1 "+gateverify.RejectThread+"\nre-ask: answer in the gate thread (event "+reask.EventID[:12]+")" {
		t.Fatalf("re-ask reply %q (%d)", res, n)
	}
	if res, n := hf.serve.result(routeMsg); n != 1 || res != "routed lead-b" {
		t.Fatalf("route reply %q (%d)", res, n)
	}
	if again := hf.step(t); len(again) != 0 {
		t.Fatalf("step 2 = %+v", again)
	}
	_, threadMsg := hf.dm(t, answer, q.EventID)
	got = hf.step(t)
	if len(got) != 1 || got[0].Gate == nil || got[0].Gate.Outcome != "approved" {
		t.Fatalf("thread answer = %+v", got)
	}
	if res, n := hf.serve.result(threadMsg); n != 1 || res != "accepted rel-1 "+rec.Class {
		t.Fatalf("accepted reply %q (%d)", res, n)
	}
	if st := hf.serve.statuses(threadMsg); len(st) != 1 || st[0] != "verifying rel-1" {
		t.Fatalf("status lines %q", st)
	}
	ans, err := hf.core.Store.ReadAnswer("rel-1")
	if err != nil {
		t.Fatal(err)
	}
	if err := gateverify.VerifyAnswerFile(*ans, hf.trust, hf.core.Pin, rec, &q, reask, hf.core.Namespace); err != nil {
		t.Fatalf("answer file: %v", err)
	}
	_, badMsg := hf.dm(t, "approve rel-1 release", "")
	got = hf.step(t)
	if len(got) != 1 || got[0].Kind != "gate" || got[0].Gate.Outcome != "rejected" || len(hf.mail.sent) != 2 {
		t.Fatalf("malformed answer = %+v, sent %d", got, len(hf.mail.sent))
	}
	if res, _ := hf.serve.result(badMsg); res != "refused - "+gateverify.RejectBadTuple {
		t.Fatalf("malformed reply %q", res)
	}
}

// Acceptance 4: the reply table. One non-status reply per inbound for a
// verified answer, every gateverify refusal reachable from a phone, and a
// routed message; each through redact.Block; none carries the answer text
// or the message (a canary in each); the canary is no credential shape.
func TestHandlerReplyTable(t *testing.T) {
	const canary = "zebra-canary-5amqb"
	for _, re := range redact.RealCredentialShapes {
		if re.MatchString(canary) {
			t.Fatalf("the canary matches a credential shape %s", re)
		}
	}
	stranger := newThrowaway(t)
	hex16 := regexp.MustCompile(`[0-9a-f]{16}`)
	cases := []struct {
		name string
		send func(t *testing.T, hf handlerFixture) string // returns the message id
		want *regexp.Regexp
	}{
		{"verified", func(t *testing.T, hf handlerFixture) string {
			_, q, a := raiseRel(t, hf.core, "g-ok")
			_, m := hf.dm(t, a, q.EventID)
			return m
		}, regexp.MustCompile(`^accepted g-ok tag$`)},
		{"replayed", func(t *testing.T, hf handlerFixture) string {
			_, q, a := raiseRel(t, hf.core, "g-rp")
			_, _ = hf.dm(t, a, q.EventID)
			hf.step(t)
			_, m := hf.dm(t, a, q.EventID) // a second owner event for a consumed gate
			return m
		}, regexp.MustCompile(`^refused g-rp ` + gatestore.RejectReplayed + `$`)},
		{"wrong pubkey", func(t *testing.T, hf handlerFixture) string {
			_, q, a := raiseRel(t, hf.core, "g-wp")
			e := nostr.Event{CreatedAt: time.Now().Unix(), Kind: 9, Tags: [][]string{{"h", hf.core.Pin.Channel}, {"e", q.EventID, "", "reply"}}, Content: a}
			_ = stranger.sk.Sign(&e)
			_ = hf.mem.Publish(context.Background(), e.Marshal())
			return hf.serve.deliver(e.ID, hf.core.Pin.Channel, e.Content)
		}, regexp.MustCompile(`^refused g-wp ` + gateverify.RejectWrongPubkey + `$`)},
		{"outside window", func(t *testing.T, hf handlerFixture) string {
			_, q, a := raiseRel(t, hf.core, "g-ow")
			hf.core.Now = func() time.Time { return time.Now().Add(2 * time.Hour) }
			_, m := hf.dm(t, a, q.EventID)
			return m
		}, regexp.MustCompile(`^refused g-ow ` + gateverify.RejectWindow + `$`)},
		{"bad signature", func(t *testing.T, hf handlerFixture) string {
			_, q, a := raiseRel(t, hf.core, "g-bs")
			hf.mem.forge = true // the relay returns the event with its signature broken
			_, m := hf.dm(t, a, q.EventID)
			return m
		}, regexp.MustCompile(`^refused g-bs ` + gateverify.RejectBadSignature + `$`)},
		{"not in thread, second time", func(t *testing.T, hf handlerFixture) string {
			_, _, a := raiseRel(t, hf.core, "g-nt")
			_, _ = hf.dm(t, a, "")
			hf.step(t)
			_, m := hf.dm(t, "A"+a[1:], "") // another owner event, still outside the thread
			return m
		}, regexp.MustCompile(`^refused g-nt ` + gateverify.RejectThread + `\nre-ask: answer in the gate thread \(event [0-9a-f]{12}\)$`)},
		{"routed", func(t *testing.T, hf handlerFixture) string {
			_, m := hf.dm(t, "@lead-b "+canary, "")
			return m
		}, regexp.MustCompile(`^routed lead-b$`)},
		{"not routed on", func(t *testing.T, hf handlerFixture) string {
			hf.h.Target.Leads = append(hf.h.Target.Leads, "ghost")
			_, m := hf.dm(t, "@ghost "+canary, "")
			return m
		}, regexp.MustCompile(`^routed lead-a not_forwarded not_a_lead$`)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			hf := newHandlerFixture(t)
			msg := c.send(t, hf)
			hf.step(t)
			res, n := hf.serve.result(msg)
			if n != 1 || !c.want.MatchString(res) {
				t.Fatalf("reply %q (%d non-status), want %s", res, n, c.want)
			}
			for _, r := range hf.serve.allReplies() {
				// the message text, the question's text (its target) and the
				// answer's digest prefix (16 hex) never reach a reply
				if strings.Contains(r.Text, canary) || strings.Contains(r.Text, "git tag") || hex16.MatchString(r.Text) {
					t.Fatalf("a reply carries message, question or answer text: %q", r.Text)
				}
				if r.Text != redact.Block(r.Text) {
					t.Fatalf("a reply did not pass redact.Block: %q", r.Text)
				}
			}
		})
	}
}

// Acceptance 2 and the principle: the attachment never reads the DM
// channel. A DM on the relay that serve did not deliver is never handled,
// and a message serve did deliver is judged by the event the verifier
// fetched, never by the text serve put in it.
func TestHandlerOnlyActsOnServeDeliveriesAndFetchedEvents(t *testing.T) {
	hf := newHandlerFixture(t)
	_, q, answer := raiseRel(t, hf.core, "rel-2")
	hf.ownerDM(t, answer, q.EventID) // on the relay, never delivered
	hf.ownerDM(t, "@lead-b undelivered", "")
	if got := hf.step(t); len(got) != 0 || len(hf.mail.sent) != 0 {
		t.Fatalf("an undelivered DM was handled: %+v", got)
	}
	if d, _ := hf.dial.counts(); d != 0 {
		t.Fatalf("the handler opened %d relay connections with nothing delivered", d)
	}
	// serve's copy says approve, the signed event says something else
	hello := hf.ownerDM(t, "hello", q.EventID)
	msg := hf.serve.deliver(hello, hf.core.Pin.Channel, answer)
	got := hf.step(t)
	if len(got) != 1 || got[0].Gate == nil || got[0].Gate.Code != gateverify.RejectBadTuple {
		t.Fatalf("serve's text was taken as the answer: %+v", got)
	}
	if res, _ := hf.serve.result(msg); res != "refused rel-2 "+gateverify.RejectBadTuple {
		t.Fatalf("reply %q", res)
	}
	if rec, _ := hf.core.Store.Load("rel-2"); hf.core.Store.State(rec, time.Now()) != gatestore.StateOpen {
		t.Fatal("the gate closed on serve's copy")
	}
	// a label naming no stored event, and a message from another sender
	none := hf.serve.deliver(strings.Repeat("ab", 32), hf.core.Pin.Channel, answer)
	hf.serve.put(Prompt{ID: "x-1", From: "lead-a", Labels: []string{"nostr:" + q.EventID}, Body: answer})
	got = hf.step(t)
	if len(got) != 1 || got[0].MessageID != none {
		t.Fatalf("handled %+v", got)
	}
	if res, _ := hf.serve.result(none); res != "refused rel-2 "+gateverify.RejectFetch {
		t.Fatalf("no event reply %q", res)
	}
	if res, n := hf.serve.result("x-1"); n != 0 {
		t.Fatalf("a message from another sender got a reply %q", res)
	}
	if _, err := (&Handler{Inbox: hf.serve}).Step(context.Background()); err == nil {
		t.Fatal("a handler without a core stepped")
	}
}

// Acceptance 5: the same event id delivered twice by serve is handled once;
// an answer outside the gate thread gets the re-ask reply.
func TestHandlerDuplicateDeliveryHandledOnce(t *testing.T) {
	hf := newHandlerFixture(t)
	_, q, answer := raiseRel(t, hf.core, "rel-3")
	id, first := hf.dm(t, answer, q.EventID)
	second := hf.serve.deliver(id, hf.core.Pin.Channel, answer)
	got := hf.step(t)
	if len(got) != 2 || got[0].Gate == nil || got[0].Gate.Outcome != "approved" || got[1].Kind != "duplicate" || got[1].Gate != nil {
		t.Fatalf("handled %+v", got)
	}
	if d, _ := hf.dial.counts(); d != 1 {
		t.Fatalf("the duplicate opened a connection (%d dials)", d)
	}
	if res, _ := hf.serve.result(first); res != "accepted rel-3 tag" {
		t.Fatalf("first %q", res)
	}
	if res, n := hf.serve.result(second); n != 1 || res != "refused rel-3 "+gatestore.RejectReplayed {
		t.Fatalf("second %q (%d)", res, n)
	}
	// a route delivered twice is forwarded once
	rid, _ := hf.dm(t, "@lead-b once", "")
	hf.serve.deliver(rid, hf.core.Pin.Channel, "@lead-b once")
	hf.step(t)
	if len(hf.mail.sent) != 2 {
		t.Fatalf("a duplicate route was delivered again: %d sends", len(hf.mail.sent))
	}
}

// Acceptance 3 (in process): one connection per verification, closed after;
// a hang is capped, yields an attention row and a status line, and no
// result, and the gate stays open for a re-answer.
func TestHandlerVerifierConnectionPerAnswer(t *testing.T) {
	hf := newHandlerFixture(t)
	_, q, answer := raiseRel(t, hf.core, "rel-4")
	_, msg := hf.dm(t, answer, q.EventID)
	var during int
	hf.mem.onQuery = func() { _, during = hf.dial.counts() }
	hf.step(t)
	if d, open := hf.dial.counts(); d != 1 || open != 0 || during != 1 {
		t.Fatalf("dials %d, open after %d, open during %d", d, open, during)
	}
	if res, _ := hf.serve.result(msg); res != "accepted rel-4 tag" {
		t.Fatalf("reply %q", res)
	}

	hang := newHandlerFixture(t)
	hang.h.VerifyTimeout = 200 * time.Millisecond
	_, q2, answer2 := raiseRel(t, hang.core, "rel-5")
	_, msg2 := hang.dm(t, answer2, q2.EventID)
	hang.mem.stall = true
	start := time.Now()
	got := hang.step(t)
	if took := time.Since(start); took > 2*time.Second {
		t.Fatalf("the hang was not capped: %s", took)
	}
	if len(got) != 1 || got[0].Kind != "attention" || !strings.Contains(got[0].Error, "did not finish within") {
		t.Fatalf("hang = %+v", got)
	}
	if res, n := hang.serve.result(msg2); n != 0 {
		t.Fatalf("a hang sent a result %q", res)
	}
	if st := hang.serve.statuses(msg2); len(st) != 2 || st[1] != "timeout rel-5 verify_timeout" {
		t.Fatalf("status lines %q", st)
	}
	if _, open := hang.dial.counts(); open != 0 {
		t.Fatal("the hung connection stayed open")
	}
	rec, _ := hang.core.Store.Load("rel-5")
	if hang.core.Store.State(rec, time.Now()) != gatestore.StateOpen {
		t.Fatal("the hang closed the gate")
	}
	// the owner answers again: a new event, verified
	hang.mem.stall = false
	_, msg3 := hang.dm(t, answer2, q2.EventID)
	hang.step(t)
	if res, _ := hang.serve.result(msg3); res != "accepted rel-5 tag" {
		t.Fatalf("re-answer %q", res)
	}
	// a connection that cannot be made is a refusal, not a hang
	hang.dial.err = errors.New("relay down")
	_, q4, answer4 := raiseRel(t, hang.core, "rel-6")
	_, msg4 := hang.dm(t, answer4, q4.EventID)
	hang.step(t)
	if res, _ := hang.serve.result(msg4); res != "refused rel-6 "+gateverify.RejectFetch {
		t.Fatalf("no connection %q", res)
	}
}

func TestHandlerReportsAFailedDelivery(t *testing.T) {
	hf := newHandlerFixture(t)
	hf.mail.err = errors.New("amq down")
	id, msg := hf.dm(t, "hello", "")
	got := hf.step(t)
	if len(got) != 1 || got[0].EventID != id || got[0].Kind != "error" || !strings.Contains(got[0].Error, "amq down") {
		t.Fatalf("handled %+v", got)
	}
	if res, _ := hf.serve.result(msg); res != "refused - not_delivered" {
		t.Fatalf("reply %q", res)
	}
}

// A message that cannot be taken stays in the inbox and is handled next
// step (the take is the once-only claim).
func TestHandlerTakeFailureKeepsTheMessage(t *testing.T) {
	hf := newHandlerFixture(t)
	_, msg := hf.dm(t, "hello lead", "")
	hf.serve.takeErr = errors.New("disk full")
	if got, err := hf.h.Step(context.Background()); err == nil || len(got) != 0 || len(hf.mail.sent) != 0 {
		t.Fatalf("take failure: %+v, %v", got, err)
	}
	hf.serve.takeErr = nil
	if got := hf.step(t); len(got) != 1 || got[0].MessageID != msg || len(hf.mail.sent) != 1 {
		t.Fatalf("after recovery: %+v", got)
	}
}

// twoSquads is one owner, relay, body and channel over two squad stores.
func twoSquads(t *testing.T) (handlerFixture, *gateverify.Core, *gateverify.Core, *Handler) {
	t.Helper()
	hf := newHandlerFixture(t)
	fixed := time.Now().Truncate(time.Second)
	a := hf.core
	a.Now = func() time.Time { return fixed }
	b := *a
	store, err := gatestore.Open(filepath.Join(realTempDir(t), "squad-b"))
	if err != nil {
		t.Fatal(err)
	}
	b.Store = store
	hf.h.Cores = []*gateverify.Core{&b}
	return hf, a, &b, hf.h
}

// The amendment 28 ruling: two squads raising the same gate id, action and
// target in the same second post different questions, so the owner's reply
// to squad A's question can never verify against squad B's record.
func TestSquadIdentitySeparatesTheQuestions(t *testing.T) {
	hf, a, b, _ := twoSquads(t)
	ctx := context.Background()
	spec := gatestore.Spec{GateID: "rel-1", Action: "tag", Target: "git tag v3.0.0", RaisedBy: "cto", Window: 30 * time.Minute}
	recA, qA, err := a.Raise(ctx, spec)
	if err != nil {
		t.Fatal(err)
	}
	recB, qB, err := b.Raise(ctx, spec)
	if err != nil {
		t.Fatal(err)
	}
	if qA.EventID == qB.EventID || recA.Squad == "" || recA.Squad == recB.Squad || !strings.Contains(recA.Text, "squad: "+recA.Squad) {
		t.Fatalf("the two squads' questions are not distinct: %s %s, squads %q %q", qA.EventID, qB.EventID, recA.Squad, recB.Squad)
	}
	prefix := strings.TrimPrefix(recA.Digest, "sha256:")[:16]
	replyA, _ := hf.dm(t, "approve rel-1 release "+prefix, qA.EventID)
	raw, _ := a.Relay.FetchByID(ctx, replyA)
	if r := b.HandleEvent(ctx, raw); r.Outcome == "approved" {
		t.Fatalf("squad B committed an answer to squad A's question: %+v", r)
	}
	got := hf.step(t)
	if len(got) != 1 || got[0].Gate == nil || got[0].Gate.Outcome != "approved" {
		t.Fatalf("the reply to A's question: %+v", got)
	}
	if ans, _ := b.Store.ReadAnswer("rel-1"); ans != nil {
		t.Fatal("squad B holds an answer for squad A's question")
	}
	old, err := gatestore.Render(gatestore.Spec{GateID: "rel-old", Action: "tag", Target: "git tag v2", RaisedBy: "cto"}, time.Now(), a.Identity())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := gateverify.Verify([]byte(`{}`), old, nil, nil, a.Pin, time.Now()); gateverify.Code(err) != gateverify.RejectNoSquad {
		t.Fatalf("a record without squad identity: %v", err)
	}
}

// Re-pass medium 1: an answer whose gate id is open in two squads and whose
// thread names neither question is ambiguous: refused, re-asked nowhere,
// and the owner's later thread reply is the answer.
func TestAmbiguousAnswerIsRefused(t *testing.T) {
	hf, a, b, _ := twoSquads(t)
	ctx := context.Background()
	spec := gatestore.Spec{GateID: "rel-2", Action: "tag", Target: "git tag v3.1.0", RaisedBy: "cto", Window: 30 * time.Minute}
	recA, _, _ := a.Raise(ctx, spec)
	_, qB, _ := b.Raise(ctx, spec)
	prefix := strings.TrimPrefix(recA.Digest, "sha256:")[:16]
	_, msg := hf.dm(t, "approve rel-2 release "+prefix, "")
	got := hf.step(t)
	if len(got) != 1 || got[0].Gate != nil || !strings.Contains(got[0].Error, "more than one squad") {
		t.Fatalf("ambiguous answer: %+v", got)
	}
	if res, _ := hf.serve.result(msg); res != "refused rel-2 ambiguous_gate" {
		t.Fatalf("reply %q", res)
	}
	if ra, _ := a.Store.ReadReask("rel-2"); ra != nil {
		t.Fatal("the ambiguous answer was re-asked in squad A")
	}
	hf.dm(t, "approve rel-2 release "+prefix, qB.EventID)
	got = hf.step(t)
	if len(got) != 1 || got[0].Gate == nil || got[0].Gate.Outcome != "approved" {
		t.Fatalf("the thread reply to B: %+v", got)
	}
	if ans, _ := b.Store.ReadAnswer("rel-2"); ans == nil {
		t.Fatal("squad B has no answer")
	}
}

// Operator mode: each squad's gates live in that squad's root, so the
// handler gives an answer to the core whose store holds the gate.
func TestHandlerAnswersAGateInAnotherSquadsStore(t *testing.T) {
	hf := newHandlerFixture(t)
	other := *hf.core
	store, err := gatestore.Open(filepath.Join(realTempDir(t), "squad-b"))
	if err != nil {
		t.Fatal(err)
	}
	other.Store = store
	hf.h.Cores = []*gateverify.Core{&other}
	_, q, answer := raiseRel(t, &other, "rel-b")
	hf.dm(t, answer, q.EventID)
	got := hf.step(t)
	if len(got) != 1 || got[0].Gate == nil || got[0].Gate.Outcome != "approved" {
		t.Fatalf("answer for squad b: %+v", got)
	}
	if a, err := other.Store.ReadAnswer("rel-b"); err != nil || a == nil {
		t.Fatalf("the answer is not in squad b's store: %v", err)
	}
	if a, err := hf.core.Store.ReadAnswer("rel-b"); err == nil && a != nil {
		t.Fatal("the answer landed in the primary store")
	}
}
