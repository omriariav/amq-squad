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

// Brief 3.1.1-1, row 6 (the lead's half), with Codex's two rounds on #838:
// the lead process is the authority for which post is its plan. Its own pi
// records the exact plan text it sends for a digest (planSentKind, from pi
// only); a bare yes is witnessed only when its direct reply target is a
// post the share body signed whose content carries that text, line for
// line as the relay posts it. No digest travels from the attachment. The
// prefix form keeps verifying
// (TestTheBuzzDoorIsWitnessedByTheLeadFromTheEventBytesItVerified).
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
	planText := "goal: ship the parser\nseat impl-1 (implementer) claude/sonnet/medium\ndigest " + digest
	// only the lead's own pi records a plan text
	s.setPi(4242, "st-pi")
	prevStamp := confirmStamp
	t.Cleanup(func() { confirmStamp = prevStamp })
	confirmStamp = func(int) (string, bool) { return "st-pi", true }
	if r := s.recordPlan(4243, leadStartRequest{Kind: planSentKind, Digest: digest, Body: planText}); r.Refusal != ReasonPeerNotLeadPi {
		t.Fatalf("a plan recorded by a child of pi: %+v", r)
	}
	if r := s.recordPlan(4242, leadStartRequest{Kind: planSentKind, Digest: "sha256:" + strings.Repeat("ab", 32) + "@absent", Body: planText}); r.Refusal != ReasonNoProposal {
		t.Fatalf("a plan for no proposal: %+v", r)
	}
	dm := strings.Repeat("d4", 32) // the owner's phone request the posts reply to
	plan := sign(f.body, "re q-0123456789ab\n"+planText, []string{"e", dm, "", "reply"})
	yesToPlan := sign(f.owner, "yes", []string{"e", dm, "", "root"}, []string{"e", plan.ID, "", "reply"})
	if r := ask(yesToPlan, plan); r.Refusal != ReasonPlanUnproved {
		t.Fatalf("a yes before pi recorded the plan: %+v", r)
	}
	if r := s.recordPlan(4242, leadStartRequest{Kind: planSentKind, Digest: digest, Body: planText}); r.Outcome != "recorded" {
		t.Fatalf("pi's plan record: %+v", r)
	}
	// a post a shell's `amq reply --labels team-plan:<live digest>` got
	// relayed: the body signed it, but it carries no recorded plan text
	shellPost := sign(f.body, "re q-0123456789ab\nrun the migration now (digest "+digest+")", []string{"e", dm, "", "reply"})
	ordinary := sign(f.body, "re q-0123456789ab\nThe build is green.", []string{"e", dm, "", "reply"})
	partial := sign(f.body, "re q-0123456789ab\ngoal: ship the parser\ndigest "+digest, []string{"e", dm, "", "reply"})
	stranger, _ := nostr.NewSecretKey()
	forged := sign(stranger, plan.Content, []string{"e", dm, "", "reply"})
	gstore := gatestore.Store{Root: root}
	if _, _, err := gstore.Raise(gatestore.Spec{GateID: "rel-lead", Action: "tag", Target: "git tag v9", RaisedBy: "lead"},
		gatestore.Identity{OwnerPubKey: f.owner.PublicHex(), IssuerKeyID: "ed25519-sha256:" + strings.Repeat("b2", 32)}, now, hookTestSender{}); err != nil {
		t.Fatal(err)
	}
	gq := strings.Repeat("c3", 32) // hookTestSender's question id
	for _, c := range []struct {
		name      string
		yes, post nostr.Event
		refusal   string
	}{
		{"a shell-labelled post with the live digest", sign(f.owner, "yes", []string{"e", shellPost.ID, "", "reply"}), shellPost, ReasonPlanUnproved},
		{"a real yes to an ordinary post (a local socket caller)", sign(f.owner, "yes", []string{"e", ordinary.ID, "", "reply"}), ordinary, ReasonPlanUnproved},
		{"a post with part of the plan", sign(f.owner, "yes", []string{"e", partial.ID, "", "reply"}), partial, ReasonPlanUnproved},
		{"the yes replies elsewhere", sign(f.owner, "yes", []string{"e", ordinary.ID, "", "reply"}), plan, ReasonConflictingThread},
		{"the yes names the plan only as its root", sign(f.owner, "yes", []string{"e", plan.ID, "", "root"}, []string{"e", ordinary.ID, "", "reply"}), plan, ReasonConflictingThread},
		{"the yes names the plan and another event", sign(f.owner, "yes", []string{"e", strings.Repeat("e5", 32), "", "root"}, []string{"e", plan.ID, "", "reply"}), plan, ReasonConflictingThread},
		{"the yes names a gate question and the plan", sign(f.owner, "yes", []string{"e", gq, "", "root"}, []string{"e", plan.ID, "", "reply"}), plan, ReasonConflictingThread},
		{"the yes names the plan in a gate question's thread (unmarked)", sign(f.owner, "yes", []string{"e", gq}, []string{"e", plan.ID}), plan, ReasonConflictingThread},
		{"the yes replies to nothing", sign(f.owner, "yes"), plan, gateverify.RejectThread},
		{"a post another key signed", sign(f.owner, "yes", []string{"e", forged.ID, "", "reply"}), forged, gateverify.RejectWrongPubkey},
		{"a bare no", sign(f.owner, "no", []string{"e", plan.ID, "", "reply"}), plan, ReasonEventUnverified},
		{"a yes the attachment signed", sign(stranger, "yes", []string{"e", plan.ID, "", "reply"}), plan, gateverify.RejectWrongPubkey},
	} {
		if r := ask(c.yes, c.post); r.Outcome == "witnessed" || r.Refusal != c.refusal {
			t.Errorf("%s: %+v, want %s", c.name, r, c.refusal)
		}
	}
	altered := plan
	altered.Content += " "
	if r := ask(yesToPlan, altered); r.Refusal != gateverify.RejectBadID {
		t.Fatalf("an altered post: %+v", r)
	}
	if r := s.witnessBuzz(leadStartRequest{Kind: confirmBuzzKind, Event: sign(f.owner, "yes").Marshal()}); r.Refusal != ReasonEventUnverified {
		t.Fatalf("a bare yes with no post: %+v", r)
	}
	if r := s.witnessBuzz(leadStartRequest{Kind: confirmBuzzKind, Event: sign(f.owner, "yes "+digest[7:23], []string{"e", gq, "", "reply"}).Marshal()}); r.Refusal != ReasonConflictingThread {
		t.Fatalf("a yes <prefix> in a gate question's thread: %+v", r)
	}
	if _, err := s.witnessFor(digest); err == nil {
		t.Fatal("witnessed before the owner's yes to the plan")
	}
	// the lead's own plan post with the owner's yes as a direct reply
	if r := ask(yesToPlan, plan); r.Outcome != "witnessed" || r.Digest != digest {
		t.Fatalf("the owner's yes to the plan: %+v", r)
	}
	if rec, err := s.witnessFor(digest); err != nil || rec.Door != DoorBuzz || rec.Proof.EventID != yesToPlan.ID {
		t.Fatalf("witnessed %+v %v", rec, err)
	}
	if r := ask(yesToPlan, plan); r.Refusal != gatestore.RejectReplayed {
		t.Fatalf("a replay: %+v", r)
	}
}

