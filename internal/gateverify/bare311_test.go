package gateverify

import (
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/gatestore"
	"github.com/omriariav/amq-squad/v3/internal/gatetarget"
	"github.com/omriariav/amq-squad/v3/internal/nostr"
	"github.com/omriariav/amq-squad/v3/internal/redact"
)

// Brief 3.1.1-1, row 2: a bare yes replied in the gate question's thread,
// signed by the pinned owner key, is accepted for that gate; a bare no
// refuses it; "Yes." and "yes" are the same; a bare yes outside every
// thread gets the plain re-ask in the one open gate's thread; a bare yes in
// another gate's thread is refused for this gate (the thread is the
// binding) and is that gate's answer.
func TestABareYesOrNoInTheThreadIsTheAnswer(t *testing.T) {
	at := testNow.Add(time.Minute)
	for _, c := range []struct {
		content, want string
	}{
		{"yes", "approved"}, {"Yes.", "approved"}, {"  YES! ", "approved"}, {"approve", "approved"},
		{"no", "denied"}, {"No.", "denied"}, {"deny", "denied"},
	} {
		f := newFixture(t)
		f.clock = at
		rec, q := f.raise("g-bare", "tag", "git tag v3.1.1")
		r := f.core.HandleEvent(ctx, f.answer(f.owner, c.content, q.EventID, at))
		if r.Outcome != c.want || r.GateID != rec.GateID {
			t.Fatalf("%q in the thread: %+v, want %s", c.content, r, c.want)
		}
		if c.want != "approved" {
			continue
		}
		ans, err := f.store.ReadAnswer(rec.GateID)
		if err != nil {
			t.Fatal(err)
		}
		// the consumer's full re-verification takes the bare answer too
		if err := VerifyAnswerFile(*ans, f.trust, f.core.Pin, rec, &q, nil, f.core.Namespace); err != nil {
			t.Fatalf("%q: the answer file does not verify: %v", c.content, err)
		}
	}
	// a bare yes signed by any key but the owner's is refused
	f := newFixture(t)
	f.clock = at
	_, q := f.raise("g-bare", "tag", "git tag v3.1.1")
	if r := f.core.HandleEvent(ctx, f.answer(f.seat, "yes", q.EventID, at)); r.Outcome != "rejected" || r.Code != RejectWrongPubkey {
		t.Fatalf("a seat's bare yes: %+v", r)
	}
	// a word that is not yes or no is no answer
	if r := f.core.HandleEvent(ctx, f.answer(f.owner, "yes please", q.EventID, at)); r.Outcome != "rejected" || r.Code != RejectBadTuple {
		t.Fatalf("yes please: %+v", r)
	}
}

func TestABareYesOutsideTheThreadGetsThePlainReask(t *testing.T) {
	f := newFixture(t)
	at := testNow.Add(time.Minute)
	f.clock = at
	rec, q := f.raise("g-out", "tag", "git tag v3.1.1")
	r := f.core.HandleEvent(ctx, f.answer(f.owner, "yes", "", at))
	if r.Outcome != "reasked" || r.Code != RejectThread || r.GateID != rec.GateID {
		t.Fatalf("a bare yes outside the thread: %+v", r)
	}
	if st := f.store.State(rec, at); st != gatestore.StateOpen {
		t.Fatalf("a bare yes outside the thread was taken: %s", st)
	}
	ra, _ := f.store.ReadReask(rec.GateID)
	re, _ := nostr.ParseEvent([]byte(ra.RawEvent))
	if above, _, _ := strings.Cut(re.Content, "\n\n"+gatestore.Rule+"\n"); above != "Reply yes or no to this message." {
		t.Fatalf("the re-ask above the rule = %q", above)
	}
	if refs := nostr.ThreadRefs(re); len(refs) != 1 || refs[0] != q.EventID {
		t.Fatalf("the re-ask is not in the gate thread: %v", refs)
	}
	// the bare yes replied to the re-ask is the answer
	if r := f.core.HandleEvent(ctx, f.answer(f.owner, "Yes", ra.EventID, at.Add(time.Second))); r.Outcome != "approved" || r.GateID != rec.GateID {
		t.Fatalf("a bare yes replied to the re-ask: %+v", r)
	}
	// with two open gates, a bare yes outside every thread names neither
	g := newFixture(t)
	g.clock = at
	g.raise("g-one", "tag", "git tag v1")
	g.raise("g-two", "tag", "git tag v2")
	if r := g.core.HandleEvent(ctx, g.answer(g.owner, "yes", "", at)); r.Outcome != "rejected" || r.Code != RejectThread || r.ReaskEventID != "" {
		t.Fatalf("a bare yes with two open gates: %+v", r)
	}
}

