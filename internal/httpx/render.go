package httpx

import (
	"bytes"
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/zadstack/gorun/internal/i18n"
)

// Template layout (inside the FS given to NewTemplates):
//
//	admin/templates/base.html          shared defines ("head", "scripts")
//	admin/templates/layouts/<n>.html   full documents; call {{template "content" .}}
//	admin/templates/partials/**.html   {{define "partials/<name>"}} blocks
//	admin/templates/pages/**.html      {{define "title"}} + {{define "content"}}
//	admin/static/*                     served at /static/admin/ (see asset func)
//
// Every file is parsed under its path relative to admin/templates. A page is
// executed through layouts/<layout>.html, or only its "content" template for
// HTMX requests (HX-Request: true).
const (
	templatesDir = "admin/templates"
	staticDir    = "admin/static"
	// StaticPrefix is where admin/static is served.
	StaticPrefix = "/static/admin/"
	// LaunchCSSPath is the public URL for the embedded launch overlay stylesheet.
	// Downstream /start pages and prior Traefik mounts use this exact path.
	LaunchCSSPath = "/static/launch.css"
	flashCookie   = "gp_flash"
)

// NavItem mirrors NavItem field for field, so callers convert with
// httpx.NavItem(k). httpx cannot import kernel: kernel installs httpx's
// middleware on the root mux.
type NavItem struct {
	ID, LabelKey, Icon, Href, Permission, Group string
	Position                                    int
}

// Funcs supplies request-scoped data to the template funcs. Any may be nil.
type Funcs struct {
	NavGroupOrder func(ctx context.Context) []string
	Nav           func(ctx context.Context) []NavItem // already permission-filtered and sorted
	AllNav        func(ctx context.Context) []NavItem // authorized items before personal visibility preferences
	User          func(ctx context.Context) any
	CSRF          func(ctx context.Context) string
	Setting       func(ctx context.Context, key string) string
}

// NavGroup is one sidebar section returned by the nav template func. Label
// is LabelKey translated for the request locale; for a group without a
// catalog entry it is the key's last segment title-cased ("field_kit" →
// "Field Kit"), never the raw key.
type NavGroup struct {
	Key, LabelKey, Label string
	Items                []NavItem
}

// navGroupOrder fixes the sidebar order; "" (ungrouped, e.g. Dashboard)
// comes first, unknown groups last in alphabetical order.
var navGroupOrder = []string{"", "content", "inbox", "structure", "appearance", "commerce", "agents", "settings", "integrations", "system"}

// Pagination is returned by the pagination template func.
type Pagination struct {
	Page, PerPage, Total, Pages, Prev, Next int
	HasPrev, HasNext                        bool
}

// Flash is a one-shot message set by SetFlash; Message is an i18n key.
type Flash struct{ Kind, Message string }

// Templates renders admin pages. In dev every request re-parses the FS when a
// source can change on disk; in prod, or when every source is embedded,
// everything is parsed once by NewTemplates.
type Templates struct {
	fsys       fs.FS
	extensions []fs.FS
	dev        bool
	i18n       *i18n.Bundle
	funcs      Funcs
	brand      func(context.Context) SidebarBrand
	log        *slog.Logger

	mu     sync.Mutex
	cached *parsedSet
}

type parsedSet struct {
	pages  map[string]*template.Template // never executed; cloned per request
	base   *template.Template
	assets map[string]string // file name → short content hash
}

// NewTemplates parses fsys (see the layout above) and fails on any
// template error, so prod boot catches broken templates.
func NewTemplates(fsys fs.FS, dev bool, b *i18n.Bundle, f Funcs, log *slog.Logger) (*Templates, error) {
	t := &Templates{fsys: fsys, dev: dev, i18n: b, funcs: f, log: log}
	set, err := t.parse()
	if err != nil {
		return nil, err
	}
	t.cached = set
	return t, nil
}

func (t *Templates) set() (*parsedSet, error) {
	if !t.dev || !t.reloadable() {
		return t.cached, nil
	}
	return t.parse()
}

// reloadable reports whether any template source can change while running.
func (t *Templates) reloadable() bool {
	return reloadable(t.fsys) || slices.ContainsFunc(t.extensions, reloadable)
}

// reloadable reports whether fsys can change while the process runs. An
// embed.FS is fixed at build time, so re-parsing it only burns CPU.
func reloadable(fsys fs.FS) bool {
	_, embedded := fsys.(embed.FS)
	return !embedded
}

