package site

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/i18n"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/taxonomy"
	"github.com/zadstack/gorun/internal/theme"
)

const (
	settingPerPage  = "site.posts_per_page"
	defaultPerPage  = 10
	maxPerPage      = 100
	maxPathSegments = 16
	formFlashCookie = "gp_form"
)

type handlers struct {
	unlock   unlockRate
	d        *kernel.Deps
	links    *theme.Linker
	limiter  *limiter
	comments *limiter // POST /forms/comment, apart from the contact form
	reports  *limiter
}

func newHandlers(d *kernel.Deps) *handlers {
	return &handlers{d: d, links: theme.NewLinker(d), limiter: newLimiter(contactRate, contactWindow),
		comments: newLimiter(contactRate, contactWindow), reports: newLimiter(contactRate, contactWindow)}
}

func (h *handlers) theme() (theme.Service, bool) {
	return kernel.Get[theme.Service](h.d, kernel.ServiceTheme)
}
func (h *handlers) content() (content.Service, bool) {
	return kernel.Get[content.Service](h.d, kernel.ServiceContent)
}
func (h *handlers) taxonomy() (taxonomy.Service, bool) {
	return kernel.Get[taxonomy.Service](h.d, kernel.ServiceTaxonomy)
}

// siteLocale returns the public site locale (setting site.locale, default
// "ar"); public pages never follow the admin user's language.
func (h *handlers) siteLocaleOf(ctx context.Context) string {
	return h.d.Settings.GetString(ctx, theme.SettingLocale, "ar")
}

// siteLocale puts the site locale into the request context.
func (h *handlers) siteLocale(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r.WithContext(i18n.WithLocale(r.Context(), h.siteLocaleOf(r.Context()))))
	})
}

// localePrefix serves /<locale>/… in that locale: the request continues with the locale in
// its context and the prefix cut off its path. The site locale's own prefix redirects (301) to
// the unprefixed URL.
func (h *handlers) localePrefix(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		loc, rest := i18n.SplitLocalePath(r.URL.Path)
		if loc == h.siteLocaleOf(r.Context()) {
			// Browsers interpret leading double slashes and backslashes as authorities.
			rest = "/" + strings.TrimLeft(strings.ReplaceAll(rest, "\\", "/"), "/")
			if r.URL.RawQuery != "" {
				rest += "?" + r.URL.RawQuery
			}
			http.Redirect(w, r, rest, http.StatusMovedPermanently)
			return
		}
		r2 := r.Clone(i18n.WithLocale(r.Context(), loc))
		r2.URL.Path, r2.URL.RawPath = rest, ""
		next.ServeHTTP(w, r2)
	})
}

// newContext builds the theme context for r (query params, form flash).
func (h *handlers) newContext(w http.ResponseWriter, r *http.Request) *theme.Context {
	q := map[string]string{}
	for k, v := range r.URL.Query() {
		if len(v) > 0 {
			q[k] = v[0]
		}
	}
	tc := &theme.Context{Request: r, Query: q, Locale: i18n.Locale(r.Context()), Extra: map[string]any{}}
	if c, err := r.Cookie(formFlashCookie); err == nil && (c.Value == "ok" || c.Value == "error") {
		tc.Extra["form_flash"] = c.Value
		http.SetCookie(w, &http.Cookie{Name: formFlashCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteLaxMode})
	}
	if c, err := r.Cookie(commentFlashCookie); err == nil && commentFlashes[c.Value] {
		tc.Extra["comment_flash"] = c.Value
		http.SetCookie(w, &http.Cookie{Name: commentFlashCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteLaxMode})
	}
	return tc
}

func (h *handlers) render(w http.ResponseWriter, r *http.Request, req kernel.KindRequest, tc *theme.Context) {
	if tc.Content != nil {
		var allowed bool
		w, allowed = h.pageGate(w, r, tc.Content)
		if !allowed {
			return
		}
		r = r.WithContext(content.WithDirectPageRead(r.Context(), tc.Content.ID))
		tc.Request = r
	}
	svc, ok := h.theme()
	if !ok {
		http.Error(w, http.StatusText(http.StatusServiceUnavailable), http.StatusServiceUnavailable)
		return
	}
	if tc.Status == 0 || tc.Status == http.StatusOK {
		tc.Alternates = h.alternates(r, tc)
	}
	// Render writes the 500 page itself on template errors (and logs).
	_ = svc.Render(r.Context(), w, req, tc)
}

func (h *handlers) visible(ctx context.Context, it *content.Item) bool {
	if it == nil {
		return false
	}
	svc, ok := h.content()
	if !ok {
		return false
	}
	typ, err := svc.GetType(ctx, it.Type)
	if err != nil || !typ.Public {
		return false
	}
	if it.Status == "published" {
		return true
	}
	u := auth.UserFromContext(ctx)
	return u != nil && auth.Can(ctx, "content.view") && (u.Role == "admin" || u.Role == "editor" || it.AuthorID == u.ID)
}

