package cli

import (
	"context"
	"crypto/ed25519"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/gatestore"
	"github.com/omriariav/amq-squad/v3/internal/gateverify"
	"github.com/omriariav/amq-squad/v3/internal/nostr"
	"github.com/omriariav/amq-squad/v3/internal/operatorauth"
	"github.com/omriariav/amq-squad/v3/internal/team"
)

// cliMemRelay is an in-memory relay for the CLI tests.
type cliMemRelay struct {
	mu     sync.Mutex
	stored [][]byte
}

func (r *cliMemRelay) Publish(_ context.Context, raw []byte) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.stored = append(r.stored, append([]byte(nil), raw...))
	return nil
}

func (r *cliMemRelay) FetchByID(ctx context.Context, id string) ([]byte, error) {
	got, _ := r.Query(ctx, gateverify.Filter{IDs: []string{id}})
	if len(got) == 0 {
		return nil, errors.New("relay has no event " + id)
	}
	return got[0], nil
}

func (r *cliMemRelay) Query(_ context.Context, f gateverify.Filter) ([][]byte, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out [][]byte
	for _, raw := range r.stored {
		e, err := nostr.ParseEvent(raw)
		if err != nil || (f.IDs != nil && !slices.Contains(f.IDs, e.ID)) || (f.Kinds != nil && !slices.Contains(f.Kinds, e.Kind)) ||
			(f.Authors != nil && !slices.Contains(f.Authors, e.PubKey)) || (f.Since > 0 && e.CreatedAt < f.Since) {
			continue
		}
		ok := true
		for name, want := range f.Tags {
			ok = ok && slices.ContainsFunc(nostr.TagValues(e, name), func(v string) bool { return slices.Contains(want, v) })
		}
		if ok {
			out = append(out, raw)
		}
	}
	return out, nil
}

type gateCLIFixture struct {
	project, base, root, home string
	owner, body               nostr.SecretKey
	relay                     *cliMemRelay
}

// newGateCLIFixture builds a v2 team with a cto launch in session s, a HOME
// with the owner's gate binding, issuer key and trust store, and an in-memory
// Buzz transport.
func newGateCLIFixture(t *testing.T) gateCLIFixture {
	t.Helper()
	project, base, _ := seedNotifyProject(t, team.DefaultOperator())
	seedNotifyLaunch(t, project, base, "s", "cto")
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	f := gateCLIFixture{project: project, base: base, root: filepath.Join(base, "s"), home: home, relay: &cliMemRelay{}}
	f.owner, _ = nostr.NewSecretKey()
	f.body, _ = nostr.NewSecretKey()
	dir := filepath.Join(home, ".amq-squad")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	pub, priv, _ := ed25519.GenerateKey(nil)
	der, _ := x509.MarshalPKCS8PrivateKey(priv)
	mustWrite(t, filepath.Join(dir, "issuer-key.pem"), string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})))
	trust, _ := json.Marshal(operatorauth.AuthorizationTrustStore{SchemaVersion: 1, Keys: []operatorauth.AuthorizationTrustKey{{
		KeyID: operatorauth.KeyIDForEd25519(pub), Algorithm: "ed25519", PublicKey: base64.StdEncoding.EncodeToString(pub), Status: "trusted"}}})
	mustWrite(t, filepath.Join(dir, "authorization-trust.json"), string(trust))
	binding, _ := json.Marshal(gateverify.Binding{Schema: 1, Relay: "wss://relay.test", Channel: "dm-chan", Owner: f.owner.PublicHex(), Body: f.body.PublicHex(), Carriers: []string{"buzz"}})
	mustWrite(t, filepath.Join(dir, "gate-binding.json"), string(binding))
	prev := gateTransport
	gateTransport = func(context.Context, gateverify.Binding, gateverify.Pin) (gateverify.Relay, nostr.SecretKey, []string, error) {
		return f.relay, f.body, nil, nil
	}
	t.Cleanup(func() { gateTransport = prev })
	return f
}

