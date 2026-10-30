package fleet

import (
	"context"
	"crypto/ed25519"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/gatestore"
	"github.com/omriariav/amq-squad/v3/internal/gatetarget"
	"github.com/omriariav/amq-squad/v3/internal/gateverify"
	"github.com/omriariav/amq-squad/v3/internal/nostr"
	"github.com/omriariav/amq-squad/v3/internal/operatorauth"
)

// Acceptance 5 (R41): the mayor never approves a typed gate. The mayor's
// process holds no owner key; an approval it signs with the key it does hold
// is rejected by the core for every typed class, the gate stays open, and
// the consumer check finds no accepted decision. The owner's own answer on
// the same fixture is accepted, so the rejection is the key, not the
// fixture.

type relay struct {
	mu     sync.Mutex
	stored [][]byte
}

func (r *relay) Publish(_ context.Context, raw []byte) error {
	if _, err := nostr.ParseEvent(raw); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.stored = append(r.stored, append([]byte(nil), raw...))
	return nil
}

func (r *relay) FetchByID(ctx context.Context, id string) ([]byte, error) {
	got, err := r.Query(ctx, gateverify.Filter{IDs: []string{id}})
	if err != nil || len(got) == 0 {
		return nil, errors.Join(err, errors.New("no event "+id))
	}
	return got[0], nil
}

func (r *relay) Query(_ context.Context, f gateverify.Filter) ([][]byte, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out [][]byte
	for _, raw := range r.stored {
		e, err := nostr.ParseEvent(raw)
		if err != nil || f.IDs != nil && !slices.Contains(f.IDs, e.ID) || f.Kinds != nil && !slices.Contains(f.Kinds, e.Kind) ||
			f.Authors != nil && !slices.Contains(f.Authors, e.PubKey) || f.Since > 0 && e.CreatedAt < f.Since {
			continue
		}
		match := true
		for name, want := range f.Tags {
			if !slices.ContainsFunc(nostr.TagValues(e, name), func(v string) bool { return slices.Contains(want, v) }) {
				match = false
			}
		}
		if match {
			out = append(out, raw)
		}
	}
	return out, nil
}

