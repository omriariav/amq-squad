package main

import (
	"strings"
	"testing"
)

// NIP-19 test vector from the NIP-19 text.
func TestNpubVector(t *testing.T) {
	const npub = "npub10elfcs4fr0l0r8af98jlmgdh9c8tcxjvz9qkw038js35mp4dma8qzvjptg"
	const pub = "7e7e9c42a91bfef19fa929e5fda1b72e0ebc1a4c1141673e2794234d86addf4e"
	got, err := ParseOwner(npub)
	if err != nil || got != pub {
		t.Fatalf("decode: %s %v", got, err)
	}
	enc, err := EncodeNpub(pub)
	if err != nil || enc != npub {
		t.Fatalf("encode: %s %v", enc, err)
	}
	if _, err := ParseOwner(npub[:len(npub)-1] + "q"); err == nil {
		t.Fatal("bad checksum accepted")
	}
	if _, err := ParseOwner(strings.Replace(npub, "npub", "nsec", 1)); err == nil {
		t.Fatal("nsec accepted as an owner")
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

// Ask 11 vector: pubkey (NIP-19 vector key), created_at 1700000000, kind 9,
// tags [["h","s1-vector"]]. Reference ids are sha256 of JSON.stringify of the
// commitment (as nostr-tools computes them); fiatjaf.com/nostr
// v0.0.0-20260916040958-27e395a0f6e7 GetID gives the "library" ids. The
// serializer must match the reference; the parser refuses all three.
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
		if got := hexOf(id[:]); got != c.reference {
			t.Fatalf("%q: id %s, reference %s", c.content, got, c.reference)
		}
		if ambiguousControl(c.content) != (c.reference != c.library) {
			t.Fatalf("%q: refusal rule disagrees with the divergence", c.content)
		}
	}
}

// Events whose id depends on the library's escaping are refused.
func TestAmbiguousControlRefused(t *testing.T) {
	k := mustKey(t)
	for _, c := range []string{"form\ffeed", "back\bspace", "nul\x00", "esc\x1b"} {
		e := Event{CreatedAt: 1700000000, Kind: 9, Tags: [][]string{{"h", "x"}}, Content: c}
		if err := k.Sign(&e); err != nil {
			t.Fatal(err)
		}
		if _, err := ParseEvent(e.Marshal()); err == nil {
			t.Fatalf("%q accepted", c)
		}
	}
	e := Event{CreatedAt: 1700000000, Kind: 9, Tags: [][]string{{"h", "x\x01"}}, Content: "ok"}
	_ = k.Sign(&e)
	if _, err := ParseEvent(e.Marshal()); err == nil {
		t.Fatal("control in a tag accepted")
	}
}

func TestSignVerifyRoundTrip(t *testing.T) {
	k := mustKey(t)
	e := Event{CreatedAt: 1700000000, Kind: 9, Tags: [][]string{{"h", "x"}}, Content: "approve g-abc merge 0123456789abcdef"}
	if err := k.Sign(&e); err != nil {
		t.Fatal(err)
	}
	p, err := ParseEvent(e.Marshal())
	if err != nil {
		t.Fatal(err)
	}
	if !p.CheckID() || p.VerifySignature() != nil {
		t.Fatal("round trip does not verify")
	}
}
