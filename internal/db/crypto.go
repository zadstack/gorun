package db

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
)

const (
	cryptoKeySize = 32 // AES-256
	gcmNonceSize  = 12
	gcmTagSize    = 16
)

// ErrDecrypt is returned when a ciphertext cannot be opened: wrong key,
// wrong AAD, tampering or truncation. Callers cannot tell these apart, by
// design.
var ErrDecrypt = errors.New("db: decrypt failed")

func newGCM(key []byte) (cipher.AEAD, error) {
	if len(key) != cryptoKeySize {
		return nil, fmt.Errorf("db: encryption key must be 32 bytes, got %d", len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("db: cipher: %w", err)
	}
	return cipher.NewGCM(block)
}

// Encrypt seals plaintext with AES-256-GCM under a 32-byte key and a fresh
// random 12-byte nonce. aad binds the ciphertext to its context (e.g.
// "settings:mail.password") so it cannot be replayed into another field.
// The result is standard base64 (with padding) of nonce || ciphertext || tag.
func Encrypt(key, plaintext []byte, aad string) (string, error) {
	gcm, err := newGCM(key)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcmNonceSize, gcmNonceSize+len(plaintext)+gcmTagSize)
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("db: nonce: %w", err)
	}
	sealed := gcm.Seal(nonce, nonce, plaintext, []byte(aad))
	return base64.StdEncoding.EncodeToString(sealed), nil
}

// Decrypt opens a value produced by Encrypt with the same key and aad. Any
// mismatch or corruption yields ErrDecrypt (wrapped); a bad key length
// yields a key error.
func Decrypt(key []byte, ciphertext string, aad string) ([]byte, error) {
	gcm, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	raw, err := base64.StdEncoding.DecodeString(ciphertext)
	if err != nil {
		return nil, fmt.Errorf("%w: invalid base64", ErrDecrypt)
	}
	if len(raw) < gcmNonceSize+gcmTagSize {
		return nil, fmt.Errorf("%w: ciphertext too short", ErrDecrypt)
	}
	plain, err := gcm.Open(nil, raw[:gcmNonceSize], raw[gcmNonceSize:], []byte(aad))
	if err != nil {
		return nil, ErrDecrypt
	}
	if plain == nil {
		plain = []byte{}
	}
	return plain, nil
}
