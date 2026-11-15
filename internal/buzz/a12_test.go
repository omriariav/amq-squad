package buzz

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/gatestore"
	"github.com/omriariav/amq-squad/v3/internal/gateverify"
	"github.com/omriariav/amq-squad/v3/internal/nostr"
	"github.com/omriariav/amq-squad/v3/internal/status"
)

// a12Fixture is one phone fixture whose handler, relay posts and gate core
// share a whole-second clock.
func a12Fixture(t *testing.T) phoneFixture {
	t.Helper()
	pf := newPhoneFixture(t)
	*pf.clock = time.Unix(pf.clock.Unix(), 0).UTC()
	pf.core.Now = pf.h.Now
	return pf
}

// a12DM signs and handles one owner DM at the stated time, optionally in a
// thread, and returns the handler result and its owner-visible reply.
func a12DM(t *testing.T, pf phoneFixture, content, thread string, at time.Time) (Handled, string) {
	t.Helper()
	tags := [][]string{{"h", pf.core.Pin.Channel}, {"p", pf.body.PublicHex()}}
	if thread != "" {
		tags = append(tags, []string{"e", thread, "", "reply"})
	}
	e := nostr.Event{CreatedAt: at.Unix(), Kind: 9, Tags: tags, Content: content}
	if err := pf.owner.sk.Sign(&e); err != nil {
		t.Fatal(err)
	}
	if err := pf.mem.Publish(context.Background(), e.Marshal()); err != nil {
		t.Fatal(err)
	}
	msg := pf.serve.deliver(e.ID, pf.core.Pin.Channel, content)
	got := pf.step(t)
	if len(got) != 1 {
		t.Fatalf("handled %d messages: %+v", len(got), got)
	}
	reply, n := pf.serve.result(msg)
	if n != 1 {
		t.Fatalf("reply %q has %d results", reply, n)
	}
	t.Logf("reply: %s", reply)
	return got[0], reply
}

// a12SecondCore adds another served store with the same channel binding and
// clock, as operator mode does for another squad.
func a12SecondCore(t *testing.T, pf phoneFixture, session string) *gateverify.Core {
	t.Helper()
	store, err := gatestore.Open(filepath.Join(realTempDir(t), "root"))
	if err != nil {
		t.Fatal(err)
	}
	c := *pf.core
	c.Store = store
	c.Namespace.Session = session
	c.Now = pf.h.Now
	pf.h.Cores = append(pf.h.Cores, &c)
	return &c
}

// a12OpenLeadRequest routes one phone question and returns the relay record's
// request id, before any lead reply is posted.
func a12OpenLeadRequest(t *testing.T, pf phoneFixture) string {
	t.Helper()
	h, _ := a12DM(t, pf, "tell me the status", "", *pf.clock)
	if h.Kind != "route" || h.RequestID == "" {
		t.Fatalf("phone question was not routed: %+v", h)
	}
	return h.RequestID
}

// a12LeadPost posts one lead reply on a fresh phone request and returns the
// request id and the relay-step result.
func a12LeadPost(t *testing.T, pf phoneFixture, text string) (string, Relayed, error) {
	t.Helper()
	rid := a12OpenLeadRequest(t, pf)
	pf.threads.reply("lead-a", CockpitThread(rid), text, "")
	got, err := pf.h.RelayStep(context.Background())
	if len(got) != 1 {
		t.Fatalf("lead reply relayed %+v", got)
	}
	t.Logf("relay: %+v error=%v", got[0], err)
	return rid, got[0], err
}

// requireA12 checks one handled gate outcome and an exact or prefixed reply.
func requireA12(t *testing.T, h Handled, reply, outcome, want string, prefix bool) {
	t.Helper()
	if h.Gate == nil || h.Gate.Outcome != outcome || (prefix && !strings.HasPrefix(reply, want)) || (!prefix && reply != want) {
		t.Fatalf("handled %+v reply %q, want outcome %s reply %q prefix=%v", h, reply, outcome, want, prefix)
	}
}

