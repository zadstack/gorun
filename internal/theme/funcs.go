package theme

import (
	"bytes"
	"context"
	"fmt"
	"html"
	"html/template"
	"maps"
	"os"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/brand"
	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/httpx"
	"github.com/zadstack/gorun/internal/i18n"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/media"
	"github.com/zadstack/gorun/internal/menus"
	"github.com/zadstack/gorun/internal/seo"
	"github.com/zadstack/gorun/internal/taxonomy"
)

// reqState is the per-render state the template funcs close over.
type reqState struct {
	ctx   context.Context
	tc    *Context
	set   *compiled
	tmpl  *template.Template // the clone being executed (nil for parse stubs)
	depth int

	// per-render caches of settings read by funcs
	front, tz       *string
	settings        map[string]string // memo for setting() within one render
	menus           map[string]*menus.Menu
	builderPreview  bool // Inline stored builder tokens inside the inert preview.
	previewIdentity *brand.Identity
	previewMenu     *menus.Menu
	previewSettings map[string]string
	// identityOverride replaces the stored identity and logo settings for
	// this render only (Appearance → Identity preview).
	identityOverride *IdentitySnapshot
}

func (st *reqState) cached(slot **string, load func() string) string {
	if *slot == nil {
		v := load()
		*slot = &v
	}
	return **slot
}

// itemPath is Linker.Item with the front page id read once per render.
func (f *funcs) itemPath(it *content.Item) string {
	front := f.st.cached(&f.st.front, func() string { return f.s.links.FrontPageID(f.st.ctx) })
	return f.s.links.ItemWithFront(f.st.ctx, it, front)
}

// funcs implements the template funcs for one render.
type funcs struct {
	s  *service
	st *reqState
}

// funcMap returns module funcs (ThemeRegistry) overlaid with the engine
// funcs; engine names win. The same map (with a dummy state) provides the
// signatures at parse time.
func (s *service) funcMap(st *reqState) template.FuncMap {
	// Optional module funcs default to empty results, so any theme (and the
	// CLI, which boots no commerce) can parse templates that call them; the
	// module's own funcs replace these when it is booted.
	fm := template.FuncMap{
		"products": func() []any { return nil },
		"product":  func(string) any { return nil },
	}
	if s.d.Theme != nil {
		maps.Copy(fm, s.d.Theme.Funcs())
	}
	f := &funcs{s: s, st: st}
	maps.Copy(fm, template.FuncMap{
		"t":               f.t,
		"dir":             func() string { return st.tc.Dir },
		"locale":          func() string { return st.tc.Locale },
		"site":            func() SiteInfo { return st.tc.Site },
		"setting":         f.setting,
		"builderStyle":    f.builderStyle,
		"builderIdentity": f.builderIdentity,
		"variant":         f.variant,
		"identity": func(...any) template.HTML {
			// Only a theme that declares the capability itself is restyled.
			if st.tc == nil || st.tc.Theme.Slug == "" || !supportsIdentity(st.tc.Theme) {
				return ""
			}
			slug := st.tc.Theme.Slug
			get := func(k string) string { return f.setting(SettingKey(slug, k)) }
			if st.identityOverride != nil {
				return identityInlineCSS(get, httpx.Nonce(st.ctx))
			}
			return identityCSS(get, httpx.Nonce(st.ctx))
		},
		"sections":       func(...any) template.HTML { return f.sectionsCSS(st.tc) },
		"destination":    f.destination,
		"related":        f.related,
		"adjacent":       f.adjacent,
		"termsOf":        f.termsOf,
		"series":         f.series,
		"libraryFilters": func(tc *Context) LibraryFilters { return f.libraryFilters(tc) },
		"builderBody":    f.builderBody,
		"addonBody": func(name, body string) template.HTML {
			if render, ok := fm[name].(func(string) template.HTML); ok {
				return render(body)
			}
			// Same sanitized-content contract as the existing safe body helper.
			return template.HTML(body)
		},
		"url":       f.url,
		"asset":     f.asset,
		"menu":      f.menu,
		"query":     f.query,
		"content":   f.content,
		"field":     field,
		"terms":     f.terms,
		"media":     f.media,
		"img":       f.img,
		"logo":      f.logo,
		"themeLogo": f.themeLogo,
		"brandImg":  f.brandImg,
		"favicon":   f.favicon,
		"seo":       f.seo,
		"comments":  f.comments,
		"component": func(name string, data ...any) (template.HTML, error) {
			return f.include("components."+name+".html", data)
		},
		"part":        func(name string, data ...any) (template.HTML, error) { return f.include("parts."+name+".html", data) },
		"paginate":    paginate,
		"date":        f.date,
		"money":       func(amount any, currency string) (string, error) { return Money(amount, currency, st.tc.Locale) },
		"safe":        safe,
		"excerpt":     excerpt,
		"readingTime": readingTime,
		"toc":         toc,
		"dict":        dict,
		"add":         func(a, b int) int { return a + b },
		"csrf":        func() string { return auth.CSRFToken(st.ctx) },
		"nonce":       func() string { return httpx.Nonce(st.ctx) },
	})
	return fm
}

