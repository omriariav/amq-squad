package cli

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/buzz"
	"github.com/omriariav/amq-squad/v3/internal/gateverify"
	"github.com/omriariav/amq-squad/v3/internal/nostr"
)

// writeTestShare writes an enrolled share in amq-remote's layout with the
// default 8 kinds. The grant signatures are placeholders: bind reads the
// share's public values, amq-remote and the relay check the signatures.
func writeTestShare(t *testing.T, root, session, owner string, notAfter time.Time) nostr.SecretKey {
	t.Helper()
	raw := make([]byte, 32)
	_, _ = rand.Read(raw)
	secret := hex.EncodeToString(raw)
	body, err := nostr.SecretKeyFromHex(secret)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "extensions", "remote", "keys", session)
	mustWrite(t, filepath.Join(dir, "body.key"), "secret "+secret+"\npublic "+body.PublicHex()+"\n")
	var tags []map[string]any
	for _, k := range buzz.DefaultKinds {
		tags = append(tags, map[string]any{"kind": k, "owner_pubkey": owner, "conditions": fmt.Sprintf("kind=%d&created_at<%d", k, notAfter.Unix()), "sig": strings.Repeat("0", 128)})
	}
	doc, _ := json.Marshal(map[string]any{"tags": tags})
	mustWrite(t, filepath.Join(dir, "share.json"), string(doc))
	return body
}

