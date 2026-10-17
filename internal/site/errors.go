package site

import (
	"net/http"

	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/redirects"
	"github.com/zadstack/gorun/internal/theme"
)

// notFound consults redirects (the front controller checks redirects before
// 404, even if the redirects middleware did not match) and otherwise
// renders the theme's 404 kind with status 404.
func (h *handlers) notFound(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if svc, ok := kernel.Get[redirects.Service](h.d, kernel.ServiceRedirects); ok {
		if rd, found := svc.Lookup(ctx, r.URL.Path); found && rd != nil && rd.ToURL != "" {
			svc.Hit(ctx, rd.ID)
			status := rd.Status
			if status < 300 || status > 399 {
				status = http.StatusMovedPermanently
			}
			http.Redirect(w, r, rd.ToURL, status)
			return
		}
	}
	tc := h.newContext(w, r)
	tc.Status = http.StatusNotFound
	h.render(w, r, kernel.KindRequest{Kind: theme.Kind404}, tc)
}
