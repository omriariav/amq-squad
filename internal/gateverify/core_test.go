package gateverify

import (
	"context"
	"encoding/hex"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/btcsuite/btcd/btcec/v2"

	"github.com/omriariav/amq-squad/v3/internal/gatestore"
	"github.com/omriariav/amq-squad/v3/internal/nostr"
	"github.com/omriariav/amq-squad/v3/internal/operatorauth"
)

var ctx = context.Background()

// Acceptance 2: each of the four classes through the core, on synthetic
// keys: approve, deny, replay, wrong digest, answer for another gate, wrong
// key, forged signature.
func TestFourClassesThroughTheCore(t *testing.T) {
	for _, c := range classes {
		t.Run(c.action, func(t *testing.T) {
			f := newFixture(t)
			rec, q := f.raise("g-"+strings.ReplaceAll(c.action, "_", "-"), c.action, c.target)
			at := testNow.Add(time.Minute)
			f.clock = at.Add(time.Second)

			// wrong digest prefix, a tuple for another gate, wrong key, forged signature
			other := rec
			other.GateID = "g-other"
			bad := strings.Replace(tuple("approve", rec), prefixOf(rec), "0000000000000000", 1)
			for name, want := range map[string]struct {
				raw  []byte
				code string
			}{
				"wrong digest":     {f.answer(f.owner, bad, q.EventID, at), RejectDigest},
				"wrong key":        {f.answer(f.seat, tuple("approve", rec), q.EventID, at), RejectWrongPubkey},
				"body key":         {f.answer(f.body, tuple("approve", rec), q.EventID, at), RejectWrongPubkey},
				"forged signature": {forge(t, f.answer(f.owner, tuple("approve", rec), q.EventID, at)), RejectBadSignature},
			} {
				if r := f.core.HandleEvent(ctx, want.raw); r.Outcome != "rejected" || r.Code != want.code {
					t.Errorf("%s: %+v, want %s", name, r, want.code)
				}
			}
			// an answer naming this gate, verified against another gate's record
			if _, err := Verify(f.answer(f.owner, tuple("approve", rec), q.EventID, at), mustRender(t, f, "g-other", c.action, c.target), &q, nil, f.core.Pin, f.clock); Code(err) != RejectGateMismatch {
				t.Errorf("another gate: %v, want gate_mismatch", err)
			}

			ok := f.answer(f.owner, tuple("approve", rec), q.EventID, at)
			r := f.core.HandleEvent(ctx, ok)
			if r.Outcome != operatorauth.DecisionApproved || r.AuthorizationID == "" {
				t.Fatalf("approve: %+v", r)
			}
			ans, err := f.store.ReadAnswer(rec.GateID)
			if err != nil || ans == nil {
				t.Fatal(err)
			}
			if err := VerifyAnswerFile(*ans, f.trust, f.core.Pin, rec, &q, nil, f.core.Namespace); err != nil {
				t.Fatalf("answer file does not verify: %v", err)
			}
			if err := operatorauth.VerifyAuthorizationEnvelope(ans.Envelope, f.trust); err != nil || ans.Envelope.Payload.Action != c.action || ans.Envelope.Payload.Target != rec.Digest {
				t.Fatalf("envelope: %v %+v", err, ans.Envelope.Payload)
			}
			// replay: the same event, and a later deny
			if r := f.core.HandleEvent(ctx, ok); r.Code != RejectReplayed {
				t.Errorf("replay: %+v", r)
			}
			if r := f.core.HandleEvent(ctx, f.answer(f.owner, tuple("deny", rec), q.EventID, at)); r.Code != RejectReplayed {
				t.Errorf("deny after approve: %+v", r)
			}

			// deny on a second gate of the class
			rec2, q2 := f.raise(rec.GateID+"-2", c.action, c.target+"x")
			r = f.core.HandleEvent(ctx, f.answer(f.owner, tuple("deny", rec2), q2.EventID, at))
			if r.Outcome != operatorauth.DecisionDenied {
				t.Fatalf("deny: %+v", r)
			}
			den, _ := f.store.ReadDenial(rec2.GateID)
			if den == nil || operatorauth.VerifyDenialEnvelope(den.Envelope, f.trust) != nil {
				t.Fatalf("deny record does not verify: %+v", den)
			}
			if a, _ := f.store.ReadAnswer(rec2.GateID); a != nil {
				t.Fatal("a deny wrote an approval")
			}
			if r := f.core.HandleEvent(ctx, f.answer(f.owner, tuple("approve", rec2), q2.EventID, at.Add(time.Second))); r.Code != RejectReplayed {
				t.Errorf("approve after deny: %+v", r)
			}
		})
	}
}

