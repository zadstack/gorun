package media

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
)

var backupObjectPattern = regexp.MustCompile(`^backups/[0-9a-hjkmnp-tv-z]{26}/[0-9]{4}/[0-9]{2}/[0-9]{2}/[0-9]{8}T[0-9]{6}Z\.(db|tar\.gz)\.gorb$`)

// PutEncryptedBackup is the in-memory compatibility entry point.
func (s *S3Storage) PutEncryptedBackup(ctx context.Context, client *http.Client, key string, body []byte) error {
	return s.PutEncryptedBackupReader(ctx, client, key, bytes.NewReader(body))
}

// PutEncryptedBackupReader hashes and rewinds private ciphertext for SigV4.
// The seekable source keeps signed payloads bounded in memory and replayable.
func (s *S3Storage) PutEncryptedBackupReader(ctx context.Context, client *http.Client, key string, body io.ReadSeeker) error {
	if client == nil || !backupObjectPattern.MatchString(key) {
		return ErrInvalidKey
	}
	size, err := body.Seek(0, io.SeekEnd)
	if err != nil || size <= 0 {
		return errors.New("backup: cannot size encrypted object")
	}
	if _, err = body.Seek(0, io.SeekStart); err != nil {
		return err
	}
	digest := sha256.New()
	count, err := io.Copy(digest, body)
	if err != nil || count != size {
		return errors.New("backup: cannot hash encrypted object")
	}
	if _, err = body.Seek(0, io.SeekStart); err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPut, s.objectURL(key), io.LimitReader(body, size))
	if err != nil {
		return err
	}
	request.ContentLength = size
	request.Header.Set("Content-Type", "application/octet-stream")
	signV4(request, hex.EncodeToString(digest.Sum(nil)), s.access, s.secret, s.region, s.now())
	response, err := client.Do(request)
	if err != nil {
		return errors.New("backup: object upload could not reach destination safely")
	}
	defer response.Body.Close()
	if response.StatusCode/100 != 2 {
		return fmt.Errorf("backup: object upload returned HTTP %d", response.StatusCode)
	}
	return nil
}

// OpenEncryptedBackup reads only private backup keys with the caller's safe client.
// Callers authenticate the entire encrypted stream and compare its plaintext digest.
func (s *S3Storage) OpenEncryptedBackup(ctx context.Context, client *http.Client, key string) (io.ReadCloser, error) {
	if client == nil || !backupObjectPattern.MatchString(key) {
		return nil, ErrInvalidKey
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, s.objectURL(key), nil)
	if err != nil {
		return nil, err
	}
	signV4(request, hexSHA256(nil), s.access, s.secret, s.region, s.now())
	response, err := client.Do(request)
	if err != nil {
		return nil, errors.New("backup: object readback could not reach destination safely")
	}
	if response.StatusCode != http.StatusOK {
		response.Body.Close()
		return nil, fmt.Errorf("backup: object readback returned HTTP %d", response.StatusCode)
	}
	return response.Body, nil
}
