package builder

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/httpx"
	"github.com/zadstack/gorun/internal/kernel"
)

func TestSiteDesignEntryUsesAllExistingBuildPermissions(t *testing.T) {
	s, _, ctx := builderTest(t)
	const role = "design-settings-only"
	if _, err := s.d.DB.Exec(`INSERT INTO roles(slug,name_json,system) VALUES(?, ?,0)`, role, `{"en":"Design settings"}`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.d.DB.Exec(`INSERT INTO role_permissions(role,permission) VALUES(?,?)`, role, "settings.manage"); err != nil {
		t.Fatal(err)
	}
	if err := auth.ReloadRoles(ctx, s.d.DB); err != nil {
		t.Fatal(err)
	}
	limited := auth.WithUser(context.Background(), &auth.User{ID: "limited-designer", Role: role, Status: auth.StatusActive})
	for _, test := range []struct {
		ctx         context.Context
		destination string
		build       bool
	}{{ctx, "/admin/site-builder", true}, {limited, "/admin/appearance/identity", false}} {
		rec := httptest.NewRecorder()
		siteDesignHome(rec, httptest.NewRequest("GET", "/admin/site-design", nil).WithContext(test.ctx))
		if rec.Code != 303 || rec.Header().Get("Location") != test.destination {
			t.Fatal(rec.Code, rec.Header())
		}
		if view := designView(s.d, test.ctx, false); view.CanBuild != test.build {
			t.Fatalf("wrong Build capability: %+v", view)
		}
	}

	tpl, _ := kernel.Get[*httpx.Templates](s.d, kernel.ServiceAdminTemplates)
	h := identityHandler{d: s.d, tpl: tpl}
	rec := httptest.NewRecorder()
	h.view(rec, httptest.NewRequest("GET", "/admin/appearance/identity", nil).WithContext(limited))
	body := rec.Body.String()
	start := strings.Index(body, `<nav class="site-design-tabs"`)
	if start < 0 {
		t.Fatal("workspace tabs missing")
	}
	end := strings.Index(body[start:], "</nav>")
	if end < 0 {
		t.Fatal("workspace tabs unclosed")
	}
	tabs := body[start : start+end]
	if strings.Contains(tabs, `href="/admin/site-builder"`) || !strings.Contains(tabs, `href="/admin/appearance/identity"`) {
		t.Fatal("settings-only user received Build tab")
	}
}
func TestSiteDesignSavedSummaryDoesNotUsePendingForm(t *testing.T) {
	s, _, ctx := builderTest(t)
	if err := s.d.Settings.Set(ctx, "site.title", "Saved public name"); err != nil {
		t.Fatal(err)
	}
	h := starterHandler{d: s.d, svc: s}
	rec := httptest.NewRecorder()
	h.render(rec, httptest.NewRequest("GET", "/admin/site-builder", nil).WithContext(ctx), 422, starterView{Form: starterForm{Title: "Pending name"}, Error: "Review the fields"})
	body := rec.Body.String()
	for _, want := range []string{"Saved public name", "Pending name", `data-design-error`, `href="/admin/appearance/identity"`, `aria-current="page"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("missing %s", want)
		}
	}
}
