package site

import (
	"errors"
	"net/http"
	"strings"

	"github.com/zadstack/gorun/internal/audit"
	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/i18n"
	"github.com/zadstack/gorun/internal/kernel"
)

// commentFlashCookie carries the outcome of a comment back to the item (.Extra.comment_flash).
const commentFlashCookie = "gp_comment"

// commentFlashes are the outcomes a page may show.
var commentFlashes = map[string]bool{"pending": true, "approved": true, "invalid": true, "closed": true, "limited": true}

// comment handles POST /forms/comment (CSRF-checked by the route middleware): rate limit per
// IP, honeypot, then content.CommentService.SubmitComment, which checks the item is open for
// comments. Every outcome redirects back to the item's #comments with a flash.
func (h *handlers) comment(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	svc, ok := kernel.Get[content.CommentService](h.d, content.ServiceComments)
	if !ok {
		http.NotFound(w, r)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxFormBody)
	if err := r.ParseForm(); err != nil {
		http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
		return
	}
	if cs, ok := h.content(); ok {
		it, err := cs.Get(ctx, r.PostFormValue("content_id"))
		if err != nil || it.Status != "published" || !h.visible(ctx, it) {
			http.NotFound(w, r)
			return
		}
		locale := it.Locale
		if locale == "" {
			locale = h.siteLocaleOf(ctx)
		}
		r = r.WithContext(i18n.WithLocale(ctx, locale))
		ctx = r.Context()
		var allowed bool
		w, allowed = h.pageGate(w, r, it)
		if !allowed {
			return
		}
	}
	back := safeBack(r.PostFormValue("_back"), r.Referer())
	if !h.comments.allow(audit.IPFromContext(ctx)) {
		h.commentResult(w, r, back, "limited")
		return
	}
	if r.PostFormValue(honeypotField) != "" {
		h.commentResult(w, r, back, "pending")
		return
	}
	c, err := svc.SubmitComment(ctx, content.CommentInput{ContentID: r.PostFormValue("content_id"), ParentID: r.PostFormValue("parent_id"),
		AuthorName: r.PostFormValue("name"), AuthorEmail: r.PostFormValue("email"), Body: r.PostFormValue("body")})
	var ve *content.ValidationError
	switch {
	case errors.As(err, &ve) && ve.Fields["comment"] != "":
		h.commentResult(w, r, back, "closed")
	case errors.As(err, &ve):
		h.commentResult(w, r, back, "invalid")
	case err != nil:
		h.d.Log.Error("site: comment", "err", err)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
	default:
		h.commentResult(w, r, back, c.Status)
	}
}

// commentResult redirects to back#comments with outcome as the flash.
func (h *handlers) commentResult(w http.ResponseWriter, r *http.Request, back, outcome string) {
	http.SetCookie(w, &http.Cookie{Name: commentFlashCookie, Value: outcome, Path: "/", MaxAge: 120, HttpOnly: true, SameSite: http.SameSiteLaxMode})
	if i := strings.IndexByte(back, '#'); i >= 0 {
		back = back[:i]
	}
	http.Redirect(w, r, back+"#comments", http.StatusSeeOther)
}
