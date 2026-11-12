package buzz

import (
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"

	"github.com/omriariav/amq-squad/v3/internal/gateverify"
	"github.com/omriariav/amq-squad/v3/internal/nostr"
)

// TestRelayRefusesALeadLineOfEachWordOfTheList proves the relay's wider
// answer-shape refusal reads every decided word without narrowing old forms.
func TestRelayRefusesALeadLineOfEachWordOfTheList(t *testing.T) {
	pf := newPhoneFixture(t)
	_, rid := pf.ask(t, "where are we?", "")
	refused := func(line string) {
		t.Helper()
		before := len(pf.relayPosts())
		pf.threads.reply("lead-a", CockpitThread(rid), "Here is where we are.\n"+line, "")
		got := pf.relay(t)
		if len(got) != 1 || got[0].Outcome != "refused" || got[0].Reason != RefusedGateShape || len(pf.relayPosts()) != before {
			t.Errorf("%q: relayed %+v, posts %d want %d", line, got, len(pf.relayPosts()), before)
		}
	}
	for _, word := range bareWordCases330() {
		for _, line := range []string{word.word, titleWord330(word.word) + ".", strings.ToUpper(word.word) + "!", word.word + "!!", "**" + word.word + "**"} {
			refused(line)
		}
	}
	for _, line := range []string{
		"yes rel-zz9", "Deny rel-zz9 release", "no rel-zz9 release 0123456789abcdef",
		"go ahead rel-zz9", "Go ahead rel-zz9 release", "go ahead rel-zz9 release 0123456789abcdef",
	} {
		refused(line)
	}
	for _, line := range []string{"ok, the build is green", "Stop the train after the next merge", "go ahead with t26 next", "No, the build is green", "okay", "sure"} {
		before := len(pf.relayPosts())
		pf.threads.reply("lead-a", CockpitThread(rid), line, "")
		got := pf.relay(t)
		if len(got) != 1 || got[0].Outcome != "posted" || len(pf.relayPosts()) != before+1 {
			t.Errorf("%q: relayed %+v, posts %d want %d", line, got, len(pf.relayPosts()), before+1)
		}
	}
}

// TestABareWordOfTheListWithNoGateOpenIsRefusedAndReachesNoLead proves the
// handler recognizes every decided word before phone routing.
func TestABareWordOfTheListWithNoGateOpenIsRefusedAndReachesNoLead(t *testing.T) {
	for _, word := range bareWordCases330() {
		hf := newHandlerFixture(t)
		_, msg := hf.dm(t, word.word, "")
		got := hf.step(t)
		res, n := hf.serve.result(msg)
		if n != 1 || len(got) != 1 || got[0].Kind != "gate" || res != "refused - not_in_gate_thread" || len(hf.mail.sent) != 0 {
			t.Errorf("%q: reply %q (%d), handled %+v, sent %+v", word.word, res, n, got, hf.mail.sent)
		}
	}
	for _, line := range []string{"ok!!", "stop, the build is red", "where are we?"} {
		hf := newHandlerFixture(t)
		_, msg := hf.dm(t, line, "")
		got := hf.step(t)
		res, n := hf.serve.result(msg)
		if n != 1 || len(got) != 1 || got[0].Kind != "route" || !strings.HasPrefix(res, "accepted q-") || len(hf.mail.sent) != 1 {
			t.Errorf("%q: reply %q (%d), handled %+v, sent %+v", line, res, n, got, hf.mail.sent)
		}
	}
}

// TestABareWordOfTheListInAGatesThreadIsThatGatesAnswer proves each yes and
// no word binds to the question it directly replies to.
func TestABareWordOfTheListInAGatesThreadIsThatGatesAnswer(t *testing.T) {
	for _, word := range bareWordCases330() {
		hf := newHandlerFixture(t)
		rec, q, _ := raiseRel(t, hf.core, "rel-list")
		_, msg := hf.dm(t, word.word, q.EventID)
		got := hf.step(t)
		res, n := hf.serve.result(msg)
		if n != 1 || len(got) != 1 || got[0].Kind != "gate" || got[0].Gate == nil || got[0].Gate.GateID != rec.GateID || got[0].Gate.Outcome != word.outcome || len(hf.mail.sent) != 0 {
			t.Errorf("%q: reply %q (%d), handled %+v, sent %+v", word.word, res, n, got, hf.mail.sent)
		}
	}
}

// TestTheTeamDoorTakesAYesWordOfTheListAndNoNoWord proves plan-post
// confirmation follows the shared list and only its approve verb.
func TestTheTeamDoorTakesAYesWordOfTheListAndNoNoWord(t *testing.T) {
	const content = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	digest := "sha256:" + content + "@absent"
	pf := newPhoneFixture(t)
	var confirmed []string
	pf.h.Confirm = func(_ string, raw []byte, _ *gateverify.PlanPost) (string, error) {
		e, err := nostr.ParseEvent(raw)
		if err != nil {
			t.Fatal(err)
		}
		confirmed = append(confirmed, e.Content)
		return digest, nil
	}
	_, rid := pf.ask(t, "propose a team", "")
	planBody := "Proposed team: lead and two implementers.\ndigest " + digest
	pf.threads.reply("lead-a", CockpitThread(rid), planBody, "", gateverify.TeamPlanLabel+content)
	got := pf.relay(t)
	posts := pf.relayPosts()
	if len(got) != 1 || got[0].Outcome != "posted" || len(posts) != 1 || pf.record(t, rid).Posts[0].PlanFor != content {
		t.Fatalf("the team plan was not relayed and recorded: %+v posts %d record %+v", got, len(posts), pf.record(t, rid).Posts)
	}
	plan := posts[0]
	for _, line := range []string{"no", "deny", "n", "stop", "\u05dc\u05d0", "ok!!", "ok, but two implementers only"} {
		before := len(confirmed)
		_, msg := pf.dm(t, line, plan.ID)
		pf.step(t)
		res, _ := pf.serve.result(msg)
		if res == "confirmed team" || len(confirmed) != before {
			t.Errorf("%q confirmed the team: reply %q calls %#v", line, res, confirmed)
		}
	}
	for _, line := range []string{"yes", "approve", "y", "ok", "go ahead", "\u05db\u05df"} {
		before := len(confirmed)
		_, msg := pf.dm(t, line, plan.ID)
		pf.step(t)
		res, _ := pf.serve.result(msg)
		if res != "confirmed team" || len(confirmed) != before+1 || confirmed[len(confirmed)-1] != line {
			t.Errorf("%q did not confirm the team: reply %q calls %#v", line, res, confirmed)
		}
	}
}

type bareWordCase330 struct {
	word    string
	outcome string
}

// bareWordCases330 spells the eleven decided words without reading gatestore internals.
func bareWordCases330() []bareWordCase330 {
	return []bareWordCase330{
		{"yes", "approved"}, {"approve", "approved"}, {"y", "approved"}, {"ok", "approved"}, {"go ahead", "approved"}, {"\u05db\u05df", "approved"},
		{"no", "denied"}, {"deny", "denied"}, {"n", "denied"}, {"stop", "denied"}, {"\u05dc\u05d0", "denied"},
	}
}

// titleWord330 upper-cases the first rune of each U+0020-separated part.
func titleWord330(word string) string {
	parts := strings.Split(word, " ")
	for i, part := range parts {
		r, size := utf8.DecodeRuneInString(part)
		parts[i] = string(unicode.ToUpper(r)) + part[size:]
	}
	return strings.Join(parts, " ")
}
