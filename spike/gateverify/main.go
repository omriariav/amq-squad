// Command gateverify is the S1 spike prototype: render a typed gate, carry
// an owner-signed BIP-340 answer over amq-remote / Buzz (nostr), fetch it
// from the relay by id, verify it in Go, and wrap it in an operatorauth
// envelope.
//
// Exit codes: 0 verified approve (or command success), 3 verified deny,
// 1 rejected or failed, 2 usage.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/operatorauth"
)

const usage = `usage: gateverify <command> [flags]

  render          render a gate record and its text from a gate spec
  verify          verify one owner answer event against a gate record
  fetch           fetch one event from a relay by id (REQ ids filter)
  watch           wait for the owner's answer in the DM channel (fallback id resolution)
  resolve         map a delivered AMQ message / serve ingress to a nostr event id
  post-gate       post the gate text into the DM as the share body (interim)
  publish         publish one signed event
  envelope-verify verify an answer file (operatorauth + bip340 re-verification)
  keygen          mint a THROWAWAY nostr key (synthetic tests only)
  issuer-keygen   mint the core's Ed25519 issuer key and trust store (scratch)
  sign            sign a synthetic event with a throwaway key
  check-event     check NIP-01 id and BIP-340 signature of events, one per line (no pin)
  npub            print "<hex> <npub>" for an npub or 64-hex pubkey
  e2e-synthetic   synthetic end to end: sign, publish, fetch, verify, envelope
`

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	cmds := map[string]func([]string, io.Writer, io.Writer) int{
		"render": cmdRender, "verify": cmdVerify, "fetch": cmdFetch, "watch": cmdWatch,
		"resolve": cmdResolve, "post-gate": cmdPostGate, "publish": cmdPublish,
		"envelope-verify": cmdEnvelopeVerify, "keygen": cmdKeygen, "issuer-keygen": cmdIssuerKeygen,
		"sign": cmdSign, "e2e-synthetic": cmdE2E, "check-event": cmdCheckEvent, "npub": cmdNpub,
	}
	f, ok := cmds[args[0]]
	if !ok {
		fmt.Fprint(stderr, usage)
		return 2
	}
	return f(args[1:], stdout, stderr)
}

func fail(stderr io.Writer, format string, a ...any) int {
	fmt.Fprintf(stderr, "gateverify: "+format+"\n", a...)
	return 1
}

func newFlags(name string, stderr io.Writer) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	return fs
}

func readJSON(path string, v any) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return strictDecode(raw, v)
}

func writeJSON(path string, v any) error {
	raw, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(raw, '\n'), 0o600)
}

func cmdRender(args []string, stdout, stderr io.Writer) int {
	fs := newFlags("render", stderr)
	root := fs.String("root", "", "AMQ root: the record is written once to <root>/extensions/amq-squad/gates/ (required)")
	spec := fs.String("spec", "", "gate spec JSON (required)")
	at := fs.String("now", "", "issue time RFC3339 (default now)")
	owner := fs.String("owner", "", "the gate's owner, npub or 64 hex (required): recorded, only this key's answer counts")
	issuer := fs.String("issuer-key", "", "the core's Ed25519 issuer key PEM (required): its key id is recorded, only it may sign the envelope")
	if fs.Parse(args) != nil || *root == "" || *spec == "" || *owner == "" || *issuer == "" {
		return 2
	}
	ownerHex, err := ParseOwner(*owner)
	if err != nil {
		return fail(stderr, "--owner: %v", err)
	}
	signer, err := operatorauth.LoadEd25519Signer(*issuer)
	if err != nil {
		return fail(stderr, "--issuer-key: %v", err)
	}
	keyID := signer.KeyID()
	signer.Destroy()
	var s GateSpec
	if err := readJSON(*spec, &s); err != nil {
		return fail(stderr, "spec: %v", err)
	}
	now := time.Now()
	if *at != "" {
		t, err := time.Parse(time.RFC3339, *at)
		if err != nil {
			return 2
		}
		now = t
	}
	rec, err := Render(s, now, GateIdentity{OwnerPubKey: ownerHex, IssuerKeyID: keyID})
	if err != nil {
		return fail(stderr, "render: %v", err)
	}
	if err := GateStoreFor(*root).Create(rec); err != nil {
		return fail(stderr, "store gate %s: %v", rec.GateID, err)
	}
	fmt.Fprintln(stdout, rec.Text)
	return 0
}

