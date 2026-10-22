package theme

import (
	"context"
	"net/http"

	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/kernel"
)

// IdentityPreviewOptions describes one identity preview render.
type IdentityPreviewOptions struct {
	View     string // "home" or "single" (newest published post; home when none)
	Locale   string
	Snapshot IdentitySnapshot
}

// identityPreviewCSP keeps the preview inert inside the admin iframe: no
// scripts, no navigation out, framed by this origin only.
// allow-same-origin keeps the document's origin so bundled fonts and media
// load; scripts stay blocked by script-src.
const identityPreviewCSP = "sandbox allow-same-origin; default-src 'none'; script-src 'none'; style-src 'self' 'unsafe-inline' https://fonts.googleapis.com; font-src 'self' data: https://fonts.gstatic.com; img-src 'self' data: blob: https:; frame-ancestors 'self'; form-action 'none'; base-uri 'none'"

// RenderIdentityPreview renders the active theme with o.Snapshot replacing
// the stored identity, logos, title and tagline for this request only.
// Nothing is persisted or cached.
func (s *service) RenderIdentityPreview(ctx context.Context, w http.ResponseWriter, r *http.Request, o IdentityPreviewOptions) error {
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("Content-Security-Policy", identityPreviewCSP)
	w.Header().Set("X-Frame-Options", "SAMEORIGIN") // the admin page frames it; /admin defaults to DENY
	req := kernel.KindRequest{Kind: KindHome}
	tc := &Context{Request: r, Locale: o.Locale, Extra: map[string]any{}, Query: map[string]string{}}
	if o.View == "single" {
		if it := s.newestPost(ctx, o.Locale); it != nil {
			req = kernel.KindRequest{Kind: KindSingle, Type: it.Type, Slug: it.Slug}
			tc.Content = it
		}
	}
	slug := s.activeSlug(ctx)
	set, err := s.set(ctx, slug, false)
	if err != nil {
		return s.fail(ctx, w, nil, s.prepare(ctx, req, tc), err)
	}
	tc.Theme = set.chain[0]
	tc = s.prepare(ctx, req, tc)
	if v := o.Snapshot.Site["site.title"]; v != "" {
		tc.Site.Title = v
	}
	tc.Site.Tagline = o.Snapshot.Site["site.tagline"]
	name, err := resolve(req, s.d.Theme, set.exists)
	if err != nil {
		return s.fail(ctx, w, set, tc, err)
	}
	override := o.Snapshot
	override.Theme = slug
	buf, err := s.executeWith(ctx, set, name, tc, func(st *reqState) { st.identityOverride = &override })
	if err != nil {
		return s.fail(ctx, w, set, tc, err)
	}
	writeHTML(w, tc.Status, buf.Bytes())
	return nil
}

// newestPost returns the most recent published post in locale (any locale
// when none matches), or nil.
func (s *service) newestPost(ctx context.Context, locale string) *content.Item {
	cs, ok := kernel.Get[content.Service](s.d, kernel.ServiceContent)
	if !ok {
		return nil
	}
	for _, loc := range []string{locale, ""} {
		page, err := cs.Query(ctx, content.Query{Type: "post", Status: "published", PublicOnly: true, Locale: loc, PerPage: 1, Page: 1, Order: []content.Order{{By: "published_at", Dir: "desc"}}})
		if err == nil && len(page.Items) > 0 {
			it := page.Items[0]
			return &it
		}
	}
	return nil
}
