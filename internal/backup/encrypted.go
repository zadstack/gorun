package backup

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"io"

	"github.com/minio/sio"
)

const (
	encryptedMagic           = "GORUN-BACKUP-1\x00"
	streamingMagic           = "GORUN-BACKUP-2\x00"
	contentMarker            = "GORUN-BACKUP-CONTENT-2\x00"
	streamSaltSize           = 32
	maxBufferedEncryptedFile = 64 << 20
	// MaxEncryptedFile is DARE's cryptographic plaintext bound, less our authenticated marker.
	// Uploads use bounded buffers; provider object-size limits still apply.
	MaxEncryptedFile int64 = (1 << 48) - int64(len(contentMarker))
)

var errBackupAuthentication = errors.New("backup: authentication failed; wrong recovery secret, filename or damaged file")

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

func decryptLegacy(r io.Reader, key []byte, name string) ([]byte, error) {
	if !fileName.MatchString(name) {
		return nil, errors.New("backup: invalid encrypted filename")
	}
	aead, err := backupCipher(key)
	if err != nil {
		return nil, err
	}
	data, err := io.ReadAll(io.LimitReader(r, maxBufferedEncryptedFile+1024))
	if err != nil {
		return nil, err
	}
	offset := len(encryptedMagic) + aead.NonceSize()
	if len(data) < offset+aead.Overhead() || len(data) > maxBufferedEncryptedFile+offset+aead.Overhead() || string(data[:len(encryptedMagic)]) != encryptedMagic {
		return nil, errors.New("backup: invalid encrypted file")
	}
	plain, err := aead.Open(nil, data[len(encryptedMagic):offset], data[offset:], []byte(encryptedMagic+name))
	if err != nil {
		return nil, errors.New("backup: authentication failed; wrong recovery secret, filename or damaged file")
	}
	return plain, nil
}

// EncryptTo writes a v2 DARE20 stream with a fresh file key bound to name.
// Memory usage is independent of the input size. Only ciphertext is written.
func EncryptTo(dst io.Writer, src io.Reader, key []byte, name string) (int64, error) {
	if !fileName.MatchString(name) {
		return 0, errors.New("backup: invalid encrypted filename")
	}
	salt := make([]byte, streamSaltSize)
	if _, err := rand.Read(salt); err != nil {
		return 0, err
	}
	cfg, err := streamConfig(key, name, salt)
	if err != nil {
		return 0, err
	}
	header := append([]byte(streamingMagic), salt...)
	n, err := dst.Write(header)
	if err != nil {
		return int64(n), err
	}
	if n != len(header) {
		return int64(n), io.ErrShortWrite
	}
	// DARE authenticates nonempty streams; the marker also authenticates an empty file.
	count, err := sio.Encrypt(dst, io.MultiReader(bytes.NewReader([]byte(contentMarker)), src), cfg)
	return int64(n) + count, err
}

func streamConfig(key []byte, name string, salt []byte) (sio.Config, error) {
	if len(key) != 32 {
		return sio.Config{}, errors.New("backup: a 32-byte recovery secret is required")
	}
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte("gorun.private.backup.v2\x00"))
	mac.Write(salt)
	mac.Write([]byte{0})
	mac.Write([]byte(name))
	return sio.Config{Key: mac.Sum(nil), MinVersion: sio.Version20, MaxVersion: sio.Version20, CipherSuites: []byte{sio.AES_GCM}}, nil
}

// DecryptTo reads v2 streams and legacy v1 files. Only authenticated chunks are
// written, but callers must consume EOF and reject all output if any error occurs.
// v1 decoding retains its original bounded whole-file authentication.
func DecryptTo(dst io.Writer, src io.Reader, key []byte, name string) (int64, error) {
	if !fileName.MatchString(name) {
		return 0, errors.New("backup: invalid encrypted filename")
	}
	header := make([]byte, len(streamingMagic))
	if _, err := io.ReadFull(src, header); err != nil {
		return 0, errBackupAuthentication
	}
	if string(header) == encryptedMagic {
		plain, err := decryptLegacy(io.MultiReader(bytes.NewReader(header), src), key, name)
		if err != nil {
			return 0, err
		}
		return io.Copy(dst, bytes.NewReader(plain))
	}
	if string(header) != streamingMagic {
		return 0, errBackupAuthentication
	}
	salt := make([]byte, streamSaltSize)
	if _, err := io.ReadFull(src, salt); err != nil {
		return 0, errBackupAuthentication
	}
	cfg, err := streamConfig(key, name, salt)
	if err != nil {
		return 0, err
	}
	reader, err := sio.DecryptReader(src, cfg)
	if err != nil {
		return 0, errBackupAuthentication
	}
	marker := make([]byte, len(contentMarker))
	if _, err = io.ReadFull(reader, marker); err != nil || string(marker) != contentMarker {
		return 0, errBackupAuthentication
	}
	count, err := io.Copy(dst, reader)
	var authenticationError sio.Error
	if errors.As(err, &authenticationError) {
		return count, errBackupAuthentication
	}
	return count, err
}

// Encrypt is the bounded in-memory convenience API. Uploads must use EncryptTo.
func Encrypt(src io.Reader, key []byte, name string) ([]byte, error) {
	limited := &io.LimitedReader{R: src, N: maxBufferedEncryptedFile + 1}
	var out bytes.Buffer
	if _, err := EncryptTo(&out, limited, key, name); err != nil {
		return nil, err
	}
	if limited.N == 0 {
		return nil, errors.New("backup: buffered encryption exceeds 64 MiB; use streaming encryption")
	}
	return out.Bytes(), nil
}

// Decrypt is the bounded in-memory convenience API. Large recovery/readback uses DecryptTo.
func Decrypt(src io.Reader, key []byte, name string) ([]byte, error) {
	var out bytes.Buffer
	limited := &boundedPlaintext{Writer: &out, remaining: maxBufferedEncryptedFile}
	if _, err := DecryptTo(limited, src, key, name); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

type boundedPlaintext struct {
	io.Writer
	remaining int
}

func (w *boundedPlaintext) Write(p []byte) (int, error) {
	if len(p) > w.remaining {
		return 0, errors.New("backup: buffered decryption exceeds 64 MiB; use streaming decryption")
	}
	n, err := w.Writer.Write(p)
	w.remaining -= n
	return n, err
}

// EncryptedSize returns the v2 ciphertext size, including the authenticated envelope.
func EncryptedSize(size int64) (int64, error) {
	if size < 0 || size > MaxEncryptedFile {
		return 0, errors.New("backup: file exceeds streaming encryption format bounds")
	}
	sealed, err := sio.EncryptedSize(uint64(size) + uint64(len(contentMarker)))
	if err != nil {
		return 0, err
	}
	return int64(sealed) + int64(len(streamingMagic)+streamSaltSize), nil
}
