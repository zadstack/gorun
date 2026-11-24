package theme

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"html"
	"net/http"
	"net/url"
	"strings"
	"sync"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/httpx"
	"github.com/zadstack/gorun/internal/i18n"
	"github.com/zadstack/gorun/web"
)

// The admin bar: a strip at the top of every public page for signed-in
// staff, with the dashboard, the editor of the page being viewed, the
// website overview and a way to hide it. The renderer inserts it after
// <body> in the page it has already executed (Render), so every theme
// gets it without a template change, and only responses that carry it are
// marked private and uncacheable. No script: the links are links and
// hiding is a request that sets a session cookie.
const (
	// AdminBarCSSPath serves web/site/admin-bar.css; AdminBarCSSURL adds a
	// content hash.
	AdminBarCSSPath = "/static/site/admin-bar.css"
	// adminBarHidePath (under /admin) sets adminBarCookie for the browser
	// session and returns to ?back.
	adminBarHidePath = "/x/site-bar/hide"
	adminBarCookie   = "gp_sitebar"
	adminBarMarker   = `data-gorun-admin-bar`
	// permAdminBar is what every staff role has and customers do not.
	permAdminBar = "content.view"
)

var adminBarCSS = sync.OnceValues(func() ([]byte, string) {
	b, err := web.FS.ReadFile("site/admin-bar.css")
	if err != nil {
		return nil, ""
	}
	sum := sha256.Sum256(b)
	return b, hex.EncodeToString(sum[:6])
})

// AdminBarCSS returns the embedded admin bar stylesheet.
func AdminBarCSS() []byte { b, _ := adminBarCSS(); return b }

// AdminBarCSSURL is AdminBarCSSPath with a content hash.
func AdminBarCSSURL() string {
	_, v := adminBarCSS()
	return AdminBarCSSPath + "?v=" + v
}

// adminBar returns the bar's markup for this render, or "" when the page
// gets none: no staff user, the user hid it, a theme preview (sandboxed
// CSP), or a request without the bar's own request.
func (s *service) adminBar(tc *Context) string {
	if tc == nil || tc.User == nil || tc.Request == nil || s.previewing(tc.Request.Context(), tc) {
		return ""
	}
	ctx := tc.Request.Context()
	if !auth.Can(ctx, permAdminBar) {
		return ""
	}
	if c, err := tc.Request.Cookie(adminBarCookie); err == nil && c.Value == "off" {
		return ""
	}
	loc := tc.User.Locale
	if loc == "" {
		loc = tc.Locale
	}
	dir := "ltr"
	t := func(key string) string { return key }
	if s.d.I18n != nil {
		dir = s.d.I18n.Dir(loc)
		t = func(key string) string { return s.d.I18n.T(loc, "theme.adminbar."+key) }
	}
	esc := html.EscapeString
	var b strings.Builder
	b.WriteString(`<div class="gp-admin-bar" ` + adminBarMarker + ` role="region" aria-label="` + esc(t("label")) + `" lang="` + esc(loc) + `" dir="` + dir + `">`)
	b.WriteString(`<a class="gp-admin-bar-brand" href="/admin"><span class="gp-admin-bar-mark" aria-hidden="true"></span>` + esc(t("dashboard")) + `</a>`)
	if tc.Content != nil && tc.Content.ID != "" && tc.Content.Type != "" && auth.Can(ctx, "content.edit") &&
		(tc.User.Role == "admin" || tc.User.Role == "editor" || tc.Content.AuthorID == tc.User.ID) {
		b.WriteString(`<a href="/admin/content/` + esc(tc.Content.Type) + `/` + esc(tc.Content.ID) + `" data-admin-bar-edit>` + esc(t("edit")) + `</a>`)
	}
	if auth.Can(ctx, PermManage) || auth.Can(ctx, PermIdentity) {
		b.WriteString(`<a href="/admin/website">` + esc(t("website")) + `</a>`)
	}
	name := tc.User.Name
	if name == "" {
		name = tc.User.Email
	}
	b.WriteString(`<span class="gp-admin-bar-user"><bdi>` + esc(name) + `</bdi></span>`)
	// The router strips a locale prefix before the page handler runs; the
	// way back is the path the visitor typed.
	back := i18n.LocalePrefix(tc.Locale, tc.Site.Locale) + tc.Request.URL.RequestURI()
	b.WriteString(`<a class="gp-admin-bar-hide" href="/admin` + adminBarHidePath + `?back=` + esc(url.QueryEscape(back)) + `" data-admin-bar-hide aria-label="` + esc(t("hide")) + `" title="` + esc(t("hide")) + `">×</a>`)
	b.WriteString(`</div>`)
	return b.String()
}

// injectAdminBar inserts the stylesheet link before </head> and the bar
// after the <body …> start tag. A page without those tags is returned as it
// is: the bar is never worth a broken document.
func injectAdminBar(page []byte, bar string) []byte {
	lower := bytes.ToLower(page)
	start := bytes.Index(lower, []byte("<body"))
	if start < 0 {
		return page
	}
	end := bytes.IndexByte(page[start:], '>')
	if end < 0 {
		return page
	}
	end += start + 1
	link := `<link rel="stylesheet" href="` + html.EscapeString(AdminBarCSSURL()) + `" data-gorun-admin-bar-css>`
	out := make([]byte, 0, len(page)+len(link)+len(bar)+1)
	if head := bytes.Index(lower[:start], []byte("</head>")); head >= 0 {
		out = append(out, page[:head]...)
		out = append(out, link...)
		out = append(out, page[head:end]...)
	} else {
		out = append(out, page[:end]...)
		out = append(out, link...)
	}
	out = append(out, bar...)
	out = append(out, page[end:]...)
	return out
}

// hideAdminBar is GET /admin/x/site-bar/hide?back=/path: the bar stays
// hidden for the rest of the browser session. back must be a path on this
// site (a leading slash, not a scheme-relative URL); anything else goes home.
func (a *admin) hideAdminBar(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{Name: adminBarCookie, Value: "off", Path: "/", HttpOnly: true,
		Secure: httpx.SecureCookie(r, a.s.d.Config.BaseURL), SameSite: http.SameSiteLaxMode})
	back := r.URL.Query().Get("back")
	if !strings.HasPrefix(back, "/") || strings.HasPrefix(back, "//") || strings.HasPrefix(back, "/\\") {
		back = "/"
	}
	http.Redirect(w, r, back, http.StatusSeeOther)
}
