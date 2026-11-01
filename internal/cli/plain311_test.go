package cli

import (
	"errors"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/gatestore"
	"github.com/omriariav/amq-squad/v3/internal/gateverify"
	"github.com/omriariav/amq-squad/v3/internal/nostr"
	"github.com/omriariav/amq-squad/v3/internal/team"
)

// Brief 3.1.1-1, row 6 (the lead's half): a bare yes replied to the relayed
// plan post is witnessed by the lead from the bytes it verified itself: the
// yes (the owner's pinned key, kind, channel, window, once-only) and the post
// (the share body's pinned key, its id recomputed, a DM in the channel, the
// yes names it in its thread, its content names the proposal). The same yes
// replied elsewhere, or with a post another key signed, is refused; the
// prefix form keeps verifying (TestTheBuzzDoorIsWitnessedByTheLeadFromTheEventBytesItVerified).
func TestTheLeadWitnessesABareYesRepliedToThePlanPost(t *testing.T) {
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
	ask := func(yes, post nostr.Event) leadStartReply {
		return s.witnessBuzz(leadStartRequest{Kind: confirmBuzzKind, Event: yes.Marshal(), Post: post.Marshal()})
	}
	plan := sign(f.body, "re q-0123456789ab\nProposed team: lead, two implementers.\ndigest "+digest+"\nprefix "+team.DigestPrefix(digest))
	other := sign(f.body, "re q-0123456789ab\nShall I start it?")
	stranger, _ := nostr.NewSecretKey()
	forged := sign(stranger, plan.Content)
	unrelated := sign(f.body, "re q-0123456789ab\nMerged as fedcba9876543210fedcba9876543210fedcba98.")
	for _, c := range []struct {
		name      string
		yes, post nostr.Event
		refusal   string
	}{
		{"the yes replies elsewhere", sign(f.owner, "yes", []string{"e", other.ID, "", "reply"}), plan, gateverify.RejectThread},
		{"the yes replies to nothing", sign(f.owner, "yes"), plan, gateverify.RejectThread},
		{"a post another key signed", sign(f.owner, "yes", []string{"e", forged.ID, "", "reply"}), forged, gateverify.RejectWrongPubkey},
		{"a post naming no proposal", sign(f.owner, "yes", []string{"e", unrelated.ID, "", "reply"}), unrelated, ReasonNoProposal},
		{"a bare no", sign(f.owner, "no", []string{"e", plan.ID, "", "reply"}), plan, ReasonEventUnverified},
		{"a yes the attachment signed", sign(stranger, "yes", []string{"e", plan.ID, "", "reply"}), plan, gateverify.RejectWrongPubkey},
	} {
		if r := ask(c.yes, c.post); r.Outcome == "witnessed" || r.Refusal != c.refusal {
			t.Errorf("%s: %+v, want %s", c.name, r, c.refusal)
		}
	}
	altered := plan
	altered.Content += " "
	if r := s.witnessBuzz(leadStartRequest{Kind: confirmBuzzKind, Event: sign(f.owner, "yes", []string{"e", plan.ID, "", "reply"}).Marshal(), Post: altered.Marshal()}); r.Refusal != gateverify.RejectBadID {
		t.Fatalf("an altered post: %+v", r)
	}
	if r := s.witnessBuzz(leadStartRequest{Kind: confirmBuzzKind, Event: sign(f.owner, "yes").Marshal()}); r.Refusal != ReasonEventUnverified {
		t.Fatalf("a bare yes with no post: %+v", r)
	}
	if _, err := s.witnessFor(digest); err == nil {
		t.Fatal("witnessed before the owner's yes to the plan")
	}
	yes := sign(f.owner, "Yes.", []string{"e", plan.ID, "", "reply"})
	if r := ask(yes, plan); r.Outcome != "witnessed" || r.Digest != digest {
		t.Fatalf("the owner's yes to the plan: %+v", r)
	}
	if rec, err := s.witnessFor(digest); err != nil || rec.Door != DoorBuzz || rec.Proof.EventID != yes.ID {
		t.Fatalf("witnessed %+v %v", rec, err)
	}
	if r := ask(yes, plan); r.Refusal != gatestore.RejectReplayed {
		t.Fatalf("a replay: %+v", r)
	}
}

// Row 6 (the attachment's half): confirmByPrefix finds the root by the
// proposal the post names and hands the lead the yes and the post; a post
// that names no proposal is gateverify.ErrNotAPlan (the handler then
// continues the request), never a lead call.
func TestTheAttachmentRoutesABareYesByThePostsProposal(t *testing.T) {
	f := newGateCLIFixture(t)
	proj, root := soloProject(t)
	digest := proposeSoloPlan(t, proj, soloTeamPlan())["digest"].(string)
	s := soloLead(t, root, proj)
	sign := func(key nostr.SecretKey, content string, tags ...[]string) nostr.Event {
		e := nostr.Event{CreatedAt: time.Now().Unix(), Kind: 9, Tags: append([][]string{{"h", "dm-chan"}}, tags...), Content: content}
		if err := key.Sign(&e); err != nil {
			t.Fatal(err)
		}
		return e
	}
	none := sign(f.body, "re q-0123456789ab\nThe build is green at fedcba9876543210fedcba98.")
	if _, err := confirmByPrefix("", sign(f.owner, "yes", []string{"e", none.ID, "", "reply"}).Marshal(), none.Marshal()); !errors.Is(err, gateverify.ErrNotAPlan) {
		t.Fatalf("a post naming no proposal: %v", err)
	}
	plan := sign(f.body, "re q-0123456789ab\nProposed team.\ndigest "+digest)
	yes := sign(f.owner, "yes", []string{"e", plan.ID, "", "reply"})
	got, err := confirmByPrefix("", yes.Marshal(), plan.Marshal())
	if err != nil || got != digest {
		t.Fatalf("the yes to the plan: %q %v", got, err)
	}
	if rec, err := s.witnessFor(digest); err != nil || rec.Proof.EventID != yes.ID {
		t.Fatalf("witnessed %+v %v", rec, err)
	}
}

// Row 3: no text asks for a tuple or for `yes <prefix>`: the refusal's two
// doors say to reply yes to the lead's post of the plan.
func TestTheDoorsTextAsksForNoPrefix(t *testing.T) {
	digest := "sha256:" + strings.Repeat("ab", 32) + "@absent"
	text := doorsText(digest)
	if regexp.MustCompile(`yes [0-9a-f]{16}`).MatchString(text) || strings.Contains(text, "with one line") {
		t.Fatalf("the doors text asks for a typed form: %s", text)
	}
	if !strings.Contains(text, "reply yes to the lead's Buzz post that relays this plan") {
		t.Fatalf("the doors text does not say how to confirm on the phone: %s", text)
	}
}
