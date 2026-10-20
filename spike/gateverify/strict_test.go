package main

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/btcec/v2/schnorr"
)

// signedRaw returns one valid owner-signed approve in NIP-01 field order.
func signedRaw(t *testing.T, f fixture, content string) string {
	t.Helper()
	return string(answer(t, f.owner, testNow.Add(time.Minute), content))
}

// TestStrictDecoding: every malformed wire form is refused before any
// semantic check. The first rows are the review's cases: duplicate keys,
// null and non-string tag elements, invalid UTF-8 and unpaired
// surrogates.
func TestStrictDecoding(t *testing.T) {
	f := newFixture(t)
	good := signedRaw(t, f, approveText(f.gate))
	e, _ := ParseEvent([]byte(good))
	rep := func(old, new string) string {
		if !strings.Contains(good, old) {
			t.Fatalf("fixture lacks %q", old)
		}
		return strings.Replace(good, old, new, 1)
	}
	cases := map[string]string{
		"duplicate key (content twice, second would win in Decode)": rep(`,"sig":`, `,"content":"deny `+f.gate.GateID+` merge `+prefixOf(f.gate)+`","sig":`),
		"duplicate key (id twice)":                                  rep(`"pubkey":`, `"id":"`+strings.Repeat("0", 64)+`","pubkey":`),
		"null tag element":                                          rep(`[["h","`+testChannel+`"]]`, `[["h",null]]`),
		"number tag element":                                        rep(`[["h","`+testChannel+`"]]`, `[["h",7]]`),
		"tag is not an array":                                       rep(`[["h","`+testChannel+`"]]`, `["h"]`),
		"tags is null":                                              rep(`[["h","`+testChannel+`"]]`, `null`),
		"content is null":                                           rep(`"content":"`+e.Content+`"`, `"content":null`),
		"invalid UTF-8 byte in content":                             rep(`"content":"approve`, "\"content\":\"\xffapprove"),
		"overlong UTF-8 encoding":                                   rep(`"content":"approve`, "\"content\":\"\xc0\xafapprove"),
		"unpaired high surrogate escape":                            rep(`"content":"approve`, `"content":"\ud83dapprove`),
		"unpaired low surrogate escape":                             rep(`"content":"approve`, `"content":"\ude42approve`),
		"reversed surrogate pair":                                   rep(`"content":"approve`, `"content":"\ude42\ud83dapprove`),
		"escaped C0 control in content":                             rep(`"content":"approve`, `"content":"\u0001approve`),
		"escaped form feed in a tag":                                rep(`"h","`+testChannel, `"h","\f`+testChannel),
		"created_at with fraction":                                  rep(`"created_at":`+itoa(e.CreatedAt), `"created_at":`+itoa(e.CreatedAt)+`.0`),
		"created_at with exponent":                                  rep(`"created_at":`+itoa(e.CreatedAt), `"created_at":1.79e9`),
		"created_at negative":                                       rep(`"created_at":`+itoa(e.CreatedAt), `"created_at":-1`),
		"created_at leading zero":                                   rep(`"created_at":`+itoa(e.CreatedAt), `"created_at":0`+itoa(e.CreatedAt)),
		"created_at as string":                                      rep(`"created_at":`+itoa(e.CreatedAt), `"created_at":"`+itoa(e.CreatedAt)+`"`),
		"kind out of range":                                         rep(`"kind":9`, `"kind":70000`),
		"unknown field":                                             rep(`{"id"`, `{"x":1,"id"`),
		"missing field":                                             rep(`,"kind":9`, ``),
		"trailing data":                                             good + `{}`,
		"two objects":                                               good + good,
		"not an object":                                             `[` + good + `]`,
		"uppercase hex id":                                          rep(`"id":"`+e.ID, `"id":"`+strings.ToUpper(e.ID)),
		"truncated":                                                 good[:len(good)-2],
		"oversized":                                                 rep(`"content":"approve`, `"content":"`+strings.Repeat(" ", maxEventBytes)+`approve`),
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseEvent([]byte(raw)); err == nil {
				t.Fatalf("accepted: %.200q", raw)
			}
			if _, err := Verify([]byte(raw), f.gate, nil, f.pin, testNow.Add(2*time.Minute)); RejectionCode(err) != RejectMalformed {
				t.Fatalf("Verify code %q", RejectionCode(err))
			}
		})
	}
	// Faithful wire forms are accepted: a valid escaped surrogate pair, and
	// escapes NIP-01 serialization reverses exactly.
	k := mustKey(t)
	emoji := Event{CreatedAt: testNow.Unix(), Kind: 9, Tags: [][]string{{"h", testChannel}}, Content: "ok \U0001F642   é"}
	if err := k.Sign(&emoji); err != nil {
		t.Fatal(err)
	}
	// The JSON escapes are assembled from parts, so no editor or tool can
	// fold them back into the literal characters (which once made this a
	// no-op). bs+"ud83d" is the six characters \ u d 8 3 d.
	const bs = "\\"
	verbatim := string(emoji.Marshal())
	pair := bs + "ud83d" + bs + "ude42"
	eAcute := bs + "u00e9"
	escaped := strings.Replace(verbatim, string(rune(0x1F642)), pair, 1)
	escaped = strings.Replace(escaped, string(rune(0xE9)), eAcute, 1)
	if escaped == verbatim || !strings.Contains(escaped, pair) || !strings.Contains(escaped, eAcute) ||
		strings.ContainsRune(escaped, 0x1F642) || strings.ContainsRune(escaped, 0xE9) || len(pair) != 12 {
		t.Fatalf("fixture did not escape: %q", escaped)
	}
	p, err := ParseEvent([]byte(escaped))
	if err != nil || !p.CheckID() || p.VerifySignature() != nil {
		t.Fatalf("escaped valid event refused: %v", err)
	}
	if p.Content != emoji.Content || p.ID != emoji.ID {
		t.Fatal("escaped event did not decode to the signed content")
	}
}

