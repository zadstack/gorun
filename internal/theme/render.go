package theme

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"os"
	"strings"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/httpx"
	"github.com/zadstack/gorun/internal/i18n"
	"github.com/zadstack/gorun/internal/kernel"
)

const (
	// Marker500 is present in every theme-engine 500 response body.
	Marker500 = "<!-- gorun:500 -->"
	// PreviewParam enables draft overrides for users with themes.manage.
	PreviewParam = "gp_preview_override"
	// HookRenderContext filters *Context before every render.
	HookRenderContext = "theme.render.context"
	// PermManage guards the appearance screens and API.
	PermManage = "themes.manage"

	maxIncludeDepth = 32
)

// compiled is one theme's parsed template set. Every template file is
// parsed on its own (parts and components into a shared base, each
// hierarchy template into a clone of it), so a broken file only breaks the
// pages that use it and still counts as existing for Resolve. Masters are
// never executed; each render clones one.
type compiled struct {
	slug  string
	chain []Info
	files map[string]source
	pages map[string]*template.Template // hierarchy templates
	base  *template.Template            // parts + components
	errs  map[string]error              // parse errors by name
	lang  map[string]map[string]string
}

func (c *compiled) exists(name string) bool { _, ok := c.files[name]; return ok }

func (s *service) compile(ctx context.Context, slug string, preview bool) (*compiled, error) {
	chain, err := loadChain(s.roots(), slug)
	if err != nil {
		return nil, err
	}
	c := &compiled{slug: slug, chain: chain, files: collect(s.layers(slug, chain, preview)),
		pages: map[string]*template.Template{}, errs: map[string]error{}, lang: loadLang(chain)}
	stubs := s.funcMap(&reqState{ctx: ctx, tc: &Context{}, set: c})
	c.base = template.New("gorun").Funcs(stubs)
	for name, f := range c.files {
		if !isPage(name) {
			if err := parseInto(c.base, f); err != nil {
				c.errs[name] = err
			}
		}
	}
	for name, f := range c.files {
		if !isPage(name) {
			continue
		}
		t, err := c.base.Clone()
		if err != nil {
			return nil, fmt.Errorf("theme: clone base: %w", err)
		}
		if err := parseInto(t, f); err != nil {
			c.errs[name] = err
			continue
		}
		c.pages[name] = t
	}
	return c, nil
}

// isPage reports a hierarchy template (templates/ dir) name.
func isPage(name string) bool {
	return !strings.HasPrefix(name, "parts.") && !strings.HasPrefix(name, "components.")
}

func parseInto(t *template.Template, f source) error {
	src, err := os.ReadFile(f.path)
	if err != nil {
		return fmt.Errorf("theme: read %s: %w", f.name, err)
	}
	if _, err := t.New(f.name).Parse(string(src)); err != nil {
		return fmt.Errorf("theme: parse %s (%s): %w", f.name, f.origin, err)
	}
	return nil
}

// Render fills tc's defaults, applies the theme.render.context filter,
// resolves req and executes the template into a buffer. On any template
// error it logs the error and writes the 500 page (templates/500.html, else
// a minimal page containing Marker500) instead; the returned error then
// reports the original failure, and the response is already written.
func (s *service) Render(ctx context.Context, w http.ResponseWriter, req kernel.KindRequest, tc *Context) error {
	if tc == nil {
		tc = &Context{}
	}
	slug := s.activeSlug(ctx)
	preview := s.previewing(ctx, tc)
	if preview {
		w.Header().Set("Cache-Control", "private, no-store") // drafts must never be cached
		w.Header().Set("Content-Security-Policy", "sandbox; default-src 'none'; script-src 'none'; style-src 'self' 'unsafe-inline'; font-src 'self' data:; img-src 'self' data: blob:; frame-ancestors 'self'; form-action 'none'; base-uri 'none'")
	}
	set, setErr := s.set(ctx, slug, preview)
	if tc.Theme.Slug == "" {
		tc.Theme = Info{Slug: slug}
		if setErr == nil {
			tc.Theme = set.chain[0]
		}
	}
	tc = s.prepare(ctx, req, tc)
	if setErr != nil {
		return s.fail(ctx, w, nil, tc, setErr)
	}
	name, err := resolve(req, s.d.Theme, set.exists)
	if err != nil {
		return s.fail(ctx, w, set, tc, err)
	}
	buf, err := s.execute(ctx, set, name, tc)
	if err != nil {
		return s.fail(ctx, w, set, tc, err)
	}
	writeHTML(w, tc.Status, buf.Bytes())
	return nil
}

func writeHTML(w http.ResponseWriter, status int, body []byte) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if status == 0 {
		status = http.StatusOK
	}
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

// previewing reports a draft-override preview: ?gp_preview_override=1 from
// a user holding themes.manage.
func (s *service) previewing(ctx context.Context, tc *Context) bool {
	return tc.Request != nil && tc.Request.URL.Query().Get(PreviewParam) == "1" && auth.Can(ctx, PermManage)
}

