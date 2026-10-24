package gateverify

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/gatestore"
	"github.com/omriariav/amq-squad/v3/internal/gatetarget"
	"github.com/omriariav/amq-squad/v3/internal/grants"
	"github.com/omriariav/amq-squad/v3/internal/nostr"
	"github.com/omriariav/amq-squad/v3/internal/operatorauth"
)

// Rows for the Codex review of 45ad7409, one per finding handled here.

func (f *fixture) verifier() Verifier {
	return Verifier{Trust: f.trust, Pin: f.core.Pin, Namespace: f.core.Namespace}
}

// Finding 1: growth needs the owner's signature, not just a trusted issuer
// envelope.
func TestReview1GrowthRequiresOwnerSignature(t *testing.T) {
	f := newFixture(t)
	at := testNow.Add(time.Minute)
	f.clock = at
	r := grants.Resolved{Grants: grants.Defaults()}
	r.MaxSeats = 2
	target := gatetarget.Grow("/r/s1", 3)
	rec, q := f.raise("grow-3", "spawn", target)
	// a trusted issuer signs an approval envelope with no owner event behind it
	ts := at.Format(time.RFC3339Nano)
	env, err := operatorauth.NewAuthorizationEnvelope(operatorauth.AuthorizationPayload{
		Namespace: f.core.Namespace.binding(), Gate: "gate/grow-3", Thread: "gate/grow-3", GateKind: rec.GateKind, Action: rec.Class, Target: rec.Digest,
		Note: expectedNote(rec), QuestionMessageID: "nostr:" + q.EventID, AnswerMessageID: "nostr:" + strings.Repeat("ab", 32), QuestionCreatedAt: ts, AnswerCreatedAt: ts, VerifiedAt: ts,
		Actor:    operatorauth.AuthorizationActor{Role: "operator", Handle: "owner", Source: "human"},
		Issuer:   operatorauth.AuthorizationIssuer{Binary: IssuerBinary, Version: "test"},
		Evidence: []operatorauth.AuthorizationEvidence{{Kind: EvidenceKindGate, Identity: "gate/grow-3", SHA256: rec.TextSHA256}},
	}, f.issuer)
	if err != nil {
		t.Fatal(err)
	}
	qq := q
	if err := f.store.Commit(gatestore.AnswerFile{Schema: gatestore.AnswerSchema, Scheme: SchemeBIP340, Decision: operatorauth.DecisionApproved, Envelope: env,
		Evidence: gatestore.Evidence{Scheme: SchemeBIP340, EventID: strings.Repeat("ab", 32)}, Gate: rec, Question: &qq}); err != nil {
		t.Fatal(err)
	}
	if d := f.verifier().SeatRequest(f.store, r, "/r/s1", 3); d.Allowed {
		t.Fatalf("an issuer-only envelope opened growth: %+v", d)
	}
	// the owner's signed answer through the core opens exactly that seat
	rec4, q4 := f.raise("grow-4", "spawn", gatetarget.Grow("/r/s1", 4))
	if res := f.core.HandleEvent(ctx, f.answer(f.owner, tuple("approve", rec4), q4.EventID, at)); res.Outcome != operatorauth.DecisionApproved {
		t.Fatalf("owner approve: %+v", res)
	}
	if d := f.verifier().SeatRequest(f.store, r, "/r/s1", 4); !d.Allowed || d.GateID != "grow-4" {
		t.Fatalf("owner-signed growth: %+v", d)
	}
	if d := f.verifier().SeatRequest(f.store, r, "/r/s1", 5); d.Allowed {
		t.Fatalf("seat 5 opened by the seat-4 gate: %+v", d)
	}
	if d := f.verifier().LimitRequest(f.store, r, "/r/s1", "files", 40, false); d.Allowed || d.Target != "grow /r/s1 files=40" {
		t.Fatalf("widening past a limit: %+v", d)
	}
	insecure := r
	insecure.Insecure = true
	if d := f.verifier().SeatRequest(f.store, insecure, "/r/s1", 4); d.Allowed || d.Reason != grants.ReasonInsecure {
		t.Fatalf("insecure grants: %+v", d)
	}
}

func copyTree(t *testing.T, from, to string) {
	t.Helper()
	err := filepath.Walk(from, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(from, p)
		dst := filepath.Join(to, rel)
		if info.IsDir() {
			return os.MkdirAll(dst, 0o700)
		}
		raw, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(dst, raw, 0o600)
	})
	if err != nil {
		t.Fatal(err)
	}
}

