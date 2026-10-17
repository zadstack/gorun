package ext_test

import (
	"bytes"
	"context"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/api/apitest"
	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/ext"
	"github.com/zadstack/gorun/internal/kernel"
)

type browser struct {
	t    *testing.T
	srv  *httptest.Server
	c    *http.Client
	csrf string
}

var csrfRe = regexp.MustCompile(`name="csrf-token" content="([^"]+)"`)

func login(t *testing.T, e *env, email, role string) *browser {
	t.Helper()
	srv := httptest.NewServer(e.app.Handler)
	t.Cleanup(srv.Close)
	users, _ := kernel.Get[auth.Service](e.app.Deps, kernel.ServiceAuth)
	if email != "admin@example.com" {
		if _, err := users.Create(context.Background(), email, "U", "correct horse battery staple", role); err != nil {
			t.Fatal(err)
		}
	}
	jar, _ := cookiejar.New(nil)
	b := &browser{t: t, srv: srv, c: &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
	_, page := b.do("GET", "/admin/login", nil, "")
	b.csrf = csrfRe.FindStringSubmatch(page)[1]
	if resp, _ := b.form("/admin/login", url.Values{"email": {email}, "password": {"correct horse battery staple"}}); resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("login: %d", resp.StatusCode)
	}
	_, page = b.do("GET", "/admin", nil, "")
	b.csrf = csrfRe.FindStringSubmatch(page)[1]
	return b
}

func (b *browser) do(method, path string, body io.Reader, ctype string, hdr ...string) (*http.Response, string) {
	b.t.Helper()
	req, _ := http.NewRequest(method, b.srv.URL+path, body)
	if ctype != "" {
		req.Header.Set("Content-Type", ctype)
	}
	if b.csrf != "" {
		req.Header.Set("X-CSRF-Token", b.csrf)
	}
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	resp, err := b.c.Do(req)
	if err != nil {
		b.t.Fatal(err)
	}
	data, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	return resp, string(data)
}

func (b *browser) form(path string, v url.Values) (*http.Response, string) {
	v.Set("_csrf", b.csrf)
	return b.do("POST", path, strings.NewReader(v.Encode()), "application/x-www-form-urlencoded")
}

func (b *browser) upload(path string, zipData []byte) (*http.Response, string) {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, _ := mw.CreateFormFile("package", "ext.zip")
	fw.Write(zipData)
	mw.Close()
	return b.do("POST", path, &buf, mw.FormDataContentType(), "HX-Request", "true")
}

func TestAdminExtensionsFlow(t *testing.T) {
	e := boot(t)
	b := login(t, e, "admin@example.com", "admin")
	resp, page := b.do("GET", "/admin/extensions", nil, "")
	if resp.StatusCode != 200 || !strings.Contains(page, `hx-encoding="multipart/form-data"`) || !strings.Contains(page, `href="/admin/extensions"`) {
		t.Fatalf("list: %d\n%s", resp.StatusCode, page)
	}
	if resp, page := b.upload("/admin/extensions/install", []byte("garbage")); resp.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(page, "field-error") {
		t.Fatalf("bad upload: %d\n%s", resp.StatusCode, page)
	}
	raw := `{"slug":"shop-sync","name":{"en":"Shop sync","ar":"مزامنة المتجر"},"version":"2.0.0","permissions":["content.view"],
		"hooks":[{"name":"content.published","kind":"event"}],"endpoint":{"url":"http://127.0.0.1:1/x","auth":"hmac"},
		"settings":[{"key":"region","label":{"en":"Region"},"type":"text","default":"eu"}],
		"admin_nav":[{"label":{"en":"Shop dashboard"},"url":"https://shop.example.com/"}]}`
	resp, page = b.upload("/admin/extensions/install", zipOf(t, map[string]string{"manifest.json": raw}))
	if resp.StatusCode != http.StatusCreated || !strings.Contains(page, `hx-history="false"`) || !strings.Contains(page, `data-reveal-secret`) || !strings.Contains(page, `value="gp_`) {
		t.Fatalf("install: %d\n%s", resp.StatusCode, page)
	}
	resp, page = b.do("GET", "/admin/extensions/shop-sync", nil, "")
	if resp.StatusCode != 200 || strings.Contains(page, "data-reveal-secret") || !strings.Contains(page, "content.published") ||
		!strings.Contains(page, "https://shop.example.com/") || !strings.Contains(page, `name="region"`) {
		t.Fatalf("detail: %d\n%s", resp.StatusCode, page)
	}
	if resp, _ := b.form("/admin/extensions/shop-sync/activate", url.Values{}); resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("activate: %d", resp.StatusCode)
	}
	if x := e.status("shop-sync"); x.Status != ext.StatusActive {
		t.Fatalf("status = %s", x.Status)
	}
	if resp, _ := b.form("/admin/extensions/shop-sync/settings", url.Values{"region": {"me"}}); resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("settings: %d", resp.StatusCode)
	}
	if v := e.app.Deps.Settings.GetString(context.Background(), "ext.shop-sync.settings.region", ""); v != "me" {
		t.Fatalf("setting = %q", v)
	}
	if resp, _ := b.form("/admin/extensions/shop-sync/deactivate", url.Values{}); resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("deactivate: %d", resp.StatusCode)
	}
	if x := e.status("shop-sync"); x.Status != ext.StatusDisabled {
		t.Fatalf("status = %s", x.Status)
	}
	_, page = b.do("GET", "/admin/extensions", nil, "")
	if !strings.Contains(page, "shop-sync") || !strings.Contains(page, `data-status="disabled"`) {
		t.Fatalf("list after install:\n%s", page)
	}
	if resp, _ := b.form("/admin/extensions/shop-sync/uninstall", url.Values{"purge": {"1"}}); resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("uninstall: %d", resp.StatusCode)
	}
	if _, err := e.svc.Status(context.Background(), "shop-sync"); err == nil {
		t.Fatal("still installed")
	}
	if resp, _ := b.do("GET", "/admin/extensions/shop-sync", nil, ""); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("detail after uninstall: %d", resp.StatusCode)
	}

	// Editors cannot manage extensions.
	ed := login(t, e, "ed@example.com", "editor")
	if resp, _ := ed.do("GET", "/admin/extensions", nil, ""); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("editor: %d", resp.StatusCode)
	}
}