// prepare fills the context defaults and runs the theme.render.context filter.
func (s *service) prepare(ctx context.Context, req kernel.KindRequest, tc *Context) *Context {
	tc.Site = s.siteInfo(ctx)
	if tc.Locale == "" {
		tc.Locale = tc.Site.Locale
	}
	tc.Dir = "ltr"
	if s.d.I18n != nil {
		tc.Dir = s.d.I18n.Dir(tc.Locale)
	}
	if tc.Theme.Slug == "" {
		tc.Theme = s.Active(ctx)
	}
	tc.Site.Theme = tc.Theme.Slug
	if tc.Kind == "" {
		tc.Kind = req.Kind
	}
	if tc.Status == 0 {
		tc.Status = http.StatusOK
	}
	if tc.User == nil {
		tc.User = auth.UserFromContext(ctx)
	}
	if tc.Extra == nil {
		tc.Extra = map[string]any{}
	}
	if tc.Query == nil {
		tc.Query = map[string]string{}
	}
	if s.d.Hooks != nil {
		v, err := s.d.Hooks.Apply(ctx, HookRenderContext, tc)
		if err != nil {
			s.log.Error("theme: render context filter", "err", err)
		}
		if next, ok := v.(*Context); ok && next != nil {
			tc = next
		}
	}
	return tc
}

// siteInfo reads the site settings; the public site locale is site.locale
// (default "ar"), independent of any admin user's preference.
func (s *service) siteInfo(ctx context.Context) SiteInfo {
	get := func(key, def string) string {
		if s.d.Settings == nil {
			return def
		}
		return s.d.Settings.GetString(ctx, key, def)
	}
	locale := get(SettingLocale, defaultSiteLocale)
	title := "GoRun"
	if s.d.I18n != nil {
		title = s.d.I18n.T(locale, "core.app.name")
	}
	info := SiteInfo{Title: get(SettingTitle, title), Tagline: get(SettingTagline, ""),
		BaseURL: kernel.BaseURL(ctx, s.d), Locale: locale,
		Dir: "ltr"}
	if s.d.I18n != nil {
		info.Dir = s.d.I18n.Dir(locale)
	}
	return info
}

// execute clones the master for name, binds request funcs and renders it
// into a buffer.
func (s *service) execute(ctx context.Context, set *compiled, name string, tc *Context) (*bytes.Buffer, error) {
	if err := set.errs[name]; err != nil {
		return nil, err
	}
	master, ok := set.pages[name]
	if !ok {
		return nil, fmt.Errorf("%w: template %q", ErrNotFound, name)
	}
	s.cloneMu.Lock()
	t, err := master.Clone()
	s.cloneMu.Unlock()
	if err != nil {
		return nil, fmt.Errorf("theme: clone %s: %w", name, err)
	}
	st := &reqState{ctx: ctx, tc: tc, set: set, tmpl: t}
	t.Funcs(s.funcMap(st))
	var buf bytes.Buffer
	if err := t.ExecuteTemplate(&buf, name, tc); err != nil {
		return nil, fmt.Errorf("theme: execute %s: %w", name, err)
	}
	return &buf, nil
}

// fail logs cause and writes the 500 page; it returns cause.
func (s *service) fail(ctx context.Context, w http.ResponseWriter, set *compiled, tc *Context, cause error) error {
	path := ""
	if tc.Request != nil {
		path = httpx.RedactPath(tc.Request.URL.Path)
	}
	s.log.Error("theme: render failed", "err", cause, "theme", tc.Theme.Slug, "kind", tc.Kind, "path", path,
		"request_id", httpx.RequestIDFromContext(ctx))
	tc.Status, tc.Kind = http.StatusInternalServerError, Kind500
	if set != nil && set.exists("500.html") {
		buf, err := s.execute(ctx, set, "500.html", tc)
		if err == nil {
			// html/template strips comments, so the marker is appended
			// (a trailing comment after </html> is valid HTML).
			buf.WriteString("\n" + Marker500 + "\n")
			writeHTML(w, http.StatusInternalServerError, buf.Bytes())
			return cause
		}
		s.log.Error("theme: 500 template failed", "err", err, "theme", tc.Theme.Slug)
	}
	writeHTML(w, http.StatusInternalServerError, []byte(s.inline500(tc)))
	return cause
}

// inline500 is the last-resort error page (no theme involved).
func (s *service) inline500(tc *Context) string {
	locale := tc.Locale
	if locale == "" {
		locale = i18n.DefaultLocale
	}
	title, body, dir := "Server error", "Something went wrong. Please try again.", "ltr"
	if s.d.I18n != nil {
		title = s.d.I18n.T(locale, "core.error.internal_title")
		body = s.d.I18n.T(locale, "core.error.internal")
		dir = s.d.I18n.Dir(locale)
	}
	esc := template.HTMLEscapeString
	return `<!doctype html><html lang="` + esc(locale) + `" dir="` + dir + `"><head><meta charset="utf-8">` +
		`<meta name="viewport" content="width=device-width, initial-scale=1"><meta name="robots" content="noindex">` +
		`<title>` + esc(title) + `</title></head><body style="font-family:system-ui,sans-serif;background:#f3f5f2;color:#141816;padding:48px 16px;text-align:center">` +
		Marker500 + `<h1 style="font-size:22px">` + esc(title) + `</h1><p style="color:#637069">` + esc(body) + `</p></body></html>`
}

// errDepth stops runaway part/component recursion.
var errDepth = errors.New("theme: part/component nesting too deep")
