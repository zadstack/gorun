package media

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// S3Config is an S3-compatible bucket (AWS S3, Cloudflare R2, MinIO…), addressed path-style.
type S3Config struct {
	Endpoint  string // https://<account>.r2.cloudflarestorage.com, https://s3.<region>.amazonaws.com
	Region    string // "auto" when empty (R2)
	Bucket    string
	AccessKey string
	SecretKey string
	PublicURL string // optional public base of the bucket (a CDN or R2 custom domain); else files are served from /media/
}

// Enabled reports whether a bucket is configured.
func (c S3Config) Enabled() bool { return c.Endpoint != "" || c.Bucket != "" }

var bucketPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]{1,61}[a-z0-9]$`)

// S3Storage stores new files in a bucket. Files stored before it was enabled stay on disk:
// Open falls back to the local driver and Delete removes both copies.
type S3Storage struct {
	endpoint               *url.URL
	region, bucket, access string
	secret, public         string
	local                  *LocalStorage
	client                 *http.Client
	now                    func() time.Time
}

// NewS3Storage validates c and returns the driver. Plain http endpoints are allowed only when
// insecure is set (dev, a local MinIO).
func NewS3Storage(c S3Config, local *LocalStorage, insecure bool) (*S3Storage, error) {
	u, err := url.Parse(strings.TrimRight(c.Endpoint, "/"))
	switch {
	case err != nil || u.Host == "" || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.User != nil:
		return nil, errors.New("media: s3 endpoint must be a bare https://host URL")
	case u.Scheme != "https" && !(insecure && u.Scheme == "http"):
		return nil, errors.New("media: s3 endpoint must use https")
	case !bucketPattern.MatchString(c.Bucket):
		return nil, errors.New("media: s3 bucket name is missing or invalid")
	case c.AccessKey == "" || c.SecretKey == "":
		return nil, errors.New("media: s3 access_key and secret_key are required")
	}
	public := strings.TrimRight(c.PublicURL, "/")
	if public != "" {
		if p, err := url.Parse(public); err != nil || p.Scheme != "https" || p.Host == "" {
			return nil, errors.New("media: s3 public_url must be an https URL")
		}
	}
	region := c.Region
	if region == "" {
		region = "auto"
	}
	u.Path = ""
	return &S3Storage{endpoint: u, region: region, bucket: c.Bucket, access: c.AccessKey, secret: c.SecretKey, public: public,
		local: local, client: &http.Client{Timeout: 60 * time.Second}, now: time.Now}, nil
}

func (s *S3Storage) objectURL(key string) string {
	return s.endpoint.String() + "/" + s.bucket + "/" + key
}

// do sends a signed request for key with body (nil for none).
func (s *S3Storage) do(ctx context.Context, method, key string, body []byte, mime string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, s.objectURL(key), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.ContentLength = int64(len(body))
	if mime != "" {
		req.Header.Set("Content-Type", mime)
	}
	signV4(req, hexSHA256(body), s.access, s.secret, s.region, s.now())
	return s.client.Do(req)
}

// Put uploads r to key.
func (s *S3Storage) Put(ctx context.Context, key string, r io.Reader, mime string) error {
	if !keyPattern.MatchString(key) {
		return ErrInvalidKey
	}
	body, err := io.ReadAll(r)
	if err != nil {
		return fmt.Errorf("media: s3 read: %w", err)
	}
	resp, err := s.do(ctx, http.MethodPut, key, body, mime)
	if err != nil {
		return fmt.Errorf("media: s3 put: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("media: s3 put %s: %s", key, s3Error(resp))
	}
	return nil
}

// Open returns the object at key, or the local file stored before the bucket was enabled.
func (s *S3Storage) Open(ctx context.Context, key string) (io.ReadCloser, error) {
	if !keyPattern.MatchString(key) {
		return nil, ErrInvalidKey
	}
	resp, err := s.do(ctx, http.MethodGet, key, nil, "")
	if err != nil {
		return nil, fmt.Errorf("media: s3 get: %w", err)
	}
	switch {
	case resp.StatusCode == http.StatusOK:
		return resp.Body, nil
	case resp.StatusCode == http.StatusNotFound:
		resp.Body.Close()
		if s.local == nil {
			return nil, fs.ErrNotExist
		}
		return s.local.Open(ctx, key)
	}
	defer resp.Body.Close()
	return nil, fmt.Errorf("media: s3 get %s: %s", key, s3Error(resp))
}

// Delete removes key from the bucket and from disk; a missing object is not an error.
func (s *S3Storage) Delete(ctx context.Context, key string) error {
	if !keyPattern.MatchString(key) {
		return ErrInvalidKey
	}
	resp, err := s.do(ctx, http.MethodDelete, key, nil, "")
	if err != nil {
		return fmt.Errorf("media: s3 delete: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 && resp.StatusCode != http.StatusNotFound {
		return fmt.Errorf("media: s3 delete %s: %s", key, s3Error(resp))
	}
	if s.local == nil {
		return nil
	}
	if err := s.local.Delete(ctx, key); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

// URL is the public URL of key: under PublicURL when set, else served by GoRun from /media/.
// SVG files are always served by GoRun, which adds their sandboxing CSP.
func (s *S3Storage) URL(key string) string {
	if s.public != "" && !strings.HasSuffix(key, ".svg") {
		return s.public + "/" + key
	}
	return URLPrefix + key
}

// Provider response bodies are untrusted and can echo signed request credentials.
func s3Error(resp *http.Response) string { return fmt.Sprintf("HTTP %d", resp.StatusCode) }

// ProbeBucket verifies signed read-only bucket access with the caller's guarded client.
// It does not change the active media driver or upload, list, or delete objects.
func (s *S3Storage) ProbeBucket(ctx context.Context, client *http.Client) error {
	if client == nil {
		return errors.New("media: probe client required")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, s.endpoint.String()+"/"+s.bucket, nil)
	if err != nil {
		return errors.New("media: invalid bucket probe")
	}
	signV4(req, hexSHA256(nil), s.access, s.secret, s.region, s.now())
	resp, err := client.Do(req)
	if err != nil {
		return errors.New("media: bucket could not be reached safely")
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("media: bucket returned HTTP %d", resp.StatusCode)
	}
	return nil
}
