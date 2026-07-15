package operatorauth

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

const AuthorizationTrustStoreSchemaVersion = 1

type AuthorizationTrustKey struct {
	KeyID     string `json:"key_id"`
	Algorithm string `json:"algorithm"`
	PublicKey string `json:"public_key"`
	Status    string `json:"status"`
}

type AuthorizationTrustStore struct {
	SchemaVersion int                     `json:"schema_version"`
	Keys          []AuthorizationTrustKey `json:"keys"`
	keys          map[string]trustedAuthorizationKey
}

type trustedAuthorizationKey struct {
	public ed25519.PublicKey
	status string
}

func LoadAuthorizationTrustStore(path string) (AuthorizationTrustStore, error) {
	raw, err := secureReadAuthorizationFile(path, false, 1<<20)
	if err != nil {
		return AuthorizationTrustStore{}, fmt.Errorf("load authorization trust store %s: %w", path, err)
	}
	var store AuthorizationTrustStore
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&store); err != nil {
		return AuthorizationTrustStore{}, fmt.Errorf("decode authorization trust store: %w", err)
	}
	var trailing any
	if err := dec.Decode(&trailing); err != io.EOF {
		return AuthorizationTrustStore{}, fmt.Errorf("authorization trust store has trailing JSON")
	}
	if store.SchemaVersion != AuthorizationTrustStoreSchemaVersion || len(store.Keys) == 0 {
		return AuthorizationTrustStore{}, fmt.Errorf("authorization trust store schema or key set is invalid")
	}
	store.keys = make(map[string]trustedAuthorizationKey, len(store.Keys))
	publicOwners := map[string]string{}
	trusted := 0
	for _, item := range store.Keys {
		if item.Algorithm != authorizationSignatureAlgorithm || (item.Status != "trusted" && item.Status != "revoked") {
			return AuthorizationTrustStore{}, fmt.Errorf("authorization trust store key metadata is invalid")
		}
		rawPublic, err := base64.StdEncoding.Strict().DecodeString(item.PublicKey)
		if err != nil || len(rawPublic) != ed25519.PublicKeySize {
			return AuthorizationTrustStore{}, fmt.Errorf("authorization trust store public key is invalid")
		}
		public := ed25519.PublicKey(append([]byte(nil), rawPublic...))
		if KeyIDForEd25519(public) != item.KeyID {
			return AuthorizationTrustStore{}, fmt.Errorf("authorization trust store key id mismatch")
		}
		if _, exists := store.keys[item.KeyID]; exists {
			return AuthorizationTrustStore{}, fmt.Errorf("authorization trust store repeats key id %q", item.KeyID)
		}
		publicText := base64.StdEncoding.EncodeToString(public)
		if owner, exists := publicOwners[publicText]; exists {
			return AuthorizationTrustStore{}, fmt.Errorf("authorization trust store repeats public key for %q and %q", owner, item.KeyID)
		}
		publicOwners[publicText] = item.KeyID
		store.keys[item.KeyID] = trustedAuthorizationKey{public: public, status: item.Status}
		if item.Status == "trusted" {
			trusted++
		}
	}
	if trusted == 0 {
		return AuthorizationTrustStore{}, fmt.Errorf("authorization trust store has no trusted key")
	}
	return store, nil
}

func (s AuthorizationTrustStore) Verify(keyID string, payload, signature []byte) error {
	item, ok := s.keys[keyID]
	if !ok {
		return fmt.Errorf("authorization key %q is not trusted", keyID)
	}
	if item.status == "revoked" {
		return fmt.Errorf("authorization key %q is revoked", keyID)
	}
	if !ed25519.Verify(item.public, payload, signature) {
		return fmt.Errorf("authorization signature is invalid")
	}
	return nil
}
