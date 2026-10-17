package commerce

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/httpx"
)

func (h *adminHandlers) entitlements(w http.ResponseWriter, r *http.Request) {
	lq, lp := listParams(r, entitlementStatuses)
	list, total, err := h.svc.Entitlements(r.Context(), lq)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	lp.Page = httpx.NewPagination(lq.Page, adminPerPage, total)
	h.tpl.Render(w, r, "pages/commerce/entitlements.html", map[string]any{
		"Entitlements": list, "List": lp, "CanManage": auth.Can(r.Context(), PermManage), "Link": r.URL.Query().Get("link"),
	})
}

// entitlementAction is POST /commerce/entitlements/{id}/{suspend|resume|reconcile|login-link}.
func (h *adminHandlers) entitlementAction(w http.ResponseWriter, r *http.Request) {
	ctx, id := r.Context(), chi.URLParam(r, "id")
	var err error
	switch chi.URLParam(r, "action") {
	case "suspend":
		err = h.svc.Suspend(ctx, id)
	case "resume":
		err = h.svc.Resume(ctx, id)
	case "reconcile":
		err = h.svc.Reconcile(ctx, id)
	case "login-link":
		var link string
		if link, err = h.svc.LoginLink(ctx, id); err == nil {
			// Shown once to the admin (e.g. to help a customer); never stored.
			h.tpl.Render(w, r, "pages/commerce/login_link.html", map[string]any{"Link": link, "ID": id})
			return
		}
	default:
		h.tpl.RenderError(w, r, http.StatusNotFound)
		return
	}
	switch {
	case errors.Is(err, ErrNotFound):
		h.tpl.RenderError(w, r, http.StatusNotFound)
		return
	case errors.Is(err, ErrPeriodEnded):
		httpx.SetFlash(w, "error", "commerce.flash.period_ended")
	case errors.Is(err, ErrNotActive):
		httpx.SetFlash(w, "error", "commerce.flash.not_active")
	case err != nil:
		h.log.Warn("commerce: entitlement action", "id", id, "err", err)
		httpx.SetFlash(w, "error", "commerce.flash.server_error")
	default:
		httpx.SetFlash(w, "success", "commerce.flash.done")
	}
	http.Redirect(w, r, "/admin/commerce/entitlements", http.StatusSeeOther)
}
