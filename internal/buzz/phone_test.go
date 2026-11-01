package buzz

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/gatestore"
	"github.com/omriariav/amq-squad/v3/internal/nostr"
	"github.com/omriariav/amq-squad/v3/internal/redact"
	"github.com/omriariav/amq-squad/v3/internal/status"
)

// Brief 31-4, the two-way phone: the rows of the lead reply relay. The
// relay is the in-process test relay (memRelay) behind the verifier's
// counting dialer, the AMQ threads a fake fed from the fake mailer's sends
// and the lead's replies; no network, no real Buzz, no key material.

var acceptedRe = regexp.MustCompile(`^accepted (q-[0-9a-f]{12}) routed `)

// routedTo reports whether a receipt is `accepted <request id> routed <dest>`.
func routedTo(reply, dest string) bool {
	m := acceptedRe.FindStringSubmatch(reply)
	return m != nil && reply == "accepted "+m[1]+" routed "+dest
}

// fakeThreads is AMQ's thread view: the attachment's question posts (from
// the fake mailer) and the lead's replies, oldest first.
type fakeThreads struct {
	mu      sync.Mutex
	mail    *fakeMailer
	replies []ThreadEntry
	reads   int
}

func (f *fakeThreads) Thread(_ context.Context, _, thread string) ([]ThreadEntry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reads++
	var out []ThreadEntry
	f.mail.mu.Lock()
	for i, m := range f.mail.sent {
		if m.Thread == thread {
			out = append(out, ThreadEntry{ID: fmt.Sprintf("msg-%d", i+1), From: m.From, Thread: thread, Labels: m.Labels, Body: m.Body})
		}
	}
	f.mail.mu.Unlock()
	for _, r := range f.replies {
		if r.Thread == thread {
			out = append(out, r)
		}
	}
	return out, nil
}

// reply is the lead's `amq reply` on the thread, with labels (and a kind).
func (f *fakeThreads) reply(from, thread, body, kind string, labels ...string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	id := fmt.Sprintf("r-%03d", len(f.replies)+1)
	f.replies = append(f.replies, ThreadEntry{ID: id, From: from, Thread: thread, Kind: kind, Labels: labels, Body: body + "\n"})
	return id
}

// phoneFixture is the handler fixture with the relay on: the body poster
// over the test relay, the fake threads, and a clock the test moves.
type phoneFixture struct {
	handlerFixture
	threads *fakeThreads
	clock   *time.Time
}

func newPhoneFixture(t *testing.T) phoneFixture {
	t.Helper()
	hf := newHandlerFixture(t)
	now := time.Now()
	pf := phoneFixture{handlerFixture: hf, threads: &fakeThreads{mail: hf.mail}, clock: &now}
	hf.h.Now = func() time.Time { return *pf.clock }
	hf.h.Poster = BodyPoster{Dial: hf.dial.dial, Pin: hf.core.Pin, Now: hf.h.Now}
	hf.h.Threads = pf.threads
	return pf
}

func (pf phoneFixture) advance(d time.Duration) { *pf.clock = pf.clock.Add(d) }

// ask is one owner DM (a reply to thread when set) through serve and the
// handler; it returns the event id and the request id of the receipt.
func (pf phoneFixture) ask(t *testing.T, content, thread string) (string, string) {
	t.Helper()
	id, msg := pf.dm(t, content, thread)
	pf.step(t)
	res, n := pf.serve.result(msg)
	m := acceptedRe.FindStringSubmatch(res)
	if n != 1 || m == nil {
		t.Fatalf("the receipt of %q: %q (%d)", content, res, n)
	}
	return id, m[1]
}

func (pf phoneFixture) relay(t *testing.T) []Relayed {
	t.Helper()
	got, err := pf.h.RelayStep(context.Background())
	if err != nil {
		t.Fatalf("relay step: %v", err)
	}
	return got
}

// relayPosts are the body's kind 9 posts that are relay posts (re <id>).
func (pf phoneFixture) relayPosts() []nostr.Event {
	var out []nostr.Event
	for _, e := range pf.mem.events(9, pf.body.PublicHex()) {
		if strings.HasPrefix(e.Content, "re q-") {
			out = append(out, e)
		}
	}
	return out
}