// gateFlags select a stored gate: the verifier and the poster read the gate
// record from the core's store under the AMQ root, never a digest or record
// a caller passes.
type gateFlags struct{ root, id *string }

func addGate(fs *flag.FlagSet) gateFlags {
	return gateFlags{
		root: fs.String("root", "", "AMQ root holding the gate store and ledger (required)"),
		id:   fs.String("gate-id", "", "gate id (required)"),
	}
}

func (g gateFlags) ok() bool { return *g.root != "" && *g.id != "" }

func (g gateFlags) load() (GateRecord, *GateQuestion, error) {
	store := GateStoreFor(*g.root)
	rec, err := store.Load(*g.id)
	if err != nil {
		return GateRecord{}, nil, err
	}
	q, err := store.Question(*g.id)
	return rec, q, err
}

type pinFlags struct{ owner, channel, body *string }

func addPin(fs *flag.FlagSet) pinFlags {
	return pinFlags{
		owner:   fs.String("owner", "", "pinned owner npub1... or 64 hex (required)"),
		channel: fs.String("channel", "", "pinned DM channel id, the NIP-29 h tag (required)"),
		body:    fs.String("body", "", "pinned share body pubkey (hex or npub); required for thread binding"),
	}
}

func (p pinFlags) pin() (Pin, error) {
	owner, err := ParseOwner(*p.owner)
	if err != nil {
		return Pin{}, err
	}
	if strings.TrimSpace(*p.channel) == "" {
		return Pin{}, errors.New("--channel is required")
	}
	pin := Pin{Owner: owner, Channel: *p.channel}
	if *p.body != "" {
		if pin.Body, err = ParseOwner(*p.body); err != nil {
			return Pin{}, fmt.Errorf("--body: %w", err)
		}
	}
	return pin, nil
}

type authFlags struct{ root, session *string }

func addAuth(fs *flag.FlagSet) authFlags {
	return authFlags{
		root:    fs.String("share-root", "", "AMQ root of the enrolled share; sign in with its body key (interim)"),
		session: fs.String("share-session", "", "share session name"),
	}
}

// dial connects and, when a share is named, signs in with its body key.
func (a authFlags) dial(ctx context.Context, relay, owner string) (*RelayClient, error) {
	c, err := Dial(ctx, relay)
	if err != nil {
		return nil, err
	}
	if *a.root == "" {
		return c, nil
	}
	ba, _, err := LoadBodyAuth(*a.root, *a.session, owner, time.Now())
	if err != nil {
		c.Close()
		return nil, err
	}
	if err := c.Auth(ctx, ba); err != nil {
		c.Close()
		return nil, fmt.Errorf("NIP-42 sign-in as the share body: %w", err)
	}
	return c, nil
}

// rejectedPlain reports a refusal that may carry no rejection code.
func rejectedPlain(stdout io.Writer, err error) int {
	code := RejectionCode(err)
	if code == "" {
		code = "not_consumed"
	}
	fmt.Fprintf(stdout, `{"result":"rejected","code":%q,"detail":%q}`+"\n", code, err.Error())
	return 1
}

func rejected(stdout io.Writer, err error) int {
	fmt.Fprintf(stdout, `{"result":"rejected","code":%q,"detail":%q}`+"\n", RejectionCode(err), err.Error())
	return 1
}