// t translates key: the theme's lang/<locale>.json first, then the
// platform catalogs (i18n bundle), then the key itself.
func (f *funcs) t(key string, args ...any) string {
	loc := f.st.tc.Locale
	for _, l := range []string{loc, "en"} {
		if msg, ok := f.st.set.lang[l][key]; ok {
			if len(args) > 0 {
				return fmt.Sprintf(msg, args...)
			}
			return msg
		}
	}
	if f.s.d.I18n != nil {
		return f.s.d.I18n.T(loc, key, args...)
	}
	return key
}

var (
	settingPrefixes = []string{"site.", "seo.", "theme.", "social.", "contact."}
	settingSecrets  = []string{"password", "secret", "token", "apikey", "api_key", "private", "smtp", "identity_draft", "identity_history"}
)

// setting returns a public setting as a string. Only keys under
// settingPrefixes are readable, never ones that look like credentials:
// templates (including MCP-written drafts) must not leak secrets.
func (f *funcs) setting(key string) string {
	k := strings.ToLower(key)
	allowed := false
	for _, p := range settingPrefixes {
		allowed = allowed || strings.HasPrefix(k, p)
	}
	for _, bad := range settingSecrets {
		allowed = allowed && !strings.Contains(k, bad)
	}
	if !allowed || f.s.d.Settings == nil {
		return ""
	}
	if key == "site.logo" {
		if value, ok := f.st.previewSettings[key]; ok {
			return value
		}
	}
	if o := f.st.identityOverride; o != nil {
		if v, ok := o.lookup(key); ok {
			return v
		}
	}
	if f.st.settings != nil {
		if v, ok := f.st.settings[key]; ok {
			return v
		}
	}
	var v any
	if err := f.s.d.Settings.Get(f.st.ctx, key, &v); err != nil || v == nil {
		f.memoSetting(key, "")
		return ""
	}
	var out string
	if s, ok := v.(string); ok {
		out = s
	} else {
		out = fmt.Sprint(v)
	}
	f.memoSetting(key, out)
	return out
}

func (f *funcs) memoSetting(key, v string) {
	if f.st.settings == nil {
		f.st.settings = map[string]string{}
	}
	f.st.settings[key] = v
}

// include executes a part or component of the current set into HTML.
func (f *funcs) include(name string, data []any) (template.HTML, error) {
	if err := f.st.set.errs[name]; err != nil {
		return "", err
	}
	if f.st.tmpl == nil || f.st.tmpl.Lookup(name) == nil {
		return "", fmt.Errorf("%w: %s", ErrNotFound, name)
	}
	if f.st.depth >= maxIncludeDepth {
		return "", errDepth
	}
	var d any = f.st.tc
	if len(data) > 0 {
		d = data[0]
	}
	f.st.depth++
	defer func() { f.st.depth-- }()
	var buf bytes.Buffer
	if err := f.st.tmpl.ExecuteTemplate(&buf, name, d); err != nil {
		return "", err
	}
	return template.HTML(buf.String()), nil
}