func (f gateCLIFixture) answer(t *testing.T, content, thread string) string {
	t.Helper()
	tags := [][]string{{"h", "dm-chan"}}
	if thread != "" {
		tags = append(tags, []string{"e", thread, "", "reply"})
	}
	e := nostr.Event{CreatedAt: time.Now().Unix(), Kind: 9, Tags: tags, Content: content}
	if err := f.owner.Sign(&e); err != nil {
		t.Fatal(err)
	}
	_ = f.relay.Publish(context.Background(), e.Marshal())
	return e.ID
}

// The typed path end to end through the CLI: gate raise writes the store via
// the core and posts the question; the owner's signed thread reply is
// verified by gate answer; verify action decides from the store; verify
// authorization --gate checks answers/<id>.json; guard opens; gate status
// reports it.
func TestTypedGateCLIRoundTripOnTheStore(t *testing.T) {
	f := newGateCLIFixture(t)
	stdout, _, err := captureOutput(t, func() error {
		return runGate([]string{"raise", "--project", f.project, "--session", "s", "--me", "cto", "--gate", "rel-v3", "--action", "tag", "--target", "git tag v3.0.0", "--json"})
	})
	if err != nil {
		t.Fatalf("gate raise: %v\n%s", err, stdout)
	}
	store, _ := gatestore.Open(f.root)
	rec, err := store.Load("rel-v3")
	if err != nil || rec.RaisedBy != "cto" || rec.Class != "tag" || rec.Target != "git tag v3.0.0" {
		t.Fatalf("record = %+v, %v", rec, err)
	}
	q, _ := store.Question("rel-v3")
	if q == nil || !strings.Contains(stdout, `"question_sent": true`) {
		t.Fatalf("question = %+v\n%s", q, stdout)
	}
	verify := func() error {
		_, _, err := captureOutput(t, func() error {
			return runVerifyAction([]string{"--project", f.project, "--session", "s", "--gate", "rel-v3", "--action", "tag", "--target", "git tag v3.0.0", "--json"})
		})
		return err
	}
	if err := verify(); ExitCode(err) != ExitActionPending {
		t.Fatalf("verify action before the answer: %v (%d)", err, ExitCode(err))
	}
	tuple := "approve rel-v3 release " + strings.TrimPrefix(rec.Digest, "sha256:")[:16]
	// a plain DM answer is re-asked, not accepted
	plain := f.answer(t, tuple, "")
	if _, _, err := captureOutput(t, func() error {
		return runGate([]string{"answer", "--project", f.project, "--session", "s", "--event", plain})
	}); ExitCode(err) != ExitActionPending {
		t.Fatalf("plain answer: %v", err)
	}
	// the thread reply arrives as a labelled mailbox message from the carrier
	threaded := f.answer(t, tuple, q.EventID)
	if _, _, err := captureOutput(t, func() error {
		return runGate([]string{"answer", "--project", f.project, "--session", "s", "--from", "cto", "--label", "nostr:" + threaded})
	}); ExitCode(err) != ExitActionUnbound {
		t.Fatalf("label from a seat: %v", err)
	}
	out, _, err := captureOutput(t, func() error {
		return runGate([]string{"answer", "--project", f.project, "--session", "s", "--from", "buzz", "--label", "acp", "--label", "nostr:" + threaded, "--json"})
	})
	if err != nil || !strings.Contains(out, `"outcome": "approved"`) {
		t.Fatalf("gate answer: %v\n%s", err, out)
	}
	if err := verify(); err != nil {
		t.Fatalf("verify action after the answer: %v", err)
	}
	if _, _, err := captureOutput(t, func() error {
		return runVerifyAction([]string{"--project", f.project, "--session", "s", "--gate", "rel-v3", "--action", "tag", "--target", "git tag v3.0.1"})
	}); ExitCode(err) != ExitActionUnbound {
		t.Fatalf("verify action for another target: %v", err)
	}
	if _, _, err := captureOutput(t, func() error {
		return runVerifyAuthorization([]string{"--project", f.project, "--session", "s", "--gate", "rel-v3", "--action", "tag", "--target", "git tag v3.0.0", "--json"})
	}); err != nil {
		t.Fatalf("verify authorization --gate: %v", err)
	}
	if _, _, err := captureOutput(t, func() error {
		return runVerifyAuthorization([]string{"--project", f.project, "--session", "s", "--gate", "rel-v3", "--action", "tag", "--target", "git tag v9"})
	}); err == nil {
		t.Fatal("verify authorization --gate accepted another target")
	}
	id := hookIdentity{Handle: "cto", Root: f.root, Session: "s", AgentDir: filepath.Join(f.root, "agents", "cto")}
	if v := evaluateGuard(id, guardCall{Tool: "Bash", Args: map[string]string{"command": "git tag v3.0.0"}, CWD: f.project}, time.Now()); v.Verdict != "allow" {
		t.Fatalf("guard after the verified answer: %+v", v)
	}
	st, _, err := captureOutput(t, func() error {
		return runGate([]string{"status", "--project", f.project, "--session", "s", "--json"})
	})
	if err != nil || !strings.Contains(st, `"state": "accepted"`) || !strings.Contains(st, `"reasked": true`) {
		t.Fatalf("gate status: %v\n%s", err, st)
	}
	// a second raise of the same id fails O_EXCL
	if _, _, err := captureOutput(t, func() error {
		return runGate([]string{"raise", "--project", f.project, "--session", "s", "--me", "cto", "--gate", "rel-v3", "--action", "tag", "--target", "git tag v3.0.0"})
	}); err == nil {
		t.Fatal("second raise accepted")
	}
}

