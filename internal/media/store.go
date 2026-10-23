package media

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/zadstack/gorun/internal/audit"
	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/db"
	"github.com/zadstack/gorun/internal/hooks"
	"github.com/zadstack/gorun/internal/settings"
)

// Hook (event) names emitted after commit; payload is the Media record.
const (
	HookUploaded = "media.uploaded"
	HookDeleted  = "media.deleted"
)

type service struct {
	db       *sql.DB
	storage  Storage
	settings *settings.Store
	hooks    *hooks.Registry
	audit    *audit.Writer
	log      *slog.Logger
	client   *http.Client // SSRF-safe (httpx.NewSafeClient)
	now      func() time.Time
}

const mediaColumns = `id, filename, storage_key, url, mime, size, width, height, alt, caption, title, author_id,
	variants_json, COALESCE(source_url, ''), checksum, created_at, focal_x, focal_y`

func scanMedia(row interface{ Scan(...any) error }) (*Media, error) {
	var m Media
	var variants string
	var fx, fy sql.NullFloat64
	err := row.Scan(&m.ID, &m.Filename, &m.StorageKey, &m.URL, &m.Mime, &m.Size, &m.Width, &m.Height, &m.Alt,
		&m.Caption, &m.Title, &m.AuthorID, &variants, &m.SourceURL, &m.Checksum, &m.CreatedAt, &fx, &fy)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("media: scan: %w", err)
	}
	if fx.Valid && fy.Valid {
		if f := (Focal{X: fx.Float64, Y: fy.Float64}); f.Valid() {
			m.Focal = &f
		}
	}
	m.Variants = map[string]Variant{}
	if err := json.Unmarshal([]byte(variants), &m.Variants); err != nil {
		return nil, fmt.Errorf("media: decode variants: %w", err)
	}
	return &m, nil
}

// Upload stores a file after sniffing, allowlist and size checks
// (media.max_upload_mb); SVG is sanitized, raster images get variants.
func (s *service) Upload(ctx context.Context, filename string, r io.Reader, meta UploadMeta) (*Media, error) {
	return s.store(ctx, filename, r, meta, s.maxUpload(ctx))
}

// FromURL downloads rawURL through the SSRF-safe client (50 MB cap) unless a
// media item with that source URL already exists.
func (s *service) FromURL(ctx context.Context, rawURL string, meta UploadMeta) (*Media, error) {
	u, err := url.Parse(rawURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("media: invalid source URL %q", rawURL)
	}
	if m, err := s.FindBySourceURL(ctx, rawURL); err == nil {
		return m, nil
	} else if !errors.Is(err, ErrNotFound) {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("media: build request: %w", err)
	}
	req.Header.Set("User-Agent", "GoRun media fetcher")
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("media: fetch %s: %w", u.Redacted(), err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("media: fetch %s: status %d", u.Redacted(), resp.StatusCode)
	}
	if resp.ContentLength > fromURLLimit {
		return nil, ErrTooLarge
	}
	name := cleanFilename(u.Path)
	if _, ok := extMimes[extOf(name)]; !ok {
		if ct, _, _ := strings.Cut(resp.Header.Get("Content-Type"), ";"); mimeExts[strings.TrimSpace(ct)] != "" {
			name += "." + mimeExts[strings.TrimSpace(ct)]
		}
	}
	meta.SourceURL = rawURL
	return s.store(ctx, name, resp.Body, meta, fromURLLimit)
}

// storedFile is a file written during store, removed again on failure.
type storedFile struct{ key string }

