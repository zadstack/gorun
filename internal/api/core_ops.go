package api

import (
	"net/http"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/menus"
	"github.com/zadstack/gorun/internal/taxonomy"
	"github.com/zadstack/gorun/internal/theme"
)

// SiteInfo is the site identity block of GET /site.
type SiteInfo struct {
	Title   string `json:"title"`
	Tagline string `json:"tagline"`
	BaseURL string `json:"base_url"`
	Locale  string `json:"locale" doc:"Default site locale (site.locale)"`
	Dir     string `json:"dir" enum:"rtl,ltr"`
}

// SiteStructure is GET /site: everything an agent or headless client needs
// to understand the site. Lists of absent modules are empty, never null.
type SiteStructure struct {
	Site       SiteInfo             `json:"site"`
	Types      []content.Type       `json:"types"`
	Taxonomies []taxonomy.Taxonomy  `json:"taxonomies"`
	Menus      []menus.Menu         `json:"menus"`
	Theme      *theme.Info          `json:"theme" doc:"Active theme (null when no theme module is installed)"`
	Templates  []theme.TemplateFile `json:"templates"`
}

// MeUser is the acting user in GET /me.
type MeUser struct {
	ID     string `json:"id"`
	Email  string `json:"email"`
	Name   string `json:"name"`
	Role   string `json:"role"`
	Locale string `json:"locale"`
}

// MeKey identifies the API key a request was made with.
type MeKey struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Me is GET /me: who is calling and what they may do.
type Me struct {
	Kind   string   `json:"kind" enum:"user,api_key"`
	User   MeUser   `json:"user"`
	APIKey *MeKey   `json:"api_key" doc:"Set when authenticated with an API key"`
	Scopes []string `json:"scopes" doc:"Effective permissions (role ∩ key scopes)"`
}

// Health is GET /health (the data /readyz checks).
type Health struct {
	Status   string `json:"status" enum:"ok"`
	Database string `json:"database" enum:"ok"`
}

func registerCoreOps(d *kernel.Deps) {
	d.API.Add(Op("GET", "/site", "Site structure: content types, taxonomies, menus, theme, templates",
		[]string{"site"}, []string{"content.view"}, nil, SiteStructure{}, siteHandler(d)))
	d.API.Add(Op("GET", "/me", "Current principal and effective scopes",
		[]string{"site"}, nil, nil, Me{}, meHandler(d)))
	d.API.Add(Op("GET", "/health", "Readiness: database reachable",
		[]string{"site"}, nil, nil, Health{}, healthHandler(d)))
}

func siteHandler(d *kernel.Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		locale := d.Settings.GetString(ctx, "site.locale", "ar")
		out := SiteStructure{
			Site: SiteInfo{
				Title:   d.Settings.GetString(ctx, "site.title", ""),
				Tagline: d.Settings.GetString(ctx, "site.tagline", ""),
				BaseURL: kernel.BaseURL(ctx, d),
				Locale:  locale,
				Dir:     "rtl",
			},
			Types: []content.Type{}, Taxonomies: []taxonomy.Taxonomy{}, Menus: []menus.Menu{}, Templates: []theme.TemplateFile{},
		}
		if d.I18n != nil {
			out.Site.Dir = d.I18n.Dir(locale)
		}
		if svc, ok := kernel.Get[content.Service](d, kernel.ServiceContent); ok {
			types, err := svc.Types(ctx)
			if err != nil {
				Fail(w, r, err)
				return
			}
			out.Types = nonNil(types)
		}
		if svc, ok := kernel.Get[taxonomy.Service](d, kernel.ServiceTaxonomy); ok {
			taxes, err := svc.Taxonomies(ctx)
			if err != nil {
				Fail(w, r, err)
				return
			}
			out.Taxonomies = nonNil(taxes)
		}
		if svc, ok := kernel.Get[menus.Service](d, kernel.ServiceMenus); ok {
			list, err := svc.List(ctx)
			if err != nil {
				Fail(w, r, err)
				return
			}
			out.Menus = nonNil(list)
		}
		if svc, ok := kernel.Get[theme.Service](d, kernel.ServiceTheme); ok {
			info := svc.Active(ctx)
			out.Theme = &info
			files, err := svc.Templates(ctx)
			if err != nil {
				Fail(w, r, err)
				return
			}
			out.Templates = nonNil(files)
		}
		WriteItem(w, out)
	}
}

func nonNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}

func meHandler(d *kernel.Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		u := auth.UserFromContext(ctx)
		if u == nil { // unreachable behind APIKeyMiddleware; kept for safety
			WriteError(w, http.StatusUnauthorized, CodeUnauthorized, "API key or session required", nil)
			return
		}
		me := Me{Kind: "user", User: MeUser{ID: u.ID, Email: u.Email, Name: u.Name, Role: u.Role, Locale: u.Locale}, Scopes: []string{}}
		if a := auth.ActorFromContext(ctx); a.Kind == "api_key" {
			me.Kind, me.APIKey = "api_key", &MeKey{ID: a.ID, Name: a.Name}
		}
		for _, p := range d.Perms.All() {
			if auth.Can(ctx, p.Name) {
				me.Scopes = append(me.Scopes, p.Name)
			}
		}
		WriteItem(w, me)
	}
}

func healthHandler(d *kernel.Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := d.DB.PingContext(r.Context()); err != nil {
			log().Error("api: health", "err", err)
			WriteError(w, http.StatusServiceUnavailable, "unavailable", "database unavailable", nil)
			return
		}
		WriteItem(w, Health{Status: "ok", Database: "ok"})
	}
}