func TestAdminInstallFromDataDirAndHealth(t *testing.T) {
	e := boot(t)
	// A package dropped into <DataDir>/extensions/<slug>/ is offered for install.
	dir := e.app.Deps.Config.DataDir + "/extensions/dropped"
	if err := copyDir(writeManifest(t, manifest("dropped", "http://127.0.0.1:1/x")), dir); err != nil {
		t.Fatal(err)
	}
	b := login(t, e, "admin@example.com", "admin")
	_, page := b.do("GET", "/admin/extensions", nil, "")
	if !strings.Contains(page, `name="slug" value="dropped"`) {
		t.Fatalf("available package not offered:\n%s", page)
	}
	resp, page := b.form("/admin/extensions/install-dir", url.Values{"slug": {"dropped"}})
	if resp.StatusCode != http.StatusCreated || !strings.Contains(page, "data-reveal-secret") {
		t.Fatalf("install-dir: %d\n%s", resp.StatusCode, page)
	}
	if resp, _ := b.form("/admin/extensions/install-dir", url.Values{"slug": {"../../etc"}}); resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("traversal slug: %d", resp.StatusCode)
	}
	_, page = b.do("GET", "/admin/system/health", nil, "")
	if !strings.Contains(page, `data-check="extensions" data-status="ok"`) {
		t.Fatalf("health check missing:\n%s", page)
	}
	e.app.Deps.DB.Exec(`UPDATE extensions SET status = 'error' WHERE slug = 'dropped'`)
	_, page = b.do("GET", "/admin/system/health", nil, "")
	if !strings.Contains(page, `data-check="extensions" data-status="fail"`) || !strings.Contains(page, "dropped") {
		t.Fatalf("health check must fail:\n%s", page)
	}
}

func TestExtensionsAPI(t *testing.T) {
	e := boot(t)
	e.install(sampleManifest("http://127.0.0.1:1/hook"))
	c := apitest.New(t, e.app, apitest.Options{Scopes: []string{"extensions.manage"}})
	code, body := c.Get("/v1/extensions")
	items, _ := body["data"].([]any)
	if code != 200 || len(items) != 1 || items[0].(map[string]any)["slug"] != "sample" {
		t.Fatalf("list: %d %v", code, body)
	}
	if code, body := c.Post("/v1/extensions/sample/activate", nil); code != 200 || body["data"].(map[string]any)["status"] != "active" {
		t.Fatalf("activate: %d %v", code, body)
	}
	if code, body := c.Post("/v1/extensions/sample/deactivate", nil); code != 200 || body["data"].(map[string]any)["status"] != "disabled" {
		t.Fatalf("deactivate: %d %v", code, body)
	}
	if code, _ := c.Post("/v1/extensions/missing/activate", nil); code != 404 {
		t.Fatalf("missing: %d", code)
	}
	if _, err := e.content.Create(e.ctx, content.Input{Type: "greeting", Title: "Salam", Status: "draft"}); err != nil {
		t.Fatal(err)
	}
	if code, body := c.Delete("/v1/extensions/sample?purge=true", nil); code != 200 || body["data"].(map[string]any)["purged"] != true {
		t.Fatalf("delete: %d %v", code, body)
	}
	if _, err := e.content.GetType(context.Background(), "greeting"); err == nil {
		t.Fatal("purge via API must delete declared types")
	}
	noScope := apitest.New(t, e.app, apitest.Options{Scopes: []string{"content.view"}})
	if code, _ := noScope.Get("/v1/extensions"); code != 403 {
		t.Fatalf("scope: %d", code)
	}
}