func (t *Templates) parse() (*parsedSet, error) {
	base := template.New("").Funcs(t.funcMap(nil, &reqState{}, nil))
	files, err := templateFiles(t.fsys, "base.html", "layouts", "partials")
	if err != nil {
		return nil, err
	}
	for _, name := range files {
		if err := parseFile(base, t.fsys, name); err != nil {
			return nil, err
		}
	}
	pages, err := templateFiles(t.fsys, "pages")
	if err != nil {
		return nil, err
	}
	set := &parsedSet{base: base, pages: map[string]*template.Template{}}
	for _, name := range pages {
		pt, err := base.Clone()
		if err != nil {
			return nil, err
		}
		if err := parseFile(pt, t.fsys, name); err != nil {
			return nil, err
		}
		set.pages[name] = pt
	}
	for _, extension := range t.extensions {
		pages, err := templateFiles(extension, "pages")
		if err != nil {
			return nil, err
		}
		for _, name := range pages {
			if _, exists := set.pages[name]; exists {
				return nil, fmt.Errorf("httpx: duplicate extension template %s", name)
			}
			pt, err := base.Clone()
			if err != nil {
				return nil, err
			}
			if err := parseFile(pt, extension, name); err != nil {
				return nil, err
			}
			set.pages[name] = pt
		}
	}
	if set.assets, err = hashAssets(t.fsys); err != nil {
		return nil, err
	}
	for _, extension := range t.extensions {
		assets, err := hashAssets(extension)
		if err != nil {
			return nil, err
		}
		for name, hash := range assets {
			if _, exists := set.assets[name]; exists {
				return nil, fmt.Errorf("httpx: duplicate extension asset %s", name)
			}
			set.assets[name] = hash
		}
	}
	return set, nil
}

// templateFiles lists .html files (relative to admin/templates) under the
// given files or directories; missing entries are skipped.
func templateFiles(fsys fs.FS, roots ...string) ([]string, error) {
	var out []string
	for _, root := range roots {
		full := path.Join(templatesDir, root)
		err := fs.WalkDir(fsys, full, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !d.IsDir() && strings.HasSuffix(p, ".html") {
				out = append(out, strings.TrimPrefix(p, templatesDir+"/"))
			}
			return nil
		})
		if err != nil && !errorsIsNotExist(err) {
			return nil, fmt.Errorf("httpx: list templates %s: %w", root, err)
		}
	}
	slices.Sort(out)
	return out, nil
}

func errorsIsNotExist(err error) bool { return errors.Is(err, fs.ErrNotExist) }

func parseFile(t *template.Template, fsys fs.FS, name string) error {
	src, err := fs.ReadFile(fsys, path.Join(templatesDir, name))
	if err != nil {
		return fmt.Errorf("httpx: read template %s: %w", name, err)
	}
	if _, err := t.New(name).Parse(string(src)); err != nil {
		return fmt.Errorf("httpx: parse template %s: %w", name, err)
	}
	return nil
}

func hashAssets(fsys fs.FS) (map[string]string, error) {
	out := map[string]string{}
	entries, err := fs.ReadDir(fsys, staticDir)
	if err != nil {
		if errorsIsNotExist(err) {
			return out, nil
		}
		return nil, fmt.Errorf("httpx: list static: %w", err)
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		b, err := fs.ReadFile(fsys, path.Join(staticDir, e.Name()))
		if err != nil {
			return nil, err
		}
		sum := sha256.Sum256(b)
		out[e.Name()] = hex.EncodeToString(sum[:])[:10]
	}
	return out, nil
}

// Render renders page inside layouts/admin.html (content only for HTMX).
func (t *Templates) Render(w http.ResponseWriter, r *http.Request, page string, data any) {
	t.RenderWith(w, r, http.StatusOK, "admin", page, data)
}

// RenderWith renders page with the given status inside layouts/<layout>.html,
// or only its "content" template when layout is "" or for HTMX requests.
// Errors are logged and answered with a plain 500.
func (t *Templates) RenderWith(w http.ResponseWriter, r *http.Request, status int, layout, page string, data any) {
	set, err := t.set()
	if err != nil {
		t.fail(w, r, err)
		return
	}
	pt, ok := set.pages[page]
	if !ok {
		t.fail(w, r, fmt.Errorf("httpx: unknown page %q", page))
		return
	}
	name := "layouts/" + layout + ".html"
	if layout == "" || contentOnly(r) {
		name = "content"
	}
	t.execute(w, r, status, set, pt, name, data)
}

// contentOnly reports an HTMX swap request. History restores (cache miss)
// and boosted navigations replace the whole page, so they get the layout.
func contentOnly(r *http.Request) bool {
	return r.Header.Get("HX-Request") == "true" &&
		r.Header.Get("HX-History-Restore-Request") != "true" &&
		r.Header.Get("HX-Boosted") != "true"
}

