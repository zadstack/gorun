package backup

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"io"
)

const MaxEncryptedFile = 64 << 20
const encryptedMagic = "GORUN-BACKUP-1\x00"

func backupCipher(key []byte) (cipher.AEAD, error) {
	if len(key) != 32 {
		return nil, errors.New("backup: a 32-byte recovery secret is required")
	}
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte("gorun.private.backup.v1"))
	block, err := aes.NewCipher(mac.Sum(nil))
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// Encrypt authenticates both the file bytes and original backup filename.
// Keep the site's secret.key/GORUN_SECRET_KEY independently to recover these files.
func Encrypt(r io.Reader, key []byte, name string) ([]byte, error) {
	if !fileName.MatchString(name) {
		return nil, errors.New("backup: invalid encrypted filename")
	}
	aead, err := backupCipher(key)
	if err != nil {
		return nil, err
	}
	plain, err := io.ReadAll(io.LimitReader(r, MaxEncryptedFile+1))
	if err != nil {
		return nil, err
	}
	if len(plain) > MaxEncryptedFile {
		return nil, errors.New("backup: encrypted upload limit is 64 MiB per file; local backup retained")
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err = rand.Read(nonce); err != nil {
		return nil, err
	}
	out := append([]byte(encryptedMagic), nonce...)
	return aead.Seal(out, nonce, plain, []byte(encryptedMagic+name)), nil
}
func Decrypt(r io.Reader, key []byte, name string) ([]byte, error) {
	if !fileName.MatchString(name) {
		return nil, errors.New("backup: invalid encrypted filename")
	}
	aead, err := backupCipher(key)
	if err != nil {
		return nil, err
	}
	data, err := io.ReadAll(io.LimitReader(r, MaxEncryptedFile+1024))
	if err != nil {
		return nil, err
	}
	offset := len(encryptedMagic) + aead.NonceSize()
	if len(data) < offset+aead.Overhead() || len(data) > MaxEncryptedFile+offset+aead.Overhead() || string(data[:len(encryptedMagic)]) != encryptedMagic {
		return nil, errors.New("backup: invalid encrypted file")
	}
	plain, err := aead.Open(nil, data[len(encryptedMagic):offset], data[offset:], []byte(encryptedMagic+name))
	if err != nil {
		return nil, errors.New("backup: authentication failed; wrong recovery secret, filename or damaged file")
	}
	return plain, nil
}
