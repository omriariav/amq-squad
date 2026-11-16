package gateverify

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/gatestore"
	"github.com/omriariav/amq-squad/v3/internal/gatetarget"
	"github.com/omriariav/amq-squad/v3/internal/mergetrain"
	"github.com/omriariav/amq-squad/v3/internal/nostr"
	"github.com/omriariav/amq-squad/v3/internal/operatorauth"
)

// A1-3, the window re-ask (D4 "the window", A0 rows 7 and 8): rows at the
// core and through the consumers.

func a13Lapsed(t *testing.T, f *fixture, r Result, rec gatestore.Record, q gatestore.Question) *gatestore.Reask {
	t.Helper()
	if r.Outcome != "reasked" || r.Code != RejectLapsed || r.ReaskEventID == "" {
		t.Fatalf("the lapsed answer: %+v", r)
	}
	w, err := f.store.ReadWindow(rec.GateID)
	if err != nil || w == nil || w.EventID != r.ReaskEventID || w.TriggerEventID != r.EventID || !f.store.WindowPublished(rec.GateID) {
		t.Fatalf("the window re-ask: %+v %v", w, err)
	}
	we := mustEvent(w.RawEvent)
	if we.PubKey != f.body.PublicHex() || we.Content != WindowReaskText(rec) || !strings.Contains(we.Content, rec.Text) || !strings.Contains(we.Content, "30 minutes") {
		t.Fatalf("the window re-ask event: %+v", we)
	}
	if refs := nostr.ThreadRefs(we); len(refs) != 1 || refs[0] != q.EventID {
		t.Fatalf("the window re-ask is not in the gate thread: %v", refs)
	}
	if got, _ := f.relay.FetchByID(ctx, we.ID); got == nil {
		t.Fatal("the window re-ask was not published")
	}
	if d := f.store.Decision(rec.GateID); d != "" {
		t.Fatalf("the lapsed answer decided the gate: %s", d)
	}
	return w
}

func a13Train(f *fixture) mergetrain.AcceptedFunc {
	v := f.verifier()
	return mergetrain.InSquad(f.store.Squad(), func(action, target string) (gatestore.Record, bool) {
		return v.AcceptedFor(f.store, action, target)
	})
}

