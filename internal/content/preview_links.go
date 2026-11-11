package content

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/db"
	"github.com/zadstack/gorun/internal/httpx"
)

// Shareable preview links: a signed-out visitor holding /preview/{token} sees
// the saved version of one item through the public theme until the link
// expires or is revoked. A token is 32 random bytes (base64url, 43
// characters); only its SHA-256 is stored.

// ServicePreview is the kernel service name of the PreviewResolver.
const ServicePreview = "content.preview"

// PreviewPathPrefix is the public path of a preview link.
const PreviewPathPrefix = "/preview/"

// PreviewTokenPattern matches a well-formed token (for routing).
const PreviewTokenPattern = `[A-Za-z0-9_-]{43}`

// maxActivePreviewLinks bounds the active links of one item.
const maxActivePreviewLinks = 20

var previewTokenRe = regexp.MustCompile(`^` + PreviewTokenPattern + `$`)

// PreviewTTLs are the offered lifetimes, by form/API value.
var PreviewTTLs = map[string]time.Duration{"1h": time.Hour, "24h": 24 * time.Hour, "7d": 7 * 24 * time.Hour}

// DefaultPreviewTTL is used when no lifetime is given.
const DefaultPreviewTTL = "24h"

// PreviewLink is a stored preview link (never its token).
type PreviewLink struct {
	ID        string `json:"id"`
	ContentID string `json:"content_id"`
	CreatedBy string `json:"created_by"`
	CreatedAt string `json:"created_at"`
	ExpiresAt string `json:"expires_at"`
	RevokedAt string `json:"revoked_at,omitempty"`
}

// PreviewLinkCreated is returned once, at creation: the only time the URL
// (which contains the token) is available.
type PreviewLinkCreated struct {
	PreviewLink
	URL string `json:"url"`
}

// PreviewLinkInput creates a link; TTL is 1h, 24h (default) or 7d.
type PreviewLinkInput struct {
	TTL string `json:"ttl,omitempty" jsonschema:"1h, 24h (default) or 7d"`
}

// PreviewResolver resolves a preview token for the public site.
type PreviewResolver interface {
	// ResolvePreview returns the item of an active link; ErrNotFound for an
	// unknown, malformed, expired or revoked token and for trashed items.
	ResolvePreview(ctx context.Context, token string) (*Item, error)
}

func hashPreviewToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// previewEditor checks that ctx may manage the preview links of id.
func (s *service) previewEditor(ctx context.Context, id string) (*Item, error) {
	if auth.UserFromContext(ctx) != nil && !auth.Can(ctx, "content.edit") {
		return nil, ErrForbidden
	}
	it, err := s.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := checkOwner(ctx, it); err != nil {
		return nil, err
	}
	if it.Status == "trash" {
		return nil, invalid(FieldErrors{"status": "content.preview.trashed"})
	}
	return it, nil
}

// CreatePreviewLink creates a link to id valid for ttl ("1h", "24h", "7d";
// "" means 24h) and returns it with its token.
func (s *service) CreatePreviewLink(ctx context.Context, id, ttl string) (*PreviewLink, string, error) {
	if ttl == "" {
		ttl = DefaultPreviewTTL
	}
	life, ok := PreviewTTLs[ttl]
	if !ok {
		return nil, "", invalid(FieldErrors{"ttl": "content.preview.ttl_invalid"})
	}
	it, err := s.previewEditor(ctx, id)
	if err != nil {
		return nil, "", err
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return nil, "", fmt.Errorf("content: preview token: %w", err)
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	now := s.now()
	link := &PreviewLink{ID: db.NewID(), ContentID: it.ID, CreatedAt: db.FormatTime(now), ExpiresAt: db.FormatTime(now.Add(life))}
	if u := auth.UserFromContext(ctx); u != nil {
		link.CreatedBy = u.ID
	}
	err = db.InTx(ctx, s.db, func(tx *sql.Tx) error {
		var active int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM content_preview_links WHERE content_id = ? AND revoked_at IS NULL AND expires_at > ?`,
			it.ID, link.CreatedAt).Scan(&active); err != nil {
			return err
		}
		if active >= maxActivePreviewLinks {
			return invalid(FieldErrors{"ttl": "content.preview.too_many"})
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO content_preview_links (id, content_id, token_hash, created_by, created_at, expires_at)
			VALUES (?, ?, ?, ?, ?, ?)`, link.ID, it.ID, hashPreviewToken(token), link.CreatedBy, link.CreatedAt, link.ExpiresAt); err != nil {
			return fmt.Errorf("content: create preview link: %w", err)
		}
		s.audit.RecordTx(ctx, tx, "content.preview_link_create", "content", it.ID,
			map[string]any{"link_id": link.ID, "expires_at": link.ExpiresAt, "ttl": ttl})
		return nil
	})
	if err != nil {
		return nil, "", err
	}
	return link, token, nil
}