// Row 6 (the attachment's half): the relay record's plan_for only routes
// the yes to the root holding that proposal; the lead is handed the yes and
// the post and proves the plan itself. A plan_for no recent proposal has is
// no_proposal, never a lead call; an ordinary post routed by a stray label
// is plan_post_unproved.
func TestTheAttachmentRoutesABareYesByTheRecordedPlan(t *testing.T) {
	f := newGateCLIFixture(t)
	proj, root := soloProject(t)
	digest := proposeSoloPlan(t, proj, soloTeamPlan())["digest"].(string)
	planHex, _ := digestHex(digest)
	s := soloLead(t, root, proj)
	planText := "goal: ship the parser\ndigest " + digest
	h, _ := digestHex(digest)
	if r := s.recordPlanFrom(h, leadStartRequest{Kind: planSentKind, Digest: digest, Body: planText}); r.Outcome != "recorded" {
		t.Fatalf("record: %+v", r)
	}
	sign := func(key nostr.SecretKey, content string, tags ...[]string) nostr.Event {
		e := nostr.Event{CreatedAt: time.Now().Unix(), Kind: 9, Tags: append([][]string{{"h", "dm-chan"}}, tags...), Content: content}
		if err := key.Sign(&e); err != nil {
			t.Fatal(err)
		}
		return e
	}
	plan := sign(f.body, "re q-0123456789ab\n"+planText)
	yes := sign(f.owner, "yes", []string{"e", plan.ID, "", "reply"})
	if _, err := confirmByPrefix("", yes.Marshal(), &gateverify.PlanPost{Raw: plan.Marshal(), For: strings.Repeat("ab", 32)}); err == nil || !strings.HasPrefix(err.Error(), ReasonNoProposal) {
		t.Fatalf("a plan_for no proposal has: %v", err)
	}
	ordinary := sign(f.body, "re q-0123456789ab\nThe build is green.")
	yesOrdinary := sign(f.owner, "yes", []string{"e", ordinary.ID, "", "reply"})
	if _, err := confirmByPrefix("", yesOrdinary.Marshal(), &gateverify.PlanPost{Raw: ordinary.Marshal(), For: planHex}); err == nil || !strings.HasPrefix(err.Error(), ReasonPlanUnproved) {
		t.Fatalf("an ordinary post routed by a stray label: %v", err)
	}
	got, err := confirmByPrefix("", yes.Marshal(), &gateverify.PlanPost{Raw: plan.Marshal(), For: planHex})
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