func itoa(n int64) string { return big.NewInt(n).String() }

// TestSignatureScalarBounds: s >= n and r >= p fail in CheckSigScalars,
// before the library verify is called. btcec v2.5.0 alone would reduce
// s mod n (schnorr/signature.go:93-94).
func TestSignatureScalarBounds(t *testing.T) {
	f := newFixture(t)
	good := answer(t, f.owner, testNow.Add(time.Minute), approveText(f.gate))
	e, _ := ParseEvent(good)
	sig, _ := hex.DecodeString(e.Sig)
	pad := func(n *big.Int) []byte { return n.FillBytes(make([]byte, 32)) }
	two256m1 := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 256), big.NewInt(1))
	n, p := btcec.S256().N, btcec.S256().P
	cases := []struct {
		name, want string
		r, s       []byte
	}{
		{"s == n", "s >= n", sig[:32], pad(n)},
		{"s == n + 1", "s >= n", sig[:32], pad(new(big.Int).Add(n, big.NewInt(1)))},
		{"s == 2^256 - 1", "s >= n", sig[:32], pad(two256m1)},
		{"r == p", "r >= p", pad(p), sig[32:]},
		{"r == 2^256 - 1", "r >= p", pad(two256m1), sig[32:]},
	}
	calls := 0
	saved := libraryVerify
	libraryVerify = func(s *schnorr.Signature, h []byte, k *btcec.PublicKey) bool { calls++; return saved(s, h, k) }
	t.Cleanup(func() { libraryVerify = saved })
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			before := calls
			bad := e
			bad.Sig = hex.EncodeToString(append(append([]byte(nil), c.r...), c.s...))
			_, err := Verify(bad.Marshal(), f.gate, nil, f.pin, testNow.Add(2*time.Minute))
			if RejectionCode(err) != RejectBadSignature || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("got %v, want bad_signature %q", err, c.want)
			}
			if calls != before {
				t.Fatal("library verify was called before the range check rejected")
			}
		})
	}
	// Boundary inside the range: s == n - 1 passes the range check (and then
	// fails verification as a wrong signature, in the library).
	if err := CheckSigScalars(append(append([]byte(nil), sig[:32]...), pad(new(big.Int).Sub(n, big.NewInt(1)))...)); err != nil {
		t.Fatalf("s == n-1 refused by the range check: %v", err)
	}
	if _, err := Verify(good, f.gate, nil, f.pin, testNow.Add(2*time.Minute)); err != nil || calls == 0 {
		t.Fatalf("good signature: %v (library calls %d)", err, calls)
	}
}

