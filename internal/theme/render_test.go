package theme

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/audit"
	"github.com/zadstack/gorun/internal/config"
	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/kernel"
)

func baseTheme(extra map[string]string) map[string]string {
	files := map[string]string{
		"templates/index.html":  `{{part "header"}}INDEX{{part "footer"}}`,
		"templates/single.html": `{{part "header"}}BASE-SINGLE {{.Content.Title}}{{part "footer"}}`,
		"parts/header.html":     `<html dir="{{dir}}" lang="{{locale}}"><body>BASE-HEADER `,
		"parts/footer.html":     ` BASE-FOOTER</body></html>`,
	}
	for k, v := range extra {
		files[k] = v
	}
	return files
}

func TestChildThemeFallsBackToParent(t *testing.T) {
	root := t.TempDir()
	writeTheme(t, root, "base", "", baseTheme(nil))
	writeTheme(t, root, "child", `{"name":"Child","parent":"base"}`, map[string]string{
		"templates/page.html": `{{part "header"}}CHILD-PAGE{{part "footer"}}`,
		"parts/footer.html":   ` CHILD-FOOTER</body></html>`,
	})
	e := newEnv(t, root, func(c *config.Config) { c.Theme = "child" })

	rec, err := e.render(kernel.KindRequest{Kind: KindSingle, Type: "post", Slug: "x"}, &Context{Content: &content.Item{Title: "Hello"}})
	if err != nil || rec.Code != 200 {
		t.Fatalf("single: %d %v", rec.Code, err)
	}
	mustContain(t, rec.Body.String(), "BASE-HEADER", "BASE-SINGLE Hello", "CHILD-FOOTER")
	mustNotContain(t, rec.Body.String(), "BASE-FOOTER")

	rec, _ = e.render(kernel.KindRequest{Kind: KindPage, Slug: "about"}, nil)
	mustContain(t, rec.Body.String(), "CHILD-PAGE")

	files, err := e.svc.Templates(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	src := map[string]string{}
	for _, f := range files {
		src[f.Name] = f.Source
	}
	if src["page.html"] != "theme" || src["single.html"] != "parent" || src["parts.footer.html"] != "theme" || src["parts.header.html"] != "parent" {
		t.Fatalf("sources: %v", src)
	}
	if got, _ := e.svc.Resolve(kernel.KindRequest{Kind: KindPage, Slug: "x"}); got != "page.html" {
		t.Fatalf("resolve page = %q", got)
	}

	// Activation validates theme.json and the parent chain.
	writeTheme(t, root, "orphan", `{"name":"Orphan","parent":"missing"}`, map[string]string{"templates/index.html": "x"})
	writeTheme(t, root, "noindex", "", map[string]string{"templates/single.html": "x"})
	writeTheme(t, root, "badjson", `{"name":`, map[string]string{})
	for slug, want := range map[string]error{"orphan": ErrInvalidTheme, "noindex": ErrInvalidTheme, "badjson": ErrInvalidTheme, "nope": ErrNotFound, "../etc": ErrNotFound} {
		if err := e.svc.Activate(t.Context(), slug); !errors.Is(err, want) {
			t.Fatalf("activate %s: %v, want %v", slug, err, want)
		}
	}
	if err := e.svc.Activate(t.Context(), "base"); err != nil {
		t.Fatal(err)
	}
	if got := e.svc.Active(t.Context()).Slug; got != "base" {
		t.Fatalf("active = %q", got)
	}
	list, _ := e.svc.List(t.Context())
	if len(list) != 4 { // base, child, noindex, orphan (badjson skipped)
		t.Fatalf("list: %+v", list)
	}
}

func TestOverrideWinsOverTheme(t *testing.T) {
	root := t.TempDir()
	writeTheme(t, root, "default", "", baseTheme(nil))
	// prod: the parsed set is cached, so saving must invalidate it.
	e := newEnv(t, root, func(c *config.Config) { c.Env = "prod" })
	single := kernel.KindRequest{Kind: KindSingle, Type: "post", Slug: "x"}
	tc := func() *Context { return &Context{Content: &content.Item{Title: "T"}} }

	rec, _ := e.render(single, tc())
	mustContain(t, rec.Body.String(), "BASE-SINGLE T")

	if err := e.svc.SaveOverride(t.Context(), "single.html", `OVERRIDE {{.Content.Title}}`, false); err != nil {
		t.Fatal(err)
	}
	rec, _ = e.render(single, tc())
	mustContain(t, rec.Body.String(), "OVERRIDE T")
	if err := e.svc.SaveOverride(t.Context(), "parts.header.html", `<p>HDR-OVERRIDE</p>`, false); err != nil {
		t.Fatal(err)
	}
	rec, _ = e.render(kernel.KindRequest{Kind: Kind404}, nil)
	mustContain(t, rec.Body.String(), "HDR-OVERRIDE", "INDEX")

	body, err := e.svc.ReadTemplate(t.Context(), "single.html")
	if err != nil || body != `OVERRIDE {{.Content.Title}}` {
		t.Fatalf("read: %q %v", body, err)
	}
	files, _ := e.svc.Templates(t.Context())
	for _, f := range files {
		if f.Name == "single.html" && f.Source != "override" {
			t.Fatalf("source: %+v", f)
		}
	}
	// Invalid names and broken templates are rejected.
	if err := e.svc.SaveOverride(t.Context(), "../../etc/passwd", "x", false); !errors.Is(err, ErrInvalidName) {
		t.Fatalf("traversal: %v", err)
	}
	if err := e.svc.SaveOverride(t.Context(), "single.html", `{{ nosuchfunc }}`, false); !errors.Is(err, ErrInvalidTemplate) {
		t.Fatalf("invalid template: %v", err)
	}
	if err := e.svc.DeleteOverride(t.Context(), "single.html"); err != nil {
		t.Fatal(err)
	}
	rec, _ = e.render(single, tc())
	mustContain(t, rec.Body.String(), "BASE-SINGLE T")
	if err := e.svc.DeleteOverride(t.Context(), "single.html"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("delete missing: %v", err)
	}
	entries, _, _ := e.app.Deps.Audit.List(t.Context(), auditQuery("theme.override.save"))
	if len(entries) != 2 {
		t.Fatalf("audit entries: %d", len(entries))
	}
}

func TestDraftOverrideNotServed(t *testing.T) {
	root := t.TempDir()
	writeTheme(t, root, "default", "", baseTheme(nil))
	e := newEnv(t, root, func(c *config.Config) { c.Env = "prod" })
	single := kernel.KindRequest{Kind: KindSingle, Type: "post", Slug: "x"}
	tc := func(url string) *Context {
		return &Context{Content: &content.Item{Title: "T"}, Request: httptest.NewRequest(http.MethodGet, url, nil)}
	}
	if err := e.svc.SaveOverride(t.Context(), "single.html", `DRAFT {{.Content.Title}}`, true); err != nil {
		t.Fatal(err)
	}
	rec, _ := e.render(single, tc("/post/x"))
	mustNotContain(t, rec.Body.String(), "DRAFT")
	mustContain(t, rec.Body.String(), "BASE-SINGLE")

	// Preview param without themes.manage: still the published version.
	rec, _ = e.render(single, tc("/post/x?gp_preview_override=1"))
	mustNotContain(t, rec.Body.String(), "DRAFT")
	// Admin preview sees the draft.
	rec, _ = e.render(single, tc("/post/x?gp_preview_override=1"), adminCtx())
	mustContain(t, rec.Body.String(), "DRAFT T")
	// Admin without the preview param: published.
	rec, _ = e.render(single, tc("/post/x"), adminCtx())
	mustNotContain(t, rec.Body.String(), "DRAFT")

	ov, _ := e.svc.Overrides(t.Context())
	if len(ov) != 1 || !ov[0].Draft || ov[0].Name != "single.html" || ov[0].UpdatedAt == "" {
		t.Fatalf("overrides: %+v", ov)
	}
	if err := e.svc.PublishOverride(t.Context(), "single.html"); err != nil {
		t.Fatal(err)
	}
	rec, _ = e.render(single, tc("/post/x"))
	mustContain(t, rec.Body.String(), "DRAFT T")
	ov, _ = e.svc.Overrides(t.Context())
	if len(ov) != 1 || ov[0].Draft {
		t.Fatalf("after publish: %+v", ov)
	}
	if err := e.svc.PublishOverride(t.Context(), "single.html"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("publish without draft: %v", err)
	}
}

func TestRenderTemplateErrorShows500Page(t *testing.T) {
	root := t.TempDir()
	writeTheme(t, root, "default", "", baseTheme(map[string]string{
		// parse-time failure: unknown func
		"templates/single.html": `{{part "header"}}SINGLE {{ nosuchfunc . }}{{part "footer"}}`,
		// exec-time failure after output was produced
		"templates/archive.html": `PARTIAL-OUTPUT {{part "header"}}{{ index .Items 5 }}`,
		// broken part used by a template
		"parts/broken.html":   `{{ if }}`,
		"templates/page.html": `BEFORE-BROKEN {{part "broken"}}`,
	}))
	e := newEnv(t, root)

	rec, err := e.render(kernel.KindRequest{Kind: KindSingle, Type: "post", Slug: "x"}, &Context{Content: &content.Item{Title: "T"}})
	if err == nil || rec.Code != http.StatusInternalServerError {
		t.Fatalf("parse error: %d %v", rec.Code, err)
	}
	mustContain(t, rec.Body.String(), Marker500)
	mustNotContain(t, rec.Body.String(), "SINGLE", "INDEX")
	mustContain(t, e.logs.String(), "level=ERROR", "theme: render failed", "nosuchfunc")

	rec, err = e.render(kernel.KindRequest{Kind: KindArchive, Type: "post"}, nil)
	if err == nil || rec.Code != http.StatusInternalServerError {
		t.Fatalf("exec error: %d %v", rec.Code, err)
	}
	mustContain(t, rec.Body.String(), Marker500)
	mustNotContain(t, rec.Body.String(), "PARTIAL-OUTPUT", "BASE-HEADER")
	mustContain(t, e.logs.String(), "index out of range")

	rec, _ = e.render(kernel.KindRequest{Kind: KindPage, Slug: "x"}, nil)
	if rec.Code != 500 {
		t.Fatalf("broken part: %d", rec.Code)
	}
	mustNotContain(t, rec.Body.String(), "BEFORE-BROKEN")

	// A themed 500.html is used when it renders; a broken one falls back.
	writeTheme(t, root, "default", "", baseTheme(map[string]string{
		"templates/single.html": `{{ nosuchfunc }}`,
		"templates/500.html":    `{{part "header"}}THEMED-500 status={{.Status}}{{part "footer"}}`,
	}))
	rec, _ = e.render(kernel.KindRequest{Kind: KindSingle, Type: "post", Slug: "x"}, nil)
	if rec.Code != 500 {
		t.Fatalf("themed 500: %d", rec.Code)
	}
	mustContain(t, rec.Body.String(), "THEMED-500 status=500", "BASE-HEADER", Marker500)
	writeTheme(t, root, "default", "", baseTheme(map[string]string{
		"templates/single.html": `{{ nosuchfunc }}`,
		"templates/500.html":    `HALF {{ index .Items 3 }}`,
	}))
	rec, _ = e.render(kernel.KindRequest{Kind: KindSingle, Type: "post", Slug: "x"}, nil)
	mustContain(t, rec.Body.String(), Marker500)
	mustNotContain(t, rec.Body.String(), "HALF")

	// No theme at all: still a 500 page, not a blank response.
	e.setting(SettingTheme, "missing")
	rec, _ = e.render(kernel.KindRequest{Kind: KindHome}, nil)
	if rec.Code != 500 || !strings.Contains(rec.Body.String(), Marker500) {
		t.Fatalf("missing theme: %d %s", rec.Code, rec.Body.String())
	}
}

func TestRTLDirFromLocale(t *testing.T) {
	root := t.TempDir()
	writeTheme(t, root, "default", "", baseTheme(map[string]string{"lang/en.json": `{"hello":"Hello"}`, "lang/ar.json": `{"hello":"مرحبا"}`,
		"templates/index.html": `{{part "header"}}{{t "hello"}} {{t "core.app.name"}} {{.Site.Dir}}{{part "footer"}}`}))
	e := newEnv(t, root)
	rec, _ := e.render(kernel.KindRequest{Kind: KindHome}, nil)
	mustContain(t, rec.Body.String(), `dir="rtl"`, `lang="ar"`, "مرحبا", "rtl")
	e.setting(SettingLocale, "en")
	rec, _ = e.render(kernel.KindRequest{Kind: KindHome}, nil)
	mustContain(t, rec.Body.String(), `dir="ltr"`, `lang="en"`, "Hello GoRun", "ltr")
}

func auditQuery(action string) audit.ListQuery { return audit.ListQuery{Action: action} }