// cmdVerify: verify; refuse a replay; write the envelope (fsynced); then
// mark consumed (fsynced). See Ledger for why this order.
func cmdVerify(args []string, stdout, stderr io.Writer) int {
	fs := newFlags("verify", stderr)
	eventPath := fs.String("event", "", "raw event JSON as fetched (required)")
	g := addGate(fs)
	p := addPin(fs)
	thread := fs.Bool("require-thread", true, "require the answer to reply to the stored, body-signed gate question")
	noLedger := fs.Bool("no-ledger", false, "classify only: do not consume (candidate checks)")
	issuer := fs.String("issuer-key", "", "core Ed25519 issuer key (PKCS#8 PEM); writes the envelope on approve")
	out := fs.String("out", "", "answer file (default <root>/extensions/amq-squad/answers/<gate-id>.json)")
	relay := fs.String("relay", "", "relay URL recorded in the evidence")
	project := fs.String("project-dir", "", "namespace project dir (absolute)")
	session := fs.String("session", "s1-spike", "namespace session")
	generation := fs.String("generation", "gen-spike", "namespace generation")
	at := fs.String("now", "", "verification time RFC3339 (default now; tests only)")
	if fs.Parse(args) != nil || *eventPath == "" || !g.ok() {
		return 2
	}
	pin, err := p.pin()
	if err != nil {
		return fail(stderr, "%v", err)
	}
	pin.RequireThread = *thread
	raw, err := os.ReadFile(*eventPath)
	if err != nil {
		return fail(stderr, "%v", err)
	}
	gate, question, err := g.load()
	if err != nil {
		return rejected(stdout, reject(RejectGateRecord, "%v", err))
	}
	now := time.Now()
	if *at != "" {
		if now, err = time.Parse(time.RFC3339, *at); err != nil {
			return 2
		}
	}
	o, err := Verify(raw, gate, question, pin, now)
	if err != nil {
		return rejected(stdout, err)
	}
	if !*noLedger {
		// A consuming verify of an approve must write its envelope; with no
		// issuer key there is no envelope, so nothing is consumed (Commit
		// refuses it too: consumption is a side effect of the write).
		ledger := LedgerFor(*g.root)
		if err := ledger.Consumed(o); err != nil { // read-only; Commit repeats it under the lock
			return rejected(stdout, err)
		}
		var env *Envelope
		if o.Decision == "approve" {
			if *issuer == "" {
				return rejectedPlain(stdout, ErrNoEnvelope)
			}
			signer, err := operatorauth.LoadEd25519Signer(*issuer)
			if err != nil {
				return fail(stderr, "%v", err)
			}
			defer signer.Destroy()
			pd := *project
			if pd == "" {
				pd, _ = os.Getwd()
			}
			f, err := BuildAnswerFile(o, raw, gate, question, pin, *relay, Namespace{ProjectDir: filepath.Clean(pd), Profile: defaultProfileName, Session: *session, Generation: *generation}, signer, now)
			if err != nil {
				return fail(stderr, "%v", err)
			}
			dest := *out
			if dest == "" {
				dest = filepath.Join(*g.root, "extensions", "amq-squad", "answers", gate.GateID+".json")
			}
			blob, _ := json.MarshalIndent(f, "", "  ")
			env = &Envelope{Path: dest, Data: append(blob, '\n')}
		}
		if err := ledger.Commit(o, env); err != nil {
			return rejectedPlain(stdout, err)
		}
	}
	if o.Decision == "deny" {
		fmt.Fprintf(stdout, `{"result":"deny","gate":%q,"event_id":%q}`+"\n", o.Answer.GateID, o.Event.ID)
		return 3
	}
	fmt.Fprintf(stdout, `{"result":"approve","gate":%q,"event_id":%q}`+"\n", o.Answer.GateID, o.Event.ID)
	return 0
}

func cmdFetch(args []string, stdout, stderr io.Writer) int {
	fs := newFlags("fetch", stderr)
	relay := fs.String("relay", "", "relay URL (required)")
	id := fs.String("id", "", "event id (required)")
	owner := fs.String("owner", "", "owner npub/hex (required with --share-root)")
	out := fs.String("out", "", "write the raw event here (default stdout)")
	a := addAuth(fs)
	if fs.Parse(args) != nil || *relay == "" || *id == "" {
		return 2
	}
	ownerHex := ""
	if *a.root != "" {
		var err error
		if ownerHex, err = ParseOwner(*owner); err != nil {
			return fail(stderr, "%v", err)
		}
	}
	ctx := context.Background()
	start := time.Now()
	c, err := a.dial(ctx, *relay, ownerHex)
	if err != nil {
		return fail(stderr, "%v", err)
	}
	defer c.Close()
	raw, err := c.FetchByID(ctx, *id)
	if err != nil {
		return fail(stderr, "%v", err)
	}
	fmt.Fprintf(stderr, "fetched %s in %s\n", *id, time.Since(start).Round(time.Millisecond))
	if *out == "" {
		fmt.Fprintln(stdout, string(raw))
		return 0
	}
	if err := os.WriteFile(*out, raw, 0o600); err != nil {
		return fail(stderr, "%v", err)
	}
	return 0
}

