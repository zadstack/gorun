package httpx

import (
	"context"
	"io"
	"log/slog"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/i18n"
)

func newTestTemplates(t *testing.T, dev bool) *Templates {
	t.Helper()
	b, err := i18n.Load()
	if err != nil {
		t.Fatal(err)
	}
	tpl, err := NewTemplates(os.DirFS("testdata"), dev, b, Funcs{
		Nav: func(context.Context) []NavItem {
			return []NavItem{{ID: "dash", Href: "/admin"}, {ID: "posts", Href: "/admin/posts", Group: "content"}}
		},
		User:    func(context.Context) any { return "ahmed" },
		CSRF:    func(context.Context) string { return "tok" },
		Setting: func(_ context.Context, k string) string { return "S:" + k },
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	return tpl
}

type pageData struct {
	Name string `json:"name"`
	At   string `json:"-"`
}

func TestRenderWrapsLayoutAndFuncs(t *testing.T) {
	for _, dev := range []bool{false, true} {
		tpl := newTestTemplates(t, dev)
		r := httptest.NewRequest("GET", "/admin/posts/1", nil)
		r = r.WithContext(i18n.WithLocale(r.Context(), "ar"))
		rec := httptest.NewRecorder()
		SetFlash(rec, "success", "core.action.save")
		r.Header.Set("Cookie", rec.Header().Get("Set-Cookie"))
		rec = httptest.NewRecorder()
		tpl.Render(rec, r, "pages/x.html", pageData{Name: "zad", At: "2026-10-10T12:30:00Z"})
		body := rec.Body.String()
		for _, want := range []string{`dir="rtl"`, "<title>X page</title>", "<h1>حفظ</h1>", "hello zad tok ahmed",
			`{&#34;name&#34;:&#34;zad&#34;}`, "2026-10-10 12:30", "4truetrue", `data-lucide="trash-2"`, "S:site.title",
			"[dash][posts*]", `<p class="success">حفظ</p>`, "/static/admin/admin.css?v="} {
			if !strings.Contains(body, want) {
				t.Errorf("dev=%v: missing %q in\n%s", dev, want, body)
			}
		}
		if rec.Code != 200 || !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/html") {
			t.Fatalf("%d %s", rec.Code, rec.Header().Get("Content-Type"))
		}
		if !strings.Contains(rec.Header().Get("Set-Cookie"), "gp_flash=;") {
			t.Errorf("flash cookie not cleared: %q", rec.Header().Get("Set-Cookie"))
		}
	}
}

func TestRenderHTMXSkipsLayoutAndPartial(t *testing.T) {
	tpl := newTestTemplates(t, false)
	r := httptest.NewRequest("GET", "/admin/x", nil)
	r.Header.Set("HX-Request", "true")
	rec := httptest.NewRecorder()
	tpl.Render(rec, r, "pages/x.html", pageData{Name: "zad"})
	if body := rec.Body.String(); strings.Contains(body, "<html") || !strings.Contains(body, "<h1>حفظ</h1>") {
		t.Fatalf("htmx render should be content only: %s", body)
	}
	rec = httptest.NewRecorder()
	tpl.RenderPartial(rec, httptest.NewRequest("GET", "/", nil), "partials/hello", "p")
	if rec.Body.String() != "hello p tok ahmed" {
		t.Fatalf("%q", rec.Body.String())
	}
}

func TestRenderUnknownPageIs500(t *testing.T) {
	tpl := newTestTemplates(t, false)
	rec := httptest.NewRecorder()
	tpl.Render(rec, httptest.NewRequest("GET", "/", nil), "pages/missing.html", nil)
	if rec.Code != 500 {
		t.Fatal(rec.Code)
	}
}

func TestRenderFullLayoutForHistoryRestoreAndBoosted(t *testing.T) {
	tpl := newTestTemplates(t, false)
	for _, h := range []string{"HX-History-Restore-Request", "HX-Boosted"} {
		r := httptest.NewRequest("GET", "/admin/x", nil)
		r.Header.Set("HX-Request", "true")
		r.Header.Set(h, "true")
		rec := httptest.NewRecorder()
		tpl.Render(rec, r, "pages/x.html", pageData{Name: "zad"})
		if !strings.Contains(rec.Body.String(), "<html") {
			t.Errorf("%s: want full layout", h)
		}
		if rec.Header().Get("Vary") != "HX-Request" {
			t.Errorf("%s: Vary=%q", h, rec.Header().Get("Vary"))
		}
	}
}

func TestNavGroupsOrderAndLabels(t *testing.T) {
	b, err := i18n.Load()
	if err != nil {
		t.Fatal(err)
	}
	tpl, err := NewTemplates(os.DirFS("testdata"), false, b, Funcs{
		Nav: func(context.Context) []NavItem {
			return []NavItem{
				{ID: "keys", Group: "system"},
				{ID: "general", Group: "settings"},
				{ID: "connections", Group: "integrations"},
				{ID: "forms", Group: "inbox"},
				{ID: "custom", Group: "field_kit"},
				{ID: "agent", Group: "agents"},
				{ID: "dash"},
				{ID: "posts", Group: "content"},
			}
		},
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	for locale, want := range map[string][]string{
		"en": {"", "Content", "Inbox", "AI Agents", "Settings", "Integrations", "Operations", "Field Kit"},
		"ar": {"", "المحتوى", "صندوق الوارد", "وكلاء الذكاء الاصطناعي", "الإعدادات", "التكاملات", "تشغيل النظام", "Field Kit"},
	} {
		r := httptest.NewRequest("GET", "/admin", nil)
		r = r.WithContext(i18n.WithLocale(r.Context(), locale))
		groups := tpl.funcMap(r, &reqState{}, nil)["nav"].(func() []NavGroup)()
		var got []string
		for _, g := range groups {
			got = append(got, g.Label)
		}
		if strings.Join(got, "|") != strings.Join(want, "|") {
			t.Errorf("%s labels %q, want %q", locale, got, want)
		}
	}
}

func TestGroupLabelFallsBackOnlyForMissingKeys(t *testing.T) {
	translate := func(k string) string {
		if k == "core.nav.group.content" {
			return "Content"
		}
		return k // the bundle returns the key when it is missing
	}
	for key, want := range map[string]string{
		"":                           "",
		"core.nav.group.content":     "Content",
		"core.nav.group.agents":      "Agents",
		"core.nav.group.field_kit":   "Field Kit",
		"core.nav.group.multi-word":  "Multi Word",
		"core.nav.group.ext.payment": "Payment",
	} {
		if got := groupLabel(translate, key); got != want {
			t.Errorf("groupLabel(%q) = %q, want %q", key, got, want)
		}
	}
}

// A cached page must evaluate permissions against each request, including
// a reader rendered after a privileged request; an unwired callback denies.
func TestCachedTemplatePermissionsFollowRequestAndDefaultToDenied(t *testing.T) {
	type permissionContextKey struct{}
	tpl := newTestTemplates(t, false)
	render := func(allowed bool) string {
		t.Helper()
		req := httptest.NewRequest("GET", "/admin/permissions", nil)
		req = req.WithContext(context.WithValue(req.Context(), permissionContextKey{}, allowed))
		rec := httptest.NewRecorder()
		tpl.Render(rec, req, "pages/permissions.html", nil)
		if rec.Code != 200 {
			t.Fatalf("render status: %d: %s", rec.Code, rec.Body.String())
		}
		return rec.Body.String()
	}
	if body := render(true); !strings.Contains(body, "read-only") || strings.Contains(body, "edit-settings") {
		t.Fatalf("nil callback did not deny: %s", body)
	}
	tpl.funcs.Can = func(ctx context.Context, permission string) bool {
		allowed, _ := ctx.Value(permissionContextKey{}).(bool)
		return allowed && permission == "settings.manage"
	}
	for _, allowed := range []bool{true, false, true, false} {
		body := render(allowed)
		if strings.Contains(body, "edit-settings") != allowed || strings.Contains(body, "read-only") == allowed {
			t.Fatalf("request permission=%v leaked through cached template: %s", allowed, body)
		}
	}
}
