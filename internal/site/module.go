// Package site is the public front controller: it maps public URLs to
// content, terms and listings and renders them through the active theme
// (theme.Service). It also serves theme assets, sitemap.xml/robots.txt (via
// seo.Service) and the contact form.
package site

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/i18n"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/theme"
)

//go:embed catalog/*.json
var catalogs embed.FS

//go:embed migrations/*.sql
var migrations embed.FS

// Module is the site kernel.Module. It registers after redirects, so the
// redirects middleware (Router.Public.Use) already wraps these routes.
type Module struct{}

// New returns the site module.
func New() *Module { return &Module{} }

// Name implements kernel.Module.
func (*Module) Name() string { return "site" }

// Migrations implements kernel.Module (form_submissions).
func (*Module) Migrations() fs.FS { return migrations }

// Catalogs implements kernel.CatalogProvider.
func (*Module) Catalogs() fs.FS { return catalogs }

// KindFormResult is the theme kind rendered after a form submission.
const KindFormResult = "form-result"

// Register mounts the public routes. Specific routes come first; GET /*
// dispatches everything else (archives, singles, terms, pages, 404).
// Routes rendering forms pass through auth.CSRFMiddleware (on these routes
// only, never Public.Use: other modules' public POSTs have their own rules);
// POST /forms/contact first passes contactGuard.
func (*Module) Register(d *kernel.Deps) error {
	h := newHandlers(d)
	d.Theme.AddKind(KindFormResult, func(kernel.KindRequest) []string { return []string{"form-result.html"} })
	d.Hooks.Action(HookFormSubmitted, 10, h.deliverForm)
	registerSubmissionsAdmin(d, h)
	registerAvailabilityAdmin(d, h)
	registerPageAccessAdmin(d, h)
	registerIntakeAdmin(d, h)
	d.Theme.AddKind("private-intake", func(kernel.KindRequest) []string { return []string{"private-intake.html"} })

	pub := d.Router.Public
	pub.Use(h.maintenance)
	pub.With(auth.CSRFMiddleware()).Post("/_page/{id}/unlock", h.pageUnlock)
	// GET routes also answer HEAD (net/http drops the body).
	get := func(r chi.Router, pattern string, fn http.HandlerFunc) {
		r.Get(pattern, fn)
		r.Head(pattern, fn)
	}
	get(pub, "/sitemap.xml", h.sitemap)
	get(pub, "/robots.txt", h.robots)
	get(pub, "/themes/{slug}/assets/*", h.asset)
	get(pub, "/site/identity.css", h.identityCSS)
	get(pub, "/site/builder-identity.css", h.builderIdentityCSS)
	get(pub, theme.CoreCSSPath, h.coreCSS)

	// The site locale is unprefixed; another supported locale lives under /<locale>/.
	// CSRF is attached per route (not on the group) so the contact form can
	// rate-limit and cap its body before the CSRF check reads it.
	pages := pub.With(h.siteLocale)
	csrf := auth.CSRFMiddleware()
	mount := func(r chi.Router) {
		p := r.With(csrf)
		get(p, "/", h.home)
		get(p, "/search", h.search)
		r.With(h.contactGuard, csrf).Post("/forms/contact", h.contact)
		p.Post("/forms/comment", h.comment)
		get(p, "/reports/{form}", h.intakeForm)
		r.With(h.intakeGuard, csrf).Post("/reports/{form}", h.intakeSubmit)
		get(p, "/*", h.dispatch)
	}
	mount(pages)
	pages.Route("/{locale:^("+strings.Join(i18n.Supported, "|")+")$}", func(r chi.Router) {
		r.Use(h.localePrefix)
		mount(r)
	})
	return nil
}
