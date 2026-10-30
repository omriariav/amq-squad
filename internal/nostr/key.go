package nostr

import (
	"encoding/hex"
	"errors"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/btcec/v2/schnorr"
)

// SecretKey is a secp256k1 key the core signs its own events with: the
// share's body key (the gate question, the re-ask, the relay sign-in) or a
// throwaway key in tests. It is never the owner's key: an owner-signed event
// is only ever verified here (R33).
type SecretKey struct{ k *btcec.PrivateKey }

// NewSecretKey mints a random throwaway key.
func NewSecretKey() (SecretKey, error) {
	k, err := btcec.NewPrivateKey()
	if err != nil {
		return SecretKey{}, err
	}
	return SecretKey{k: k}, nil
}

// SecretKeyFromHex parses 64 hex characters.
func SecretKeyFromHex(s string) (SecretKey, error) {
	raw, err := hex.DecodeString(s)
	if err != nil || len(raw) != 32 {
		return SecretKey{}, errors.New("secret key: want 64 hex characters")
	}
	k, _ := btcec.PrivKeyFromBytes(raw)
	if k.Key.IsZero() {
		return SecretKey{}, errors.New("secret key: zero scalar")
	}
	return SecretKey{k: k}, nil
}

// PublicHex is the x-only public key as 64 lowercase hex.
func (s SecretKey) PublicHex() string {
	return hex.EncodeToString(schnorr.SerializePubKey(s.k.PubKey()))
}

// Sign fills pubkey, id and sig.
func (s SecretKey) Sign(e *Event) error {
	if e.Tags == nil {
		e.Tags = [][]string{}
	}
	e.PubKey = s.PublicHex()
	id := e.ComputeID()
	e.ID = hex.EncodeToString(id[:])
	sig, err := schnorr.Sign(s.k, id[:])
	if err != nil {
		return err
	}
	e.Sig = hex.EncodeToString(sig.Serialize())
	return nil
}