func cmdWatch(args []string, stdout, stderr io.Writer) int {
	fs := newFlags("watch", stderr)
	relay := fs.String("relay", "", "relay URL (required)")
	g := addGate(fs)
	timeout := fs.Duration("timeout", 30*time.Minute, "give up after")
	p := addPin(fs)
	a := addAuth(fs)
	if fs.Parse(args) != nil || *relay == "" || !g.ok() {
		return 2
	}
	pin, err := p.pin()
	if err != nil {
		return fail(stderr, "%v", err)
	}
	gate, _, err := g.load()
	if err != nil {
		return fail(stderr, "gate: %v", err)
	}
	_, nb, _, _ := gate.window()
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	filter := map[string]any{"kinds": []int{9}, "authors": []string{pin.Owner}, "#h": []string{pin.Channel}, "since": nb.Unix()}
	raw, err := WatchWithRetry(ctx, func(ctx context.Context) (*RelayClient, error) { return a.dial(ctx, *relay, pin.Owner) },
		filter, answerFilter(gate, pin), func(f string, v ...any) { fmt.Fprintf(stderr, "watch: "+f+"\n", v...) })
	if err != nil {
		return fail(stderr, "watch: %v", err)
	}
	e, _ := ParseEvent(raw)
	fmt.Fprintln(stdout, e.ID)
	return 0
}

// answerFilter admits only events that are worth resolving: signed by the
// pinned owner (checked, not trusted from the relay filter), naming this
// gate. Verify still decides.
func answerFilter(gate GateRecord, pin Pin) func(Event) bool {
	return func(e Event) bool {
		if e.PubKey != pin.Owner || e.VerifySignature() != nil {
			return false
		}
		a, err := ParseAnswer(e.Content)
		return err == nil && a.GateID == gate.GateID
	}
}

func cmdResolve(args []string, stdout, stderr io.Writer) int {
	fs := newFlags("resolve", stderr)
	mode := fs.String("mode", "", "cockpit | mailbox | ingress (required)")
	root := fs.String("root", "", "AMQ root")
	sender := fs.String("sender", "", "cockpit: amq-acp AM_ME handle")
	operator := fs.String("operator", "", "operator handle the answer is delivered to")
	message := fs.String("message", "", "delivered AMQ message file")
	stateDir := fs.String("acp-state", "", "mailbox: amq-acp state dir (default ~/.amq/remote/acp)")
	owner := fs.String("owner", "", "ingress: pinned owner")
	channel := fs.String("channel", "", "ingress: pinned DM channel")
	since := fs.Int64("since", 0, "ingress: unix seconds lower bound")
	session := fs.String("session", "", "ingress: share session")
	if fs.Parse(args) != nil {
		return 2
	}
	switch *mode {
	case "cockpit":
		id, err := ResolveCockpit(*root, *sender, *operator, *message)
		if err != nil {
			return fail(stderr, "%v", err)
		}
		fmt.Fprintln(stdout, id)
	case "mailbox":
		dir := *stateDir
		if dir == "" {
			home, _ := os.UserHomeDir()
			dir = filepath.Join(home, ".amq", "remote", "acp")
		}
		id, err := ResolveMailbox(dir, *operator, *message)
		if err != nil {
			return fail(stderr, "%v", err)
		}
		fmt.Fprintln(stdout, id)
	case "ingress":
		o, err := ParseOwner(*owner)
		if err != nil {
			return fail(stderr, "%v", err)
		}
		claims, skipped, err := ResolveIngress(*root, *session, o, *channel, *since)
		if err != nil {
			return fail(stderr, "%v", err)
		}
		for _, s := range skipped {
			fmt.Fprintf(stderr, "resolve: skipped %s\n", s)
		}
		for _, c := range claims {
			fmt.Fprintln(stdout, c.EventID)
		}
	default:
		return 2
	}
	return 0
}

