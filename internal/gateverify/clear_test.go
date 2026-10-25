package gateverify

import (
	"context"
	"fmt"
	"testing"

	"github.com/omriariav/amq-squad/v3/internal/gatestore"
	"github.com/omriariav/amq-squad/v3/internal/gatetarget"
)

// The Buzz-signed seat clear's rows (phase 5): a quarantine_clear gate is
// answered and verified as the four typed gates are, and a clear completes
// only for the handle, nonce and quarantine the request named.

const (
	clearInc = "t1@1761350000000000000"
	clearCfg = "sha256:" + "ab12ab12ab12ab12ab12ab12ab12ab12ab12ab12ab12ab12ab12ab12ab12ab12"
)

// clearRequest raises seat's clear request n (the gate, then the current
// request file) and returns the gate with its question.
func (f *fixture) clearRequest(seat string, n int) (gatestore.Record, gatestore.Question) {
	f.t.Helper()
	nonce := fmt.Sprintf("%032x", n)
	id := "clear-" + nonce
	rec, q := f.raise(id, "quarantine_clear", gatetarget.QuarantineClear(seat, nonce, clearInc, clearCfg))
	if err := f.store.PutClearRequest(gatestore.ClearRequest{Handle: seat, Nonce: nonce, GateID: id, Incarnation: clearInc, ConfigDigest: clearCfg, At: f.clock.UTC().Format("2006-01-02T15:04:05Z")}); err != nil {
		f.t.Fatal(err)
	}
	return rec, q
}

// approve has the owner answer gate rec in its thread and the core verify it.
func (f *fixture) approve(rec gatestore.Record, q gatestore.Question) string {
	f.t.Helper()
	res := f.core.HandleEvent(context.Background(), f.answer(f.owner, tuple("approve", rec), q.EventID, f.clock))
	if res.Outcome != "approved" {
		f.t.Fatalf("owner answer: %+v", res)
	}
	return res.EventID
}

func TestClearCompletesOnlyOnTheOwnersVerifiedAnswer(t *testing.T) {
	f := newFixture(t)
	rec, q := f.clearRequest("s1", 1)
	v := f.verifier()
	// no answer: the clear waits as an open gate
	if _, err := v.AuthorizeClear(f.store, "s1", rec.GateID, clearInc, clearCfg); Code(err) != RejectClearUnsigned {
		t.Fatalf("before any answer: %v", err)
	}
	// a seat's own key (not the owner's) never opens it
	if res := f.core.HandleEvent(context.Background(), f.answer(f.seat, tuple("approve", rec), q.EventID, f.clock)); res.Outcome != "rejected" {
		t.Fatalf("a non-owner answer: %+v", res)
	}
	if _, err := v.AuthorizeClear(f.store, "s1", rec.GateID, clearInc, clearCfg); Code(err) != RejectClearUnsigned {
		t.Fatalf("after a non-owner answer: %v", err)
	}
	// a plain DM answer (outside the gate thread) is re-asked, not accepted
	if res := f.core.HandleEvent(context.Background(), f.answer(f.owner, tuple("approve", rec), "", f.clock)); res.Outcome != "reasked" {
		t.Fatalf("a plain DM answer: %+v", res)
	}
	if _, err := v.AuthorizeClear(f.store, "s1", rec.GateID, clearInc, clearCfg); Code(err) != RejectClearUnsigned {
		t.Fatalf("after a plain DM answer: %v", err)
	}
	event := f.approve(rec, q)
	p, err := v.AuthorizeClear(f.store, "s1", rec.GateID, clearInc, clearCfg)
	if err != nil || p.GateID != rec.GateID || p.EventID != event {
		t.Fatalf("the owner's answer: %+v, %v", p, err)
	}
	// the record check at the commit step needs the mark a completed clear leaves
	if err := v.CheckClear(f.store, "s1", rec.GateID, event, clearInc, clearCfg); Code(err) != RejectClearUnsigned {
		t.Fatalf("a clear without its mark verified: %v", err)
	}
	if err := f.store.MarkClearUsed(gatestore.ClearUse{GateID: rec.GateID, Handle: "s1", EventID: event, At: "2026-10-24T12:00:00Z"}); err != nil {
		t.Fatal(err)
	}
	if err := v.CheckClear(f.store, "s1", rec.GateID, event, clearInc, clearCfg); err != nil {
		t.Fatalf("the record's proof: %v", err)
	}
	if err := v.CheckClear(f.store, "s1", rec.GateID, fmt.Sprintf("%064x", 5), clearInc, clearCfg); Code(err) != RejectClearUnsigned {
		t.Fatalf("a record naming another event verified: %v", err)
	}
}