// W1 (row 7): an answer in the thread created after not_after gets the
// window re-ask and is not accepted; replayed, it never approves; an answer
// in the re-ask's second is refused and consumes nothing; one created after
// approves; row 8 after it; the train's lookup (growth.go AcceptedFor, the
// merge_train.go:278 wiring) finds the gate, and not without windows/<id>.json.
func TestA13WindowReaskThroughTheCoreAndTheTrain(t *testing.T) {
	f := newFixture(t)
	target := gatetarget.MergeBeyond("o/r", 7, strings.Repeat("a", 40), []string{"protected"})
	rec, q := f.raise("merge-w1", "spawn", target)
	_, _, na, _ := rec.Window()
	if _, ok := a13Train(f).Accepted("spawn", target); ok {
		t.Fatal("an unanswered gate counts")
	}
	f.clock = na.Add(2 * time.Minute)
	trigger := f.answer(f.owner, tuple("approve", rec), q.EventID, na.Add(time.Minute))
	w := a13Lapsed(t, f, f.core.HandleEvent(ctx, trigger), rec, q)
	if st := f.store.State(rec, f.clock); st != gatestore.StateOpen {
		t.Fatalf("state after the window re-ask: %s", st)
	}
	// the trigger again: never a decision, no second window re-ask
	if r := f.core.HandleEvent(ctx, trigger); r.Outcome != "rejected" || r.Code != RejectBeforeReask {
		t.Fatalf("the trigger replayed: %+v", r)
	}
	// created in the window re-ask's own second: refused, nothing consumed
	same := f.answer(f.owner, tuple("approve", rec), q.EventID, f.clock)
	if r := f.core.HandleEvent(ctx, same); r.Outcome != "rejected" || r.Code != RejectBeforeReask || !strings.Contains(r.Detail, "answer again") {
		t.Fatalf("the same-second answer: %+v", r)
	}
	if d, st := f.store.Decision(rec.GateID), f.store.State(rec, f.clock); d != "" || st != gatestore.StateOpen {
		t.Fatalf("answered_before_reask consumed the gate: %q %s", d, st)
	}
	if w2, _ := f.store.ReadWindow(rec.GateID); w2 == nil || w2.EventID != w.EventID {
		t.Fatalf("the window re-ask changed: %+v", w2)
	}
	// one second later, replied to the question (the root): approved
	f.clock = f.clock.Add(time.Second)
	good := f.answer(f.owner, tuple("approve", rec), q.EventID, f.clock)
	if r := f.core.HandleEvent(ctx, good); r.Outcome != operatorauth.DecisionApproved {
		t.Fatalf("the answer after the window re-ask: %+v", r)
	}
	// row 8: the same event again, and a later owner answer
	if r := f.core.HandleEvent(ctx, good); r.Outcome != "rejected" || r.Code != RejectReplayed {
		t.Fatalf("the approval replayed: %+v", r)
	}
	f.clock = f.clock.Add(time.Second)
	if r := f.core.HandleEvent(ctx, f.answer(f.owner, tuple("deny", rec), q.EventID, f.clock)); r.Outcome != "rejected" || r.Code != RejectReplayed {
		t.Fatalf("a later deny: %+v", r)
	}
	// the consumers
	if err := f.verifier().Accepted(f.store, rec); err != nil {
		t.Fatalf("Verifier.Accepted: %v", err)
	}
	if id, ok := a13Train(f).Accepted("spawn", target); !ok || id != rec.GateID {
		t.Fatalf("the train's lookup: %q %v", id, ok)
	}
	ans, _ := f.store.ReadAnswer(rec.GateID)
	if err := VerifyAnswerFile(*ans, f.trust, f.core.Pin, rec, &q, nil, f.core.Namespace); err == nil {
		t.Fatal("the approval re-verified without its window re-ask")
	}
	if err := os.Remove(f.store.Dir("windows", rec.GateID+".json")); err != nil {
		t.Fatal(err)
	}
	if err := f.verifier().Accepted(f.store, rec); err == nil {
		t.Fatal("Verifier.Accepted without windows/<id>.json")
	}
	if _, ok := a13Train(f).Accepted("spawn", target); ok {
		t.Fatal("the train found the gate without windows/<id>.json")
	}
}

// W2 (Q1 "presented after"; amendment 2): created inside the window and
// delivered after it: gate_lapsed, re-asked, not approved. A reply tagged
// only to the window re-ask, created after it, approves, and re-verifies.
func TestA13PresentedAfterAndAReplyToTheWindowReask(t *testing.T) {
	f := newFixture(t)
	rec, q := f.raise("g-w2", "tag", "git tag v2")
	_, _, na, _ := rec.Window()
	f.clock = na.Add(time.Minute)
	w := a13Lapsed(t, f, f.core.HandleEvent(ctx, f.answer(f.owner, tuple("approve", rec), q.EventID, na.Add(-time.Minute))), rec, q)
	f.clock = f.clock.Add(5 * time.Second)
	if r := f.core.HandleEvent(ctx, f.answer(f.owner, "yes", w.EventID, f.clock)); r.Outcome != operatorauth.DecisionApproved {
		t.Fatalf("a bare yes to the window re-ask: %+v", r)
	}
	if err := f.verifier().Accepted(f.store, rec); err != nil {
		t.Fatalf("Verifier.Accepted: %v", err)
	}
}

