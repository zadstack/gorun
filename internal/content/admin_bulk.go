package content

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"maps"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/httpx"
	"github.com/zadstack/gorun/internal/i18n"
	"github.com/zadstack/gorun/internal/kernel"
)

// Bulk actions on the content list (POST /admin/content/{type}/bulk).
// Trash and delete keep their original behaviour. Every other action runs
// item by item through the same service method as the editor (Publish,
// Unpublish, Update), so ownership, publish permission, approvals hooks,
// validation, revisions and audit entries apply exactly as for one item.
// The outcome ("12 published, 2 skipped") is kept in a short-lived cookie
// for the list page that the request redirects to.

const (
	bulkMaxItems   = 200 // ids accepted per request
	bulkMaxSkipped = 40  // skipped ids reported by id (the rest are counted)
	bulkCookie     = "gorun_content_bulk"
)

// bulkActions are the actions that report a per-item outcome.
var bulkActions = []string{"publish", "unpublish", "schedule", "author", "term_add", "term_remove"}

// Skip reasons, translated as content.bulk.reason.<reason>.
const (
	skipNotFound     = "not_found"
	skipNotOwner     = "not_owner"
	skipNoPermission = "no_permission"
	skipTrashed      = "trashed"
	skipUnchanged    = "unchanged"
	skipConflict     = "conflict"
	skipInvalid      = "invalid"
	skipError        = "error"
)

var bulkSkipReasons = []string{skipNoPermission, skipNotOwner, skipConflict, skipInvalid, skipTrashed, skipUnchanged, skipNotFound, skipError}

var errBulkUnchanged = errors.New("content: bulk action changes nothing")

type bulkRequest struct {
	Action    string
	PublishAt string
	AuthorID  string
	Taxonomy  string
	Term      string
}

type bulkSkip struct {
	ID     string `json:"i"`
	Reason string `json:"r"`
}

type bulkResult struct {
	Action  string     `json:"a"`
	Done    int        `json:"n"`
	Skipped []bulkSkip `json:"s,omitempty"`
	More    int        `json:"m,omitempty"` // skipped beyond bulkMaxSkipped
}

func (res *bulkResult) skip(id, reason string) {
	if len(res.Skipped) < bulkMaxSkipped {
		res.Skipped = append(res.Skipped, bulkSkip{ID: id, Reason: reason})
		return
	}
	res.More++
}

func (h *adminHandlers) bulk(w http.ResponseWriter, r *http.Request) {
	t, ok := h.typeFromURL(w, r)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		h.tpl.RenderError(w, r, http.StatusBadRequest)
		return
	}
	ctx := r.Context()
	back := bulkReturn(t, r.PostForm.Get("return"))
	action := r.PostForm.Get("action")
	if action == "" || action == "trash" || action == "delete" {
		if !auth.Can(ctx, "content.delete") {
			h.fail(w, r, ErrForbidden)
			return
		}
		h.bulkDelete(w, r, t, action == "delete", back)
		return
	}
	if kind, ok := bulkNeedsChoice(ctx, action, r.PostForm); ok {
		ids := bulkIDs(r.PostForm["ids"])
		if len(ids) == 0 {
			httpx.SetFlash(w, "info", "content.flash.bulk_none")
			http.Redirect(w, r, back, http.StatusSeeOther)
			return
		}
		h.bulkChoose(w, r, t, kind, ids)
		return
	}
	req, errKey := h.parseBulk(ctx, t, action, r.PostForm)
	if errKey != "" {
		httpx.SetFlash(w, "error", errKey)
		http.Redirect(w, r, back, http.StatusSeeOther)
		return
	}
	ids := bulkIDs(r.PostForm["ids"])
	if len(ids) == 0 {
		httpx.SetFlash(w, "info", "content.flash.bulk_none")
		http.Redirect(w, r, back, http.StatusSeeOther)
		return
	}
	res := h.applyBulk(ctx, t, req, ids)
	setBulkCookie(w, t, res)
	http.Redirect(w, r, back, http.StatusSeeOther)
}

// bulkDelete is the original trash / permanent delete action.
func (h *adminHandlers) bulkDelete(w http.ResponseWriter, r *http.Request, t *Type, hard bool, back string) {
	trashed, deleted := 0, 0
	for _, id := range bulkIDs(r.PostForm["ids"]) {
		it, err := h.svc.Get(r.Context(), id)
		if err != nil || it.Type != t.Slug {
			continue
		}
		purge := hard && it.Status == "trash"
		if err := h.svc.Delete(r.Context(), id, purge); err != nil {
			continue
		}
		if purge {
			deleted++
		} else {
			trashed++
		}
	}
	switch {
	case deleted > 0 && trashed == 0:
		httpx.SetFlash(w, "success", "content.flash.bulk_deleted")
	case trashed > 0 && deleted == 0:
		httpx.SetFlash(w, "success", "content.flash.bulk_trashed")
	case trashed > 0:
		httpx.SetFlash(w, "success", "content.flash.bulk_mixed")
	default:
		httpx.SetFlash(w, "info", "content.flash.bulk_none")
	}
	http.Redirect(w, r, back, http.StatusSeeOther)
}

