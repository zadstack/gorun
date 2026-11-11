package content

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/db"
	"github.com/zadstack/gorun/internal/httpx"
)

// Editor autosave. Every autosave goes into content_autosaves, one row per
// item and user, and never into the item: a draft keeps its revision history
// (each real save adds a revision and history is pruned), a published or
// scheduled item keeps its live content, and no save hooks, search indexing
// or audit entries run every 30 seconds. The row records the edit token of
// the version the editor was opened on; an autosave whose token no longer
// matches the item is refused with ErrConflict, so an autosave never sits on
// top of somebody else's newer save. A normal save by the same user removes
// the row.

const (
	// maxAutosaveBytes bounds one autosave request body.
	maxAutosaveBytes = 4 << 20
	// maxAutosaveValues bounds the number of form values kept.
	maxAutosaveValues = 2000
)

// Autosave is one user's unsaved editor state for one item.
type Autosave struct {
	ContentID     string     `json:"content_id"`
	UserID        string     `json:"user_id"`
	Fields        url.Values `json:"fields"`
	BaseEditToken string     `json:"base_edit_token"`
	BaseUpdatedAt string     `json:"base_updated_at"`
	SavedAt       string     `json:"saved_at"`
}

// autosaveKey reports whether a form key is kept in an autosave: the text
// the editor types (title, slug, excerpt, body), SEO and custom fields.
// Status, dates, author, terms and tokens are deliberately left out: they
// are decisions, not drafts.
func autosaveKey(k string) bool {
	switch k {
	case "title", "slug", "excerpt", "body":
		return true
	}
	return strings.HasPrefix(k, "fields[") || strings.HasPrefix(k, "seo[")
}

func autosaveFields(form url.Values) url.Values {
	out := url.Values{}
	n := 0
	for k, vals := range form {
		if !autosaveKey(k) {
			continue
		}
		n += len(vals)
		if n > maxAutosaveValues {
			break
		}
		out[k] = append([]string(nil), vals...)
	}
	return out
}

// autosaveUser returns the signed-in user allowed to autosave it.
func autosaveUser(ctx context.Context, it *Item) (*auth.User, error) {
	u := auth.UserFromContext(ctx)
	if u == nil || !auth.Can(ctx, "content.edit") {
		return nil, ErrForbidden
	}
	if err := checkOwner(ctx, it); err != nil {
		return nil, err
	}
	return u, nil
}