// W3 (A2: yesterday's answer cannot approve today's re-ask): an answer made
// inside the first window but delivered after the window re-ask is refused;
// a fresh one approves.
func TestA13YesterdaysAnswer(t *testing.T) {
	f := newFixture(t)
	rec, q := f.raise("g-w3", "tag", "git tag v3")
	_, _, na, _ := rec.Window()
	yesterday := f.answer(f.owner, tuple("approve", rec), q.EventID, testNow.Add(time.Minute))
	f.clock = na.Add(24 * time.Hour)
	a13Lapsed(t, f, f.core.HandleEvent(ctx, f.answer(f.owner, "no", q.EventID, f.clock)), rec, q)
	if r := f.core.HandleEvent(ctx, yesterday); r.Outcome != "rejected" || r.Code != RejectBeforeReask {
		t.Fatalf("yesterday's answer: %+v", r)
	}
	f.clock = f.clock.Add(time.Minute)
	if r := f.core.HandleEvent(ctx, f.answer(f.owner, tuple("approve", rec), q.EventID, f.clock)); r.Outcome != operatorauth.DecisionApproved {
		t.Fatalf("today's answer: %+v", r)
	}
}

// W4 (Q2): one window re-ask per gate. After the asked-again window, an
// answer is outside_window as today and no second window re-ask is posted.
func TestA13ASecondLapseIsOutsideWindow(t *testing.T) {
	f := newFixture(t)
	rec, q := f.raise("g-w4", "tag", "git tag v4")
	_, _, na, _ := rec.Window()
	f.clock = na.Add(time.Minute)
	w := a13Lapsed(t, f, f.core.HandleEvent(ctx, f.answer(f.owner, tuple("approve", rec), q.EventID, f.clock)), rec, q)
	_, end, err := w.WindowEnd(rec)
	if err != nil || end.Sub(f.clock) != 30*time.Minute {
		t.Fatalf("WindowEnd: %s %v", end, err)
	}
	if st := f.store.State(rec, end); st != gatestore.StateOpen {
		t.Fatalf("state at the derived end: %s", st)
	}
	if st := f.store.State(rec, end.Add(time.Second)); st != gatestore.StateExpired {
		t.Fatalf("state after the derived end: %s", st)
	}
	f.clock = end.Add(time.Second)
	if r := f.core.HandleEvent(ctx, f.answer(f.owner, tuple("approve", rec), q.EventID, f.clock)); r.Outcome != "rejected" || r.Code != RejectWindow {
		t.Fatalf("the answer after the second window: %+v", r)
	}
	// presented after the derived end, created inside it: outside_window too
	f.clock = end.Add(time.Minute)
	if r := f.core.HandleEvent(ctx, f.answer(f.owner, tuple("approve", rec), q.EventID, end.Add(-time.Minute))); r.Outcome != "rejected" || r.Code != RejectWindow {
		t.Fatalf("presented after the second window: %+v", r)
	}
	if w2, _ := f.store.ReadWindow(rec.GateID); w2 == nil || w2.EventID != w.EventID {
		t.Fatalf("a second window re-ask: %+v", w2)
	}
}

// W5, W6 (amendment 3): a late answer outside the thread is refused
// not_in_gate_thread (core.go: the re-ask's open check refuses a lapsed
// gate), one in another thread wrong_gate_thread; neither posts anything.
func TestA13LateAnswersOutsideTheThread(t *testing.T) {
	f := newFixture(t)
	rec, _ := f.raise("g-w5", "tag", "git tag v5")
	_, _, na, _ := rec.Window()
	f.clock = na.Add(time.Minute)
	if r := f.core.HandleEvent(ctx, f.answer(f.owner, tuple("approve", rec), "", f.clock)); r.Outcome != "rejected" || r.Code != RejectThread || r.ReaskEventID != "" {
		t.Fatalf("a late unthreaded answer: %+v", r)
	}
	if r := f.core.HandleEvent(ctx, f.answer(f.owner, tuple("approve", rec), strings.Repeat("c", 64), f.clock)); r.Outcome != "rejected" || r.Code != RejectWrongThread {
		t.Fatalf("a late answer in another thread: %+v", r)
	}
	if ra, _ := f.store.ReadReask(rec.GateID); ra != nil {
		t.Fatalf("a re-ask was posted: %+v", ra)
	}
	if w, _ := f.store.ReadWindow(rec.GateID); w != nil {
		t.Fatalf("a window re-ask was posted: %+v", w)
	}
	if st := f.store.State(rec, f.clock); st != gatestore.StateExpired {
		t.Fatalf("state: %s", st)
	}
}

