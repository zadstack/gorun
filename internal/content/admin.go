package content

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/httpx"
	"github.com/zadstack/gorun/internal/i18n"
	"github.com/zadstack/gorun/internal/kernel"
)

// adminHandlers serves the content and content-type admin screens.
type adminHandlers struct {
	d   *kernel.Deps
	svc *service
	tpl *httpx.Templates
}

// canPickMedia reports whether the editor can offer the media picker: the
// media module (booted after content, so looked up per request) is present
// and the user may browse media (media.upload guards /admin/x/media/picker).
func (h *adminHandlers) canPickMedia(ctx context.Context) bool {
	_, ok := kernel.Get[any](h.d, kernel.ServiceMedia)
	return ok && auth.Can(ctx, "media.upload")
}

// registerAdmin mounts the admin screens, adds the sidebar entries (a nav
// provider listing every public type per request, "Content Types") and the
// content_counts widget.
// Without the admin renderer (auth module absent) it does nothing.
func registerAdmin(d *kernel.Deps, svc *service) error {
	tpl, ok := kernel.Get[*httpx.Templates](d, kernel.ServiceAdminTemplates)
	if !ok {
		d.Log.Warn("content: admin templates unavailable; admin screens not mounted")
		return nil
	}
	h := &adminHandlers{d: d, svc: svc, tpl: tpl}
	d.Admin.AddNavProvider(h.typeNav)
	d.Admin.AddNav(kernel.NavItem{ID: "content.types", LabelKey: "content.nav.types", Icon: "shapes", Href: "/admin/types",
		Permission: "types.manage", Group: "structure", Position: 10})
	d.Admin.AddWidget("content_counts", h.countsWidget)
	d.Admin.AddNav(kernel.NavItem{ID: "content.comments", LabelKey: "content.nav.comments", Icon: "message-square", Href: "/admin/comments",
		Permission: PermModerateComments, Group: "content", Position: 60})

	r := d.Router.Admin
	view, create, edit, del := auth.RequirePerm("content.view"), auth.RequirePerm("content.create"), auth.RequirePerm("content.edit"), auth.RequirePerm("content.delete")
	r.With(view).Get("/content/{type}", h.list)
	r.With(create).Get("/content/{type}/new", h.newItem)
	r.With(create).Post("/content/{type}", h.createItem)
	r.With(del).Post("/content/{type}/bulk", h.bulk)
	r.With(view).Get("/content/{type}/{id}", h.editItem)
	r.With(edit).Post("/content/{type}/{id}", h.updateItem)
	r.With(del).Post("/content/{type}/{id}/trash", h.trashItem)
	r.With(del).Post("/content/{type}/{id}/delete", h.deleteItem)
	r.With(edit).Post("/content/{type}/{id}/revisions/{rev}/restore", h.restoreItem)
	r.With(create).Post("/content/{type}/{id}/translations", h.translateItem)
	r.With(edit).Post("/content/{type}/{id}/translations/unlink", h.unlinkItem)
	r.With(edit).Get("/x/content/{type}/row", h.repeaterRow)
	r.With(view).Get("/x/content/pick", h.pick)
	moderate := auth.RequirePerm(PermModerateComments)
	r.With(moderate).Get("/comments", h.commentsList)
	r.With(moderate).Post("/comments/{id}/status", h.commentStatus)
	r.With(moderate).Post("/comments/{id}/delete", h.commentDelete)

	manage := auth.RequirePerm("types.manage")
	r.With(manage).Get("/types", h.typesList)
	r.With(manage).Get("/types/new", h.typeNew)
	r.With(manage).Post("/types", h.typeCreate)
	r.With(manage).Get("/types/{slug}", h.typeEdit)
	r.With(manage).Post("/types/{slug}", h.typeUpdate)
	r.With(manage).Post("/types/{slug}/delete", h.typeDelete)
	return nil
}

// typeNav is the admin nav provider: one sidebar entry per public content
// type, evaluated per request so types created at runtime appear at once,
// labelled in the request (admin user's) locale.
func (h *adminHandlers) typeNav(ctx context.Context) []kernel.NavItem {
	types, err := h.svc.Types(ctx)
	if err != nil {
		h.d.Log.Error("content: nav types", "err", err)
		return nil
	}
	locale := i18n.Locale(ctx)
	out := make([]kernel.NavItem, 0, len(types))
	for _, t := range types {
		if !t.Public {
			continue
		}
		out = append(out, kernel.NavItem{ID: "content.type." + t.Slug, LabelKey: navLabel(t, locale), Icon: iconOr(t.Icon, "file-text"),
			Href: "/admin/content/" + t.Slug, Permission: "content.view", Group: "content", Position: 10 + t.MenuPosition})
	}
	return out
}

// navLabel returns the sidebar label: an i18n key for the system types, or
// the type's plural name in locale (the request locale) for custom types,
// which t passes through unchanged.
func navLabel(t Type, locale string) string {
	if t.System {
		return "content.nav.type." + t.Slug
	}
	return localized(t.Plural, locale, localized(t.Name, locale, t.Slug))
}

func iconOr(icon, def string) string {
	if icon == "" {
		return def
	}
	return icon
}

// localized picks m[locale], then en, then ar, then any value, then def.
func localized(m map[string]string, locale, def string) string {
	for _, k := range []string{locale, "en", "ar"} {
		if v := m[k]; v != "" {
			return v
		}
	}
	for _, v := range m {
		if v != "" {
			return v
		}
	}
	return def
}

func (h *adminHandlers) typeFromURL(w http.ResponseWriter, r *http.Request) (*Type, bool) {
	t, err := h.svc.GetType(r.Context(), chi.URLParam(r, "type"))
	if err != nil {
		h.fail(w, r, err)
		return nil, false
	}
	return t, true
}

// tr translates key for the request locale.
func (h *adminHandlers) tr(ctx context.Context, key string) string {
	if h.d.I18n == nil {
		return key
	}
	return h.d.I18n.T(i18n.Locale(ctx), key)
}

// fail renders the error page for err.
func (h *adminHandlers) fail(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, ErrNotFound):
		h.tpl.RenderError(w, r, http.StatusNotFound)
	case errors.Is(err, ErrForbidden):
		h.tpl.RenderError(w, r, http.StatusForbidden)
	case errors.Is(err, ErrConflict):
		h.tpl.RenderError(w, r, http.StatusConflict)
	default:
		h.d.Log.Error("content admin", "err", err, "path", r.URL.Path)
		h.tpl.RenderError(w, r, http.StatusInternalServerError)
	}
}

// pageLinks are list pagination URLs that keep the current filters.
type pageLinks struct {
	Page, Pages, Total int
	PrevURL, NextURL   string
}

func newPageLinks(base string, q url.Values, page, perPage, total int) pageLinks {
	p := httpx.NewPagination(page, perPage, total)
	link := func(n int) string {
		v := url.Values{}
		for k, vals := range q {
			v[k] = vals
		}
		v.Set("page", strconv.Itoa(n))
		return base + "?" + v.Encode()
	}
	out := pageLinks{Page: p.Page, Pages: p.Pages, Total: total}
	if p.HasPrev {
		out.PrevURL = link(p.Prev)
	}
	if p.HasNext {
		out.NextURL = link(p.Next)
	}
	return out
}
