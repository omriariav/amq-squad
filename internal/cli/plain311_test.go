package cli

import (
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/gatestore"
	"github.com/omriariav/amq-squad/v3/internal/gatetarget"
	"github.com/omriariav/amq-squad/v3/internal/gateverify"
	"github.com/omriariav/amq-squad/v3/internal/nostr"
	"github.com/omriariav/amq-squad/v3/internal/redact"
)

// Brief 3.1.1-1, row 6 (the lead's half), with Codex's P1 on #838: a bare
// yes replied to the recorded plan post is witnessed by the lead from the
// bytes it verified itself: the yes (the owner's pinned key, kind, channel,
// window, once-only), its direct reply target the post, the post (the share
// body's pinned key, its id recomputed, a DM in the channel), and the
// proposal the relay record marks the post with (plan_for), one of the
// lead's own recent proposals. No post text is read for the proposal. The
// prefix form keeps verifying
// (TestTheBuzzDoorIsWitnessedByTheLeadFromTheEventBytesItVerified).
func TestTheLeadWitnessesABareYesRepliedToThePlanPost(t *testing.T) {
	f := newGateCLIFixture(t)
	proj, root := soloProject(t)
	digest := proposeSoloPlan(t, proj, soloTeamPlan())["digest"].(string)
	planFor, _ := digestHex(digest)
	s := newLeadStarter(root, proj, "test", nil)
	now := time.Now()
	sign := func(key nostr.SecretKey, content string, tags ...[]string) nostr.Event {
		e := nostr.Event{CreatedAt: now.Unix(), Kind: 9, Tags: append([][]string{{"h", "dm-chan"}}, tags...), Content: content}
		if err := key.Sign(&e); err != nil {
			t.Fatal(err)
		}
		return e
	}
	ask := func(yes, post nostr.Event, forHex string) leadStartReply {
		return s.witnessBuzz(leadStartRequest{Kind: confirmBuzzKind, Event: yes.Marshal(), Post: post.Marshal(), PlanFor: forHex})
	}
	plan := sign(f.body, "re q-0123456789ab\nProposed team: lead, two implementers.")
	other := sign(f.body, "re q-0123456789ab\nShall I start it?")
	stranger, _ := nostr.NewSecretKey()
	forged := sign(stranger, plan.Content)
	for _, c := range []struct {
		name      string
		yes, post nostr.Event
		forHex    string
		refusal   string
	}{
		{"the yes replies elsewhere", sign(f.owner, "yes", []string{"e", other.ID, "", "reply"}), plan, planFor, gateverify.RejectThread},
		{"the yes names the plan only as its root", sign(f.owner, "yes", []string{"e", plan.ID, "", "root"}, []string{"e", other.ID, "", "reply"}), plan, planFor, gateverify.RejectThread},
		{"the yes replies to nothing", sign(f.owner, "yes"), plan, planFor, gateverify.RejectThread},
		{"a post another key signed", sign(f.owner, "yes", []string{"e", forged.ID, "", "reply"}), forged, planFor, gateverify.RejectWrongPubkey},
		{"a plan for no recent proposal", sign(f.owner, "yes", []string{"e", plan.ID, "", "reply"}), plan, strings.Repeat("ab", 32), ReasonNoProposal},
		{"no plan digest", sign(f.owner, "yes", []string{"e", plan.ID, "", "reply"}), plan, "", ReasonEventUnverified},
		{"a bare no", sign(f.owner, "no", []string{"e", plan.ID, "", "reply"}), plan, planFor, ReasonEventUnverified},
		{"a yes the attachment signed", sign(stranger, "yes", []string{"e", plan.ID, "", "reply"}), plan, planFor, gateverify.RejectWrongPubkey},
	} {
		if r := ask(c.yes, c.post, c.forHex); r.Outcome == "witnessed" || r.Refusal != c.refusal {
			t.Errorf("%s: %+v, want %s", c.name, r, c.refusal)
		}
	}
	altered := plan
	altered.Content += " "
	if r := ask(sign(f.owner, "yes", []string{"e", plan.ID, "", "reply"}), altered, planFor); r.Refusal != gateverify.RejectBadID {
		t.Fatalf("an altered post: %+v", r)
	}
	if r := s.witnessBuzz(leadStartRequest{Kind: confirmBuzzKind, Event: sign(f.owner, "yes").Marshal()}); r.Refusal != ReasonEventUnverified {
		t.Fatalf("a bare yes with no post: %+v", r)
	}
	if _, err := s.witnessFor(digest); err == nil {
		t.Fatal("witnessed before the owner's yes to the plan")
	}
	yes := sign(f.owner, "Yes.", []string{"e", plan.ID, "", "reply"})
	if r := ask(yes, plan, planFor); r.Outcome != "witnessed" || r.Digest != digest {
		t.Fatalf("the owner's yes to the plan: %+v", r)
	}
	if rec, err := s.witnessFor(digest); err != nil || rec.Door != DoorBuzz || rec.Proof.EventID != yes.ID {
		t.Fatalf("witnessed %+v %v", rec, err)
	}
	if r := ask(yes, plan, planFor); r.Refusal != gatestore.RejectReplayed {
		t.Fatalf("a replay: %+v", r)
	}
}

