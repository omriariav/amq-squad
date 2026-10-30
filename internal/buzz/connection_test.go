package buzz

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/gatestore"
	"github.com/omriariav/amq-squad/v3/internal/gateverify"
)

// enrolledOnRelay is an enrolled share under ~/.amq-squad/buzz and a
// binding to a local websocket relay, and a core over the share's store.
func enrolledOnRelay(t *testing.T) (*wsRelay, gateverify.Binding, gateverify.Pin, coreFixture) {
	t.Helper()
	buzzDir := buzzHome(t)
	relay := newWSRelay(t)
	f := newCoreFixture(t, nil)
	root := filepath.Join(buzzDir, "root")
	writeShare(t, root, DefaultShareSession, f.owner, f.body, DefaultKinds, time.Now().Add(24*time.Hour))
	b := gateverify.Binding{Schema: gateverify.BindingSchema, Relay: relay.url(), Channel: f.core.Pin.Channel, Owner: f.owner.pubHex(), Body: f.body.PublicHex(),
		Carriers: []string{CarrierHandle}, ShareRoot: root, ShareSession: DefaultShareSession}
	f.core.RelayURL = relay.url()
	return relay, b, f.core.Pin, f
}

// Acceptance 3 on a websocket relay: the verifier's fetch is one REQ by
// event id on a fresh connection, closed after: one connection open during
// the verification, none after. A stalled fetch is capped and yields the
// attention row and a status line, not a result.
func TestVerifierConnectionOnTheRelay(t *testing.T) {
	relay, b, pin, f := enrolledOnRelay(t)
	ctx := budget(t) // ends a hung run only; each connection close is awaited on the relay's bell
	// gate raise posts the question on its own connection, then closes it
	q := func(id string) (gatestore.Record, gatestore.Question, string) {
		conn, sh, post, err := Connect(ctx, b, pin, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		f.core.Relay, f.core.Body, f.core.BodyGrant = conn, sh.Body, post
		rec, qq, a := raiseRel(t, f.core, id)
		conn.Close()
		f.core.Relay = nil
		return rec, qq, a
	}
	_, question, answer := q("rel-ws")
	await(ctx, t, &relay.bell, "the raise connection closes", func() bool { return relay.open() == 0 })
	serve := &fakeServe{}
	h := &Handler{Core: f.core, Dial: VerifyDialer(b, pin), Inbox: serve, Mail: &fakeMailer{}, Target: Target{Root: f.root, Handle: "lead-a"}}
	id := f.ownerDMOn(t, relay, answer, question.EventID)
	msg := serve.deliver(id, pin.Channel, answer)
	got, err := h.Step(ctx)
	if err != nil || len(got) != 1 || got[0].Gate == nil || got[0].Gate.Outcome != "approved" {
		t.Fatalf("verified on the relay: %+v, %v", got, err)
	}
	if ids := relay.idsREQs(); !slices.Equal(ids, []int{1}) {
		t.Fatalf("connections open at each REQ by id: %v, want [1]", ids)
	}
	await(ctx, t, &relay.bell, "the verifier's connection closes", func() bool { return relay.open() == 0 })
	if res, _ := serve.result(msg); res != "accepted rel-ws tag" {
		t.Fatalf("reply %q", res)
	}
	t.Logf("MEASURED: connections open during the verification 1, after 0; relay connections in all %d (one raise, one verification)", relay.totalConns())

	// a stalled fetch
	_, q2, a2 := q("rel-ws2")
	relay.mu.Lock()
	relay.stallIDs = true
	relay.mu.Unlock()
	h.VerifyTimeout = 300 * time.Millisecond
	id2 := f.ownerDMOn(t, relay, a2, q2.EventID)
	msg2 := serve.deliver(id2, pin.Channel, a2)
	start := time.Now()
	got, err = h.Step(ctx)
	if err != nil || len(got) != 1 || got[0].Kind != "attention" || time.Since(start) > 3*time.Second {
		t.Fatalf("stalled fetch: %+v, %v after %s", got, err, time.Since(start))
	}
	if res, n := serve.result(msg2); n != 0 {
		t.Fatalf("a stalled fetch sent a result %q", res)
	}
	if st := serve.statuses(msg2); len(st) != 2 || st[1] != "timeout rel-ws2 verify_timeout" {
		t.Fatalf("status lines %q", st)
	}
	await(ctx, t, &relay.bell, "the stalled connection closes", func() bool { return relay.open() == 0 })
}

// ownerDMOn publishes an owner DM on the websocket relay's store.
func (f coreFixture) ownerDMOn(t *testing.T, r *wsRelay, content, thread string) string {
	t.Helper()
	saved := f.relay
	f.relay = &r.mem
	id := f.ownerDM(t, content, thread)
	f.relay = saved
	return id
}

// Acceptance 1 (the binding): remote bind and attach accept only the share
// bound to the mailbox adapter with the handle, reply_timeout 60s, DM
// commands on and the handle's inbox; the retired presence-only fake target
// is refused, with no fallback.
func TestCheckMailboxShare(t *testing.T) {
	root := realTempDir(t)
	write := func(adapters, share string) {
		raw := `{"schema_version":2,"layer":"remote","adapters":[` + adapters + `],"relay":{"url":"wss://relay.example","shares":[` + share + `]}}`
		writeFile(t, filepath.Join(root, "extensions", "remote", "manifest.json"), raw)
	}
	good := `{"kind":"mailbox","target":"amq-squad","config":{"handle":"attach","reply_timeout":"60s"}}`
	share := `{"target":"amq-squad","session":"amq-squad","owner_pubkey":"` + strings.Repeat("a", 64) + `","dm_channel_id":"dm-1","native_session_id":"attach","commands":true,"min_evidence":"submitted","presence":true,"name":"amq-squad"}`
	write(good, share)
	if _, err := CheckMailboxShare(root, "amq-squad", "dm-1", "wss://relay.example"); err == nil || !strings.Contains(err.Error(), "no inbox") {
		t.Fatalf("no inbox: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, "agents", "attach", "inbox", "new"), 0o700); err != nil {
		t.Fatal(err)
	}
	mb, err := CheckMailboxShare(root, "amq-squad", "dm-1", "wss://relay.example")
	if err != nil || mb.Kind != "mailbox" || mb.Handle != "attach" || mb.ReplyTimeout != "60s" || !mb.Commands || mb.MinEvidence != "submitted" {
		t.Fatalf("good binding: %+v, %v", mb, err)
	}
	for name, c := range map[string][2]string{
		"fake target (4-5 deviation 1)": {`{"kind":"fake","target":"buzz-presence","epoch":"amq-squad-buzz-1"}`, strings.Replace(strings.Replace(share, `"amq-squad","session"`, `"buzz-presence","session"`, 1), `"commands":true,`, ``, 1)},
		"default reply timeout":         {`{"kind":"mailbox","target":"amq-squad","config":{"handle":"attach"}}`, share},
		"another handle":                {`{"kind":"mailbox","target":"amq-squad","config":{"handle":"user","reply_timeout":"60s"}}`, share},
		"commands off":                  {good, strings.Replace(share, `"commands":true,`, ``, 1)},
		"another channel":               {good, strings.Replace(share, `"dm-1"`, `"dm-2"`, 1)},
		"admitted evidence":             {good, strings.Replace(share, `"submitted"`, `"admitted"`, 1)},
		"native session not the handle": {good, strings.Replace(share, `"native_session_id":"attach"`, `"native_session_id":"x"`, 1)},
	} {
		write(c[0], c[1])
		if _, err := CheckMailboxShare(root, "amq-squad", "dm-1", "wss://relay.example"); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	write(good, share)
	if _, err := CheckMailboxShare(root, "amq-squad", "dm-1", "wss://other.example"); err == nil {
		t.Error("another relay: accepted")
	}
}

// The inbox through the real amq CLI (the installed one is enough: list,
// read and reply are unchanged across the floor): serve's delivery shape in,
// the status and the result replies out to the carrier, the take moves the
// message to cur.
func TestAMQInboxThroughTheCLI(t *testing.T) {
	if _, err := exec.LookPath("amq"); err != nil {
		t.Skip("amq not installed")
	}
	root := filepath.Join(realTempDir(t), "share")
	if out, err := exec.Command("amq", "init", "--root", root, "--agents", "remote,attach,buzz").CombinedOutput(); err != nil {
		t.Fatalf("amq init: %v\n%s", err, out)
	}
	ctx := context.Background()
	m := AMQMailer{}
	event := strings.Repeat("e1", 32)
	// the shape serve's mailbox adapter writes (v0.116.2 mailbox.go prompt)
	if _, err := m.Send(ctx, Mail{Root: root, From: CarrierHandle, To: MailboxHandle, Subject: "Buzz DM", Body: "hello" + ServePromptSuffix + " Answer with `amq reply --id X`.",
		Labels: []string{"acp", "buzz", "nostr:" + event}}); err != nil {
		t.Fatal(err)
	}
	in := AMQInbox{Root: root, Handle: MailboxHandle}
	list, err := in.New(ctx)
	if err != nil || len(list) != 1 || list[0].From != CarrierHandle || !slices.Contains(list[0].Labels, "nostr:"+event) {
		t.Fatalf("new = %+v, %v", list, err)
	}
	p, err := in.Take(ctx, list[0].ID)
	if err != nil || !strings.HasPrefix(p.Body, "hello") {
		t.Fatalf("take = %+v, %v", p, err)
	}
	if again, _ := in.New(ctx); len(again) != 0 {
		t.Fatalf("the taken message is still new: %+v", again)
	}
	if err := in.Reply(ctx, p.ID, "verifying -", true); err != nil {
		t.Fatal(err)
	}
	if err := in.Reply(ctx, p.ID, "routed user", false); err != nil {
		t.Fatal(err)
	}
	list2 := exec.Command("amq", "list", "--root", root, "--me", CarrierHandle, "--new", "--json")
	list2.Env = append(os.Environ(), "AMQ_NO_UPDATE_CHECK=1")
	out, err := list2.Output()
	if err != nil {
		t.Fatalf("amq list: %v\n%s", err, out)
	}
	var rows []struct {
		Kind string `json:"kind"`
	}
	_ = json.Unmarshal(out, &rows)
	if len(rows) != 2 {
		t.Fatalf("the carrier's inbox holds %d replies, want 2:\n%s", len(rows), out)
	}
	files, _ := filepath.Glob(filepath.Join(root, "agents", CarrierHandle, "inbox", "new", "*.md"))
	var kinds []string
	for _, f := range files {
		raw, _ := os.ReadFile(f)
		for _, k := range []string{`"kind": "status"`, `"kind": "answer"`} {
			if strings.Contains(string(raw), k) {
				kinds = append(kinds, k)
			}
		}
	}
	slices.Sort(kinds)
	if !slices.Equal(kinds, []string{`"kind": "answer"`, `"kind": "status"`}) {
		t.Fatalf("reply kinds %v", kinds)
	}
}
