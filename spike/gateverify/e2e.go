package main

// Synthetic end to end on a public relay: a THROWAWAY owner key signs a
// kind 9 approve over a rendered gate, the event is published, fetched back
// by id over a fresh connection, verified, wrapped in an operatorauth
// envelope by a scratch issuer key, and the envelope is verified again. The
// content is synthetic (gate ids, a digest, PR numbers); no real key, code
// or path is published.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/operatorauth"
)

type e2eResult struct {
	Relay        string `json:"relay"`
	GateID       string `json:"gate_id"`
	EventID      string `json:"event_id"`
	OwnerNpub    string `json:"owner_npub_throwaway"`
	PublishMS    int64  `json:"publish_ms"`
	FetchMS      int64  `json:"fetch_ms"`
	VerifyUS     int64  `json:"verify_and_envelope_us"`
	RoundTripMS  int64  `json:"round_trip_ms"`
	Decision     string `json:"decision"`
	EnvelopeOK   bool   `json:"envelope_verified"`
	AuthzID      string `json:"authorization_id"`
	AnswerFile   string `json:"answer_file"`
	FetchedBytes int    `json:"fetched_bytes"`
}

func randHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func cmdE2E(args []string, stdout, stderr io.Writer) int {
	fs := newFlags("e2e-synthetic", stderr)
	relay := fs.String("relay", "wss://nos.lol", "public relay")
	home, _ := os.UserHomeDir()
	scratch := fs.String("scratch", filepath.Join(home, ".amq-squad", "spike", "s1", "e2e"), "scratch dir (outside the repo)")
	runs := fs.Int("runs", 1, "repetitions")
	if fs.Parse(args) != nil {
		return 2
	}
	if err := os.MkdirAll(*scratch, 0o700); err != nil {
		return fail(stderr, "%v", err)
	}
	ownerKeyPath := filepath.Join(*scratch, "owner-throwaway.secret")
	sk, err := loadSecret(ownerKeyPath)
	if err != nil {
		if sk, err = NewSecretKey(); err != nil {
			return fail(stderr, "%v", err)
		}
		if err := os.WriteFile(ownerKeyPath, []byte(sk.Hex()+"\n"), 0o600); err != nil {
			return fail(stderr, "%v", err)
		}
	}
	issuerKey := filepath.Join(*scratch, "issuer.pem")
	storePath := filepath.Join(*scratch, "trust.json")
	if _, err := os.Stat(issuerKey); err != nil {
		if _, err := WriteIssuerKey(issuerKey, storePath); err != nil {
			return fail(stderr, "issuer: %v", err)
		}
	}
	signer, err := operatorauth.LoadEd25519Signer(issuerKey)
	if err != nil {
		return fail(stderr, "%v", err)
	}
	defer signer.Destroy()
	store, err := operatorauth.LoadAuthorizationTrustStore(storePath)
	if err != nil {
		return fail(stderr, "%v", err)
	}
	bodyKeyPath := filepath.Join(*scratch, "body-throwaway.secret")
	body, err := loadSecret(bodyKeyPath)
	if err != nil {
		if body, err = NewSecretKey(); err != nil {
			return fail(stderr, "%v", err)
		}
		if err := os.WriteFile(bodyKeyPath, []byte(body.Hex()+"\n"), 0o600); err != nil {
			return fail(stderr, "%v", err)
		}
	}
	pin := Pin{Owner: sk.PublicHex(), Channel: "s1-synthetic-" + randHex(4), Body: body.PublicHex(), RequireThread: true}
	npub, _ := EncodeNpub(pin.Owner)
	failed := 0
	for i := 0; i < *runs; i++ {
		res, err := e2eOnce(*relay, *scratch, sk, body, pin, npub, signer, store)
		if err != nil {
			fmt.Fprintf(stderr, "run %d: %v\n", i+1, err)
			failed++
			continue
		}
		raw, _ := json.Marshal(res)
		fmt.Fprintln(stdout, string(raw))
	}
	if failed > 0 {
		return 1
	}
	return 0
}