func (s *service) store(ctx context.Context, filename string, r io.Reader, meta UploadMeta, limit int64) (*Media, error) {
	data, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, fmt.Errorf("media: read upload: %w", err)
	}
	if int64(len(data)) > limit {
		return nil, ErrTooLarge
	}
	if len(data) == 0 {
		return nil, ErrEmpty
	}
	filename = cleanFilename(filename)
	mimeType, ext, err := s.detect(ctx, filename, data)
	if err != nil {
		return nil, err
	}
	if mimeType == "image/svg+xml" {
		if data, err = sanitizeSVG(data); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrNotAllowed, err)
		}
	}
	orientation := 1
	if rasterMimes[mimeType] {
		if clean, o, err := stripMetadata(data, mimeType); err != nil {
			s.log.Warn("media: metadata not stripped", "filename", filename, "err", err)
		} else {
			orientation = o
			if s.stripEnabled(ctx) {
				data = clean
			}
		}
	}
	sum := sha256.Sum256(data)
	now := s.now().UTC()
	id := db.NewID()
	base, err := s.uploadBase(ctx, meta, mimeType, now, id)
	if err != nil {
		return nil, err
	}
	if managed, ok := s.storage.(interface {
		UploadKey(context.Context, string) (string, error)
	}); ok {
		key, err := managed.UploadKey(ctx, base+"."+ext)
		if err != nil {
			return nil, err
		}
		base = strings.TrimSuffix(key, "."+ext)
	}
	m := &Media{ID: id, Filename: filename, StorageKey: base + "." + ext, Mime: mimeType, Size: int64(len(data)),
		Alt: meta.Alt, Caption: meta.Caption, Title: meta.Title, AuthorID: meta.AuthorID, SourceURL: meta.SourceURL,
		Checksum: hex.EncodeToString(sum[:]), CreatedAt: db.FormatTime(now), Variants: map[string]Variant{}}
	if m.AuthorID == "" {
		if u := auth.UserFromContext(ctx); u != nil {
			m.AuthorID = u.ID
		}
	}
	if m.Title == "" {
		m.Title = strings.TrimSuffix(filename, path.Ext(filename))
	}
	m.URL = s.storage.URL(m.StorageKey)

	var written []storedFile
	cleanup := func() {
		for _, f := range written {
			if err := s.storage.Delete(context.WithoutCancel(ctx), f.key); err != nil {
				s.log.Error("media: cleanup", "key", f.key, "err", err)
			}
		}
	}
	if err := s.storage.Put(ctx, m.StorageKey, bytes.NewReader(data), mimeType); err != nil {
		return nil, err
	}
	written = append(written, storedFile{m.StorageKey})
	if rasterMimes[mimeType] {
		if err := s.addVariants(ctx, m, data, orientation, base, &written); err != nil {
			cleanup()
			return nil, err
		}
	}
	if err := s.insert(ctx, m); err != nil {
		cleanup()
		if isUniqueSourceViolation(err) && m.SourceURL != "" {
			return s.FindBySourceURL(ctx, m.SourceURL) // lost a concurrent FromURL race
		}
		return nil, err
	}
	s.audit.Record(ctx, "media.upload", "media", m.ID, map[string]any{"filename": m.Filename, "mime": m.Mime,
		"size": m.Size, "source_url": m.SourceURL})
	s.hooks.Emit(ctx, HookUploaded, m)
	return m, nil
}

// addVariants records the dimensions and stores the variants of a raster
// image. Undecodable or oversized images are kept without variants.
func (s *service) addVariants(ctx context.Context, m *Media, data []byte, orientation int, base string, written *[]storedFile) error {
	cfg, err := imageConfig(data)
	if err != nil {
		s.log.Warn("media: unreadable image header", "filename", m.Filename, "err", err)
		return nil
	}
	m.Width, m.Height = cfg.Width, cfg.Height
	if orientation >= 5 { // displayed rotated by 90°
		m.Width, m.Height = cfg.Height, cfg.Width
	}
	if cfg.Width*cfg.Height > maxPixels || cfg.Width <= 0 || cfg.Height <= 0 {
		return nil
	}
	variants, err := makeVariants(data, m.Mime, orientation)
	if err != nil {
		s.log.Warn("media: variants skipped", "filename", m.Filename, "err", err)
		return nil
	}
	for _, v := range variants {
		key := base + "-" + v.Name + "." + v.Ext
		if err := s.storage.Put(ctx, key, bytes.NewReader(v.Data), v.Mime); err != nil {
			return err
		}
		*written = append(*written, storedFile{key})
		m.Variants[v.Name] = Variant{URL: s.storage.URL(key), Width: v.Width, Height: v.Height}
	}
	return nil
}

