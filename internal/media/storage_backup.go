package media

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
)

var backupObjectPattern = regexp.MustCompile(`^backups/[0-9a-hjkmnp-tv-z]{26}/[0-9]{4}/[0-9]{2}/[0-9]{2}/[0-9]{8}T[0-9]{6}Z\.(db|tar\.gz)\.gorb$`)

// PutEncryptedBackup accepts only private backup keys, outside the media route grammar.
// The caller encrypts and bounds the bytes before upload. ACLs are not used as privacy proof.
func (s *S3Storage) PutEncryptedBackup(ctx context.Context, client *http.Client, key string, body []byte) error {
	if client == nil || !backupObjectPattern.MatchString(key) || len(body) > (64<<20)+128 {
		return ErrInvalidKey
	}
	copyDriver := *s
	copyDriver.client = client
	resp, err := copyDriver.do(ctx, http.MethodPut, key, body, "application/octet-stream")
	if err != nil {
		return fmt.Errorf("backup: object upload could not reach destination safely")
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("backup: object upload returned HTTP %d", resp.StatusCode)
	}
	return nil
}
