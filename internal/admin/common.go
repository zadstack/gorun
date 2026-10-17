package admin

import (
	"bytes"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/httpx"
	"github.com/zadstack/gorun/internal/i18n"
)

const (
	defaultPerPage = 20
	maxPerPage     = 100
)

// pageParams parses page / per_page (defaults 1 / 20, max 100).
func pageParams(q url.Values) (page, perPage int) {
	page, _ = strconv.Atoi(q.Get("page"))
	perPage, _ = strconv.Atoi(q.Get("per_page"))
	if page < 1 {
		page = 1
	}
	if perPage < 1 {
		perPage = defaultPerPage
	}
	return page, min(perPage, maxPerPage)
}

// pager is the pagination block of list pages; URLs keep the filters.
type pager struct {
	Page, Pages, Total int
	PrevURL, NextURL   string
}

func newPager(base string, q url.Values, page, perPage, total int) pager {
	p := httpx.NewPagination(page, perPage, total)
	link := func(n int) string {
		v := url.Values{}
		for k, vals := range q {
			if k != "page" && len(vals) > 0 && vals[0] != "" {
				v[k] = vals[:1]
			}
		}
		v.Set("page", strconv.Itoa(n))
		return base + "?" + v.Encode()
	}
	out := pager{Page: p.Page, Pages: p.Pages, Total: total}
	if p.HasPrev {
		out.PrevURL = link(p.Prev)
	}
	if p.HasNext {
		out.NextURL = link(p.Next)
	}
	return out
}

// tr translates key for the request locale.
func (h *handlers) tr(r *http.Request, key string, args ...any) string {
	if h.d.I18n == nil {
		return key
	}
	return h.d.I18n.T(i18n.Locale(r.Context()), key, args...)
}

// translateFields turns i18n-key field errors into messages (API use).
func (h *handlers) translateFields(r *http.Request, fields map[string]string) map[string]string {
	out := make(map[string]string, len(fields))
	for k, v := range fields {
		out[k] = h.tr(r, v)
	}
	return out
}

func (h *handlers) serverError(w http.ResponseWriter, r *http.Request, err error) {
	h.d.Log.Error("admin: handler", "err", err, "path", httpx.RedactPath(r.URL.Path), "request_id", httpx.RequestIDFromContext(r.Context()))
	if strings.HasPrefix(r.URL.Path, "/api/") {
		httpx.Error(w, http.StatusInternalServerError, "internal", h.tr(r, "core.error.internal"), nil)
		return
	}
	h.tpl.RenderError(w, r, http.StatusInternalServerError)
}

// redirect answers a form POST: HX-Redirect for HTMX, else 303.
func redirect(w http.ResponseWriter, r *http.Request, to string) {
	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("HX-Redirect", to)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	http.Redirect(w, r, to, http.StatusSeeOther)
}

// fragment renders a page's "content" template into HTML (widgets).
func (h *handlers) fragment(r *http.Request, page string, data any) (template.HTML, error) {
	bw := &bufferWriter{header: http.Header{}}
	h.tpl.RenderWith(bw, r, http.StatusOK, "", page, data)
	if bw.status >= http.StatusBadRequest {
		return "", fmt.Errorf("admin: render %s: status %d", page, bw.status)
	}
	return template.HTML(bw.buf.String()), nil //nolint:gosec // output of html/template
}

// bufferWriter captures a rendered response.
type bufferWriter struct {
	header http.Header
	buf    bytes.Buffer
	status int
}

func (b *bufferWriter) Header() http.Header { return b.header }
func (b *bufferWriter) WriteHeader(status int) {
	if b.status == 0 {
		b.status = status
	}
}
func (b *bufferWriter) Write(p []byte) (int, error) {
	if b.status == 0 {
		b.status = http.StatusOK
	}
	return b.buf.Write(p)
}

// can wraps auth.Can for the request.
func can(r *http.Request, perm string) bool { return auth.Can(r.Context(), perm) }

// apiDeny writes the 403 envelope unless the principal holds perm.
func (h *handlers) apiDeny(w http.ResponseWriter, r *http.Request, perm string) bool {
	if auth.Can(r.Context(), perm) {
		return false
	}
	httpx.Error(w, http.StatusForbidden, "forbidden", h.tr(r, "core.error.forbidden"), nil)
	return true
}

func urlID(r *http.Request) string { return chi.URLParam(r, "id") }