// W7 (amendment 6; row 8 across the thread re-ask): a thread re-ask, then
// a lapse, then the window re-ask, then an approval replied to the thread
// re-ask; the core and the consumers agree; the thread re-ask's trigger
// replayed is never a decision.
func TestA13BothReasksOnOneGate(t *testing.T) {
	f := newFixture(t)
	target := gatetarget.MergeBeyond("o/r", 8, strings.Repeat("b", 40), []string{"protected"})
	rec, q := f.raise("merge-w7", "spawn", target)
	_, _, na, _ := rec.Window()
	at := testNow.Add(time.Minute)
	f.clock = at
	plain := f.answer(f.owner, tuple("approve", rec), "", at)
	if r := f.core.HandleEvent(ctx, plain); r.Outcome != "reasked" || r.Code != RejectThread {
		t.Fatalf("the plain answer: %+v", r)
	}
	ra, _ := f.store.ReadReask(rec.GateID)
	f.clock = na.Add(time.Minute)
	a13Lapsed(t, f, f.core.HandleEvent(ctx, f.answer(f.owner, tuple("approve", rec), ra.EventID, na.Add(30*time.Second))), rec, q)
	if r := f.core.HandleEvent(ctx, plain); r.Outcome != "rejected" || r.Code != RejectThread {
		t.Fatalf("the thread re-ask's trigger replayed: %+v", r)
	}
	f.clock = f.clock.Add(time.Minute)
	good := f.answer(f.owner, tuple("approve", rec), ra.EventID, f.clock)
	if r := f.core.HandleEvent(ctx, good); r.Outcome != operatorauth.DecisionApproved {
		t.Fatalf("the reply to the thread re-ask after the window re-ask: %+v", r)
	}
	if r := f.core.HandleEvent(ctx, good); r.Code != RejectReplayed {
		t.Fatalf("replayed: %+v", r)
	}
	if err := f.verifier().Accepted(f.store, rec); err != nil {
		t.Fatalf("Verifier.Accepted: %v", err)
	}
	if id, ok := a13Train(f).Accepted("spawn", target); !ok || id != rec.GateID {
		t.Fatalf("the train's lookup: %q %v", id, ok)
	}
}

// W8 (Q4, the trigger never approves): a lapsed answer dated ahead of the
// host's clock (inside the 60 s allowance) triggers a window re-ask dated
// before it; the trigger is still refused, by its id.
func TestA13ATriggerAheadOfTheClockNeverApproves(t *testing.T) {
	f := newFixture(t)
	rec, q := f.raise("g-w8", "tag", "git tag v8")
	_, _, na, _ := rec.Window()
	f.clock = na.Add(time.Minute)
	trigger := f.answer(f.owner, tuple("approve", rec), q.EventID, f.clock.Add(30*time.Second))
	a13Lapsed(t, f, f.core.HandleEvent(ctx, trigger), rec, q)
	f.clock = f.clock.Add(time.Minute)
	if r := f.core.HandleEvent(ctx, trigger); r.Outcome != "rejected" || r.Code != RejectBeforeReask {
		t.Fatalf("the trigger, dated after the window re-ask: %+v", r)
	}
}

// W9 (row 8 across the history re-ask): the history re-ask's reply approves
// once; that event and the trigger replayed are already_consumed.
func TestA13RowEightAcrossTheHistoryReask(t *testing.T) {
	f := newFixture(t)
	rec, q := f.raise("g-w9", "tag", "git tag v9")
	qe, _ := nostr.ParseEvent([]byte(q.RawEvent))
	edit := f.signed(f.owner, KindEdit, "approve git tag v9-evil", [][]string{{"e", q.EventID}}, qe.CreatedAt+1)
	ee, _ := nostr.ParseEvent(edit)
	f.relay.inject(f.signed(f.owner, KindDeletion, "", [][]string{{"e", ee.ID}}, qe.CreatedAt+2))
	at := testNow.Add(time.Minute)
	f.clock = at
	trigger := f.answer(f.owner, tuple("approve", rec), q.EventID, at)
	if r := f.core.HandleEvent(ctx, trigger); r.Outcome != "reasked" || r.Code != RejectAlteredHistory {
		t.Fatalf("the history re-ask: %+v", r)
	}
	ra, _ := f.store.ReadReask(rec.GateID)
	f.clock = at.Add(time.Minute)
	good := f.answer(f.owner, tuple("approve", rec), ra.EventID, f.clock)
	if r := f.core.HandleEvent(ctx, good); r.Outcome != operatorauth.DecisionApproved {
		t.Fatalf("the reply: %+v", r)
	}
	for name, raw := range map[string][]byte{"the approval": good, "the trigger": trigger} {
		if r := f.core.HandleEvent(ctx, raw); r.Outcome != "rejected" || r.Code != RejectReplayed {
			t.Fatalf("%s replayed: %+v", name, r)
		}
	}
}

