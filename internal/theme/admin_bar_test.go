package theme

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/kernel"
)

// Signed-in staff get the admin bar at the top of every public page, with
// the editor of the page being viewed when they may edit it; visitors and
// customers get the page unchanged and cacheable.
func TestAdminBarOnPublicPages(t *testing.T) {
	e := newEnv(t, "../../themes")
	page := content.Item{ID: "01HZZ0000000000000000000AB", Type: "page", Slug: "about", Title: "About", Status: "published", Body: "<p>hi</p>"}

	rec, err := e.render(kernel.KindRequest{Kind: KindPage, Type: "page", Slug: "about"}, &Context{Content: &page})
	if err != nil {
		t.Fatal(err)
	}
	if body := rec.Body.String(); strings.Contains(body, adminBarMarker) || strings.Contains(body, AdminBarCSSPath) {
		t.Fatal("anonymous visitor got the admin bar")
	}
	if cc := rec.Header().Get("Cache-Control"); strings.Contains(cc, "no-store") {
		t.Fatalf("anonymous page marked no-store: %q", cc)
	}

	rec, err = e.render(kernel.KindRequest{Kind: KindPage, Type: "page", Slug: "about"}, &Context{Content: &page}, adminCtx())
	if err != nil {
		t.Fatal(err)
	}
	body := rec.Body.String()
	for _, want := range []string{
		`<link rel="stylesheet" href="` + AdminBarCSSURL() + `" data-gorun-admin-bar-css></head>`,
		`<div class="gp-admin-bar" ` + adminBarMarker,
		`href="/admin"`, `href="/admin/content/page/01HZZ0000000000000000000AB" data-admin-bar-edit`, `href="/admin/website"`,
		`<bdi>Admin</bdi>`, `href="/admin/x/site-bar/hide?back=%2F" data-admin-bar-hide`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("staff page lacks %s", want)
		}
	}
	bodyTag := strings.Index(body, "<body")
	if bodyTag < 0 {
		t.Fatal("no <body>")
	}
	afterBody := body[bodyTag+strings.Index(body[bodyTag:], ">")+1:]
	if !strings.HasPrefix(afterBody, `<div class="gp-admin-bar" `) {
		t.Errorf("the bar is not the first thing after <body>: %.80q", afterBody)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "private, no-store" {
		t.Fatalf("staff page Cache-Control = %q", cc)
	}

	// A contributor who did not write the page gets the bar without the editor.
	contributor := auth.WithUser(context.Background(), &auth.User{ID: "u2", Role: "contributor", Status: auth.StatusActive, Name: "Contributor"})
	rec, _ = e.render(kernel.KindRequest{Kind: KindPage, Type: "page", Slug: "about"}, &Context{Content: &page}, contributor)
	if body := rec.Body.String(); !strings.Contains(body, adminBarMarker) || strings.Contains(body, "data-admin-bar-edit") {
		t.Error("contributor: bar missing or edit link offered for someone else's page")
	}

	// A page in another locale lives under /<locale>/; hide returns there.
	req := httptest.NewRequest(http.MethodGet, "/about?x=1", nil).WithContext(adminCtx())
	rec, _ = e.render(kernel.KindRequest{Kind: KindPage, Type: "page", Slug: "about"}, &Context{Content: &page, Request: req, Locale: "en"}, adminCtx())
	if body := rec.Body.String(); !strings.Contains(body, `href="/admin/x/site-bar/hide?back=%2Fen%2Fabout%3Fx%3D1"`) {
		t.Errorf("hide link does not return to the locale-prefixed page: %s", body[strings.Index(body, "site-bar/hide"):][:60])
	}

	// Hidden for the browser session once the cookie is set.
	req = httptest.NewRequest(http.MethodGet, "/about", nil).WithContext(adminCtx())
	req.AddCookie(&http.Cookie{Name: adminBarCookie, Value: "off"})
	rec, _ = e.render(kernel.KindRequest{Kind: KindPage, Type: "page", Slug: "about"}, &Context{Content: &page, Request: req}, adminCtx())
	if strings.Contains(rec.Body.String(), adminBarMarker) {
		t.Error("the bar ignores the hide cookie")
	}
}

