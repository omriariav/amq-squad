package nostr

import (
	"encoding/hex"
	"math/big"
	"strings"
	"testing"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/btcec/v2/schnorr"
)

func mustKey(t *testing.T) SecretKey {
	t.Helper()
	k, err := NewSecretKey()
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func signed(t *testing.T, k SecretKey, content string) Event {
	t.Helper()
	e := Event{CreatedAt: 1700000000, Kind: 9, Tags: [][]string{{"h", "chan"}, {"e", strings.Repeat("ab", 32), "", "root"}}, Content: content}
	if err := k.Sign(&e); err != nil {
		t.Fatal(err)
	}
	return e
}

// NIP-19 test vector from the NIP-19 text.
func TestNpubVector(t *testing.T) {
	const npub = "npub10elfcs4fr0l0r8af98jlmgdh9c8tcxjvz9qkw038js35mp4dma8qzvjptg"
	const pub = "7e7e9c42a91bfef19fa929e5fda1b72e0ebc1a4c1141673e2794234d86addf4e"
	got, err := ParsePubKey(npub)
	if err != nil || got != pub {
		t.Fatalf("decode: %s %v", got, err)
	}
	enc, err := EncodeNpub(pub)
	if err != nil || enc != npub {
		t.Fatalf("encode: %s %v", enc, err)
	}
	if _, err := ParsePubKey(npub[:len(npub)-1] + "q"); err == nil {
		t.Fatal("bad checksum accepted")
	}
	if _, err := ParsePubKey(strings.Replace(npub, "npub", "nse"+"c", 1)); err == nil {
		t.Fatal("a secret-key bech32 form accepted as a public key")
	}
}

func TestSerializationEscapes(t *testing.T) {
	e := Event{PubKey: strings.Repeat("a", 64), CreatedAt: 1, Kind: 9, Tags: [][]string{{"h", "c"}},
		Content: "q\" b\\ n\n r\r t\t bs\b ff\f nul\x00 lt< amp& ls  é"}
	got := string(SerializeForID(e))
	want := `[0,"` + strings.Repeat("a", 64) + `",1,9,[["h","c"]],"q\" b\\ n\n r\r t\t bs\b ff\f nul\u0000 lt< amp& ls` + " " + ` é"]`
	if got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
}

// Ask 11 vector (S1 report 2.3): reference ids are sha256 of JSON.stringify
// of the commitment; the parser refuses every content whose id differs
// between libraries.
func TestAsk11Vector(t *testing.T) {
	cases := []struct{ content, reference, library string }{
		{"a\fb", "c1ce746eee1ef178e988713471e2a1062bed99175770cbb2f29cf7382807278c", "d559be4e02658fe077bc203d34aa73356a5e54854a2a8696cd3c760f01b55174"},
		{"a\bb", "a7d8977f8f6a1240d1bc7542ffc9514b8988d1cb7f8418d091c72f013cd7fe22", "60d419d2c4af0f1eaeaf30cd6bb6f2d4628c391bcf0f8efcb12f43bad7e967e3"},
		{"a\x01b", "37847e11712fa7d0d1197c382c92b79106f736520ac3ccc9ad367a85105db0dd", "6dde4a54306107d2ae43334cb097cbc4cbeab54a0e5dd167af5cb23cff7d462e"},
		{"a\nb", "20123c9e3cb553383e7040d2375ce679933681b53c432a3538f837e4609317c7", "20123c9e3cb553383e7040d2375ce679933681b53c432a3538f837e4609317c7"},
	}
	for _, c := range cases {
		e := Event{PubKey: "7e7e9c42a91bfef19fa929e5fda1b72e0ebc1a4c1141673e2794234d86addf4e", CreatedAt: 1700000000, Kind: 9, Tags: [][]string{{"h", "s1-vector"}}, Content: c.content}
		id := e.ComputeID()
		if got := hex.EncodeToString(id[:]); got != c.reference {
			t.Fatalf("%q: id %s, reference %s", c.content, got, c.reference)
		}
		if ambiguousControl(c.content) != (c.reference != c.library) {
			t.Fatalf("%q: refusal rule disagrees with the divergence", c.content)
		}
	}
}

func TestAmbiguousControlRefused(t *testing.T) {
	k := mustKey(t)
	for _, c := range []string{"form\ffeed", "back\bspace", "nul\x00", "esc\x1b"} {
		if _, err := ParseEvent(signed(t, k, c).Marshal()); err == nil {
			t.Fatalf("%q accepted", c)
		}
	}
	e := Event{CreatedAt: 1700000000, Kind: 9, Tags: [][]string{{"h", "x\x01"}}, Content: "ok"}
	_ = k.Sign(&e)
	if _, err := ParseEvent(e.Marshal()); err == nil {
		t.Fatal("control in a tag accepted")
	}
}

func TestSignVerifyRoundTripAndThreadRefs(t *testing.T) {
	k := mustKey(t)
	e := signed(t, k, "approve g1 release 0123456789abcdef")
	p, err := ParseEvent(e.Marshal())
	if err != nil {
		t.Fatal(err)
	}
	if !p.CheckID() || p.VerifySignature() != nil {
		t.Fatal("round trip does not verify")
	}
	if refs := ThreadRefs(p); len(refs) != 1 || refs[0] != strings.Repeat("ab", 32) {
		t.Fatalf("thread refs = %v", refs)
	}
	if h := TagValues(p, "h"); len(h) != 1 || h[0] != "chan" {
		t.Fatalf("h = %v", h)
	}
	other := mustKey(t)
	forged := p
	forged.PubKey = other.PublicHex()
	if forged.VerifySignature() == nil {
		t.Fatal("a signature verified under another key")
	}
}

// Plan 7.3 conformance: strict decoding refuses invalid UTF-8, duplicate
// keys and null tag elements (and the rest of S1's 28 wire forms).
func TestStrictDecoding(t *testing.T) {
	k := mustKey(t)
	e := signed(t, k, "approve g1 release 0123456789abcdef")
	good := string(e.Marshal())
	rep := func(old, new string) string {
		if !strings.Contains(good, old) {
			t.Fatalf("fixture lacks %q", old)
		}
		return strings.Replace(good, old, new, 1)
	}
	ca := big.NewInt(e.CreatedAt).String()
	cases := map[string]string{
		"duplicate key (content twice)": rep(`,"sig":`, `,"content":"deny g1 release 0123456789abcdef","sig":`),
		"duplicate key (id twice)":      rep(`"pubkey":`, `"id":"`+strings.Repeat("0", 64)+`","pubkey":`),
		"null tag element":              rep(`["h","chan"]`, `["h",null]`),
		"number tag element":            rep(`["h","chan"]`, `["h",7]`),
		"tag is not an array":           rep(`[["h","chan"],`, `["h",`),
		"tags is null":                  rep(`"tags":[["h","chan"],["e","`+strings.Repeat("ab", 32)+`","","root"]]`, `"tags":null`),
		"content is null":               rep(`"content":"approve g1 release 0123456789abcdef"`, `"content":null`),
		"invalid UTF-8 byte":            rep(`"content":"approve`, "\"content\":\"\xffapprove"),
		"overlong UTF-8":                rep(`"content":"approve`, "\"content\":\"\xc0\xafapprove"),
		"unpaired high surrogate":       rep(`"content":"approve`, `"content":"\ud83dapprove`),
		"unpaired low surrogate":        rep(`"content":"approve`, `"content":"\ude42approve`),
		"reversed surrogate pair":       rep(`"content":"approve`, `"content":"\ude42\ud83dapprove`),
		"escaped C0 control":            rep(`"content":"approve`, `"content":"\u0001approve`),
		"escaped form feed in a tag":    rep(`"h","chan`, `"h","\fchan`),
		"created_at fraction":           rep(`"created_at":`+ca, `"created_at":`+ca+`.0`),
		"created_at exponent":           rep(`"created_at":`+ca, `"created_at":1.79e9`),
		"created_at negative":           rep(`"created_at":`+ca, `"created_at":-1`),
		"created_at leading zero":       rep(`"created_at":`+ca, `"created_at":0`+ca),
		"created_at string":             rep(`"created_at":`+ca, `"created_at":"`+ca+`"`),
		"kind out of range":             rep(`"kind":9`, `"kind":70000`),
		"unknown field":                 rep(`{"id"`, `{"x":1,"id"`),
		"missing field":                 rep(`,"kind":9`, ``),
		"trailing data":                 good + `{}`,
		"two objects":                   good + good,
		"not an object":                 `[` + good + `]`,
		"uppercase hex id":              rep(`"id":"`+e.ID, `"id":"`+strings.ToUpper(e.ID)),
		"truncated":                     good[:len(good)-2],
		"oversized":                     rep(`"content":"approve`, `"content":"`+strings.Repeat(" ", maxEventBytes)+`approve`),
	}
	for name, raw := range cases {
		if _, err := ParseEvent([]byte(raw)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	// a valid escaped surrogate pair and a é escape decode faithfully
	emoji := signed(t, k, "ok \U0001F642 é")
	const bs = "\\"
	escaped := strings.Replace(string(emoji.Marshal()), string(rune(0x1F642)), bs+"ud83d"+bs+"ude42", 1)
	escaped = strings.Replace(escaped, string(rune(0xE9)), bs+"u00e9", 1)
	p, err := ParseEvent([]byte(escaped))
	if err != nil || !p.CheckID() || p.VerifySignature() != nil || p.Content != emoji.Content {
		t.Fatalf("escaped valid event refused: %v", err)
	}
}

// Plan 7.3 conformance: an explicit s < n (and r < p) check before the
// library call; btcec v2.5.0 alone reduces s mod n.
func TestSignatureScalarBounds(t *testing.T) {
	k := mustKey(t)
	e := signed(t, k, "approve g1 release 0123456789abcdef")
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
	libraryVerify = func(s *schnorr.Signature, h []byte, pk *btcec.PublicKey) bool { calls++; return saved(s, h, pk) }
	t.Cleanup(func() { libraryVerify = saved })
	for _, c := range cases {
		bad := e
		bad.Sig = hex.EncodeToString(append(append([]byte(nil), c.r...), c.s...))
		before := calls
		if err := bad.VerifySignature(); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v, want %q", c.name, err, c.want)
		}
		if calls != before {
			t.Errorf("%s: library verify called before the range check", c.name)
		}
	}
	if err := CheckSigScalars(append(append([]byte(nil), sig[:32]...), pad(new(big.Int).Sub(n, big.NewInt(1)))...)); err != nil {
		t.Fatalf("s == n-1 refused: %v", err)
	}
	if err := e.VerifySignature(); err != nil || calls == 0 {
		t.Fatalf("good signature: %v (library calls %d)", err, calls)
	}
}