// PreviewLinks lists the active (not revoked, not expired) links of id, newest first.
func (s *service) PreviewLinks(ctx context.Context, id string) ([]PreviewLink, error) {
	if _, err := s.previewEditor(ctx, id); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id, content_id, created_by, created_at, expires_at FROM content_preview_links
		WHERE content_id = ? AND revoked_at IS NULL AND expires_at > ? ORDER BY id DESC`, id, s.nowString())
	if err != nil {
		return nil, fmt.Errorf("content: list preview links: %w", err)
	}
	defer rows.Close()
	out := []PreviewLink{}
	for rows.Next() {
		var l PreviewLink
		if err := rows.Scan(&l.ID, &l.ContentID, &l.CreatedBy, &l.CreatedAt, &l.ExpiresAt); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// RevokePreviewLink revokes link linkID of item id at once. Revoking an
// already revoked or expired link is a no-op; an unknown link is ErrNotFound.
func (s *service) RevokePreviewLink(ctx context.Context, id, linkID string) error {
	it, err := s.Get(ctx, id)
	if err != nil {
		return err
	}
	if auth.UserFromContext(ctx) != nil && !auth.Can(ctx, "content.edit") {
		return ErrForbidden
	}
	if err := checkOwner(ctx, it); err != nil {
		return err
	}
	return db.InTx(ctx, s.db, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE content_preview_links SET revoked_at = COALESCE(revoked_at, ?) WHERE id = ? AND content_id = ?`,
			s.nowString(), linkID, id)
		if err != nil {
			return fmt.Errorf("content: revoke preview link: %w", err)
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrNotFound
		}
		s.audit.RecordTx(ctx, tx, "content.preview_link_revoke", "content", id, map[string]any{"link_id": linkID})
		return nil
	})
}