// Finding 2: an approval binds its namespace. A's whole gate store copied into
// session B, with the same owner binding, never counts for B, and a verifier
// with another project, profile, session or generation refuses it.
func TestReview2ApprovalDoesNotReplayAcrossNamespaces(t *testing.T) {
	f := newFixture(t)
	at := testNow.Add(time.Minute)
	f.clock = at
	rec, q := f.raise("tag-v1", "tag", "git tag v1")
	if r := f.core.HandleEvent(ctx, f.answer(f.owner, tuple("approve", rec), q.EventID, at)); r.Outcome != operatorauth.DecisionApproved {
		t.Fatalf("approve: %+v", r)
	}
	if err := f.verifier().Accepted(f.store, rec); err != nil {
		t.Fatalf("own namespace: %v", err)
	}
	other := f.store.Root + "-b"
	copyTree(t, f.store.Dir(), filepath.Join(other, "extensions", "amq-squad"))
	storeB, _ := gatestore.Open(other)
	nsB := f.core.Namespace
	nsB.Session = "s2"
	vb := f.verifier()
	vb.Namespace = nsB
	if err := vb.Accepted(storeB, rec); err == nil {
		t.Fatal("session B accepted session A's approval")
	}
	for name, mutate := range map[string]func(*Namespace){
		"project":    func(n *Namespace) { n.ProjectDir = "/other" },
		"profile":    func(n *Namespace) { n.Profile = "release" },
		"generation": func(n *Namespace) { n.Generation = "g2" },
	} {
		v := f.verifier()
		mutate(&v.Namespace)
		if err := v.Accepted(f.store, rec); err == nil {
			t.Errorf("%s: accepted under another namespace", name)
		}
	}
}

