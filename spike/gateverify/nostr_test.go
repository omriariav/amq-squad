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
