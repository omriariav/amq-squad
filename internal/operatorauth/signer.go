package operatorauth

import (
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"strings"
)

const authorizationSignatureAlgorithm = "ed25519"

type AuthorizationSigner interface {
	KeyID() string
	Sign([]byte) ([]byte, error)
	Destroy()
}

type fileEd25519Signer struct {
	keyID string
	key   ed25519.PrivateKey
}

func KeyIDForEd25519(public ed25519.PublicKey) string {
	sum := sha256.Sum256(public)
	return "ed25519-sha256:" + hex.EncodeToString(sum[:])
}

func LoadEd25519Signer(path string) (AuthorizationSigner, error) {
	raw, err := secureReadAuthorizationFile(path, true, 64<<10)
	if err != nil {
		return nil, fmt.Errorf("load authorization signing key %s: %w", path, err)
	}
	defer zeroBytes(raw)
	block, rest := pem.Decode(raw)
	if block == nil || block.Type != "PRIVATE KEY" || len(block.Headers) != 0 || strings.TrimSpace(string(rest)) != "" {
		return nil, fmt.Errorf("load authorization signing key %s: expected exactly one unencrypted PKCS#8 PRIVATE KEY PEM block", path)
	}
	defer zeroBytes(block.Bytes)
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("load authorization signing key %s: invalid PKCS#8 key", path)
	}
	key, ok := parsed.(ed25519.PrivateKey)
	if !ok || len(key) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("load authorization signing key %s: key is not Ed25519", path)
	}
	owned := append(ed25519.PrivateKey(nil), key...)
	public := owned.Public().(ed25519.PublicKey)
	return &fileEd25519Signer{keyID: KeyIDForEd25519(public), key: owned}, nil
}

func (s *fileEd25519Signer) KeyID() string { return s.keyID }

func (s *fileEd25519Signer) Sign(payload []byte) ([]byte, error) {
	if s == nil || len(s.key) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("authorization signer is unavailable")
	}
	return ed25519.Sign(s.key, payload), nil
}

func (s *fileEd25519Signer) Destroy() {
	if s == nil {
		return
	}
	zeroBytes(s.key)
	s.key = nil
}

func zeroBytes(data []byte) {
	for i := range data {
		data[i] = 0
	}
}
