package pattern

import (
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/zadstack/gorun/internal/httpx"
	"github.com/zadstack/gorun/internal/kernel"
)

// registerPublicAssets serves /patterns/{slug}/assets/* from installed packages.
func registerPublicAssets(d *kernel.Deps, s *service) {
	pub := d.Router.Public
	h := func(w http.ResponseWriter, r *http.Request) {
		slug := chi.URLParam(r, "slug")
		rest := chi.URLParam(r, "*")
		if rest == "" || strings.HasSuffix(rest, "/") {
			http.NotFound(w, r)
			return
		}
		info, err := s.Lookup(slug)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		fsys := os.DirFS(filepath.Join(info.Dir, "assets"))
		clean := strings.TrimPrefix(filepath.ToSlash(filepath.Clean("/"+rest)), "/")
		st, err := fs.Stat(fsys, clean)
		if err != nil || st.IsDir() {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Cache-Control", httpx.AssetCacheControl(r, clean))
		http.ServeFileFS(w, r, fsys, clean)
	}
	pub.Get("/patterns/{slug}/assets/*", h)
	pub.Head("/patterns/{slug}/assets/*", h)
}