// url makes an absolute URL with the site base URL from a path, a content
// item, a term or a menu item. An item links to its own locale; a term and
// the routes every locale has (see localeRoutes) stay in the page's locale
// (/<locale>/… off the site locale); any other path is left where it points.
func (f *funcs) url(v any) string {
	base := f.st.tc.Site.BaseURL
	switch x := v.(type) {
	case string:
		if strings.HasPrefix(x, "http://") || strings.HasPrefix(x, "https://") || strings.HasPrefix(x, "//") {
			return x
		}
		if !strings.HasPrefix(x, "/") {
			x = "/" + x
		}
		return base + f.localized(x)
	case *content.Item:
		return base + f.itemPath(x)
	case content.Item:
		return base + f.itemPath(&x)
	case *taxonomy.Term:
		return base + f.localizedTerm(f.s.links.Term(x))
	case taxonomy.Term:
		return base + f.localizedTerm(f.s.links.Term(&x))
	case *menus.Item:
		return x.URL
	case menus.Item:
		return x.URL
	}
	return base + "/"
}

// localeRoutes are the site paths that exist in every locale. Other string paths (a page
// slug, a menu link, another module's route) may exist in one locale only, so url leaves them.
var localeRoutes = map[string]bool{"/": true, "/search": true}

// localized puts a locale route under the page's locale prefix.
func (f *funcs) localized(p string) string {
	path, _, _ := strings.Cut(p, "?")
	path, _, _ = strings.Cut(path, "#")
	if !localeRoutes[path] {
		return p
	}
	return i18n.LocalizePath(f.st.tc.Locale, f.st.tc.Site.Locale, p)
}

// localizedTerm puts a term archive under the page's locale prefix (term lists exist in every locale).
func (f *funcs) localizedTerm(p string) string {
	return i18n.LocalizePath(f.st.tc.Locale, f.st.tc.Site.Locale, p)
}

// asset returns /patterns/<slug>/assets/<p>?v=<mtime> when the active
// pattern owns the file, else /themes/<slug>/assets/<p>?v=<mtime> for the
// first theme in the chain that has it.
func (f *funcs) asset(p string) string {
	clean := strings.TrimPrefix(path.Clean("/"+p), "/")
	if f.st.set.patternDir != "" && f.st.set.patternSlug != "" {
		st, err := os.Stat(filepath.Join(f.st.set.patternDir, "assets", filepath.FromSlash(clean)))
		if err == nil && !st.IsDir() {
			return "/patterns/" + f.st.set.patternSlug + "/assets/" + clean + "?v=" + strconv.FormatInt(st.ModTime().Unix(), 10)
		}
	}
	for _, info := range f.st.set.chain {
		st, err := os.Stat(filepath.Join(info.Dir, "assets", filepath.FromSlash(clean)))
		if err == nil && !st.IsDir() {
			return "/themes/" + info.Slug + "/assets/" + clean + "?v=" + strconv.FormatInt(st.ModTime().Unix(), 10)
		}
	}
	return "/themes/" + f.st.set.slug + "/assets/" + clean
}

// menu returns the menu at location loc with item URLs resolved, or nil.
func (f *funcs) menu(loc string) *menus.Menu {
	if f.st.previewMenu != nil && (loc == "primary" || loc == "footer") {
		return f.st.previewMenu
	}
	if f.st.menus != nil {
		if m, ok := f.st.menus[loc]; ok {
			return m
		}
	}
	svc, ok := kernel.Get[menus.Service](f.s.d, kernel.ServiceMenus)
	if !ok {
		return nil
	}
	m, err := svc.ByLocationLocale(f.st.ctx, loc, f.st.tc.Locale)
	if err != nil || m == nil {
		f.memoMenu(loc, nil)
		return nil
	}
	for i := range m.Items {
		f.resolveMenuItem(&m.Items[i])
	}
	f.memoMenu(loc, m)
	return m
}

func (f *funcs) memoMenu(loc string, m *menus.Menu) {
	if f.st.menus == nil {
		f.st.menus = map[string]*menus.Menu{}
	}
	f.st.menus[loc] = m
}

// resolveMenuItem runs the menus.resolve_url filter (so other modules'
// resolvers apply) on it and its children, then fills any URL still empty
// with the Linker.
func (f *funcs) resolveMenuItem(it *menus.Item) {
	if f.s.d.Hooks != nil {
		if _, err := f.s.d.Hooks.Apply(f.st.ctx, HookMenuResolveURL, it); err != nil {
			f.s.log.Warn("theme: menus.resolve_url", "err", err)
		}
	}
	f.s.links.MenuItem(f.st.ctx, it)
	for i := range it.Children {
		f.resolveMenuItem(&it.Children[i])
	}
}

