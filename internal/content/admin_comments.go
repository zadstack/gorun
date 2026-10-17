package content

import (
	"errors"
	"net/http"
	"slices"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/zadstack/gorun/internal/httpx"
)

// commentFlash is the flash shown after moving a comment to a status.
var commentFlash = map[string]string{CommentPending: "content.comments.flash_pending", CommentApproved: "content.comments.flash_approved",
	CommentSpam: "content.comments.flash_spam", CommentTrash: "content.comments.flash_trash"}

// commentsView is the data of Content → Comments.
type commentsView struct {
	Status   string
	Statuses []string
	Counts   map[string]int
	Comments []Comment
	Pages    pageLinks
}

// commentsList shows the comments of one status (pending by default), newest first.
func (h *adminHandlers) commentsList(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	status := q.Get("status")
	if !slices.Contains(CommentStatuses, status) {
		status = CommentPending
	}
	page, _ := strconv.Atoi(q.Get("page"))
	res, err := h.svc.Comments(r.Context(), CommentQuery{Status: status, Page: page, PerPage: adminPerPage})
	if err != nil {
		h.fail(w, r, err)
		return
	}
	counts, err := h.svc.CommentCounts(r.Context())
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.tpl.Render(w, r, "pages/content/comments.html", commentsView{Status: status, Statuses: CommentStatuses, Counts: counts,
		Comments: res.Items, Pages: newPageLinks("/admin/comments", q, res.Page, res.PerPage, res.Total)})
}

// commentStatus moves a comment to the posted status and returns to its list.
func (h *adminHandlers) commentStatus(w http.ResponseWriter, r *http.Request) {
	id, status := chi.URLParam(r, "id"), r.PostFormValue("status")
	c, err := h.svc.GetComment(r.Context(), id)
	if err == nil {
		err = h.svc.SetCommentStatus(r.Context(), id, status)
	}
	var ve *ValidationError
	switch {
	case errors.As(err, &ve):
		httpx.SetFlash(w, "error", "content.validation.comment_status")
	case err != nil:
		h.fail(w, r, err)
		return
	default:
		httpx.SetFlash(w, "success", commentFlash[status])
	}
	back := "/admin/comments"
	if c != nil {
		back += "?status=" + c.Status
	}
	http.Redirect(w, r, back, http.StatusSeeOther)
}

// commentDelete deletes a comment for good.
func (h *adminHandlers) commentDelete(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	c, err := h.svc.GetComment(r.Context(), id)
	if err == nil {
		err = h.svc.DeleteComment(r.Context(), id)
	}
	if err != nil {
		h.fail(w, r, err)
		return
	}
	httpx.SetFlash(w, "success", "content.comments.flash_deleted")
	http.Redirect(w, r, "/admin/comments?status="+c.Status, http.StatusSeeOther)
}