// TestAnUnthreadedBareAnswerBindsTheOneOpenGateOnly covers the handler's
// store selection, shared-DM recency rule, sidecar compatibility, timestamp
// bounds, reply shape and unchanged security checks.
func TestAnUnthreadedBareAnswerBindsTheOneOpenGateOnly(t *testing.T) {
	t.Run("R1 no gate", func(t *testing.T) {
		pf := a12Fixture(t)
		h, reply := a12DM(t, pf, "yes", "", *pf.clock)
		requireA12(t, h, reply, "rejected", "refused - "+gateverify.RejectThread, false)
	})
	t.Run("R2 one gate", func(t *testing.T) {
		pf := a12Fixture(t)
		rec, _, _ := raiseRel(t, pf.core, "rel-2")
		pf.advance(5 * time.Second)
		h, reply := a12DM(t, pf, "yes", "", *pf.clock)
		requireA12(t, h, reply, "approved", "accepted rel-2 tag", false)
		ra, _ := pf.core.Store.ReadReask(rec.GateID)
		if ra != nil || len(pf.mail.sent) != 0 || pf.core.Store.State(rec, *pf.clock) == gatestore.StateOpen {
			t.Fatalf("reask %+v mail %+v state %s", ra, pf.mail.sent, pf.core.Store.State(rec, *pf.clock))
		}
	})
	t.Run("R3 two gates one store", func(t *testing.T) {
		pf := a12Fixture(t)
		raiseRel(t, pf.core, "rel-3a")
		raiseRel(t, pf.core, "rel-3b")
		h, reply := a12DM(t, pf, "yes", "", *pf.clock)
		if h.Gate == nil || h.Gate.Outcome != "rejected" || !strings.Contains(reply, gateverify.RejectThread) {
			t.Fatalf("handled %+v reply %q", h, reply)
		}
	})
	t.Run("R4 two served stores", func(t *testing.T) {
		pf := a12Fixture(t)
		raiseRel(t, pf.core, "rel-4a")
		other := a12SecondCore(t, pf, "other")
		raiseRel(t, other, "rel-4b")
		h, reply := a12DM(t, pf, "yes", "", *pf.clock)
		if h.Gate != nil || reply != "refused - ambiguous_gate" {
			t.Fatalf("handled %+v reply %q", h, reply)
		}
	})
	t.Run("R5 later lead question", func(t *testing.T) {
		pf := a12Fixture(t)
		raiseRel(t, pf.core, "rel-5")
		pf.advance(2 * time.Second)
		a12LeadPost(t, pf, "Shall I go on?")
		pf.advance(2 * time.Second)
		h, reply := a12DM(t, pf, "yes", "", *pf.clock)
		requireA12(t, h, reply, "reasked", "refused rel-5 "+gateverify.RejectThread+"\nre-ask: ", true)
	})
	t.Run("R6 later lead statement", func(t *testing.T) {
		pf := a12Fixture(t)
		raiseRel(t, pf.core, "rel-6")
		pf.advance(2 * time.Second)
		a12LeadPost(t, pf, "The build is green. Is it? It is.")
		pf.advance(2 * time.Second)
		h, reply := a12DM(t, pf, "yes", "", *pf.clock)
		requireA12(t, h, reply, "approved", "accepted rel-6 tag", false)
	})
	t.Run("R7 unreadable relay record", func(t *testing.T) {
		pf := a12Fixture(t)
		raiseRel(t, pf.core, "rel-7")
		dir := status.RelayDir(pf.h.Target.Root)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "q-000000000000.json"), []byte("{not json"), 0o600); err != nil {
			t.Fatal(err)
		}
		h, _ := a12DM(t, pf, "yes", "", *pf.clock)
		if h.Gate == nil || h.Gate.Outcome != "reasked" {
			t.Fatalf("handled %+v", h)
		}
	})
	t.Run("R8 answer older", func(t *testing.T) {
		pf := a12Fixture(t)
		raiseRel(t, pf.core, "rel-8")
		h, _ := a12DM(t, pf, "yes", "", pf.clock.Add(-time.Second))
		if h.Gate == nil || h.Gate.Outcome != "reasked" {
			t.Fatalf("handled %+v", h)
		}
	})
	t.Run("R9 equal second", func(t *testing.T) {
		pf := a12Fixture(t)
		raiseRel(t, pf.core, "rel-9")
		h, reply := a12DM(t, pf, "yes", "", *pf.clock)
		requireA12(t, h, reply, "approved", "accepted rel-9 tag", false)
	})
	t.Run("R10 bare no", func(t *testing.T) {
		pf := a12Fixture(t)
		raiseRel(t, pf.core, "rel-10")
		h, reply := a12DM(t, pf, "no", "", *pf.clock)
		requireA12(t, h, reply, "denied", "accepted rel-10 tag", false)
	})
	t.Run("R11 typed tuple", func(t *testing.T) {
		pf := a12Fixture(t)
		_, _, tuple := raiseRel(t, pf.core, "rel-11")
		h, reply := a12DM(t, pf, tuple, "", *pf.clock)
		requireA12(t, h, reply, "reasked", "refused rel-11 "+gateverify.RejectThread+"\nre-ask: ", true)
	})
	t.Run("R12 answer after reask", func(t *testing.T) {
		pf := a12Fixture(t)
		_, _, tuple := raiseRel(t, pf.core, "rel-12")
		first, _ := a12DM(t, pf, tuple, "", *pf.clock)
		h, reply := a12DM(t, pf, "yes", "", *pf.clock)
		if first.Gate == nil || first.Gate.Outcome != "reasked" {
			t.Fatalf("first %+v", first)
		}
		requireA12(t, h, reply, "rejected", "refused rel-12 "+gateverify.RejectThread+"\nre-ask: ", true)
	})
	t.Run("R13 missing endings", func(t *testing.T) {
		pf := a12Fixture(t)
		raiseRel(t, pf.core, "rel-13")
		pf.advance(2 * time.Second)
		rid, _, _ := a12LeadPost(t, pf, "Done.")
		if err := os.Remove(endingsPath(status.RelayDir(pf.h.Target.Root), rid)); err != nil {
			t.Fatal(err)
		}
		h, _ := a12DM(t, pf, "yes", "", *pf.clock)
		if h.Gate == nil || h.Gate.Outcome != "reasked" {
			t.Fatalf("handled %+v", h)
		}
	})
	t.Run("R14 posting in flight", func(t *testing.T) {
		pf := a12Fixture(t)
		raiseRel(t, pf.core, "rel-14")
		pf.advance(2 * time.Second)
		rid, _, _ := a12LeadPost(t, pf, "Done.")
		rec := pf.record(t, rid)
		rec.Posting = &status.RelayPosting{ReplyID: "r-999", Kind: "answer", Parts: []string{sha256Hex("later")}}
		if err := writeRelayRecord(status.RelayDir(pf.h.Target.Root), rec, *pf.clock); err != nil {
			t.Fatal(err)
		}
		h, _ := a12DM(t, pf, "yes", "", *pf.clock)
		if h.Gate == nil || h.Gate.Outcome != "reasked" {
			t.Fatalf("handled %+v", h)
		}
	})
	t.Run("R15 post before gate", func(t *testing.T) {
		pf := a12Fixture(t)
		a12LeadPost(t, pf, "Shall I go on?")
		pf.advance(2 * time.Second)
		raiseRel(t, pf.core, "rel-15")
		h, reply := a12DM(t, pf, "yes", "", *pf.clock)
		requireA12(t, h, reply, "approved", "accepted rel-15 tag", false)
	})
	t.Run("R16 endings grammar", func(t *testing.T) {
		for _, s := range []string{"Go on?", "Go on?  \n\t", "Go on?.", "**Shall I go on?**", "Shall I go on? 🙂", "(shall I?)", "\"Go on?\"", "Go on？", "Go on؟"} {
			if !endsInQuestion([]string{s}) {
				t.Errorf("false for %q", s)
			}
		}
		for _, s := range []string{"Go on", "", "a?\nb", "Is it? It is.", "Done.", "v2?3"} {
			if endsInQuestion([]string{s}) {
				t.Errorf("true for %q", s)
			}
		}
		if !endsInQuestion([]string{"part one", "part two?"}) || endsInQuestion([]string{"part one?", "part two"}) {
			t.Fatal("parts are not joined in order")
		}
		t.Logf("endings grammar true and false cases passed")
	})
	t.Run("R17 future only unthreaded", func(t *testing.T) {
		pf := a12Fixture(t)
		_, q, _ := raiseRel(t, pf.core, "rel-17")
		future := pf.clock.Add(5 * time.Second)
		h, reply := a12DM(t, pf, "yes", "", future)
		requireA12(t, h, reply, "reasked", "refused rel-17 "+gateverify.RejectThread+"\nre-ask: ", true)
		h, reply = a12DM(t, pf, "yes", q.EventID, future)
		requireA12(t, h, reply, "approved", "accepted rel-17 tag", false)
	})
	t.Run("R18 decorated later question", func(t *testing.T) {
		pf := a12Fixture(t)
		raiseRel(t, pf.core, "rel-18")
		pf.advance(2 * time.Second)
		a12LeadPost(t, pf, "**Shall I go on?**")
		pf.advance(2 * time.Second)
		h, _ := a12DM(t, pf, "yes", "", *pf.clock)
		if h.Gate == nil || h.Gate.Outcome != "reasked" {
			t.Fatalf("handled %+v", h)
		}
	})
	t.Run("G1 later answered gate", func(t *testing.T) {
		pf := a12Fixture(t)
		raiseRel(t, pf.core, "rel-g1a")
		pf.advance(2 * time.Second)
		_, q, _ := raiseRel(t, pf.core, "rel-g1b")
		pf.advance(time.Second)
		_, reply := a12DM(t, pf, "yes", q.EventID, *pf.clock)
		if reply != "accepted rel-g1b tag" {
			t.Fatalf("reply %q", reply)
		}
		pf.advance(2 * time.Second)
		h, _ := a12DM(t, pf, "yes", "", *pf.clock)
		if h.Gate == nil || h.Gate.Outcome != "reasked" {
			t.Fatalf("handled %+v", h)
		}
	})
	t.Run("G1b later gate other store", func(t *testing.T) {
		pf := a12Fixture(t)
		raiseRel(t, pf.core, "rel-g1c")
		other := a12SecondCore(t, pf, "other")
		pf.advance(2 * time.Second)
		_, q, _ := raiseRel(t, other, "rel-g1d")
		pf.advance(time.Second)
		_, reply := a12DM(t, pf, "no", q.EventID, *pf.clock)
		if reply != "accepted rel-g1d tag" {
			t.Fatalf("reply %q", reply)
		}
		pf.advance(2 * time.Second)
		h, _ := a12DM(t, pf, "yes", "", *pf.clock)
		if h.Gate == nil || h.Gate.Outcome != "reasked" {
			t.Fatalf("handled %+v", h)
		}
	})
	t.Run("G2 later question other root", func(t *testing.T) {
		pf := a12Fixture(t)
		raiseRel(t, pf.core, "rel-g2")
		other := a12SecondCore(t, pf, "other")
		pf.advance(2 * time.Second)
		rid, _, _ := a12LeadPost(t, pf, "Shall I go on?")
		from := status.RelayDir(pf.h.Target.Root)
		to := status.RelayDir(other.Store.Root)
		if err := os.MkdirAll(to, 0o700); err != nil {
			t.Fatal(err)
		}
		for _, suffix := range []string{".json", EndingsSuffix} {
			if err := os.Rename(filepath.Join(from, rid+suffix), filepath.Join(to, rid+suffix)); err != nil {
				t.Fatal(err)
			}
		}
		pf.advance(2 * time.Second)
		h, _ := a12DM(t, pf, "yes", "", *pf.clock)
		if h.Gate == nil || h.Gate.Outcome != "reasked" {
			t.Fatalf("handled %+v", h)
		}
	})
	t.Run("S1 lead post same second", func(t *testing.T) {
		pf := a12Fixture(t)
		raiseRel(t, pf.core, "rel-s1")
		a12LeadPost(t, pf, "Shall I go on?")
		h, _ := a12DM(t, pf, "yes", "", *pf.clock)
		if h.Gate == nil || h.Gate.Outcome != "reasked" {
			t.Fatalf("handled %+v", h)
		}
	})
	t.Run("S2 gate question same second", func(t *testing.T) {
		pf := a12Fixture(t)
		raiseRel(t, pf.core, "rel-s2a")
		_, q, _ := raiseRel(t, pf.core, "rel-s2b")
		_, reply := a12DM(t, pf, "no", q.EventID, *pf.clock)
		if reply != "accepted rel-s2b tag" {
			t.Fatalf("reply %q", reply)
		}
		h, _ := a12DM(t, pf, "yes", "", *pf.clock)
		if h.Gate == nil || h.Gate.Outcome != "reasked" {
			t.Fatalf("handled %+v", h)
		}
	})
	t.Run("I1 insecure approval", func(t *testing.T) {
		pf := a12Fixture(t)
		rec, _, _ := raiseRel(t, pf.core, "rel-i1")
		pf.core.Insecure = true
		h, reply := a12DM(t, pf, "yes", "", *pf.clock)
		requireA12(t, h, reply, "rejected", "refused rel-i1 "+gateverify.RejectInsecure, false)
		if pf.core.Store.State(rec, *pf.clock) != gatestore.StateOpen {
			t.Fatal("gate closed")
		}
	})
	t.Run("F1 failed endings write", func(t *testing.T) {
		pf := a12Fixture(t)
		raiseRel(t, pf.core, "rel-f1")
		pf.advance(2 * time.Second)
		rid := a12OpenLeadRequest(t, pf)
		if err := os.MkdirAll(endingsPath(status.RelayDir(pf.h.Target.Root), rid), 0o700); err != nil {
			t.Fatal(err)
		}
		pf.threads.reply("lead-a", CockpitThread(rid), "Done.", "")
		got, err := pf.h.RelayStep(context.Background())
		if len(got) != 1 || got[0].Outcome != "posted" || got[0].Error == "" || err == nil || !strings.Contains(err.Error(), rid+": "+RelayEndingsFailed+": ") {
			t.Fatalf("relay %+v error %v", got, err)
		}
		pf.advance(2 * time.Second)
		h, _ := a12DM(t, pf, "yes", "", *pf.clock)
		if h.Gate == nil || h.Gate.Outcome != "reasked" {
			t.Fatalf("handled %+v", h)
		}
	})
	t.Run("U1 open count direct", func(t *testing.T) {
		pf := a12Fixture(t)
		_, q, _ := raiseRel(t, pf.core, "rel-u1a")
		other := a12SecondCore(t, pf, "other")
		pf.advance(-10 * time.Second)
		raiseRel(t, other, "rel-u1b")
		pf.advance(10 * time.Second)
		if pf.h.unthreadedBinds(q) {
			t.Fatal("two open gates bound")
		}
		t.Logf("two-store open count refused")
	})
	t.Run("K1 relay record compatibility", func(t *testing.T) {
		pf := a12Fixture(t)
		rid, _, _ := a12LeadPost(t, pf, "Shall I go on?")
		dir := status.RelayDir(pf.h.Target.Root)
		raw, err := os.ReadFile(relayRecordPath(dir, rid))
		if err != nil {
			t.Fatal(err)
		}
		rec, err := status.DecodeRelayRecord(raw, rid)
		if err != nil || len(rec.Posts) != 1 {
			t.Fatalf("decode %v record %+v", err, rec)
		}
		var obj map[string]any
		if err := json.Unmarshal(raw, &obj); err != nil {
			t.Fatal(err)
		}
		post := obj["posts"].([]any)[0].(map[string]any)
		keys := make([]string, 0, len(post))
		for k := range post {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		want := []string{"at", "events", "kind", "reply_id", "sha256"}
		paths, _ := filepath.Glob(filepath.Join(dir, "*.json"))
		matches, _ := filepath.Match("*.json", rid+EndingsSuffix)
		if !slices.Equal(keys, want) || len(paths) != 1 || matches {
			t.Fatalf("keys %v paths %v endings matches=%v", keys, paths, matches)
		}
		t.Logf("relay keys %v json files %d endings matches=%v", keys, len(paths), matches)
	})
}