// V (Verify alone): the code each late input gets after the reorder.
func TestA13VerifyCodes(t *testing.T) {
	f := newFixture(t)
	rec, q := f.raise("g-v", "tag", "git tag v10")
	_, _, na, _ := rec.Window()
	for name, c := range map[string]struct {
		raw  []byte
		now  time.Time
		code string
	}{
		"created after not_after, in the thread":   {f.answer(f.owner, tuple("approve", rec), q.EventID, na.Add(time.Second)), na.Add(time.Second), RejectLapsed},
		"presented after not_after, in the thread": {f.answer(f.owner, tuple("approve", rec), q.EventID, na), na.Add(time.Second), RejectLapsed},
		"created after not_after, no thread":       {f.answer(f.owner, tuple("approve", rec), "", na.Add(time.Second)), na.Add(time.Second), RejectThread},
		"created before not_before":                {f.answer(f.owner, tuple("approve", rec), q.EventID, testNow.Add(-61*time.Second)), testNow, RejectWindow},
		"at not_after exactly":                     {f.answer(f.owner, tuple("approve", rec), q.EventID, na), na, ""},
	} {
		if _, err := Verify(c.raw, rec, &q, nil, f.core.Pin, c.now); Code(err) != c.code {
			t.Errorf("%s: %v, want %q", name, err, c.code)
		}
	}
}

// W10: a stored window re-ask that fails its checks (not the body's signed
// event in the thread) opens no window, and a lapsed answer then posts
// nothing: outside_window, the bad record left as it is.
func TestA13ABadWindowReaskOpensNothing(t *testing.T) {
	f := newFixture(t)
	rec, q := f.raise("g-w10", "tag", "git tag v11")
	_, _, na, _ := rec.Window()
	f.clock = na.Add(time.Minute)
	stray := f.signed(f.seat, 9, "asked again", [][]string{{"h", testChannel}, {"e", q.EventID, "", "root"}}, f.clock.Unix())
	se, _ := nostr.ParseEvent(stray)
	if err := f.store.PutWindow(gatestore.Reask{GateID: rec.GateID, TriggerEventID: strings.Repeat("d4", 32), EventID: se.ID, RawEvent: string(stray), At: f.clock.Format(time.RFC3339)}); err != nil {
		t.Fatal(err)
	}
	f.clock = f.clock.Add(time.Minute)
	if r := f.core.HandleEvent(ctx, f.answer(f.owner, tuple("approve", rec), q.EventID, f.clock)); r.Outcome != "rejected" || r.Code != RejectWindow {
		t.Fatalf("a lapsed answer beside a bad window re-ask: %+v", r)
	}
	if got, _ := f.relay.FetchByID(ctx, se.ID); got != nil {
		t.Fatal("the bad window re-ask was published")
	}
	if f.store.WindowPublished(rec.GateID) {
		t.Fatal("the bad window re-ask was marked published")
	}
}

// failRelay is the fixture's relay whose Publish fails while fail is set.
type failRelay struct {
	*memRelay
	fail      bool
	onPublish func(raw []byte) // called before each publish that goes through
}

func (r *failRelay) Publish(ctx context.Context, raw []byte) error {
	if r.fail {
		return errors.New("relay down")
	}
	if r.onPublish != nil {
		r.onPublish(raw)
	}
	return r.memRelay.Publish(ctx, raw)
}