func TestClearAnswerWithAnOldNonceIsRefusedAsReplayed(t *testing.T) {
	f := newFixture(t)
	v := f.verifier()
	old, oq := f.clearRequest("s1", 1)
	event := f.approve(old, oq)
	// a second quarantine of the seat: request 2 supersedes request 1
	cur, _ := f.clearRequest("s1", 2)
	if _, err := v.AuthorizeClear(f.store, "s1", old.GateID, clearInc, clearCfg); Code(err) != RejectReplayed {
		t.Fatalf("the old nonce's accepted answer cleared the new request: %v", err)
	}
	if _, err := v.AuthorizeClear(f.store, "s1", cur.GateID, clearInc, clearCfg); Code(err) != RejectClearUnsigned {
		t.Fatalf("the new request without its own answer: %v", err)
	}
	// an answer that already lifted a quarantine lifts no second one
	f2 := newFixture(t)
	r1, q1 := f2.clearRequest("s1", 1)
	e1 := f2.approve(r1, q1)
	if _, err := f2.verifier().AuthorizeClear(f2.store, "s1", r1.GateID, clearInc, clearCfg); err != nil {
		t.Fatal(err)
	}
	if err := f2.store.MarkClearUsed(gatestore.ClearUse{GateID: r1.GateID, Handle: "s1", EventID: e1, At: "2026-10-24T12:00:00Z"}); err != nil {
		t.Fatal(err)
	}
	if _, err := f2.verifier().AuthorizeClear(f2.store, "s1", r1.GateID, clearInc, clearCfg); Code(err) != RejectReplayed {
		t.Fatalf("a used answer cleared again: %v", err)
	}
	// the owner's event itself is single use in the store, as for any typed gate
	if res := f2.core.HandleEvent(context.Background(), f2.answer(f2.owner, tuple("approve", r1), q1.EventID, f2.clock)); res.Code != RejectReplayed {
		t.Fatalf("a re-sent answer: %+v", res)
	}
	// an answer for an earlier quarantine of the seat (another incarnation)
	if _, err := f.verifier().AuthorizeClear(f.store, "s1", old.GateID, "t1@1", clearCfg); Code(err) != RejectReplayed {
		t.Fatalf("an answer for another incarnation: %v", err)
	}
	_ = event
}

func TestClearAnswerForAnotherHandleIsRefused(t *testing.T) {
	f := newFixture(t)
	v := f.verifier()
	other, oq := f.clearRequest("s2", 7)
	f.approve(other, oq)
	mine, _ := f.clearRequest("s1", 8)
	if _, err := v.AuthorizeClear(f.store, "s1", other.GateID, clearInc, clearCfg); Code(err) != RejectClearHandle {
		t.Fatalf("seat s2's answer cleared s1: %v", err)
	}
	if err := v.CheckClear(f.store, "s1", other.GateID, "", clearInc, clearCfg); Code(err) != RejectClearHandle {
		t.Fatalf("seat s2's answer verified as s1's record: %v", err)
	}
	if _, err := v.AuthorizeClear(f.store, "s1", mine.GateID, clearInc, clearCfg); Code(err) != RejectClearUnsigned {
		t.Fatalf("s1's own request without an answer: %v", err)
	}
	// a gate of another class is no clear
	rel, rq := f.raise("rel-1", "tag", gatetarget.Command("git tag", "v1"))
	f.approve(rel, rq)
	if _, err := v.AuthorizeClear(f.store, "s1", rel.GateID, clearInc, clearCfg); Code(err) != RejectClearUnsigned {
		t.Fatalf("a tag gate cleared a seat: %v", err)
	}
}

func TestClearTargetRoundTrip(t *testing.T) {
	tg := gatetarget.QuarantineClear("s1", "n0", clearInc, clearCfg)
	h, n, i, c, ok := gatetarget.ParseQuarantineClear(tg)
	if !ok || h != "s1" || n != "n0" || i != clearInc || c != clearCfg {
		t.Fatalf("parse %q: %v %v %v %v %v", tg, h, n, i, c, ok)
	}
	for _, bad := range []string{"", "clear s1", "clear s1 nonce=a incarnation=b", "grow s1 nonce=a incarnation=b config=c", "clear s1 nonce=a incarnation=b config=c extra"} {
		if _, _, _, _, ok := gatetarget.ParseQuarantineClear(bad); ok {
			t.Errorf("%q parsed", bad)
		}
	}
}