func e2eOnce(relay, scratch string, sk, body SecretKey, pin Pin, npub string, signer operatorauth.AuthorizationSigner, trust operatorauth.AuthorizationTrustStore) (e2eResult, error) {
	// scratch plays the AMQ root: the gate store and the ledger live under it.
	gates, ledger := GateStoreFor(scratch), LedgerFor(scratch)
	spec := GateSpec{
		GateID: "s1-" + randHex(4), Class: "merge", Repo: "synthetic/s1-spike",
		BaseSHA: randHex(20), PRs: []PRRef{{Number: 101, HeadSHA: randHex(20)}, {Number: 102, HeadSHA: randHex(20)}},
		WindowMinutes: 30,
	}
	rendered, err := Render(spec, time.Now(), GateIdentity{OwnerPubKey: pin.Owner, IssuerKeyID: signer.KeyID()})
	if err != nil {
		return e2eResult{}, err
	}
	// The record is stored before the question is sent (binding rule).
	if err := gates.Create(rendered); err != nil {
		return e2eResult{}, err
	}
	ctx := context.Background()
	question := Event{CreatedAt: time.Now().Unix(), Kind: 9, Tags: [][]string{{"h", pin.Channel}, {"p", pin.Owner}}, Content: rendered.Text}
	if err := body.Sign(&question); err != nil {
		return e2eResult{}, err
	}
	qc, err := Dial(ctx, relay)
	if err != nil {
		return e2eResult{}, err
	}
	err = qc.Publish(ctx, question.Marshal())
	qc.Close()
	if err != nil {
		return e2eResult{}, fmt.Errorf("publish question: %w", err)
	}
	if err := gates.PutQuestion(GateQuestion{GateID: rendered.GateID, EventID: question.ID, RawEvent: string(question.Marshal())}); err != nil {
		return e2eResult{}, err
	}
	prefix := strings.TrimPrefix(rendered.Digest, "sha256:")[:minDigestPrefix]
	evt := Event{CreatedAt: time.Now().Unix(), Kind: 9, Tags: [][]string{{"h", pin.Channel}, {"e", question.ID, "", "reply"}},
		Content: fmt.Sprintf("approve %s merge %s", rendered.GateID, prefix)}
	if err := sk.Sign(&evt); err != nil {
		return e2eResult{}, err
	}
	t0 := time.Now()
	pub, err := Dial(ctx, relay)
	if err != nil {
		return e2eResult{}, err
	}
	err = pub.Publish(ctx, evt.Marshal())
	pub.Close()
	if err != nil {
		return e2eResult{}, fmt.Errorf("publish: %w", err)
	}
	t1 := time.Now()
	// A fresh connection, so the fetch proves the relay stored the event.
	fc, err := Dial(ctx, relay)
	if err != nil {
		return e2eResult{}, err
	}
	raw, err := fc.FetchByID(ctx, evt.ID)
	fc.Close()
	if err != nil {
		return e2eResult{}, fmt.Errorf("fetch: %w", err)
	}
	t2 := time.Now()
	// The verifier reads the gate and question from the store, not from
	// anything in hand.
	gate, err := gates.Load(rendered.GateID)
	if err != nil {
		return e2eResult{}, err
	}
	q, err := gates.Question(rendered.GateID)
	if err != nil {
		return e2eResult{}, err
	}
	o, err := Verify(raw, gate, q, pin, time.Now())
	if err != nil {
		return e2eResult{}, fmt.Errorf("verify: %w", err)
	}
	if err := ledger.Consumed(o); err != nil {
		return e2eResult{}, err
	}
	cwd, _ := os.Getwd()
	f, err := BuildAnswerFile(o, raw, gate, q, pin, relay, Namespace{ProjectDir: filepath.Clean(cwd), Profile: defaultProfileName, Session: "s1-synthetic", Generation: "gen-synthetic"}, signer, time.Now())
	if err != nil {
		return e2eResult{}, err
	}
	answerPath := filepath.Join(scratch, "extensions", "amq-squad", "answers", gate.GateID+".json")
	blob, _ := json.MarshalIndent(f, "", "  ")
	// Commit writes the envelope, then consumes, under the gate's lock.
	if err := ledger.Commit(o, &Envelope{Path: answerPath, Data: append(blob, '\n')}); err != nil {
		return e2eResult{}, err
	}
	if err := VerifyAnswerFile(f, trust, pin, false); err != nil {
		return e2eResult{}, fmt.Errorf("answer file: %w", err)
	}
	t3 := time.Now()
	_ = os.WriteFile(filepath.Join(scratch, gate.GateID+".event.json"), raw, 0o600)
	return e2eResult{
		Relay: relay, GateID: gate.GateID, EventID: evt.ID, OwnerNpub: npub,
		PublishMS: t1.Sub(t0).Milliseconds(), FetchMS: t2.Sub(t1).Milliseconds(), VerifyUS: t3.Sub(t2).Microseconds(),
		RoundTripMS: t3.Sub(t0).Milliseconds(), Decision: o.Decision, EnvelopeOK: true,
		AuthzID: f.Envelope.Payload.AuthorizationID, AnswerFile: answerPath, FetchedBytes: len(raw),
	}, nil
}
