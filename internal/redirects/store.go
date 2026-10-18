package redirects

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/zadstack/gorun/internal/audit"
	"github.com/zadstack/gorun/internal/db"
	"github.com/zadstack/gorun/internal/hooks"
)

// HookSaved is emitted (async) after redirects change; payload SavedEvent.
const HookSaved = "redirects.saved"

// hitFlushDelay is how long hits are buffered before one batched write.
const hitFlushDelay = 10 * time.Second

// reloadBackoff is how long a failed reload keeps serving the old table.
const reloadBackoff = 5 * time.Second

// cacheTTL bounds how stale the lookup table can be for writes made by
// other processes sharing the database (CLI importer, stdio MCP); writes in
// this process invalidate it immediately.
const cacheTTL = 30 * time.Second

const maxPathLen = 2048

// ErrNotFound is returned for unknown redirects.
var ErrNotFound = errors.New("redirects: not found")

// ValidationError lists invalid fields (values are i18n keys).
type ValidationError struct {
	Fields map[string]string `json:"fields"`
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("redirects: invalid input: %v", e.Fields)
}

// SavedEvent is the HookSaved payload.
type SavedEvent struct {
	Action   string `json:"action"` // save | delete | import
	ID       string `json:"id,omitempty"`
	FromPath string `json:"from_path,omitempty"`
	Count    int    `json:"count,omitempty"`
}

// ValidStatus reports whether code is an allowed redirect status.
func ValidStatus(code int) bool { return code == 301 || code == 302 || code == 307 || code == 308 }

type service struct {
	db    *sql.DB
	hooks *hooks.Registry
	audit *audit.Writer
	log   *slog.Logger

	// siteHost returns the site's host ("example.com"), to reject absolute
	// targets that loop back to their own source.
	siteHost func(ctx context.Context) string

	cacheMu  sync.RWMutex
	cache    map[string]Redirect // enabled redirects by from_path; nil = not loaded
	loadedAt time.Time           // zero = stale (invalidated)
	gen      uint64              // bumped by invalidate; a load started earlier is discarded
	retryAt  time.Time           // after a failed load, no reload before this
	loadMu   sync.Mutex          // single-flight for reloads
	now      func() time.Time

	hitMu    sync.Mutex
	hits     map[string]int
	hitTimer *time.Timer
}

func newService(d *sql.DB, h *hooks.Registry, a *audit.Writer, log *slog.Logger) *service {
	return &service{db: d, hooks: h, audit: a, log: log, now: time.Now, siteHost: func(context.Context) string { return "" }}
}

// NormalizePath returns p as stored and matched: percent-decoded, query and
// fragment removed, a leading "/" and no trailing "/" (except the root).
// Matching stays case-sensitive.
func NormalizePath(p string) string {
	p = strings.TrimSpace(p)
	if u, err := url.Parse(p); err == nil && u.Scheme == "" && u.Host == "" {
		p = u.Path
	} else if i := strings.IndexAny(p, "?#"); i >= 0 {
		p = p[:i]
	}
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	for len(p) > 1 && strings.HasSuffix(p, "/") {
		p = strings.TrimSuffix(p, "/")
	}
	return p
}

// lookupKey normalizes an already-decoded request path for matching: a
// leading "/" and no trailing "/" (except the root). It does not parse or
// decode (a decoded path may legitimately contain "?" or "%").
func lookupKey(p string) string {
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	for len(p) > 1 && strings.HasSuffix(p, "/") {
		p = p[:len(p)-1]
	}
	return p
}

// HostOf returns the lower-cased host of an absolute URL, or "".
func HostOf(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return ""
	}
	return strings.ToLower(u.Host)
}

// ReservedPrefixes are application paths a redirect may never take over:
// saving one is a validation error and the middleware never answers them.
var ReservedPrefixes = []string{"/admin", "/api", "/mcp", "/auth", "/webhooks", "/media", "/static", "/checkout",
	"/account", "/healthz", "/readyz", "/metrics"}

// Reserved reports whether path is one of ReservedPrefixes or below one,
// by whole path segment and ignoring case ("/admin" and "/Admin/x" are,
// "/administration" and "/my-account" are not).
func Reserved(path string) bool {
	p := strings.ToLower(path)
	for _, pre := range ReservedPrefixes {
		if p == pre || strings.HasPrefix(p, pre+"/") {
			return true
		}
	}
	return false
}

