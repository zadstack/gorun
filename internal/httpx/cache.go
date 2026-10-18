package httpx

import (
	"net/http"
	"path"
	"regexp"
)

// Cache-Control values for static assets.
const (
	CacheImmutable = "public, max-age=31536000, immutable"
	CacheShort     = "public, max-age=3600"
)

// versionedName matches a file name carrying a release version, e.g.
// htmx-org-2.0.11.js or tailwindcss-3.4.17.js (vendored, never rewritten).
var versionedName = regexp.MustCompile(`[-.@]v?\d+\.\d+\.\d+(?:[-.][0-9A-Za-z.]+)?\.[A-Za-z0-9]+$`)

// AssetCacheControl returns CacheImmutable for asset URLs whose content
// cannot change under the same URL: a ?v= cache-busting query, or a file
// name carrying its release version. Everything else gets CacheShort.
func AssetCacheControl(r *http.Request, name string) string {
	if r.URL.Query().Get("v") != "" || versionedName.MatchString(path.Base(name)) {
		return CacheImmutable
	}
	return CacheShort
}
