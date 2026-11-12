package cli

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/nostr"
)

// TestTheLeadWitnessesAYesWordOfTheListRepliedToThePlanPost proves the lead's
// independent witness follows ParseBare's approve verb and rejects every no
// word and every line outside the one-mark rule.
func TestTheLeadWitnessesAYesWordOfTheListRepliedToThePlanPost(t *testing.T) {
	for i, word := range []string{"ok", "y", "go ahead", "\u05db\u05df", "OK."} {
		t.Run(fmt.Sprintf("yes-%d", i), func(t *testing.T) {
			got, digest := witnessWord330(t, word)
			if got.Outcome != "witnessed" || got.Digest != digest {
				t.Fatalf("%q: %+v, want witnessed %s", word, got, digest)
			}
		})
	}
	for i, word := range []string{"stop", "n", "\u05dc\u05d0", "ok!!", "ok, but"} {
		t.Run(fmt.Sprintf("no-%d", i), func(t *testing.T) {
			got, _ := witnessWord330(t, word)
			if got.Outcome == "witnessed" || got.Refusal != ReasonEventUnverified {
				t.Fatalf("%q: %+v, want %s", word, got, ReasonEventUnverified)
			}
		})
	}
}

// witnessWord330 builds a fresh recorded plan and asks the lead to witness
// one owner's signed reply to the body-signed plan post.
func witnessWord330(t *testing.T, word string) (leadStartReply, string) {
	t.Helper()
	f := newGateCLIFixture(t)
	proj, root := soloProject(t)
	digest := proposeSoloPlan(t, proj, soloTeamPlan())["digest"].(string)
	s := newLeadStarter(root, proj, "test", nil)
	now := time.Now()
	sign := func(key nostr.SecretKey, content string, tags ...[]string) nostr.Event {
		e := nostr.Event{CreatedAt: now.Unix(), Kind: 9, Tags: append([][]string{{"h", "dm-chan"}}, tags...), Content: content}
		if err := key.Sign(&e); err != nil {
			t.Fatal(err)
		}
		return e
	}
	planText := "goal: ship the parser\nseat impl-1 (implementer) claude/sonnet/medium\ndigest " + digest
	s.setPi(4242, "st-pi")
	previousStamp := confirmStamp
	t.Cleanup(func() { confirmStamp = previousStamp })
	confirmStamp = func(int) (string, bool) { return "st-pi", true }
	if r := s.recordPlan(4242, leadStartRequest{Kind: planSentKind, Digest: digest, Body: planText}); r.Outcome != "recorded" {
		t.Fatalf("record plan: %+v", r)
	}
	dm := strings.Repeat("d4", 32)
	plan := sign(f.body, "re q-0123456789ab\n"+planText, []string{"e", dm, "", "reply"})
	answer := sign(f.owner, word, []string{"e", dm, "", "root"}, []string{"e", plan.ID, "", "reply"})
	return s.witnessBuzz(leadStartRequest{Kind: confirmBuzzKind, Event: answer.Marshal(), Post: plan.Marshal()}), digest
}
