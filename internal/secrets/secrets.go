// Package secrets encrypts small blobs (tenant credentials) at rest with AES-256-GCM.
package secrets

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
)

type Box struct{ aead cipher.AEAD }

// New builds a Box from a 32-byte key given as 64 hex chars or base64.
func New(key string) (*Box, error) {
	var raw []byte
	if b, err := hex.DecodeString(key); err == nil && len(b) == 32 {
		raw = b
	} else if b, err := base64.StdEncoding.DecodeString(key); err == nil && len(b) == 32 {
		raw = b
	} else {
		return nil, errors.New("SECRETS_KEY must be 32 bytes, as 64 hex characters or base64 (e.g. `openssl rand -hex 32`)")
	}
	return fromRaw(raw)
}

// FromPassphrase derives a key from arbitrary text. Development only.
func FromPassphrase(p string) *Box {
	sum := sha256.Sum256([]byte("aisp-dev-secrets:" + p))
	b, _ := fromRaw(sum[:])
	return b
}

func fromRaw(raw []byte) (*Box, error) {
	blk, err := aes.NewCipher(raw)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(blk)
	if err != nil {
		return nil, err
	}
	return &Box{aead: aead}, nil
}

func (b *Box) Seal(plain []byte) (string, error) {
	nonce := make([]byte, b.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	return "v1:" + base64.StdEncoding.EncodeToString(b.aead.Seal(nonce, nonce, plain, nil)), nil
}

func (b *Box) Open(s string) ([]byte, error) {
	if len(s) < 3 || s[:3] != "v1:" {
		return nil, errors.New("unrecognised secret format")
	}
	raw, err := base64.StdEncoding.DecodeString(s[3:])
	if err != nil || len(raw) < b.aead.NonceSize() {
		return nil, errors.New("corrupt secret")
	}
	n := b.aead.NonceSize()
	plain, err := b.aead.Open(nil, raw[:n], raw[n:], nil)
	if err != nil {
		return nil, fmt.Errorf("decrypt failed (wrong SECRETS_KEY?)")
	}
	return plain, nil
}
