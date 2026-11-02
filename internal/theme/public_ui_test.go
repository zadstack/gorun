package theme

import (
	"context"
	"html"
	"net/url"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/config"
	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/menus"
)

func TestPublicActionNavigationBoundary(t *testing.T) {
	e, _ := compositionEnv(t, `{{with publicAction .Extra.action}}{{.State}}|{{.URL}}|{{.Label}}|{{.Reason}}{{end}}`)
	e.setting(SettingLocale, "en")
	for _, tc := range []struct{ url, state, want string }{
		{"/search?q=عمل&page=2#results", "", "available|http://example.test/ar/search?q=عمل&amp;page=2#results"},
		{"#details", "", "available|#details"},
		{"https://example.org/details?x=1", "", "available|https://example.org/details?x=1"},
		{"https://example.org", "disabled", "disabled||"},
		{"/account", "requires_setup", "requires_setup||"},
		{"javascript:alert(1)", "", "unavailable||"},
		{"//example.org", "", "unavailable||"},
		{"/%2fexample.org", "", "unavailable||"},
		{"https://user:password@example.org", "", "unavailable||"},
		{"/%0aexample.org", "", "unavailable||"},
		{"/safe", "arbitrary-state", "unavailable||"},
	} {
		out := renderIn(t, e, "ar", &Context{Extra: map[string]any{"action": map[string]any{"label": "<open>", "url": tc.url, "state": tc.state, "reason": "<reason>"}}})
		if !strings.HasPrefix(out, tc.want) {
			t.Errorf("%q/%s: %s", tc.url, tc.state, out)
		}
		mustContain(t, out, "&lt;open&gt;|&lt;reason&gt;")
	}
}

func TestPublicDetailExcludesDraftAndPrivateContent(t *testing.T) {
	e, cs := compositionEnv(t, `{{with publicDetail .Content}}{{.Title}}|{{.Locale}}|{{.Fallback}}{{else}}unavailable{{end}}`)
	ctx := context.Background()
	if err := cs.SaveType(ctx, content.Type{Slug: "private-record", Name: map[string]string{"en": "Private"}, Public: false, Supports: []string{"title", "body"}}); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ typ, status, locale, want string }{
		{"page", "published", "ar", "Visible|ar|false"},
		{"page", "published", "en", "Visible|en|true"},
		{"page", "draft", "ar", "unavailable"},
		{"private-record", "published", "ar", "unavailable"},
	} {
		it := mustItem(t, cs, content.Input{Type: tc.typ, Title: "Visible", Locale: tc.locale, Status: content.Status(tc.status)})
		if got := renderIn(t, e, "ar", &Context{Content: it}); got != tc.want {
			t.Errorf("%s/%s/%s: %s", tc.typ, tc.status, tc.locale, got)
		}
	}
	blocked := mustItem(t, cs, content.Input{Type: "page", Title: "Protected summary", Locale: "ar", Status: "published"})
	policies := cs.(content.PageAccessService)
	if _, err := policies.SavePageAccess(adminCtx(), blocked.ID, content.PageAccessInput{Mode: "blocked"}, 0); err != nil {
		t.Fatal(err)
	}
	if got := renderIn(t, e, "ar", &Context{Content: blocked}); got != "unavailable" {
		t.Fatalf("blocked public item leaked: %s", got)
	}
	rec, err := e.render(kernel.KindRequest{Kind: "index"}, &Context{Locale: "ar", Content: blocked}, adminCtx())
	if err != nil {
		t.Fatal(err)
	}
	if rec.Body.String() != "unavailable" {
		t.Fatal("staff context must not make a protected detail public")
	}

}