func TestABareYesInAnotherGatesThreadIsThatGatesNeverThisOnes(t *testing.T) {
	f := newFixture(t)
	at := testNow.Add(time.Minute)
	f.clock = at
	recA, qA := f.raise("g-a", "tag", "git tag v1")
	recB, qB := f.raise("g-b", "tag", "git tag v2")
	inB := f.answer(f.owner, "yes", qB.EventID, at)
	// the verifier refuses B's thread for A
	if _, err := Verify(inB, recA, &qA, nil, f.core.Pin, at); Code(err) != RejectWrongThread {
		t.Fatalf("a bare yes in B's thread verified for A: %v", err)
	}
	r := f.core.HandleEvent(ctx, inB)
	if r.Outcome != "approved" || r.GateID != recB.GateID {
		t.Fatalf("a bare yes in B's thread: %+v", r)
	}
	if st := f.store.State(recA, at); st != gatestore.StateOpen {
		t.Fatalf("gate A was decided by a yes in B's thread: %s", st)
	}
	// a bare yes naming both threads binds to neither
	e := nostr.Event{CreatedAt: at.Unix(), Kind: 9, Content: "yes", Tags: [][]string{{"h", testChannel}, {"e", qA.EventID, "", "root"}, {"e", qB.EventID, "", "reply"}}}
	if err := f.owner.Sign(&e); err != nil {
		t.Fatal(err)
	}
	if r := f.core.HandleEvent(ctx, e.Marshal()); r.Outcome != "rejected" || r.Code != RejectWrongThread {
		t.Fatalf("a bare yes naming two gates: %+v", r)
	}
}

// Row 3: the tuple form still verifies (core_test's rows are unchanged),
// and no re-ask the core composes contains "with one line" or a tuple.
func TestNoReaskAsksForATuple(t *testing.T) {
	f := newFixture(t)
	rec, _ := f.raise("g-tup", "tag", "git tag v1")
	tupleRe := regexp.MustCompile(`(?i)\b(approve|deny) \S+ (release|send|credential|grow) [0-9a-f]{16}`)
	a := rec.BareAnswer("approve")
	for name, text := range map[string]string{"re-ask": ReaskText(a), "history re-ask": HistoryReaskText(rec, a)} {
		if strings.Contains(text, "with one line") || tupleRe.MatchString(text) {
			t.Errorf("%s asks for a tuple:\n%s", name, text)
		}
		if !strings.Contains(text, "Reply yes or no to this message.") {
			t.Errorf("%s does not say how to answer:\n%s", name, text)
		}
	}
	// the typed tuple keeps verifying in the thread
	_, q := f.raise("g-tup2", "tag", "git tag v2")
	rec2, _ := f.store.Load("g-tup2")
	if r := f.core.HandleEvent(ctx, f.answer(f.owner, tuple("approve", rec2), q.EventID, testNow.Add(time.Minute))); r.Outcome != "approved" {
		t.Fatalf("the tuple form: %+v", r)
	}
}

// Codex re-check on #838 (a test gap): a bare yes replied to a question
// composed from a title carrying a canary verifies for its gate. The stored
// question is redacted; the verifier's bound fields are the record's own and
// unchanged: the target and its digest (of the raw target), the text's
// digest (of the redacted text the question carries), and the answer file's
// envelope binds them.
func TestABareYesVerifiesAgainstARedactedQuestion(t *testing.T) {
	const secret = "zebra311verifyvalue"
	canary := "GH_TOKEN=" + secret
	for _, re := range redact.RealCredentialShapes {
		if re.MatchString(canary) {
			t.Fatalf("the canary matches a real credential shape %s", re)
		}
	}
	f := newFixture(t)
	at := testNow.Add(time.Minute)
	f.clock = at
	target := gatetarget.MergeBeyond("omriariav/amq-squad", 7, "4852f761aa0b1c2d3e4f5a6b7c8d9e0f1a2b3c4d", []string{"ci_green"})
	rec, q, err := f.core.Raise(ctx, gatestore.Spec{GateID: "merge-canary", Action: "spawn", Target: target, RaisedBy: "lead", Facts: gatestore.SafeFacts("Fix "+canary, "v3")})
	if err != nil {
		t.Fatal(err)
	}
	qe, _ := nostr.ParseEvent([]byte(q.RawEvent))
	if strings.Contains(rec.Text, secret) || strings.Contains(qe.Content, secret) || strings.Contains(rec.Facts.Title, secret) || !strings.Contains(rec.Text, redact.Placeholder) {
		t.Fatalf("the canary reached the question:\n%s", qe.Content)
	}
	if rec.Target != target || rec.Digest != gatetarget.Digest("spawn", target) || qe.Content != rec.Text {
		t.Fatalf("the bound fields changed: target %q digest %s", rec.Target, rec.Digest)
	}
	r := f.core.HandleEvent(ctx, f.answer(f.owner, "yes", q.EventID, at))
	if r.Outcome != "approved" || r.GateID != rec.GateID {
		t.Fatalf("a bare yes to the redacted question: %+v", r)
	}
	ans, err := f.store.ReadAnswer(rec.GateID)
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyAnswerFile(*ans, f.trust, f.core.Pin, rec, &q, nil, f.core.Namespace); err != nil {
		t.Fatalf("the answer file does not verify: %v", err)
	}
	p := ans.Envelope.Payload
	if p.Target != rec.Digest || p.Action != "spawn" || p.Gate != "gate/"+rec.GateID {
		t.Fatalf("the envelope binds %s %s %s", p.Target, p.Action, p.Gate)
	}
	for _, ev := range p.Evidence {
		if ev.Kind == EvidenceKindGate && ev.SHA256 != rec.TextSHA256 {
			t.Fatalf("the gate-text evidence %s is not the record's %s", ev.SHA256, rec.TextSHA256)
		}
	}
}