func mustRender(t *testing.T, f *fixture, id, action, target string) gatestore.Record {
	t.Helper()
	r, err := gatestore.Render(gatestore.Spec{GateID: id, Action: action, Target: target, RaisedBy: "lead", Squad: f.core.Store.Squad()}, testNow, f.core.Identity())
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// forge keeps id and content and replaces the signature with another valid
// signature over another message.
func forge(t *testing.T, raw []byte) []byte {
	e, err := nostr.ParseEvent(raw)
	if err != nil {
		t.Fatal(err)
	}
	other := mustKey(t)
	decoy := nostr.Event{CreatedAt: e.CreatedAt, Kind: 9, Tags: [][]string{}, Content: "decoy"}
	_ = other.Sign(&decoy)
	e.Sig = decoy.Sig
	return e.Marshal()
}

// Acceptance 3: the plan 7.3 conformance rules, each through the core.
func TestConformanceRules(t *testing.T) {
	f := newFixture(t)
	rec, q := f.raise("g1", "tag", "git tag v1")
	at := testNow.Add(time.Minute)
	f.clock = at
	good := string(f.answer(f.owner, tuple("approve", rec), q.EventID, at))
	e, _ := nostr.ParseEvent([]byte(good))
	sig, _ := hex.DecodeString(e.Sig)
	sEqN := e
	sEqN.Sig = hex.EncodeToString(append(append([]byte(nil), sig[:32]...), btcec.S256().N.FillBytes(make([]byte, 32))...))
	sPlus := e
	sPlus.Sig = hex.EncodeToString(append(append([]byte(nil), sig[:32]...), new(big.Int).Add(btcec.S256().N, big.NewInt(5)).FillBytes(make([]byte, 32))...))
	cases := []struct {
		name string
		raw  []byte
		code string
	}{
		{"s == n", sEqN.Marshal(), RejectBadSignature},
		{"s > n", sPlus.Marshal(), RejectBadSignature},
		{"invalid UTF-8", []byte(strings.Replace(good, `"content":"approve`, "\"content\":\"\xffapprove", 1)), RejectMalformed},
		{"duplicate key", []byte(strings.Replace(good, `,"sig":`, `,"content":"deny g1 release `+prefixOf(rec)+`","sig":`, 1)), RejectMalformed},
		{"null tag element", []byte(strings.Replace(good, `["h","`+testChannel+`"]`, `["h",null]`, 1)), RejectMalformed},
		{"LF in the answer", f.answer(f.owner, tuple("approve", rec)+"\n", q.EventID, at), RejectBadTuple},
		{"CR in the answer", f.answer(f.owner, "\r"+tuple("approve", rec), q.EventID, at), RejectBadTuple},
		{"LF inside the tuple", f.answer(f.owner, strings.Replace(tuple("approve", rec), " release", "\nrelease", 1), q.EventID, at), RejectBadTuple},
		{"created 61 s in the future", f.answer(f.owner, tuple("approve", rec), q.EventID, at.Add(61*time.Second)), RejectFuture},
	}
	for _, c := range cases {
		if r := f.core.HandleEvent(ctx, c.raw); r.Outcome != "rejected" || r.Code != c.code {
			t.Errorf("%s: %+v, want %s", c.name, r, c.code)
		}
	}
	// the window edges: created_at - 60 s <= now <= not_after
	edge := f.answer(f.owner, tuple("approve", rec), q.EventID, at.Add(60*time.Second))
	if _, err := Verify(edge, rec, &q, nil, f.core.Pin, at); err != nil {
		t.Fatalf("created exactly 60 s ahead: %v", err)
	}
	_, _, na, _ := rec.Window()
	if _, err := Verify(f.answer(f.owner, tuple("approve", rec), q.EventID, at), rec, &q, nil, f.core.Pin, na.Add(time.Second)); Code(err) != RejectWindow {
		t.Fatalf("presented past not_after: %v", err)
	}
	if _, err := Verify(f.answer(f.owner, tuple("approve", rec), q.EventID, testNow.Add(-61*time.Second)), rec, &q, nil, f.core.Pin, at); Code(err) != RejectWindow {
		t.Fatalf("created before not_before: %v", err)
	}
	// nothing was consumed by any refusal
	if st := f.store.State(rec, at); st != gatestore.StateOpen {
		t.Fatalf("state after refusals = %s", st)
	}
	// and the answer file is written before the consumed mark
	if r := f.core.HandleEvent(ctx, []byte(good)); r.Outcome != operatorauth.DecisionApproved {
		t.Fatalf("good: %+v", r)
	}
	ans, _ := os.Stat(f.store.Dir("answers", "g1.json"))
	mark, _ := os.Stat(f.store.Dir("consumed", "gates", "g1"))
	if ans == nil || mark == nil || ans.ModTime().After(mark.ModTime()) {
		t.Fatalf("answer %v mark %v", ans, mark)
	}
}

// Acceptance 4: a non-thread answer that matches an open gate gets one
// re-ask in the gate thread and is not accepted; the next thread reply is.
// The two answers have the shapes of the S1 owner run: g-owner-01 carried
// only h and p tags, g-owner-02 the Buzz thread reply tag
// ["e", <root>, "", "reply"].
func TestThreadReask(t *testing.T) {
	f := newFixture(t)
	rec, q := f.raise("g-owner-01", "tag", "git tag v3.0.0")
	at := testNow.Add(time.Minute)
	f.clock = at
	plain := f.answer(f.owner, tuple("approve", rec), "", at)
	r := f.core.HandleEvent(ctx, plain)
	if r.Outcome != "reasked" || r.Code != RejectThread || r.ReaskEventID == "" {
		t.Fatalf("plain DM answer: %+v", r)
	}
	if st := f.store.State(rec, at); st != gatestore.StateOpen {
		t.Fatalf("the plain answer was accepted: %s", st)
	}
	reask, _ := f.store.ReadReask(rec.GateID)
	re, _ := nostr.ParseEvent([]byte(reask.RawEvent))
	if re.PubKey != f.body.PublicHex() || !strings.Contains(re.Content, "Reply here to confirm") || !strings.Contains(re.Content, tuple("approve", rec)) {
		t.Fatalf("re-ask = %+v", re)
	}
	if refs := nostr.ThreadRefs(re); len(refs) != 1 || refs[0] != q.EventID {
		t.Fatalf("re-ask not in the gate thread: %v", refs)
	}
	if got, _ := f.relay.FetchByID(ctx, re.ID); got == nil {
		t.Fatal("re-ask not published")
	}
	// a second plain answer gets no second re-ask and is refused
	r = f.core.HandleEvent(ctx, f.answer(f.owner, tuple("approve", rec), "", at.Add(time.Second)))
	if r.Outcome != "rejected" || r.Code != RejectThread || r.ReaskEventID != "" {
		t.Fatalf("second plain answer: %+v", r)
	}
	// the thread reply (to the question, as g-owner-02) is the answer
	r = f.core.HandleEvent(ctx, f.answer(f.owner, tuple("approve", rec), q.EventID, at.Add(2*time.Second)))
	if r.Outcome != operatorauth.DecisionApproved {
		t.Fatalf("thread reply: %+v", r)
	}
	// a reply to the re-ask in the same thread also counts (second gate)
	rec2, _ := f.raise("g-owner-02", "tag", "git tag v3.0.1")
	if r := f.core.HandleEvent(ctx, f.answer(f.owner, tuple("approve", rec2), "", at)); r.Outcome != "reasked" {
		t.Fatalf("g-owner-02 plain: %+v", r)
	}
	re2, _ := f.store.ReadReask(rec2.GateID)
	if r := f.core.HandleEvent(ctx, f.answer(f.owner, tuple("approve", rec2), re2.EventID, at.Add(time.Second))); r.Outcome != operatorauth.DecisionApproved {
		t.Fatalf("reply to the re-ask: %+v", r)
	}
	// a non-thread answer that does not match the gate gets no re-ask
	rec3, _ := f.raise("g3", "tag", "git tag v4")
	if r := f.core.HandleEvent(ctx, f.answer(f.owner, strings.Replace(tuple("approve", rec3), prefixOf(rec3), "ffffffffffffffff", 1), "", at)); r.Outcome != "rejected" || r.Code != RejectDigest {
		t.Fatalf("non-matching plain answer: %+v", r)
	}
	if r, _ := f.store.ReadReask("g3"); r != nil {
		t.Fatal("a non-matching answer was re-asked")
	}
	// a seat's plain message naming the gate gets no re-ask either
	if r := f.core.HandleEvent(ctx, f.answer(f.seat, tuple("approve", rec3), "", at)); r.Code != RejectWrongPubkey {
		t.Fatalf("seat plain answer: %+v", r)
	}
	if r, _ := f.store.ReadReask("g3"); r != nil {
		t.Fatal("a seat's message was re-asked")
	}
}

// The real Buzz events of the S1 owner run parse and verify with this
// package unchanged: g-owner-01 has no thread reference (the re-ask case),
// g-owner-02 carries the Buzz thread reply tag.
func TestS1OwnerRunEventsParse(t *testing.T) {
	dir := filepath.Join("..", "..", "docs", "v3-spike", "S1-evidence", "realkey")
	for name, wantRefs := range map[string]int{"g-owner-01-answer-event.json": 0, "g-owner-02-answer-event.json": 1} {
		raw, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		e, err := nostr.ParseEvent(raw)
		if err != nil || !e.CheckID() || e.VerifySignature() != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if refs := nostr.ThreadRefs(e); len(refs) != wantRefs {
			t.Fatalf("%s: thread refs %v", name, refs)
		}
		if a, err := gatestore.ParseAnswer(e.Content); err == nil {
			t.Fatalf("%s: the S1 merge class parsed as a typed class: %+v", name, a)
		}
	}
}

// Acceptance 6: acceptance is final; a dot-led gate id verifies end to end.
func TestAcceptanceFinalAndDotLedID(t *testing.T) {
	f := newFixture(t)
	rec, q := f.raise(".g-dot", "tag", "git tag v1")
	at := testNow.Add(time.Minute)
	f.clock = at
	if r := f.core.HandleEvent(ctx, f.answer(f.owner, tuple("approve", rec), q.EventID, at)); r.Outcome != operatorauth.DecisionApproved {
		t.Fatalf("dot-led approve: %+v", r)
	}
	_, _, na, _ := rec.Window()
	if st := f.store.State(rec, na.Add(48*time.Hour)); st != gatestore.StateAccepted {
		t.Fatalf("state past not_after = %s", st)
	}
	ans, _ := f.store.ReadAnswer(rec.GateID)
	if err := VerifyAnswerFile(*ans, f.trust, f.core.Pin, rec, &q, nil, f.core.Namespace); err != nil {
		t.Fatalf("answer file past not_after: %v", err)
	}
}

func TestHistoryCheck(t *testing.T) {
	cases := map[string]struct {
		mutate func(f *fixture, q nostr.Event)
		code   string
	}{
		"edit with other text": {func(f *fixture, q nostr.Event) {
			f.relay.inject(f.signed(f.body, KindEdit, "approve something else", [][]string{{"e", q.ID}}, q.CreatedAt+1))
		}, RejectAltered},
		"edit by the owner with other text": {func(f *fixture, q nostr.Event) {
			f.relay.inject(f.signed(f.owner, KindEdit, "x", [][]string{{"e", q.ID}}, q.CreatedAt+1))
		}, RejectAltered},
		"question deleted": {func(f *fixture, q nostr.Event) {
			f.relay.inject(f.signed(f.seat, KindDeletion, "", [][]string{{"e", q.ID}}, q.CreatedAt+1))
		}, RejectAltered},
		"group deletion by the body": {func(f *fixture, q nostr.Event) {
			f.relay.inject(f.signed(f.body, KindGroupDeletion, "", [][]string{{"e", strings.Repeat("9", 64)}}, 1))
		}, RejectAltered},
		"owner deletion since the question": {func(f *fixture, q nostr.Event) {
			f.relay.inject(f.signed(f.owner, KindDeletion, "", [][]string{{"e", strings.Repeat("8", 64)}}, q.CreatedAt+2))
		}, RejectAltered},
		"history read fails": {func(f *fixture, q nostr.Event) { f.relay.failQuery = true }, RejectHistory},
		"unchanged edit and an old owner deletion": {func(f *fixture, q nostr.Event) {
			f.relay.inject(f.signed(f.body, KindEdit, q.Content, [][]string{{"e", q.ID}}, q.CreatedAt+1))
			f.relay.inject(f.signed(f.owner, KindDeletion, "", [][]string{{"e", strings.Repeat("7", 64)}}, q.CreatedAt-100))
		}, ""},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			rec, q := f.raise("g1", "tag", "git tag v1")
			qe, _ := nostr.ParseEvent([]byte(q.RawEvent))
			c.mutate(f, qe)
			at := testNow.Add(time.Minute)
			f.clock = at
			r := f.core.HandleEvent(ctx, f.answer(f.owner, tuple("approve", rec), q.EventID, at))
			if c.code == "" {
				if r.Outcome != operatorauth.DecisionApproved {
					t.Fatalf("clean history refused: %+v", r)
				}
				return
			}
			if r.Code != c.code {
				t.Fatalf("%+v, want %s", r, c.code)
			}
			if st := f.store.State(rec, at); st != gatestore.StateOpen {
				t.Fatalf("an altered gate was consumed: %s", st)
			}
		})
	}
}

