package site

import (
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/zadstack/gorun/internal/httpx"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/seo"
	"github.com/zadstack/gorun/internal/theme"
)

// asset serves /themes/{slug}/assets/* from the installed theme's assets
// directory only (no directory listings, no traversal: the file server is
// rooted at assets/ and cleans the path).
func (h *handlers) asset(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r, "slug")
	rest := chi.URLParam(r, "*")
	svc, ok := h.theme()
	if !ok || rest == "" || strings.HasSuffix(rest, "/") {
		http.NotFound(w, r)
		return
	}
	dir, ok := themeDir(r, svc, slug)
	if !ok {
		http.NotFound(w, r)
		return
	}
	fsys := os.DirFS(filepath.Join(dir, "assets"))
	clean := strings.TrimPrefix(filepath.ToSlash(filepath.Clean("/"+rest)), "/")
	st, err := fs.Stat(fsys, clean)
	if err != nil || st.IsDir() {
		http.NotFound(w, r)
		return
	}
	// Public fonts must also load in opaque-origin, script-free previews.
	switch strings.ToLower(filepath.Ext(clean)) {
	case ".woff", ".woff2", ".ttf", ".otf":
		w.Header().Set("Access-Control-Allow-Origin", "*")
	}
	w.Header().Set("Cache-Control", httpx.AssetCacheControl(r, clean)) // ?v= links are immutable
	http.ServeFileFS(w, r, fsys, clean)
}

// themeDir returns the directory of installed theme slug (one theme.json
// read via theme.InfoLookup; listing every theme is the fallback).
func themeDir(r *http.Request, svc theme.Service, slug string) (string, bool) {
	if l, ok := svc.(theme.InfoLookup); ok {
		info, err := l.Lookup(slug)
		return info.Dir, err == nil
	}
	list, err := svc.List(r.Context())
	if err != nil {
		return "", false
	}
	for _, t := range list {
		if t.Slug == slug {
			return t.Dir, true
		}
	}
	return "", false
}

func (h *handlers) sitemap(w http.ResponseWriter, r *http.Request) {
	svc, ok := kernel.Get[seo.Service](h.d, kernel.ServiceSEO)
	if !ok {
		http.NotFound(w, r)
		return
	}
	b, err := svc.Sitemap(r.Context())
	if err != nil {
		h.d.Log.Error("site: sitemap", "err", err)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	_, _ = w.Write(b)
}

func (h *handlers) robots(w http.ResponseWriter, r *http.Request) {
	svc, ok := kernel.Get[seo.Service](h.d, kernel.ServiceSEO)
	body := "User-agent: *\nAllow: /\n"
	if ok {
		body = svc.Robots(r.Context())
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte(body))
}
