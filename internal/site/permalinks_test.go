package site_test

import (
	"context"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/config"
	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/media"
	"github.com/zadstack/gorun/internal/menus"
	"github.com/zadstack/gorun/internal/seo"
	"github.com/zadstack/gorun/internal/settings/settingsmod"
	"github.com/zadstack/gorun/internal/site"
	"github.com/zadstack/gorun/internal/taxonomy"
	"github.com/zadstack/gorun/internal/theme"
)

// bootReal starts the real content stack (no fakes) with the repo themes.
func bootReal(t *testing.T, extra ...kernel.Module) *env {
	t.Helper()
	cfg := config.Config{Env: "dev", DataDir: t.TempDir(), Addr: ":0", SecretKey: make([]byte, 32), BaseURL: "http://example.test", Theme: "default"}
	modules := append([]kernel.Module{settingsmod.New(), auth.New(), content.New(), taxonomy.New(), media.New(), menus.New()}, extra...)
	modules = append(modules, seo.New(), theme.New(theme.WithThemesDir("../../themes")), site.New())
	app, err := kernel.Boot(context.Background(), cfg, modules)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(app.Handler)
	t.Cleanup(func() { srv.Close(); app.Close() })
	jar, _ := cookiejar.New(nil)
	c := &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return &env{t: t, app: app, srv: srv, c: c}
}

func publish(t *testing.T, cs content.Service, in content.Input) *content.Item {
	t.Helper()
	in.Status = "published"
	it, err := cs.Create(context.Background(), in)
	if err != nil {
		t.Fatalf("create %s: %v", in.Slug, err)
	}
	return it
}

// I1: the sitemap and content menu items use the theme permalinks, and
// those permalinks answer 200.
func TestSitemapAndMenuUsePermalinks(t *testing.T) {
	e := bootReal(t)
	ctx := context.Background()
	cs, _ := kernel.Get[content.Service](e.app.Deps, kernel.ServiceContent)
	post := publish(t, cs, content.Input{Type: "post", Slug: "hello-world", Title: "Hello world", Body: "<p>x</p>"})
	parent := publish(t, cs, content.Input{Type: "page", Slug: "company", Title: "Company"})
	publish(t, cs, content.Input{Type: "page", Slug: "team", Title: "Team", ParentID: parent.ID})

	_, xml := e.get("/sitemap.xml")
	mustContain(t, xml, "<loc>http://example.test/post/hello-world</loc>", "<loc>http://example.test/company/team</loc>",
		"<loc>http://example.test/company</loc>")
	mustNotContain(t, xml, "<loc>http://example.test/hello-world</loc>", "<loc>http://example.test/team</loc>")

	for _, p := range []string{"/post/hello-world", "/company/team"} {
		if resp, body := e.get(p); resp.StatusCode != http.StatusOK {
			t.Fatalf("GET %s: %d\n%s", p, resp.StatusCode, body)
		}
	}

	ms, _ := kernel.Get[menus.Service](e.app.Deps, kernel.ServiceMenus)
	m, err := ms.Save(ctx, menus.Menu{Slug: "main", Name: "Main", Location: "primary"})
	if err != nil {
		t.Fatal(err)
	}
	if err := ms.SaveItems(ctx, m.ID, []menus.Item{
		{Kind: menus.KindContent, RefID: post.ID},
		{Kind: menus.KindCustom, Label: "Custom", URL: "/custom-link"},
	}); err != nil {
		t.Fatal(err)
	}
	got, err := ms.ByLocation(ctx, "primary")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Items) != 2 || got.Items[0].URL != "/post/hello-world" || got.Items[1].URL != "/custom-link" {
		t.Fatalf("menu items: %+v", got.Items)
	}
	_, home := e.get("/")
	mustContain(t, home, `/post/hello-world"`, `/custom-link"`)
	if strings.Contains(home, `example.test/hello-world"`) || strings.Contains(home, `href="/hello-world"`) {
		t.Fatalf("home links the fallback path:\n%s", home)
	}
}