func TestInjectAdminBarKeepsBrokenPagesIntact(t *testing.T) {
	bar := `<div data-gorun-admin-bar></div>`
	if got := string(injectAdminBar([]byte("<p>no body tag</p>"), bar, false)); got != "<p>no body tag</p>" {
		t.Fatalf("page without <body> changed: %q", got)
	}
	got := string(injectAdminBar([]byte(`<html><head><title>x</title></head><BODY class="a">hi</BODY></html>`), bar, false))
	if !strings.Contains(got, `data-gorun-admin-bar-css></head>`) || !strings.Contains(got, `<BODY class="a">`+bar+`hi`) || strings.Contains(got, "<script") {
		t.Fatalf("injection = %q", got)
	}
	got = string(injectAdminBar([]byte(`<html><head></head><body>hi</body></html>`), bar, true))
	if !strings.Contains(got, `<script defer src="`+AdminBarJSURL()+`" data-gorun-admin-bar-js></script></head>`) {
		t.Fatalf("inline injection lacks the script: %q", got)
	}
}

// A page published from the structured builder carries the in-place editor
// for staff who may edit it: the page id and labels on the bar, the script
// in the head. Other pages, and people who cannot edit, get the plain bar.
func TestAdminBarInlineEditorOnBuilderPages(t *testing.T) {
	e := newEnv(t, "../../themes")
	page := content.Item{ID: "01HZZ0000000000000000000AC", Type: "page", Slug: "home", Title: "Home", Status: "published", Template: "builder", Body: `<section id="gp-a" class="gp-section gp-hero"><h1>Hello</h1></section>`}
	rec, err := e.render(kernel.KindRequest{Kind: KindPage, Type: "page", Slug: "home"}, &Context{Content: &page}, adminCtx())
	if err != nil {
		t.Fatal(err)
	}
	body := rec.Body.String()
	for _, want := range []string{
		`data-admin-bar-builder="01HZZ0000000000000000000AC"`, `data-admin-bar-publish`, `data-admin-bar-labels="{`, `&#34;edit&#34;:&#34;تعديل في مكانه&#34;`,
		`<script defer src="` + AdminBarJSURL() + `" data-gorun-admin-bar-js></script></head>`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("builder page lacks %s", want)
		}
	}
	if strings.Contains(body, `data-admin-bar-csrf`) {
		t.Error("the CSRF token must not be printed into the page; draft.json hands it out")
	}

	// The same page for a contributor who did not write it: bar, no editor.
	contributor := auth.WithUser(context.Background(), &auth.User{ID: "u2", Role: "contributor", Status: auth.StatusActive, Name: "Contributor"})
	rec, _ = e.render(kernel.KindRequest{Kind: KindPage, Type: "page", Slug: "home"}, &Context{Content: &page}, contributor)
	if body := rec.Body.String(); strings.Contains(body, "data-admin-bar-builder") || strings.Contains(body, "data-gorun-admin-bar-js") {
		t.Error("contributor got the in-place editor for someone else's page")
	}

	// The Studio editor (texts only) gets the in-place editor and Publish
	// but not the full editor link.
	studio := auth.WithUser(context.Background(), &auth.User{ID: "u3", Role: "studio", Status: auth.StatusActive, Name: "Client"})
	rec, _ = e.render(kernel.KindRequest{Kind: KindPage, Type: "page", Slug: "home"}, &Context{Content: &page}, studio)
	body = rec.Body.String()
	if !strings.Contains(body, `data-admin-bar-builder="01HZZ0000000000000000000AC"`) || !strings.Contains(body, "data-gorun-admin-bar-js") || !strings.Contains(body, "data-admin-bar-publish") {
		t.Error("studio editor lacks the in-place editor")
	}
	if strings.Contains(body, "data-admin-bar-edit") || strings.Contains(body, `href="/admin/website"`) {
		t.Error("studio editor offered the full editor or the website overview")
	}

	// A plain page (no builder template) never loads the script.
	plain := content.Item{ID: "01HZZ0000000000000000000AD", Type: "page", Slug: "about", Title: "About", Status: "published", Body: "<p>hi</p>"}
	rec, _ = e.render(kernel.KindRequest{Kind: KindPage, Type: "page", Slug: "about"}, &Context{Content: &plain}, adminCtx())
	if body := rec.Body.String(); strings.Contains(body, "data-admin-bar-builder") || strings.Contains(body, "data-gorun-admin-bar-js") {
		t.Error("plain page got the in-place editor")
	}
	if len(AdminBarJS()) == 0 || !strings.HasPrefix(AdminBarJSURL(), AdminBarJSPath+"?v=") {
		t.Error("inline-edit.js is not embedded")
	}
}
