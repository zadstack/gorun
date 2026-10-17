package wpmigrate

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/zadstack/gorun/internal/db"
)

// SourceInput creates a REST source. AppPassword is encrypted at rest and
// never returned.
type SourceInput struct {
	Name        string `json:"name"`
	URL         string `json:"url"`
	Username    string `json:"username"`
	AppPassword string `json:"app_password"`
}

// SourceView is a source as shown to users (no secret).
type SourceView struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	Kind            string `json:"kind"`
	URL             string `json:"url"`
	Username        string `json:"username"`
	HasPassword     bool   `json:"has_password"`
	CreatedAt       string `json:"created_at"`
	LastDiscoveryAt string `json:"last_discovery_at"`
}

type sourceRow struct {
	SourceView
	passwordEnc string
	filePath    string
}

// MigrationView is a migration summary.
type MigrationView struct {
	ID         string  `json:"id"`
	SourceID   string  `json:"source_id"`
	SourceName string  `json:"source_name"`
	SourceURL  string  `json:"source_url"`
	SourceKind string  `json:"source_kind"`
	Status     string  `json:"status"`
	DryRun     bool    `json:"dry_run"`
	Step       string  `json:"step"`
	Failed     int     `json:"failed"` // failed items
	Options    Options `json:"options"`
	CreatedAt  string  `json:"created_at"`
	StartedAt  string  `json:"started_at"`
	FinishedAt string  `json:"finished_at"`
}

type migrationRow struct {
	ID, SourceID, Status  string
	Options               Options
	Mapping               Mapping
	Discovery             *Report
	Stats                 map[string]json.RawMessage
	DryRun                bool
	FetchedThrough        int
	Step                  string
	Phase, Run, Cursor    int
	CreatedBy             string
	StartedAt, FinishedAt string
	CreatedAt, UpdatedAt  string
}

func (s *service) CreateSource(ctx context.Context, in SourceInput) (*SourceView, error) {
	u, err := parseSiteURL(in.URL)
	if err != nil {
		return nil, err
	}
	in.Username, in.AppPassword = strings.TrimSpace(in.Username), strings.TrimSpace(in.AppPassword)
	if (in.Username == "") != (in.AppPassword == "") {
		return nil, fmt.Errorf("%w: username and application password go together", ErrInvalidSource)
	}
	id := db.NewID()
	enc := ""
	if in.AppPassword != "" {
		if enc, err = db.Encrypt(s.cfg.SecretKey, []byte(in.AppPassword), "wp_source:"+id); err != nil {
			return nil, fmt.Errorf("wpmigrate: encrypt app password: %w", err)
		}
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		name = u.Host
	}
	now := db.Now()
	if _, err := s.db.ExecContext(ctx, `INSERT INTO wp_sources (id, name, kind, url, username, app_password_enc, created_at)
		VALUES (?, ?, 'rest', ?, ?, ?, ?)`, id, name, u.String(), in.Username, enc, now); err != nil {
		return nil, fmt.Errorf("wpmigrate: create source: %w", err)
	}
	s.audit.Record(ctx, "wpmigrate.source_create", "wp_source", id, map[string]any{"url": u.String(), "auth": in.Username != ""})
	return s.Source(ctx, id)
}

// createWXRSource returns the WXR source for the export at path, reusing
// one when it is the same import again (see wxrSourceFor, byName false).
func (s *service) createWXRSource(ctx context.Context, name, path string) (*SourceView, error) {
	src, _, err := s.wxrSourceFor(ctx, name, path, false)
	return src, err
}

