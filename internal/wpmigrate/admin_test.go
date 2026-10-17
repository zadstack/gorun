package wpmigrate

import (
	"bytes"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/zadstack/gorun/internal/admin"
	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/kernel"
)

type browser struct {
	t    *testing.T
	base string
	c    *http.Client
}

var (
	csrfField = regexp.MustCompile(`name="_csrf" value="([^"]+)"`)
	csrfMeta  = regexp.MustCompile(`<meta name="csrf-token" content="([^"]+)"`)
	migID     = regexp.MustCompile(`/admin/migrations/([0-9A-Z]{26})/scan`)
)

func (e *env) browser() *browser { return e.browserAs("owner@example.com") }

func (e *env) createEditor() {
	e.t.Helper()
	users, _ := kernel.Get[auth.Service](e.app.Deps, kernel.ServiceAuth)
	if _, err := users.Create(e.ctx(), "editor@example.com", "Editor", "correct horse battery", "editor"); err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) browserAs(email string) *browser {
	e.t.Helper()
	jar, _ := cookiejar.New(nil)
	b := &browser{t: e.t, base: e.srv.URL, c: &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
	_, page := b.do("GET", "/admin/login", nil, "")
	m := csrfField.FindStringSubmatch(page)
	if m == nil {
		e.t.Fatalf("no csrf on login page")
	}
	resp, _ := b.do("POST", "/admin/login", url.Values{"_csrf": {m[1]}, "email": {email}, "password": {"correct horse battery"}}, "")
	if resp.StatusCode != http.StatusSeeOther {
		e.t.Fatalf("login: %d", resp.StatusCode)
	}
	return b
}

func (b *browser) do(method, path string, form url.Values, ct string, headers ...string) (*http.Response, string) {
	b.t.Helper()
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
		ct = "application/x-www-form-urlencoded"
	}
	return b.raw(method, path, body, ct, headers...)
}

func (b *browser) raw(method, path string, body io.Reader, ct string, headers ...string) (*http.Response, string) {
	b.t.Helper()
	req, _ := http.NewRequest(method, b.base+path, body)
	if ct != "" {
		req.Header.Set("Content-Type", ct)
	}
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	resp, err := b.c.Do(req)
	if err != nil {
		b.t.Fatal(err)
	}
	out, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	return resp, string(out)
}

func (b *browser) csrf() string {
	b.t.Helper()
	_, page := b.do("GET", "/admin/migrations", nil, "")
	m := csrfMeta.FindStringSubmatch(page)
	if m == nil {
		b.t.Fatalf("no csrf meta:\n%.500s", page)
	}
	return m[1]
}

func (b *browser) post(path string, form url.Values, headers ...string) (*http.Response, string) {
	b.t.Helper()
	if form == nil {
		form = url.Values{}
	}
	form.Set("_csrf", b.csrf())
	return b.do("POST", path, form, "", headers...)
}

func must(t *testing.T, body string, want ...string) {
	t.Helper()
	for _, w := range want {
		if !strings.Contains(body, w) {
			t.Fatalf("body lacks %q:\n%.3000s", w, body)
		}
	}
}

