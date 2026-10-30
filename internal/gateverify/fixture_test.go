package gateverify

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
	"github.com/omriariav/amq-squad/v3/internal/gatetarget"
	"github.com/omriariav/amq-squad/v3/internal/nostr"
	"github.com/omriariav/amq-squad/v3/internal/operatorauth"
)

var testNow = time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)

const testChannel = "d63ab8d9-c495-4893-9538-f47a0c0cdc70"

// memRelay is an in-memory Relay: it stores what is published or injected
// and answers queries by NIP-01 filter. It models a relay that returns its
// full history; failQuery makes every Query fail.
type memRelay struct {
	mu        sync.Mutex
	stored    [][]byte
	failQuery bool
}

func (r *memRelay) inject(raw []byte) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.stored = append(r.stored, append([]byte(nil), raw...))
}

func (r *memRelay) Publish(_ context.Context, raw []byte) error {
	if _, err := nostr.ParseEvent(raw); err != nil {
		return err
	}
	r.inject(raw)
	return nil
}

func (r *memRelay) FetchByID(ctx context.Context, id string) ([]byte, error) {
	got, err := r.Query(ctx, Filter{IDs: []string{id}})
	if err != nil || len(got) == 0 {
		return nil, errors.Join(err, errors.New("relay has no event "+id))
	}
	return got[0], nil
}

func (r *memRelay) Query(_ context.Context, f Filter) ([][]byte, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.failQuery {
		return nil, errors.New("relay closed the request")
	}
	var out [][]byte
	for _, raw := range r.stored {
		if e, err := nostr.ParseEvent(raw); err == nil && filterMatches(f, e) {
			out = append(out, raw)
		}
	}
	return out, nil
}

func filterMatches(f Filter, e nostr.Event) bool {
	switch {
	case f.IDs != nil && !slices.Contains(f.IDs, e.ID):
		return false
	case f.Kinds != nil && !slices.Contains(f.Kinds, e.Kind):
		return false
	case f.Authors != nil && !slices.Contains(f.Authors, e.PubKey):
		return false
	case f.Since > 0 && e.CreatedAt < f.Since:
		return false
	}
	for name, want := range f.Tags {
		if !slices.ContainsFunc(nostr.TagValues(e, name), func(v string) bool { return slices.Contains(want, v) }) {
			return false
		}
	}
	return true
}

type fixture struct {
	t      *testing.T
	owner  nostr.SecretKey
	body   nostr.SecretKey
	seat   nostr.SecretKey
	relay  *memRelay
	store  gatestore.Store
	core   *Core
	trust  operatorauth.AuthorizationTrustStore
	issuer operatorauth.AuthorizationSigner
	clock  time.Time
}

func mustKey(t *testing.T) nostr.SecretKey {
	t.Helper()
	k, err := nostr.NewSecretKey()
	if err != nil {
		t.Fatal(err)
	}
	return k
}

// newIssuer writes a throwaway Ed25519 issuer key and its trust store under
// dir and loads both through operatorauth.
func newIssuer(t *testing.T, dir string) (operatorauth.AuthorizationSigner, operatorauth.AuthorizationTrustStore) {
	t.Helper()
	dir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
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

func newFixture(t *testing.T) *fixture {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	store, err := gatestore.Open(filepath.Join(dir, "root"))
	if err != nil {
		t.Fatal(err)
	}
	f := &fixture{t: t, owner: mustKey(t), body: mustKey(t), seat: mustKey(t), relay: &memRelay{}, store: store, clock: testNow}
	f.issuer, f.trust = newIssuer(t, dir)
	f.core = &Core{
		Store: store, Pin: Pin{Owner: f.owner.PublicHex(), Channel: testChannel, Body: f.body.PublicHex()},
		Relay: f.relay, RelayURL: "wss://relay.test", Body: f.body, Issuer: f.issuer,
		Namespace: Namespace{ProjectDir: dir, Profile: "default", Session: "s1", Generation: "g1"}, Version: "test",
		Carriers: []string{"buzz"}, Now: func() time.Time { return f.clock },
	}
	return f
}

// raise raises a gate through the core at the fixture clock.
func (f *fixture) raise(id, action, target string) (gatestore.Record, gatestore.Question) {
	f.t.Helper()
	rec, q, err := f.core.Raise(context.Background(), gatestore.Spec{GateID: id, Action: action, Target: target, RaisedBy: "lead"})
	if err != nil {
		f.t.Fatal(err)
	}
	return rec, q
}

func prefixOf(rec gatestore.Record) string { return strings.TrimPrefix(rec.Digest, "sha256:")[:16] }

func tuple(verb string, rec gatestore.Record) string {
	return verb + " " + rec.GateID + " " + rec.TypedClass + " " + prefixOf(rec)
}

// answer signs a kind 9 owner reply. thread "" means a plain DM (no e tag),
// as g-owner-01; otherwise the Buzz thread-reply tag of g-owner-02.
func (f *fixture) answer(k nostr.SecretKey, content, thread string, at time.Time) []byte {
	f.t.Helper()
	tags := [][]string{{"h", testChannel}}
	if thread != "" {
		tags = append(tags, []string{"e", thread, "", "reply"})
	}
	tags = append(tags, []string{"p", f.body.PublicHex()})
	e := nostr.Event{CreatedAt: at.Unix(), Kind: 9, Tags: tags, Content: content}
	if err := k.Sign(&e); err != nil {
		f.t.Fatal(err)
	}
	return e.Marshal()
}

// classes are the four typed classes, one action and target each.
var classes = []struct{ action, target string }{
	{"tag", gatetarget.Command("git tag", "v1.2.0")},
	{"external_send", gatetarget.MCPSend("mcp__slack__send_message", "C1")},
	{"credential_change", gatetarget.Command("gh auth login", "")},
	{"spawn", gatetarget.Grow("/r/s1", 13)},
}