// validTarget accepts site paths ("/x", not "//host") and http(s) URLs.
func validTarget(t string) bool {
	if t == "" || len(t) > maxPathLen || strings.ContainsAny(t, " \x00\r\n\t") {
		return false
	}
	if strings.HasPrefix(t, "/") {
		return !strings.HasPrefix(t, "//") && !strings.HasPrefix(t, `/\`)
	}
	u, err := url.Parse(t)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}

// clean validates r and normalizes its fields; host is the site's host,
// used to catch absolute targets that point back at the source.
func clean(r Redirect, host string) (Redirect, error) {
	fields := map[string]string{}
	rawFrom := strings.TrimSpace(r.FromPath)
	r.FromPath = NormalizePath(rawFrom)
	r.ToURL = strings.TrimSpace(r.ToURL)
	if r.Status == 0 {
		r.Status = 301
	}
	if rawFrom == "" || strings.HasPrefix(rawFrom, "//") || strings.Contains(rawFrom, "://") || len(r.FromPath) > maxPathLen ||
		strings.ContainsAny(r.FromPath, "\x00\r\n\t") {
		fields["from_path"] = "redirects.error.from_invalid"
	} else if Reserved(r.FromPath) {
		fields["from_path"] = "redirects.error.from_reserved"
	}
	if !validTarget(r.ToURL) {
		fields["to_url"] = "redirects.error.to_invalid"
	} else if isLoop(r.FromPath, r.ToURL, host) {
		fields["to_url"] = "redirects.error.loop"
	}
	if !ValidStatus(r.Status) {
		fields["status"] = "redirects.error.status_invalid"
	}
	if len(fields) > 0 {
		return r, &ValidationError{Fields: fields}
	}
	return r, nil
}

// isLoop reports a target that resolves to from on this site: a site path,
// or an absolute URL on host.
func isLoop(from, to, host string) bool {
	if strings.HasPrefix(to, "/") {
		return NormalizePath(to) == from
	}
	u, err := url.Parse(to)
	if err != nil || host == "" || !strings.EqualFold(u.Host, host) {
		return false
	}
	return NormalizePath(u.Path) == from
}

const columns = `id, from_path, to_url, status, hits, enabled, created_at`

func scan(row interface{ Scan(...any) error }) (Redirect, error) {
	var r Redirect
	err := row.Scan(&r.ID, &r.FromPath, &r.ToURL, &r.Status, &r.Hits, &r.Enabled, &r.CreatedAt)
	return r, err
}

// Lookup finds the enabled redirect for path (an already-decoded request
// path; only slashes are normalized). It reads an in-memory table loaded on
// first use, marked stale on every change in this process and after
// cacheTTL; reloads run outside the table lock (single-flight), and a failed
// reload keeps serving the old table for reloadBackoff.
func (s *service) Lookup(ctx context.Context, path string) (*Redirect, bool) {
	r, ok := s.table(ctx)[lookupKey(path)]
	if !ok {
		return nil, false
	}
	return &r, true
}

func (s *service) table(ctx context.Context) map[string]Redirect {
	s.cacheMu.RLock()
	c, now := s.cache, s.now()
	stale := c == nil || now.Sub(s.loadedAt) >= cacheTTL
	backoff := now.Before(s.retryAt)
	s.cacheMu.RUnlock()
	if !stale || backoff {
		return c
	}
	if c == nil {
		s.loadMu.Lock() // first load: wait for it
	} else if !s.loadMu.TryLock() {
		return c // another request is reloading; serve the old table
	}
	defer s.loadMu.Unlock()
	s.cacheMu.RLock()
	c, gen := s.cache, s.gen
	stale = c == nil || s.now().Sub(s.loadedAt) >= cacheTTL
	s.cacheMu.RUnlock()
	if !stale {
		return c // reloaded while we waited
	}
	lctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	loaded, err := s.loadEnabled(lctx)
	s.cacheMu.Lock()
	defer s.cacheMu.Unlock()
	if err != nil {
		s.log.Error("redirects: load", "err", err)
		s.retryAt = s.now().Add(reloadBackoff)
		return s.cache
	}
	if s.gen == gen {
		s.cache, s.loadedAt = loaded, s.now()
	}
	return loaded
}

func (s *service) loadEnabled(ctx context.Context) (map[string]Redirect, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+columns+` FROM redirects WHERE enabled = 1`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]Redirect{}
	for rows.Next() {
		r, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out[r.FromPath] = r
	}
	return out, rows.Err()
}

// invalidate marks the table stale (kept until the reload replaces it).
func (s *service) invalidate() {
	s.cacheMu.Lock()
	s.loadedAt, s.retryAt = time.Time{}, time.Time{}
	s.gen++
	s.cacheMu.Unlock()
}

var sortColumns = map[string]string{"created_at": "created_at", "from_path": "from_path", "hits": "hits", "status": "status"}

// List returns a page of redirects (newest first by default) and the total.
func (s *service) List(ctx context.Context, q ListQuery) ([]Redirect, int, error) {
	page, perPage := normalizePage(q.Page, q.PerPage)
	where, args := "", []any{}
	if term := strings.TrimSpace(q.Search); term != "" {
		like := "%" + strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(term) + "%"
		where = ` WHERE from_path LIKE ? ESCAPE '\' OR to_url LIKE ? ESCAPE '\'`
		args = append(args, like, like)
	}
	var total int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM redirects`+where, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("redirects: count: %w", err)
	}
	col, ok := sortColumns[q.Sort]
	if !ok {
		col = "created_at"
	}
	dir := "DESC"
	if strings.EqualFold(q.Order, "asc") {
		dir = "ASC"
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+columns+` FROM redirects`+where+` ORDER BY `+col+` `+dir+`, id `+dir+
		` LIMIT ? OFFSET ?`, append(args, perPage, (page-1)*perPage)...)
	if err != nil {
		return nil, 0, fmt.Errorf("redirects: list: %w", err)
	}
	defer rows.Close()
	out := []Redirect{}
	for rows.Next() {
		r, err := scan(rows)
		if err != nil {
			return nil, 0, fmt.Errorf("redirects: scan: %w", err)
		}
		out = append(out, r)
	}
	return out, total, rows.Err()
}

func (s *service) get(ctx context.Context, id string) (*Redirect, error) {
	r, err := scan(s.db.QueryRowContext(ctx, `SELECT `+columns+` FROM redirects WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("redirects: get: %w", err)
	}
	return &r, nil
}