// Row 6 (the attachment's half): confirmByPrefix finds the root by the
// recorded plan's digest and hands the lead the yes, the post and the
// digest; a digest no recent proposal has is no_proposal, never a lead call.
func TestTheAttachmentRoutesABareYesByTheRecordedPlan(t *testing.T) {
	f := newGateCLIFixture(t)
	proj, root := soloProject(t)
	digest := proposeSoloPlan(t, proj, soloTeamPlan())["digest"].(string)
	planFor, _ := digestHex(digest)
	s := soloLead(t, root, proj)
	sign := func(key nostr.SecretKey, content string, tags ...[]string) nostr.Event {
		e := nostr.Event{CreatedAt: time.Now().Unix(), Kind: 9, Tags: append([][]string{{"h", "dm-chan"}}, tags...), Content: content}
		if err := key.Sign(&e); err != nil {
			t.Fatal(err)
		}
		return e
	}
	plan := sign(f.body, "re q-0123456789ab\nProposed team.")
	yes := sign(f.owner, "yes", []string{"e", plan.ID, "", "reply"})
	if _, err := confirmByPrefix("", yes.Marshal(), &gateverify.PlanPost{Raw: plan.Marshal(), For: strings.Repeat("ab", 32)}); err == nil || !strings.HasPrefix(err.Error(), ReasonNoProposal) {
		t.Fatalf("a plan for no proposal: %v", err)
	}
	got, err := confirmByPrefix("", yes.Marshal(), &gateverify.PlanPost{Raw: plan.Marshal(), For: planFor})
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

// Codex on #838 (P1): a canary in a PR title is absent from the question,
// from status's gates[].question and from `gate status` (the answer key's
// source; the screen shows status's field verbatim, the key the record's
// text, pi's row "the gates block and the answer key show the question
// above the rule verbatim"). A record status reads without validating (a
// hand-written file) is redacted by status too.
func TestACanaryInATitleReachesNoQuestionStatusOrKey(t *testing.T) {
	f := newGateCLIFixture(t)
	const secret = "zebra311statusvalue"
	canary := "GH_TOKEN=" + secret
	for _, re := range redact.RealCredentialShapes {
		if re.MatchString(canary) {
			t.Fatalf("the canary matches a real credential shape %s", re)
		}
	}
	store, err := gatestore.Open(f.root)
	if err != nil {
		t.Fatal(err)
	}
	ident := gatestore.Identity{OwnerPubKey: f.owner.PublicHex(), IssuerKeyID: "ed25519-sha256:" + strings.Repeat("b2", 32)}
	rec, err := gatestore.Render(gatestore.Spec{GateID: "merge-canary", Action: "spawn", Target: gatetarget.MergeBeyond("owner/fixture", 7, strings.Repeat("a", 40), []string{"ci_green"}),
		RaisedBy: "lead", Squad: store.Squad(), Facts: gatestore.SafeFacts("Fix "+canary, "main")}, time.Now(), ident)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Create(rec); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(rec.Text, secret) {
		t.Fatalf("the question carries the canary:\n%s", rec.Text)
	}
	out, _, err := captureOutput(t, func() error {
		return runGateStatus([]string{"--project", f.project, "--session", "s", "--me", "cto", "--json"})
	})
	if err != nil || strings.Contains(out, secret) || !strings.Contains(out, "merge-canary") {
		t.Fatalf("gate status: %v\n%s", err, out)
	}
	// a record status reads without validating it
	hand := `{"gate_id":"hand-1","class":"tag","digest":"sha256:` + strings.Repeat("c", 64) + `","raised_by":"lead","not_after":"2999-01-01T00:00:00Z",` +
		`"text":"Create a tag named v1?\n` + canary + `\nYes lets it run. No stops it.\n\n----------\namq-squad gate hand-1"}`
	mustWrite(t, filepath.Join(store.Dir("gates"), "hand-1.json"), hand)
	st := buildStatusContract(f.root, f.project, "test", time.Now())
	seen := 0
	for _, g := range st.Gates {
		if strings.Contains(g.Question, secret) {
			t.Fatalf("status gate %s carries the canary: %q", g.ID, g.Question)
		}
		if strings.Contains(g.Question, redact.Placeholder) {
			seen++
		}
	}
	if seen != 2 {
		t.Fatalf("status gates = %+v, want both questions redacted", st.Gates)
	}
}