// a13Unpublished lapses a gate while the relay refuses publishes: the window
// re-ask is reserved, not published, and the trigger is refused.
func a13Unpublished(t *testing.T, f *fixture, rec gatestore.Record, q gatestore.Question) (*failRelay, []byte, *gatestore.Reask) {
	t.Helper()
	fr := &failRelay{memRelay: f.relay, fail: true}
	f.core.Relay = fr
	_, _, na, _ := rec.Window()
	f.clock = na.Add(time.Minute)
	trigger := f.answer(f.owner, tuple("approve", rec), q.EventID, f.clock)
	if r := f.core.HandleEvent(ctx, trigger); r.Outcome != "rejected" || !strings.Contains(r.Detail, "publish the window re-ask") {
		t.Fatalf("the lapsed answer while the relay is down: %+v", r)
	}
	w, _ := f.store.ReadWindow(rec.GateID)
	if w == nil || f.store.WindowPublished(rec.GateID) {
		t.Fatalf("the window re-ask after a failed publish: %+v published %v", w, f.store.WindowPublished(rec.GateID))
	}
	if got, _ := f.relay.FetchByID(ctx, w.EventID); got != nil {
		t.Fatal("the window re-ask reached the relay")
	}
	fr.fail = false
	return fr, trigger, w
}

// W11 (the gap, row i): the first publish of the window re-ask fails; the
// trigger delivered again publishes the SAME event and marks it, and is
// still refused.
func TestA13AFailedWindowPublishIsRetriedByTheTrigger(t *testing.T) {
	f := newFixture(t)
	rec, q := f.raise("g-w11", "tag", "git tag v12")
	_, trigger, w := a13Unpublished(t, f, rec, q)
	f.clock = f.clock.Add(time.Minute)
	if r := f.core.HandleEvent(ctx, trigger); r.Outcome != "rejected" || r.Code != RejectBeforeReask {
		t.Fatalf("the trigger again: %+v", r)
	}
	if w2, _ := f.store.ReadWindow(rec.GateID); w2 == nil || w2.EventID != w.EventID || !f.store.WindowPublished(rec.GateID) {
		t.Fatalf("after the retry: %+v published %v", w2, f.store.WindowPublished(rec.GateID))
	}
	if got, _ := f.relay.FetchByID(ctx, w.EventID); got == nil {
		t.Fatal("the same window re-ask was not published")
	}
}

// W12 (the gap, row ii): the first publish fails; a fresh in-thread answer
// after it publishes the window re-ask first, and is then judged: approved.
func TestA13AFailedWindowPublishIsRetriedBeforeTheAnswer(t *testing.T) {
	f := newFixture(t)
	rec, q := f.raise("g-w12", "tag", "git tag v13")
	fr, _, w := a13Unpublished(t, f, rec, q)
	published := false
	fr.onPublish = func(raw []byte) {
		if e, _ := nostr.ParseEvent(raw); e.ID == w.EventID {
			published = true
			if d := f.store.Decision(rec.GateID); d != "" {
				t.Errorf("the window re-ask was published after the gate was decided (%s)", d)
			}
		}
	}
	f.clock = f.clock.Add(time.Minute)
	if r := f.core.HandleEvent(ctx, f.answer(f.owner, tuple("approve", rec), w.EventID, f.clock)); r.Outcome != operatorauth.DecisionApproved {
		t.Fatalf("the fresh answer: %+v", r)
	}
	if !published || !f.store.WindowPublished(rec.GateID) {
		t.Fatal("the window re-ask was not published before the answer was judged")
	}
	if got, _ := f.relay.FetchByID(ctx, w.EventID); got == nil {
		t.Fatal("the window re-ask is not on the relay")
	}
}

