package buzz

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/btcec/v2/schnorr"

	"github.com/omriariav/amq-squad/v3/internal/gatestore"
	"github.com/omriariav/amq-squad/v3/internal/gateverify"
	"github.com/omriariav/amq-squad/v3/internal/nostr"
	"github.com/omriariav/amq-squad/v3/internal/operatorauth"
	"github.com/omriariav/amq-squad/v3/internal/status"
)

// realTempDir is t.TempDir with symlinks resolved (macOS /var is a symlink,
// and the owner-file readers refuse a symlinked ancestor).
func realTempDir(t *testing.T) string {
	t.Helper()
	d, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// buzzHome points HOME at a fresh directory and returns its
// ~/.amq-squad/buzz, the only place a share root may live.
func buzzHome(t *testing.T) string {
	t.Helper()
	home := realTempDir(t)
	t.Setenv("HOME", home)
	return filepath.Join(home, ".amq-squad", "buzz")
}

// throwaway is a throwaway owner key for tests: never a real key.
type throwaway struct {
	priv *btcec.PrivateKey
	sk   nostr.SecretKey
}

func newThrowaway(t *testing.T) throwaway {
	t.Helper()
	priv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	sk, err := nostr.SecretKeyFromHex(hex.EncodeToString(priv.Serialize()))
	if err != nil {
		t.Fatal(err)
	}
	return throwaway{priv: priv, sk: sk}
}

func (k throwaway) hex() string    { return hex.EncodeToString(k.priv.Serialize()) }
func (k throwaway) pubHex() string { return k.sk.PublicHex() }

// grant signs one NIP-OA grant: sha256("nostr:agent-auth:" body ":" conditions).
func (k throwaway) grant(t *testing.T, body string, kind int, notAfter int64) Tag {
	t.Helper()
	cond := fmt.Sprintf("kind=%d&created_at<%d", kind, notAfter)
	sum := sha256.Sum256([]byte("nostr:agent-auth:" + body + ":" + cond))
	sig, err := schnorr.Sign(k.priv, sum[:])
	if err != nil {
		t.Fatal(err)
	}
	return Tag{Kind: kind, OwnerPubKey: k.pubHex(), Conditions: cond, Sig: hex.EncodeToString(sig.Serialize())}
}

// writeShare writes an enrolled share in amq-remote's layout.
func writeShare(t *testing.T, root, session string, owner throwaway, body nostr.SecretKey, kinds []int, notAfter time.Time) {
	t.Helper()
	dir := filepath.Join(root, "extensions", "remote", "keys", session)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	b := newThrowawayFrom(t, body)
	if err := os.WriteFile(filepath.Join(dir, "body.key"), []byte("secret "+b+"\npublic "+body.PublicHex()+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var tags []Tag
	for _, k := range kinds {
		tags = append(tags, owner.grant(t, body.PublicHex(), k, notAfter.Unix()))
	}
	raw, _ := json.Marshal(map[string]any{"tags": tags})
	if err := os.WriteFile(filepath.Join(dir, "share.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

// bodies maps a test body key to its secret hex (nostr.SecretKey keeps it
// unexported, so tests mint bodies through newBody).
var (
	bodiesMu sync.Mutex
	bodies   = map[string]string{}
)

func newBody(t *testing.T) nostr.SecretKey {
	t.Helper()
	k := newThrowaway(t)
	bodiesMu.Lock()
	bodies[k.pubHex()] = k.hex()
	bodiesMu.Unlock()
	return k.sk
}

func newThrowawayFrom(t *testing.T, body nostr.SecretKey) string {
	t.Helper()
	bodiesMu.Lock()
	defer bodiesMu.Unlock()
	h, ok := bodies[body.PublicHex()]
	if !ok {
		t.Fatal("body not minted with newBody")
	}
	return h
}

func newIssuer(t *testing.T, dir string) (operatorauth.AuthorizationSigner, operatorauth.AuthorizationTrustStore) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	der, _ := x509.MarshalPKCS8PrivateKey(priv)
	keyPath, trustPath := filepath.Join(dir, "issuer.pem"), filepath.Join(dir, "trust.json")
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(operatorauth.AuthorizationTrustStore{SchemaVersion: 1, Keys: []operatorauth.AuthorizationTrustKey{{
		KeyID: operatorauth.KeyIDForEd25519(pub), Algorithm: "ed25519", PublicKey: base64.StdEncoding.EncodeToString(pub), Status: "trusted"}}})
	if err := os.WriteFile(trustPath, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	signer, err := operatorauth.LoadEd25519Signer(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	trust, err := operatorauth.LoadAuthorizationTrustStore(trustPath)
	if err != nil {
		t.Fatal(err)
	}
	return signer, trust
}

// coreFixture is a 3-1 core over an in-memory relay, the owner and body
// throwaway keys, a gate store and an issuer.
type coreFixture struct {
	root  string
	owner throwaway
	body  nostr.SecretKey
	relay gateverify.Relay
	core  *gateverify.Core
	trust operatorauth.AuthorizationTrustStore
}

func newCoreFixture(t *testing.T, relay gateverify.Relay) coreFixture {
	t.Helper()
	dir := realTempDir(t)
	f := coreFixture{root: filepath.Join(dir, "root"), owner: newThrowaway(t), body: newBody(t), relay: relay}
	if err := os.MkdirAll(f.root, 0o700); err != nil {
		t.Fatal(err)
	}
	store, err := gatestore.Open(f.root)
	if err != nil {
		t.Fatal(err)
	}
	issuer, trust := newIssuer(t, dir)
	f.trust = trust
	f.core = &gateverify.Core{Store: store, Pin: gateverify.Pin{Owner: f.owner.pubHex(), Channel: "dm-4-5", Body: f.body.PublicHex()},
		Relay: relay, RelayURL: "ws://127.0.0.1/test", Body: f.body, Issuer: issuer, Carriers: []string{CarrierHandle}, Version: "3",
		Namespace: gateverify.Namespace{ProjectDir: dir, Profile: "p", Session: "s", Generation: "g1"}}
	return f
}

// ownerDM signs and publishes a kind 9 from the owner in the channel.
func (f coreFixture) ownerDM(t *testing.T, content, thread string) string {
	t.Helper()
	tags := [][]string{{"h", f.core.Pin.Channel}, {"p", f.body.PublicHex()}}
	if thread != "" {
		tags = append(tags, []string{"e", thread, "", "reply"})
	}
	e := nostr.Event{CreatedAt: time.Now().Unix(), Kind: 9, Tags: tags, Content: content}
	if err := f.owner.sk.Sign(&e); err != nil {
		t.Fatal(err)
	}
	if err := f.relay.Publish(context.Background(), e.Marshal()); err != nil {
		t.Fatal(err)
	}
	return e.ID
}

// fakeMailer records AMQ sends.
type fakeMailer struct {
	mu   sync.Mutex
	sent []Mail
	err  error
}

func (m *fakeMailer) Send(_ context.Context, mail Mail) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.err != nil {
		return "", m.err
	}
	m.sent = append(m.sent, mail)
	return fmt.Sprintf("msg-%d", len(m.sent)), nil
}

// statusDoc builds a one-squad status document with one seat.
func statusDoc(t *testing.T, handle string, live bool) []byte {
	t.Helper()
	now := time.Now().UTC()
	seat := status.SeatInput{Handle: handle, Live: live, HasSignal: true, LastMessage: "merged #12"}
	if live {
		seat.LastActivity = &now
	}
	st := status.Build(status.Input{Root: "/r", Session: "s", Now: now, Seats: []status.SeatInput{seat},
		Gates: []status.GateInput{{ID: "g-open", Class: "tag", Digest: "sha256:" + fmt.Sprintf("%064d", 0), State: "open", RaisedBy: handle, Since: now}}})
	raw, err := json.Marshal(st)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
