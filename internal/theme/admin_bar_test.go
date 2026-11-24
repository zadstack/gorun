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
	if got := string(injectAdminBar([]byte("<p>no body tag</p>"), bar)); got != "<p>no body tag</p>" {
		t.Fatalf("page without <body> changed: %q", got)
	}
	got := string(injectAdminBar([]byte(`<html><head><title>x</title></head><BODY class="a">hi</BODY></html>`), bar))
	if !strings.Contains(got, `data-gorun-admin-bar-css></head>`) || !strings.Contains(got, `<BODY class="a">`+bar+`hi`) {
		t.Fatalf("injection = %q", got)
	}
}