func (f *fixture) signed(k nostr.SecretKey, kind int, content string, tags [][]string, at int64) []byte {
	f.t.Helper()
	e := nostr.Event{CreatedAt: at, Kind: kind, Tags: append([][]string{{"h", testChannel}}, tags...), Content: content}
	if err := k.Sign(&e); err != nil {
		f.t.Fatal(err)
	}
	return e.Marshal()
}

func TestInsecureGrantsRefuseApprovalsNotDenials(t *testing.T) {
	f := newFixture(t)
	f.core.Insecure = true
	rec, q := f.raise("g1", "tag", "git tag v1")
	at := testNow.Add(time.Minute)
	f.clock = at
	if r := f.core.HandleEvent(ctx, f.answer(f.owner, tuple("approve", rec), q.EventID, at)); r.Code != RejectInsecure {
		t.Fatalf("approve under insecure grants: %+v", r)
	}
	if st := f.store.State(rec, at); st != gatestore.StateOpen {
		t.Fatalf("state = %s", st)
	}
	if r := f.core.HandleEvent(ctx, f.answer(f.owner, tuple("deny", rec), q.EventID, at)); r.Outcome != operatorauth.DecisionDenied {
		t.Fatalf("deny under insecure grants: %+v", r)
	}
}

// Concurrent approve and deny events for one gate: exactly one wins.
func TestConcurrentAnswers(t *testing.T) {
	f := newFixture(t)
	at := testNow.Add(time.Minute)
	f.clock = at
	for round := 0; round < 10; round++ {
		rec, q := f.raise("r"+string(rune('a'+round)), "tag", "git tag v"+string(rune('a'+round)))
		raws := [][]byte{
			f.answer(f.owner, tuple("approve", rec), q.EventID, at),
			f.answer(f.owner, tuple("approve", rec), q.EventID, at.Add(time.Second)),
			f.answer(f.owner, tuple("deny", rec), q.EventID, at),
		}
		var wg sync.WaitGroup
		results := make([]Result, len(raws))
		for i, raw := range raws {
			wg.Add(1)
			go func(i int, raw []byte) { defer wg.Done(); results[i] = f.core.HandleEvent(ctx, raw) }(i, raw)
		}
		wg.Wait()
		won := 0
		for _, r := range results {
			if r.Outcome == operatorauth.DecisionApproved || r.Outcome == operatorauth.DecisionDenied {
				won++
			} else if r.Code != RejectReplayed {
				t.Fatalf("round %d: %+v", round, r)
			}
		}
		if won != 1 {
			t.Fatalf("round %d: %d winners %+v", round, won, results)
		}
	}
}

