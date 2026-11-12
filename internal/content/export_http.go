package content

import (
	"errors"
	"mime"
	"net/http"
	"net/url"
	"time"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/httpx"
	"github.com/zadstack/gorun/internal/kernel"
)

// exportResponse sets the attachment headers on the first write, so an export refused before
// anything streams can still answer with an ordinary error.
type exportResponse struct {
	w       http.ResponseWriter
	format  string
	name    string
	started bool
}

func (e *exportResponse) Write(p []byte) (int, error) {
	if !e.started {
		e.started = true
		h := e.w.Header()
		if e.format == "csv" {
			h.Set("Content-Type", "text/csv; charset=utf-8")
		} else {
			h.Set("Content-Type", "application/json; charset=utf-8")
		}
		h.Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": e.name}))
		h.Set("Cache-Control", "private, no-store")
		h.Set("X-Content-Type-Options", "nosniff")
		e.w.WriteHeader(http.StatusOK)
	}
	return e.w.Write(p)
}

// exportFilename is content-<type|all>-<UTC time>.<format>.
func exportFilename(o ExportOptions, now time.Time) string {
	typ := o.Type
	if typ == "" {
		typ = "all"
	}
	return "content-" + typ + "-" + now.UTC().Format("20060102-150405") + "." + o.Format
}

// serveExport streams the export as an attachment and audits it (who, via, filters, count;
// never contents). It returns an error only when nothing was written yet, for the caller to
// answer; a failure mid-stream is logged and leaves the download truncated.
func (s *service) serveExport(w http.ResponseWriter, r *http.Request, d *kernel.Deps, opts ExportOptions, via string) error {
	ctx := r.Context()
	opts.BaseURL = kernel.BaseURL(ctx, d)
	rc := http.NewResponseController(w)
	opts.Flush = func() { _ = rc.Flush() }
	out := &exportResponse{w: w, format: opts.Format, name: exportFilename(opts, s.now())}
	res, err := s.Export(ctx, out, opts)
	if err != nil && !out.started {
		return err
	}
	if err != nil {
		s.log.Error("content: export interrupted", "via", via, "count", res.Count, "err", err)
	}
	s.audit.Record(ctx, "content.export", "content", opts.Type, map[string]any{"via": via, "format": out.format,
		"filters": opts, "count": res.Count, "next_after": res.NextAfter, "complete": err == nil})
	return nil
}

// exportContent is GET /v1/content/export.
func (a *api) exportContent(w http.ResponseWriter, r *http.Request) {
	if !a.allow(w, r, "content.view") {
		return
	}
	opts, err := ParseExportOptions(r.URL.Query())
	if err == nil {
		err = a.svc.serveExport(w, r, a.d, opts, "api")
	}
	if err != nil {
		a.fail(w, r, err)
	}
}

// exportList is GET /admin/content/{type}/export: the list's current filters (search, status,
// language, author, view) as a download. Authors and contributors export their own items, like
// the list shows them.
func (h *adminHandlers) exportList(w http.ResponseWriter, r *http.Request) {
	t, ok := h.typeFromURL(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	qs := r.URL.Query()
	v := url.Values{"type": {t.Slug}, "format": {qs.Get("format")}, "search": {qs.Get("search")}, "status": {qs.Get("status")},
		"locale": {qs.Get("locale")}, "author": {qs.Get("author")}}
	switch qs.Get("view") {
	case "mine":
		v.Set("status", "draft")
		v.Set("author", auth.UserFromContext(ctx).ID)
	case "review":
		v.Set("status", "pending")
	}
	if u := auth.UserFromContext(ctx); ownOnly(u) {
		v.Set("author", u.ID)
	}
	opts, err := ParseExportOptions(v)
	if err == nil {
		err = h.svc.serveExport(w, r, h.d, opts, "admin")
	}
	if err == nil {
		return
	}
	var ve *ValidationError
	if !errors.As(err, &ve) {
		h.fail(w, r, err)
		return
	}
	msg := "content.flash.invalid"
	for _, m := range ve.Fields {
		msg = m
		break
	}
	httpx.SetFlash(w, "error", msg)
	back := "/admin/content/" + t.Slug
	if raw := r.URL.Query(); len(raw) > 0 {
		raw.Del("format")
		back += "?" + raw.Encode()
	}
	http.Redirect(w, r, back, http.StatusSeeOther)
}