func (f *funcs) visible(it *content.Item) bool {
	if it == nil {
		return false
	}
	svc, ok := kernel.Get[content.Service](f.s.d, kernel.ServiceContent)
	if !ok {
		return false
	}
	typ, err := svc.GetType(f.st.ctx, it.Type)
	if err != nil || !typ.Public {
		return false
	}
	if it.Status == "published" {
		return content.PubliclyReadable(f.st.ctx, svc, it.ID)
	}
	u := auth.UserFromContext(f.st.ctx)
	return u != nil && auth.Can(f.st.ctx, "content.view") && (u.Role == "admin" || u.Role == "editor" || it.AuthorID == u.ID)
}

// content returns the item with id, or nil when missing or not visible.
func (f *funcs) content(id string) *content.Item {
	svc, ok := kernel.Get[content.Service](f.s.d, kernel.ServiceContent)
	if !ok || id == "" {
		return nil
	}
	it, err := svc.Get(f.st.ctx, id)
	if err != nil || !f.visible(it) {
		return nil
	}
	return it
}

// query runs a content query from a map (see BuildQuery).
func (f *funcs) query(args map[string]any) (content.Page[content.Item], error) {
	svc, ok := kernel.Get[content.Service](f.s.d, kernel.ServiceContent)
	if !ok {
		return content.Page[content.Item]{}, nil
	}
	q, err := BuildQuery(args, auth.Can(f.st.ctx, "content.view"))
	if err != nil {
		return content.Page[content.Item]{}, err
	}
	q.PublicOnly = true
	if q.Locale == "" { // the page's locale, unless the template asks for one
		q.PublicLocale = f.st.tc.Locale
	}
	return svc.Query(f.st.ctx, q)
}

// itemOf extracts a content item from a *Context, *content.Item or Item.
func itemOf(v any) *content.Item {
	switch x := v.(type) {
	case *Context:
		if x != nil {
			return x.Content
		}
	case *content.Item:
		return x
	case content.Item:
		return &x
	}
	return nil
}

// field reads a custom field of an item (or a key of a map, e.g. a
// repeater row); nil when absent.
func field(v any, key string) any {
	if m, ok := v.(map[string]any); ok {
		return m[key]
	}
	if it := itemOf(v); it != nil {
		return it.Fields[key]
	}
	return nil
}

// terms resolves the item's term slugs in taxonomy tax to terms.
func (f *funcs) terms(v any, tax string) []taxonomy.Term {
	it := itemOf(v)
	svc, ok := kernel.Get[taxonomy.Service](f.s.d, kernel.ServiceTaxonomy)
	if it == nil || !ok {
		return nil
	}
	var out []taxonomy.Term
	for _, slug := range it.Terms[tax] {
		if t, err := svc.GetTermBySlug(f.st.ctx, tax, slug); err == nil && t != nil {
			out = append(out, *t)
		}
	}
	return out
}

// media returns the media item for v (an id, *media.Media or Media), or nil.
func (f *funcs) media(v any) *media.Media {
	switch x := v.(type) {
	case *media.Media:
		return x
	case media.Media:
		return &x
	case string:
		svc, ok := kernel.Get[media.Service](f.s.d, kernel.ServiceMedia)
		if !ok || x == "" {
			return nil
		}
		m, err := svc.Get(f.st.ctx, x)
		if err != nil {
			return nil
		}
		return m
	}
	return nil
}

// safeURL accepts relative and http(s) URLs only.
func safeURL(u string) bool {
	lu := strings.ToLower(strings.TrimSpace(u))
	if strings.HasPrefix(lu, "http://") || strings.HasPrefix(lu, "https://") {
		return true
	}
	return u != "" && !strings.Contains(strings.SplitN(lu, "/", 2)[0], ":")
}

