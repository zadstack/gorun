package media

import (
	"errors"
	"io"
	"io/fs"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
)

// serveHandler serves stored files with immutable cache headers (storage
// keys are random and never reused). SVG gets a sandboxing CSP so a file
// opened directly can never run script.
func serveHandler(storage Storage) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		key := chi.URLParam(r, "*")
		if !keyPattern.MatchString(key) {
			http.NotFound(w, r)
			return
		}
		rc, err := storage.Open(r.Context(), key)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) || errors.Is(err, ErrInvalidKey) {
				http.NotFound(w, r)
				return
			}
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return
		}
		defer rc.Close()
		ct := contentTypeForKey(key)
		h := w.Header()
		h.Set("Content-Type", ct)
		h.Set("Cache-Control", "public, max-age=31536000, immutable")
		h.Set("X-Content-Type-Options", "nosniff")
		if strings.HasPrefix(ct, "image/svg+xml") {
			h.Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; img-src data:; sandbox")
		}
		if f, ok := rc.(*os.File); ok {
			modTime := time.Time{}
			if st, err := f.Stat(); err == nil {
				modTime = st.ModTime()
			}
			http.ServeContent(w, r, "", modTime, f)
			return
		}
		if rs, ok := rc.(io.ReadSeeker); ok {
			http.ServeContent(w, r, "", time.Time{}, rs)
			return
		}
		_, _ = io.Copy(w, rc)
	}
}
