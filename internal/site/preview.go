package site

import (
	"bytes"
	"html"
	"net/http"
	"slices"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/httpx"
	"github.com/zadstack/gorun/internal/i18n"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/theme"
)

// Preview links (docs/EDITING_SAFETY.md#shareable-preview-links): GET
// /preview/{token} renders the linked item through the active theme as if it
// were published, for anyone holding the link. Unknown, expired and revoked
// tokens all get the same 404 page.

const (
	previewRate   = 60 // requests per client IP and window
	previewWindow = time.Minute
)

func setPreviewHeaders(w http.ResponseWriter) {
	h := w.Header()
	h.Set("X-Robots-Tag", "noindex, nofollow")
	h.Set("Cache-Control", "private, no-store")
	h.Set("Referrer-Policy", "no-referrer")
}

func (h *handlers) preview(w http.ResponseWriter, r *http.Request) {
	setPreviewHeaders(w)
	if !h.previews.allow(httpx.ClientIP(r)) {
		w.Header().Set("Retry-After", "60")
		http.Error(w, http.StatusText(http.StatusTooManyRequests), http.StatusTooManyRequests)
		return
	}
	resolver, ok := kernel.Get[content.PreviewResolver](h.d, content.ServicePreview)
	var it *content.Item
	var err error
	if ok {
		it, err = resolver.ResolvePreview(r.Context(), chi.URLParam(r, "token"))
	}
	if ok && err == nil && !h.publicType(r, it) {
		it = nil
	}
	if !ok || err != nil || it == nil {
		h.previewNotFound(w, r)
		return
	}
	ctx := r.Context()
	if it.Locale != "" && slices.Contains(i18n.ContentLocales(ctx, h.d.Settings), it.Locale) {
		ctx = i18n.WithLocale(ctx, it.Locale)
	}
	ctx = content.WithDirectPageRead(ctx, it.ID)
	r = r.WithContext(ctx)
	// Rendered as the published page would be; the stored item is unchanged.
	shown := *it
	if shown.Status != "published" {
		shown.Status = "published"
		if shown.PublishedAt == "" {
			shown.PublishedAt = shown.PublishAt
		}
		if shown.PublishedAt == "" {
			shown.PublishedAt = shown.UpdatedAt
		}
	}
	svc, ok := h.theme()
	if !ok {
		http.Error(w, http.StatusText(http.StatusServiceUnavailable), http.StatusServiceUnavailable)
		return
	}
	tc := h.newContext(w, r)
	tc.Content = &shown
	tc.Extra["preview_link"] = true
	req := kernel.KindRequest{Kind: theme.KindSingle, Type: shown.Type, Slug: shown.Slug, Template: shown.Template}
	if shown.Type == "page" {
		req = kernel.KindRequest{Kind: theme.KindPage, Type: shown.Type, Slug: shown.Slug, Template: shown.Template, Purpose: theme.PagePurpose(&shown)}
	}
	buf := &bufferedWriter{header: http.Header{}}
	_ = svc.Render(ctx, buf, req, tc)
	for k, v := range buf.header {
		w.Header()[k] = v
	}
	setPreviewHeaders(w) // the theme may set its own cache headers
	w.Header().Del("Content-Length")
	status := buf.status
	if status == 0 {
		status = http.StatusOK
	}
	w.WriteHeader(status)
	_, _ = w.Write(injectRibbon(buf.body.Bytes(), h.ribbon(r)))
}

func (h *handlers) publicType(r *http.Request, it *content.Item) bool {
	svc, ok := h.content()
	if !ok || it == nil {
		return false
	}
	t, err := svc.GetType(r.Context(), it.Type)
	return err == nil && t.Public
}

// previewNotFound renders the theme's 404 page (no redirect lookup, so the
// answer never depends on the token).
func (h *handlers) previewNotFound(w http.ResponseWriter, r *http.Request) {
	tc := h.newContext(w, r)
	tc.Status = http.StatusNotFound
	if svc, ok := h.theme(); ok {
		_ = svc.Render(r.Context(), w, kernel.KindRequest{Kind: theme.Kind404}, tc)
		return
	}
	http.NotFound(w, r)
}

func (h *handlers) ribbon(r *http.Request) []byte {
	loc := i18n.Locale(r.Context())
	text, dir := "Preview", "ltr"
	if h.d.I18n != nil {
		text, dir = h.d.I18n.T(loc, "site.preview.ribbon"), h.d.I18n.Dir(loc)
	}
	return []byte(`<div role="note" data-gorun-preview dir="` + dir + `" lang="` + html.EscapeString(loc) + `" style="position:sticky;top:0;z-index:2147483647;` +
		`margin:0;padding:6px 12px;background:#20252b;color:#fff;font:600 13px/1.4 system-ui,sans-serif;text-align:center">` +
		html.EscapeString(text) + `</div>`)
}

// injectRibbon inserts ribbon right after the opening <body> tag (or at the
// start when there is none).
func injectRibbon(page, ribbon []byte) []byte {
	lower := bytes.ToLower(page)
	i := bytes.Index(lower, []byte("<body"))
	if i < 0 {
		return append(ribbon, page...)
	}
	j := bytes.IndexByte(page[i:], '>')
	if j < 0 {
		return append(ribbon, page...)
	}
	at := i + j + 1
	out := make([]byte, 0, len(page)+len(ribbon))
	out = append(out, page[:at]...)
	out = append(out, ribbon...)
	return append(out, page[at:]...)
}

// bufferedWriter collects a theme render so the ribbon can be added.
type bufferedWriter struct {
	header http.Header
	status int
	body   bytes.Buffer
}

func (b *bufferedWriter) Header() http.Header { return b.header }
func (b *bufferedWriter) WriteHeader(status int) {
	if b.status == 0 {
		b.status = status
	}
}
func (b *bufferedWriter) Write(p []byte) (int, error) {
	if b.status == 0 {
		b.status = http.StatusOK
	}
	return b.body.Write(p)
}
