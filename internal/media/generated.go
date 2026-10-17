package media

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/zadstack/gorun/internal/db"
)

var generatedIDPattern = regexp.MustCompile(`^[0-9A-HJKMNP-TV-Z]{26}$`)

// StorageSiteID survives host/domain changes and separates sites sharing a bucket.
func StorageSiteID(ctx context.Context, database *sql.DB) (string, error) {
	id := db.NewID()
	raw, _ := json.Marshal(id)
	if _, err := database.ExecContext(ctx, `INSERT INTO settings(key,value_json,updated_at) VALUES('storage.site_id',?,?) ON CONFLICT(key) DO NOTHING`, string(raw), db.Now()); err != nil {
		return "", err
	}
	var value string
	if err := database.QueryRowContext(ctx, `SELECT value_json FROM settings WHERE key='storage.site_id'`).Scan(&value); err != nil {
		return "", err
	}
	if err := json.Unmarshal([]byte(value), &id); err != nil || !generatedIDPattern.MatchString(id) {
		return "", errors.New("media: invalid storage site identity")
	}
	return strings.ToLower(id), nil
}

func (s *service) uploadBase(ctx context.Context, meta UploadMeta, mime string, now time.Time, id string) (string, error) {
	if meta.GeneratedContentID == "" && meta.GeneratedKind == "" {
		return fmt.Sprintf("%04d/%02d/%s", now.Year(), int(now.Month()), strings.ToLower(id)), nil
	}
	if !generatedIDPattern.MatchString(meta.GeneratedContentID) || (meta.GeneratedKind != "image" && meta.GeneratedKind != "video") || !strings.HasPrefix(mime, meta.GeneratedKind+"/") {
		return "", errors.New("media: invalid generated content metadata")
	}
	var exists int
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM content WHERE id=? AND status!='trash'`, meta.GeneratedContentID).Scan(&exists); err != nil {
		return "", fmt.Errorf("media: generated content lookup: %w", err)
	}
	if exists != 1 {
		return "", errors.New("media: generated content not found")
	}
	site, err := StorageSiteID(ctx, s.db)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("sites/%s/content/%s/%04d/%02d/%02d/%s", site, strings.ToLower(meta.GeneratedContentID), now.Year(), int(now.Month()), now.Day(), strings.ToLower(id)), nil
}