// W13 (the Codex pass on r4, finding 5): the window re-ask shows the
// question's text again, so its history is checked like the question's: an
// edit of it showing other text refuses the answer under it.
func TestA13AnEditedWindowReaskRefusesTheAnswer(t *testing.T) {
	f := newFixture(t)
	rec, q := f.raise("g-w13", "tag", "git tag v14")
	_, _, na, _ := rec.Window()
	f.clock = na.Add(time.Minute)
	w := a13Lapsed(t, f, f.core.HandleEvent(ctx, f.answer(f.owner, tuple("approve", rec), q.EventID, f.clock)), rec, q)
	we := mustEvent(w.RawEvent)
	f.relay.inject(f.signed(f.owner, KindEdit, "approve git tag v14-evil", [][]string{{"e", we.ID}}, we.CreatedAt+1))
	f.clock = f.clock.Add(time.Minute)
	if r := f.core.HandleEvent(ctx, f.answer(f.owner, "yes", w.EventID, f.clock)); r.Outcome != "rejected" || r.Code != RejectAltered {
		t.Fatalf("an answer under an edited window re-ask: %+v", r)
	}
	if d := f.store.Decision(rec.GateID); d != "" {
		t.Fatalf("the gate was decided: %s", d)
	}
}

// W14 (the Codex pass on r4, finding 4): only the window re-ask's own text
// opens a window: the gate's thread re-ask copied into windows/<id>.json
// opens nothing and is not published.
func TestA13AThreadReaskIsNoWindowReask(t *testing.T) {
	f := newFixture(t)
	rec, q := f.raise("g-w14", "tag", "git tag v15")
	_, _, na, _ := rec.Window()
	f.clock = na.Add(-10 * time.Minute)
	if r := f.core.HandleEvent(ctx, f.answer(f.owner, tuple("approve", rec), "", f.clock)); r.Outcome != "reasked" || r.Code != RejectThread {
		t.Fatalf("the plain answer: %+v", r)
	}
	ra, _ := f.store.ReadReask(rec.GateID)
	if err := f.store.PutWindow(*ra); err != nil {
		t.Fatal(err)
	}
	f.clock = na.Add(5 * time.Minute)
	if r := f.core.HandleEvent(ctx, f.answer(f.owner, tuple("approve", rec), q.EventID, f.clock)); r.Outcome != "rejected" || r.Code != RejectWindow {
		t.Fatalf("an answer after the window beside a copied thread re-ask: %+v", r)
	}
	if f.store.WindowPublished(rec.GateID) || f.store.Decision(rec.GateID) != "" {
		t.Fatal("the copied re-ask was published or the gate decided")
	}
}

// a13SignWindow signs and stores the gate's window re-ask the way
// windowReask does, at the fixture clock, as another handler would.
func a13SignWindow(t *testing.T, f *fixture, rec gatestore.Record, q gatestore.Question, trigger string) gatestore.Reask {
	t.Helper()
	extra := [][]string{{"e", q.EventID, f.core.RelayURL, "root"}, {"squad", rec.Squad}}
	evt, err := f.core.sign(WindowReaskText(rec), extra)
	if err != nil {
		t.Fatal(err)
	}
	r := gatestore.Reask{GateID: rec.GateID, TriggerEventID: trigger, EventID: evt.ID, RawEvent: string(evt.Marshal()), At: f.clock.Format(time.RFC3339)}
	if err := f.store.PutWindow(r); err != nil {
		t.Fatal(err)
	}
	if err := f.relay.Publish(ctx, []byte(r.RawEvent)); err != nil {
		t.Fatal(err)
	}
	if err := f.store.MarkWindowPublished(rec.GateID); err != nil {
		t.Fatal(err)
	}
	return r
}

