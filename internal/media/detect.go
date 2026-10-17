package media

import (
	"context"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"path"
	"slices"
	"strings"
)

// Settings keys (defaults below apply when unset).
const (
	SettingMaxUploadMB   = "media.max_upload_mb"
	SettingAllowedMimes  = "media.allowed_mimes"
	defaultMaxUploadMB   = 25
	fromURLLimit         = 50 << 20
	maxFilenameRunes     = 200
	defaultFetchFilename = "download"
)

// DefaultAllowedMimes is the allowlist used when media.allowed_mimes is unset.
var DefaultAllowedMimes = []string{"image/jpeg", "image/png", "image/gif", "image/webp", "image/svg+xml",
	"application/pdf", "application/zip", "video/mp4", "audio/mpeg"}

// Upload errors.
var (
	ErrNotFound   = errors.New("media: not found")
	ErrTooLarge   = errors.New("media: file too large")
	ErrEmpty      = errors.New("media: empty file")
	ErrNotAllowed = errors.New("media: file type not allowed")
)

// extMimes maps a lower-cased extension to the MIME type it must sniff as.
var extMimes = map[string]string{
	"jpg": "image/jpeg", "jpeg": "image/jpeg", "png": "image/png", "gif": "image/gif", "webp": "image/webp",
	"svg": "image/svg+xml", "pdf": "application/pdf", "zip": "application/zip", "mp4": "video/mp4",
	"mp3": "audio/mpeg",
}

// mimeExts is the canonical extension stored for each MIME type.
var mimeExts = map[string]string{
	"image/jpeg": "jpg", "image/png": "png", "image/gif": "gif", "image/webp": "webp", "image/svg+xml": "svg",
	"application/pdf": "pdf", "application/zip": "zip", "video/mp4": "mp4", "audio/mpeg": "mp3",
}

// contentTypeForKey returns the Content-Type to serve a stored key with.
func contentTypeForKey(key string) string {
	ext := strings.TrimPrefix(path.Ext(key), ".")
	if m, ok := extMimes[ext]; ok {
		if m == "image/svg+xml" {
			return m + "; charset=utf-8"
		}
		return m
	}
	return "application/octet-stream"
}

func extOf(filename string) string {
	return strings.ToLower(strings.TrimPrefix(path.Ext(filename), "."))
}

// sniff returns the MIME type of data by content (http.DetectContentType,
// parameters stripped) with two refinements: SVG (which sniffs as text/xml,
// text/plain, or text/html when it starts with a comment) when the document
// is well-formed XML whose root element is svg, and MP3 frames without an
// ID3 tag (which sniff as application/octet-stream).
func sniff(data []byte) string {
	mt, _, err := mime.ParseMediaType(http.DetectContentType(data))
	if err != nil {
		return "application/octet-stream"
	}
	switch mt {
	case "text/xml", "text/plain", "application/xml", "text/html": // "<!-- … --><svg>" sniffs as HTML
		if isSVG(data) {
			return "image/svg+xml"
		}
	case "application/octet-stream":
		if len(data) > 2 && data[0] == 0xFF && data[1]&0xE0 == 0xE0 {
			return "audio/mpeg"
		}
	}
	return mt
}

// detect validates an upload: the sniffed type must match the extension's
// type and be in the allowlist. It returns the MIME type and the canonical
// extension to store under.
func (s *service) detect(ctx context.Context, filename string, data []byte) (string, string, error) {
	sniffed := sniff(data)
	if want, ok := extMimes[extOf(filename)]; !ok || want != sniffed {
		return "", "", fmt.Errorf("%w: %s content with .%s extension", ErrNotAllowed, sniffed, extOf(filename))
	}
	if !slices.Contains(s.allowedMimes(ctx), sniffed) {
		return "", "", fmt.Errorf("%w: %s", ErrNotAllowed, sniffed)
	}
	return sniffed, mimeExts[sniffed], nil
}

func (s *service) allowedMimes(ctx context.Context) []string {
	var v []string
	if err := s.settings.Get(ctx, SettingAllowedMimes, &v); err != nil || len(v) == 0 {
		return DefaultAllowedMimes
	}
	return v
}

// maxUpload is the media.max_upload_mb setting in bytes.
func (s *service) maxUpload(ctx context.Context) int64 {
	var mb int64
	if err := s.settings.Get(ctx, SettingMaxUploadMB, &mb); err != nil || mb <= 0 {
		mb = defaultMaxUploadMB
	}
	return mb << 20
}

// cleanFilename keeps the base name, trimmed to a sane length.
func cleanFilename(name string) string {
	name = path.Base(strings.ReplaceAll(name, `\`, "/"))
	name = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, name)
	if name == "." || name == "/" || name == "" {
		return defaultFetchFilename
	}
	if r := []rune(name); len(r) > maxFilenameRunes {
		ext := path.Ext(name)
		name = string(r[:maxFilenameRunes-len([]rune(ext))]) + ext
	}
	return name
}