// Finding 4: every reader uses the recovered decision; an approval file next
// to a deny record (a crash under older code) is never accepted.
func TestReview4VerifierUsesTheRecoveredDecision(t *testing.T) {
	f := newFixture(t)
	at := testNow.Add(time.Minute)
	f.clock = at
	rec, q := f.raise("g1", "tag", "git tag v1")
	if r := f.core.HandleEvent(ctx, f.answer(f.owner, tuple("approve", rec), q.EventID, at)); r.Outcome != operatorauth.DecisionApproved {
		t.Fatalf("approve: %+v", r)
	}
	raw, _ := os.ReadFile(f.store.Dir("answers", "g1.json"))
	if err := os.MkdirAll(f.store.Dir("denials"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.store.Dir("denials", "g1.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := f.verifier().Accepted(f.store, rec); err == nil {
		t.Fatal("an approval beside a deny record verified")
	}
	if st := f.store.State(rec, at); st != gatestore.StateDenied {
		t.Fatalf("state = %s", st)
	}
}

// Finding 5: one thread reference outside the gate's thread is a conflict,
// whatever the other tags say.
func TestReview5MixedThreadReferencesAreRejected(t *testing.T) {
	f := newFixture(t)
	at := testNow.Add(time.Minute)
	f.clock = at
	recA, qA := f.raise("g-a", "tag", "git tag v1")
	_, qB := f.raise("g-b", "tag", "git tag v2")
	for name, tags := range map[string][][]string{
		"root B, unmarked A":  {{"e", qB.EventID, "", "root"}, {"e", qA.EventID}},
		"reply A, root B":     {{"e", qA.EventID, "", "reply"}, {"e", qB.EventID, "", "root"}},
		"unmarked A and B":    {{"e", qA.EventID}, {"e", qB.EventID}},
		"reply A, unmarked B": {{"e", qA.EventID, "", "reply"}, {"e", qB.EventID}},
	} {
		e := nostr.Event{CreatedAt: at.Unix(), Kind: 9, Tags: append([][]string{{"h", testChannel}}, tags...), Content: tuple("approve", recA)}
		if err := f.owner.Sign(&e); err != nil {
			t.Fatal(err)
		}
		if r := f.core.HandleEvent(ctx, e.Marshal()); r.Code != RejectWrongThread {
			t.Errorf("%s: %+v", name, r)
		}
	}
	if st := f.store.State(recA, at); st != gatestore.StateOpen {
		t.Fatalf("state = %s", st)
	}
	// the gate's own thread, root and reply both naming A, is accepted
	ok := nostr.Event{CreatedAt: at.Unix(), Kind: 9, Tags: [][]string{{"h", testChannel}, {"e", qA.EventID, "", "root"}, {"e", qA.EventID, "", "reply"}}, Content: tuple("approve", recA)}
	_ = f.owner.Sign(&ok)
	if r := f.core.HandleEvent(ctx, ok.Marshal()); r.Outcome != operatorauth.DecisionApproved {
		t.Fatalf("own thread: %+v", r)
	}
}

// failingRelay fails the next n publishes.
type failingRelay struct {
	*memRelay
	fail int
}

func (r *failingRelay) Publish(ctx context.Context, raw []byte) error {
	if r.fail > 0 {
		r.fail--
		return errors.New("relay unavailable")
	}
	return r.memRelay.Publish(ctx, raw)
}

// Finding 7: the re-ask is reserved before it is published; a failed
// publication is retried with the same event, never a second re-ask.
func TestReview7ReaskPublishedOnceAcrossRetries(t *testing.T) {
	f := newFixture(t)
	at := testNow.Add(time.Minute)
	f.clock = at
	rec, _ := f.raise("g1", "tag", "git tag v1")
	fr := &failingRelay{memRelay: f.relay, fail: 1}
	f.core.Relay = fr
	first := f.core.HandleEvent(ctx, f.answer(f.owner, tuple("approve", rec), "", at))
	if first.Outcome != "rejected" || !strings.Contains(first.Detail, "reserved") {
		t.Fatalf("publish failure: %+v", first)
	}
	reserved, _ := f.store.ReadReask("g1")
	if reserved == nil || f.store.ReaskPublished("g1") {
		t.Fatalf("reservation = %+v published=%v", reserved, f.store.ReaskPublished("g1"))
	}
	second := f.core.HandleEvent(ctx, f.answer(f.owner, tuple("approve", rec), "", at.Add(time.Second)))
	if second.Outcome != "reasked" || second.ReaskEventID != reserved.EventID {
		t.Fatalf("retry: %+v (reserved %s)", second, reserved.EventID)
	}
	if got, _ := f.relay.FetchByID(ctx, reserved.EventID); got == nil {
		t.Fatal("the reserved re-ask was not published")
	}
	third := f.core.HandleEvent(ctx, f.answer(f.owner, tuple("approve", rec), "", at.Add(2*time.Second)))
	if third.Outcome != "rejected" || third.Code != RejectThread || third.ReaskEventID != "" {
		t.Fatalf("after publication: %+v", third)
	}
	n := 0
	for _, raw := range f.relay.stored {
		if e, err := nostr.ParseEvent(raw); err == nil && strings.Contains(e.Content, "Reply here to confirm") {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("%d re-asks on the relay", n)
	}
}

// Second review, 4c: a crash after the gate mark and before the event mark
// is repaired by the core before its replay check, not left forever.
func TestReview4cHalfMarkedDecisionIsRepaired(t *testing.T) {
	f := newFixture(t)
	at := testNow.Add(time.Minute)
	f.clock = at
	rec, q := f.raise("g1", "tag", "git tag v1")
	ok := f.answer(f.owner, tuple("approve", rec), q.EventID, at)
	if r := f.core.HandleEvent(ctx, ok); r.Outcome != operatorauth.DecisionApproved {
		t.Fatalf("approve: %+v", r)
	}
	e, _ := nostr.ParseEvent(ok)
	eventMark := f.store.Dir("consumed", "events", e.ID)
	if err := os.Remove(eventMark); err != nil {
		t.Fatal(err)
	}
	// any later answer for the gate runs recovery first
	if r := f.core.HandleEvent(ctx, f.answer(f.owner, tuple("deny", rec), q.EventID, at.Add(time.Second))); r.Code != RejectReplayed {
		t.Fatalf("deny after the half-marked approval: %+v", r)
	}
	if _, err := os.Stat(eventMark); err != nil {
		t.Fatalf("the event mark was not repaired: %v", err)
	}
	// and the approval's own event is single-use again across gates
	f.raise("g2", "tag", "git tag v2")
	if err := f.store.Replayed("g2", e.ID); !errors.Is(err, gatestore.ErrConsumed) {
		t.Fatalf("the repaired event mark does not block reuse: %v", err)
	}
	if st := f.store.State(rec, at); st != gatestore.StateAccepted {
		t.Fatalf("state = %s", st)
	}
}
