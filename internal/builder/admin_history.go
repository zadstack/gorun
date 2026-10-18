package builder

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"

	"github.com/go-chi/chi/v5"
	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/httpx"
	"github.com/zadstack/gorun/internal/i18n"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/theme"
)

type restorePage struct {
	ID        string
	Revision  LayoutRevision
	UpdatedAt string
	Summary   string
}

func (h *adminHandlers) revisionPreview(w http.ResponseWriter, r *http.Request) {
	item, err := h.svc.editable(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	rev, err := h.svc.GetRevision(r.Context(), item.ID, chi.URLParam(r, "rev"))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	html, err := Render(rev.Document)
	if err != nil {
		http.Error(w, "Invalid document", http.StatusUnprocessableEntity)
		return
	}
	previewItem := *item
	previewItem.Body = html
	options, themed, err := previewOptions(h.d, r.Context(), item.ID)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	if themed {
		renderer, ok := kernel.Get[interface {
			RenderBuilderThemePreview(context.Context, *content.Item, theme.BuilderPreviewOptions) (string, error)
		}](h.d, kernel.ServiceTheme)
		if ok {
			html, err = renderer.RenderBuilderThemePreview(r.Context(), &previewItem, options)
			if err != nil {
				h.d.Log.Error("builder: revision preview", "err", err)
				http.Error(w, "Preview unavailable", http.StatusUnprocessableEntity)
				return
			}
			writePreviewHTML(w, html)
			return
		}
	}
	if renderer, ok := kernel.Get[interface {
		RenderBuilderBody(context.Context, *content.Item) (string, error)
	}](h.d, kernel.ServiceTheme); ok {
		html, err = renderer.RenderBuilderBody(r.Context(), &previewItem)
		if err != nil {
			h.d.Log.Error("builder: revision preview", "err", err)
			http.Error(w, "Preview unavailable", http.StatusUnprocessableEntity)
			return
		}
	}
	locale := item.Locale
	if locale == "" {
		locale = h.d.Settings.GetString(r.Context(), "site.locale", i18n.DefaultLocale)
	}
	if !slices.Contains(i18n.Supported, locale) {
		locale = i18n.DefaultLocale
	}
	dir := "ltr"
	if locale == "ar" {
		dir = "rtl"
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; img-src 'self' https: data:; style-src 'unsafe-inline'; form-action 'none'; sandbox")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = fmt.Fprintf(w, `<!doctype html><html lang="%s" dir="%s"><meta charset="utf-8"><meta http-equiv="Content-Security-Policy" content="default-src 'none'; img-src https: http: data:; style-src 'unsafe-inline'; form-action 'none'"><meta name="viewport" content="width=device-width,initial-scale=1"><style>%s</style><body>%s</body></html>`, locale, dir, builderCSS+previewBrand(h.d, r.Context(), item.ID), html)
}

func writePreviewHTML(w http.ResponseWriter, html string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; img-src 'self' https: data:; style-src 'unsafe-inline'; font-src data:; form-action 'none'; base-uri 'none'; sandbox")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte(html))
}

func (h *adminHandlers) restoreForm(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	draft, err := h.svc.Load(r.Context(), id)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	rev, err := h.svc.GetRevision(r.Context(), id, chi.URLParam(r, "rev"))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.tpl.RenderWith(w, r, http.StatusOK, "admin", "pages/builder/restore.html", restorePage{
		ID: id, Revision: *rev, UpdatedAt: draft.UpdatedAt, Summary: rev.Summary,
	})
}

func (h *adminHandlers) restoreSubmit(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	revID := chi.URLParam(r, "rev")
	if err := r.ParseForm(); err != nil {
		http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
		return
	}
	expected := r.FormValue("updated_at")
	if _, err := h.svc.RestoreRevision(r.Context(), id, revID, expected); err != nil {
		if errors.Is(err, ErrConflict) {
			httpx.SetFlash(w, "error", "builder.history_conflict")
			http.Redirect(w, r, "/admin/builder/"+id, http.StatusSeeOther)
			return
		}
		if errors.Is(err, content.ErrNotFound) || errors.Is(err, ErrNotFound) {
			httpx.SetFlash(w, "error", "builder.history_missing")
			http.Redirect(w, r, "/admin/builder/"+id, http.StatusSeeOther)
			return
		}
		h.fail(w, r, err)
		return
	}
	httpx.SetFlash(w, "success", "builder.history_restored")
	http.Redirect(w, r, "/admin/builder/"+id, http.StatusSeeOther)
}