// wxrSourceFor finds the WXR source an export at path belongs to, so
// re-importing updates what the earlier imports created (every prior-item
// match is scoped by source) instead of duplicating the site:
//   - a source already registered for the same file path;
//   - with byName (an explicit --source-name), a WXR source of that name;
//   - a WXR source of the same WordPress site (the export's base site URL),
//     e.g. a newer export or the same file uploaded again in the wizard.
//
// A reused source points at path from then on. Otherwise a new source is
// created. reused reports which happened.
func (s *service) wxrSourceFor(ctx context.Context, name, path string, byName bool) (src *SourceView, reused bool, err error) {
	site := ""
	if f, err := s.wxr.get(path); err == nil {
		site = wxrSiteKey(firstNonEmpty(f.BaseBlogURL, f.Link, f.BaseSiteURL))
	}
	var id string
	q := func(where string, args ...any) bool {
		if id != "" {
			return true
		}
		err := s.db.QueryRowContext(ctx, `SELECT id FROM wp_sources WHERE kind = 'wxr' AND `+where+` ORDER BY created_at DESC, id DESC LIMIT 1`, args...).Scan(&id)
		return err == nil
	}
	_ = q("file_path = ?", path) || (byName && name != "" && q("name = ?", name)) || (site != "" && q("url = ?", site))
	if id != "" {
		old, err := s.getSource(ctx, id)
		if err != nil {
			return nil, false, err
		}
		if old.filePath != path || (site != "" && old.URL != site) {
			if _, err := s.db.ExecContext(ctx, `UPDATE wp_sources SET file_path = ?, url = CASE WHEN ? = '' THEN url ELSE ? END WHERE id = ?`,
				path, site, site, id); err != nil {
				return nil, false, fmt.Errorf("wpmigrate: update wxr source: %w", err)
			}
			s.wxr.evict(old.filePath)
			s.audit.Record(ctx, "wpmigrate.source_update", "wp_source", id, map[string]any{"kind": "wxr", "file": baseName(path)})
		}
		v, err := s.Source(ctx, id)
		return v, true, err
	}
	id = db.NewID()
	if _, err := s.db.ExecContext(ctx, `INSERT INTO wp_sources (id, name, kind, url, file_path, created_at) VALUES (?, ?, 'wxr', ?, ?, ?)`,
		id, name, site, path, db.Now()); err != nil {
		return nil, false, fmt.Errorf("wpmigrate: create wxr source: %w", err)
	}
	s.audit.Record(ctx, "wpmigrate.source_create", "wp_source", id, map[string]any{"kind": "wxr", "name": name})
	v, err := s.Source(ctx, id)
	return v, false, err
}

// wxrSiteKey normalises a WXR site URL into the key that identifies a
// WordPress site: the blog URL (wp:base_blog_url, then the channel link;
// wp:base_site_url is the network root on multisite, so subsites would
// collide) with the host lower-cased, the scheme ignored (an http and an
// https export are the same site) and no trailing slash. "" when unusable.
func wxrSiteKey(raw string) string {
	u, err := parseSiteURL(raw)
	if err != nil {
		return ""
	}
	return "https://" + strings.ToLower(u.Host) + strings.TrimRight(u.EscapedPath(), "/")
}

// sourceDocumentURL is the URL a source's own content is relative to. A WXR
// source stores wxrSiteKey (scheme normalised for matching), so its export's
// own blog URL keeps the real scheme for resolving relative references.
func (s *service) sourceDocumentURL(src *sourceRow) string {
	if src.Kind == "wxr" && src.filePath != "" {
		if f, err := s.wxr.get(src.filePath); err == nil {
			if site := firstNonEmpty(f.BaseBlogURL, f.Link, f.BaseSiteURL); site != "" {
				return site
			}
		}
	}
	return src.URL
}