// img renders a responsive <img> for media v at variant (srcset of every
// variant and the original, alt, width/height, lazy loading). An optional
// third argument is the class attribute.
func (f *funcs) img(v any, variant string, class ...string) template.HTML {
	m := f.media(v)
	if m == nil {
		return ""
	}
	src, w, h := m.URL, m.Width, m.Height
	if vr, ok := m.Variants[variant]; ok && vr.URL != "" {
		src, w, h = vr.URL, vr.Width, vr.Height
	}
	if !safeURL(src) {
		return ""
	}
	type cand struct {
		url string
		w   int
	}
	var cands []cand
	seen := map[int]bool{}
	for _, vr := range m.Variants {
		if vr.Width > 0 && safeURL(vr.URL) && !seen[vr.Width] {
			seen[vr.Width] = true
			cands = append(cands, cand{vr.URL, vr.Width})
		}
	}
	if m.Width > 0 && safeURL(m.URL) && !seen[m.Width] {
		cands = append(cands, cand{m.URL, m.Width})
	}
	sort.Slice(cands, func(i, j int) bool { return cands[i].w < cands[j].w })
	esc := template.HTMLEscapeString
	var b strings.Builder
	b.WriteString(`<img src="` + esc(src) + `"`)
	if len(cands) > 1 {
		parts := make([]string, len(cands))
		for i, c := range cands {
			parts[i] = esc(c.url) + " " + strconv.Itoa(c.w) + "w"
		}
		b.WriteString(` srcset="` + strings.Join(parts, ", ") + `"`)
		if w > 0 {
			b.WriteString(` sizes="(max-width: ` + strconv.Itoa(w) + `px) 100vw, ` + strconv.Itoa(w) + `px"`)
		}
	}
	b.WriteString(` alt="` + esc(m.Alt) + `"`)
	if w > 0 && h > 0 {
		b.WriteString(` width="` + strconv.Itoa(w) + `" height="` + strconv.Itoa(h) + `"`)
	}
	if len(class) > 0 && class[0] != "" {
		b.WriteString(` class="` + esc(class[0]) + `"`)
	}
	b.WriteString(` loading="lazy" decoding="async">`)
	return template.HTML(b.String())
}

// seo renders the head meta for the page via seo.Service: the item's SEO
// map, falling back to its title/excerpt/permalink/featured image, or the
// page title (Extra["title"]) and site title for listings; then the
// hreflang alternates of the page.
func (f *funcs) seo(v any) template.HTML {
	tc := f.st.tc
	if c, ok := v.(*Context); ok && c != nil {
		tc = c
	}
	return f.seoMeta(tc) + alternateLinks(tc)
}

// alternateLinks are the hreflang links of tc's alternates, x-default on the site locale's.
func alternateLinks(tc *Context) template.HTML {
	if len(tc.Alternates) == 0 {
		return ""
	}
	var b strings.Builder
	def := tc.Alternates[0].URL
	for _, a := range tc.Alternates {
		b.WriteString(`<link rel="alternate" hreflang="` + template.HTMLEscapeString(a.Locale) + `" href="` + template.HTMLEscapeString(a.URL) + `">` + "\n")
		if a.Locale == tc.Site.Locale {
			def = a.URL
		}
	}
	b.WriteString(`<link rel="alternate" hreflang="x-default" href="` + template.HTMLEscapeString(def) + `">` + "\n")
	return template.HTML(b.String())
}

// seoMeta is the meta part of seo.
func (f *funcs) seoMeta(tc *Context) template.HTML {
	site := tc.Site
	var meta seo.Meta
	fb := seo.Meta{Title: site.Title, Description: site.Tagline}
	if t, ok := tc.Extra["title"].(string); ok && t != "" {
		fb.Title = t
	}
	if it := tc.Content; it != nil {
		meta = seo.FromMap(it.SEO)
		fb.Title = it.Title
		path := f.itemPath(it)
		if path == "/" {
			fb.Title = site.Title
		}
		fb.Description = excerpt(it, 30)
		fb.Canonical = site.BaseURL + path
		if m := f.media(it.FeaturedMediaID); m != nil {
			fb.OGImage = m.URL
			if vr, ok := m.Variants["large"]; ok && vr.URL != "" {
				fb.OGImage = vr.URL
			}
		}
	}
	if tc.Kind == Kind404 || tc.Kind == KindSearch || tc.Kind == Kind500 {
		fb.Robots = "noindex"
	}
	fb.OGTitle, fb.OGDescription = fb.Title, fb.Description
	if svc, ok := kernel.Get[seo.Service](f.s.d, kernel.ServiceSEO); ok {
		return svc.Render(f.st.ctx, meta, fb)
	}
	title := meta.Title
	if title == "" {
		title = fb.Title
		if title != "" && site.Title != "" && title != site.Title {
			title += " · " + site.Title
		}
	}
	out := "<title>" + template.HTMLEscapeString(title) + "</title>"
	if fb.Description != "" {
		out += `<meta name="description" content="` + template.HTMLEscapeString(fb.Description) + `">`
	}
	return template.HTML(out)
}