// Without a transport the raise still writes the record first and reports
// the question as not sent; gate post sends it once a transport exists.
func TestTypedGateRaiseWithoutTransport(t *testing.T) {
	f := newGateCLIFixture(t)
	gateTransport = func(context.Context, gateverify.Binding, gateverify.Pin) (gateverify.Relay, nostr.SecretKey, []string, error) {
		return nil, nostr.SecretKey{}, nil, errors.New("no Buzz transport")
	}
	out, _, err := captureOutput(t, func() error {
		return runGate([]string{"raise", "--project", f.project, "--session", "s", "--me", "cto", "--gate", "send-1", "--action", "external_send", "--target", "mcp__slack__send_message C1"})
	})
	if err != nil || !strings.Contains(out, "question not sent yet") {
		t.Fatalf("raise without transport: %v\n%s", err, out)
	}
	store, _ := gatestore.Open(f.root)
	if _, err := store.Load("send-1"); err != nil {
		t.Fatal(err)
	}
	gateTransport = func(context.Context, gateverify.Binding, gateverify.Pin) (gateverify.Relay, nostr.SecretKey, []string, error) {
		return f.relay, f.body, nil, nil
	}
	if _, _, err := captureOutput(t, func() error {
		return runGate([]string{"post", "--project", f.project, "--session", "s", "--gate", "send-1"})
	}); err != nil {
		t.Fatal(err)
	}
	if q, _ := store.Question("send-1"); q == nil {
		t.Fatal("gate post recorded no question")
	}
	if _, _, err := captureOutput(t, func() error {
		return runGate([]string{"post", "--project", f.project, "--session", "s", "--gate", "send-1"})
	}); err == nil {
		t.Fatal("second post accepted")
	}
}