// Save creates (empty ID) or updates a redirect. Hits are never written
// here (they only grow through Hit).
func (s *service) Save(ctx context.Context, r Redirect) (*Redirect, error) {
	r, err := clean(r, s.siteHost(ctx))
	if err != nil {
		return nil, err
	}
	created := r.ID == ""
	err = db.InTx(ctx, s.db, func(tx *sql.Tx) error {
		var other string
		err := tx.QueryRowContext(ctx, `SELECT id FROM redirects WHERE from_path = ? AND id != ?`, r.FromPath, r.ID).Scan(&other)
		if err == nil {
			return &ValidationError{Fields: map[string]string{"from_path": "redirects.error.from_taken"}}
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if created {
			r.ID, r.CreatedAt = db.NewID(), db.Now()
			_, err = tx.ExecContext(ctx, `INSERT INTO redirects (id, from_path, to_url, status, enabled, created_at)
				VALUES (?, ?, ?, ?, ?, ?)`, r.ID, r.FromPath, r.ToURL, r.Status, r.Enabled, r.CreatedAt)
		} else {
			var res sql.Result
			res, err = tx.ExecContext(ctx, `UPDATE redirects SET from_path = ?, to_url = ?, status = ?, enabled = ? WHERE id = ?`,
				r.FromPath, r.ToURL, r.Status, r.Enabled, r.ID)
			if err == nil {
				if n, _ := res.RowsAffected(); n == 0 {
					return ErrNotFound
				}
			}
		}
		if err != nil {
			return err
		}
		action := "redirects.update"
		if created {
			action = "redirects.create"
		}
		s.audit.RecordTx(ctx, tx, action, "redirect", r.ID, map[string]any{"from_path": r.FromPath, "to_url": r.ToURL,
			"status": r.Status, "enabled": r.Enabled})
		return nil
	})
	if err != nil {
		var ve *ValidationError
		if errors.As(err, &ve) || errors.Is(err, ErrNotFound) {
			return nil, err
		}
		return nil, fmt.Errorf("redirects: save: %w", err)
	}
	s.invalidate()
	s.hooks.Emit(ctx, HookSaved, SavedEvent{Action: "save", ID: r.ID, FromPath: r.FromPath})
	return s.get(ctx, r.ID)
}

// Delete removes a redirect.
func (s *service) Delete(ctx context.Context, id string) error {
	r, err := s.get(ctx, id)
	if err != nil {
		return err
	}
	err = db.InTx(ctx, s.db, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM redirects WHERE id = ?`, id); err != nil {
			return err
		}
		s.audit.RecordTx(ctx, tx, "redirects.delete", "redirect", id, map[string]any{"from_path": r.FromPath})
		return nil
	})
	if err != nil {
		return fmt.Errorf("redirects: delete: %w", err)
	}
	s.invalidate()
	s.hooks.Emit(ctx, HookSaved, SavedEvent{Action: "delete", ID: id, FromPath: r.FromPath})
	return nil
}

// Hit counts a use of the redirect without writing in the request path:
// hits are buffered and written in one batch hitFlushDelay after the first
// buffered hit.
func (s *service) Hit(_ context.Context, id string) {
	s.hitMu.Lock()
	defer s.hitMu.Unlock()
	if s.hits == nil {
		s.hits = map[string]int{}
	}
	s.hits[id]++
	if s.hitTimer == nil {
		s.hitTimer = time.AfterFunc(hitFlushDelay, func() {
			if err := s.flushHits(context.Background()); err != nil {
				s.log.Error("redirects: flush hits", "err", err)
			}
		})
	}
}

// flushHits writes the buffered hit counts.
func (s *service) flushHits(ctx context.Context) error {
	s.hitMu.Lock()
	pending := s.hits
	s.hits = nil
	if s.hitTimer != nil {
		s.hitTimer.Stop()
		s.hitTimer = nil
	}
	s.hitMu.Unlock()
	if len(pending) == 0 {
		return nil
	}
	return db.InTx(ctx, s.db, func(tx *sql.Tx) error {
		for id, n := range pending {
			if _, err := tx.ExecContext(ctx, `UPDATE redirects SET hits = hits + ? WHERE id = ?`, n, id); err != nil {
				return err
			}
		}
		return nil
	})
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