// ResolvePreview implements PreviewResolver. The lookup is by the token's
// SHA-256 (the token itself is never stored or compared), confirmed with a
// constant-time comparison; every failure is the same ErrNotFound.
func (s *service) ResolvePreview(ctx context.Context, token string) (*Item, error) {
	if !previewTokenRe.MatchString(token) {
		return nil, ErrNotFound
	}
	hash := hashPreviewToken(token)
	var stored, contentID, expires string
	var revoked sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT token_hash, content_id, expires_at, revoked_at FROM content_preview_links WHERE token_hash = ?`, hash).
		Scan(&stored, &contentID, &expires, &revoked)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("content: resolve preview: %w", err)
	}
	if subtle.ConstantTimeCompare([]byte(stored), []byte(hash)) != 1 || revoked.Valid {
		return nil, ErrNotFound
	}
	if at, err := db.ParseTime(expires); err != nil || !s.now().Before(at) {
		return nil, ErrNotFound
	}
	it, err := s.getItem(ctx, s.db, contentID)
	if err != nil {
		return nil, ErrNotFound
	}
	if it.Status == "trash" {
		return nil, ErrNotFound
	}
	s.fillTerms(ctx, it)
	return it, nil
}

// previewURL is the absolute URL of token: the configured site address
// (site.base_url, else the server's base URL), else the request's host.
func previewURL(r *http.Request, base, token string) string {
	base = strings.TrimRight(base, "/")
	if base == "" && r != nil {
		scheme := "http"
		if httpx.RequestIsHTTPS(r) {
			scheme = "https"
		}
		base = scheme + "://" + r.Host
	}
	return base + PreviewPathPrefix + token
}

func (h *adminHandlers) siteBaseURL(ctx context.Context) string {
	if h.d.Settings != nil {
		if v := h.d.Settings.GetString(ctx, "site.base_url", ""); v != "" {
			return v
		}
	}
	return ""
}

// --- admin ---

type previewLinkView struct {
	Type      *Type
	TypeName  string
	Item      *Item
	EditURL   string
	URL       string
	ExpiresAt string
}

// createPreviewLink handles the editor's "Create preview link": JSON for
// scripts, otherwise a page showing the link once.
func (h *adminHandlers) createPreviewLink(w http.ResponseWriter, r *http.Request) {
	t, it, ok := h.itemFromURL(w, r)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		h.tpl.RenderError(w, r, http.StatusBadRequest)
		return
	}
	link, token, err := h.svc.CreatePreviewLink(r.Context(), it.ID, r.PostForm.Get("preview_ttl"))
	var ve *ValidationError
	if err != nil {
		if wantsJSON(r) {
			switch {
			case errors.As(err, &ve):
				msgs := map[string]string{}
				for k, v := range ve.Fields {
					msgs[k] = h.tr(r.Context(), v)
				}
				httpx.Error(w, http.StatusUnprocessableEntity, "validation_failed", "validation failed", msgs)
			case errors.Is(err, ErrForbidden):
				httpx.Error(w, http.StatusForbidden, "forbidden", "not allowed", nil)
			default:
				h.d.Log.Error("content: create preview link", "id", it.ID, "err", err)
				httpx.Error(w, http.StatusInternalServerError, "internal", "internal error", nil)
			}
			return
		}
		if errors.As(err, &ve) {
			for _, v := range ve.Fields {
				httpx.SetFlash(w, "error", v)
				break
			}
			http.Redirect(w, r, "/admin/content/"+t.Slug+"/"+it.ID, http.StatusSeeOther)
			return
		}
		h.fail(w, r, err)
		return
	}
	u := previewURL(r, h.siteBaseURL(r.Context()), token)
	if wantsJSON(r) {
		httpx.JSON(w, http.StatusCreated, PreviewLinkCreated{PreviewLink: *link, URL: u}, nil)
		return
	}
	w.Header().Set("Referrer-Policy", "no-referrer")
	h.tpl.RenderWith(w, r, http.StatusCreated, "admin", "pages/content/preview_link.html", previewLinkView{Type: t, Item: it,
		EditURL: "/admin/content/" + t.Slug + "/" + it.ID, URL: u, ExpiresAt: link.ExpiresAt})
}

func (h *adminHandlers) revokePreviewLink(w http.ResponseWriter, r *http.Request) {
	t, it, ok := h.itemFromURL(w, r)
	if !ok {
		return
	}
	err := h.svc.RevokePreviewLink(r.Context(), it.ID, chi.URLParam(r, "link"))
	if wantsJSON(r) {
		switch {
		case err == nil:
			httpx.JSON(w, http.StatusOK, map[string]bool{"revoked": true}, nil)
		case errors.Is(err, ErrNotFound):
			httpx.Error(w, http.StatusNotFound, "not_found", "not found", nil)
		case errors.Is(err, ErrForbidden):
			httpx.Error(w, http.StatusForbidden, "forbidden", "not allowed", nil)
		default:
			h.d.Log.Error("content: revoke preview link", "id", it.ID, "err", err)
			httpx.Error(w, http.StatusInternalServerError, "internal", "internal error", nil)
		}
		return
	}
	if err != nil {
		h.fail(w, r, err)
		return
	}
	httpx.SetFlash(w, "success", "content.flash.preview_revoked")
	http.Redirect(w, r, "/admin/content/"+t.Slug+"/"+it.ID, http.StatusSeeOther)
}

// --- REST ---

func (a *api) listPreviewLinks(w http.ResponseWriter, r *http.Request) {
	if !a.allow(w, r, "content.edit") {
		return
	}
	links, err := a.svc.PreviewLinks(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		a.fail(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, links, httpx.Meta{Total: len(links), Page: 1, PerPage: len(links)})
}

func (a *api) createPreviewLink(w http.ResponseWriter, r *http.Request) {
	if !a.allow(w, r, "content.edit") {
		return
	}
	var in PreviewLinkInput
	if r.ContentLength != 0 && !a.decode(w, r, &in) {
		return
	}
	link, token, err := a.svc.CreatePreviewLink(r.Context(), chi.URLParam(r, "id"), in.TTL)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	base := ""
	if a.d.Settings != nil {
		base = a.d.Settings.GetString(r.Context(), "site.base_url", "")
	}
	w.Header().Set("Cache-Control", "no-store")
	httpx.JSON(w, http.StatusCreated, PreviewLinkCreated{PreviewLink: *link, URL: previewURL(r, base, token)}, nil)
}

func (a *api) revokePreviewLink(w http.ResponseWriter, r *http.Request) {
	if !a.allow(w, r, "content.edit") {
		return
	}
	if err := a.svc.RevokePreviewLink(r.Context(), chi.URLParam(r, "id"), chi.URLParam(r, "link")); err != nil {
		a.fail(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]bool{"revoked": true}, nil)
}