// W15 (the second Codex pass, finding 1a): an answer verified before
// not_after, with a window re-ask reserved by another handler before its
// commit, is not committed: the commit's precondition refuses, and the
// answer judged again against that window re-ask is answered_before_reask.
// (Without the precondition the store would hold an approval every
// consumer refuses.)
func TestA13AWindowReaskBeforeTheCommitStopsIt(t *testing.T) {
	f := newFixture(t)
	target := gatetarget.MergeBeyond("o/r", 15, strings.Repeat("f", 40), []string{"protected"})
	rec, q := f.raise("merge-w15", "spawn", target)
	_, _, na, _ := rec.Window()
	f.clock = na.Add(-time.Minute)
	answer := f.answer(f.owner, tuple("approve", rec), q.EventID, f.clock)
	fired := false
	beforeCommit = func(id string) {
		if id == rec.GateID && !fired {
			fired = true
			f.clock = na.Add(30 * time.Second)
			a13SignWindow(t, f, rec, q, strings.Repeat("d4", 32))
		}
	}
	defer func() { beforeCommit = func(string) {} }()
	r := f.core.HandleEvent(ctx, answer)
	if !fired || r.Outcome != "rejected" || r.Code != RejectBeforeReask {
		t.Fatalf("an answer whose commit met a new window re-ask: %+v (fired %v)", r, fired)
	}
	if d := f.store.Decision(rec.GateID); d != "" {
		t.Fatalf("the gate was decided: %s", d)
	}
	if _, ok := a13Train(f).Accepted("spawn", target); ok {
		t.Fatal("the train found an accepted gate")
	}
}

// W16 (the second Codex pass, finding 1b): an answer after the window,
// whose pass read no window re-ask just before another handler reserved
// one, is judged against that window re-ask (not re-asked again): approved.
func TestA13AWindowReaskAfterTheReadIsUsed(t *testing.T) {
	f := newFixture(t)
	rec, q := f.raise("g-w16", "tag", "git tag v16")
	_, _, na, _ := rec.Window()
	f.clock = na.Add(2 * time.Minute)
	other := f.answer(f.owner, tuple("approve", rec), q.EventID, na.Add(time.Minute))
	fired := false
	afterWindowRead = func(id string) {
		if id == rec.GateID && !fired {
			fired = true
			a13Lapsed(t, f, f.core.HandleEvent(ctx, other), rec, q)
		}
	}
	defer func() { afterWindowRead = func(string) {} }()
	r := f.core.HandleEvent(ctx, f.answer(f.owner, tuple("approve", rec), q.EventID, f.clock.Add(time.Second)))
	if !fired || r.Outcome != operatorauth.DecisionApproved {
		t.Fatalf("an answer whose pass read no window re-ask: %+v (fired %v)", r, fired)
	}
}

// W17 (the third Codex pass): an answer after the window whose pass read
// no window re-ask, while another handler commits an approval made inside
// the original window before this pass reserves: the reserve refuses the
// decided gate (already_consumed), no window re-ask is stored, and the
// approval stays good for every consumer.
func TestA13NoWindowReaskAfterAnApproval(t *testing.T) {
	f := newFixture(t)
	target := gatetarget.MergeBeyond("o/r", 17, strings.Repeat("e", 40), []string{"protected"})
	rec, q := f.raise("merge-w17", "spawn", target)
	_, _, na, _ := rec.Window()
	inside := f.answer(f.owner, tuple("approve", rec), q.EventID, na.Add(-time.Minute))
	late := na.Add(2 * time.Minute)
	f.clock = late
	fired := false
	afterWindowRead = func(id string) {
		if id == rec.GateID && !fired {
			fired = true
			f.clock = na.Add(-30 * time.Second)
			if r := f.core.HandleEvent(ctx, inside); r.Outcome != operatorauth.DecisionApproved {
				t.Errorf("the approval inside the window: %+v", r)
			}
			f.clock = late
		}
	}
	defer func() { afterWindowRead = func(string) {} }()
	r := f.core.HandleEvent(ctx, f.answer(f.owner, tuple("approve", rec), q.EventID, late))
	if !fired || r.Outcome != "rejected" || r.Code != RejectReplayed {
		t.Fatalf("the late answer after another handler's approval: %+v (fired %v)", r, fired)
	}
	if w, _ := f.store.ReadWindow(rec.GateID); w != nil {
		t.Fatalf("a window re-ask on a decided gate: %+v", w)
	}
	if err := f.verifier().Accepted(f.store, rec); err != nil {
		t.Fatalf("Verifier.Accepted: %v", err)
	}
	if id, ok := a13Train(f).Accepted("spawn", target); !ok || id != rec.GateID {
		t.Fatalf("the train's lookup: %q %v", id, ok)
	}
}