// Behavior change (3-1 ruling P): operator answer refuses the typed gates.
func TestOperatorAnswerRefusesTypedGates(t *testing.T) {
	project, base, _ := seedNotifyProject(t, team.DefaultOperator())
	seedNotifyLaunch(t, project, base, "s", "cto")
	calls := withAMQCommandSeams(t, amqEnv{Root: ".agent-mail/{session}", BaseRoot: ".agent-mail"}, "Sent forbidden to cto\n")
	for _, action := range []string{"tag", "github_release", "package_publish", "external_send", "credential_change", "spawn"} {
		err := runOperator([]string{"answer", "--project", project, "--session", "s", "--gate", "g", "--approved", "--action", action})
		if err == nil || !strings.Contains(err.Error(), "amq-squad gate answer") {
			t.Errorf("%s: %v", action, err)
		}
	}
	// a v2 typed question in the mailbox for a typed action is refused too
	question := typedQuestion(project, "s", "q-tag", "tag", "v1", "", time.Now().UTC())
	writeAuthorizationMessage(t, base, "s", "user", "new", question)
	err := runOperator([]string{"answer", "--project", project, "--session", "s", "--gate", question.Thread, "--approved"})
	if err == nil || !strings.Contains(err.Error(), "amq-squad gate answer") {
		t.Fatalf("typed AMQ question: %v", err)
	}
	if len(*calls) != 0 {
		t.Fatalf("refusals sent AMQ messages: %+v", *calls)
	}
}

// flakyRelay rejects the next n publishes.
type flakyRelay struct {
	*cliMemRelay
	fail int
}

func (r *flakyRelay) Publish(ctx context.Context, raw []byte) error {
	if r.fail > 0 {
		r.fail--
		return errors.New("relay rejected the event")
	}
	return r.cliMemRelay.Publish(ctx, raw)
}

// Second review, new defect: a relay rejection of the question is reported as
// unsent with the retry instruction; question_sent follows the published
// marker, and gate post sends the recorded question once.
func TestReview9PublishFailureReportsUnsent(t *testing.T) {
	f := newGateCLIFixture(t)
	flaky := &flakyRelay{cliMemRelay: f.relay, fail: 1}
	gateTransport = func(context.Context, gateverify.Binding, gateverify.Pin) (gateverify.Relay, nostr.SecretKey, []string, error) {
		return flaky, f.body, nil, nil
	}
	out, _, err := captureOutput(t, func() error {
		return runGate([]string{"raise", "--project", f.project, "--session", "s", "--me", "cto", "--gate", "rel-9", "--action", "tag", "--target", "git tag v9", "--json"})
	})
	if err != nil || !strings.Contains(out, `"question_sent": false`) || !strings.Contains(out, "relay rejected") {
		t.Fatalf("json raise with a rejecting relay: %v\n%s", err, out)
	}
	text, _, _ := captureOutput(t, func() error {
		return runGate([]string{"raise", "--project", f.project, "--session", "s", "--me", "cto", "--gate", "rel-9b", "--action", "tag", "--target", "git tag v9b"})
	})
	if !strings.Contains(text, "question posted") {
		t.Fatalf("second raise (relay healthy) = %s", text)
	}
	flaky.fail = 1
	text, _, _ = captureOutput(t, func() error {
		return runGate([]string{"raise", "--project", f.project, "--session", "s", "--me", "cto", "--gate", "rel-9c", "--action", "tag", "--target", "git tag v9c"})
	})
	if strings.Contains(text, "question posted") || !strings.Contains(text, "amq-squad gate post --gate rel-9c") {
		t.Fatalf("text raise with a rejecting relay = %s", text)
	}
	st, _, _ := captureOutput(t, func() error {
		return runGate([]string{"status", "--project", f.project, "--session", "s", "--gate", "rel-9", "--json"})
	})
	if !strings.Contains(st, `"question_sent": false`) {
		t.Fatalf("status before the retry = %s", st)
	}
	store, _ := gatestore.Open(f.root)
	recorded, _ := store.Question("rel-9")
	if _, _, err := captureOutput(t, func() error {
		return runGate([]string{"post", "--project", f.project, "--session", "s", "--gate", "rel-9"})
	}); err != nil {
		t.Fatalf("gate post retry: %v", err)
	}
	if got, _ := f.relay.FetchByID(context.Background(), recorded.EventID); got == nil {
		t.Fatal("the retry did not publish the recorded question")
	}
	st, _, _ = captureOutput(t, func() error {
		return runGate([]string{"status", "--project", f.project, "--session", "s", "--gate", "rel-9", "--json"})
	})
	if !strings.Contains(st, `"question_sent": true`) {
		t.Fatalf("status after the retry = %s", st)
	}
}