// bySlug finds a visible item of typ by slug: first in the exact site
// locale, then among items stored without a locale (""); never in another
// locale.
func (h *handlers) bySlug(ctx context.Context, typ, slug string) *content.Item {
	svc, ok := h.content()
	if !ok || slug == "" {
		return nil
	}
	it, err := svc.GetBySlug(ctx, typ, slug, i18n.Locale(ctx))
	if errors.Is(err, content.ErrNotFound) {
		it, err = svc.GetBySlug(ctx, typ, slug, "")
	}
	if err != nil || !h.visible(ctx, it) {
		return nil
	}
	return it
}

func pageParam(r *http.Request) int {
	n, err := strconv.Atoi(r.URL.Query().Get("page"))
	if err != nil || n < 1 {
		return 1
	}
	return n
}

func (h *handlers) perPage(ctx context.Context) int {
	var n int
	if err := h.d.Settings.Get(ctx, settingPerPage, &n); err != nil || n < 1 {
		return defaultPerPage
	}
	return min(n, maxPerPage)
}

// list runs a published listing query and fills tc.Items/Pagination.
func (h *handlers) list(ctx context.Context, r *http.Request, tc *theme.Context, q content.Query) error {
	svc, ok := h.content()
	if !ok {
		return nil
	}
	q.Status, q.Page, q.PerPage, q.PublicLocale = "published", pageParam(r), h.perPage(ctx), i18n.Locale(ctx)
	q.PublicOnly = true
	if len(q.Order) == 0 {
		q.Order = []content.Order{{By: "published_at", Dir: "desc"}}
	}
	res, err := svc.Query(ctx, q)
	if err != nil {
		return err
	}
	tc.Items = res.Items
	lib, _ := tc.Extra["library"].(*theme.Library)
	tc.Pagination = theme.PageURLs(theme.NewPagination(res.Page, res.PerPage, res.Total), r.URL, lib)
	return nil
}

// home renders the static front page (setting site.front_page) as a page,
// or the latest posts with the home kind.
func (h *handlers) home(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	tc := h.newContext(w, r)
	if it := h.frontPage(ctx); it != nil {
		h.renderItem(w, r, tc, it)
		return
	}
	if err := h.list(ctx, r, tc, content.Query{Type: "post"}); err != nil {
		h.serverError(w, r, tc, err)
		return
	}
	h.render(w, r, kernel.KindRequest{Kind: theme.KindHome}, tc)
}

// frontPage is the static front page (setting site.front_page) in the request locale: the page
// itself, or its translation; nil when there is none to show.
func (h *handlers) frontPage(ctx context.Context) *content.Item {
	id := h.d.Settings.GetString(ctx, theme.SettingFront, "")
	svc, ok := h.content()
	if id == "" || !ok {
		return nil
	}
	it, err := svc.Get(ctx, id)
	if err != nil {
		return nil
	}
	if loc := i18n.Locale(ctx); it.Locale != "" && it.Locale != loc {
		members, _ := svc.Translations(ctx, it.ID)
		it = nil
		for i := range members {
			if members[i].Locale == loc {
				it = &members[i]
			}
		}
	}
	if it == nil || !h.visible(ctx, it) {
		return nil
	}
	return it
}

// renderItem renders a page (kind page) or any other item (kind single).
func (h *handlers) renderItem(w http.ResponseWriter, r *http.Request, tc *theme.Context, it *content.Item) {
	tc.Content = it
	if it.Type == "page" {
		h.render(w, r, kernel.KindRequest{Kind: theme.KindPage, Type: it.Type, Slug: it.Slug, Template: it.Template, Purpose: theme.PagePurpose(it)}, tc)
		return
	}
	h.render(w, r, kernel.KindRequest{Kind: theme.KindSingle, Type: it.Type, Slug: it.Slug, Template: it.Template}, tc)
}

// search renders /search?q= over published content of public types.
func (h *handlers) search(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	tc := h.newContext(w, r)
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	loc := tc.Locale
	tc.Extra["title"] = h.d.I18n.T(loc, "site.search.empty_title")
	if q != "" {
		tc.Extra["title"] = h.d.I18n.T(loc, "site.search.title", q)
		if err := h.list(ctx, r, tc, content.Query{Search: q}); err != nil {
			h.serverError(w, r, tc, err)
			return
		}
		tc.Items = h.publicOnly(ctx, tc.Items)
	}
	h.render(w, r, kernel.KindRequest{Kind: theme.KindSearch}, tc)
}

// publicOnly drops items of non-public content types (search spans types).
func (h *handlers) publicOnly(ctx context.Context, items []content.Item) []content.Item {
	types := h.publicTypes(ctx)
	out := items[:0:0]
	for _, it := range items {
		if _, ok := types[it.Type]; ok {
			out = append(out, it)
		}
	}
	return out
}

func (h *handlers) publicTypes(ctx context.Context) map[string]content.Type {
	out := map[string]content.Type{}
	svc, ok := h.content()
	if !ok {
		return out
	}
	types, err := svc.Types(ctx)
	if err != nil {
		h.d.Log.Error("site: list content types", "err", err)
		return out
	}
	for _, t := range types {
		if t.Public {
			out[t.Slug] = t
		}
	}
	return out
}

func (h *handlers) serverError(w http.ResponseWriter, r *http.Request, tc *theme.Context, err error) {
	h.d.Log.Error("site: request failed", "err", err, "path", r.URL.Path)
	tc.Status = http.StatusInternalServerError
	h.render(w, r, kernel.KindRequest{Kind: theme.Kind500}, tc)
}