// safe marks already-sanitized HTML (content bodies) as safe.
func safe(v any) template.HTML {
	switch x := v.(type) {
	case template.HTML:
		return x
	case string:
		return template.HTML(x)
	case nil:
		return ""
	}
	return template.HTML(template.HTMLEscapeString(fmt.Sprint(v)))
}

// excerpt strips tags and truncates to n words (default 30) with "…".
// v is a string or a content item (its Excerpt, else its Body).
func excerpt(v any, n ...int) string {
	var s string
	switch x := v.(type) {
	case string:
		s = x
	case template.HTML:
		s = string(x)
	default:
		if it := itemOf(v); it != nil {
			s = it.Excerpt
			if strings.TrimSpace(s) == "" {
				s = it.Body
			}
		}
	}
	limit := 30
	if len(n) > 0 && n[0] > 0 {
		limit = n[0]
	}
	words := strings.FieldsFunc(html.UnescapeString(stripTags(s)), unicode.IsSpace)
	if len(words) <= limit {
		return strings.Join(words, " ")
	}
	return strings.Join(words[:limit], " ") + "…"
}

// readingWordsPerMinute is a conservative pace for Arabic and English prose.
const readingWordsPerMinute = 200

// readingTime estimates whole minutes to read an item's body (or an HTML
// string), never less than one; markup and attributes are not counted.
func readingTime(v any) int {
	var s string
	switch x := v.(type) {
	case string:
		s = x
	case template.HTML:
		s = string(x)
	default:
		if it := itemOf(v); it != nil {
			s = it.Body
		}
	}
	words := len(strings.FieldsFunc(html.UnescapeString(stripTags(s)), unicode.IsSpace))
	return max(1, (words+readingWordsPerMinute-1)/readingWordsPerMinute)
}