func cmdPostGate(args []string, stdout, stderr io.Writer) int {
	fs := newFlags("post-gate", stderr)
	relay := fs.String("relay", "", "relay URL (required)")
	g := addGate(fs)
	p := addPin(fs)
	a := addAuth(fs)
	if fs.Parse(args) != nil || *relay == "" || !g.ok() || *a.root == "" {
		return 2
	}
	pin, err := p.pin()
	if err != nil {
		return fail(stderr, "%v", err)
	}
	store := GateStoreFor(*g.root)
	gate, err := store.Load(*g.id)
	if err != nil {
		return fail(stderr, "gate: %v", err)
	}
	if q, err := store.Question(gate.GateID); err != nil || q != nil {
		return fail(stderr, "gate %s already has a posted question (or an unreadable one): %v", gate.GateID, err)
	}
	now := time.Now()
	ba, _, err := LoadBodyAuth(*a.root, *a.session, pin.Owner, now)
	if err != nil {
		return fail(stderr, "%v", err)
	}
	grant, err := GrantTag(*a.root, *a.session, 9, now)
	if err != nil {
		return fail(stderr, "%v", err)
	}
	// The same tags amq-remote serve puts on a row (buzzio/carrier.go:1432):
	// the DM channel and the owner, plus the kind 9 NIP-OA grant.
	evt := Event{CreatedAt: now.Unix(), Kind: 9, Tags: [][]string{{"h", pin.Channel}, {"p", pin.Owner}, grant}, Content: gate.Text}
	if err := ba.Secret.Sign(&evt); err != nil {
		return fail(stderr, "%v", err)
	}
	ctx := context.Background()
	c, err := a.dial(ctx, *relay, pin.Owner)
	if err != nil {
		return fail(stderr, "%v", err)
	}
	defer c.Close()
	if err := c.Publish(ctx, evt.Marshal()); err != nil {
		return fail(stderr, "publish gate: %v", err)
	}
	if err := store.PutQuestion(GateQuestion{GateID: gate.GateID, EventID: evt.ID, RawEvent: string(evt.Marshal())}); err != nil {
		return fail(stderr, "store question: %v", err)
	}
	fmt.Fprintln(stdout, evt.ID)
	return 0
}

func cmdPublish(args []string, stdout, stderr io.Writer) int {
	fs := newFlags("publish", stderr)
	relay := fs.String("relay", "", "relay URL (required)")
	eventPath := fs.String("event", "", "signed event JSON (required)")
	owner := fs.String("owner", "", "owner (with --share-root)")
	a := addAuth(fs)
	if fs.Parse(args) != nil || *relay == "" || *eventPath == "" {
		return 2
	}
	raw, err := os.ReadFile(*eventPath)
	if err != nil {
		return fail(stderr, "%v", err)
	}
	ownerHex := ""
	if *a.root != "" {
		if ownerHex, err = ParseOwner(*owner); err != nil {
			return fail(stderr, "%v", err)
		}
	}
	ctx := context.Background()
	c, err := a.dial(ctx, *relay, ownerHex)
	if err != nil {
		return fail(stderr, "%v", err)
	}
	defer c.Close()
	if err := c.Publish(ctx, []byte(strings.TrimSpace(string(raw)))); err != nil {
		return fail(stderr, "%v", err)
	}
	fmt.Fprintln(stdout, "published")
	return 0
}

func cmdEnvelopeVerify(args []string, stdout, stderr io.Writer) int {
	fs := newFlags("envelope-verify", stderr)
	answer := fs.String("answer", "", "answer file (required)")
	store := fs.String("store", "", "operatorauth trust store with the issuer key (required)")
	allowUnbound := fs.Bool("allow-unbound", false, "accept an answer file with no gate question (no thread binding); refused by default")
	p := addPin(fs)
	if fs.Parse(args) != nil || *answer == "" || *store == "" {
		return 2
	}
	pin, err := p.pin()
	if err != nil {
		return fail(stderr, "%v", err)
	}
	var f AnswerFile
	raw, err := os.ReadFile(*answer)
	if err != nil {
		return fail(stderr, "%v", err)
	}
	if err := json.Unmarshal(raw, &f); err != nil {
		return fail(stderr, "%v", err)
	}
	ts, err := operatorauth.LoadAuthorizationTrustStore(*store)
	if err != nil {
		return fail(stderr, "%v", err)
	}
	if err := VerifyAnswerFile(f, ts, pin, *allowUnbound); err != nil {
		return fail(stderr, "%v", err)
	}
	fmt.Fprintf(stdout, "envelope ok: %s gate %s target %s answer %s\n", f.Envelope.Payload.AuthorizationID, f.Envelope.Payload.Gate, f.Envelope.Payload.Target, f.Envelope.Payload.AnswerMessageID)
	return 0
}

