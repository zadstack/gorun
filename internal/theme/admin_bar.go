package theme

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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
// marked private and uncacheable. The bar itself needs no script: the
// links are links and hiding is a request that sets a session cookie. On a
// page the visitor may edit that was published from the structured builder,
// the bar also carries the page id and loads web/site/inline-edit.js, which
// adds "Edit in place": the section texts become editable where they stand
// and save to the same builder draft the editor uses (docs/THEMES.md).
const (
	// AdminBarCSSPath serves web/site/admin-bar.css; AdminBarCSSURL adds a
	// content hash.
	AdminBarCSSPath = "/static/site/admin-bar.css"
	// AdminBarJSPath serves web/site/inline-edit.js, linked only into pages
	// that carry the bar and an editable builder page.
	AdminBarJSPath = "/static/site/inline-edit.js"
	// adminBarHidePath (under /admin) sets adminBarCookie for the browser
	// session and returns to ?back.
	adminBarHidePath = "/x/site-bar/hide"
	adminBarCookie   = "gp_sitebar"
	adminBarMarker   = `data-gorun-admin-bar`
	// permAdminBar is what every staff role has and customers do not.
	permAdminBar = "content.view"
	// permEditText and permPublishText are the Studio editor's permissions
	// (builder.PermEditText, builder.PermPublishText; theme cannot import
	// builder): texts and images in place, and publishing those changes.
	permEditText    = "content.edit_text"
	permPublishText = "content.publish_text"
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

var adminBarJS = sync.OnceValues(func() ([]byte, string) {
	b, err := web.FS.ReadFile("site/inline-edit.js")
	if err != nil {
		return nil, ""
	}
	sum := sha256.Sum256(b)
	return b, hex.EncodeToString(sum[:6])
})

// AdminBarJS returns the embedded in-place editing script.
func AdminBarJS() []byte { b, _ := adminBarJS(); return b }

// AdminBarJSURL is AdminBarJSPath with a content hash.
func AdminBarJSURL() string {
	_, v := adminBarJS()
	return AdminBarJSPath + "?v=" + v
}

// inlineEditLabels are the strings inline-edit.js shows, keyed as it reads
// them from data-admin-bar-labels (theme.adminbar.inline_<key>).
var inlineEditLabels = []string{"edit", "done", "loading", "saving", "saved", "error", "conflict", "reload", "pending", "open_editor", "publish", "published", "hint"}

// AdminBarCSSURL is AdminBarCSSPath with a content hash.
func AdminBarCSSURL() string {
	_, v := adminBarCSS()
	return AdminBarCSSPath + "?v=" + v
}

// adminBar returns the bar's markup for this render, or "" when the page
// gets none: no staff user, the user hid it, a theme preview (sandboxed
// CSP), or a request without the bar's own request. inline reports whether
// the bar carries the in-place editor (the page is a builder page the user
// may edit), so the renderer links the script too.
func (s *service) adminBar(tc *Context) (bar string, inline bool) {
	if tc == nil || tc.User == nil || tc.Request == nil || s.previewing(tc.Request.Context(), tc) {
		return "", false
	}
	ctx := tc.Request.Context()
	if !auth.Can(ctx, permAdminBar) {
		return "", false
	}
	if c, err := tc.Request.Cookie(adminBarCookie); err == nil && c.Value == "off" {
		return "", false
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
	// Authors and contributors edit their own pages; every other staff role
	// (admin, editor, the Studio editor, custom roles) edits any page its
	// permissions allow, as the builder's own ownership rule has it.
	owns := tc.Content != nil && tc.Content.ID != "" && tc.Content.Type != "" &&
		((tc.User.Role != "author" && tc.User.Role != "contributor") || tc.Content.AuthorID == tc.User.ID)
	canEdit := owns && auth.Can(ctx, "content.edit")
	// In-place editing: a published builder page (Template "builder", the
	// section ids survive in its body) that this user may edit, with the
	// full editor (content.edit) or texts only (content.edit_text, the
	// Studio editor). The script posts to the builder's own save and publish
	// routes with the CSRF token draft.json hands it; the page is already
	// private and uncacheable.
	inline = owns && (canEdit || auth.Can(ctx, permEditText)) && tc.Content.Type == "page" && tc.Content.Template == "builder"
	var b strings.Builder
	b.WriteString(`<div class="gp-admin-bar" ` + adminBarMarker + ` role="region" aria-label="` + esc(t("label")) + `" lang="` + esc(loc) + `" dir="` + dir + `"`)
	if inline {
		labels := map[string]string{}
		for _, key := range inlineEditLabels {
			labels[key] = t("inline_" + key)
		}
		lj, _ := json.Marshal(labels)
		b.WriteString(` data-admin-bar-builder="` + esc(tc.Content.ID) + `" data-admin-bar-labels="` + esc(string(lj)) + `"`)
		if auth.Can(ctx, "content.publish") || auth.Can(ctx, permPublishText) {
			b.WriteString(` data-admin-bar-publish`)
		}
	}
	b.WriteString(`>`)
	b.WriteString(`<a class="gp-admin-bar-brand" href="/admin"><span class="gp-admin-bar-mark" aria-hidden="true"></span>` + esc(t("dashboard")) + `</a>`)
	if canEdit {
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
	return b.String(), inline
}

// injectAdminBar inserts the stylesheet link (and, for an editable builder
// page, the in-place editing script) before </head> and the bar after the
// <body …> start tag. A page without those tags is returned as it is: the
// bar is never worth a broken document.
func injectAdminBar(page []byte, bar string, inline bool) []byte {
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
	if inline {
		link += `<script defer src="` + html.EscapeString(AdminBarJSURL()) + `" data-gorun-admin-bar-js></script>`
	}
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