// VerifyAnswerFile does not rest on the issuer key: a trusted issuer that
// mislabels the action, the target or the event is refused.
func TestVerifyAnswerFileTamper(t *testing.T) {
	f := newFixture(t)
	rec, q := f.raise("g1", "tag", "git tag v1")
	at := testNow.Add(time.Minute)
	f.clock = at
	raw := f.answer(f.owner, tuple("approve", rec), q.EventID, at)
	o, err := Verify(raw, rec, &q, nil, f.core.Pin, at)
	if err != nil {
		t.Fatal(err)
	}
	good, err := BuildAnswerFile(o, raw, rec, q, f.core.Pin, "wss://relay.test", f.core.Namespace, "test", f.issuer, at)
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyAnswerFile(good, f.trust, f.core.Pin, rec, &q, nil, f.core.Namespace); err != nil {
		t.Fatal(err)
	}
	resign := func(mutate func(*operatorauth.AuthorizationPayload)) gatestore.AnswerFile {
		p := good.Envelope.Payload
		mutate(&p)
		env, err := operatorauth.NewAuthorizationEnvelope(p, f.issuer)
		if err != nil {
			t.Fatal(err)
		}
		out := good
		out.Envelope = env
		return out
	}
	otherRaw := f.answer(f.owner, tuple("approve", rec), q.EventID, at.Add(time.Second))
	cases := map[string]gatestore.AnswerFile{
		"other action":   resign(func(p *operatorauth.AuthorizationPayload) { p.Action, p.GateKind = "github_release", "release" }),
		"other target":   resign(func(p *operatorauth.AuthorizationPayload) { p.Target = "sha256:" + strings.Repeat("0", 64) }),
		"other question": resign(func(p *operatorauth.AuthorizationPayload) { p.QuestionMessageID = "nostr:" + strings.Repeat("1", 64) }),
		"raw swapped": func() gatestore.AnswerFile {
			out := good
			out.Evidence.RawEvent = string(otherRaw)
			return out
		}(),
		"record copy edited": func() gatestore.AnswerFile {
			out := good
			out.Gate.RaisedBy = "seat"
			return out
		}(),
	}
	for name, file := range cases {
		if err := VerifyAnswerFile(file, f.trust, f.core.Pin, rec, &q, nil, f.core.Namespace); err == nil {
			t.Errorf("%s: verified", name)
		}
	}
	// a seat-signed "approve" can never become an envelope: Verify refuses it first
	if _, err := Verify(f.answer(f.seat, tuple("approve", rec), q.EventID, at), rec, &q, nil, f.core.Pin, at); Code(err) != RejectWrongPubkey {
		t.Fatalf("seat approve: %v", err)
	}
	// another issuer key is refused by the record
	other, _ := newIssuer(t, t.TempDir())
	if _, err := BuildAnswerFile(o, raw, rec, q, f.core.Pin, "", f.core.Namespace, "test", other, at); err == nil {
		t.Fatal("an issuer the record does not name built an answer")
	}
}