// stripTags removes markup, turning tags into spaces.
func stripTags(s string) string {
	var b strings.Builder
	in := false
	for _, r := range s {
		switch {
		case r == '<':
			in = true
		case r == '>' && in:
			in = false
			b.WriteByte(' ')
		case !in:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// dict builds a map from key/value pairs.
func dict(kv ...any) (map[string]any, error) {
	if len(kv)%2 != 0 {
		return nil, fmt.Errorf("dict: odd number of arguments")
	}
	m := make(map[string]any, len(kv)/2)
	for i := 0; i < len(kv); i += 2 {
		k, ok := kv[i].(string)
		if !ok {
			return nil, fmt.Errorf("dict: key %v is not a string", kv[i])
		}
		m[k] = kv[i+1]
	}
	return m, nil
}

// logoSettings is the precedence of site settings per logo variant.
var logoSettings = map[string][]string{"primary": {"site.logo"}, "dark": {"site.logo_dark", "site.logo"}, "light": {"site.logo_light", "site.logo"}, "icon": {"site.icon"}}

// logo returns the media item for a logo variant ("primary", "dark",
// "light", "icon") following the site-setting precedence, or nil. Themes
// render it with {{img (logo "dark") "thumbnail"}}.
func (f *funcs) logo(variant string) *media.Media {
	for _, key := range logoSettings[variant] {
		if id := f.setting(key); id != "" {
			if m := f.media(id); m != nil {
				return m
			}
		}
	}
	return nil
}

// themeLogo returns the public URL of a logo the theme manifest ships for
// variant ("" when the theme declares none). Site settings take precedence:
// themes should try {{logo "icon"}} first.
func (f *funcs) themeLogo(variant string) string {
	info := f.s.Active(f.st.ctx)
	path, ok := info.Logos[variant]
	if !ok || !ValidAssetPath(path) {
		return ""
	}
	return "/themes/" + info.Slug + "/assets/" + path
}

// brandImg renders the site mark for the primary logo m. The mark follows
// the page, not the visitor's system setting: the dark logo when the page
// background is dark (the identity's dark scheme or background, else a dark
// builder background), the primary logo otherwise. Without a distinct dark
// logo it is the same <img> as {{img}}. Only a theme that declares
// "color-scheme:auto" (a page that follows the system setting itself) gets a
// <picture> whose dark source follows prefers-color-scheme.
func (f *funcs) brandImg(m *media.Media, variant string, class ...string) template.HTML {
	if m == nil {
		return ""
	}
	dark := f.logo("dark")
	if dark == nil || dark.ID == m.ID {
		return f.img(m, variant, class...)
	}
	if bg := f.pageBackground(); bg != "" {
		if c, ok := parseColor(bg); ok && luminance(c) < 0.5 {
			return f.img(dark, variant, class...)
		}
		return f.img(m, variant, class...)
	}
	if !f.themeSupports("color-scheme:auto") {
		return f.img(m, variant, class...)
	}
	src := dark.URL
	if v, ok := dark.Variants[variant]; ok && v.URL != "" {
		src = v.URL
	}
	if !safeURL(src) {
		return f.img(m, variant, class...)
	}
	return template.HTML(`<picture><source srcset="` + template.HTMLEscapeString(src) + `" media="(prefers-color-scheme: dark)">` + string(f.img(m, variant, class...)) + `</picture>`)
}

// pageBackground is the page background the identity layers set: the site
// identity's (its own colour or its dark scheme), else the builder
// identity's, else "" (the theme's own, which is light for every shipped
// theme).
func (f *funcs) pageBackground() string {
	if f.st.tc != nil && supportsIdentity(f.st.tc.Theme) {
		slug := f.st.tc.Theme.Slug
		if bg := identityBackground(func(k string) string { return f.setting(SettingKey(slug, k)) }); bg != "" {
			return bg
		}
	}
	if f.st.previewIdentity != nil {
		return f.st.previewIdentity.Background
	}
	var b brand.Identity
	if f.s.d.Settings != nil && f.s.d.Settings.Get(f.st.ctx, "builder.identity", &b) == nil && brand.Validate(b) == nil {
		return b.Background
	}
	return ""
}

// themeSupports reports whether the active theme declares capability c.
func (f *funcs) themeSupports(c string) bool {
	return f.st.tc != nil && slices.Contains(f.st.tc.Theme.Supports, c)
}

// favicon renders the <link rel="icon"> for the site icon: the site.icon
// media when set, else the theme manifest's icon, else nothing.
func (f *funcs) favicon() template.HTML {
	var href, mime string
	if m := f.logo("icon"); m != nil && safeURL(m.URL) {
		href, mime = m.URL, m.Mime
	} else if u := f.themeLogo("icon"); u != "" {
		href = u
	}
	if href == "" {
		return ""
	}
	attrs := ` href="` + template.HTMLEscapeString(href) + `"`
	if mime != "" {
		attrs += ` type="` + template.HTMLEscapeString(mime) + `"`
	}
	return template.HTML(`<link rel="icon"` + attrs + `>`)
}

// sectionsCSS is what {{sections .}} prints in every theme head: the core
// site stylesheet and, on commerce pages, the store stylesheet (see
// coreStylesheets), then the section library (sections.css, resolved through
// the theme chain) only when the page prints content that uses its gp-s-
// blocks: the item being rendered or any listed item (index templates print
// full bodies).
func (f *funcs) sectionsCSS(tc *Context) template.HTML {
	out := f.coreStylesheets(tc)
	if usesSections(tc) {
		out += `<link rel="stylesheet" href="` + template.HTMLEscapeString(f.asset("sections.css")) + `">`
	}
	return template.HTML(out)
}

// usesSections reports whether any body the page may print carries a gp-s-
// class (the sanitizer keeps only gp-* classes, so the prefix is reliable):
// the item being rendered, any listed item, any string in Extra, or a module
// that renders its own sanitized HTML and sets Extra["sections"] = true (the
// commerce product page does this for a description using the blocks).
func usesSections(tc *Context) bool {
	if tc == nil {
		return false
	}
	if tc.Content != nil && strings.Contains(tc.Content.Body, "gp-s-") {
		return true
	}
	for i := range tc.Items {
		if strings.Contains(tc.Items[i].Body, "gp-s-") {
			return true
		}
	}
	if on, _ := tc.Extra["sections"].(bool); on {
		return true
	}
	for _, v := range tc.Extra {
		if x, ok := v.(string); ok && strings.Contains(x, "gp-s-") {
			return true
		}
	}
	return false
}