// TestCrashAndRestart drives the CLI (run) as separate invocations, the
// way restarts happen.
func TestCrashAndRestart(t *testing.T) {
	f := newFixture(t)
	dir := realTempDir(t)
	eventPath := filepath.Join(dir, "event.json")
	if err := os.WriteFile(eventPath, answer(t, f.owner, testNow.Add(time.Minute), approveText(f.gate), f.reply()), 0o600); err != nil {
		t.Fatal(err)
	}
	verify := func(extra ...string) (int, string) {
		var out, errb bytes.Buffer
		args := append([]string{"verify", "--event", eventPath, "--root", f.root, "--gate-id", f.gate.GateID,
			"--owner", f.pin.Owner, "--channel", testChannel, "--body", f.body.PublicHex(),
			"--project-dir", "/repo", "--now", testNow.Add(2 * time.Minute).Format(time.RFC3339)}, extra...)
		rc := run(args, &out, &errb)
		return rc, out.String() + errb.String()
	}
	answerPath := filepath.Join(f.root, "extensions", "amq-squad", "answers", f.gate.GateID+".json")
	ledger := LedgerFor(f.root)
	o, _ := Verify(answer(t, f.owner, testNow.Add(time.Minute), approveText(f.gate), f.reply()), f.gate, f.question, f.tpin, testNow.Add(2*time.Minute))

	// A consuming verify without an issuer key writes nothing and consumes
	// nothing: there is no envelope, so there is no consume.
	if rc, out := verify(); rc == 0 || !strings.Contains(out, "issuer key is required") {
		t.Fatalf("verify without issuer: rc %d %s", rc, out)
	}
	if _, err := os.Stat(ledger.Dir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("ledger touched without an issuer: %v", err)
	}
	if _, err := os.Stat(answerPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("envelope written without an issuer: %v", err)
	}
	if err := ledger.Commit(o, nil); !errors.Is(err, ErrNoEnvelope) {
		t.Fatalf("Commit approve without envelope: %v", err)
	}
	if err := ledger.Consumed(o); err != nil {
		t.Fatalf("approval consumed without an envelope: %v", err)
	}

	// Crash after the envelope is written, before the consume mark: the
	// approval is unconsumed and re-verifies on restart.
	if err := writeAtomic(answerPath, []byte("{\"partial\": true}\n")); err != nil {
		t.Fatal(err)
	}
	// A temp file left by a crash mid-write does not block anything.
	_ = os.WriteFile(filepath.Join(filepath.Dir(answerPath), ".tmp-crashed"), []byte("x"), 0o600)
	if err := ledger.Consumed(o); err != nil {
		t.Fatalf("unconsumed after crash: %v", err)
	}
	if rc, out := verify("--issuer-key", f.issuerKey); rc != 0 || !strings.Contains(out, `"result":"approve"`) {
		t.Fatalf("restart after crash: rc %d %s", rc, out)
	}
	raw, _ := os.ReadFile(answerPath)
	if !strings.Contains(string(raw), `"envelope"`) {
		t.Fatal("restart did not rewrite the envelope")
	}
	// Second restart: the answer is consumed now.
	if rc, out := verify("--issuer-key", f.issuerKey); rc != 1 || !strings.Contains(out, "already_consumed") {
		t.Fatalf("replay after consume: rc %d %s", rc, out)
	}
	// A restart with no issuer reports the replay, not a missing issuer.
	if rc, out := verify(); rc != 1 || !strings.Contains(out, "already_consumed") {
		t.Fatalf("replay without issuer: rc %d %s", rc, out)
	}
	// Crash between the gate mark and the event mark: still a replay.
	g := newFixture(t)
	o2, _ := Verify(answer(t, g.owner, testNow.Add(time.Minute), approveText(g.gate)), g.gate, nil, g.pin, testNow.Add(2*time.Minute))
	l2 := LedgerFor(g.root)
	if err := writeOnce(filepath.Join(l2.Dir, "gate-"+g.gate.GateID), []byte("approve\n")); err != nil {
		t.Fatal(err)
	}
	env := &Envelope{Path: filepath.Join(g.root, "answers", "x.json"), Data: []byte("{}\n")}
	if RejectionCode(l2.Consumed(o2)) != RejectReplayed || RejectionCode(l2.Commit(o2, env)) != RejectReplayed {
		t.Fatal("half-marked gate not treated as consumed")
	}
	if _, err := os.Stat(env.Path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("a refused commit wrote its envelope")
	}
	// The verifier refuses a gate id with no stored record (a caller cannot
	// supply one).
	var out bytes.Buffer
	if rc := run([]string{"verify", "--event", eventPath, "--root", f.root, "--gate-id", "g-not-stored", "--owner", f.pin.Owner, "--channel", testChannel, "--require-thread=false"}, &out, &out); rc != 1 || !strings.Contains(out.String(), RejectGateRecord) {
		t.Fatalf("unstored gate: rc %d %s", rc, out.String())
	}
}