func TestRemoteBindWritesTheOwnerFiles(t *testing.T) {
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	owner, _ := nostr.NewSecretKey()
	shareRoot := filepath.Join(home, ".amq-squad", "buzz", "root")
	body := writeTestShare(t, shareRoot, "amq-squad", owner.PublicHex(), time.Now().Add(30*24*time.Hour))
	bind := func() (remoteBindData, error) {
		var data remoteBindData
		stdout, _, err := captureOutput(t, func() error {
			return runRemote([]string{"bind", "--relay", "wss://relay.example", "--channel", "dm-1", "--json"}, "test")
		})
		if err == nil {
			var env struct{ Data remoteBindData }
			if jerr := json.Unmarshal([]byte(stdout), &env); jerr != nil {
				t.Fatalf("json: %v\n%s", jerr, stdout)
			}
			data = env.Data
		}
		return data, err
	}
	// 5-amq-B: a share still bound to the presence-only fake adapter (4-5
	// deviation 1) is refused, and nothing is written; no fallback
	writeShareManifest(t, shareRoot, "wss://relay.example", "dm-1", "fake")
	if _, err := bind(); err == nil || !strings.Contains(err.Error(), "retired") {
		t.Fatalf("bind on the fake adapter: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".amq-squad", "gate-binding.json")); err == nil {
		t.Fatal("a refused bind wrote the binding")
	}
	writeShareManifest(t, shareRoot, "wss://relay.example", "dm-1", "mailbox")
	data, err := bind()
	if err != nil {
		t.Fatal(err)
	}
	if data.Grants != 8 || data.Body != body.PublicHex() || data.Owner != owner.PublicHex() || len(data.Created) != 2 {
		t.Fatalf("bind = %+v", data)
	}
	if m := data.Mailbox; m.Kind != "mailbox" || m.Handle != "attach" || m.ReplyTimeout != "60s" || !m.Commands || m.Channel != "dm-1" {
		t.Fatalf("bind's mailbox binding = %+v", m)
	}
	b, pin, err := gateverify.LoadBinding(filepath.Join(home, ".amq-squad", "gate-binding.json"))
	if err != nil || pin.Body != body.PublicHex() || pin.Owner != owner.PublicHex() || b.Channel != "dm-1" || b.ShareRoot != shareRoot || b.ShareSession != "amq-squad" || b.Carriers[0] != "buzz" {
		t.Fatalf("binding %+v %+v, %v", b, pin, err)
	}
	for _, p := range []string{"gate-binding.json", "issuer-key.pem", "authorization-trust.json"} {
		info, err := os.Stat(filepath.Join(home, ".amq-squad", p))
		if err != nil || info.Mode().Perm() != 0o600 {
			t.Fatalf("%s: %v %v", p, info.Mode(), err)
		}
	}
	keyBefore, _ := os.ReadFile(filepath.Join(home, ".amq-squad", "issuer-key.pem"))
	// a second bind (a renewal) keeps the issuer key and the trust store
	data, err = bind()
	if err != nil || len(data.Created) != 0 {
		t.Fatalf("rebind = %+v, %v", data, err)
	}
	keyAfter, _ := os.ReadFile(filepath.Join(home, ".amq-squad", "issuer-key.pem"))
	if string(keyBefore) != string(keyAfter) {
		t.Fatal("rebind replaced the issuer key")
	}
	// a trust store that does not trust the issuer key is the owner's to fix
	mustWrite(t, filepath.Join(home, ".amq-squad", "authorization-trust.json"),
		`{"schema_version":1,"keys":[{"key_id":"ed25519-sha256:`+strings.Repeat("ab", 32)+`","algorithm":"ed25519","public_key":"`+strings.Repeat("A", 43)+`=","status":"trusted"}]}`)
	if _, err := bind(); err == nil {
		t.Fatal("bind accepted a trust store that does not trust the issuer")
	}
	// the bind output carries no secret of the body
	stdout, _, _ := captureOutput(t, func() error {
		return runRemote([]string{"bind", "--relay", "wss://relay.example", "--channel", "dm-1"}, "test")
	})
	keyFile, _ := os.ReadFile(filepath.Join(shareRoot, "extensions", "remote", "keys", "amq-squad", "body.key"))
	secret := strings.Fields(string(keyFile))[1]
	if strings.Contains(stdout, secret) {
		t.Fatal("bind printed the body secret")
	}
	// an expired share does not bind
	writeTestShare(t, shareRoot, "old", owner.PublicHex(), time.Now().Add(-time.Hour))
	if _, _, err := captureOutput(t, func() error {
		return runRemote([]string{"bind", "--relay", "wss://relay.example", "--channel", "dm-1", "--share-session", "old"}, "test")
	}); err == nil {
		t.Fatal("an expired share bound")
	}
}

func TestRemoteNpub(t *testing.T) {
	k, _ := nostr.NewSecretKey()
	npub, _ := nostr.EncodeNpub(k.PublicHex())
	for _, in := range []string{k.PublicHex(), npub} {
		stdout, _, err := captureOutput(t, func() error { return runRemote([]string{"npub", in}, "test") })
		if err != nil || strings.TrimSpace(stdout) != k.PublicHex()+" "+npub {
			t.Fatalf("npub %s = %q, %v", in, stdout, err)
		}
	}
	if _, _, err := captureOutput(t, func() error { return runRemote([]string{"npub", "nope"}, "test") }); err == nil {
		t.Fatal("a bad key was accepted")
	}
}

type remoteFakeEndpoint struct{ running, starts, stops int }

func (f *remoteFakeEndpoint) Running() bool { return f.running > 0 }
func (f *remoteFakeEndpoint) Start() error  { f.running, f.starts = 1, f.starts+1; return nil }
func (f *remoteFakeEndpoint) Stop() error {
	if f.running > 0 {
		f.stops++
	}
	f.running = 0
	return nil
}

type remoteFakeMailer struct{ sent []buzz.Mail }

func (m *remoteFakeMailer) Send(_ context.Context, mail buzz.Mail) (string, error) {
	m.sent = append(m.sent, mail)
	return fmt.Sprintf("m%d", len(m.sent)), nil
}

type remoteFakeService struct{ st buzz.ServiceState }

func (s remoteFakeService) Status(context.Context, string) (buzz.ServiceState, error) {
	return s.st, nil
}

// remote attach --once through the CLI: the core over the gate fixture's
// transport, the pump's routing through the mailer, and the endpoint
// started only while the lead's status row is present.
func TestRemoteAttachOnce(t *testing.T) {
	f := newGateCLIFixture(t)
	ep, mailer := &remoteFakeEndpoint{}, &remoteFakeMailer{}
	prevE, prevM, prevS := remoteEndpoint, remoteMailer, remoteService
	remoteEndpoint = func(string) buzz.Endpoint { return ep }
	remoteMailer = mailer
	remoteService = remoteFakeService{buzz.ServiceState{State: "missing", Root: "/m"}}
	t.Cleanup(func() { remoteEndpoint, remoteMailer, remoteService = prevE, prevM, prevS })
	args := []string{"attach", "--once", "--json", "--project", f.project, "--session", "s", "--me", "cto", "--lead", "cto", "--leads", "lead"}
	seedNotifyLaunch(t, f.project, f.base, "s", "lead")   // role lead
	seedNotifyLaunch(t, f.project, f.base, "s", "worker") // role worker
	// 4-5 review item 3: a destination that is not a lead seat is refused
	// at attach, with the reason
	bad := []string{"attach", "--once", "--project", f.project, "--session", "s", "--me", "cto", "--lead", "cto", "--leads", "lead,worker"}
	if _, _, err := captureOutput(t, func() error { return runRemote(bad, "test") }); err == nil || !strings.Contains(err.Error(), `worker is a "worker" seat, not a lead`) {
		t.Fatalf("--leads worker: %v", err)
	}

	f.answer(t, "@lead rebase #12", "")
	stdout, _, err := captureOutput(t, func() error { return runRemote(args, "test") })
	if err != nil {
		t.Fatalf("attach: %v\n%s", err, stdout)
	}
	if ep.starts != 0 || !strings.Contains(stdout, `"kind":"attached"`) || !strings.Contains(stdout, `"mailbox":"cto"`) {
		t.Fatalf("attach with no live lead: starts %d\n%s", ep.starts, stdout)
	}
	if len(mailer.sent) != 2 || mailer.sent[0].To != "cto" || mailer.sent[1].To != "lead" || mailer.sent[1].Body != "rebase #12" {
		t.Fatalf("sent = %+v\n%s", mailer.sent, stdout)
	}

	// the lead's session is live: presence starts the endpoint, and the
	// one-tick run stops it again on exit (offline)
	raw, _ := json.Marshal(map[string]any{"status": "active", "last_seen": time.Now().UTC()})
	mustWrite(t, filepath.Join(f.root, "agents", "cto", "presence.json"), string(raw))
	// (serve answers /status itself and never delivers it: v0.116.2
	// internal/remote/buzzio/normalize.go; the attachment sees plain text)
	f.answer(t, "standup at ten", "")
	stdout, _, err = captureOutput(t, func() error { return runRemote(args, "test") })
	if err != nil {
		t.Fatalf("attach 2: %v\n%s", err, stdout)
	}
	if ep.starts != 1 || ep.stops != 1 || !strings.Contains(stdout, `"action":"started"`) || !strings.Contains(stdout, `"reply":"routed cto"`) {
		t.Fatalf("attach with a live lead: starts %d stops %d\n%s", ep.starts, ep.stops, stdout)
	}
	if len(mailer.sent) != 3 {
		t.Fatalf("the first message was delivered twice: %+v", mailer.sent)
	}

	// remote status: presence, activity and the login service warning
	stdout, _, err = captureOutput(t, func() error {
		return runRemote([]string{"status", "--json", "--project", f.project, "--session", "s", "--me", "cto", "--lead", "cto"}, "test")
	})
	if err != nil {
		t.Fatalf("status: %v\n%s", err, stdout)
	}
	var env struct{ Data remoteStatusData }
	if err := json.Unmarshal([]byte(stdout), &env); err != nil {
		t.Fatalf("%v\n%s", err, stdout)
	}
	if !env.Data.Presence.Present || !strings.Contains(env.Data.ServiceWarning, "absent") {
		t.Fatalf("status = %+v", env.Data)
	}
}