// RenderPartial executes a single named template (e.g. "partials/flash")
// without any layout.
func (t *Templates) RenderPartial(w http.ResponseWriter, r *http.Request, name string, data any) {
	set, err := t.set()
	if err != nil {
		t.fail(w, r, err)
		return
	}
	t.execute(w, r, http.StatusOK, set, set.base, name, data)
}

// RenderError renders pages/errors/<status>.html (403, 404, else 500) in
// the auth layout, falling back to plain text.
func (t *Templates) RenderError(w http.ResponseWriter, r *http.Request, status int) {
	page := "pages/errors/500.html"
	switch status {
	case http.StatusForbidden:
		page = "pages/errors/403.html"
	case http.StatusNotFound:
		page = "pages/errors/404.html"
	}
	set, err := t.set()
	if err != nil || set.pages[page] == nil {
		http.Error(w, http.StatusText(status), status)
		return
	}
	t.RenderWith(w, r, status, "auth", page, nil)
}

func (t *Templates) execute(w http.ResponseWriter, r *http.Request, status int, set *parsedSet, tmpl *template.Template, name string, data any) {
	t.mu.Lock()
	c, err := tmpl.Clone()
	t.mu.Unlock()
	if err != nil {
		t.fail(w, r, err)
		return
	}
	st := &reqState{}
	c.Funcs(t.funcMap(r, st, set.assets))
	var buf bytes.Buffer
	if err := c.ExecuteTemplate(&buf, name, data); err != nil {
		t.fail(w, r, err)
		return
	}
	if st.flashRead {
		http.SetCookie(w, &http.Cookie{Name: flashCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteLaxMode})
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Vary", "HX-Request")
	w.WriteHeader(status)
	_, _ = buf.WriteTo(w)
}

func (t *Templates) fail(w http.ResponseWriter, r *http.Request, err error) {
	t.log.Error("httpx: render", "err", err, "path", RedactPath(r.URL.Path), "request_id", RequestIDFromContext(r.Context()))
	http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
}

type reqState struct{ flashRead bool }

var iconName = regexp.MustCompile(`^[a-z0-9-]{1,40}$`)

// funcMap builds the template funcs for one request. With r == nil it only
// provides the signatures for parsing.
func (t *Templates) funcMap(r *http.Request, st *reqState, assets map[string]string) template.FuncMap {
	ctx := context.Background()
	if r != nil {
		ctx = r.Context()
	}
	locale := i18n.Locale(ctx)
	translate := func(key string, args ...any) string {
		if t.i18n == nil {
			return key
		}
		return t.i18n.T(locale, key, args...)
	}
	return template.FuncMap{
		"t":            translate,
		"sidebarBrand": func() SidebarBrand { return t.sidebarBrand(ctx) },
		"locale":       func() string { return locale },
		"dir": func() string {
			if t.i18n == nil {
				return "ltr"
			}
			return t.i18n.Dir(locale)
		},
		"csrf": func() string {
			if t.funcs.CSRF == nil {
				return ""
			}
			return t.funcs.CSRF(ctx)
		},
		"nonce": func() string { return Nonce(ctx) },
		"user": func() any {
			if t.funcs.User == nil {
				return nil
			}
			return t.funcs.User(ctx)
		},
		"setting": func(key string) string {
			if t.funcs.Setting == nil {
				return ""
			}
			return t.funcs.Setting(ctx, key)
		},
		"nav": func() []NavGroup {
			if t.funcs.Nav == nil {
				return nil
			}
			groups := groupNav(t.funcs.Nav(ctx), t.preferredNavGroups(ctx))
			for i := range groups {
				groups[i].Label = groupLabel(func(k string) string { return translate(k) }, groups[i].LabelKey)
			}
			return groups
		},
		"allNav": func() []NavGroup {
			if t.funcs.AllNav == nil {
				return nil
			}
			groups := groupNav(t.funcs.AllNav(ctx), t.preferredNavGroups(ctx))
			for i := range groups {
				groups[i].Label = groupLabel(func(k string) string { return translate(k) }, groups[i].LabelKey)
			}
			return groups
		},
		"active": func(href string) bool {
			if r == nil {
				return false
			}
			p := r.URL.Path
			return p == href || (href != "/admin" && strings.HasPrefix(p, strings.TrimSuffix(href, "/")+"/"))
		},
		"path":  func() string { return pathOf(r) },
		"icon":  icon,
		"json":  toJSON,
		"date":  formatDate,
		"asset": func(name string) string { return assetURL(assets, name) },
		"pagination": func(page, perPage, total int) Pagination {
			return NewPagination(page, perPage, total)
		},
		"flashes": func() []Flash {
			if r == nil {
				return nil
			}
			f, ok := readFlash(r)
			if !ok {
				return nil
			}
			st.flashRead = true
			return []Flash{f}
		},
	}
}

func pathOf(r *http.Request) string {
	if r == nil {
		return ""
	}
	return r.URL.Path
}

func (t *Templates) preferredNavGroups(ctx context.Context) []string {
	if t.funcs.NavGroupOrder == nil {
		return nil
	}
	return t.funcs.NavGroupOrder(ctx)
}

func groupNav(items []NavItem, preferred ...[]string) []NavGroup {
	byGroup := map[string][]NavItem{}
	var extra []string
	for _, it := range items {
		if _, seen := byGroup[it.Group]; !seen && !slices.Contains(navGroupOrder, it.Group) {
			extra = append(extra, it.Group)
		}
		byGroup[it.Group] = append(byGroup[it.Group], it)
	}
	slices.Sort(extra)
	var out []NavGroup
	order := append(slices.Clone(navGroupOrder), extra...)
	if len(preferred) > 0 {
		order = append(slices.Clone(preferred[0]), order...)
	}
	seen := map[string]bool{}
	for _, g := range order {
		if seen[g] {
			continue
		}
		seen[g] = true
		if len(byGroup[g]) == 0 {
			continue
		}
		label := ""
		if g != "" {
			label = "core.nav.group." + g
		}
		out = append(out, NavGroup{Key: g, LabelKey: label, Items: byGroup[g]})
	}
	return out
}

// groupLabel translates a nav group key; when translate returns the key
// itself (no catalog entry) it falls back to the last key segment,
// title-cased, with "_" and "-" as word separators.
func groupLabel(translate func(string) string, key string) string {
	if key == "" {
		return ""
	}
	if s := translate(key); s != key {
		return s
	}
	words := strings.FieldsFunc(key[strings.LastIndex(key, ".")+1:], func(r rune) bool { return r == '_' || r == '-' })
	for i, w := range words {
		r, n := utf8.DecodeRuneInString(w)
		words[i] = string(unicode.ToUpper(r)) + w[n:]
	}
	return strings.Join(words, " ")
}

func icon(name string, class ...string) template.HTML {
	if !iconName.MatchString(name) {
		name = "circle"
	}
	cls := "w-4 h-4"
	if len(class) > 0 {
		cls = template.HTMLEscapeString(class[0])
	}
	return template.HTML(`<i data-lucide="` + name + `" class="` + cls + `" aria-hidden="true"></i>`)
}

// toJSON marshals v for use in scripts and attributes; encoding/json escapes
// <, > and & so the output is safe inside <script>.
func toJSON(v any) (template.JS, error) {
	b, err := json.Marshal(v)
	return template.JS(b), err
}

func formatDate(s string) string {
	if s == "" {
		return "—"
	}
	ts, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return s
	}
	return ts.UTC().Format("2006-01-02 15:04")
}