// bulkIDs drops empty and repeated ids and caps the request.
func bulkIDs(in []string) []string {
	out := make([]string, 0, len(in))
	for _, id := range in {
		if id = strings.TrimSpace(id); id != "" && !slices.Contains(out, id) {
			out = append(out, id)
		}
		if len(out) == bulkMaxItems {
			break
		}
	}
	return out
}

// bulkReturn is the list URL to go back to, keeping only list filters.
func bulkReturn(t *Type, raw string) string {
	base := "/admin/content/" + t.Slug
	q, err := url.ParseQuery(strings.TrimPrefix(raw, "?"))
	if err != nil {
		return base
	}
	keep := url.Values{}
	for _, k := range []string{"search", "status", "locale", "author", "view", "layout", "page"} {
		if v := q.Get(k); v != "" {
			keep.Set(k, v)
		}
	}
	if len(keep) == 0 {
		return base
	}
	return base + "?" + keep.Encode()
}

// parseBulk validates the request as a whole; an error key refuses it
// before any item changes.
func (h *adminHandlers) parseBulk(ctx context.Context, t *Type, action string, form url.Values) (bulkRequest, string) {
	req := bulkRequest{Action: action}
	switch action {
	case "publish", "unpublish":
	case "schedule":
		raw := strings.TrimSpace(form.Get("publish_at"))
		at, ok := ParseDatetime(raw)
		if !ok {
			return req, "content.bulk.error.schedule_invalid"
		}
		if !at.After(h.svc.now()) {
			return req, "content.bulk.error.schedule_past"
		}
		req.PublishAt = raw
	case "author":
		req.AuthorID = strings.TrimSpace(form.Get("author_id"))
		if !h.canAuthor(ctx, req.AuthorID) {
			return req, "content.bulk.error.author_invalid"
		}
	case "term_add", "term_remove":
		tax, term, _ := strings.Cut(form.Get("term"), "/")
		if !slices.Contains(t.Taxonomies, tax) || term == "" || !h.termExists(ctx, tax, term) {
			return req, "content.bulk.error.term_invalid"
		}
		req.Taxonomy, req.Term = tax, term
	default:
		return req, "content.bulk.error.action"
	}
	return req, ""
}

// canAuthor reports whether id is an active staff user whose role may
// create content, the people the bulk author select offers.
func (h *adminHandlers) canAuthor(ctx context.Context, id string) bool {
	us, ok := kernel.Get[auth.Service](h.d, kernel.ServiceAuth)
	if !ok || id == "" {
		return false
	}
	u, err := us.Get(ctx, id)
	return err == nil && permittedAuthor(u)
}

func permittedAuthor(u *auth.User) bool {
	return u != nil && u.Status == auth.StatusActive && u.Role != "customer" && auth.RoleAllows(u.Role, "content.create")
}

func (h *adminHandlers) termExists(ctx context.Context, tax, slug string) bool {
	var one int
	return h.d.DB.QueryRowContext(ctx, `SELECT 1 FROM terms WHERE taxonomy = ? AND slug = ?`, tax, slug).Scan(&one) == nil
}

// applyBulk runs req on each item and records what happened.
func (h *adminHandlers) applyBulk(ctx context.Context, t *Type, req bulkRequest, ids []string) bulkResult {
	res := bulkResult{Action: req.Action}
	u := auth.UserFromContext(ctx)
	for _, id := range ids {
		it, err := h.svc.Get(ctx, id)
		switch {
		case errors.Is(err, ErrForbidden) && ownOnly(u):
			res.skip(id, skipNotOwner)
			continue
		case errors.Is(err, ErrForbidden):
			res.skip(id, skipNoPermission)
			continue
		case err != nil || it.Type != t.Slug:
			res.skip(id, skipNotFound)
			continue
		case ownOnly(u) && it.AuthorID != u.ID:
			res.skip(id, skipNotOwner)
			continue
		case it.Status == "trash":
			res.skip(id, skipTrashed)
			continue
		}
		if reason := h.bulkOne(ctx, req, it); reason != "" {
			res.skip(id, reason)
			continue
		}
		res.Done++
	}
	return res
}

// bulkOne applies req to it through the single-item service path and
// returns a skip reason, or "" when the item changed.
func (h *adminHandlers) bulkOne(ctx context.Context, req bulkRequest, it *Item) string {
	err := h.bulkChange(ctx, req, it)
	var ve *ValidationError
	switch {
	case err == nil:
		return ""
	case errors.Is(err, errBulkUnchanged):
		return skipUnchanged
	case errors.Is(err, ErrForbidden):
		return skipNoPermission
	case errors.Is(err, ErrConflict), errors.Is(err, ErrPublishConflict):
		return skipConflict
	case errors.Is(err, ErrNotFound):
		return skipNotFound
	case errors.As(err, &ve):
		return skipInvalid
	default:
		h.d.Log.Error("content: bulk action", "action", req.Action, "id", it.ID, "err", err)
		return skipError
	}
}