func isUniqueSourceViolation(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE") && strings.Contains(err.Error(), "source_url")
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func (s *service) insert(ctx context.Context, m *Media) error {
	variants, err := json.Marshal(m.Variants)
	if err != nil {
		return fmt.Errorf("media: encode variants: %w", err)
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO media (id, filename, storage_key, url, mime, size, width, height,
		alt, caption, title, author_id, variants_json, source_url, checksum, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		m.ID, m.Filename, m.StorageKey, m.URL, m.Mime, m.Size, m.Width, m.Height, m.Alt, m.Caption, m.Title,
		m.AuthorID, string(variants), nullable(m.SourceURL), m.Checksum, m.CreatedAt)
	if err != nil {
		return fmt.Errorf("media: insert: %w", err)
	}
	return nil
}

// Get returns one media item or ErrNotFound.
func (s *service) Get(ctx context.Context, id string) (*Media, error) {
	return scanMedia(s.db.QueryRowContext(ctx, `SELECT `+mediaColumns+` FROM media WHERE id = ?`, id))
}

// FindBySourceURL returns the item imported from url or ErrNotFound.
func (s *service) FindBySourceURL(ctx context.Context, url string) (*Media, error) {
	if url == "" {
		return nil, ErrNotFound
	}
	if strings.Contains(url, RevisionMarker) { // an exact revision
		return scanMedia(s.db.QueryRowContext(ctx, `SELECT `+mediaColumns+` FROM media WHERE source_url = ?`, url))
	}
	// The plain URL or any revision of it; the newest is the current file
	// (newest upload, or the revision last passed to MarkCurrent).
	// The revision branch is a range on the unique source_url index: every
	// "<url>#gorun-rev-…" sorts between lo and hi (binary collation; hi is
	// the marker with its last byte incremented).
	lo := url + RevisionMarker
	hi := url + RevisionMarker[:len(RevisionMarker)-1] + string(RevisionMarker[len(RevisionMarker)-1]+1)
	return scanMedia(s.db.QueryRowContext(ctx, `SELECT `+mediaColumns+` FROM media
		WHERE source_url = ? OR (source_url >= ? AND source_url < ?)
		ORDER BY CASE WHEN current_at != '' THEN current_at ELSE id END DESC LIMIT 1`, url, lo, hi))
}

// MarkCurrent makes media id the current revision of its source URL again
// (an importer re-used an older revision whose bytes came back): plain-URL
// lookups return it until a newer revision is uploaded or marked.
func (s *service) MarkCurrent(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `UPDATE media SET current_at = ? WHERE id = ?`, db.NewID(), id)
	if err != nil {
		return fmt.Errorf("media: mark current: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	s.audit.Record(ctx, "media.mark_current", "media", id, nil)
	return nil
}

// RevisionMarker separates a source URL from a revision suffix. When a
// re-imported file's bytes changed, the new media row keeps the source URL
// plus RevisionMarker and a content hash (source_url is unique), so
// FindBySourceURL and FromURL of the plain URL return the newest revision.
const RevisionMarker = "#gorun-rev-"

var sortColumns = map[string]string{"created_at": "created_at", "filename": "filename", "size": "size", "title": "title"}

// List returns a page of media (newest first by default) and the total.
func (s *service) List(ctx context.Context, q ListQuery) ([]Media, int, error) {
	page, perPage := normalizePage(q.Page, q.PerPage)
	where, args := "", []any{}
	clauses := []string{}
	if term := strings.TrimSpace(q.Search); term != "" {
		like := "%" + escapeLike(term) + "%"
		clauses = append(clauses, `(filename LIKE ? ESCAPE '\' OR title LIKE ? ESCAPE '\' OR alt LIKE ? ESCAPE '\')`)
		args = append(args, like, like, like)
	}
	if q.MimePrefix != "" {
		clauses = append(clauses, `mime LIKE ? ESCAPE '\'`)
		args = append(args, escapeLike(q.MimePrefix)+"%")
	}
	if q.MinSize > 0 {
		clauses = append(clauses, `size>=?`)
		args = append(args, q.MinSize)
	}
	if q.MaxSize > 0 {
		clauses = append(clauses, `size<=?`)
		args = append(args, q.MaxSize)
	}
	if len(clauses) > 0 {
		where = " WHERE " + strings.Join(clauses, " AND ")
	}
	var total int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM media`+where, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("media: count: %w", err)
	}
	col, ok := sortColumns[q.Sort]
	if !ok {
		col = "created_at"
	}
	dir := "DESC"
	if strings.EqualFold(q.Order, "asc") {
		dir = "ASC"
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+mediaColumns+` FROM media`+where+
		` ORDER BY `+col+` `+dir+`, id `+dir+` LIMIT ? OFFSET ?`, append(args, perPage, (page-1)*perPage)...)
	if err != nil {
		return nil, 0, fmt.Errorf("media: list: %w", err)
	}
	defer rows.Close()
	out := []Media{}
	for rows.Next() {
		m, err := scanMedia(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, *m)
	}
	return out, total, rows.Err()
}

// Update applies the non-nil fields of patch.
func (s *service) Update(ctx context.Context, id string, p Patch) (*Media, error) {
	m, err := s.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	changed := map[string]string{}
	set := func(dst *string, v *string, name string) {
		if v == nil {
			return
		}
		if nv := strings.TrimSpace(*v); nv != *dst {
			*dst = nv
			changed[name] = nv
		}
	}
	set(&m.Alt, p.Alt, "alt")
	set(&m.Caption, p.Caption, "caption")
	set(&m.Title, p.Title, "title")
	switch {
	case p.Focal != nil && !p.Focal.Valid():
		return nil, ErrFocal
	case p.Focal != nil && (m.Focal == nil || *m.Focal != *p.Focal):
		f := *p.Focal
		m.Focal = &f
		changed["focal"] = strconv.FormatFloat(f.X, 'f', 3, 64) + "," + strconv.FormatFloat(f.Y, 'f', 3, 64)
	case p.Focal == nil && p.ClearFocal && m.Focal != nil:
		m.Focal = nil
		changed["focal"] = ""
	}
	if len(changed) == 0 {
		return m, nil
	}
	var fx, fy any
	if m.Focal != nil {
		fx, fy = m.Focal.X, m.Focal.Y
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE media SET alt = ?, caption = ?, title = ?, focal_x = ?, focal_y = ? WHERE id = ?`,
		m.Alt, m.Caption, m.Title, fx, fy, id); err != nil {
		return nil, fmt.Errorf("media: update: %w", err)
	}
	s.audit.Record(ctx, "media.update", "media", id, changed)
	return m, nil
}

// Delete removes the row, then the stored original and variants.
func (s *service) Delete(ctx context.Context, id string) error {
	m, err := s.Get(ctx, id)
	if err != nil {
		return err
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM media WHERE id = ?`, id); err != nil {
		return fmt.Errorf("media: delete: %w", err)
	}
	keys := []string{m.StorageKey}
	for _, v := range m.Variants {
		keys = append(keys, path.Join(path.Dir(m.StorageKey), path.Base(v.URL)))
	}
	for _, k := range keys {
		if err := s.storage.Delete(ctx, k); err != nil {
			s.log.Error("media: delete file", "key", k, "err", err)
		}
	}
	s.audit.Record(ctx, "media.delete", "media", id, map[string]any{"filename": m.Filename})
	s.hooks.Emit(ctx, HookDeleted, m)
	return nil
}

// URL returns the URL of the named variant, or the original's URL when the
// variant does not exist (or variant is "" / "full").
func (s *service) URL(m *Media, variant string) string {
	if m == nil {
		return ""
	}
	if v, ok := m.Variants[variant]; ok && v.URL != "" {
		return v.URL
	}
	return m.URL
}

func normalizePage(page, perPage int) (int, int) {
	if page < 1 {
		page = 1
	}
	if perPage < 1 {
		perPage = 20
	}
	return page, min(perPage, 100)
}

func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}
