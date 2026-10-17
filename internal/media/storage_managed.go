package media

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/zadstack/gorun/internal/httpx"
	"github.com/zadstack/gorun/internal/settings"
)

// ManagedStorage keeps old destinations readable after upload activation changes.
// Only media-purpose destinations and the public media key grammar are accepted.
type ManagedStorage struct {
	db       *sql.DB
	settings *settings.Store
	secret   []byte
	fallback Storage
	client   *http.Client
}

func SafeStorageClient() *http.Client {
	c := httpx.NewSafeClientOpts(httpx.SafeClientOptions{Timeout: 90 * time.Second})
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return c
}
func (s *ManagedStorage) UploadKey(ctx context.Context, key string) (string, error) {
	if !keyPattern.MatchString(key) || strings.HasPrefix(key, "remote/") {
		return "", ErrInvalidKey
	}
	var id string
	err := s.settings.Get(ctx, "storage.active.media", &id)
	if errors.Is(err, settings.ErrNotFound) || err == nil && id == "" {
		return key, nil
	}
	if err != nil {
		return "", err
	}
	d, err := LoadDestination(ctx, s.settings, id)
	if err != nil {
		return "", err
	}
	if d.Purpose != "media" {
		return "", ErrInvalidKey
	}
	if !strings.HasPrefix(key, "sites/") {
		site, err := StorageSiteID(ctx, s.db)
		if err != nil {
			return "", err
		}
		key = "sites/" + site + "/uploads/" + key
	}
	return "remote/" + id + "/" + key, nil
}
func (s *ManagedStorage) driver(ctx context.Context, key string) (Storage, string, error) {
	if !keyPattern.MatchString(key) {
		return nil, "", ErrInvalidKey
	}
	if !strings.HasPrefix(key, "remote/") {
		return s.fallback, key, nil
	}
	parts := strings.SplitN(key, "/", 3)
	var site string
	if err := s.settings.Get(ctx, "storage.site_id", &site); err != nil {
		return nil, "", err
	}
	if !generatedIDPattern.MatchString(site) || !strings.HasPrefix(parts[2], "sites/"+strings.ToLower(site)+"/") {
		return nil, "", ErrInvalidKey
	}
	d, err := LoadDestination(ctx, s.settings, parts[1])
	if err != nil {
		return nil, "", err
	}
	if d.Purpose != "media" {
		return nil, "", ErrInvalidKey
	}
	cfg, err := DestinationConfig(d, s.secret)
	if err != nil {
		return nil, "", err
	}
	driver, err := NewS3Storage(cfg, nil, false)
	if err != nil {
		return nil, "", err
	}
	driver.client = s.client
	if driver.client == nil {
		driver.client = SafeStorageClient()
	}
	return driver, parts[2], nil
}
func (s *ManagedStorage) Put(ctx context.Context, key string, r io.Reader, mime string) error {
	d, k, err := s.driver(ctx, key)
	if err != nil {
		return err
	}
	return d.Put(ctx, k, r, mime)
}
func (s *ManagedStorage) Open(ctx context.Context, key string) (io.ReadCloser, error) {
	d, k, err := s.driver(ctx, key)
	if err != nil {
		return nil, err
	}
	return d.Open(ctx, k)
}
func (s *ManagedStorage) Delete(ctx context.Context, key string) error {
	d, k, err := s.driver(ctx, key)
	if err != nil {
		return err
	}
	return d.Delete(ctx, k)
}
func (s *ManagedStorage) URL(key string) string {
	if strings.HasPrefix(key, "remote/") {
		return URLPrefix + key
	}
	return s.fallback.URL(key)
}