func (h *adminHandlers) bulkChange(ctx context.Context, req bulkRequest, it *Item) error {
	switch req.Action {
	case "publish":
		if it.Status == "published" {
			return errBulkUnchanged
		}
		return h.svc.Publish(ctx, it.ID)
	case "unpublish":
		if !isLive(it.Status) {
			return errBulkUnchanged
		}
		return h.svc.Unpublish(ctx, it.ID)
	}
	// The rest edit the item like the editor's Save, which needs content.edit.
	if !auth.Can(ctx, "content.edit") {
		return ErrForbidden
	}
	in := InputFromItem(it)
	in.SEO, in.Terms = maps.Clone(it.SEO), maps.Clone(it.Terms)
	if in.Terms == nil {
		in.Terms = map[string][]string{}
	}
	switch req.Action {
	case "schedule":
		if at, ok := ParseDatetime(req.PublishAt); ok && it.Status == "scheduled" && it.PublishAt != "" {
			if cur, ok := ParseDatetime(it.PublishAt); ok && cur.Equal(at) {
				return errBulkUnchanged
			}
		}
		in.Status, in.PublishAt = "scheduled", req.PublishAt
	case "author":
		// Authors and contributors keep their own byline (as in the editor).
		if ownOnly(auth.UserFromContext(ctx)) {
			return ErrForbidden
		}
		if it.AuthorID == req.AuthorID {
			return errBulkUnchanged
		}
		in.AuthorID = req.AuthorID
	case "term_add":
		if slices.Contains(in.Terms[req.Taxonomy], req.Term) {
			return errBulkUnchanged
		}
		in.Terms[req.Taxonomy] = append(slices.Clone(in.Terms[req.Taxonomy]), req.Term)
	case "term_remove":
		kept := slices.DeleteFunc(slices.Clone(in.Terms[req.Taxonomy]), func(s string) bool { return s == req.Term })
		if len(kept) == len(in.Terms[req.Taxonomy]) {
			return errBulkUnchanged
		}
		in.Terms[req.Taxonomy] = kept
	}
	_, err := h.svc.Update(ctx, it.ID, in)
	return err
}

func setBulkCookie(w http.ResponseWriter, t *Type, res bulkResult) {
	raw, err := json.Marshal(res)
	if err != nil {
		return
	}
	http.SetCookie(w, &http.Cookie{Name: bulkCookie, Value: base64.RawURLEncoding.EncodeToString(raw),
		Path: "/admin/content/" + t.Slug, MaxAge: 120, HttpOnly: true, SameSite: http.SameSiteLaxMode})
}

// --- result summary on the list page ---

type bulkSkipGroup struct {
	Reason string
	Titles []string
	Count  int
}

type bulkSummary struct {
	Action, Done, Skipped string
	Ok                    bool
	Groups                []bulkSkipGroup
}

// takeBulkSummary reads and clears the outcome of the last bulk request.
// Titles are shown only for items the user may open.
func (h *adminHandlers) takeBulkSummary(w http.ResponseWriter, r *http.Request, t *Type) *bulkSummary {
	c, err := r.Cookie(bulkCookie)
	if err != nil || c.Value == "" {
		return nil
	}
	http.SetCookie(w, &http.Cookie{Name: bulkCookie, Value: "", Path: "/admin/content/" + t.Slug, MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteLaxMode})
	raw, err := base64.RawURLEncoding.DecodeString(c.Value)
	var res bulkResult
	if err != nil || json.Unmarshal(raw, &res) != nil || !slices.Contains(bulkActions, res.Action) || res.Done < 0 || res.More < 0 {
		return nil
	}
	ctx := r.Context()
	locale := i18n.Locale(ctx)
	tr := func(key string, args ...any) string {
		if h.d.I18n == nil {
			return key
		}
		return h.d.I18n.T(locale, key, args...)
	}
	skipped := len(res.Skipped) + res.More
	out := &bulkSummary{Action: res.Action, Done: tr("content.bulk.done."+res.Action, res.Done), Ok: skipped == 0}
	if skipped > 0 {
		out.Skipped = tr("content.bulk.skipped", skipped)
	}
	byReason := map[string]*bulkSkipGroup{}
	for _, s := range res.Skipped {
		if !slices.Contains(bulkSkipReasons, s.Reason) {
			s.Reason = skipError
		}
		g := byReason[s.Reason]
		if g == nil {
			g = &bulkSkipGroup{Reason: tr("content.bulk.reason." + s.Reason)}
			byReason[s.Reason] = g
		}
		g.Count++
		title := tr("content.bulk.hidden_item")
		if it, err := h.svc.Get(ctx, s.ID); err == nil && it.Type == t.Slug {
			title = it.Title
			if title == "" {
				title = tr("content.list.untitled")
			}
		}
		g.Titles = append(g.Titles, title)
	}
	for _, reason := range bulkSkipReasons {
		if g := byReason[reason]; g != nil {
			out.Groups = append(out.Groups, *g)
		}
	}
	if res.More > 0 {
		out.Groups = append(out.Groups, bulkSkipGroup{Reason: tr("content.bulk.reason.more"), Count: res.More})
	}
	return out
}