func (pf phoneFixture) record(t *testing.T, rid string) *status.RelayRecord {
	t.Helper()
	r, err := readRelayRecord(status.RelayDir(pf.h.Target.Root), rid)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func (pf phoneFixture) recordText(t *testing.T, rid string) string {
	t.Helper()
	raw, err := os.ReadFile(relayRecordPath(status.RelayDir(pf.h.Target.Root), rid))
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// Row 1: a lead reply on the cockpit thread is posted to the owner's DM
// through the test relay, starting with `re <request id>`, after
// redact.Block; the receipt is `accepted <request id>` at once and the
// question went to the lead on cockpit/<request id>.
func TestRelayPostsTheLeadsReplyToTheOwnersDM(t *testing.T) {
	const secret = "zebrarelay31value"
	canary := "GH_TOKEN=" + secret
	for _, re := range redact.RealCredentialShapes {
		if re.MatchString(canary) {
			t.Fatalf("the canary matches a real credential shape %s", re)
		}
	}
	if redact.Block(canary) == canary {
		t.Fatal("redact.Block does not redact the canary; pick another")
	}
	pf := newPhoneFixture(t)
	ev, rid := pf.ask(t, "is the build green?", "")
	if rid != RequestID(ev) || len(pf.mail.sent) != 1 || pf.mail.sent[0].Thread != CockpitThread(rid) || pf.mail.sent[0].To != "lead-a" {
		t.Fatalf("the question post: rid %s, sent %+v", rid, pf.mail.sent)
	}
	if got := pf.relay(t); len(got) != 0 || len(pf.relayPosts()) != 0 {
		t.Fatalf("a post before the lead replied: %+v", got)
	}
	pf.threads.reply("lead-b", CockpitThread(rid), "not the lead's thread", "") // another handle: never relayed
	reply := pf.threads.reply("lead-a", CockpitThread(rid), "The build is green.\nIt used "+canary+" to push.", "")
	got := pf.relay(t)
	posts := pf.relayPosts()
	if len(got) != 1 || got[0].Outcome != "posted" || got[0].ReplyID != reply || len(posts) != 1 {
		t.Fatalf("relayed %+v, posts %d", got, len(posts))
	}
	p := posts[0]
	if !strings.HasPrefix(p.Content, "re "+rid+"\nThe build is green.\n") || strings.Contains(p.Content, secret) || p.Content != redact.Block(p.Content) {
		t.Fatalf("the post: %q", p.Content)
	}
	if refs := nostr.ThreadRefs(p); !slices.Equal(refs, []string{ev}) || !slices.Equal(nostr.TagValues(p, "h"), []string{pf.core.Pin.Channel}) {
		t.Fatalf("the post is not in the request's DM conversation: refs %v", refs)
	}
	rec := pf.record(t, rid)
	if len(rec.Posts) != 1 || rec.Posts[0].SHA256 != sha256Hex(p.Content) || rec.RelayedAt == nil || rec.LastReply != reply {
		t.Fatalf("the record: %+v", rec)
	}
	if txt := pf.recordText(t, rid); strings.Contains(txt, "build is green") || strings.Contains(txt, secret) {
		t.Fatalf("the record carries the reply's text: %s", txt)
	}
	if d, open := pf.dial.counts(); open != 0 || d < 2 {
		t.Fatalf("connections: %d dials, %d open", d, open)
	}
	if again := pf.relay(t); len(again) != 0 || len(pf.relayPosts()) != 1 {
		t.Fatalf("posted twice: %+v", again)
	}
}

// Row 2: the progress rate bound. Five progress replies in a minute yield
// one progress post; the newest held one posts when the bound passes; two
// answers yield two posts; a result closes the request.
func TestRelayProgressRateBound(t *testing.T) {
	pf := newPhoneFixture(t)
	_, rid := pf.ask(t, "rebase and land #12", "")
	th := CockpitThread(rid)
	for i := 1; i <= 5; i++ {
		if i == 3 {
			pf.threads.reply("lead-a", th, "progress 3: kind status", "status") // the pi lead's amq_reply sets a kind
		} else {
			pf.threads.reply("lead-a", th, fmt.Sprintf("progress %d", i), "", LabelProgress)
		}
		pf.relay(t)
		pf.advance(12 * time.Second)
	}
	if posts := pf.relayPosts(); len(posts) != 1 || posts[0].Content != "re "+rid+"\nprogress 1" {
		t.Fatalf("five progress replies in a minute made %d posts: %+v", len(posts), posts)
	}
	pf.advance(ProgressBound)
	got := pf.relay(t)
	if posts := pf.relayPosts(); len(posts) != 2 || posts[1].Content != "re "+rid+"\nprogress 5" || len(got) != 1 || got[0].Outcome != "posted" {
		t.Fatalf("after the bound: %+v, relayed %+v", posts, got)
	}
	pf.threads.reply("lead-a", th, "answer one", "")
	pf.threads.reply("lead-a", th, "answer two", "")
	pf.relay(t)
	pf.threads.reply("lead-a", th, "landed #12", "", LabelResult)
	pf.relay(t)
	posts := pf.relayPosts()
	if len(posts) != 5 || posts[2].Content != "re "+rid+"\nanswer one" || posts[3].Content != "re "+rid+"\nanswer two" {
		t.Fatalf("two answers and a result: %d posts", len(posts))
	}
	rec := pf.record(t, rid)
	if !rec.Closed || len(rec.Posts) != 5 || len(rec.Seen) != 8 {
		t.Fatalf("the record after the result: closed %v, posts %d, seen %d", rec.Closed, len(rec.Posts), len(rec.Seen))
	}
	reads := pf.threads.reads
	if again := pf.relay(t); len(again) != 0 || pf.threads.reads != reads {
		t.Fatalf("a closed request is still followed: %+v", again)
	}
}

// Row 3: the gate-shaped refusal, one sub-row per form; the sentences
// that merely contain the word post unchanged; a relay post never lands in
// a gate question's thread.
func TestRelayRefusesGateShapedLines(t *testing.T) {
	refused := func(t *testing.T, pf phoneFixture, rid, line string) {
		t.Helper()
		before := len(pf.relayPosts())
		pf.threads.reply("lead-a", CockpitThread(rid), "Here is where we are.\n"+line, "")
		got := pf.relay(t)
		if len(got) != 1 || got[0].Outcome != "refused" || got[0].Reason != RefusedGateShape || len(pf.relayPosts()) != before {
			t.Fatalf("%q: relayed %+v", line, got)
		}
		rec := pf.record(t, rid)
		last := rec.Refusals[len(rec.Refusals)-1]
		if last.Reason != RefusedGateShape || last.SHA256 != sha256Hex(strings.TrimSpace(line)) || strings.Contains(pf.recordText(t, rid), line) {
			t.Fatalf("%q: the refusal record %+v", line, last)
		}
	}
	t.Run("whole-line answer forms", func(t *testing.T) {
		pf := newPhoneFixture(t)
		_, _, typed := raiseRel(t, pf.core, "rel-form")
		_, rid := pf.ask(t, "where are we?", "")
		for _, line := range []string{"yes", "No.", "Approve!", typed, "deny rel-form", "  YES rel-form release  "} {
			refused(t, pf, rid, line)
		}
	})
	t.Run("an open gate's id", func(t *testing.T) {
		pf := newPhoneFixture(t)
		raiseRel(t, pf.core, "rel-idform")
		_, rid := pf.ask(t, "where are we?", "")
		refused(t, pf, rid, "I raised rel-idform for the tag, it waits on you.")
	})
	t.Run("an open gate's question text", func(t *testing.T) {
		pf := newPhoneFixture(t)
		rec, _, _ := raiseRel(t, pf.core, "rel-text")
		_, rid := pf.ask(t, "where are we?", "")
		line := "for the record, digest: " + rec.Digest // the question's digest line, without the gate id
		if strings.Contains(line, "rel-text") {
			t.Fatal("the line names the gate id; the row would not isolate the text form")
		}
		refused(t, pf, rid, line)
	})
	t.Run("sentences with the word post unchanged", func(t *testing.T) {
		pf := newPhoneFixture(t)
		raiseRel(t, pf.core, "rel-open")
		_, rid := pf.ask(t, "where are we?", "")
		for _, line := range []string{"No, the build is green", "yes we can take t26 next"} {
			pf.threads.reply("lead-a", CockpitThread(rid), line, "")
			if got := pf.relay(t); len(got) != 1 || got[0].Outcome != "posted" {
				t.Fatalf("%q: relayed %+v", line, got)
			}
			posts := pf.relayPosts()
			if posts[len(posts)-1].Content != "re "+rid+"\n"+line {
				t.Fatalf("%q: posted %q", line, posts[len(posts)-1].Content)
			}
		}
	})
	t.Run("never into a gate question's thread", func(t *testing.T) {
		pf := newPhoneFixture(t)
		_, q, _ := raiseRel(t, pf.core, "rel-thread")
		// the owner asks inside the gate thread (a question, not an
		// answer): the request posts top level, never in that thread
		_, rid := pf.ask(t, "what does this gate cover?", q.EventID)
		if rec := pf.record(t, rid); rec.PostTo != "" {
			t.Fatalf("the request posts into %s", rec.PostTo)
		}
		pf.threads.reply("lead-a", CockpitThread(rid), "It covers the v3.0.0 tag.", "")
		pf.relay(t)
		posts := pf.relayPosts()
		if len(posts) != 1 || len(nostr.ThreadRefs(posts[0])) != 0 {
			t.Fatalf("the post's thread: %+v", posts)
		}
		// a record that names the gate question's thread is refused by the
		// poster's guard
		rec := pf.record(t, rid)
		rec.PostTo = q.EventID
		if err := writeRelayRecord(status.RelayDir(pf.h.Target.Root), rec, *pf.clock); err != nil {
			t.Fatal(err)
		}
		pf.threads.reply("lead-a", CockpitThread(rid), "More on the tag.", "")
		got := pf.relay(t)
		if len(got) != 1 || got[0].Outcome != "refused" || got[0].Reason != RefusedGateThread || len(pf.relayPosts()) != 1 {
			t.Fatalf("a post into the gate thread: %+v", got)
		}
		for _, e := range pf.mem.events(9, pf.body.PublicHex()) {
			if e.ID != q.EventID && slices.Contains(nostr.ThreadRefs(e), q.EventID) && strings.HasPrefix(e.Content, "re q-") {
				t.Fatalf("a relay post in the gate thread: %q", e.Content)
			}
		}
	})
}

// Row 4: the relay takes a poster interface, and the lead-side packages
// import neither the key path (the share's body key, nostr signing, the
// verifier's connection) nor the poster; the pi extension names no key
// path either.
func TestTheLeadSideHoldsNoKeyPathNorPoster(t *testing.T) {
	if f, ok := reflect.TypeOf(Handler{}).FieldByName("Poster"); !ok || f.Type.Kind() != reflect.Interface {
		t.Fatal("the relay's poster is not an interface")
	}
	goBin := filepath.Join(runtime.GOROOT(), "bin", "go")
	lead := []string{"status", "schedproc", "task", "scheduler", "piruntime", "leadsession"}
	args := []string{"list", "-deps"}
	for _, p := range lead {
		args = append(args, "github.com/omriariav/amq-squad/v3/internal/"+p)
	}
	cmd := exec.Command(goBin, args...)
	cmd.Env = append(os.Environ(), "GOFLAGS=-mod=readonly", "GOPROXY=off", "GOWORK=off")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go list: %v\n%s", err, out)
	}
	for _, dep := range strings.Fields(string(out)) {
		for _, keyPath := range []string{"/internal/buzz", "/internal/nostr", "/internal/gateverify"} {
			if strings.HasSuffix(dep, keyPath) {
				t.Errorf("a lead-side package depends on %s", dep)
			}
		}
	}
	files, _ := filepath.Glob(filepath.Join("..", "..", "pi", "extensions", "*.ts"))
	more, _ := filepath.Glob(filepath.Join("..", "..", "pi", "extensions", "lib", "*.ts"))
	if len(files)+len(more) < 5 {
		t.Fatalf("the pi extension's sources are missing: %v %v", files, more)
	}
	for _, f := range append(files, more...) {
		raw, _ := os.ReadFile(f)
		for _, k := range []string{"body.key", "extensions/remote/keys", "remote attach"} {
			if strings.Contains(string(raw), k) {
				t.Errorf("%s names %q", f, k)
			}
		}
	}
}

// Row 5: an attachment restarted after one relayed post posts nothing
// again and relays the next reply; a held progress survives the restart.
func TestARestartedAttachmentNeitherRepostsNorSkips(t *testing.T) {
	pf := newPhoneFixture(t)
	_, rid := pf.ask(t, "status of t26?", "")
	th := CockpitThread(rid)
	pf.threads.reply("lead-a", th, "t26 is in review", "")
	pf.threads.reply("lead-a", th, "working on it", "", LabelProgress)
	pf.relay(t)
	pf.threads.reply("lead-a", th, "still working", "", LabelProgress) // held by the bound
	pf.relay(t)
	if n := len(pf.relayPosts()); n != 2 {
		t.Fatalf("before the restart: %d posts", n)
	}
	// the restart: a new handler over the same roots, nothing in memory
	restarted := &Handler{Core: pf.core, Dial: pf.dial.dial, Inbox: pf.serve, Mail: pf.mail, Target: pf.h.Target, Now: pf.h.Now,
		Poster: BodyPoster{Dial: pf.dial.dial, Pin: pf.core.Pin, Now: pf.h.Now}, Threads: pf.threads}
	if got, err := restarted.RelayStep(context.Background()); err != nil || len(got) != 0 || len(pf.relayPosts()) != 2 {
		t.Fatalf("the restarted attachment posted again: %+v %v", got, err)
	}
	next := pf.threads.reply("lead-a", th, "t26 merged", "")
	got, err := restarted.RelayStep(context.Background())
	posts := pf.relayPosts()
	if err != nil || len(got) != 2 || got[1].ReplyID != next || got[1].Outcome != "posted" || posts[len(posts)-1].Content != "re "+rid+"\nt26 merged" {
		t.Fatalf("the next reply after the restart: %+v %v", got, err)
	}
	if got[0].Outcome != "superseded" {
		t.Fatalf("the held progress was not superseded by the answer: %+v", got[0])
	}
}

// Row 6: a gate question still goes by its own path and is not duplicated
// by the relay: a prepared release's two questions appear once each, even
// when the lead quotes them on the cockpit thread.
func TestAGateQuestionGoesByItsOwnPathOnce(t *testing.T) {
	pf := newPhoneFixture(t)
	tagRec, _, _ := raiseRel(t, pf.core, "rel-v310-tag")
	relRec, _, _ := raiseRel(t, pf.core, "rel-v310-release")
	_, rid := pf.ask(t, "is v3.1.0 ready?", "")
	th := CockpitThread(rid)
	pf.threads.reply("lead-a", th, "Prepared. The questions:\n\n"+tagRec.Text, "")
	pf.threads.reply("lead-a", th, relRec.Text, "", LabelResult)
	pf.threads.reply("lead-a", th, "Both gates are waiting on you in Buzz.", "")
	pf.relay(t)
	count := map[string]int{}
	for _, e := range pf.mem.events(9, pf.body.PublicHex()) {
		for _, r := range []gatestore.Record{tagRec, relRec} {
			if strings.Contains(e.Content, r.Text) {
				count[r.GateID]++
			}
		}
	}
	if count["rel-v310-tag"] != 1 || count["rel-v310-release"] != 1 {
		t.Fatalf("question copies on the relay: %v", count)
	}
	if posts := pf.relayPosts(); len(posts) != 1 || posts[0].Content != "re "+rid+"\nBoth gates are waiting on you in Buzz." {
		t.Fatalf("relay posts: %+v", posts)
	}
}

// Row 8: the conversation. Three owner DMs, the first fresh and the next
// two replies to the relayed posts, make one request id, one cockpit
// thread with three lead turns, and three relayed posts each starting with
// the same `re <request id>`; a fourth DM that is not a reply opens a
// second request. A reply to a relayed post that is itself a whole-line
// answer form reaches the lead as text, and the gate stays open.
func TestThePhoneConversation(t *testing.T) {
	pf := newPhoneFixture(t)
	gate, _, typed := raiseRel(t, pf.core, "rel-conv")
	_, rid := pf.ask(t, "@lead-b what is left for 3.1?", "")
	th := CockpitThread(rid)
	if len(pf.mail.sent) != 2 || pf.mail.sent[1].To != "lead-b" || pf.mail.sent[1].Thread != th {
		t.Fatalf("the question: %+v", pf.mail.sent)
	}
	pf.threads.reply("lead-b", th, "t26 and the notes.", "")
	pf.relay(t)
	p1 := pf.relayPosts()[0]
	if _, again := pf.ask(t, "who has t26?", p1.ID); again != rid {
		t.Fatalf("a reply to a relayed post opened %s, not %s", again, rid)
	}
	pf.threads.reply("lead-b", th, "codex-t26 has it.", "")
	pf.relay(t)
	p2 := pf.relayPosts()[1]
	if _, again := pf.ask(t, typed, p2.ID); again != rid {
		t.Fatalf("the third turn opened %s", again)
	}
	if st := pf.core.Store.State(gate, time.Now()); st != gatestore.StateOpen {
		t.Fatalf("an answer form in the conversation reached the gate path: %s", st)
	}
	pf.threads.reply("lead-b", th, "That is not how a gate is answered; answer in its thread.", "")
	pf.relay(t)
	posts := pf.relayPosts()
	if len(posts) != 3 {
		t.Fatalf("%d relay posts", len(posts))
	}
	for _, p := range posts {
		if !strings.HasPrefix(p.Content, "re "+rid+"\n") {
			t.Fatalf("a post does not start with re %s: %q", rid, p.Content)
		}
	}
	var turns []Mail
	for _, m := range pf.mail.sent {
		if m.To == "lead-b" {
			turns = append(turns, m)
		}
	}
	if len(turns) != 3 || turns[0].Thread != th || turns[1].Thread != th || turns[2].Thread != th || turns[2].Body != typed {
		t.Fatalf("lead turns: %+v", turns)
	}
	if rec := pf.record(t, rid); len(rec.Turns) != 3 || len(rec.Posts) != 3 || rec.Lead != "lead-b" || rec.Route != "lead-b" {
		t.Fatalf("the record: %+v", rec)
	}
	_, rid2 := pf.ask(t, "and the release date?", "")
	if rid2 == rid {
		t.Fatal("a fresh DM continued the request")
	}
	recs, _ := loadRelayRecords(status.RelayDir(pf.h.Target.Root))
	if len(recs) != 2 {
		t.Fatalf("%d requests", len(recs))
	}
}

// The gate path keeps an answer in a gate question's thread even when the
// event also replies to a relay post (a reply in that thread, as today).
func TestAnAnswerInTheGateThreadStaysOnTheGatePath(t *testing.T) {
	pf := newPhoneFixture(t)
	rec, q, typed := raiseRel(t, pf.core, "rel-keep")
	_, rid := pf.ask(t, "what is rel-keep for?", "")
	pf.threads.reply("lead-a", CockpitThread(rid), "It tags v3.1.0.", "")
	pf.relay(t)
	post := pf.relayPosts()[0]
	e := nostr.Event{CreatedAt: time.Now().Unix(), Kind: 9, Content: typed,
		Tags: [][]string{{"h", pf.core.Pin.Channel}, {"p", pf.body.PublicHex()}, {"e", q.EventID, "", "root"}, {"e", post.ID, "", "reply"}}}
	if err := pf.owner.sk.Sign(&e); err != nil {
		t.Fatal(err)
	}
	if err := pf.mem.Publish(context.Background(), e.Marshal()); err != nil {
		t.Fatal(err)
	}
	msg := pf.serve.deliver(e.ID, pf.core.Pin.Channel, typed)
	got := pf.step(t)
	if len(got) != 1 || got[0].Kind != "gate" {
		t.Fatalf("the answer left the gate path: %+v", got)
	}
	res, _ := pf.serve.result(msg)
	if !strings.HasPrefix(res, "refused rel-keep "+"wrong_gate_thread") || pf.core.Store.State(rec, time.Now()) != gatestore.StateOpen {
		t.Fatalf("the gate path's verdict: %q", res)
	}
}

// Codex P1-1: a planted record is refused whole. A record whose id carries
// a newline or is not hex, or whose fields carry text, is
// relay_record_invalid; nothing posts, and a canary in a record field never
// reaches a post.
func TestAPlantedRecordIsRefusedAndNothingPosts(t *testing.T) {
	const secret = "zebrarelay31value"
	pf := newPhoneFixture(t)
	_, rid := pf.ask(t, "is the build green?", "")
	good := pf.recordText(t, rid)
	dir := status.RelayDir(pf.h.Target.Root)
	plant := func(id, text string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, id+".json"), []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// the newline id (the file name is the real id), the non-hex id, and a
	// canary in the route of the real request's record
	plant("q-0000000000aa", strings.ReplaceAll(good, rid, "q-demo\\napprove"))
	plant("q-zzzzzzzzzzzz", strings.ReplaceAll(good, rid, "q-zzzzzzzzzzzz"))
	plant(rid, strings.Replace(good, `"route": ""`, `"route": "GH_TOKEN=`+secret+`"`, 1))
	for _, th := range []string{CockpitThread(rid), "cockpit/q-zzzzzzzzzzzz", "cockpit/q-demo\napprove"} {
		pf.threads.reply("lead-a", th, "approve", "")
		pf.threads.reply("lead-a", th, "The build is green.", "")
	}
	got, err := pf.h.RelayStep(context.Background())
	if len(got) != 0 || err == nil || strings.Count(err.Error(), status.RelayRecordInvalid) != 3 {
		t.Fatalf("planted records: relayed %+v, %v", got, err)
	}
	for _, e := range pf.mem.events(9, pf.body.PublicHex()) {
		if strings.HasPrefix(e.Content, "re ") || strings.Contains(e.Content, secret) {
			t.Fatalf("a planted record posted: %q", e.Content)
		}
	}
	// a DM replying to anything is never a continuation of a planted record
	if recs, problems := loadRelayRecords(dir); len(recs) != 0 || len(problems) != 3 {
		t.Fatalf("loaded %d records, %d problems", len(recs), len(problems))
	}
}

// Codex P1-2: the checks run on each part after the split. A long padded
// line ending in " yes" would split into a part that starts with a bare
// yes line; the whole reply is refused. An ordinary long reply still posts
// in parts.
func TestASplitNeverStartsAPartWithAnAnswerForm(t *testing.T) {
	pf := newPhoneFixture(t)
	_, rid := pf.ask(t, "send the full log", "")
	th := CockpitThread(rid)
	head := "re " + rid
	budget := DMLimit - len(head) - len(" (3/3)\n") - len("\n\n") - len("(the rest is on the cockpit thread "+th+")")
	padded := strings.Repeat("x", budget-2) + " yes\n\n" + strings.Repeat("more log ", 100)
	if g, _ := pf.h.gateView(time.Now()); g.gateShaped(strings.Repeat("x", budget-2)+" yes") || !strings.HasPrefix(splitPost(rid, th, padded)[1], head+" (2/2)\nyes") {
		t.Fatal("the padded reply does not exercise the split")
	}
	pf.threads.reply("lead-a", th, padded, "")
	got := pf.relay(t)
	if len(got) != 1 || got[0].Outcome != "refused" || got[0].Reason != RefusedGateShape || len(pf.relayPosts()) != 0 {
		t.Fatalf("the padded reply: %+v, %d posts", got, len(pf.relayPosts()))
	}
	long := strings.TrimSpace(strings.Repeat(strings.Repeat("word ", 300)+"\n\n", 4))
	pf.threads.reply("lead-a", th, long, "")
	got = pf.relay(t)
	if posts := pf.relayPosts(); len(got) != 1 || got[0].Outcome != "posted" || len(posts) != 2 || !strings.HasPrefix(posts[1].Content, head+" (2/2)\n") {
		t.Fatalf("an ordinary long reply: %+v, %d posts", got, len(posts))
	}
}

// Codex P1-3 and the plan reviewer: a crash between publish and the
// event-id write is a named state, not a duplicate. The intent is written
// before the publish; a restart publishes that part once more at most; a
// second crash there leaves relay_post_unconfirmed in status, and no later
// tick publishes it again on its own (the owner may have received the
// first attempt). The owner's next turn on the request clears the state
// and the conversation goes on.
func TestACrashBetweenPublishAndEventWriteIsANamedState(t *testing.T) {
	pf := newPhoneFixture(t)
	_, rid := pf.ask(t, "is the build green?", "")
	th := CockpitThread(rid)
	pf.threads.reply("lead-a", th, "Checking.", "")
	pf.relay(t)
	first := pf.relayPosts()[0]
	pf.threads.reply("lead-a", th, "Green.", "")
	crash := errors.New("the attachment died after the publish")
	relayAfterPublish = func() error { return crash }
	t.Cleanup(func() { relayAfterPublish = nil })
	if _, err := pf.h.RelayStep(context.Background()); err == nil || !strings.Contains(err.Error(), crash.Error()) {
		t.Fatalf("the crash: %v", err)
	}
	restarted := func() *Handler {
		return &Handler{Core: pf.core, Dial: pf.dial.dial, Inbox: pf.serve, Mail: pf.mail, Target: pf.h.Target, Now: pf.h.Now,
			Poster: BodyPoster{Dial: pf.dial.dial, Pin: pf.core.Pin, Now: pf.h.Now}, Threads: pf.threads}
	}
	_, _ = restarted().RelayStep(context.Background()) // the one retry, and it crashes too
	relayAfterPublish = nil
	got, err := restarted().RelayStep(context.Background())
	if err != nil || len(got) != 1 || got[0].Outcome != "refused" || got[0].Reason != status.RelayPostUnconfirmed {
		t.Fatalf("the second restart: %+v %v", got, err)
	}
	for i := 0; i < 3; i++ {
		pf.advance(time.Minute)
		if again, _ := restarted().RelayStep(context.Background()); len(again) != 0 {
			t.Fatalf("a later tick acted on the unconfirmed post: %+v", again)
		}
	}
	if n := len(pf.relayPosts()); n != 3 {
		t.Fatalf("%d posts: the first answer and the crashed one twice (at most one extra)", n)
	}
	requests := func() []status.Request {
		return status.Collect(status.CollectOptions{Root: pf.h.Target.Root, Session: "s", Now: time.Now()}).Requests
	}
	if r := requests(); len(r) != 1 || !slices.Equal(r[0].Refused, []string{status.RelayPostUnconfirmed}) {
		t.Fatalf("status does not name the unconfirmed post: %+v", r)
	}
	// the owner's next turn clears it; the conversation goes on
	if _, again := pf.ask(t, "and the deploy?", first.ID); again != rid {
		t.Fatalf("the next turn opened %s", again)
	}
	if r := requests(); len(r) != 1 || len(r[0].Refused) != 0 {
		t.Fatalf("the next turn did not clear the state: %+v", r)
	}
	pf.threads.reply("lead-a", th, "Deployed.", "")
	if got := pf.relay(t); len(got) != 1 || got[0].Outcome != "posted" || len(pf.relayPosts()) != 4 {
		t.Fatalf("the conversation after the clear: %+v", got)
	}
	if rec := pf.record(t, rid); rec.Refusals[0].ClearedAt == nil || rec.Posting != nil {
		t.Fatalf("the record: %+v", rec.Refusals)
	}
	// one crash only: the restart publishes once more and records it
	pf2 := newPhoneFixture(t)
	_, rid2 := pf2.ask(t, "and now?", "")
	pf2.threads.reply("lead-a", CockpitThread(rid2), "Still green.", "")
	relayAfterPublish = func() error { return crash }
	_, _ = pf2.h.RelayStep(context.Background())
	relayAfterPublish = nil
	got, err = pf2.h.RelayStep(context.Background())
	if err != nil || len(got) != 1 || got[0].Outcome != "posted" || len(pf2.relayPosts()) != 2 {
		t.Fatalf("one crash: %+v %v, %d posts", got, err, len(pf2.relayPosts()))
	}
	if again := pf2.relay(t); len(again) != 0 || len(pf2.relayPosts()) != 2 {
		t.Fatalf("posted again after the record: %+v", again)
	}
}

// Codex P1-3: a record store that fails publishes nothing (no durable
// intent, no publish); once it writes again, the reply posts once.
func TestAFailingRecordStorePostsNothing(t *testing.T) {
	pf := newPhoneFixture(t)
	_, rid := pf.ask(t, "is the build green?", "")
	pf.threads.reply("lead-a", CockpitThread(rid), "Green.", "")
	relayRecordWrite = func() error { return errors.New("disk full") }
	t.Cleanup(func() { relayRecordWrite = nil })
	for i := 0; i < 3; i++ {
		if _, err := pf.h.RelayStep(context.Background()); err == nil {
			t.Fatal("a failing record store reported no error")
		}
	}
	if n := len(pf.relayPosts()); n != 0 {
		t.Fatalf("%d posts with a failing record store", n)
	}
	relayRecordWrite = nil
	if got := pf.relay(t); len(got) != 1 || got[0].Outcome != "posted" || len(pf.relayPosts()) != 1 {
		t.Fatalf("after the store recovered: %+v", got)
	}
}

// flakyPoster fails the publishes whose call numbers are in fail (an
// ambiguous failure: the relay may have the event) or in before (a failure
// before anything was written, ErrNotSent).
type flakyPoster struct {
	inner        Poster
	calls        int
	fail, before map[int]bool
}

func (f *flakyPoster) Post(ctx context.Context, text, replyTo string) (string, error) {
	f.calls++
	if f.before[f.calls] {
		return "", notSent(errors.New("the relay did not answer the dial"))
	}
	if f.fail[f.calls] {
		return "", errors.New("relay closed the connection")
	}
	return f.inner.Post(ctx, text, replyTo)
}

// The plan reviewer on publish: a failed post keeps its try, since the
// relay writes the event before it awaits the acknowledgment
// (gateverify/relay.go WSRelay.Publish), so a timeout after the send may
// follow an accepted event. An ambiguous failure gets at most two attempts
// in all and then relay_post_unconfirmed; a failure before anything was
// written (ErrNotSent) does not consume a try.
func TestAFailedPostKeepsItsTryUnlessNothingWasSent(t *testing.T) {
	pf := newPhoneFixture(t)
	flaky := &flakyPoster{inner: pf.h.Poster, fail: map[int]bool{1: true, 2: true, 3: true, 4: true}}
	pf.h.Poster = flaky
	_, rid := pf.ask(t, "is the build green?", "")
	pf.threads.reply("lead-a", CockpitThread(rid), "Green.", "")
	var last []Relayed
	for i := 0; i < 5; i++ {
		last, _ = pf.h.RelayStep(context.Background())
	}
	if flaky.calls != 2 {
		t.Fatalf("an ambiguous failure was attempted %d times, want 2 in all", flaky.calls)
	}
	if rec := pf.record(t, rid); len(rec.Refusals) != 1 || rec.Refusals[0].Reason != status.RelayPostUnconfirmed || rec.Posting != nil {
		t.Fatalf("after two ambiguous failures: %+v (last %+v)", rec.Refusals, last)
	}
	pf2 := newPhoneFixture(t)
	flaky2 := &flakyPoster{inner: pf2.h.Poster, before: map[int]bool{1: true, 2: true, 3: true}}
	pf2.h.Poster = flaky2
	_, rid2 := pf2.ask(t, "is the build green?", "")
	pf2.threads.reply("lead-a", CockpitThread(rid2), "Green.", "")
	for i := 0; i < 3; i++ {
		_, _ = pf2.h.RelayStep(context.Background())
		if rec := pf2.record(t, rid2); rec.Posting == nil || rec.Posting.Tries != 0 {
			t.Fatalf("a failure before the send consumed a try: %+v", rec.Posting)
		}
	}
	if got := pf2.relay(t); len(got) != 1 || got[0].Outcome != "posted" || flaky2.calls != 4 || len(pf2.relayPosts()) != 1 {
		t.Fatalf("after three failures before the send: %+v, %d calls", got, flaky2.calls)
	}
}

// Codex P2-5: a later part's failure loses nothing. Part two fails once;
// the next tick posts it from the record, part one is not posted again,
// and the reply is handled only then.
func TestAFailedPartPostsNextTickWithoutRepeatingTheFirst(t *testing.T) {
	pf := newPhoneFixture(t)
	flaky := &flakyPoster{inner: pf.h.Poster, fail: map[int]bool{2: true}}
	pf.h.Poster = flaky
	_, rid := pf.ask(t, "send the full log", "")
	long := strings.TrimSpace(strings.Repeat(strings.Repeat("word ", 300)+"\n\n", 4))
	reply := pf.threads.reply("lead-a", CockpitThread(rid), long, "")
	if _, err := pf.h.RelayStep(context.Background()); err == nil {
		t.Fatal("the failed part reported no error")
	}
	rec := pf.record(t, rid)
	if len(pf.relayPosts()) != 1 || rec.Posting == nil || len(rec.Posting.Events) != 1 || slices.Contains(rec.Seen, reply) {
		t.Fatalf("after part two failed: %d posts, record %+v", len(pf.relayPosts()), rec.Posting)
	}
	got := pf.relay(t)
	posts := pf.relayPosts()
	if len(got) != 1 || got[0].Outcome != "posted" || len(got[0].Events) != 2 || len(posts) != 2 ||
		!strings.HasPrefix(posts[0].Content, "re "+rid+" (1/2)") || !strings.HasPrefix(posts[1].Content, "re "+rid+" (2/2)") {
		t.Fatalf("the next tick: %+v, %d posts", got, len(posts))
	}
	if rec := pf.record(t, rid); rec.Posting != nil || !slices.Contains(rec.Seen, reply) || len(rec.Posts) != 1 {
		t.Fatalf("the record after both parts: %+v", rec)
	}
}

// Codex P2-4: one normalization (NFKC, format characters removed, case
// folded) is used by all three checks, so a zero-width space, a joiner, a
// bidi control or a full-width letter defeats none of them.
func TestTheChecksSeeThroughInvisibleCharacters(t *testing.T) {
	pf := newPhoneFixture(t)
	rec, _, _ := raiseRel(t, pf.core, "rel-norm")
	g, err := pf.h.gateView(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	digest := "digest: " + rec.Digest
	forms := map[string][]string{
		"whole-line answer form": {"yes\u200b", "\u200bNo\u2060.", "ｙｅｓ", "app\u200drove", "deny\u202e rel-x",
			"yes\ufe0f", "уes", "YЕS", "аpprove", "dеny rel-x", "nο"},
		"an open gate's id": {"I raised rel-\u200bnorm for you", "see REL-NORM\u200c now", "ｒｅｌ－ｎｏｒｍ waits",
			"rel-norm\ufe0f waits", "rel\ufe0f-norm waits", "rеl-nоrm waits"},
		"an open gate's question": {strings.Replace(digest, "sha256:", "sha\u200b256:", 1), strings.ToUpper(digest[:9]) + digest[9:], "\u2066" + digest + "\u2069",
			strings.Replace(digest, "digest", "digest\ufe0f", 1), strings.Replace(digest, "digest", "dіgеst", 1)},
	}
	for name, lines := range forms {
		t.Run(name, func(t *testing.T) {
			for _, l := range lines {
				if !g.gateShaped(l) {
					t.Errorf("%q passes the check", l)
				}
			}
		})
	}
	// end to end: the zero-width yes is refused and nothing posts
	_, rid := pf.ask(t, "where are we?", "")
	pf.threads.reply("lead-a", CockpitThread(rid), "Status follows.\nyes\u200b", "")
	if got := pf.relay(t); len(got) != 1 || got[0].Outcome != "refused" || len(pf.relayPosts()) != 0 {
		t.Fatalf("the zero-width yes: %+v", got)
	}
	// and an ordinary sentence still posts
	pf.threads.reply("lead-a", CockpitThread(rid), "No\u200b, the build is green", "")
	if got := pf.relay(t); len(got) != 1 || got[0].Outcome != "posted" {
		t.Fatalf("the sentence: %+v", got)
	}
}

// Codex P2-6: the request's record is written before the question goes to
// the lead and before the DM is marked handled. A record that cannot be
// written is refused by name, posts nothing to the lead and leaves the DM
// unhandled, so serve's redelivery is accepted; a delivery that fails takes
// the record back.
func TestTheRecordComesBeforeTheQuestion(t *testing.T) {
	pf := newPhoneFixture(t)
	relayRecordWrite = func() error { return errors.New("disk full") }
	t.Cleanup(func() { relayRecordWrite = nil })
	ev, msg := pf.dm(t, "is the build green?", "")
	pf.step(t)
	if res, _ := pf.serve.result(msg); res != "refused - "+RelayRecordFailed || len(pf.mail.sent) != 0 {
		t.Fatalf("a failed record: %q, sent %d", res, len(pf.mail.sent))
	}
	relayRecordWrite = nil
	again := pf.serve.deliver(ev, pf.core.Pin.Channel, "is the build green?")
	pf.step(t)
	if res, _ := pf.serve.result(again); !strings.HasPrefix(res, "accepted "+RequestID(ev)+" ") || len(pf.mail.sent) != 1 {
		t.Fatalf("the redelivery: %q, sent %d", res, len(pf.mail.sent))
	}
	if rec := pf.record(t, RequestID(ev)); len(rec.Turns) != 1 || rec.Turns[0].MessageID != "msg-1" {
		t.Fatalf("the record: %+v", rec.Turns)
	}
	// a delivery that fails leaves no record behind
	pf.mail.err = errors.New("amq down")
	ev2, msg2 := pf.dm(t, "and the train?", "")
	pf.step(t)
	if res, _ := pf.serve.result(msg2); res != "refused - not_delivered" {
		t.Fatalf("a failed delivery: %q", res)
	}
	if _, err := os.Stat(relayRecordPath(status.RelayDir(pf.h.Target.Root), RequestID(ev2))); !os.IsNotExist(err) {
		t.Fatalf("a failed delivery left its record: %v", err)
	}
}

// Codex P2-7: every amq a buzz test runs goes through amqCmd (a scratch
// HOME and XDG directories, AMQ_NO_UPDATE_CHECK=1); no test file execs amq
// another way.
func TestTestsExecAMQOnlyThroughTheHelper(t *testing.T) {
	direct := regexp.MustCompile(`exec\.Command(Context)?\((ctx, |[a-zA-Z]+Ctx, |context\.Background\(\), )?"amq"`)
	files, _ := filepath.Glob("*_test.go")
	for _, f := range files {
		raw, _ := os.ReadFile(f)
		for i, line := range strings.Split(string(raw), "\n") {
			if direct.MatchString(line) && !(f == "helpers_test.go" && strings.Contains(line, "exec.CommandContext(ctx, \"amq\", args...)")) {
				t.Errorf("%s:%d execs amq outside amqCmd: %s", f, i+1, strings.TrimSpace(line))
			}
		}
	}
	env := amqEnv(t, nil)
	got := map[string]string{}
	for _, kv := range env {
		k, v, _ := strings.Cut(kv, "=")
		got[k] = v
	}
	real, _ := os.UserHomeDir()
	if got["AMQ_NO_UPDATE_CHECK"] != "1" || got["HOME"] == "" || got["HOME"] == real || !strings.HasPrefix(got["XDG_CACHE_HOME"], got["HOME"]) || !strings.HasPrefix(got["XDG_CONFIG_HOME"], got["HOME"]) {
		t.Fatalf("the helper's environment: HOME %q, XDG cache %q, update check %q", got["HOME"], got["XDG_CACHE_HOME"], got["AMQ_NO_UPDATE_CHECK"])
	}
}

// The plan reviewer: the question-text check skips a question line shorter
// than MinQuestionLine, so a reply with a short common word of an open
// question posts, while a reply carrying a full question line is refused.
func TestAShortQuestionLineDoesNotRefuseOrdinaryReplies(t *testing.T) {
	pf := newPhoneFixture(t)
	rec, _, _ := raiseRel(t, pf.core, "rel-short")
	g, err := pf.h.gateView(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	// a question line as short as a bare tag, as a gate target can be
	g.lines = append(g.lines, normalizeLine("v3.1"), normalizeLine("yes or no"))
	for _, ok := range []string{"we ship v3.1 today", "answer yes or no later, no rush"} {
		if g.gateShaped(ok) {
			t.Errorf("%q is refused by a short question line", ok)
		}
	}
	if !g.gateShaped("pasted: digest: " + rec.Digest) {
		t.Error("a full question line passes")
	}
}

// Codex re-check #2: redaction runs over the whole body before the split.
// A quoted assignment whose value the split would cut (neither half matches
// the redactor alone) is absent from every part.
func TestACanaryAcrossTheSplitBoundaryIsRedacted(t *testing.T) {
	value := strings.Repeat("zebra ", 900) // 5,400 characters, no credential shape
	canary := `GH_TOKEN="` + value + `"`
	for _, re := range redact.RealCredentialShapes {
		if re.MatchString(canary) {
			t.Fatalf("the canary matches a real credential shape %s", re)
		}
	}
	spans, leaks := 0, false
	for _, p := range splitPost("q-0123456789ab", "t", "Here it is.\n\n"+canary) {
		if strings.Contains(p, "zebra") {
			spans++
			leaks = leaks || strings.Contains(redact.Block(p), "zebra")
		}
	}
	if spans < 2 || !leaks {
		t.Fatal("the canary does not span the split, or every part alone is redacted; the row would not isolate the order")
	}
	pf := newPhoneFixture(t)
	_, rid := pf.ask(t, "send the deploy env", "")
	pf.threads.reply("lead-a", CockpitThread(rid), "Here it is.\n\n"+canary+"\n\n"+strings.TrimSpace(strings.Repeat(strings.Repeat("log ", 400)+"\n\n", 3)), "")
	got := pf.relay(t)
	posts := pf.relayPosts()
	if len(got) != 1 || got[0].Outcome != "posted" || len(posts) < 2 {
		t.Fatalf("relayed %+v, %d posts", got, len(posts))
	}
	for _, p := range posts {
		if strings.Contains(p.Content, "zebra") {
			t.Fatalf("a part carries the value: %q", p.Content[:80])
		}
	}
}

// failStoreOn makes the record store fail on its nth operation from now
// (a write or a removal), once.
func failStoreOn(t *testing.T, n int) {
	t.Helper()
	calls := 0
	relayRecordWrite = func() error {
		calls++
		if calls == n {
			return errors.New("disk full")
		}
		return nil
	}
	t.Cleanup(func() { relayRecordWrite = nil })
}

// Codex re-check #5: a record whose turn was never delivered (a crash
// between the record write and the delivery) is taken back on the next
// step: a fresh request's record is removed (the relay never re-sends the
// question), a continuation's turn is dropped. A rollback that fails after
// a failed delivery is named, and the next step recovers the orphan.
func TestAnUndeliveredTurnIsTakenBackAndARollbackFailureIsNamed(t *testing.T) {
	pf := newPhoneFixture(t)
	dir := status.RelayDir(pf.h.Target.Root)
	// the crash: the record is written, the question never sent
	orphan := pf.ownerDM(t, "is the build green?", "")
	e, _ := pf.mem.FetchByID(context.Background(), orphan)
	ev, _ := nostr.ParseEvent(e)
	if _, _, err := pf.h.openTurn(ev, RequestID(orphan), nil, *pf.clock); err != nil {
		t.Fatal(err)
	}
	got := pf.relay(t)
	if len(got) != 1 || got[0].Outcome != "recovered" || got[0].Reason != RelayTurnUndelivered {
		t.Fatalf("the orphan: %+v", got)
	}
	if _, err := os.Stat(relayRecordPath(dir, RequestID(orphan))); !os.IsNotExist(err) || len(pf.mail.sent) != 0 {
		t.Fatalf("the orphan's record is still there (%v) or a question went out (%d)", err, len(pf.mail.sent))
	}
	// a continuation's undelivered turn is dropped, the request kept
	_, rid := pf.ask(t, "and the train?", "")
	pf.threads.reply("lead-a", CockpitThread(rid), "Running.", "")
	pf.relay(t)
	post := pf.relayPosts()[0]
	cont := pf.ownerDM(t, "and after it?", post.ID)
	raw, _ := pf.mem.FetchByID(context.Background(), cont)
	cev, _ := nostr.ParseEvent(raw)
	if _, _, err := pf.h.openTurn(cev, rid, pf.record(t, rid), *pf.clock); err != nil {
		t.Fatal(err)
	}
	if got := pf.relay(t); len(got) != 1 || got[0].Outcome != "recovered" || len(pf.record(t, rid).Turns) != 1 {
		t.Fatalf("the continuation's orphan: %+v, turns %d", got, len(pf.record(t, rid).Turns))
	}
	// a failed delivery whose rollback fails: named; the next step recovers
	pf.mail.err = errors.New("amq down")
	failStoreOn(t, 2) // 1: the record write, 2: the rollback's removal
	_, msg := pf.dm(t, "and the release?", "")
	handled := pf.step(t)
	if res, _ := pf.serve.result(msg); res != "refused - not_delivered" || len(handled) != 1 || !strings.Contains(handled[0].Error, RelayRollbackFailed) {
		t.Fatalf("the failed rollback: %q, %+v", res, handled)
	}
	relayRecordWrite = nil
	pf.mail.err = nil
	if got := pf.relay(t); len(got) != 1 || got[0].Outcome != "recovered" {
		t.Fatalf("the rollback's orphan: %+v", got)
	}
	if recs, _ := loadRelayRecords(dir); len(recs) != 1 {
		t.Fatalf("%d records after the recovery, want the one delivered request", len(recs))
	}
}

// A post longer than one DM is split at paragraph boundaries with (i/n)
// marks, at most three parts; the rest is replaced by one line naming the
// cockpit thread.
func TestALongReplyIsSplitIntoAtMostThreeParts(t *testing.T) {
	para := strings.Repeat("word ", 300) // 1500 bytes
	body := strings.TrimSpace(strings.Repeat(para+"\n\n", 12))
	parts := splitPost("q-0123456789ab", "cockpit/q-0123456789ab", body)
	if len(parts) != 3 {
		t.Fatalf("%d parts", len(parts))
	}
	for i, p := range parts {
		if len(p) > DMLimit || !strings.HasPrefix(p, fmt.Sprintf("re q-0123456789ab (%d/3)\n", i+1)) {
			t.Fatalf("part %d: %d bytes, %q", i+1, len(p), p[:40])
		}
	}
	if !strings.HasSuffix(parts[2], "(the rest is on the cockpit thread cockpit/q-0123456789ab)") {
		t.Fatalf("the last part: %q", parts[2][len(parts[2])-80:])
	}
	if one := splitPost("q-0123456789ab", "t", "short"); len(one) != 1 || one[0] != "re q-0123456789ab\nshort" {
		t.Fatalf("a short post: %q", one)
	}
	two := splitPost("q-0123456789ab", "t", para+"\n\n"+para+"\n\n"+para)
	if len(two) != 2 || strings.Contains(two[1], "the rest is") {
		t.Fatalf("a post of two parts: %d", len(two))
	}
}

// The relay records are JSON a status reader decodes strictly.
func TestARelayRecordIsAStatusRelayRecord(t *testing.T) {
	pf := newPhoneFixture(t)
	_, rid := pf.ask(t, "hello", "")
	dec := json.NewDecoder(strings.NewReader(pf.recordText(t, rid)))
	dec.DisallowUnknownFields()
	var r status.RelayRecord
	if err := dec.Decode(&r); err != nil || r.Schema != status.RelayRecordSchema || r.Thread != CockpitThread(rid) || r.RootEvent == "" || r.PostTo != r.RootEvent {
		t.Fatalf("record %+v, %v", r, err)
	}
	if fi, err := os.Stat(relayRecordPath(status.RelayDir(pf.h.Target.Root), rid)); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("record mode: %v %v", fi, err)
	}
}

// AMQThreads and the thread on the question post through the real amq CLI
// in a scratch root (the installed one is enough).
func TestAMQThreadsThroughTheCLI(t *testing.T) {
	if _, err := exec.LookPath("amq"); err != nil {
		t.Skip("amq not installed")
	}
	amqProcess(t)
	ctx := context.Background()
	root := filepath.Join(realTempDir(t), "root")
	if out, err := amqCmd(ctx, t, nil, "init", "--root", root, "--agents", "buzz,cto").CombinedOutput(); err != nil {
		t.Fatalf("amq init: %v\n%s", err, out)
	}
	id, err := AMQMailer{}.Send(ctx, Mail{Root: root, From: CarrierHandle, To: "cto", Body: "is the build green?", Labels: []string{"buzz"}, Thread: "cockpit/q-0123456789ab"})
	if err != nil {
		t.Fatal(err)
	}
	reply := amqCmd(ctx, t, nil, "reply", "--root", root, "--me", "cto", "--id", id, "--labels", LabelProgress, "--body", "-", "--json")
	reply.Stdin = strings.NewReader("building")
	if out, err := reply.CombinedOutput(); err != nil {
		t.Fatalf("amq reply: %v\n%s", err, out)
	}
	got, err := AMQThreads{}.Thread(ctx, root, "cockpit/q-0123456789ab")
	if err != nil || len(got) != 2 || got[0].ID != id || got[1].From != "cto" || strings.TrimSpace(got[1].Body) != "building" || replyKind(got[1]) != "progress" {
		t.Fatalf("thread = %+v, %v", got, err)
	}
}