func cmdKeygen(args []string, stdout, stderr io.Writer) int {
	fs := newFlags("keygen", stderr)
	out := fs.String("out", "", "file for the THROWAWAY secret (0600, outside the repo) (required)")
	if fs.Parse(args) != nil || *out == "" {
		return 2
	}
	sk, err := NewSecretKey()
	if err != nil {
		return fail(stderr, "%v", err)
	}
	if err := os.WriteFile(*out, []byte(sk.Hex()+"\n"), 0o600); err != nil {
		return fail(stderr, "%v", err)
	}
	npub, _ := EncodeNpub(sk.PublicHex())
	fmt.Fprintf(stdout, "%s %s\n", sk.PublicHex(), npub)
	return 0
}

func loadSecret(path string) (SecretKey, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return SecretKey{}, err
	}
	defer clear(raw)
	return SecretKeyFromHex(strings.TrimSpace(string(raw)))
}

func cmdIssuerKeygen(args []string, stdout, stderr io.Writer) int {
	fs := newFlags("issuer-keygen", stderr)
	key := fs.String("key", "", "issuer PEM to write (required)")
	store := fs.String("store", "", "trust store JSON to write (required)")
	if fs.Parse(args) != nil || *key == "" || *store == "" {
		return 2
	}
	id, err := WriteIssuerKey(*key, *store)
	if err != nil {
		return fail(stderr, "%v", err)
	}
	fmt.Fprintln(stdout, id)
	return 0
}

func cmdSign(args []string, stdout, stderr io.Writer) int {
	fs := newFlags("sign", stderr)
	key := fs.String("key", "", "throwaway secret file (required)")
	kind := fs.Int("kind", 9, "event kind")
	channel := fs.String("channel", "", "h tag")
	content := fs.String("content", "", "content")
	reply := fs.String("reply", "", "event id this replies to")
	created := fs.Int64("created-at", 0, "created_at (default now)")
	if fs.Parse(args) != nil || *key == "" {
		return 2
	}
	sk, err := loadSecret(*key)
	if err != nil {
		return fail(stderr, "%v", err)
	}
	e := Event{Kind: *kind, Content: *content, CreatedAt: *created, Tags: [][]string{}}
	if e.CreatedAt == 0 {
		e.CreatedAt = time.Now().Unix()
	}
	if *channel != "" {
		e.Tags = append(e.Tags, []string{"h", *channel})
	}
	if *reply != "" {
		e.Tags = append(e.Tags, []string{"e", *reply, "", "reply"})
	}
	if err := sk.Sign(&e); err != nil {
		return fail(stderr, "%v", err)
	}
	fmt.Fprintln(stdout, string(e.Marshal()))
	return 0
}

func cmdNpub(args []string, stdout, stderr io.Writer) int {
	if len(args) != 1 {
		return 2
	}
	h, err := ParseOwner(args[0])
	if err != nil {
		return fail(stderr, "%v", err)
	}
	n, _ := EncodeNpub(h)
	fmt.Fprintf(stdout, "%s %s\n", h, n)
	return 0
}

func cmdCheckEvent(args []string, stdout, stderr io.Writer) int {
	if len(args) != 1 {
		return 2
	}
	raw, err := os.ReadFile(args[0])
	if err != nil {
		return fail(stderr, "%v", err)
	}
	bad := 0
	for i, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		e, err := ParseEvent([]byte(line))
		switch {
		case err != nil:
			fmt.Fprintf(stdout, "%d parse: %v\n", i, err)
			bad++
		case !e.CheckID():
			fmt.Fprintf(stdout, "%d id mismatch\n", i)
			bad++
		case e.VerifySignature() != nil:
			fmt.Fprintf(stdout, "%d sig: %v\n", i, e.VerifySignature())
			bad++
		default:
			fmt.Fprintf(stdout, "%d ok %s\n", i, e.ID)
		}
	}
	if bad > 0 {
		return 1
	}
	return 0
}