// TestConcurrentApproveDeny: an approve and a deny of one gate committed at
// the same moment. Exactly one wins. A losing approve never publishes an
// envelope, and a winning approve always has one. The hook widens the
// window between the replay check and the writes, so the test fails if the
// transaction lock is removed.
func TestConcurrentApproveDeny(t *testing.T) {
	saved := commitHook
	commitHook = func() { time.Sleep(3 * time.Millisecond) }
	t.Cleanup(func() { commitHook = saved })
	wins := map[string]int{}
	ns := Namespace{ProjectDir: "/repo", Profile: "spike", Session: "s1", Generation: "gen-1"}
	answers := filepath.Join("extensions", "amq-squad", "answers")
	for i := 0; i < 25; i++ {
		f := newFixture(t)
		at := testNow.Add(2 * time.Minute)
		// Two distinct owner approves (different event ids) and one deny of
		// the same gate. Each approve has its own real, signed answer file
		// and its own output path.
		type contender struct {
			name string
			o    Outcome
			env  *Envelope
			err  error
		}
		var cs []*contender
		for j, sec := range []int{60, 61} {
			raw := answer(t, f.owner, testNow.Add(time.Duration(sec)*time.Second), approveText(f.gate), f.reply())
			o, err := Verify(raw, f.gate, f.question, f.tpin, at)
			if err != nil {
				t.Fatal(err)
			}
			af, err := BuildAnswerFile(o, raw, f.gate, f.question, f.tpin, "", ns, f.signer, at)
			if err != nil {
				t.Fatal(err)
			}
			blob, _ := json.MarshalIndent(af, "", "  ")
			path := filepath.Join(f.root, answers, fmt.Sprintf("%s.approve-%d.json", f.gate.GateID, j))
			cs = append(cs, &contender{name: fmt.Sprintf("approve-%d", j), o: o, env: &Envelope{Path: path, Data: append(blob, '\n')}})
		}
		dn, err := Verify(answer(t, f.owner, testNow.Add(time.Minute), "deny "+f.gate.GateID+" merge "+prefixOf(f.gate)), f.gate, nil, f.pin, at)
		if err != nil {
			t.Fatal(err)
		}
		cs = append(cs, &contender{name: "deny", o: dn})
		if cs[0].o.Event.ID == cs[1].o.Event.ID {
			t.Fatal("setup: the two approves must be distinct events")
		}

		ledger := LedgerFor(f.root)
		var wg sync.WaitGroup
		start := make(chan struct{})
		for _, c := range cs {
			wg.Add(1)
			go func(c *contender) { defer wg.Done(); <-start; c.err = ledger.Commit(c.o, c.env) }(c)
		}
		close(start)
		wg.Wait()

		var winner *contender
		for _, c := range cs {
			switch {
			case c.err == nil && winner == nil:
				winner = c
			case c.err == nil:
				t.Fatalf("round %d: two winners (%s, %s)", i, winner.name, c.name)
			case RejectionCode(c.err) != RejectReplayed:
				t.Fatalf("round %d: %s: unexpected error %v", i, c.name, c.err)
			}
		}
		if winner == nil {
			t.Fatalf("round %d: no winner", i)
		}
		wins[winner.name]++

		// The gate mark names the winner's decision and event.
		mark, _ := os.ReadFile(filepath.Join(ledger.Dir, "gate-"+f.gate.GateID))
		if !strings.HasPrefix(string(mark), winner.o.Decision+" "+winner.o.Event.ID+" ") {
			t.Fatalf("round %d: gate mark %q does not name winner %s (%s)", i, mark, winner.name, winner.o.Event.ID)
		}
		for _, c := range cs {
			if c.env == nil {
				continue
			}
			got, err := os.ReadFile(c.env.Path)
			if c != winner {
				// A loser leaves no file at its own path.
				if !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("round %d: loser %s left a file (%v)", i, c.name, err)
				}
				continue
			}
			// The winner's bytes are exactly the bytes it wrote.
			if err != nil || !bytes.Equal(got, c.env.Data) {
				t.Fatalf("round %d: winner %s bytes changed (%v)", i, c.name, err)
			}
			// And they re-verify against the stored record, naming the
			// winning event.
			var af AnswerFile
			if err := json.Unmarshal(got, &af); err != nil {
				t.Fatal(err)
			}
			if err := VerifyAnswerFile(af, f.trust, f.tpin, f.trusted(), false); err != nil {
				t.Fatalf("round %d: winner envelope does not re-verify: %v", i, err)
			}
			if af.Envelope.Payload.AnswerMessageID != "nostr:"+winner.o.Event.ID || af.Evidence.EventID != winner.o.Event.ID {
				t.Fatalf("round %d: winner envelope names %s, winner is %s", i, af.Envelope.Payload.AnswerMessageID, winner.o.Event.ID)
			}
		}
		// Nothing else was written into the answers directory.
		ents, _ := os.ReadDir(filepath.Join(f.root, answers))
		want := 0
		if winner.env != nil {
			want = 1
		}
		if len(ents) != want {
			t.Fatalf("round %d: answers dir has %d entries, want %d", i, len(ents), want)
		}
	}
	t.Logf("winners over 25 rounds: %v", wins)
}