func assetURL(assets map[string]string, name string) string {
	u := StaticPrefix + name
	if h, ok := assets[name]; ok {
		u += "?v=" + h
	}
	return u
}

// NewPagination computes page links; page and perPage are clamped to ≥ 1.
func NewPagination(page, perPage, total int) Pagination {
	page, perPage = max(page, 1), max(perPage, 1)
	pages := max((total+perPage-1)/perPage, 1)
	return Pagination{Page: page, PerPage: perPage, Total: total, Pages: pages,
		Prev: page - 1, Next: page + 1, HasPrev: page > 1, HasNext: page < pages}
}

// SetFlash stores a one-shot message (kind: success, error, info; message:
// an i18n key) shown by the next rendered page.
func SetFlash(w http.ResponseWriter, kind, message string) {
	http.SetCookie(w, &http.Cookie{Name: flashCookie, Value: url.QueryEscape(kind + "|" + message),
		Path: "/", MaxAge: 120, HttpOnly: true, SameSite: http.SameSiteLaxMode})
}

func readFlash(r *http.Request) (Flash, bool) {
	c, err := r.Cookie(flashCookie)
	if err != nil || c.Value == "" {
		return Flash{}, false
	}
	v, err := url.QueryUnescape(c.Value)
	if err != nil {
		return Flash{}, false
	}
	kind, msg, ok := strings.Cut(v, "|")
	if !ok || !slices.Contains([]string{"success", "error", "info"}, kind) {
		return Flash{}, false
	}
	return Flash{Kind: kind, Message: msg}, true
}

// RegisterExtension attaches module-owned admin pages and asset hashes during
// module registration, before serving requests. Extensions cannot override core
// templates or other extensions. The owning module serves its static assets.
func (t *Templates) RegisterExtension(fsys fs.FS) error {
	t.extensions = append(t.extensions, fsys)
	set, err := t.parse()
	if err != nil {
		t.extensions = t.extensions[:len(t.extensions)-1]
		return err
	}
	t.cached = set
	return nil
}