// setOptions replaces a migration's stored options.
func (s *service) setOptions(ctx context.Context, id string, opts Options) error {
	raw, err := json.Marshal(opts.normalized())
	if err != nil {
		return err
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE migrations SET options_json = ?, updated_at = ? WHERE id = ?`, string(raw), db.Now(), id); err != nil {
		return fmt.Errorf("wpmigrate: set options: %w", err)
	}
	s.audit.Record(ctx, "wpmigrate.options_reset", "migration", id, map[string]any{"options": opts.normalized()})
	return nil
}

// latestMigration returns the newest migration of source id.
func (s *service) latestMigration(ctx context.Context, sourceID string) (string, bool) {
	var id string
	err := s.db.QueryRowContext(ctx, `SELECT id FROM migrations WHERE source_id = ? ORDER BY created_at DESC, id DESC LIMIT 1`, sourceID).Scan(&id)
	return id, err == nil
}

const sourceColumns = `id, name, kind, url, username, app_password_enc, file_path, created_at, COALESCE(last_discovery_at, '')`

func scanSource(row interface{ Scan(...any) error }) (*sourceRow, error) {
	var r sourceRow
	if err := row.Scan(&r.ID, &r.Name, &r.Kind, &r.URL, &r.Username, &r.passwordEnc, &r.filePath, &r.CreatedAt, &r.LastDiscoveryAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	r.HasPassword = r.passwordEnc != ""
	return &r, nil
}

func (s *service) getSource(ctx context.Context, id string) (*sourceRow, error) {
	return scanSource(s.db.QueryRowContext(ctx, `SELECT `+sourceColumns+` FROM wp_sources WHERE id = ?`, id))
}

// Source returns one source.
func (s *service) Source(ctx context.Context, id string) (*SourceView, error) {
	r, err := s.getSource(ctx, id)
	if err != nil {
		return nil, err
	}
	return &r.SourceView, nil
}

// Sources lists sources, newest first.
func (s *service) Sources(ctx context.Context) ([]SourceView, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+sourceColumns+` FROM wp_sources ORDER BY created_at DESC, id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SourceView{}
	for rows.Next() {
		r, err := scanSource(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r.SourceView)
	}
	return out, rows.Err()
}

func (s *service) password(r *sourceRow) (string, error) {
	if r.passwordEnc == "" {
		return "", nil
	}
	b, err := db.Decrypt(s.cfg.SecretKey, r.passwordEnc, "wp_source:"+r.ID)
	if err != nil {
		return "", fmt.Errorf("wpmigrate: decrypt app password: %w", err)
	}
	return string(b), nil
}

// CreateMigration starts a draft migration of a source.
func (s *service) CreateMigration(ctx context.Context, sourceID string, opts Options, createdBy string) (*MigrationView, error) {
	if _, err := s.getSource(ctx, sourceID); err != nil {
		return nil, err
	}
	id, now := db.NewID(), db.Now()
	o, err := json.Marshal(opts.normalized())
	if err != nil {
		return nil, fmt.Errorf("wpmigrate: encode options: %w", err)
	}
	// A source scanned before gets its last good report right away.
	status, disc := StatusDraft, "{}"
	var last string
	_ = s.db.QueryRowContext(ctx, `SELECT COALESCE(last_discovery_json, '') FROM wp_sources WHERE id = ?`, sourceID).Scan(&last)
	var rep Report
	if last != "" && json.Unmarshal([]byte(last), &rep) == nil && rep.Auth.OK {
		status, disc = StatusScanned, last
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO migrations (id, source_id, status, options_json, discovery_json, created_by, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, id, sourceID, status, string(o), disc, createdBy, now, now); err != nil {
		return nil, fmt.Errorf("wpmigrate: create migration: %w", err)
	}
	s.audit.Record(ctx, "wpmigrate.create", "migration", id, map[string]any{"source_id": sourceID})
	return s.Migration(ctx, id)
}

const migrationColumns = `m.id, m.source_id, m.status, m.options_json, m.mapping_json, m.discovery_json, m.stats_json, m.dry_run,
	m.current_step, m.current_phase, m.run, m.cursor, m.fetched_through, m.created_by, COALESCE(m.started_at, ''), COALESCE(m.finished_at, ''),
	m.created_at, m.updated_at`

func scanMigration(row interface{ Scan(...any) error }) (*migrationRow, error) {
	var m migrationRow
	var opts, mapping, disc, stats string
	if err := row.Scan(&m.ID, &m.SourceID, &m.Status, &opts, &mapping, &disc, &stats, &m.DryRun, &m.Step, &m.Phase, &m.Run, &m.Cursor,
		&m.FetchedThrough, &m.CreatedBy, &m.StartedAt, &m.FinishedAt, &m.CreatedAt, &m.UpdatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	_ = json.Unmarshal([]byte(opts), &m.Options)
	m.Options = m.Options.normalized()
	_ = json.Unmarshal([]byte(mapping), &m.Mapping)
	if disc != "" && disc != "{}" {
		m.Discovery = &Report{}
		_ = json.Unmarshal([]byte(disc), m.Discovery)
	}
	m.Stats = map[string]json.RawMessage{}
	_ = json.Unmarshal([]byte(stats), &m.Stats)
	return &m, nil
}

func (s *service) getMigration(ctx context.Context, id string) (*migrationRow, error) {
	return scanMigration(s.db.QueryRowContext(ctx, `SELECT `+migrationColumns+` FROM migrations m WHERE m.id = ?`, id))
}

// Migration returns one migration summary.
func (s *service) Migration(ctx context.Context, id string) (*MigrationView, error) {
	list, err := s.listMigrations(ctx, `WHERE m.id = ?`, id)
	if err != nil {
		return nil, err
	}
	if len(list) == 0 {
		return nil, ErrNotFound
	}
	return &list[0], nil
}

// Migrations lists migrations, newest first.
func (s *service) Migrations(ctx context.Context) ([]MigrationView, error) {
	return s.listMigrations(ctx, `ORDER BY m.created_at DESC, m.id DESC`)
}

func (s *service) listMigrations(ctx context.Context, tail string, args ...any) ([]MigrationView, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT m.id, m.source_id, w.name, w.url, w.kind, m.status, m.dry_run, m.current_step,
		(SELECT count(*) FROM migration_items i WHERE i.migration_id = m.id AND i.status = 'failed'), m.options_json, m.created_at, COALESCE(m.started_at, ''), COALESCE(m.finished_at, '')
		FROM migrations m JOIN wp_sources w ON w.id = m.source_id `+tail, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []MigrationView{}
	for rows.Next() {
		var v MigrationView
		var opts string
		if err := rows.Scan(&v.ID, &v.SourceID, &v.SourceName, &v.SourceURL, &v.SourceKind, &v.Status, &v.DryRun, &v.Step,
			&v.Failed, &opts, &v.CreatedAt, &v.StartedAt, &v.FinishedAt); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(opts), &v.Options)
		v.Options = v.Options.normalized()
		out = append(out, v)
	}
	return out, rows.Err()
}

// setStats merges key into migrations.stats_json.
func (s *service) setStats(ctx context.Context, id, key string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `UPDATE migrations SET stats_json = json_set(stats_json, '$.'||?, json(?)), updated_at = ? WHERE id = ?`,
		key, string(b), db.Now(), id)
	return err
}

// itemRow is a migration_items row.
type itemRow struct {
	ID, MigrationID, Kind, WPType, WPID, WPSlug, WPURL string
	LocalKind, LocalID, LocalURL, Status, Error        string
	Checksum                                           string
	Phase, Attempts                                    int
	Payload                                            json.RawMessage
	Would, Meta                                        map[string]any
}

const itemColumns = `id, migration_id, kind, wp_type, wp_id, wp_slug, wp_url, local_kind, local_id, local_url, status, error,
	checksum, phase, attempts, payload_json, COALESCE(would_json, ''), COALESCE(meta_json, '')`

func scanItem(row interface{ Scan(...any) error }) (*itemRow, error) {
	var it itemRow
	var payload, would, meta string
	if err := row.Scan(&it.ID, &it.MigrationID, &it.Kind, &it.WPType, &it.WPID, &it.WPSlug, &it.WPURL, &it.LocalKind, &it.LocalID,
		&it.LocalURL, &it.Status, &it.Error, &it.Checksum, &it.Phase, &it.Attempts, &payload, &would, &meta); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	it.Payload = json.RawMessage(payload)
	if would != "" {
		_ = json.Unmarshal([]byte(would), &it.Would)
	}
	if meta != "" {
		_ = json.Unmarshal([]byte(meta), &it.Meta)
	}
	return &it, nil
}

func (s *service) getItem(ctx context.Context, id string) (*itemRow, error) {
	return scanItem(s.db.QueryRowContext(ctx, `SELECT `+itemColumns+` FROM migration_items WHERE id = ?`, id))
}

// items returns a migration's items of kind (all kinds when ""), in insert
// order.
func (s *service) items(ctx context.Context, mid, kind string, statuses ...string) ([]itemRow, error) {
	q := `SELECT ` + itemColumns + ` FROM migration_items WHERE migration_id = ?`
	args := []any{mid}
	if kind != "" {
		q += ` AND kind = ?`
		args = append(args, kind)
	}
	if len(statuses) > 0 {
		q += ` AND status IN (` + strings.TrimSuffix(strings.Repeat("?,", len(statuses)), ",") + `)`
		for _, st := range statuses {
			args = append(args, st)
		}
	}
	rows, err := s.db.QueryContext(ctx, q+` ORDER BY phase, rowid`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []itemRow
	for rows.Next() {
		it, err := scanItem(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *it)
	}
	return out, rows.Err()
}

// upsertItem stores an item fetched from the source. A new item, or one
// whose checksum changed, becomes pending; an unchanged done/skipped item
// is left alone; preview and failed items become pending again. In a dry
// run done/skipped items are never touched (preview rows exist only for
// items not imported yet). It returns whether the item needs processing.
func (s *service) upsertItem(ctx context.Context, mid string, it itemRow, dry bool) (bool, error) {
	payload := string(it.Payload)
	if payload == "" {
		payload = "{}"
	}
	now := db.Now()
	var id, status, checksum string
	err := s.db.QueryRowContext(ctx, `SELECT id, status, checksum FROM migration_items WHERE migration_id = ? AND kind = ? AND wp_type = ? AND wp_id = ?`,
		mid, it.Kind, it.WPType, it.WPID).Scan(&id, &status, &checksum)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		_, err = s.db.ExecContext(ctx, `INSERT INTO migration_items (id, migration_id, kind, wp_type, wp_id, wp_slug, wp_url, status,
			phase, checksum, payload_json, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, 'pending', ?, ?, ?, ?)`,
			db.NewID(), mid, it.Kind, it.WPType, it.WPID, it.WPSlug, it.WPURL, it.Phase, it.Checksum, payload, now)
		return true, err
	case err != nil:
		return false, err
	}
	if dry && (status == ItemDone || status == ItemSkipped) {
		return false, nil // a preview never touches items already imported
	}
	if checksum == it.Checksum && (status == ItemDone || status == ItemSkipped) {
		_, err = s.db.ExecContext(ctx, `UPDATE migration_items SET phase = ?, payload_json = ?, updated_at = ? WHERE id = ?`,
			it.Phase, payload, now, id)
		return false, err
	}
	_, err = s.db.ExecContext(ctx, `UPDATE migration_items SET status = 'pending', error = '', wp_slug = ?, wp_url = ?, phase = ?,
		checksum = ?, payload_json = ?, updated_at = ? WHERE id = ?`, it.WPSlug, it.WPURL, it.Phase, it.Checksum, payload, now, id)
	return true, err
}

// finishItem records an item's outcome.
func (s *service) finishItem(ctx context.Context, it *itemRow) error {
	var would, meta any
	if it.Would != nil {
		b, _ := json.Marshal(it.Would)
		would = string(b)
	}
	if it.Meta != nil {
		b, _ := json.Marshal(it.Meta)
		meta = string(b)
	}
	_, err := s.db.ExecContext(ctx, `UPDATE migration_items SET status = ?, local_kind = ?, local_id = ?, local_url = ?, error = ?,
		attempts = attempts + 1, would_json = ?, meta_json = COALESCE(?, meta_json), updated_at = ? WHERE id = ?`,
		it.Status, it.LocalKind, it.LocalID, it.LocalURL, it.Error, would, meta, db.Now(), it.ID)
	return err
}

// mapped returns the local id and url an earlier item (this migration —
// including an item being re-imported — or a previous migration of the
// same source) produced for a WordPress object.
func (s *service) mapped(ctx context.Context, m *migrationRow, kind, wpType string, wpID int) (localID, localURL string, ok bool) {
	if wpID == 0 {
		return "", "", false
	}
	q := `SELECT i.local_id, i.local_url FROM migration_items i JOIN migrations g ON g.id = i.migration_id
		WHERE i.kind = ? AND i.wp_id = ? AND i.local_id != '' AND (i.status = 'done' OR i.migration_id = ?) AND g.source_id = ?`
	args := []any{kind, strconv.Itoa(wpID), m.ID, m.SourceID}
	if wpType != "" {
		q += ` AND i.wp_type = ?`
		args = append(args, wpType)
	}
	q += ` ORDER BY (i.migration_id = ?) DESC, i.updated_at DESC LIMIT 1`
	args = append(args, m.ID)
	if err := s.db.QueryRowContext(ctx, q, args...).Scan(&localID, &localURL); err != nil {
		return "", "", false
	}
	return localID, localURL, true
}

// logEntry appends to migration_log (best effort).
func (s *service) logEntry(ctx context.Context, mid, level, itemID, msg string, meta map[string]any) {
	var m any
	if meta != nil {
		b, _ := json.Marshal(meta)
		m = string(b)
	}
	var item any
	if itemID != "" {
		item = itemID
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO migration_log (id, migration_id, at, level, item_id, message, meta_json)
		VALUES (?, ?, ?, ?, ?, ?, ?)`, db.NewID(), mid, db.Now(), level, item, msg, m); err != nil {
		s.log.Error("wpmigrate: log", "err", err)
	}
}

// Log returns the newest log entries (oldest first within the window).
func (s *service) Log(ctx context.Context, migrationID string, limit int) ([]LogEntry, error) {
	if _, err := s.getMigration(ctx, migrationID); err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id, at, level, COALESCE(item_id, ''), message, COALESCE(meta_json, '')
		FROM migration_log WHERE migration_id = ? ORDER BY at DESC, rowid DESC LIMIT ?`, migrationID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []LogEntry{}
	for rows.Next() {
		var e LogEntry
		var meta string
		if err := rows.Scan(&e.ID, &e.At, &e.Level, &e.ItemID, &e.Message, &meta); err != nil {
			return nil, err
		}
		if meta != "" {
			_ = json.Unmarshal([]byte(meta), &e.Meta)
		}
		if e.Message == "validate_report" {
			delete(e.Meta, "markdown")
			if e.Meta == nil {
				e.Meta = map[string]any{}
			}
			e.Meta["current_report_path"] = "/api/v1/migrations/" + migrationID + "/report"
		}
		out = append(out, e)
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, rows.Err()
}

// Status returns progress counts.
func (s *service) Status(ctx context.Context, migrationID string) (*Status, error) {
	m, err := s.getMigration(ctx, migrationID)
	if err != nil {
		return nil, err
	}
	st := &Status{ID: m.ID, SourceID: m.SourceID, Status: m.Status, DryRun: m.DryRun, Step: m.Step, Phase: m.Phase,
		StartedAt: m.StartedAt, FinishedAt: m.FinishedAt, CreatedAt: m.CreatedAt, Counts: map[string]map[string]int{}}
	rows, err := s.db.QueryContext(ctx, `SELECT kind, status, count(*) FROM migration_items WHERE migration_id = ? GROUP BY kind, status`, m.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var kind, status string
		var n int
		if err := rows.Scan(&kind, &status, &n); err != nil {
			return nil, err
		}
		if st.Counts[kind] == nil {
			st.Counts[kind] = map[string]int{}
		}
		st.Counts[kind][status] = n
		st.Total += n
		switch status {
		case ItemDone, ItemPreview:
			st.Done += n
		case ItemFailed:
			st.Failed += n
		case ItemSkipped:
			st.Skipped += n
		default:
			st.Pending += n
		}
	}
	if st.Total > 0 {
		st.Percent = (st.Total - st.Pending) * 100 / st.Total
	}
	if m.Status == StatusCompleted || m.Status == StatusValidated || m.Status == StatusPreviewed {
		st.Percent = 100
	}
	return st, rows.Err()
}
