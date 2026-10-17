package site_test

import (
	"context"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/config"
	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/settings/settingsmod"
	"github.com/zadstack/gorun/internal/site"
	"github.com/zadstack/gorun/internal/theme"
	"github.com/zadstack/gorun/internal/theme/fake"
)

// A theme without form-result.html: the contact form redirects back with a
// gp_form flash cookie.
func TestContactFormRedirectFallback(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"theme.json":                  `{"name":"Mini"}`,
		"templates/index.html":        `INDEX`,
		"templates/page-contact.html": `FLASH=[{{.Extra.form_flash}}]<input type="hidden" name="_csrf" value="{{csrf}}">`,
	}
	for p, body := range files {
		full := filepath.Join(root, "mini", filepath.FromSlash(p))
		os.MkdirAll(filepath.Dir(full), 0o755)
		os.WriteFile(full, []byte(body), 0o644)
	}
	cfg := config.Config{Env: "dev", DataDir: t.TempDir(), Addr: ":0", SecretKey: make([]byte, 32), BaseURL: "http://example.test", Theme: "mini"}
	fm := fake.NewModule()
	app, err := kernel.Boot(context.Background(), cfg, []kernel.Module{settingsmod.New(), auth.New(), fm, theme.New(theme.WithThemesDir(root)), site.New()})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(app.Handler)
	t.Cleanup(func() { srv.Close(); app.Close() })
	jar, _ := cookiejar.New(nil)
	e := &env{t: t, app: app, srv: srv, fake: fm,
		c: &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
	fm.Content.Add(content.Item{Type: "page", Slug: "contact", Title: "Contact"})

	_, page := e.get("/contact")
	mustContain(t, page, "FLASH=[]")
	token := csrfFrom(t, page)
	form := func(back string, valid bool) url.Values {
		v := url.Values{"_csrf": {token}, "name": {"Sara"}, "email": {"sara@example.com"}, "message": {"Hi"}, "_back": {back}}
		if !valid {
			v.Set("email", "bad")
		}
		return v
	}
	flash := func(resp *http.Response) string {
		for _, c := range resp.Cookies() {
			if c.Name == "gp_form" {
				return c.Value
			}
		}
		return ""
	}

	resp, _ := e.do("POST", "/forms/contact", form("/contact", true))
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/contact" || flash(resp) != "ok" {
		t.Fatalf("valid: %d %q %q", resp.StatusCode, resp.Header.Get("Location"), flash(resp))
	}
	_, page = e.get("/contact")
	mustContain(t, page, "FLASH=[ok]")
	_, page = e.get("/contact")
	mustContain(t, page, "FLASH=[]") // one-shot

	resp, _ = e.do("POST", "/forms/contact", form("/contact", false))
	if resp.StatusCode != http.StatusSeeOther || flash(resp) != "error" {
		t.Fatalf("invalid: %d %q", resp.StatusCode, flash(resp))
	}
	// (5 submissions per minute: three more fit the rate limit.)
	for _, back := range []string{"/\t/evil.example", "//evil.example", "https://evil.example/x"} {
		resp, _ := e.do("POST", "/forms/contact", form(back, true))
		if loc := resp.Header.Get("Location"); resp.StatusCode != http.StatusSeeOther || loc != "/" {
			t.Fatalf("back %q: %d Location %q", back, resp.StatusCode, loc)
		}
	}
}