// SaveAutosave stores the editor fields of the signed-in user for item id.
// base is the edit token the editor was loaded with; when the item changed
// since, nothing is stored and ErrConflict is returned.
func (s *service) SaveAutosave(ctx context.Context, id, base string, form url.Values) (*Autosave, error) {
	it, err := s.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	u, err := autosaveUser(ctx, it)
	if err != nil {
		return nil, err
	}
	if base == "" {
		return nil, ErrConflict
	}
	a := &Autosave{ContentID: id, UserID: u.ID, Fields: autosaveFields(form), BaseEditToken: base, SavedAt: s.nowString()}
	payload, err := json.Marshal(a.Fields)
	if err != nil {
		return nil, fmt.Errorf("content: encode autosave: %w", err)
	}
	err = db.InTx(ctx, s.db, func(tx *sql.Tx) error {
		current, err := s.getItem(ctx, tx, id)
		if err != nil {
			return err
		}
		if err := checkOwner(ctx, current); err != nil {
			return err
		}
		if EditToken(current) != base {
			return ErrConflict
		}
		a.BaseUpdatedAt = current.UpdatedAt
		_, err = tx.ExecContext(ctx, `INSERT INTO content_autosaves (content_id, user_id, payload_json, base_edit_token, base_updated_at, saved_at)
			VALUES (?, ?, ?, ?, ?, ?)
			ON CONFLICT (content_id, user_id) DO UPDATE SET payload_json = excluded.payload_json,
				base_edit_token = excluded.base_edit_token, base_updated_at = excluded.base_updated_at, saved_at = excluded.saved_at`,
			id, u.ID, string(payload), base, a.BaseUpdatedAt, a.SavedAt)
		if err != nil {
			return fmt.Errorf("content: save autosave: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return a, nil
}

// Autosave returns the signed-in user's autosave for id, or nil.
func (s *service) Autosave(ctx context.Context, id string) (*Autosave, error) {
	u := auth.UserFromContext(ctx)
	if u == nil {
		return nil, nil
	}
	var a Autosave
	var payload string
	err := s.db.QueryRowContext(ctx, `SELECT content_id, user_id, payload_json, base_edit_token, base_updated_at, saved_at
		FROM content_autosaves WHERE content_id = ? AND user_id = ?`, id, u.ID).
		Scan(&a.ContentID, &a.UserID, &payload, &a.BaseEditToken, &a.BaseUpdatedAt, &a.SavedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("content: get autosave: %w", err)
	}
	if err := json.Unmarshal([]byte(payload), &a.Fields); err != nil {
		return nil, fmt.Errorf("content: decode autosave: %w", err)
	}
	return &a, nil
}

// DiscardAutosave removes the signed-in user's autosave for id.
func (s *service) DiscardAutosave(ctx context.Context, id string) error {
	u := auth.UserFromContext(ctx)
	if u == nil {
		return nil
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM content_autosaves WHERE content_id = ? AND user_id = ?`, id, u.ID); err != nil {
		return fmt.Errorf("content: discard autosave: %w", err)
	}
	return nil
}

// autosaveView feeds the editor's restore banner.
type autosaveView struct {
	SavedAt string
	Stale   bool   // the item was saved since this autosave's base version
	Payload string // JSON of the saved form fields
}

func (h *adminHandlers) autosaveBanner(ctx context.Context, it *Item) *autosaveView {
	a, err := h.svc.Autosave(ctx, it.ID)
	if err != nil {
		h.d.Log.Warn("content: autosave banner", "id", it.ID, "err", err)
		return nil
	}
	if a == nil || len(a.Fields) == 0 {
		return nil
	}
	raw, _ := json.Marshal(a.Fields)
	return &autosaveView{SavedAt: a.SavedAt, Stale: a.BaseEditToken != EditToken(it), Payload: string(raw)}
}

// autosave answers the editor's background save with the JSON envelope.
func (h *adminHandlers) autosave(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	it, err := h.svc.Get(r.Context(), id)
	if err == nil && it.Type != chi.URLParam(r, "type") {
		err = ErrNotFound
	}
	var a *Autosave
	if err == nil {
		r.Body = http.MaxBytesReader(w, r.Body, maxAutosaveBytes)
		if perr := r.ParseForm(); perr != nil {
			httpx.Error(w, http.StatusRequestEntityTooLarge, "too_large", "autosave too large", nil)
			return
		}
		a, err = h.svc.SaveAutosave(r.Context(), id, r.PostForm.Get("edit_token"), r.PostForm)
	}
	switch {
	case err == nil:
		httpx.JSON(w, http.StatusOK, map[string]string{"saved_at": a.SavedAt}, nil)
	case errors.Is(err, ErrConflict):
		httpx.Error(w, http.StatusConflict, "conflict", h.tr(r.Context(), "content.autosave.conflict"), nil)
	case errors.Is(err, ErrForbidden):
		httpx.Error(w, http.StatusForbidden, "forbidden", h.tr(r.Context(), "content.autosave.forbidden"), nil)
	case errors.Is(err, ErrNotFound):
		httpx.Error(w, http.StatusNotFound, "not_found", "not found", nil)
	default:
		h.d.Log.Error("content: autosave", "id", id, "err", err)
		httpx.Error(w, http.StatusInternalServerError, "internal", "internal error", nil)
	}
}

// discardAutosave removes the user's autosave: JSON for scripts, a redirect
// back to the editor for a plain form post.
func (h *adminHandlers) discardAutosave(w http.ResponseWriter, r *http.Request) {
	t, it, ok := h.itemFromURL(w, r)
	if !ok {
		return
	}
	if err := checkOwner(r.Context(), it); err != nil {
		h.fail(w, r, err)
		return
	}
	if err := h.svc.DiscardAutosave(r.Context(), it.ID); err != nil {
		h.fail(w, r, err)
		return
	}
	if wantsJSON(r) {
		httpx.JSON(w, http.StatusOK, map[string]bool{"discarded": true}, nil)
		return
	}
	httpx.SetFlash(w, "success", "content.flash.autosave_discarded")
	http.Redirect(w, r, "/admin/content/"+t.Slug+"/"+it.ID, http.StatusSeeOther)
}

func wantsJSON(r *http.Request) bool {
	return strings.Contains(r.Header.Get("Accept"), "application/json")
}
