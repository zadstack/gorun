package redirects

import (
	"net/http"
	"strings"
)

// skipPrefixes are never redirected (embedded admin assets).
var skipPrefixes = []string{"/static/admin/"}

// Middleware answers GET/HEAD requests whose path has an enabled redirect
// (exact, case-sensitive, trailing-slash-insensitive) and counts the hit.
// The original query string is kept unless the target has its own.
func Middleware(svc Service) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodGet && r.Method != http.MethodHead {
				next.ServeHTTP(w, r)
				return
			}
			for _, p := range skipPrefixes {
				if strings.HasPrefix(r.URL.Path, p) {
					next.ServeHTTP(w, r)
					return
				}
			}
			if Reserved(r.URL.Path) { // rows saved before the rule existed
				next.ServeHTTP(w, r)
				return
			}
			rd, ok := svc.Lookup(r.Context(), r.URL.Path)
			if !ok {
				next.ServeHTTP(w, r)
				return
			}
			if isLoop(lookupKey(r.URL.Path), rd.ToURL, r.Host) {
				next.ServeHTTP(w, r) // absolute target on this very host and path
				return
			}
			target := rd.ToURL
			if r.URL.RawQuery != "" && !strings.Contains(target, "?") {
				if i := strings.Index(target, "#"); i >= 0 {
					target = target[:i] + "?" + r.URL.RawQuery + target[i:]
				} else {
					target += "?" + r.URL.RawQuery
				}
			}
			svc.Hit(r.Context(), rd.ID)
			w.Header().Set("Cache-Control", "no-cache")
			http.Redirect(w, r, target, rd.Status)
		})
	}
}
