package nostr

// NIP-19 npub encoding and decoding (BIP-173 bech32, 32-byte payload), ported
// from spike S1. Only the public npub form exists here.

import (
	"encoding/hex"
	"errors"
	"strings"
)

const bech32Charset = "qpzry9x8gf2tvdw0s3jn54khce6mua7l"

var bech32Gen = [5]uint32{0x3b6a57b2, 0x26508e6d, 0x1ea119fa, 0x3d4233dd, 0x2a1462b3}

func bech32Polymod(values []byte) uint32 {
	chk := uint32(1)
	for _, v := range values {
		top := chk >> 25
		chk = (chk&0x1ffffff)<<5 ^ uint32(v)
		for i := 0; i < 5; i++ {
			if (top>>i)&1 == 1 {
				chk ^= bech32Gen[i]
			}
		}
	}
	return chk
}

func bech32HRPExpand(hrp string) []byte {
	out := make([]byte, 0, len(hrp)*2+1)
	for i := 0; i < len(hrp); i++ {
		out = append(out, hrp[i]>>5)
	}
	out = append(out, 0)
	for i := 0; i < len(hrp); i++ {
		out = append(out, hrp[i]&31)
	}
	return out
}

func convertBits(data []byte, from, to uint, pad bool) ([]byte, error) {
	acc, bits := uint32(0), uint(0)
	maxv := uint32(1)<<to - 1
	var out []byte
	for _, v := range data {
		if uint32(v)>>from != 0 {
			return nil, errors.New("bech32: invalid data value")
		}
		acc = acc<<from | uint32(v)
		bits += from
		for bits >= to {
			bits -= to
			out = append(out, byte(acc>>bits&maxv))
		}
	}
	if pad {
		if bits > 0 {
			out = append(out, byte(acc<<(to-bits)&maxv))
		}
	} else if bits >= from || acc<<(to-bits)&maxv != 0 {
		return nil, errors.New("bech32: invalid padding")
	}
	return out, nil
}

// EncodeNpub renders a 64-hex x-only pubkey as npub1...
func EncodeNpub(pubHex string) (string, error) {
	raw, err := hex.DecodeString(pubHex)
	if err != nil || len(raw) != 32 {
		return "", errors.New("npub: want a 64-hex pubkey")
	}
	data, _ := convertBits(raw, 8, 5, true)
	values := append(bech32HRPExpand("npub"), data...)
	pm := bech32Polymod(append(values, 0, 0, 0, 0, 0, 0)) ^ 1
	var b strings.Builder
	b.WriteString("npub1")
	for _, d := range data {
		b.WriteByte(bech32Charset[d])
	}
	for i := 0; i < 6; i++ {
		b.WriteByte(bech32Charset[(pm>>(5*(5-i)))&31])
	}
	return b.String(), nil
}

// ParsePubKey accepts npub1... or 64 lowercase hex and returns 64 hex.
func ParsePubKey(s string) (string, error) {
	s = strings.TrimSpace(s)
	if hex64.MatchString(s) {
		return s, nil
	}
	if strings.ToLower(s) != s {
		return "", errors.New("owner: npub must be lowercase")
	}
	pos := strings.LastIndexByte(s, '1')
	if pos < 1 || s[:pos] != "npub" || len(s)-pos-1 < 6 {
		return "", errors.New("owner: want npub1... or 64 lowercase hex")
	}
	var data []byte
	for i := pos + 1; i < len(s); i++ {
		d := strings.IndexByte(bech32Charset, s[i])
		if d < 0 {
			return "", errors.New("owner: invalid bech32 character")
		}
		data = append(data, byte(d))
	}
	if bech32Polymod(append(bech32HRPExpand("npub"), data...)) != 1 {
		return "", errors.New("owner: npub checksum does not verify")
	}
	raw, err := convertBits(data[:len(data)-6], 5, 8, false)
	if err != nil || len(raw) != 32 {
		return "", errors.New("owner: npub payload is not 32 bytes")
	}
	return hex.EncodeToString(raw), nil
}
