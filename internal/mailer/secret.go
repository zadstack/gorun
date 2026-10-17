package mailer

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
)

// encPrefix marks a value encrypted by EncryptSecret (AES-256-GCM, random
// nonce, base64url of nonce||ciphertext).
const encPrefix = "enc:v1:"

// EncryptSecret encrypts plain with key (32 bytes, cfg.SecretKey).
// TODO(consolidate): migrate to db.Encrypt with fallback read (db.Encrypt
// is AAD-bound std base64 without the enc:v1: prefix; DecryptSecret must
// keep reading existing enc:v1: values until they are re-saved).
func EncryptSecret(key []byte, plain string) (string, error) {
	gcm, err := newGCM(key)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("mailer: nonce: %w", err)
	}
	sealed := gcm.Seal(nonce, nonce, []byte(plain), nil)
	return encPrefix + base64.RawURLEncoding.EncodeToString(sealed), nil
}

// DecryptSecret reverses EncryptSecret.
func DecryptSecret(key []byte, enc string) (string, error) {
	if !strings.HasPrefix(enc, encPrefix) {
		return "", errors.New("mailer: secret is not encrypted")
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(enc, encPrefix))
	if err != nil {
		return "", fmt.Errorf("mailer: decode secret: %w", err)
	}
	gcm, err := newGCM(key)
	if err != nil {
		return "", err
	}
	if len(raw) < gcm.NonceSize() {
		return "", errors.New("mailer: secret too short")
	}
	plain, err := gcm.Open(nil, raw[:gcm.NonceSize()], raw[gcm.NonceSize():], nil)
	if err != nil {
		return "", fmt.Errorf("mailer: decrypt secret: %w", err)
	}
	return string(plain), nil
}

func newGCM(key []byte) (cipher.AEAD, error) {
	if len(key) != 32 {
		return nil, fmt.Errorf("mailer: secret key must be 32 bytes, got %d", len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("mailer: cipher: %w", err)
	}
	return cipher.NewGCM(block)
}
