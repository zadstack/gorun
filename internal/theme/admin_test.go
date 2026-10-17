package theme

import (
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/zadstack/gorun/internal/content"
)

func adminThemes(t *testing.T) string {
	root := t.TempDir()
	writeTheme(t, root, "default", "", baseTheme(nil))
	writeTheme(t, root, "child", `{"name":"Child Theme","version":"2.1.0","parent":"default","locations":["primary"],"supports":["rtl"]}`,
		map[string]string{"templates/page.html": "CHILD-PAGE", "screenshot.png": "\x89PNG\r\n\x1a\nfake"})
	return root
}

func TestAdminThemesListAndActivate(t *testing.T) {
	e := newEnv(t, adminThemes(t))
	c := e.client()
	if resp, _ := c.get("/admin/appearance/themes"); resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("anonymous: %d", resp.StatusCode)
	}
	e.login(c, "admin@example.com", "admin")
	resp, page := c.get("/admin/appearance/themes")
	if resp.StatusCode != 200 {
		t.Fatalf("themes page: %d", resp.StatusCode)
	}
	mustContain(t, page, "Child Theme", "2.1.0", "/admin/appearance/themes/child/screenshot.png", `action="/admin/appearance/themes/child/activate"`, "/admin/appearance/templates")
	if resp, body := c.get("/admin/appearance/themes/child/screenshot.png"); resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "image/png" || body == "" {
		t.Fatalf("screenshot: %d %q", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	if resp, _ := c.get("/admin/appearance/themes/default/screenshot.png"); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("missing screenshot: %d", resp.StatusCode)
	}
	token := csrfFrom(t, page)
	if resp, _ := c.post("/admin/appearance/themes/child/activate", url.Values{}); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("activate without csrf: %d", resp.StatusCode)
	}
	resp, _ = c.post("/admin/appearance/themes/child/activate", url.Values{"_csrf": {token}})
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/admin/appearance/themes" {
		t.Fatalf("activate: %d %q", resp.StatusCode, resp.Header.Get("Location"))
	}
	if got := e.svc.Active(t.Context()).Slug; got != "child" {
		t.Fatalf("active = %q", got)
	}
	resp, _ = c.post("/admin/appearance/themes/nope/activate", url.Values{"_csrf": {token}})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("activate unknown: %d", resp.StatusCode)
	}
	if got := e.svc.Active(t.Context()).Slug; got != "child" {
		t.Fatalf("active after bad activate = %q", got)
	}

	// Editors lack themes.manage.
	c2 := e.client()
	e.login(c2, "editor@example.com", "editor")
	if resp, _ := c2.get("/admin/appearance/themes"); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("editor themes: %d", resp.StatusCode)
	}
	_, dash := c2.get("/admin")
	mustNotContain(t, dash, "/admin/appearance/themes")
}

func TestAdminTemplateOverrideFlow(t *testing.T) {
	e := newEnv(t, adminThemes(t))
	e.fake.Content.Add(content.Item{Type: "post", Slug: "x", Title: "Post X"})
	c := e.client()
	e.login(c, "admin@example.com", "admin")

	resp, page := c.get("/admin/appearance/templates")
	if resp.StatusCode != 200 {
		t.Fatalf("templates: %d", resp.StatusCode)
	}
	mustContain(t, page, "single.html", "parts.header.html", "/admin/appearance/templates/edit?name=single.html")

	resp, page = c.get("/admin/appearance/templates/edit?name=single.html")
	if resp.StatusCode != 200 {
		t.Fatalf("editor: %d", resp.StatusCode)
	}
	mustContain(t, page, "BASE-SINGLE", `name="body"`, "gp_preview_override=1", "template-editor.js", "data-code-source", `type="module"`)
	token := csrfFrom(t, page)

	// Save draft: not live, visible in editor and preview.
	resp, _ = c.post("/admin/appearance/templates/edit?name=single.html", url.Values{"_csrf": {token}, "action": {"draft"}, "body": {"DRAFT-BODY"}})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("save draft: %d", resp.StatusCode)
	}
	_, page = c.get("/admin/appearance/templates/edit?name=single.html")
	mustContain(t, page, "DRAFT-BODY")
	if got, _ := e.svc.ReadTemplate(t.Context(), "single.html"); got == "DRAFT-BODY" {
		t.Fatal("draft must not be published")
	}

	for _, action := range []string{"draft", "publish"} {
		resp, _ := c.post("/admin/appearance/templates/edit?name=single.html", url.Values{"_csrf": {token}, "action": {action}})
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("missing source (%s): %d", action, resp.StatusCode)
		}
		if draft, ok := e.svc.readDraft(t.Context(), "single.html"); !ok || draft != "DRAFT-BODY" {
			t.Fatalf("missing source changed draft: %q", draft)
		}
		if got, _ := e.svc.ReadTemplate(t.Context(), "single.html"); got == "" {
			t.Fatal("missing source cleared published template")
		}
	}

	// A template error is shown, not saved.
	resp, page = c.post("/admin/appearance/templates/edit?name=single.html", url.Values{"_csrf": {token}, "action": {"publish"}, "body": {"{{ nosuchfunc }}"}})
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("invalid publish: %d", resp.StatusCode)
	}
	mustContain(t, page, "nosuchfunc", `role="alert"`)

	// Publish makes it live.
	resp, _ = c.post("/admin/appearance/templates/edit?name=single.html", url.Values{"_csrf": {token}, "action": {"publish"}, "body": {"LIVE-BODY"}})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("publish: %d", resp.StatusCode)
	}
	if got, _ := e.svc.ReadTemplate(t.Context(), "single.html"); got != "LIVE-BODY" {
		t.Fatalf("published body = %q", got)
	}
	_, page = c.get("/admin/appearance/templates")
	mustContain(t, page, `data-source="override"`)
	if _, err := os.Stat(filepath.Join(e.cfg.DataDir, "overrides", "default", "templates", "single.html")); err != nil {
		t.Fatal(err)
	}

	// Delete restores the theme file.
	resp, _ = c.post("/admin/appearance/templates/edit?name=single.html", url.Values{"_csrf": {token}, "action": {"delete"}})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("delete: %d", resp.StatusCode)
	}
	if got, _ := e.svc.ReadTemplate(t.Context(), "single.html"); got == "LIVE-BODY" {
		t.Fatal("override not deleted")
	}
	if resp, _ := c.get("/admin/appearance/templates/edit?name=..%2Fx"); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("bad name: %d", resp.StatusCode)
	}
	// New templates can be created by name.
	resp, page = c.get("/admin/appearance/templates/edit?name=page-pricing.html")
	if resp.StatusCode != 200 {
		t.Fatalf("new template editor: %d", resp.StatusCode)
	}
}
