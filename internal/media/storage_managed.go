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

// ServiceStorage is the kernel service name of the active media storage.
const ServiceStorage = "media.storage"

// StorageInfo describes which backend new uploads go to and whether it is
// fully configured. Configured is local validation only; a passed
// operation test (write/read/delete of a probe object) is the evidence that
// it works.
type StorageInfo struct {
	Kind        string `json:"kind" enum:"local,s3"`
	Source      string `json:"source" doc:"settings:storage.active.media, config:s3 or local"`
	Configured  bool   `json:"configured"`
	Reason      string `json:"reason"`
	Remediation string `json:"remediation"`
}

// ActiveStorage is the storage new uploads use, with key mapping and a
// description, published for capability checks.
type ActiveStorage interface {
	Storage
	UploadKey(ctx context.Context, key string) (string, error)
	Describe(ctx context.Context) StorageInfo
}

// Describe reports the active backend: a managed destination from settings
// when selected, else the config-level S3 bucket, else local disk.
func (s *ManagedStorage) Describe(ctx context.Context) StorageInfo {
	var id string
	err := s.settings.Get(ctx, "storage.active.media", &id)
	if err == nil && id != "" {
		info := StorageInfo{Kind: "s3", Source: "settings:storage.active.media", Configured: true, Reason: "configured_not_verified", Remediation: "Run the storage operation test to verify write, read and delete on the active destination."}
		d, err := LoadDestination(ctx, s.settings, id)
		if err != nil || d.Purpose != "media" {
			info.Configured, info.Reason, info.Remediation = false, "destination_unreadable", "Re-select the media destination under Settings → Storage."
			return info
		}
		if _, err := DestinationConfig(d, s.secret); err != nil {
			info.Configured, info.Reason, info.Remediation = false, "destination_credentials_unreadable", "Re-enter the destination credentials with the current site secret key."
		}
		return info
	}
	if err != nil && !errors.Is(err, settings.ErrNotFound) {
		return StorageInfo{Kind: "local", Source: "settings:storage.active.media", Reason: "settings_read_failed", Remediation: "Check the database; the active storage setting could not be read."}
	}
	if _, ok := s.fallback.(*S3Storage); ok {
		return StorageInfo{Kind: "s3", Source: "config:s3", Configured: true, Reason: "configured_not_verified", Remediation: "Run the storage operation test to verify write, read and delete on the configured bucket."}
	}
	return StorageInfo{Kind: "local", Source: "local", Configured: true, Reason: "local_disk", Remediation: "Files are stored under the data directory; no external configuration is required."}
}
