package site

import (
	"net/http"

	"github.com/zadstack/gorun/internal/brand"
	"github.com/zadstack/gorun/internal/httpx"
	"github.com/zadstack/gorun/internal/theme"
)

// identityCSS serves the active theme's site-identity stylesheet as a
// cacheable asset (linked from {{identity .}}).
func (h *handlers) identityCSS(w http.ResponseWriter, r *http.Request) {
	svc, ok := h.theme()
	if !ok || h.d.Settings == nil {
		http.NotFound(w, r)
		return
	}
	info := svc.Active(r.Context())
	if !theme.SupportsIdentity(info) {
		http.NotFound(w, r)
		return
	}
	slug := info.Slug
	css := theme.IdentityStylesheet(func(key string) string {
		return h.d.Settings.GetString(r.Context(), theme.SettingKey(slug, key), "")
	})
	if css == "" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/css; charset=utf-8")
	w.Header().Set("Cache-Control", httpx.AssetCacheControl(r, "identity.css"))
	_, _ = w.Write([]byte(css))
}

// builderIdentityCSS serves builder brand tokens as a cacheable asset
// (linked from {{builderIdentity}}).
func (h *handlers) builderIdentityCSS(w http.ResponseWriter, r *http.Request) {
	if h.d.Settings == nil {
		http.NotFound(w, r)
		return
	}
	var identity brand.Identity
	if err := h.d.Settings.Get(r.Context(), "builder.identity", &identity); err != nil {
		http.NotFound(w, r)
		return
	}
	css := theme.BuilderIdentityStylesheet(identity)
	if css == "" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/css; charset=utf-8")
	w.Header().Set("Cache-Control", httpx.AssetCacheControl(r, "builder-identity.css"))
	_, _ = w.Write([]byte(css))
}