func TestDefaultPublicUIDetailAndChildOptIn(t *testing.T) {
	root := t.TempDir()
	if err := os.CopyFS(root, os.DirFS("../../themes")); err != nil {
		t.Fatal(err)
	}
	e := newEnv(t, root)
	it := e.fake.Content.Add(content.Item{Type: "page", Title: strings.Repeat("تفاصيل ", 30) + "<script>", Slug: "record", Locale: "ar", Template: "public-detail", Body: "<p>Public body</p>"})
	out, err := e.render(kernel.KindRequest{Kind: KindPage, Type: "page", Slug: "record", Template: "public-detail"}, &Context{Locale: "ar", Content: &it, Query: map[string]string{"back": "/ar/search?q=عمل&page=2#results"}})
	if err != nil {
		t.Fatal(err)
	}
	body := out.Body.String()
	back := regexp.MustCompile(`class="gp-public-action" href="([^"]+)"`).FindStringSubmatch(body)
	if len(back) != 2 {
		t.Fatal("missing return action")
	}
	target, parseErr := url.Parse(html.UnescapeString(back[1]))
	if parseErr != nil {
		t.Fatal(parseErr)
	}
	if target.Path != "/ar/search" || target.Query().Get("q") != "عمل" || target.Query().Get("page") != "2" || target.Fragment != "results" {
		t.Fatalf("return lost discovery state: %s", target)
	}

	mustContain(t, body, "public-ui.css", `class="gp-public-detail"`, `id="public-content" tabindex="-1"`, "&lt;script&gt;")
	rec, err := e.render(kernel.KindRequest{Kind: KindSearch}, &Context{Locale: "ar", Query: map[string]string{"q": "missing"}})
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, rec.Body.String(), `class="gp-public-state"`, `data-state="empty"`)
	// A child inheriting the template does not inherit the opt-in declaration.
	writeTheme(t, root, "legacy-child", `{"name":"Legacy","parent":"default"}`, map[string]string{})
	child := newEnv(t, root, func(c *config.Config) { c.Theme = "legacy-child" })
	rec, err = child.render(kernel.KindRequest{Kind: KindSearch}, &Context{Query: map[string]string{"q": "missing"}})
	if err != nil {
		t.Fatal(err)
	}
	mustNotContain(t, rec.Body.String(), "public-ui.css", "gp-public-state")
	// A custom override still wins over the new explicit page template.
	writeTheme(t, root, "custom", `{"name":"Custom","parent":"default","supports":["public-ui"]}`, map[string]string{"templates/page-public-detail.html": "CUSTOM DETAIL"})
	custom := newEnv(t, root, func(c *config.Config) { c.Theme = "custom" })
	rec, err = custom.render(kernel.KindRequest{Kind: KindPage, Template: "public-detail"}, &Context{Content: &it})
	if err != nil {
		t.Fatal(err)
	}
	if rec.Body.String() != "CUSTOM DETAIL" {
		t.Fatal(rec.Body.String())
	}
}

func TestPublicComponentsKeepSecondaryInformationAndInactiveActionsSafe(t *testing.T) {
	root := t.TempDir()
	if err := os.CopyFS(root, os.DirFS("../../themes")); err != nil {
		t.Fatal(err)
	}
	writeTheme(t, root, "fixture", `{"name":"Fixture","parent":"default","supports":["public-ui"]}`, map[string]string{"templates/front-page.html": `{{part "header" .}}{{component "public-navigation" (publicNavigation "primary")}}{{component "public-detail" (dict "View" (publicDetail .Content) "SecondaryTitle" "More details" "SecondaryText" "<img src=x onerror=alert(1)>" "Action" (dict "label" "Request" "url" "/do-not-follow" "state" "disabled"))}}{{part "footer" .}}`})
	e := newEnv(t, root, func(c *config.Config) { c.Theme = "fixture" })
	if _, err := e.fake.Menus.Save(context.Background(), menus.Menu{Slug: "primary", Location: "primary", Items: []menus.Item{{Label: "Home", Kind: "custom", URL: "/"}}}); err != nil {
		t.Fatal(err)
	}
	it := e.fake.Content.Add(content.Item{Type: "page", Slug: "guide", Title: "Guide", Body: "Useful description", Locale: "ar"})
	rec, err := e.render(kernel.KindRequest{Kind: KindHome}, &Context{Content: &it})
	if err != nil {
		t.Fatal(err)
	}
	body := rec.Body.String()
	mustContain(t, body, `<details><summary>More details</summary>`, `&lt;img src=x onerror=alert(1)&gt;`, `aria-disabled="true" data-state="disabled"`, `هذا الإجراء غير متاح حاليًا.`)
	mustContain(t, body, `aria-current="page">Home</a>`)
	mustNotContain(t, body, `href="/do-not-follow"`, `<img src=x`)
}
