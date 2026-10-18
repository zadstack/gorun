// Package launch serves the public /start journey (customer onboarding chrome)
// and relies on launch overlay assets embedded under web/launch.
package launch

import (
	"embed"
	"html/template"
	"io/fs"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/i18n"
	"github.com/zadstack/gorun/internal/kernel"
)

//go:embed catalog/*.json templates/*
var files embed.FS

// Module is the launch kernel.Module.
type Module struct{}

// New returns the launch module.
func New() *Module { return &Module{} }

func (*Module) Name() string      { return "launch" }
func (*Module) Migrations() fs.FS { return nil }
func (*Module) Catalogs() fs.FS   { return files }

// Register mounts /start, /start/login and /start/verify.
func (*Module) Register(d *kernel.Deps) error {
	tpl, err := template.New("launch").Funcs(template.FuncMap{
		"t": func(locale, key string, args ...any) string {
			return d.I18n.T(locale, key, args...)
		},
	}).ParseFS(files, "templates/*.html")
	if err != nil {
		return err
	}
	h := &handlers{d: d, tpl: tpl}
	d.Router.Public.Group(func(r chi.Router) {
		r.Use(h.locale)
		r.Use(auth.CSRFMiddleware())
		r.Get("/start", h.startGet)
		r.Post("/start", h.startPost)
		r.Get("/start/login", h.loginGet)
		r.Post("/start/login", h.loginPost)
		r.Get("/start/verify", h.verifyGet)
	})
	return nil
}

type handlers struct {
	d   *kernel.Deps
	tpl *template.Template
}

func (h *handlers) locale(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		loc := h.d.Settings.GetString(r.Context(), "site.locale", i18n.DefaultLocale)
		if c, err := r.Cookie("gp_locale"); err == nil && containsLocale(c.Value) {
			loc = c.Value
		}
		if hl := r.URL.Query().Get("hl"); containsLocale(hl) {
			loc = hl
			http.SetCookie(w, &http.Cookie{
				Name: "gp_locale", Value: hl, Path: "/", MaxAge: 365 * 24 * 3600,
				HttpOnly: true, SameSite: http.SameSiteLaxMode,
				Secure: strings.HasPrefix(kernel.BaseURL(r.Context(), h.d), "https"),
			})
		}
		next.ServeHTTP(w, r.WithContext(i18n.WithLocale(r.Context(), loc)))
	})
}

func containsLocale(loc string) bool {
	for _, s := range i18n.Supported {
		if s == loc {
			return true
		}
	}
	return false
}

func (h *handlers) brand(r *http.Request) string {
	ctx := r.Context()
	if t := strings.TrimSpace(h.d.Settings.GetString(ctx, "site.title", "")); t != "" {
		return t
	}
	return h.d.I18n.T(i18n.Locale(ctx), "launch.brand.fallback")
}

func (h *handlers) tr(r *http.Request, key string, args ...any) string {
	return h.d.I18n.T(i18n.Locale(r.Context()), key, args...)
}

func (h *handlers) path(r *http.Request, suffix string) string {
	return suffix
}