// TestWizardStepsHTTP walks the seven wizard steps over HTTP with the real
// job queue, plus the WXR upload path.
func TestWizardStepsHTTP(t *testing.T) {
	e := newEnv(t)
	e.wp = newFixtureServer(t)
	e.wp.limitMedia(3, "image/png")
	b := e.browser()

	resp, page := b.do("GET", "/admin/migrations", nil, "")
	if resp.StatusCode != 200 {
		t.Fatalf("list: %d", resp.StatusCode)
	}
	must(t, page, "ووردبريس", `href="/admin/migrations/new"`, `href="/admin/migrations"`)

	// 1. Connect: a bad URL re-renders without echoing the password.
	resp, page = b.post("/admin/migrations/connect", url.Values{"url": {"ftp://nope"}, "username": {fixtureUser}, "app_password": {"secret-should-not-echo"}})
	if resp.StatusCode != http.StatusUnprocessableEntity || strings.Contains(page, "secret-should-not-echo") {
		t.Fatalf("bad connect: %d echo=%v", resp.StatusCode, strings.Contains(page, "secret-should-not-echo"))
	}
	resp, _ = b.post("/admin/migrations/connect", url.Values{"url": {e.wp.URL()}, "username": {fixtureUser},
		"app_password": {fixturePass}, "slug_policy": {"suffix"}})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("connect: %d", resp.StatusCode)
	}
	loc := resp.Header.Get("Location")
	m := migID.FindStringSubmatch(loc)
	if m == nil {
		t.Fatalf("redirect = %q", loc)
	}
	id := m[1]

	// 2. Scan: three colour-coded buckets.
	_, page = b.do("GET", loc, nil, "")
	must(t, page, `action="/admin/migrations/`+id+`/scan"`)
	resp, page = b.post("/admin/migrations/"+id+"/scan", nil)
	if resp.StatusCode != 200 {
		t.Fatalf("scan: %d", resp.StatusCode)
	}
	must(t, page, "border-green bg-green-soft", "border-amber bg-amber-soft", "bg-panel-2", "Yoast SEO", "ep_plans", "EXAMPLE PRESS", "7.1.3")

	// 3. Map types.
	_, page = b.do("GET", "/admin/migrations/"+id+"/map", nil, "")
	must(t, page, `name="type.post"`, `name="type.product"`, `name="tax.category"`)
	resp, _ = b.post("/admin/migrations/"+id+"/map", url.Values{"type.post": {"post"}, "type.page": {"page"}, "type.product": {"commerce.product"},
		"tax.category": {"category"}, "tax.post_tag": {"tag"}})
	if resp.StatusCode != http.StatusSeeOther || !strings.HasSuffix(resp.Header.Get("Location"), "/fields") {
		t.Fatalf("map: %d %s", resp.StatusCode, resp.Header.Get("Location"))
	}
	// 4. Map fields (the fixture exposes no custom meta).
	_, page = b.do("GET", "/admin/migrations/"+id+"/fields", nil, "")
	must(t, page, `action="/admin/migrations/`+id+`/fields"`)
	resp, _ = b.post("/admin/migrations/"+id+"/fields", nil)
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("fields: %d", resp.StatusCode)
	}
	// 5. Preview.
	resp, page = b.post("/admin/migrations/"+id+"/preview", nil)
	if resp.StatusCode != 200 {
		t.Fatalf("preview: %d", resp.StatusCode)
	}
	must(t, page, "garden-kit-2", `href="/admin/migrations/`+id+`/import"`)
	if n := e.count(`SELECT count(*) FROM content`); n != 0 {
		t.Fatalf("preview wrote %d content rows", n)
	}
	// 6. Import with live progress (real job workers).
	_, page = b.do("GET", "/admin/migrations/"+id+"/import", nil, "")
	must(t, page, `hx-get="/admin/x/migrations/`+id+`/progress"`)
	resp, page = b.post("/admin/migrations/"+id+"/start", nil, "HX-Request", "true")
	if resp.StatusCode != 200 {
		t.Fatalf("start: %d", resp.StatusCode)
	}
	must(t, page, `id="mig-progress"`)
	deadline := time.Now().Add(60 * time.Second)
	for {
		_, page = b.do("GET", "/admin/x/migrations/"+id+"/progress", nil, "", "HX-Request", "true")
		if strings.Contains(page, `data-status="validated"`) {
			break
		}
		if strings.Contains(page, `data-status="failed"`) || time.Now().After(deadline) {
			t.Fatalf("import did not finish:\n%.3000s", page)
		}
		if strings.Contains(page, `data-status="running"`) && !strings.Contains(page, `hx-trigger="every 2s"`) {
			t.Fatal("running progress must poll every 2s")
		}
		time.Sleep(200 * time.Millisecond)
	}
	if strings.Contains(page, `hx-trigger="every 2s"`) {
		t.Fatal("finished progress must stop polling")
	}
	must(t, page, `href="/admin/migrations/`+id+`/validate"`)
	// 7. Validate + Markdown download.
	_, page = b.do("GET", "/admin/migrations/"+id+"/validate", nil, "")
	must(t, page, `href="/admin/migrations/`+id+`/report.md"`, "ep_plans")
	resp, md := b.do("GET", "/admin/migrations/"+id+"/report.md", nil, "")
	if resp.StatusCode != 200 || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/markdown") || !strings.Contains(md, "# Migration report") {
		t.Fatalf("report.md: %d %s", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	// Pause/resume controls answer HTMX with the fragment.
	resp, _ = b.post("/admin/migrations/"+id+"/pause", nil, "HX-Request", "true")
	if resp.StatusCode != 200 {
		t.Fatalf("pause on finished: %d", resp.StatusCode)
	}

	if resp, _ = b.post("/admin/migrations/"+id+"/xpausex", nil); resp.StatusCode != http.StatusNotFound && resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("unknown action: %d", resp.StatusCode)
	}

	// Health check reports the last migration.
	checks := []admin.HealthCheck{}
	v, _ := e.app.Deps.Hooks.Apply(e.ctx(), admin.HealthCheckFilter, &checks)
	found := false
	for _, c := range *v.(*[]admin.HealthCheck) {
		found = found || (c.Name == "wpmigrate" && c.Status == admin.CheckOK)
	}
	if !found {
		t.Fatalf("health checks = %+v", checks)
	}

	// WXR upload path (multipart, CSRF header like HTMX).
	raw, _ := os.ReadFile("testdata/wxr/sample.xml")
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, _ := mw.CreateFormFile("wxr", "example-press.WordPress.xml")
	_, _ = fw.Write(raw)
	_ = mw.WriteField("currency", "aed")
	_ = mw.Close()
	resp, _ = b.raw("POST", "/admin/migrations/upload", &buf, mw.FormDataContentType(), "X-CSRF-Token", b.csrf(), "HX-Request", "true")
	if resp.StatusCode != http.StatusNoContent || !strings.Contains(resp.Header.Get("HX-Redirect"), "/scan") {
		t.Fatalf("upload: %d %q", resp.StatusCode, resp.Header.Get("HX-Redirect"))
	}
	var bad bytes.Buffer
	mw = multipart.NewWriter(&bad)
	fw, _ = mw.CreateFormFile("wxr", "x.xml")
	_, _ = fw.Write([]byte("<rss><channel></channel></rss>"))
	_ = mw.Close()
	resp, _ = b.raw("POST", "/admin/migrations/upload", &bad, mw.FormDataContentType(), "X-CSRF-Token", b.csrf())
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("invalid upload: %d", resp.StatusCode)
	}
	resp, _ = b.raw("POST", "/admin/migrations/upload", strings.NewReader("x"), "multipart/form-data; boundary=x")
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("upload without CSRF header: %d", resp.StatusCode)
	}

	// Permission: an editor cannot reach the wizard.
	e.createEditor()
	eb := e.browserAs("editor@example.com")
	resp, _ = eb.do("GET", "/admin/migrations", nil, "")
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("editor access: %d", resp.StatusCode)
	}
}
