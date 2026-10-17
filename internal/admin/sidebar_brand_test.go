package admin_test

import (
	"context"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/media"
	"regexp"
	"strings"
	"testing"
)

func sidebarBrandHTML(t *testing.T, s *site) string {
	t.Helper()
	r, b := s.do("GET", "/admin", nil)
	if r.StatusCode != 200 {
		t.Fatal(r.StatusCode)
	}
	m := regexp.MustCompile(`(?s)<a[^>]*data-sidebar-brand[^>]*>.*?</a>`).FindString(b)
	if m == "" {
		t.Fatal("sidebar site identity missing")
	}
	return m
}
func TestSidebarBrandUsesCurrentSiteAndSafeMedia(t *testing.T) {
	s := boot(t, media.New())
	s.login("brand@example.invalid", "admin", "ar")
	ctx := context.Background()
	id := uploadPNG(t, s)
	ms, _ := kernel.Get[media.Service](s.app.Deps, kernel.ServiceMedia)
	m, e := ms.Get(ctx, id)
	if e != nil {
		t.Fatal(e)
	}
	set := func(k, v string) {
		t.Helper()
		if e := s.app.Deps.Settings.Set(ctx, k, v); e != nil {
			t.Fatal(e)
		}
	}
	set("site.title", `My <script>alert(1)</script> موقع`)
	set("site.logo", id)
	b := sidebarBrandHTML(t, s)
	mustContain(t, b, `My &lt;script&gt;alert(1)&lt;/script&gt; موقع`, `src="`+m.URL+`"`)
	mustNotContain(t, b, "GoRun", "<script>")
	// A media record's external URL is not an admin sidebar asset destination.
	if _, e = s.app.Deps.DB.Exec(`UPDATE media SET url='https://external.invalid/logo.png' WHERE id=?`, id); e != nil {
		t.Fatal(e)
	}
	b = sidebarBrandHTML(t, s)
	mustContain(t, b, `src="`+m.URL+`"`)
	mustNotContain(t, b, "external.invalid")
	set("site.title", "")
	b = sidebarBrandHTML(t, s)
	mustNotContain(t, b, "GoRun")
	mustContain(t, b, "<img")
	set("site.title", `My <script>alert(1)</script> موقع`)
	for _, logo := range []string{"", "missing", "https://external.invalid/logo.png"} {
		set("site.logo", logo)
		b = sidebarBrandHTML(t, s)
		mustContain(t, b, "My &lt;script&gt;")
		mustNotContain(t, b, "<img", "GoRun")
	}
	set("site.logo", id)
	if _, e = s.app.Deps.DB.Exec(`UPDATE media SET mime='application/pdf' WHERE id=?`, id); e != nil {
		t.Fatal(e)
	}
	mustNotContain(t, sidebarBrandHTML(t, s), "<img")
	if _, e = s.app.Deps.DB.Exec(`UPDATE media SET mime='image/png',storage_key='../escape.png' WHERE id=?`, id); e != nil {
		t.Fatal(e)
	}
	mustNotContain(t, sidebarBrandHTML(t, s), "<img")
	other := boot(t, media.New())
	other.login("other-brand@example.invalid", "admin", "en")
	if e = other.app.Deps.Settings.Set(ctx, "site.title", "Other website"); e != nil {
		t.Fatal(e)
	}
	if e = other.app.Deps.Settings.Set(ctx, "site.logo", id); e != nil {
		t.Fatal(e)
	}
	b = sidebarBrandHTML(t, other)
	mustContain(t, b, "Other website")
	mustNotContain(t, b, "My &lt;script&gt;", "<img")
}
func TestSidebarBrandFallbackAndNoMediaModule(t *testing.T) {
	s := boot(t)
	s.login("fallback@example.invalid", "admin", "en")
	b := sidebarBrandHTML(t, s)
	mustContain(t, b, "GoRun")
	if e := s.app.Deps.Settings.Set(context.Background(), "site.title", "  Plain site  "); e != nil {
		t.Fatal(e)
	}
	if e := s.app.Deps.Settings.Set(context.Background(), "site.logo", "unresolved"); e != nil {
		t.Fatal(e)
	}
	b = sidebarBrandHTML(t, s)
	if !strings.Contains(b, "Plain site") || strings.Contains(b, "GoRun") || strings.Contains(b, "<img") {
		t.Fatal("title fallback invalid")
	}
}
