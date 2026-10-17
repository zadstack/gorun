package theme

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/menus"
)

func TestOverridesRejectInvalidActiveThemeSlug(t *testing.T) {
	root := t.TempDir()
	writeTheme(t, root, "default", "", baseTheme(nil))
	e := newEnv(t, root)
	e.setting(SettingTheme, "../../x")
	if err := e.svc.SaveOverride(t.Context(), "single.html", "X", false); !errors.Is(err, ErrInvalidTheme) {
		t.Fatalf("save: %v", err)
	}
	if err := e.svc.SaveOverride(t.Context(), "single.html", "X", true); !errors.Is(err, ErrInvalidTheme) {
		t.Fatalf("save draft: %v", err)
	}
	if err := e.svc.DeleteOverride(t.Context(), "single.html"); !errors.Is(err, ErrInvalidTheme) {
		t.Fatalf("delete: %v", err)
	}
	if err := e.svc.PublishOverride(t.Context(), "single.html"); !errors.Is(err, ErrInvalidTheme) {
		t.Fatalf("publish: %v", err)
	}
	if _, err := e.svc.Overrides(t.Context()); !errors.Is(err, ErrInvalidTheme) {
		t.Fatalf("overrides: %v", err)
	}
	// Nothing escaped the data dir.
	if _, err := os.Stat(filepath.Join(e.cfg.DataDir, "..", "x")); err == nil {
		t.Fatal("wrote outside the overrides dir")
	}
}

func TestSaveOverrideRejectsEscaperErrors(t *testing.T) {
	root := t.TempDir()
	writeTheme(t, root, "default", "", baseTheme(nil))
	e := newEnv(t, root)
	// Parses fine, but html/template's escaper rejects it on first execute.
	bad := `<a href="{{.Kind}}`
	for _, draft := range []bool{false, true} {
		if err := e.svc.SaveOverride(t.Context(), "single.html", bad, draft); !errors.Is(err, ErrInvalidTemplate) {
			t.Fatalf("draft=%v: %v", draft, err)
		}
	}
	// Runtime errors from empty data are not template errors.
	for _, ok := range []string{`{{.Content.Title}}`, `{{index .Items 3}}`, `{{part "header" .}}x{{range .Items}}{{.Title}}{{end}}`, `<a href="{{url .Content}}">x</a>`} {
		if err := e.svc.SaveOverride(t.Context(), "single.html", ok, true); err != nil {
			t.Fatalf("valid %q rejected: %v", ok, err)
		}
	}
}

func TestEscaperErrorsAndDeleteViaAdminAndAPI(t *testing.T) {
	e := newEnv(t, adminThemes(t))
	for _, op := range e.app.Deps.API.Operations() {
		e.app.Deps.Router.API.Method(op.Method, op.Path, op.Handler)
	}
	c := e.client()
	token := e.login(c, "admin@example.com", "admin")
	_, page := c.get("/admin/appearance/templates/edit?name=single.html")
	form := url.Values{"_csrf": {csrfFrom(t, page)}, "action": {"publish"}, "body": {`<a href="{{.Kind}}`}}
	resp, page := c.post("/admin/appearance/templates/edit?name=single.html", form)
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("admin escaper: %d", resp.StatusCode)
	}
	mustContain(t, page, `role="alert"`, "تعذّر حفظ الملف")
	mustNotContain(t, page, "theme: invalid template") // internal prefix stays in logs
	form.Set("action", "delete")
	resp, page = c.post("/admin/appearance/templates/edit?name=single.html", form)
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("admin delete missing: %d", resp.StatusCode)
	}
	mustContain(t, page, "لا يوجد تخصيص لهذا الملف")
	mustNotContain(t, page, "theme: not found")

	hdr := []string{"Content-Type", "application/json", "X-CSRF-Token", token}
	resp, body := c.do("PUT", "/api/v1/templates/single.html/draft", strings.NewReader(`{"body":"<a href=\"{{.Kind}}"}`), hdr...)
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("api escaper: %d %s", resp.StatusCode, body)
	}
	if resp, body := c.do("DELETE", "/api/v1/templates/single.html/override", nil, hdr...); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("api delete missing: %d %s", resp.StatusCode, body)
	}
	c.do("PUT", "/api/v1/templates/single.html/draft", strings.NewReader(`{"body":"D"}`), hdr...)
	c.do("POST", "/api/v1/templates/single.html/publish", nil, hdr...)
	if resp, body := c.do("DELETE", "/api/v1/templates/single.html/override", nil, hdr...); resp.StatusCode != 200 || !strings.Contains(body, "BASE-SINGLE") {
		t.Fatalf("api delete: %d %s", resp.StatusCode, body)
	}
	if got, _ := e.svc.ReadTemplate(t.Context(), "single.html"); got == "D" {
		t.Fatal("override still live")
	}
}

func TestPreviewIsNotCacheableAndMenuUsesHook(t *testing.T) {
	root := t.TempDir()
	writeTheme(t, root, "default", "", baseTheme(map[string]string{
		"templates/index.html": `{{with menu "primary"}}{{range .Items}}[{{.URL}}]{{end}}{{end}}`}))
	e := newEnv(t, root)
	e.app.Deps.Hooks.Filter(HookMenuResolveURL, 1, func(_ context.Context, v any) (any, error) {
		if it, ok := v.(*menus.Item); ok && it.Kind == "custom-kind" {
			it.URL = "/from-hook"
		}
		return v, nil
	})
	e.fake.Menus.Save(t.Context(), menus.Menu{Location: "primary", Items: []menus.Item{{Kind: "custom-kind", RefID: "x"}, {Kind: "archive", RefID: "post"}}})
	rec, _ := e.render(kernel.KindRequest{Kind: KindHome}, nil)
	mustContain(t, rec.Body.String(), "[/from-hook][/blog/]")
	if cc := rec.Header().Get("Cache-Control"); cc != "" {
		t.Fatalf("normal render Cache-Control %q", cc)
	}
	tc := &Context{Request: httptest.NewRequest("GET", "/?gp_preview_override=1", nil)}
	rec, _ = e.render(kernel.KindRequest{Kind: KindHome}, tc, adminCtx())
	if csp := rec.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "sandbox") || !strings.Contains(csp, "script-src 'none'") {
		t.Fatalf("draft preview executes scripts: %q", csp)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "private, no-store" {
		t.Fatalf("preview Cache-Control %q", cc)
	}
}