func key(t *testing.T) nostr.SecretKey {
	t.Helper()
	k, err := nostr.NewSecretKey()
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func issuer(t *testing.T, dir string) (operatorauth.AuthorizationSigner, operatorauth.AuthorizationTrustStore) {
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

const channel = "d63ab8d9-c495-4893-9538-f47a0c0cdc70"

func TestMayorAnswerForATypedGateIsRejectedByTheCore(t *testing.T) {
	ctx := context.Background()
	at := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	for _, c := range []struct{ action, target string }{
		{"tag", gatetarget.Command("git tag", "v1.2.0")},
		{"external_send", gatetarget.MCPSend("mcp__slack__send_message", "C1")},
		{"credential_change", gatetarget.Command("gh auth login", "")},
		{"spawn", gatetarget.Grow("/r/s1", 13)},
	} {
		t.Run(c.action, func(t *testing.T) {
			dir, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			store, _ := gatestore.Open(filepath.Join(dir, "root"))
			owner, body := key(t), key(t)
			mayorKey := key(t) // the only key the mayor's process holds
			signer, trust := issuer(t, dir)
			clock := at
			core := &gateverify.Core{Store: store, Pin: gateverify.Pin{Owner: owner.PublicHex(), Channel: channel, Body: body.PublicHex()},
				Relay: &relay{}, RelayURL: "wss://relay.test", Body: body, Issuer: signer,
				Namespace: gateverify.Namespace{ProjectDir: dir, Profile: "default", Session: "s1", Generation: "g1"}, Version: "test",
				Now: func() time.Time { return clock }}
			verifier := gateverify.Verifier{Trust: trust, Pin: core.Pin, Namespace: core.Namespace}
			rec, q, err := core.Raise(ctx, gatestore.Spec{GateID: "g-" + strings.ReplaceAll(c.action, "_", "-"), Action: c.action, Target: c.target, RaisedBy: "lead"})
			if err != nil {
				t.Fatal(err)
			}
			tuple := "approve " + rec.GateID + " " + rec.TypedClass + " " + strings.TrimPrefix(rec.Digest, "sha256:")[:16]
			sign := func(k nostr.SecretKey, content string) []byte {
				e := nostr.Event{CreatedAt: at.Add(time.Minute).Unix(), Kind: 9, Content: content,
					Tags: [][]string{{"h", channel}, {"e", q.EventID, "", "reply"}, {"p", body.PublicHex()}}}
				if err := k.Sign(&e); err != nil {
					t.Fatal(err)
				}
				return e.Marshal()
			}
			clock = at.Add(time.Minute + time.Second)

			if mayorKey.PublicHex() == core.Pin.Owner || mayorKey.PublicHex() == core.Pin.Body {
				t.Fatal("the mayor's key is the owner's or the body's")
			}
			r := core.HandleEvent(ctx, sign(mayorKey, tuple))
			if r.Outcome != "rejected" || r.Code != gateverify.RejectWrongPubkey {
				t.Fatalf("mayor-signed approval: %+v, want rejected %s", r, gateverify.RejectWrongPubkey)
			}
			if d := store.Decision(rec.GateID); d == gatestore.StateAccepted {
				t.Fatal("a mayor answer left an accepted decision")
			}
			if err := verifier.Accepted(store, rec); err == nil {
				t.Fatal("the consumer check accepted a gate the mayor answered")
			}
			if _, ok := verifier.AcceptedFor(store, c.action, c.target); ok {
				t.Fatal("AcceptedFor found a gate the mayor answered")
			}
			// the issuer key alone cannot wrap the mayor's answer either
			ev, _ := nostr.ParseEvent(sign(mayorKey, tuple))
			ans, _ := gatestore.ParseAnswer(tuple)
			if _, err := gateverify.BuildAnswerFile(gateverify.Outcome{Decision: "approve", Event: ev, Answer: ans}, ev.Marshal(), rec, q, core.Pin, "wss://relay.test", core.Namespace, "test", signer, clock); err == nil {
				t.Fatal("an answer file was built for a mayor-signed event")
			}

			// control: the owner's answer on the same gate is accepted
			if r := core.HandleEvent(ctx, sign(owner, tuple)); r.Outcome != operatorauth.DecisionApproved {
				t.Fatalf("owner approval: %+v", r)
			}
			if err := verifier.Accepted(store, rec); err != nil {
				t.Fatalf("owner approval does not pass the consumer check: %v", err)
			}
		})
	}
}

// The structural half of acceptance 5: the mayor's and the lead session's
// code has no path that answers a gate. No non-test file of internal/fleet or
// internal/leadsession imports the gate store, the verifier, the nostr
// signer or operatorauth, so none can sign, build or commit an answer.
func TestMayorHasNoGateAnsweringPath(t *testing.T) {
	forbidden := []string{"internal/gatestore", "internal/gateverify", "internal/nostr", "internal/operatorauth"}
	checked := 0
	for _, dir := range []string{".", filepath.Join("..", "leadsession")} {
		files, _ := filepath.Glob(filepath.Join(dir, "*.go"))
		for _, path := range files {
			if strings.HasSuffix(path, "_test.go") {
				continue
			}
			f, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
			if err != nil {
				t.Fatal(err)
			}
			checked++
			for _, imp := range f.Imports {
				p, _ := strconv.Unquote(imp.Path.Value)
				for _, bad := range forbidden {
					if strings.HasSuffix(p, bad) {
						t.Errorf("%s imports %s: the mayor must have no gate-answering path", path, p)
					}
				}
			}
		}
	}
	if checked < 5 {
		t.Fatalf("checked only %d files", checked)
	}
}
